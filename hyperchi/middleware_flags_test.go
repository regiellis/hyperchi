package hyperchi

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/regiellis/hyperchi/hyperchi/config"
)

// flagApp builds an AutoMiddleware app from cfg with one page that prints the
// template's csrfToken and one POST route.
func flagApp(t *testing.T, cfg *config.Config) *HyperChi {
	t.Helper()
	cfg.Database.Disabled = true
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "form.tmpl"), []byte(`{{define "form"}}token=[{{.csrfToken}}]{{end}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	app := NewWithConfig(cfg)
	if err := app.LoadTemplates(filepath.Join(dir, "*.tmpl")); err != nil {
		t.Fatal(err)
	}
	app.AutoMiddleware()
	app.Get("/form", func(c *Context) error { return c.Layout("none").Page("form", nil) })
	app.Post("/save", func(c *Context) error { return c.Text("saved") })
	return app
}

var tokenInPage = regexp.MustCompile(`token=\[([^\]]*)\]`)

// Security.CSRF (on by default) makes AutoMiddleware require a token on
// unsafe requests, and templates get one as .csrfToken.
func TestAutoMiddlewareCSRFOnByDefault(t *testing.T) {
	app := flagApp(t, config.Default())

	page := httptest.NewRecorder()
	app.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/form", nil))
	m := tokenInPage.FindStringSubmatch(page.Body.String())
	if m == nil || m[1] == "" {
		t.Fatalf("page has no csrfToken: %q", page.Body.String())
	}
	cookies := page.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("no csrf_token cookie set")
	}

	bare := httptest.NewRecorder()
	app.ServeHTTP(bare, httptest.NewRequest(http.MethodPost, "/save", nil))
	if bare.Code != http.StatusForbidden {
		t.Errorf("POST without a token: %d, want 403", bare.Code)
	}

	req := httptest.NewRequest(http.MethodPost, "/save", nil)
	req.Header.Set("X-CSRF-Token", m[1])
	for _, c := range cookies {
		req.AddCookie(c)
	}
	ok := httptest.NewRecorder()
	app.ServeHTTP(ok, req)
	if ok.Code != http.StatusOK {
		t.Errorf("POST with the page token: %d, want 200", ok.Code)
	}
}

func TestAutoMiddlewareCSRFOff(t *testing.T) {
	cfg := config.Default()
	cfg.Security.CSRF = false
	app := flagApp(t, cfg)
	w := httptest.NewRecorder()
	app.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/save", nil))
	if w.Code != http.StatusOK {
		t.Errorf("POST with CSRF off: %d, want 200", w.Code)
	}
}

// Security.CORS=false keeps the CORS headers off even in development, where
// the origin list otherwise defaults to "*".
func TestAutoMiddlewareCORSFlag(t *testing.T) {
	for _, on := range []bool{true, false} {
		cfg := config.Default()
		cfg.Security.CORS = on
		app := flagApp(t, cfg)
		w := httptest.NewRecorder()
		app.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/form", nil))
		if got := w.Header().Get("Access-Control-Allow-Origin") != ""; got != on {
			t.Errorf("CORS=%v: Access-Control-Allow-Origin present = %v", on, got)
		}
	}
}

// Security.RateLimit is opt-in: off by default, and when on AutoMiddleware
// limits each client.
func TestAutoMiddlewareRateLimitFlag(t *testing.T) {
	if config.Default().Security.RateLimit {
		t.Error("RateLimit should default to false")
	}
	for _, on := range []bool{false, true} {
		cfg := config.Default()
		cfg.Security.RateLimit = on
		app := flagApp(t, cfg)
		w := httptest.NewRecorder()
		app.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/form", nil))
		if got := w.Header().Get("X-RateLimit-Limit") != ""; got != on {
			t.Errorf("RateLimit=%v: X-RateLimit-Limit present = %v", on, got)
		}
		if !strings.Contains(w.Body.String(), "token=") {
			t.Errorf("RateLimit=%v: page not served: %d", on, w.Code)
		}
	}
}
