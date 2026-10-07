package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/regiellis/hyperchi/hyperchi/config"
)

func TestLobbyAndNames(t *testing.T) {
	srv, _ := testServer(t, Options{})
	p := newPlayer(t, srv, "")

	tests := []struct {
		name string
		form string
		code int
		want string
	}{
		{"empty", "", http.StatusUnprocessableEntity, "Name is required."},
		{"too short", "A", http.StatusUnprocessableEntity, "Name must be 2 to 20 characters."},
		{"markup", "<b>bold</b>", http.StatusUnprocessableEntity, "can use letters, numbers"},
		{"good", "  Ana   Lee ", http.StatusOK, "You are <strong>Ana Lee</strong>."},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body, resp := p.post("/name", url.Values{"name": {tc.form}}, true)
			if resp.StatusCode != tc.code || !strings.Contains(body, tc.want) {
				t.Fatalf("%d %s", resp.StatusCode, body)
			}
		})
	}
	// The name is signed into a cookie and survives the next page load.
	body, _ := p.get("/")
	if !strings.Contains(body, "Playing as <strong>Ana Lee</strong>") {
		t.Fatal("name did not stick")
	}
}

func TestPlayerCookiesAreSigned(t *testing.T) {
	srv, _ := testServer(t, Options{})
	p := newPlayer(t, srv, "Ana")
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	// Tamper with the signed player cookie: the server ignores it and mints
	// a new ID, which also drops the name signed with it.
	var forged []*http.Cookie
	for _, c := range p.client.Jar.Cookies(u) {
		if c.Name == playerCookie {
			// Change one character inside the value (not the last, whose
			// spare bits base64 may ignore).
			i := len(c.Value) / 2
			flip := "A"
			if c.Value[i] == 'A' {
				flip = "B"
			}
			c.Value = c.Value[:i] + flip + c.Value[i+1:]
		}
		forged = append(forged, c)
	}
	p.client.Jar.SetCookies(u, forged)
	body, resp := p.get("/")
	if !strings.Contains(body, "Pick a name") {
		t.Fatal("a forged player cookie kept its name")
	}
	if !strings.Contains(resp.Header.Get("Set-Cookie"), playerCookie+"=") {
		t.Fatal("no fresh player cookie after a forged one")
	}
}

func TestCreateAndJoinTables(t *testing.T) {
	srv, _ := testServer(t, Options{})
	anon := newPlayer(t, srv, "")
	host := newPlayer(t, srv, "Host")
	guest := newPlayer(t, srv, "Guest")

	body, resp := anon.post("/rooms", url.Values{"target": {"5000"}}, true)
	if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, "Pick a name") {
		t.Fatalf("nameless create: %d %s", resp.StatusCode, body)
	}
	body, resp = host.post("/rooms", url.Values{"target": {"777"}}, true)
	if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, "must be one of: 2000, 5000, 10000") {
		t.Fatalf("bad target: %d %s", resp.StatusCode, body)
	}

	// An htmx form gets HX-Redirect; a plain form gets a 303.
	_, resp = host.post("/rooms", url.Values{"target": {"5000"}}, true)
	loc := resp.Header.Get("HX-Redirect")
	if !strings.HasPrefix(loc, "/r/") {
		t.Fatalf("htmx create: %d %v", resp.StatusCode, resp.Header)
	}
	code := strings.TrimPrefix(loc, "/r/")

	for _, tc := range []struct{ code, want string }{
		{"", "That code is required."},
		{"abc", "That code is five letters and numbers"},
		{"ZZZZZ", "That code does not match an open table"},
	} {
		body, resp := guest.post("/join", url.Values{"code": {tc.code}}, true)
		if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, tc.want) {
			t.Fatalf("join %q: %d %s", tc.code, resp.StatusCode, body)
		}
	}
	guest.join(strings.ToLower(code)) // codes are case-insensitive

	body, resp = guest.get("/r/" + code)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `hx-ws:connect="/ws/r/`+code+`"`) || !strings.Contains(body, "Guest") {
		t.Fatalf("room page: %d", resp.StatusCode)
	}
	if !strings.Contains(body, `value="start"`) {
		t.Fatal("two seated players but no start button")
	}

	body, resp = guest.get("/r/QQQQQ")
	if resp.StatusCode != http.StatusNotFound || !strings.Contains(body, "No open table has the code QQQQQ") {
		t.Fatalf("missing room: %d", resp.StatusCode)
	}
}

func TestAssets(t *testing.T) {
	srv, _ := testServer(t, Options{})
	p := newPlayer(t, srv, "")
	for _, tc := range []struct {
		path, ctype string
		code        int
	}{
		{"/static/js/htmx.min.js", "javascript", http.StatusOK},
		{"/static/js/arena.js", "javascript", http.StatusOK},
		{"/static/css/arena.css", "text/css", http.StatusOK},
		{"/hyperchi/js/hx-ws.js", "javascript", http.StatusOK},
		{"/static/js", "", http.StatusNotFound},
		{"/static/../main.go", "", http.StatusNotFound},
		{"/static/missing.css", "", http.StatusNotFound},
		{"/healthz", "text/plain", http.StatusOK},
	} {
		_, resp := p.get(tc.path)
		if resp.StatusCode != tc.code || !strings.Contains(resp.Header.Get("Content-Type"), tc.ctype) {
			t.Errorf("%s: %d %s", tc.path, resp.StatusCode, resp.Header.Get("Content-Type"))
		}
	}

	// Development revalidates every time, and the ETag makes that cheap.
	_, resp := p.get("/static/css/arena.css")
	etag := resp.Header.Get("ETag")
	if resp.Header.Get("Cache-Control") != "no-cache" || etag == "" || resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("arena.css headers: %v", resp.Header)
	}
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/static/css/arena.css", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("If-None-Match", etag)
	if _, resp := p.do(req); resp.StatusCode != http.StatusNotModified {
		t.Fatalf("conditional GET: %d, want 304", resp.StatusCode)
	}
}

