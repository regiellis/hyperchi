package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/regiellis/hyperchi/hyperchi"
)

// post sends the signup form from its own client address, so tests that are
// not about the rate limit never share a bucket.
func post(t *testing.T, app http.Handler, form url.Values, peer string, headers ...string) response {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, signupPath, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = peer + ":40000"
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	return do(t, app, req)
}

func subscribers(t *testing.T, app *hyperchi.HyperChi, email string) int {
	t.Helper()
	n, err := app.DB.Count("subscribers", "email = ?", email)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

var htmx = []string{"HX-Request", "true"}

func TestSignupStoresEmail(t *testing.T) {
	app := newTestApp(t, 5)
	res := post(t, app, url.Values{"email": {"Reader@Example.com"}, "list": {"launch"}}, "192.0.2.1", htmx...)
	if res.code != http.StatusOK {
		t.Fatalf("status %d: %s", res.code, res.body)
	}
	mustContain(t, "confirmation", res.body, "You are on the list", "reader@example.com", "no confirmation email was sent")
	if strings.Contains(res.body, "<html") {
		t.Error("htmx confirmation rendered the layout")
	}
	if n := subscribers(t, app, "reader@example.com"); n != 1 {
		t.Fatalf("stored %d rows, want 1", n)
	}
	var list string
	if err := app.DB.QueryRow("SELECT list FROM subscribers WHERE email = ?", "reader@example.com").Scan(&list); err != nil || list != "launch" {
		t.Errorf("list = %q, %v", list, err)
	}

	plain := post(t, app, url.Values{"email": {"plain@example.com"}, "list": {"studio"}}, "192.0.2.2")
	if plain.code != http.StatusSeeOther || plain.header.Get("Location") != thanksPath {
		t.Errorf("plain post: %d to %q, want 303 to %s", plain.code, plain.header.Get("Location"), thanksPath)
	}
}

func TestSignupDuplicateIs422(t *testing.T) {
	app := newTestApp(t, 5)
	first := post(t, app, url.Values{"email": {"twice@example.com"}, "list": {"studio"}}, "192.0.2.10", htmx...)
	if first.code != http.StatusOK {
		t.Fatalf("first signup: %d", first.code)
	}
	again := post(t, app, url.Values{"email": {"TWICE@example.com"}, "list": {"launch"}}, "192.0.2.11", htmx...)
	if again.code != http.StatusUnprocessableEntity {
		t.Fatalf("duplicate: status %d, want 422", again.code)
	}
	mustContain(t, "duplicate", again.body, `<form class="signup"`, duplicateMessage,
		`aria-invalid="true" aria-describedby="signup-email-error" autofocus`, `value="TWICE@example.com"`,
		`value="launch" checked`)
	if n := subscribers(t, app, "twice@example.com"); n != 1 {
		t.Errorf("stored %d rows, want 1", n)
	}

	// Without htmx the same failure re-renders the whole newsletter page.
	page := post(t, app, url.Values{"email": {"twice@example.com"}, "list": {"studio"}}, "192.0.2.12")
	if page.code != http.StatusUnprocessableEntity {
		t.Fatalf("plain duplicate: %d", page.code)
	}
	mustContain(t, "plain duplicate", page.body, "<html", duplicateMessage, `<meta name="robots" content="noindex">`)
}

func TestSignupMalformedIs422(t *testing.T) {
	app := newTestApp(t, 5)
	cases := []struct {
		name   string
		form   url.Values
		fields []string
	}{
		{"bad email", url.Values{"email": {"not-an-address"}, "list": {"studio"}}, []string{"signup-email-error"}},
		{"missing email", url.Values{"list": {"studio"}}, []string{"signup-email-error"}},
		{"unknown list", url.Values{"email": {"a@example.com"}, "list": {"everything"}}, []string{"signup-list-error"}},
		{"both", url.Values{"email": {"x"}}, []string{"signup-email-error", "signup-list-error"}},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := post(t, app, tc.form, fmt.Sprintf("192.0.2.%d", 20+i), htmx...)
			if res.code != http.StatusUnprocessableEntity {
				t.Fatalf("status %d, want 422", res.code)
			}
			for _, id := range tc.fields {
				mustContain(t, tc.name, res.body, `id="`+id+`"`)
			}
			if strings.Contains(res.body, "must be one of") {
				t.Error("raw validator text reached the visitor")
			}
		})
	}
	if n, err := app.DB.Count("subscribers", "1 = 1"); err != nil || n != 0 {
		t.Errorf("malformed input stored %d rows (%v)", n, err)
	}
}

func TestSignupProtection(t *testing.T) {
	app := newTestApp(t, 2)
	honeypot := post(t, app, url.Values{"email": {"bot@example.com"}, "list": {"studio"}, "website": {"http://spam.example"}}, "198.51.100.1", htmx...)
	if honeypot.code != http.StatusOK || subscribers(t, app, "bot@example.com") != 0 {
		t.Errorf("honeypot: status %d, stored %d", honeypot.code, subscribers(t, app, "bot@example.com"))
	}

	cross := post(t, app, url.Values{"email": {"c@example.com"}, "list": {"studio"}}, "198.51.100.2", "Sec-Fetch-Site", "cross-site")
	if cross.code != http.StatusForbidden || !strings.Contains(cross.body, "<html") {
		t.Errorf("cross-site post: %d", cross.code)
	}

	req := httptest.NewRequest(http.MethodPost, signupPath, strings.NewReader(`{"email":"j@example.com"}`))
	req.Header.Set("Content-Type", "application/json")
	if res := do(t, app, req); res.code != http.StatusUnsupportedMediaType {
		t.Errorf("JSON body: %d, want 415", res.code)
	}

	// Burst 2: the third post from one client is limited, and htmx is told
	// to show the message in the form's status line instead of replacing it.
	peer := "198.51.100.3"
	for i, email := range []string{"r1@example.com", "r2@example.com"} {
		if res := post(t, app, url.Values{"email": {email}, "list": {"studio"}}, peer, htmx...); res.code != http.StatusOK {
			t.Fatalf("post %d: %d", i+1, res.code)
		}
	}
	limited := post(t, app, url.Values{"email": {"r3@example.com"}, "list": {"studio"}}, peer, htmx...)
	if limited.code != http.StatusTooManyRequests || limited.header.Get("Retry-After") == "" {
		t.Fatalf("third post: %d, Retry-After %q", limited.code, limited.header.Get("Retry-After"))
	}
	if limited.header.Get("HX-Retarget") != "#newsletter-status" || !strings.Contains(limited.body, "Too many tries") {
		t.Errorf("429 for htmx: retarget %q, body %q", limited.header.Get("HX-Retarget"), limited.body)
	}
	if subscribers(t, app, "r3@example.com") != 0 {
		t.Error("a limited post was stored")
	}
}
