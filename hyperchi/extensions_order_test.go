package hyperchi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// EnableAllExtensions installs its middleware as deferred global middleware,
// so enabling extensions after a route is registered must not panic. chi's Use
// would ("all middlewares must be defined before routes on a mux").
func TestEnableAllExtensionsAfterRoutesDoesNotPanic(t *testing.T) {
	app := New()

	// The route comes first: this order is what chi's Use cannot handle.
	app.Get("/first", func(c *Context) error { return c.Text("ok") })

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("EnableAllExtensions panicked after a route was registered: %v", r)
		}
	}()
	app.Extensions().EnableAllExtensions()

	// The pre-existing route still serves, and the deferred extension middleware
	// runs for it (setting its debug header on htmx requests).
	req := httptest.NewRequest(http.MethodGet, "/first", nil)
	req.Header.Set("HX-Request", "true")
	w := httptest.NewRecorder()
	app.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if got := w.Header().Get("X-HTMX-Extensions"); got == "" {
		t.Fatal("extension middleware did not run for a route registered before EnableAllExtensions")
	}
}
