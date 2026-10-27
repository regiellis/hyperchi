package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var sampleLibrary = map[string]string{
	"runbooks/nginx-reload.md": "---\ntitle: Reload the reverse proxy\ntags: nginx, proxy\n---\n\n# Reload the reverse proxy\n\nRun nginx -t first.\n\n## Reload\n\nsudo systemctl reload nginx\n\n### After\n\nCheck the upstreams.\n",
	"configs/nginx/lab.conf":   "server {\n    listen 443 ssl;\n    server_name <lab>;\n}\n",
	"notes/xss.md":             "# Notes <img src=x onerror=alert(1)>\n\n<script>alert('md')</script>\n\nplain words\n",
	"notes/xss.txt":            "<script>alert('txt')</script> & friends",
	"manuals/ups.pdf":          "%PDF-1.7\x00binary",
	"site/index.html":          "<script>alert('stored')</script>",
}

func TestSearchFullPageAndFragment(t *testing.T) {
	s := testSite(t, sampleLibrary)

	res, page := get(t, s, "/?q=nginx")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}
	mustContain(t, page, "<!DOCTYPE html>", `id="main"`, `class="results"`, `value="nginx"`,
		`href="/doc/runbooks/nginx-reload.md"`, "<mark>nginx</mark>", `hx-sse:connect="/events?channels=index-status"`,
		`<script defer src="/hyperchi/js/hx-sse.js">`)

	res, frag := get(t, s, "/?q=nginx&type=&tag=", "HX-Request", "true")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("fragment status %d", res.StatusCode)
	}
	mustContain(t, frag, `class="results"`, `data-query="nginx"`, `hx-push-url="true"`, "<mark>nginx</mark>")
	mustNotContain(t, frag, "<html", "<header", "index-status")
	if push := res.Header.Get("HX-Push-Url"); push != "/?q=nginx" {
		t.Errorf("HX-Push-Url %q, want the canonical URL without empty filters", push)
	}

	// A boosted navigation or a history restore needs the whole page.
	_, boosted := get(t, s, "/?q=nginx", "HX-Request", "true", "HX-Boosted", "true")
	mustContain(t, boosted, "<!DOCTYPE html>")

	_, filtered := get(t, s, "/?q=nginx&type=config", "HX-Request", "true")
	mustContain(t, filtered, "configs/nginx/lab.conf")
	mustNotContain(t, filtered, "runbooks/nginx-reload.md")
}

func TestSearchInputNeverErrors(t *testing.T) {
	s := testSite(t, sampleLibrary)
	for _, q := range hostileQueries {
		for _, hx := range []string{"", "true"} {
			req := "/?q=" + urlQueryEscape(q)
			res, body := get(t, s, req, "HX-Request", hx)
			if res.StatusCode != http.StatusOK {
				t.Errorf("q=%q htmx=%q: status %d: %.200s", q, hx, res.StatusCode, body)
			}
			mustNotContain(t, body, "<script>alert")
		}
	}
}

func TestBadFiltersAreClientErrors(t *testing.T) {
	s := testSite(t, sampleLibrary)
	for _, target := range []string{"/?type=exe", "/?tag=Not%20A%20Tag", "/?tag=%3Cscript%3E"} {
		res, body := get(t, s, target, "HX-Request", "true")
		if res.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status %d", target, res.StatusCode)
		}
		mustNotContain(t, body, "<script>")
	}
}

func TestResultsEscapeLibraryContent(t *testing.T) {
	s := testSite(t, sampleLibrary)
	_, body := get(t, s, "/?q=alert", "HX-Request", "true")
	mustContain(t, body, "&lt;script&gt;")
	mustNotContain(t, body, "<script>alert", "<img src=x")
}

