package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/regiellis/hyperchi/hyperchi"
	"github.com/regiellis/hyperchi/hyperchi/config"
)

// waitlistApp is the showcase app with the wizard's session middleware, as
// main installs it for an HTTPS origin.
func waitlistApp(t *testing.T) *hyperchi.HyperChi {
	t.Helper()
	return waitlistAppFor(t, true)
}

func waitlistAppFor(t *testing.T, https bool) *hyperchi.HyperChi {
	t.Helper()
	cfg := config.Production()
	cfg.Database.Name = filepath.Join(t.TempDir(), "waitlist.db")
	app := hyperchi.NewWithConfig(cfg)
	t.Cleanup(func() {
		if err := app.CloseDB(); err != nil {
			t.Error(err)
		}
		if err := app.CleanupKVStores(); err != nil {
			t.Error(err)
		}
	})
	sessions, err := waitlistSessions(app, https)
	if err != nil {
		t.Fatal(err)
	}
	app.Use(sessions)
	if err := app.LoadTemplates("templates/*.tmpl"); err != nil {
		t.Fatal(err)
	}
	if err := setupRoutes(app, ""); err != nil {
		t.Fatal(err)
	}
	return app
}

// postStep submits one wizard step as htmx does, carrying the session cookie.
func postStep(t *testing.T, app http.Handler, cookies []*http.Cookie, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/waitlist", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("HX-Request", "true")
	for _, c := range cookies {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	return w
}

// The wizard rejects a bad step with 422 and its message, keeps answers in the
// session between steps, shows them again on a later step, and finishes with
// HX-Redirect to the completion page.
func TestWaitlistWizard(t *testing.T) {
	app := waitlistApp(t)

	w := postStep(t, app, nil, url.Values{"step": {"1"}, "email": {"nope"}})
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad email: status %d, want 422", w.Code)
	}
	if body := w.Body.String(); !strings.Contains(body, "Enter a valid work email") || !strings.Contains(body, `value="nope"`) {
		t.Fatalf("bad email: step not re-rendered with message and value: %s", body)
	}
	if !strings.Contains(w.Header().Get("HX-Trigger"), "showValidationError") {
		t.Fatalf("bad email: HX-Trigger %q", w.Header().Get("HX-Trigger"))
	}

	w = postStep(t, app, nil, url.Values{"step": {"1"}, "email": {"ada@example.com"}})
	if w.Code != http.StatusOK || !strings.Contains(w.Header().Get("HX-Trigger"), "nextStep") {
		t.Fatalf("good email: status %d, HX-Trigger %q", w.Code, w.Header().Get("HX-Trigger"))
	}
	cookies := w.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("good email: no session cookie")
	}

	w = postStep(t, app, cookies, url.Values{"step": {"2"}, "role": {"wizard"}})
	if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "Pick the role") {
		t.Fatalf("bad role: status %d, body %s", w.Code, w.Body.String())
	}

	w = postStep(t, app, cookies, url.Values{"step": {"2"}, "role": {"lead"}, "stack": {"htmx", "sqlite"}})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "ada@example.com") {
		t.Fatalf("good role: status %d, body %s", w.Code, w.Body.String())
	}

	before := waitlistCompletions.Load()
	w = postStep(t, app, cookies, url.Values{"step": {"3"}})
	if w.Header().Get("HX-Redirect") != "/waitlist/done" {
		t.Fatalf("confirm: status %d, HX-Redirect %q", w.Code, w.Header().Get("HX-Redirect"))
	}
	if waitlistCompletions.Load() != before+1 {
		t.Fatal("confirm: Complete did not run")
	}
}

// Only the wizard's route opens a session; the rest of the site sets no
// session cookie.
func TestOnlyWaitlistHasSessions(t *testing.T) {
	app := waitlistApp(t)
	w := httptest.NewRecorder()
	app.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/docs/routing", nil))
	if got := w.Header().Values("Set-Cookie"); len(got) != 0 {
		t.Fatalf("docs page set cookies: %q", got)
	}
}

// The session cookie is Secure only when the site has an HTTPS origin: over
// plain http (loopback, the Tailscale preview) a Secure cookie never comes
// back, and "back" would show an empty step.
func TestWaitlistCookieSecureFollowsTheOrigin(t *testing.T) {
	for _, https := range []bool{true, false} {
		w := postStep(t, waitlistAppFor(t, https), nil, url.Values{"step": {"1"}, "email": {"ada@example.com"}})
		cookies := w.Result().Cookies()
		if len(cookies) != 1 {
			t.Fatalf("https=%v: got %d cookies, want the session cookie", https, len(cookies))
		}
		if cookies[0].Secure != https {
			t.Errorf("https=%v: cookie Secure = %v", https, cookies[0].Secure)
		}
	}
}
