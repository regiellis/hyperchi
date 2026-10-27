package hyperchi

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestPrefetchSetupAndAssets(t *testing.T) {
	t.Parallel()
	app := toolbarApp(t, true)
	if app.PrefetchScript() != "" {
		t.Fatal("enabled by default")
	}
	app.Get("/existing", func(c *Context) error { return c.Text("ok") })
	if err := app.EnablePrefetch(); err != nil {
		t.Fatal(err)
	}
	if err := app.EnablePrefetch(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(app.PrefetchScript()), "/hyperchi/js/prefetch.js") {
		t.Fatal("missing script")
	}
	app.Group("/group", func(g *HyperChi) {
		if g.EnablePrefetch() == nil {
			t.Error("accepted group setup")
		}
	})
	source, err := os.ReadFile("../frontend/assets/scripts/hyperchi-prefetch.js")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(source, prefetchJS) {
		t.Fatal("run task sync-runtime-assets")
	}
	w := getAsset(app, "GET", "/hyperchi/js/prefetch.js")
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), source) || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/javascript") {
		t.Fatalf("asset response %d", w.Code)
	}
	for _, method := range []string{"GET", "HEAD"} {
		req := httptest.NewRequest(method, "/hyperchi/js/prefetch.js", nil)
		req.Header.Set("If-None-Match", w.Header().Get("ETag"))
		out := httptest.NewRecorder()
		app.ServeHTTP(out, req)
		if out.Code != 304 || out.Body.Len() != 0 {
			t.Fatalf("conditional %s: %d", method, out.Code)
		}
	}
	if head := getAsset(app, "HEAD", "/hyperchi/js/prefetch.js"); head.Code != 200 || head.Body.Len() != 0 {
		t.Fatal("HEAD response")
	}
}
func TestPrefetchPreservesVaryAndStreaming(t *testing.T) {
	t.Parallel()
	app := toolbarApp(t, false)
	app.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Header().Set("Vary", "Origin"); next.ServeHTTP(w, r) })
	})
	app.Handle("GET", "/stream", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Vary", "Accept")
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Error(err)
		}
		_, _ = w.Write([]byte("stream"))
	}))
	if err := app.EnablePrefetch(); err != nil {
		t.Fatal(err)
	}
	w := getAsset(app, "GET", "/stream")
	// Result records headers at the first write; Header() can mask late changes.
	vary := strings.Join(w.Result().Header.Values("Vary"), ",")
	for _, name := range []string{"Accept", "HX-Request", "HX-Request-Type", "HX-Boosted", "HX-Source", "HX-Target", "HX-Current-URL"} {
		if !strings.Contains(vary, name) {
			t.Errorf("missing %s in %q", name, vary)
		}
	}
	if w.Body.String() != "stream" || !w.Flushed {
		t.Fatal("stream altered")
	}
}
