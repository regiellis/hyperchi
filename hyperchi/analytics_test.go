package hyperchi

import (
	"bytes"
	"context"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/regiellis/hyperchi/hyperchi/analytics"
)

func TestAnalyticsSetupAndRequests(t *testing.T) {
	t.Parallel()
	app := toolbarApp(t, true)
	if app.AnalyticsScript() != "" {
		t.Fatal("enabled by default")
	}
	var events []analytics.RequestEvent
	cfg := analytics.Config{Recorder: analytics.RecorderFunc(func(_ context.Context, e analytics.RequestEvent) error { events = append(events, e); return nil })}
	if err := app.EnableAnalytics(analytics.Config{}); err == nil {
		t.Fatal("missing recorder accepted")
	}
	if err := app.EnableAnalytics(cfg); err != nil {
		t.Fatal(err)
	}
	if err := app.EnableAnalytics(cfg); err == nil {
		t.Fatal("duplicate setup accepted")
	}
	app.Get("/users/{id}", func(c *Context) error { return NotFound("absent") })
	w := devGet(app, "/users/secret?token=private")
	if w.Code != 404 || len(events) != 1 || events[0].Route != "/users/{id}" || events[0].Status != 404 {
		t.Fatalf("events %+v", events)
	}
	source, err := os.ReadFile("../frontend/assets/scripts/hyperchi-analytics.js")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(source, analyticsJS) {
		t.Fatal("run task sync-runtime-assets")
	}
	if !strings.Contains(string(app.AnalyticsScript()), "/hyperchi/js/analytics.js") {
		t.Fatal("missing script tag")
	}
	if _, ok := app.funcMap["analyticsScript"]; !ok {
		t.Fatal("missing template helper")
	}
	w = devGet(app, "/hyperchi/js/analytics.js")
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), source) || len(events) != 1 {
		t.Fatal("script or exclusion failed")
	}
	r := httptest.NewRequest("GET", "/hyperchi/js/analytics.js", nil)
	r.Header.Set("If-None-Match", w.Header().Get("ETag"))
	w = httptest.NewRecorder()
	app.ServeHTTP(w, r)
	if w.Code != 304 {
		t.Fatal("etag not honored")
	}
	for _, tt := range []struct {
		method string
		want   int
	}{{"HEAD", 200}, {"POST", 405}} {
		t.Run(tt.method, func(t *testing.T) {
			w := httptest.NewRecorder()
			app.ServeHTTP(w, httptest.NewRequest(tt.method, "/hyperchi/js/analytics.js", nil))
			if w.Code != tt.want {
				t.Fatalf("status %d", w.Code)
			}
			if tt.method == "HEAD" && w.Body.Len() != 0 {
				t.Fatal("HEAD body")
			}
		})
	}
}

func TestAnalyticsWithOtherFeatures(t *testing.T) {
	t.Parallel()
	app := toolbarApp(t, false)
	cfg := analytics.Config{Recorder: analytics.RecorderFunc(func(context.Context, analytics.RequestEvent) error { return nil })}
	if err := app.EnableAnalytics(cfg); err != nil {
		t.Fatal(err)
	}
	if err := app.EnableDevToolbar(DevToolbarOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := app.EnablePrefetch(); err != nil {
		t.Fatal(err)
	}
	app.Get("/hello", func(c *Context) error { return c.Text("ok") })
	if w := devGet(app, "/hello"); w.Code != 200 {
		t.Fatal(w.Code)
	}
	other := toolbarApp(t, true)
	other.Get("/first", func(c *Context) error { return c.Text("ok") })
	if err := other.EnableAnalytics(cfg); err == nil {
		t.Fatal("late registration accepted")
	}
}
