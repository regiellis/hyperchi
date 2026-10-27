package hyperchi

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A no-JS multipart upload form carries its token as a form field, not a
// header; the middleware must find it there.
func TestCSRFAcceptsTheTokenFieldInAMultipartForm(t *testing.T) {
	app := newTemplateApp(t, true)
	app.Use(app.Middleware.CSRF())
	app.Get("/form", func(c *Context) error { return c.Text(c.CSRFToken()) })
	app.Post("/upload", func(c *Context) error { return c.Text("stored") })

	page := httptest.NewRecorder()
	app.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/form", nil))
	cookie := page.Result().Cookies()[0]

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if err := mw.WriteField("csrf_token", page.Body.String()); err != nil {
		t.Fatal(err)
	}
	fw, err := mw.CreateFormFile("file", "list.csv")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write([]byte("email\na@b.co\n")); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("multipart form with a token field = %d %q", rec.Code, rec.Body.String())
	}
}
