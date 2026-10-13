package hyperchi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

// Apps that embed their assets need the same confined, cached file serving as
// StaticWithOptions, from an fs.FS.
func TestStaticFSServesEmbeddedFilesSafely(t *testing.T) {
	fsys := fstest.MapFS{
		"css/site.css":    {Data: []byte("body{color:red}")},
		".env":            {Data: []byte("SECRET=1")},
		"css/.hidden.css": {Data: []byte("x")},
	}
	app := New()
	if err := app.StaticFS("/static/*", fsys, StaticOptions{}); err != nil {
		t.Fatal(err)
	}

	get := func(path string, header http.Header) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		for k, v := range header {
			req.Header[k] = v
		}
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		return rec
	}

	ok := get("/static/css/site.css", nil)
	if ok.Code != http.StatusOK || ok.Body.String() != "body{color:red}" {
		t.Fatalf("site.css = %d %q", ok.Code, ok.Body.String())
	}
	if ct := ok.Header().Get("Content-Type"); ct != "text/css; charset=utf-8" {
		t.Fatalf("Content-Type = %q", ct)
	}
	etag := ok.Header().Get("ETag")
	if etag == "" || ok.Header().Get("Cache-Control") == "" || ok.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("missing caching headers: %v", ok.Header())
	}
	if again := get("/static/css/site.css", http.Header{"If-None-Match": {etag}}); again.Code != http.StatusNotModified {
		t.Fatalf("conditional request = %d, want 304", again.Code)
	}

	for _, path := range []string{"/static/.env", "/static/css/.hidden.css", "/static/css/", "/static/../go.mod", "/static/missing.css"} {
		if rec := get(path, nil); rec.Code != http.StatusNotFound {
			t.Errorf("%s = %d, want 404", path, rec.Code)
		}
	}
	if err := app.StaticFS("/static", fsys, StaticOptions{}); err == nil {
		t.Error("a pattern without /* was accepted")
	}
}
