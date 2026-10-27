package hyperchi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/regiellis/hyperchi/hyperchi/config"
)

// Every GET route answers HEAD with the GET handler's status and headers and
// no body, with or without AutoMiddleware; an explicit Head route still wins.
func TestGetRoutesAnswerHead(t *testing.T) {
	cfg := config.Default()
	cfg.Database.Disabled = true
	app := NewWithConfig(cfg)
	app.Get("/page", func(c *Context) error { return c.SetHeader("X-Page", "yes").Text("hello") })
	app.Get("/own", func(c *Context) error { return c.Text("get") })
	app.Head("/own", func(c *Context) error { return c.SetHeader("X-Own", "head").NoContent() })

	w := httptest.NewRecorder()
	app.ServeHTTP(w, httptest.NewRequest(http.MethodHead, "/page", nil))
	if w.Code != http.StatusOK || w.Header().Get("X-Page") != "yes" || w.Body.Len() != 0 {
		t.Errorf("HEAD /page: %d, X-Page %q, %d body bytes", w.Code, w.Header().Get("X-Page"), w.Body.Len())
	}

	w = httptest.NewRecorder()
	app.ServeHTTP(w, httptest.NewRequest(http.MethodHead, "/own", nil))
	if w.Header().Get("X-Own") != "head" {
		t.Errorf("HEAD /own did not use its own Head route: %v", w.Header())
	}

	w = httptest.NewRecorder()
	app.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/page", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /page: %d, want 405", w.Code)
	}
}
