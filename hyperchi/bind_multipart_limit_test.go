package hyperchi

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
)

type uploadForm struct {
	Title string `form:"title" validate:"required"`
}

// Bind refuses a multipart body over its limit with 413, rather than spilling
// the parts past the in-memory limit to disk without bound.
func TestBindRefusesOversizedMultipartBodies(t *testing.T) {
	prev := maxMultipartBody
	maxMultipartBody = 1 << 10
	t.Cleanup(func() { maxMultipartBody = prev })

	app := New()
	app.Post("/upload", func(c *Context) error {
		in, err := Bind[uploadForm](c)
		if err != nil {
			return err
		}
		return c.Text(in.Title)
	}, JSONErrors)

	body := func(size int) (*bytes.Buffer, string) {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		if err := mw.WriteField("title", "report"); err != nil {
			t.Fatal(err)
		}
		fw, err := mw.CreateFormFile("file", "big.bin")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write(bytes.Repeat([]byte("x"), size)); err != nil {
			t.Fatal(err)
		}
		if err := mw.Close(); err != nil {
			t.Fatal(err)
		}
		return &buf, mw.FormDataContentType()
	}

	small, ct := body(100)
	req := httptest.NewRequest(http.MethodPost, "/upload", small)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "report" {
		t.Fatalf("small upload = %d %q", rec.Code, rec.Body.String())
	}

	big, ct := body(4 << 10)
	req = httptest.NewRequest(http.MethodPost, "/upload", big)
	req.Header.Set("Content-Type", ct)
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized upload = %d %q, want 413", rec.Code, rec.Body.String())
	}
}
