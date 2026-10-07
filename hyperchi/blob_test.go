package hyperchi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Blob sends a body with any content type, which Text and HTML cannot: they
// set their own.
func TestBlobSendsBodyWithGivenContentType(t *testing.T) {
	app := New()
	app.Get("/doc.md", func(c *Context) error {
		return c.Status(http.StatusAccepted).Blob("text/markdown; charset=utf-8", []byte("# Title\n"))
	})
	app.Get("/raw", func(c *Context) error {
		return c.Blob("", []byte{0x01, 0x02})
	})

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/doc.md", nil))
	if rec.Code != http.StatusAccepted || rec.Header().Get("Content-Type") != "text/markdown; charset=utf-8" || rec.Body.String() != "# Title\n" {
		t.Fatalf("got %d %q %q", rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
	}

	app.Head("/doc.md", func(c *Context) error {
		return c.Blob("text/markdown; charset=utf-8", []byte("# Title\n"))
	})
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodHead, "/doc.md", nil))
	if rec.Header().Get("Content-Type") != "text/markdown; charset=utf-8" || rec.Body.Len() != 0 {
		t.Fatalf("HEAD got %q with %d body bytes", rec.Header().Get("Content-Type"), rec.Body.Len())
	}

	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/raw", nil))
	if got := rec.Header().Get("Content-Type"); got != "application/octet-stream" {
		t.Fatalf("empty content type sent as %q, want application/octet-stream", got)
	}
}