func TestDocumentViews(t *testing.T) {
	s := testSite(t, sampleLibrary)

	res, md := get(t, s, "/doc/runbooks/nginx-reload.md", "HX-Request", "true")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}
	mustContain(t, md, `<h2 id="reload">Reload</h2>`, `class="outline"`, `href="#reload"`, `class="l3" href="#after"`,
		"hide-first-h1", `href="/raw/runbooks/nginx-reload.md"`, "#nginx")
	mustNotContain(t, md, "<html")

	_, conf := get(t, s, "/doc/configs/nginx/lab.conf")
	mustContain(t, conf, "<!DOCTYPE html>", `<tr id="L3">`, `<a href="#L3">3</a>`, "server_name &lt;lab&gt;;")

	// Markdown's raw HTML is dropped by the renderer, and a heading's text is escaped.
	_, xss := get(t, s, "/doc/notes/xss.md", "HX-Request", "true")
	mustNotContain(t, xss, "<script>alert", "<img src=x")

	_, pdf := get(t, s, "/doc/manuals/ups.pdf", "HX-Request", "true")
	mustContain(t, pdf, "This file is listed, not shown.", reasonUnsupported)

	res, _ = get(t, s, "/doc/runbooks/missing.md")
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("missing document: %d", res.StatusCode)
	}
}

func TestRawDownloads(t *testing.T) {
	s := testSite(t, sampleLibrary)
	cases := []struct {
		target, contentType, disposition string
	}{
		{"/raw/runbooks/nginx-reload.md", "text/markdown; charset=utf-8", `inline; filename=nginx-reload.md`},
		{"/raw/runbooks/nginx-reload.md?download=1", "text/markdown; charset=utf-8", `attachment; filename=nginx-reload.md`},
		{"/raw/configs/nginx/lab.conf", "text/plain; charset=utf-8", `inline; filename=lab.conf`},
		{"/raw/manuals/ups.pdf", "application/pdf", `attachment; filename=ups.pdf`},
		// HTML in the library is never served as HTML.
		{"/raw/site/index.html", "application/octet-stream", `attachment; filename=index.html`},
	}
	for _, c := range cases {
		res, body := get(t, s, c.target)
		if res.StatusCode != http.StatusOK {
			t.Errorf("%s: status %d", c.target, res.StatusCode)
			continue
		}
		if got := res.Header.Get("Content-Type"); got != c.contentType {
			t.Errorf("%s: content type %q, want %q", c.target, got, c.contentType)
		}
		if got := res.Header.Get("Content-Disposition"); got != c.disposition {
			t.Errorf("%s: disposition %q, want %q", c.target, got, c.disposition)
		}
		if res.Header.Get("Content-Security-Policy") != "sandbox" || res.Header.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: missing sandbox headers", c.target)
		}
		if c.target == "/raw/configs/nginx/lab.conf" && body != sampleLibrary["configs/nginx/lab.conf"] {
			t.Errorf("raw body %q", body)
		}
	}
}

func TestRawRespectsSizeLimit(t *testing.T) {
	s := testSite(t, map[string]string{"big.txt": strings.Repeat("x", 64)}, func(o *options) { o.MaxRawBytes = 32 })
	if res, _ := get(t, s, "/raw/big.txt"); res.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("status %d", res.StatusCode)
	}
}

