package main

import (
	"bytes"
	"fmt"
	"html"
	"math"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"path/filepath"
	"strings"
	"testing"

	"github.com/regiellis/hyperchi/hyperchi"
	"github.com/regiellis/hyperchi/hyperchi/config"
)

func inputTestApp(t *testing.T) *hyperchi.HyperChi {
	t.Helper()
	cfg := config.Default()
	cfg.Database.Name = filepath.Join(t.TempDir(), "input.db")
	app := hyperchi.NewWithConfig(cfg)
	t.Cleanup(func() { _ = app.CloseDB() })
	if err := setupRoutes(app, ""); err != nil {
		t.Fatal(err)
	}
	return app
}

func TestEchoEscapesUntrustedTarget(t *testing.T) {
	app := inputTestApp(t)
	req := httptest.NewRequest(http.MethodGet, "/demo/echo", nil)
	payload := `<img src=x onerror=alert(1)>`
	req.Header.Set("HX-Request", "true")
	req.Header.Set("HX-Target", payload)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), payload) || !strings.Contains(rec.Body.String(), html.EscapeString(payload)) {
		t.Fatalf("unsafe echo: status %d, body %q", rec.Code, rec.Body.String())
	}
}

func TestActivityRejectsMalformedForm(t *testing.T) {
	app := inputTestApp(t)
	req := httptest.NewRequest(http.MethodPost, "/activity/send", strings.NewReader("message=%zz"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestUploadRejectsMultipleFiles(t *testing.T) {
	app := inputTestApp(t)
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	for i := range 2 {
		header := make(textproto.MIMEHeader)
		header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="test%d.txt"`, i))
		header.Set("Content-Type", "text/plain")
		part, err := form.CreatePart(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write([]byte("hello")); err != nil {
			t.Fatal(err)
		}
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/upload", &body)
	req.Header.Set("Content-Type", form.FormDataContentType())
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "Too many files") {
		t.Fatalf("status %d, body %q", rec.Code, rec.Body.String())
	}
}

func TestChangelogPaginationRejectsOverflow(t *testing.T) {
	for _, tc := range []struct{ page, size int }{
		{math.MaxInt, 6}, {math.MaxInt/6 + 2, 6}, {-1, 6}, {1, 0}, {1, -1},
	} {
		got, err := changelogPage(tc.page, tc.size)
		if err != nil || len(got.([]Entry)) != 0 {
			t.Errorf("page(%d,%d) = %v, %v", tc.page, tc.size, got, err)
		}
	}
	got, err := changelogPage(1, math.MaxInt)
	if err != nil || len(got.([]Entry)) != len(changelog) {
		t.Fatalf("large page size: %v, %v", got, err)
	}
}