// TestRunsFromAnyDirectory renders pages and serves files with the working
// directory somewhere else: templates and static files are embedded.
func TestRunsFromAnyDirectory(t *testing.T) {
	t.Chdir(t.TempDir())
	srv, _ := testServer(t, Options{})
	p := newPlayer(t, srv, "Ana")
	code := p.createRoom(2000)
	if body, resp := p.get("/r/" + code); resp.StatusCode != http.StatusOK || !strings.Contains(body, "Waiting for players") {
		t.Fatalf("room page: %d", resp.StatusCode)
	}
	if _, resp := p.get("/static/js/arena.js"); resp.StatusCode != http.StatusOK {
		t.Fatalf("arena.js: %d", resp.StatusCode)
	}
}

// TestFormsNeedTheCSRFToken: a post without the page's token, or with
// another one, is refused before the handler runs.
func TestFormsNeedTheCSRFToken(t *testing.T) {
	srv, _ := testServer(t, Options{})
	p := newPlayer(t, srv, "Ana")
	token := p.csrf

	for _, tc := range []struct{ name, token string }{{"missing", ""}, {"wrong", "not-the-token"}} {
		p.csrf = tc.token
		body, resp := p.post("/rooms", url.Values{"target": {"2000"}}, true)
		if resp.StatusCode != http.StatusForbidden || resp.Header.Get("HX-Redirect") != "" {
			t.Fatalf("%s token: %d %s", tc.name, resp.StatusCode, body)
		}
	}

	// Each render masks the token differently, and every rendered token is
	// good: the token from a second page load, after an asset fetch, works.
	body, _ := p.get("/")
	second := pageToken(body)
	if second == "" {
		t.Fatal("the second page load rendered no CSRF token")
	}
	p.get("/static/css/arena.css")
	p.csrf = second
	if _, resp := p.post("/rooms", url.Values{"target": {"2000"}}, true); !strings.HasPrefix(resp.Header.Get("HX-Redirect"), "/r/") {
		t.Fatalf("post with the second page's token: %d", resp.StatusCode)
	}
	p.csrf = token
	if _, resp := p.post("/rooms", url.Values{"target": {"2000"}}, true); !strings.HasPrefix(resp.Header.Get("HX-Redirect"), "/r/") {
		t.Fatalf("post with the first page's token: %d", resp.StatusCode)
	}
}

// TestProductionSettings checks that ENVIRONMENT=production reaches the app
// through config.FromEnv, as main builds it.
func TestProductionSettings(t *testing.T) {
	t.Setenv("ENVIRONMENT", "production")
	cfg := config.FromEnv()
	if !cfg.IsProduction() {
		t.Fatal("ENVIRONMENT=production did not take effect")
	}
	if _, _, err := newApp(cfg, appOptions{}); err == nil || !strings.Contains(err.Error(), "SECRET_KEY") {
		t.Fatalf("production without SECRET_KEY: err = %v, want a refusal", err)
	}
	cfg.Security.SecretKey = strings.Repeat("k", config.MinSecretKeyLength)
	app, _, err := newApp(cfg, appOptions{})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)
	p := newPlayer(t, srv, "")

	resp, err := http.Get(srv.URL + "/") // no cookie jar: the first visit
	if err != nil {
		t.Fatal(err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resp.Header.Get("Content-Security-Policy"), "default-src 'self'") {
		t.Fatal("no CSP in production")
	}
	if !strings.Contains(resp.Header.Get("Set-Cookie"), "Secure") {
		t.Fatalf("player cookie is not Secure in production: %s", resp.Header.Get("Set-Cookie"))
	}
	if _, resp := p.get("/static/css/arena.css"); resp.Header.Get("Cache-Control") != "public, max-age=3600" {
		t.Fatalf("static cache in production = %q", resp.Header.Get("Cache-Control"))
	}

	// The hub keeps its same-origin default: another site cannot open a socket.
	ctx := context.Background()
	if conn, err := p.dial(ctx, "/ws/lobby", http.Header{"Origin": {"https://evil.example"}}); err == nil {
		if cerr := conn.CloseNow(); cerr != nil {
			t.Log(cerr)
		}
		t.Fatal("cross-origin WebSocket accepted in production")
	}
	conn, err := p.dial(ctx, "/ws/lobby", http.Header{"Origin": {srv.URL}})
	if err != nil {
		t.Fatalf("same-origin WebSocket refused: %v", err)
	}
	if err := conn.CloseNow(); err != nil {
		t.Log(err)
	}
}
