package hyperchi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/regiellis/hyperchi/hyperchi/config"
)

func toolbarApp(t *testing.T, production bool) *HyperChi {
	t.Helper()
	cfg := config.Default()
	cfg.Database.Disabled = true
	cfg.Assets.HotReload = false
	if production {
		cfg.Server.Environment = "production"
	}
	app := NewWithConfig(cfg)
	t.Cleanup(func() {
		if err := app.Close(); err != nil {
			t.Error(err)
		}
	})
	return app
}
func devGet(app http.Handler, target string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", "http://localhost"+target, nil)
	req.RemoteAddr = "127.0.0.1:5000"
	w := httptest.NewRecorder()
	app.ServeHTTP(w, req)
	return w
}
func devSnapshot(t *testing.T, app http.Handler) ([]devRoute, []devRequest) {
	t.Helper()
	w := devGet(app, devToolbarPath+"/data")
	if w.Code != 200 {
		t.Fatalf("data: %d %s", w.Code, w.Body)
	}
	var result struct {
		Routes   []devRoute
		Requests []devRequest
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result.Routes, result.Requests
}
func TestDevToolbarRoutesAndLogs(t *testing.T) {
	t.Parallel()
	app := toolbarApp(t, false)
	if err := app.EnableDevToolbar(DevToolbarOptions{MaxRequests: 2}); err != nil {
		t.Fatal(err)
	}
	app.Group("/users", func(g *HyperChi) { g.Get("/{id}", func(c *Context) error { return c.Status(201).Text("hello") }) })
	for _, path := range []string{"/users/secret-one?token=secret-query", "/users/secret-two", "/users/secret-three"} {
		if w := devGet(app, path); w.Code != 201 || w.Body.String() != "hello" {
			t.Fatalf("response changed: %d %s", w.Code, w.Body)
		}
	}
	routes, requests := devSnapshot(t, app)
	if len(requests) != 2 || requests[0].ID != 3 || requests[1].ID != 2 {
		t.Fatalf("retention: %+v", requests)
	}
	for _, r := range requests {
		if r.Route != "/users/{id}" || r.Status != 201 || r.Bytes != 5 || r.Aborted || r.DurationMS < 0 {
			t.Fatalf("request: %+v", r)
		}
	}
	found := false
	for _, r := range routes {
		if r.Method == "GET" && r.Pattern == "/users/{id}" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing grouped route: %+v", routes)
	}
	data := devGet(app, devToolbarPath+"/data")
	if strings.Contains(data.Body.String(), "secret-") {
		t.Fatal("recorded sensitive request values")
	}
	if data.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("cacheable developer data")
	}
	devGet(app, "/missing-secret?password=secret")
	_, requests = devSnapshot(t, app)
	if requests[0].Route != "(unmatched)" || requests[0].Status != 404 {
		t.Fatalf("unmatched: %+v", requests[0])
	}
}
func TestDevToolbarAccess(t *testing.T) {
	t.Parallel()
	app := toolbarApp(t, false)
	// An existing IP middleware must not turn a remote client into a local one.
	app.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { r.RemoteAddr = "127.0.0.1:1234"; next.ServeHTTP(w, r) })
	})
	if err := app.EnableDevToolbar(DevToolbarOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, remote, host, origin, site string
		want                             int
	}{
		{"localhost", "127.0.0.1:1", "localhost:8000", "", "same-origin", 200},
		{"IPv6", "[::1]:1", "[::1]:8000", "", "", 200},
		{"remote peer", "192.0.2.1:1", "localhost", "", "", 404},
		{"remote host", "127.0.0.1:1", "evil.example", "", "", 404},
		{"cross site", "127.0.0.1:1", "localhost", "", "cross-site", 404},
		{"cross origin", "127.0.0.1:1", "localhost", "http://evil.example", "", 404},
		{"null origin", "127.0.0.1:1", "localhost", "null", "", 404},
		{"matching origin", "127.0.0.1:1", "localhost:8000", "http://localhost:8000", "", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, endpoint := range []string{"/data", "/toolbar.js", "/toolbar.css"} {
				r := httptest.NewRequest("GET", "http://localhost"+devToolbarPath+endpoint, nil)
				r.RemoteAddr = tc.remote
				r.Host = tc.host
				r.Header.Set("Origin", tc.origin)
				r.Header.Set("Sec-Fetch-Site", tc.site)
				r.Header.Set("X-Forwarded-For", "127.0.0.1")
				w := httptest.NewRecorder()
				app.ServeHTTP(w, r)
				if w.Code != tc.want {
					t.Fatalf("%s: %d want %d", endpoint, w.Code, tc.want)
				}
			}
		})
	}
}
func TestDevToolbarDisabledAndSetup(t *testing.T) {
	t.Parallel()
	app := toolbarApp(t, true)
	if err := app.EnableDevToolbar(DevToolbarOptions{}); err != nil {
		t.Fatal(err)
	}
	if app.DevToolbar() != "" || len(app.router.Routes()) != 0 || len(app.deferredMiddleware) != 0 {
		t.Fatal("production toolbar enabled")
	}
	if w := devGet(app, devToolbarPath+"/data"); w.Code != 404 {
		t.Fatal(w.Code)
	}
	dev := toolbarApp(t, false)
	if dev.DevToolbar() != "" {
		t.Fatal("enabled by default")
	}
	if err := dev.EnableDevToolbar(DevToolbarOptions{MaxRequests: -1}); err == nil {
		t.Fatal("accepted invalid capacity")
	}
	if err := dev.EnableDevToolbar(DevToolbarOptions{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(dev.DevToolbar()), "/toolbar.js") {
		t.Fatal("missing script")
	}
	if err := dev.EnableDevToolbar(DevToolbarOptions{}); err == nil {
		t.Fatal("accepted duplicate setup")
	}
	late := toolbarApp(t, false)
	late.Get("/", func(c *Context) error { return c.Text("ok") })
	if err := late.EnableDevToolbar(DevToolbarOptions{}); err == nil {
		t.Fatal("accepted late setup")
	}
}
func TestDevToolbarConcurrentAndStreaming(t *testing.T) {
	t.Parallel()
	app := toolbarApp(t, false)
	if err := app.EnableDevToolbar(DevToolbarOptions{MaxRequests: 10}); err != nil {
		t.Fatal(err)
	}
	app.Handle("GET", "/stream", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Error(err)
		}
		_, _ = io.WriteString(w, "data: ok\n\n")
	}))
	var wg sync.WaitGroup
	for range 40 {
		wg.Go(func() {
			w := devGet(app, "/stream")
			if w.Code != 200 || w.Body.String() != "data: ok\n\n" {
				t.Error("stream changed")
			}
			devGet(app, devToolbarPath+"/data")
		})
	}
	wg.Wait()
	_, requests := devSnapshot(t, app)
	if len(requests) != 10 || requests[0].ID != 40 {
		t.Fatalf("requests: %+v", requests)
	}
}
func TestDevToolbarPanicAndHEAD(t *testing.T) {
	t.Parallel()
	app := toolbarApp(t, false)
	app.Use(app.Middleware.Recovery())
	if err := app.EnableDevToolbar(DevToolbarOptions{}); err != nil {
		t.Fatal(err)
	}
	app.Get("/panic", func(c *Context) error { panic("test") })
	if w := devGet(app, "/panic"); w.Code != 500 {
		t.Fatal(w.Code)
	}
	_, requests := devSnapshot(t, app)
	if len(requests) != 1 || requests[0].Aborted || requests[0].Status != 500 {
		t.Fatalf("panic log: %+v", requests)
	}
	for _, path := range []string{"/data", "/toolbar.js", "/toolbar.css"} {
		r := httptest.NewRequest("HEAD", "http://localhost"+devToolbarPath+path, nil)
		r.RemoteAddr = "127.0.0.1:1"
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		if w.Code != 200 || w.Body.Len() != 0 {
			t.Fatalf("HEAD %s: %d %s", path, w.Code, w.Body)
		}
	}
}

func TestDevToolbarMiddlewareRejection(t *testing.T) {
	t.Parallel()
	app := toolbarApp(t, false)
	app.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/private" {
				http.Error(w, "denied", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	})
	if err := app.EnableDevToolbar(DevToolbarOptions{}); err != nil {
		t.Fatal(err)
	}
	app.Get("/private", func(c *Context) error { t.Error("blocked handler ran"); return c.Text("secret") })
	devGet(app, "/private")
	_, requests := devSnapshot(t, app)
	if len(requests) != 1 || requests[0].Status != 403 || requests[0].Route != "(unmatched)" {
		t.Fatalf("rejected request: %+v", requests)
	}
}
