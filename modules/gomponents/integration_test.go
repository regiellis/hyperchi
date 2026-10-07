package gomponentsadapter_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/regiellis/hyperchi/hyperchi"
	"github.com/regiellis/hyperchi/hyperchi/config"
	gomponentsadapter "github.com/regiellis/hyperchi/modules/gomponents"
	g "maragu.dev/gomponents"
	h "maragu.dev/gomponents/html"
)

// newApp registers the adapter the way an application does and starts the
// integration manager, which Serve would otherwise do.
func newApp(t *testing.T) *hyperchi.HyperChi {
	t.Helper()
	// Production, so a 500 never shows its cause (development pages do).
	cfg := config.Production()
	cfg.Database.Disabled = true
	app, err := hyperchi.NewWithConfigE(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Integrations().Register("gomponents", gomponentsadapter.New()); err != nil {
		t.Fatal(err)
	}
	app.Get("/ok", func(c *hyperchi.Context) error {
		return c.RenderWith("gomponents", h.Main(h.H1(g.Text("Lists")), gomponentsadapter.HxGet("/lists")))
	})
	app.Post("/invalid", func(c *hyperchi.Context) error {
		return c.Status(http.StatusUnprocessableEntity).RenderWith("gomponents", h.P(g.Text("Email is required.")))
	})
	app.Get("/broken", func(c *hyperchi.Context) error {
		return c.RenderWith("gomponents", h.Div(g.Text("half a page"), g.NodeFunc(func(io.Writer) error {
			return errors.New("database went away")
		})))
	})
	app.Get("/wrong", func(c *hyperchi.Context) error {
		return c.RenderWith("gomponents", "<p>not a node</p>")
	})
	if err := app.Integrations().Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := app.Close(); err != nil {
			t.Error(err)
		}
	})
	return app
}

func do(app *hyperchi.HyperChi, method, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func TestRenderWithSendsHTML(t *testing.T) {
	rec := do(newApp(t), http.MethodGet, "/ok")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Fatalf("content type %q", ct)
	}
	if body := rec.Body.String(); body != `<main hx-get="/lists"><h1>Lists</h1></main>` {
		t.Fatalf("body %s", body)
	}
}

func TestRenderWithKeepsTheStatus(t *testing.T) {
	rec := do(newApp(t), http.MethodPost, "/invalid")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Fatalf("content type %q", ct)
	}
}

func TestRenderErrorsSendNoPartialPage(t *testing.T) {
	app := newApp(t)
	for _, path := range []string{"/broken", "/wrong"} {
		rec := do(app, http.MethodGet, path)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("%s: status %d", path, rec.Code)
		}
		if body := rec.Body.String(); strings.Contains(body, "half a page") || strings.Contains(body, "database went away") {
			t.Fatalf("%s leaked a partial render or the cause: %s", path, body)
		}
	}
}
