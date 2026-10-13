package assets

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
)

func TestModuleVersion(t *testing.T) {
	dep := func(path, version string, replace *debug.Module) *debug.Module {
		return &debug.Module{Path: path, Version: version, Replace: replace}
	}
	for _, tc := range []struct {
		name string
		info *debug.BuildInfo
		ok   bool
		want string
	}{
		{"no build info", nil, false, "(devel)"},
		{"hyperchi is the main module", &debug.BuildInfo{Main: debug.Module{Path: hyperchiModule, Version: "v0.2.0"}}, true, "v0.2.0"},
		{"main module without a version", &debug.BuildInfo{Main: debug.Module{Path: hyperchiModule}}, true, "(devel)"},
		{"hyperchi is a dependency", &debug.BuildInfo{
			Main: debug.Module{Path: "example.com/app", Version: "v9.9.9"},
			Deps: []*debug.Module{dep("github.com/go-chi/chi/v5", "v5.0.0", nil), dep(hyperchiModule, "v0.1.0", nil)},
		}, true, "v0.1.0"},
		{"replaced by a version", &debug.BuildInfo{
			Main: debug.Module{Path: "example.com/app"},
			Deps: []*debug.Module{dep(hyperchiModule, "v0.1.0", dep("example.com/fork", "v0.1.1", nil))},
		}, true, "v0.1.1"},
		{"replaced by a local path", &debug.BuildInfo{
			Main: debug.Module{Path: "example.com/app"},
			Deps: []*debug.Module{dep(hyperchiModule, "v0.1.0", dep("../hyperchi", "", nil))},
		}, true, "(devel)"},
		{"not linked in", &debug.BuildInfo{Main: debug.Module{Path: "example.com/app", Version: "v1.0.0"}}, true, "(devel)"},
	} {
		if got := moduleVersion(tc.info, tc.ok); got != tc.want {
			t.Errorf("%s: moduleVersion = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// The manifest's version comes from build info, not a hard-coded string.
func TestBundleHeroiconsWritesManifest(t *testing.T) {
	static := t.TempDir()
	b := NewBundler(&Config{StaticDir: static})
	if err := b.BundleHeroicons(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(static, "icons", "heroicons", "manifest.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Version string            `json:"version"`
		Icons   map[string]string `json:"icons"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("manifest is not JSON: %v", err)
	}
	if want := moduleVersion(debug.ReadBuildInfo()); manifest.Version != want || manifest.Version == "2.0.0" {
		t.Errorf("version = %q, want %q from the build info", manifest.Version, want)
	}
	if len(manifest.Icons) != len(heroiconsOutline) || !strings.HasPrefix(manifest.Icons["check"], "<svg") {
		t.Errorf("icons = %d entries, check = %q", len(manifest.Icons), manifest.Icons["check"])
	}
	if strings.Contains(string(data), `\u003c`) || !strings.Contains(string(data), "<svg") {
		t.Error("SVG markup was HTML-escaped")
	}

	// An existing manifest is kept.
	if err := os.WriteFile(path, []byte(`{"custom":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := b.BundleHeroicons(); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != `{"custom":true}` {
		t.Errorf("existing manifest replaced: %q, %v", got, err)
	}
}

func TestBundleSkipsExistingFiles(t *testing.T) {
	static := t.TempDir()
	for _, rel := range []string{"css/tailwind.min.css", "js/tailwindcss.min.js", "js/htmx.min.js"} {
		writeTestFile(t, filepath.Join(static, filepath.FromSlash(rel)), "cached")
	}
	b := NewBundler(&Config{StaticDir: static})
	// The files exist, so neither call touches the network.
	if err := b.BundleTailwind(); err != nil {
		t.Fatal(err)
	}
	if err := b.BundleHTMX(); err != nil {
		t.Fatal(err)
	}
}

func TestDownloadFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ok.js" {
			http.NotFound(w, r)
			return
		}
		if _, err := w.Write([]byte("var bundled = 1")); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(srv.Close)

	sum := sha256.Sum256([]byte("var bundled = 1"))
	okSHA := hex.EncodeToString(sum[:])

	dir := t.TempDir()
	b := NewBundler(&Config{StaticDir: dir})
	out := filepath.Join(dir, "nested", "ok.js")
	if err := b.downloadFile(srv.URL+"/ok.js", okSHA, out); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(out); err != nil || string(got) != "var bundled = 1" {
		t.Fatalf("downloaded = %q, %v", got, err)
	}
	if err := b.downloadFile(srv.URL+"/missing.js", okSHA, filepath.Join(dir, "missing.js")); err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("404 download: err = %v", err)
	}
	if err := b.downloadFile("http://127.0.0.1:0/x", okSHA, filepath.Join(dir, "x")); err == nil {
		t.Error("unreachable host: no error")
	}
	blocker := filepath.Join(dir, "file")
	writeTestFile(t, blocker, "x")
	if err := b.downloadFile(srv.URL+"/ok.js", okSHA, filepath.Join(blocker, "under-a-file.js")); err == nil {
		t.Error("a directory that cannot be created: no error")
	}
}

// A CDN response that does not match the pinned hash must never be saved.
func TestDownloadFileRejectsHashMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := w.Write([]byte("tampered")); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	b := NewBundler(&Config{StaticDir: dir})
	out := filepath.Join(dir, "js", "htmx.min.js")
	err := b.downloadFile(srv.URL, htmxSHA256, out)
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("mismatched download: err = %v", err)
	}
	entries, rerr := os.ReadDir(filepath.Dir(out))
	if rerr != nil {
		t.Fatal(rerr)
	}
	if len(entries) != 0 {
		t.Fatalf("rejected download left files behind: %v", entries)
	}
	if err := b.downloadFile(srv.URL, "", out); err == nil {
		t.Fatal("download without a pinned hash was accepted")
	}
}

func TestBundlerAssetPaths(t *testing.T) {
	b := NewBundler(&Config{})
	for got, want := range map[string]string{
		b.GetTailwindPath():               "/static/js/tailwindcss.min.js",
		b.GetHTMXPath():                   "/static/js/htmx.min.js",
		b.GetHeroiconsPath():              "/static/icons/heroicons",
		b.GetAssetPath("css", "app.css"):  "/static/css/app.css",
		b.GetAssetPath("fonts", "a.woff"): "/static/fonts/a.woff",
	} {
		if got != want {
			t.Errorf("path = %q, want %q", got, want)
		}
	}
}
