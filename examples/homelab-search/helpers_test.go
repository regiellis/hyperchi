package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/regiellis/hyperchi/hyperchi/config"
)

// testSite builds the app over a library in t.TempDir, with its database in
// t.TempDir too, and primes the index. No background goroutine runs: tests
// drive the watcher with step.
func testSite(t *testing.T, files map[string]string, tweak ...func(*options)) *site {
	t.Helper()
	tmp := t.TempDir()
	lib := filepath.Join(tmp, "library")
	if err := os.MkdirAll(lib, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFiles(t, lib, files)
	cfg := config.Default()
	cfg.Database.Name = filepath.Join(tmp, "index.db")
	opts := defaultOptions()
	opts.LibraryDir = lib
	for _, f := range tweak {
		f(&opts)
	}
	s, err := newSite(cfg, opts)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		if err := s.close(); err != nil {
			t.Error(err)
		}
	})
	if err := s.prime(ctx); err != nil {
		t.Fatal(err)
	}
	return s
}

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// get sends a request through the whole app, middleware included.
func get(t *testing.T, s *site, target string, header ...string) (*http.Response, string) {
	t.Helper()
	return do(t, s, http.MethodGet, target, header...)
}

func do(t *testing.T, s *site, method, target string, header ...string) (*http.Response, string) {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	rec := httptest.NewRecorder()
	s.app.ServeHTTP(rec, req)
	res := rec.Result()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if err := res.Body.Close(); err != nil {
		t.Fatal(err)
	}
	return res, string(body)
}

func hitPaths(r Results) []string {
	var out []string
	for _, h := range r.Hits {
		out = append(out, h.Path)
	}
	return out
}

func search(t *testing.T, s *site, p SearchParams) Results {
	t.Helper()
	res, err := s.ix.Search(context.Background(), p)
	if err != nil {
		t.Fatalf("search %+v: %v", p, err)
	}
	return res
}

func mustContain(t *testing.T, body string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(body, want) {
			t.Errorf("body is missing %q", want)
		}
	}
}

func mustNotContain(t *testing.T, body string, unwanted ...string) {
	t.Helper()
	for _, u := range unwanted {
		if strings.Contains(body, u) {
			t.Errorf("body contains %q", u)
		}
	}
}
