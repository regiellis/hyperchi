package hyperchi

import (
	"errors"
	"html/template"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/regiellis/hyperchi/hyperchi/config"
)

func productionTestApp(t *testing.T) *HyperChi {
	t.Helper()
	cfg := config.Production()
	cfg.Database.Name = filepath.Join(t.TempDir(), "test.db")
	app, err := NewWithConfigE(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := app.CloseDB(); err != nil {
			t.Error(err)
		}
	})
	return app
}

func TestProductionConfigControlsMiddleware(t *testing.T) {
	t.Setenv("ENVIRONMENT", "development")
	t.Setenv("GO_ENV", "development")
	app := productionTestApp(t)
	w := httptest.NewRecorder()
	app.Middleware.Security()(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Header().Get("Content-Security-Policy") == "" || w.Header().Get("Strict-Transport-Security") == "" {
		t.Error("production config omitted security headers")
	}
	w = httptest.NewRecorder()
	app.ToHTTP(func(*Context) error { return errors.New("private-probe-detail") })(w, httptest.NewRequest("GET", "/", nil))
	if strings.Contains(w.Body.String(), "private-probe-detail") {
		t.Error("production config exposed development details")
	}
}

func TestConstructorReportsDatabaseFailure(t *testing.T) {
	cfg := config.Production()
	cfg.Database.Name = t.TempDir() // A directory cannot be opened as a database.
	app, err := NewWithConfigE(cfg)
	if err == nil || app != nil {
		t.Fatalf("got app=%v, err=%v; want nil app and database error", app != nil, err)
	}
}

func TestProductionLayoutFailureReturnsError(t *testing.T) {
	t.Setenv("ENVIRONMENT", "production")
	for _, layout := range []string{"base", "missing"} {
		ts := template.Must(template.New("test").Parse(`{{define "page"}}content{{end}}{{define "layout-base"}}{{index .missing 0}}{{end}}`))
		_, err := (&TemplateRenderer{Template: ts, Name: "page", Layout: layout}).Render(nil, nil, httptest.NewRequest("GET", "/", nil))
		if err == nil {
			t.Errorf("layout %s accepted", layout)
		}
	}
}

func TestRateLimitIgnoresUntrustedForwardingHeaders(t *testing.T) {
	app := productionTestApp(t)
	h := app.Middleware.RateLimit(1, time.Hour)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	for i, forwarded := range []string{"198.51.100.1", "198.51.100.2"} {
		r := httptest.NewRequest("POST", "/", nil)
		r.RemoteAddr = "192.0.2.1:1234"
		r.Header.Set("X-Forwarded-For", forwarded)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		want := 204
		if i == 1 {
			want = 429
		}
		if w.Code != want {
			t.Errorf("request %d: got %d, want %d", i, w.Code, want)
		}
	}
}
