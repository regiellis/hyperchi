package main

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

func TestPagesNeedASignedInUser(t *testing.T) {
	a := newTestApp(t, nil)
	res, _ := a.get("/lists", false)
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/setup" {
		t.Fatalf("with no accounts: %d %q", res.StatusCode, res.Header.Get("Location"))
	}
	if _, err := createUser(a.s.db, "first@example.com", "a long enough password", "First"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/lists", "/letters", "/sendouts", "/audit", "/team", "/sendouts/1/export.csv"} {
		res, _ := a.get(path, false)
		if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/login?next="+url.QueryEscape(path) {
			t.Errorf("%s: %d %q", path, res.StatusCode, res.Header.Get("Location"))
		}
	}
	res, _ = a.get("/lists", true)
	if res.Header.Get("HX-Redirect") != "/login" {
		t.Fatalf("htmx: %d %v", res.StatusCode, res.Header)
	}
	res, _ = a.get("/setup", false)
	if res.Header.Get("Location") != "/login" {
		t.Fatalf("setup after the first account: %d %q", res.StatusCode, res.Header.Get("Location"))
	}
	res, _ = a.post("/setup", url.Values{"email": {"x@example.com"}, "name": {"X"}, "password": {"another long password"}})
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("setup POST after the first account: %d", res.StatusCode)
	}
}

func TestLogin(t *testing.T) {
	a := newTestApp(t, nil)
	if _, err := createUser(a.s.db, "team@example.com", "a long enough password", "Team"); err != nil {
		t.Fatal(err)
	}
	res, body := a.post("/login", url.Values{"email": {"team@example.com"}, "password": {"wrong password"}})
	if res.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, "do not match") || !strings.Contains(body, `value="team@example.com"`) {
		t.Fatalf("wrong password: %d %s", res.StatusCode, body)
	}
	res, _ = a.post("/login", url.Values{"email": {"TEAM@example.com"}, "password": {"a long enough password"}, "next": {"/letters"}})
	if res.StatusCode != http.StatusOK || res.Header.Get("HX-Redirect") != "/letters" {
		t.Fatalf("login: %d %v", res.StatusCode, res.Header)
	}
	res, body = a.get("/letters", false)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "Sign out") {
		t.Fatalf("after login: %d", res.StatusCode)
	}
	res, _ = a.post("/logout", nil)
	if res.Header.Get("HX-Redirect") != "/login" {
		t.Fatalf("logout: %d %v", res.StatusCode, res.Header)
	}
	res, _ = a.get("/letters", false)
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("after logout: %d", res.StatusCode)
	}
	if n := a.count("audit_log", "action IN ('signed in', 'signed out')"); n != 2 {
		t.Fatalf("%d audit rows", n)
	}
}

func TestSafeNext(t *testing.T) {
	for in, want := range map[string]string{
		"/letters": "/letters", "": "/lists", "https://evil.example": "/lists", "//evil.example": "/lists", `/\evil.example`: "/lists",
	} {
		if got := safeNext(in); got != want {
			t.Errorf("safeNext(%q) = %q", in, got)
		}
	}
}

func TestPostsNeedTheCSRFToken(t *testing.T) {
	a := newTestApp(t, nil)
	a.signIn()
	send := func(token string) *http.Response {
		req, err := http.NewRequest(http.MethodPost, a.srv.URL+"/lists", strings.NewReader("name=Quarterly"))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("HX-Request", "true")
		if token != "" {
			req.Header.Set("X-CSRF-Token", token)
		}
		res, _ := a.do(req)
		return res
	}
	token := a.csrf()
	if res := send(""); res.StatusCode != http.StatusForbidden {
		t.Fatalf("no token: %d", res.StatusCode)
	}
	if res := send("not-the-token"); res.StatusCode != http.StatusForbidden {
		t.Fatalf("wrong token: %d", res.StatusCode)
	}
	if n := a.count("lists", ""); n != 0 {
		t.Fatalf("a refused POST created %d lists", n)
	}
	if res := send(token); res.StatusCode != http.StatusOK || !strings.HasPrefix(res.Header.Get("HX-Redirect"), "/lists/") {
		t.Fatalf("right token: %d %v", res.StatusCode, res.Header)
	}
	// A plain form post carries the token as a field instead: the masked
	// token the page rendered into its form, not the raw cookie value.
	_, page := a.get("/lists", false)
	m := regexp.MustCompile(`name="csrf_token" value="([^"]+)"`).FindStringSubmatch(page)
	if m == nil {
		t.Fatalf("the lists page has no csrf_token field: %s", page)
	}
	req, err := http.NewRequest(http.MethodPost, a.srv.URL+"/lists", strings.NewReader(url.Values{
		"name": {"Annual"}, "csrf_token": {m[1]},
	}.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if res, _ := a.do(req); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("form field token: %d", res.StatusCode)
	}
}
