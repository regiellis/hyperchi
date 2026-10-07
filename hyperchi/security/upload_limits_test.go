package security

import (
	"bytes"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"
)

// multipartBody builds a multipart body with one text/plain file per size.
func multipartBody(t *testing.T, sizes ...int) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	for i, size := range sizes {
		part, err := form.CreatePart(textproto.MIMEHeader{
			"Content-Disposition": {fmt.Sprintf(`form-data; name="file"; filename="f%d.txt"`, i)},
			"Content-Type":        {"text/plain"},
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(bytes.Repeat([]byte("a"), size)); err != nil {
			t.Fatal(err)
		}
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	return &body, form.FormDataContentType()
}

func uploadRequest(t *testing.T, sizes ...int) *http.Request {
	t.Helper()
	body, contentType := multipartBody(t, sizes...)
	r := httptest.NewRequest(http.MethodPost, "/upload", body)
	r.Header.Set("Content-Type", contentType)
	return r
}

func publicUploadConfig() *FileUploadConfig {
	cfg := DefaultUploadConfig()
	cfg.RequireAuth = false
	return cfg
}

func serveUpload(cfg *FileUploadConfig, r *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	UploadMiddleware(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(rec, r)
	return rec
}

// A body that is not valid multipart is the client's mistake, not a size
// problem.
func TestUploadMiddlewareMalformedBodyIs400(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/upload", strings.NewReader("this is not multipart"))
	r.Header.Set("Content-Type", "multipart/form-data; boundary=xyz")
	rec := serveUpload(publicUploadConfig(), r)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body %q)", rec.Code, rec.Body.String())
	}
}

func TestUploadMiddlewareOversizedBodyIs413(t *testing.T) {
	cfg := publicUploadConfig()
	cfg.MaxFileSize = 1024
	cfg.MaxFiles = 1
	// Past the 1024-byte total plus the 1 MiB multipart allowance.
	rec := serveUpload(cfg, uploadRequest(t, 2<<20))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rec.Code)
	}
}

func TestUploadMiddlewareTooManyFilesIs400(t *testing.T) {
	cfg := publicUploadConfig()
	cfg.MaxFiles = 1
	rec := serveUpload(cfg, uploadRequest(t, 5, 5))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestUploadMiddlewareOversizedFileIs413(t *testing.T) {
	cfg := publicUploadConfig()
	cfg.MaxFileSize = 10
	rec := serveUpload(cfg, uploadRequest(t, 11))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rec.Code)
	}
}

// The combined size defaults to MaxFiles * MaxFileSize, the same budget the
// body limit is sized from, so a request with every file at the per-file
// limit passes.
func TestUploadTotalSizeBoundary(t *testing.T) {
	for _, tc := range []struct {
		name     string
		maxTotal int64
		sizes    []int
		valid    bool
	}{
		{name: "default total, every file at the limit", sizes: []int{10, 10, 10}, valid: true},
		{name: "explicit total, exactly at it", maxTotal: 25, sizes: []int{10, 10, 5}, valid: true},
		{name: "explicit total, one byte over", maxTotal: 25, sizes: []int{10, 10, 6}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := publicUploadConfig()
			cfg.MaxFileSize = 10
			cfg.MaxFiles = 3
			cfg.MaxTotalSize = tc.maxTotal
			res := NewUploadValidator(cfg).ValidateUpload(uploadRequest(t, tc.sizes...))
			if res.Valid != tc.valid {
				t.Fatalf("Valid = %v, want %v (errors %v)", res.Valid, tc.valid, res.Errors)
			}
			if !tc.valid {
				var limit *UploadLimitError
				if !errors.As(res.Err, &limit) || limit.Limit != tc.maxTotal {
					t.Fatalf("Err = %v, want an *UploadLimitError for the %d-byte total", res.Err, tc.maxTotal)
				}
			}
		})
	}
}

// A direct caller has no ResponseWriter, so ValidateUpload applies its own
// body limit. Going over it must be reported as a size limit, not a parse
// failure.
func TestValidateUploadDirectBodyLimitIsTyped(t *testing.T) {
	cfg := publicUploadConfig()
	cfg.MaxFileSize = 1024
	cfg.MaxFiles = 1
	res := NewUploadValidator(cfg).ValidateUpload(uploadRequest(t, 2<<20))
	if res.Valid {
		t.Fatal("oversized body validated")
	}
	var limit *UploadLimitError
	if !errors.As(res.Err, &limit) {
		t.Fatalf("Err = %v, want an *UploadLimitError", res.Err)
	}
}
