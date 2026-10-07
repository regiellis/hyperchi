package hyperchi

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Middleware written outside the framework (an optional module's rate limiter,
// an app's own net/http middleware) answers refusals through the same error
// pipeline as the framework's own denials.
func TestRespondStatusUsesTheErrorPipeline(t *testing.T) {
	app := New()
	var seen int
	app.OnError(func(c *Context, err error) {
		var he *HTTPError
		if errors.As(err, &he) {
			seen = he.Code
		}
		if werr := c.Status(seen).Text("handled"); werr != nil {
			t.Error(werr)
		}
	})
	limit := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			app.RespondStatus(w, r, http.StatusTooManyRequests, NewHTTPError(http.StatusTooManyRequests, "Slow down."))
		})
	}
	app.Get("/", func(c *Context) error { return c.Text("in") }, limit)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusTooManyRequests || rec.Body.String() != "handled" || seen != http.StatusTooManyRequests {
		t.Fatalf("got %d %q (OnError saw %d)", rec.Code, rec.Body.String(), seen)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("refusal is cacheable: %v", rec.Header())
	}
}