func TestPathTraversalIsRejected(t *testing.T) {
	tmp := t.TempDir()
	s := testSite(t, map[string]string{"ok.txt": "fine"})
	// A file beside the library, one directory up from it.
	secret := filepath.Join(filepath.Dir(s.ix.dir), "secret.txt")
	if err := os.WriteFile(secret, []byte("TOPSECRET"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "outside.txt"), []byte("TOPSECRET"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(tmp, "outside.txt"), filepath.Join(s.ix.dir, "link.txt")); err != nil {
		t.Logf("symlinks unavailable: %v", err)
	}
	attempts := []string{
		"../secret.txt",
		"..%2fsecret.txt",
		"..%2Fsecret.txt",
		"%2e%2e/secret.txt",
		"%2e%2e%2fsecret.txt",
		"ok.txt/../../secret.txt",
		"./../secret.txt",
		"..%5csecret.txt",
		"%2fetc%2fpasswd",
		"/etc/passwd",
		"link.txt",
		".hidden",
		"%2e%2e%2f%2e%2e%2f%2e%2e%2fetc%2fpasswd",
	}
	for _, prefix := range []string{"/doc/", "/raw/"} {
		for _, a := range attempts {
			res, body := get(t, s, prefix+a)
			if res.StatusCode != http.StatusNotFound {
				t.Errorf("%s%s: status %d", prefix, a, res.StatusCode)
			}
			mustNotContain(t, body, "TOPSECRET", "root:")
		}
	}
	if res, body := get(t, s, "/raw/ok.txt"); res.StatusCode != http.StatusOK || body != "fine" {
		t.Errorf("control request: %d %q", res.StatusCode, body)
	}
}

func TestRescan(t *testing.T) {
	s := testSite(t, sampleLibrary)
	if res, _ := do(t, s, http.MethodPost, "/rescan"); res.StatusCode != http.StatusBadRequest {
		t.Errorf("plain POST: %d", res.StatusCode)
	}
	writeFiles(t, s.ix.dir, map[string]string{"new/rescanned.md": "# Rescanned\n"})
	res, body := do(t, s, http.MethodPost, "/rescan", "HX-Request", "true")
	if res.StatusCode != http.StatusOK {
		t.Errorf("htmx POST: %d", res.StatusCode)
	}
	files := len(sampleLibrary) + 1
	mustContain(t, body, "Rescan now", "Last full scan", "up to date", fmt.Sprintf("<dt>Files</dt><dd>%d</dd>", files))
	mustNotContain(t, body, "<html", "scanning")
	if _, err := s.ix.Lookup(context.Background(), "new/rescanned.md"); err != nil {
		t.Errorf("rescan missed the new file: %v", err)
	}
}

func TestBasicAuth(t *testing.T) {
	s := testSite(t, sampleLibrary, func(o *options) { o.AuthUser, o.AuthPassword = "admin", "correct horse" })
	for _, target := range []string{"/", "/raw/configs/nginx/lab.conf", "/events", "/static/css/app.css", "/hyperchi/js/hx-sse.js"} {
		res, body := get(t, s, target)
		if res.StatusCode != http.StatusUnauthorized || !strings.HasPrefix(res.Header.Get("WWW-Authenticate"), "Basic ") {
			t.Errorf("%s without credentials: %d %q", target, res.StatusCode, res.Header.Get("WWW-Authenticate"))
		}
		mustNotContain(t, body, "listen 443")
	}
	cases := map[string]int{
		basic("admin", "correct horse"): http.StatusOK,
		basic("admin", "wrong"):         http.StatusUnauthorized,
		basic("root", "correct horse"):  http.StatusUnauthorized,
		"Bearer correct horse":          http.StatusUnauthorized,
	}
	for auth, want := range cases {
		if res, _ := get(t, s, "/", "Authorization", auth); res.StatusCode != want {
			t.Errorf("Authorization %q: %d, want %d", auth, res.StatusCode, want)
		}
	}
}

func TestOptionsFromEnv(t *testing.T) {
	t.Setenv("BASIC_AUTH_USER", "admin")
	t.Setenv("BASIC_AUTH_PASSWORD", "")
	if _, err := optionsFromEnv(); err == nil {
		t.Error("a user without a password was accepted")
	}
	t.Setenv("BASIC_AUTH_PASSWORD", "pw")
	t.Setenv("MAX_FILE_BYTES", "-1")
	if _, err := optionsFromEnv(); err == nil {
		t.Error("a negative size cap was accepted")
	}
	t.Setenv("MAX_FILE_BYTES", "2048")
	t.Setenv("WATCH_INTERVAL", "500ms")
	t.Setenv("LIBRARY_DIR", "/srv/docs")
	o, err := optionsFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if o.MaxFileBytes != 2048 || o.WatchInterval.String() != "500ms" || o.LibraryDir != "/srv/docs" || o.AuthUser != "admin" {
		t.Errorf("options %+v", o)
	}
}

func TestProductionConfigTakesEffect(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "prod.db")
	t.Setenv("ENVIRONMENT", "production")
	t.Setenv("DB_PATH", dbPath)
	t.Setenv("PORT", "9191")
	cfg := loadConfig()
	if !cfg.IsProduction() || cfg.Server.PortFallback || cfg.Database.Name != dbPath || cfg.Server.Port != 9191 {
		t.Fatalf("config %+v", cfg.Server)
	}
	opts := defaultOptions()
	opts.LibraryDir = t.TempDir()
	s, err := newSite(cfg, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.close(); err != nil {
			t.Error(err)
		}
	})
	if !s.app.IsProduction() {
		t.Error("app is not in production mode")
	}
	res, _ := get(t, s, "/")
	if res.Header.Get("Content-Security-Policy") == "" {
		t.Error("production responses carry no Content-Security-Policy")
	}
	if res.Header.Get("Cache-Control") != "" && strings.Contains(res.Header.Get("Cache-Control"), "public") {
		t.Errorf("page cache-control %q", res.Header.Get("Cache-Control"))
	}
	if res, _ := get(t, s, "/static/css/app.css"); res.Header.Get("Cache-Control") != "public, max-age=3600" {
		t.Errorf("static cache-control %q", res.Header.Get("Cache-Control"))
	}
	if _, err := os.Stat(dbPath); err != nil {
		t.Errorf("DB_PATH was not used: %v", err)
	}
}

func TestDevelopmentConfigDefaults(t *testing.T) {
	for _, key := range []string{"DB_PATH", "DB_NAME", "ENVIRONMENT", "GO_ENV", "PRODUCTION"} {
		t.Setenv(key, "")
	}
	cfg := loadConfig()
	if cfg.Database.Name != "homelab-search.db" || !cfg.Server.PortFallback || !cfg.IsDevelopment() || cfg.Server.Host != "0.0.0.0" {
		t.Errorf("development defaults: db=%q fallback=%v env=%q host=%q",
			cfg.Database.Name, cfg.Server.PortFallback, cfg.Server.Environment, cfg.Server.Host)
	}
}

// TestStaticAssets pins the vendored htmx build and checks the embedded
// assets and the framework's SSE extension script are served.
func TestStaticAssets(t *testing.T) {
	s := testSite(t, nil)
	res, htmx := get(t, s, "/static/js/htmx.min.js")
	sum := sha256.Sum256([]byte(htmx))
	if res.StatusCode != http.StatusOK || hex.EncodeToString(sum[:]) != "e484d9171a9db30a39c8f16e3d709d4137f3211c659f8e6125816635033d593f" {
		t.Errorf("htmx.min.js: status %d, sha256 %x", res.StatusCode, sum)
	}
	for _, target := range []string{"/static/js/hyperchi-directives.js", "/static/js/app.js", "/static/css/app.css", "/hyperchi/js/hx-sse.js"} {
		res, body := get(t, s, target)
		if res.StatusCode != http.StatusOK || len(body) < 100 {
			t.Errorf("%s: status %d, %d bytes", target, res.StatusCode, len(body))
		}
	}
	for _, target := range []string{"/static/", "/static/js/", "/static/missing.js"} {
		if res, _ := get(t, s, target); res.StatusCode != http.StatusNotFound {
			t.Errorf("%s: status %d", target, res.StatusCode)
		}
	}
}

func basic(user, password string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+password))
}

func urlQueryEscape(s string) string { return url.QueryEscape(s) }

// TestRunsFromAnyWorkingDirectory renders pages and serves assets from a
// directory with no templates/ or static/ in it: both are embedded.
func TestRunsFromAnyWorkingDirectory(t *testing.T) {
	t.Chdir(t.TempDir())
	s := testSite(t, sampleLibrary)

	res, page := get(t, s, "/?q=nginx")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("search page: %d", res.StatusCode)
	}
	mustContain(t, page, "<!DOCTYPE html>", `href="/static/css/app.css"`, "Reload the reverse proxy")
	if res, doc := get(t, s, "/doc/runbooks/nginx-reload.md"); res.StatusCode != http.StatusOK {
		t.Errorf("document page: %d", res.StatusCode)
	} else {
		mustContain(t, doc, "systemctl reload nginx")
	}
	for _, target := range []string{"/static/css/app.css", "/static/js/app.js", "/static/favicon.svg"} {
		res, body := get(t, s, target)
		if res.StatusCode != http.StatusOK || len(body) == 0 || res.Header.Get("ETag") == "" {
			t.Errorf("%s: status %d, %d bytes, ETag %q", target, res.StatusCode, len(body), res.Header.Get("ETag"))
		}
	}
}
