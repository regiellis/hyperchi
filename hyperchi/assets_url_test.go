package hyperchi

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"
)

func assetVersion(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])[:assetVersionLen]
}

// assetApp is a development app (config.Default) with no static mounts.
func assetApp(t *testing.T) *HyperChi {
	t.Helper()
	app := New()
	t.Cleanup(func() {
		if err := app.CloseDB(); err != nil {
			t.Errorf("close db: %v", err)
		}
	})
	return app
}

func writeAssetFile(t *testing.T, path, content string, mtime time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

func getAsset(app http.Handler, method, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

func TestAssetTemplateFunctionVersionsStaticFSFiles(t *testing.T) {
	app := assetApp(t)
	css := "body{color:red}"
	if err := app.StaticFS("/static/*", fstest.MapFS{
		"css/app.css":     {Data: []byte(css)},
		"img/my logo.svg": {Data: []byte("<svg/>")},
	}, StaticOptions{}); err != nil {
		t.Fatal(err)
	}

	want := "/static/css/app.css?v=" + assetVersion(css)
	got, err := app.AssetURL("/static/css/app.css")
	if err != nil || got != want {
		t.Fatalf("AssetURL = %q, %v; want %q", got, err, want)
	}

	dir := t.TempDir()
	tmpl := `{{define "head"}}<link rel="stylesheet" href="{{asset "/static/css/app.css"}}"><img src="{{asset "/static/img/my%20logo.svg"}}">{{end}}` +
		`{{define "broken"}}<link href="{{asset "/static/css/missing.css"}}">{{end}}`
	if err := os.WriteFile(filepath.Join(dir, "head.tmpl"), []byte(tmpl), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := app.LoadTemplates(filepath.Join(dir, "*.tmpl")); err != nil {
		t.Fatal(err)
	}
	html, err := app.RenderToString("head", nil)
	if err != nil {
		t.Fatal(err)
	}
	wantHTML := `<link rel="stylesheet" href="` + want + `"><img src="/static/img/my%20logo.svg?v=` + assetVersion("<svg/>") + `">`
	if string(html) != wantHTML {
		t.Fatalf("rendered\n%s\nwant\n%s", html, wantHTML)
	}
	if _, err := app.RenderToString("broken", nil); err == nil || !strings.Contains(err.Error(), "missing.css") {
		t.Fatalf("a missing asset rendered without error: %v", err)
	}
}

func TestAssetURLRejectsHostilePaths(t *testing.T) {
	app := assetApp(t)
	fsys := fstest.MapFS{
		"css/app.css":        {Data: []byte("a")},
		"css/.hidden.css":    {Data: []byte("h")},
		".env":               {Data: []byte("SECRET=1")},
		"secret.txt/inner":   {Data: []byte("x")},
		"css/app.css.backup": {Data: []byte("b")},
	}
	if err := app.StaticFS("/static/*", fsys, StaticOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{
		"/static/../go.mod",
		"/static/%2e%2e/go.mod",
		"/static/css/../../go.mod",
		"/static/css/%2E%2E/%2E%2E/go.mod",
		"/static/.env",
		"/static/%2eenv",
		"/static/css/.hidden.css",
		"/static/css",
		"/static/css/",
		"/static/",
		"/static",
		"/static//css/app.css",
		"/static/./css/app.css",
		"/static/css\\app.css",
		"/static/css%5Capp.css",
		"/static/css/app.css?v=1",
		"/static/css/app.css#top",
		"/static/%zz",
		"/static/css/missing.css",
		"static/css/app.css",
		"/other/app.css",
		"",
	} {
		if got, err := app.AssetURL(p); err == nil {
			t.Errorf("AssetURL(%q) = %q, want an error", p, got)
		}
	}

	// A root mount must not let a protocol-relative URL through.
	root := assetApp(t)
	if err := root.StaticFS("/*", fstest.MapFS{"app.css": {Data: []byte("a")}}, StaticOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"//evil.example/app.css", "/../app.css", "/%2F%2Fevil.example/app.css"} {
		if got, err := root.AssetURL(p); err == nil {
			t.Errorf("root mount: AssetURL(%q) = %q, want an error", p, got)
		}
	}
	if got, err := root.AssetURL("/app.css"); err != nil || got != "/app.css?v="+assetVersion("a") {
		t.Errorf("root mount: AssetURL(/app.css) = %q, %v", got, err)
	}
}

// A disk mount hashes through os.Root, so a symlink out of the directory is
// refused for versioning as it is for serving.
func TestAssetURLRefusesSymlinkEscape(t *testing.T) {
	dir := t.TempDir()
	public := filepath.Join(dir, "public")
	writeAssetFile(t, filepath.Join(public, "app.css"), "a", time.Now())
	writeAssetFile(t, filepath.Join(dir, "secret.txt"), "secret", time.Now())
	if err := os.Symlink(filepath.Join(dir, "secret.txt"), filepath.Join(public, "leak.css")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	app := assetApp(t)
	if err := app.StaticWithOptions("/assets/*", public, StaticOptions{}); err != nil {
		t.Fatal(err)
	}
	if got, err := app.AssetURL("/assets/leak.css"); err == nil {
		t.Errorf("AssetURL followed a symlink out of the root: %q", got)
	}
	if rec := getAsset(app, http.MethodGet, "/assets/leak.css"); rec.Code != http.StatusNotFound {
		t.Errorf("GET leak.css = %d, want 404", rec.Code)
	}
	if _, err := app.AssetURL("/assets/app.css"); err != nil {
		t.Errorf("regular file: %v", err)
	}
}

// In development a disk file is re-checked on every call, so an edit (here a
// same-size rewrite with a new modification time) changes the version.
func TestAssetURLRehashesEditedFileInDevelopment(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "app.css")
	start := time.Now().Add(-time.Hour).Truncate(time.Second)
	writeAssetFile(t, file, "aaaa", start)
	app := assetApp(t)
	if err := app.StaticWithOptions("/static/*", dir, StaticOptions{Development: true}); err != nil {
		t.Fatal(err)
	}
	first, err := app.AssetURL("/static/app.css")
	if err != nil || first != "/static/app.css?v="+assetVersion("aaaa") {
		t.Fatalf("first = %q, %v", first, err)
	}
	writeAssetFile(t, file, "bbbb", start.Add(time.Minute))
	second, err := app.AssetURL("/static/app.css")
	if err != nil || second != "/static/app.css?v="+assetVersion("bbbb") {
		t.Fatalf("after edit = %q, %v", second, err)
	}
	// Development always revalidates, even with the current version.
	if rec := getAsset(app, http.MethodGet, second); rec.Header().Get("Cache-Control") != "no-cache" || rec.Body.String() != "bbbb" {
		t.Errorf("development versioned GET = %q %q", rec.Header().Get("Cache-Control"), rec.Body.String())
	}
}

// In production a version is computed once. A changed file keeps its old
// version until the static handler sees the new content, which then updates
// the shared cache.
func TestAssetURLCachesInProduction(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "app.css")
	start := time.Now().Add(-time.Hour).Truncate(time.Second)
	writeAssetFile(t, file, "old!", start)
	app := productionTestApp(t)
	if err := app.StaticWithOptions("/static/*", dir, StaticOptions{}); err != nil {
		t.Fatal(err)
	}
	oldURL, err := app.AssetURL("/static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	if rec := getAsset(app, http.MethodGet, oldURL); rec.Header().Get("Cache-Control") != staticImmutableCache {
		t.Fatalf("current version Cache-Control = %q", rec.Header().Get("Cache-Control"))
	}

	writeAssetFile(t, file, "new!", start.Add(time.Minute))
	if again, err := app.AssetURL("/static/app.css"); err != nil || again != oldURL {
		t.Fatalf("production re-hashed without a request: %q, %v", again, err)
	}
	stale := getAsset(app, http.MethodGet, oldURL)
	if stale.Body.String() != "new!" || stale.Header().Get("Cache-Control") != staticDefaultCache {
		t.Fatalf("stale version = %q %q, want the new body under the normal policy", stale.Body.String(), stale.Header().Get("Cache-Control"))
	}
	newURL, err := app.AssetURL("/static/app.css")
	if err != nil || newURL != "/static/app.css?v="+assetVersion("new!") {
		t.Fatalf("after the handler saw the change: %q, %v", newURL, err)
	}
}

func TestStaticVersionedRequestsAreImmutable(t *testing.T) {
	dir := t.TempDir()
	writeAssetFile(t, filepath.Join(dir, "site.css"), "disk", time.Now().Add(-time.Hour))
	fsys := fstest.MapFS{"site.css": {Data: []byte("embedded")}}

	app := assetApp(t)
	mounts := []struct {
		prefix string
		mount  func(pattern string, options StaticOptions) error
	}{
		{"/disk/", func(p string, o StaticOptions) error { return app.StaticWithOptions(p, dir, o) }},
		{"/embed/", func(p string, o StaticOptions) error { return app.StaticFS(p, fsys, o) }},
	}
	for _, m := range mounts {
		for name, options := range map[string]StaticOptions{
			"plain":  {},
			"custom": {CacheControl: "public, max-age=60"},
			"dev":    {Development: true},
			"policy": {CachePolicy: func(*http.Request) string { return "private" }},
		} {
			if err := m.mount(m.prefix+name+"/*", options); err != nil {
				t.Fatal(err)
			}
		}
	}

	for _, m := range mounts {
		versioned, err := app.AssetURL(m.prefix + "plain/site.css")
		if err != nil {
			t.Fatal(err)
		}
		v := strings.TrimPrefix(versioned, m.prefix+"plain/site.css?v=")
		wrong := strings.Repeat("0", assetVersionLen)
		cases := []struct {
			target, want string
		}{
			{m.prefix + "plain/site.css", staticDefaultCache},
			{m.prefix + "plain/site.css?v=" + v, staticImmutableCache},
			{m.prefix + "plain/site.css?v=" + wrong, staticDefaultCache},
			{m.prefix + "plain/site.css?v=" + v + "0", staticDefaultCache},
			{m.prefix + "plain/site.css?v=" + strings.ToUpper(v), staticDefaultCache},
			{m.prefix + "plain/site.css?v=&v=" + v, staticDefaultCache},
			{m.prefix + "custom/site.css", "public, max-age=60"},
			{m.prefix + "custom/site.css?v=" + v, staticImmutableCache},
			{m.prefix + "dev/site.css?v=" + v, "no-cache"},
			{m.prefix + "policy/site.css?v=" + v, "private"},
		}
		for _, tc := range cases {
			for _, method := range []string{http.MethodGet, http.MethodHead} {
				rec := getAsset(app, method, tc.target)
				if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != tc.want {
					t.Errorf("%s %s = %d %q, want %q", method, tc.target, rec.Code, rec.Header().Get("Cache-Control"), tc.want)
				}
			}
		}
	}

	// A miss stays uncacheable whatever ?v= says.
	if rec := getAsset(app, http.MethodGet, "/embed/plain/missing.css?v="+strings.Repeat("a", assetVersionLen)); rec.Code != http.StatusNotFound || rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("miss = %d %q", rec.Code, rec.Header().Get("Cache-Control"))
	}
}

// The standalone handlers honor the version too, with the same hash.
func TestStandaloneStaticHandlerHonorsVersion(t *testing.T) {
	h := NewStaticFSHandler(fstest.MapFS{"app.js": {Data: []byte("go()")}}, StaticOptions{})
	rec := getAsset(h, http.MethodGet, "/app.js?v="+assetVersion("go()"))
	if rec.Header().Get("Cache-Control") != staticImmutableCache {
		t.Errorf("standalone versioned = %q", rec.Header().Get("Cache-Control"))
	}
}

func TestAssetURLPrefersLongestMount(t *testing.T) {
	app := assetApp(t)
	if err := app.StaticFS("/static/*", fstest.MapFS{"vendor/lib.js": {Data: []byte("outer")}}, StaticOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := app.StaticFS("/static/vendor/*", fstest.MapFS{"lib.js": {Data: []byte("inner")}}, StaticOptions{}); err != nil {
		t.Fatal(err)
	}
	got, err := app.AssetURL("/static/vendor/lib.js")
	if err != nil || got != "/static/vendor/lib.js?v="+assetVersion("inner") {
		t.Fatalf("AssetURL = %q, %v; want the inner mount's version", got, err)
	}
	if rec := getAsset(app, http.MethodGet, got); rec.Body.String() != "inner" || rec.Header().Get("Cache-Control") != staticImmutableCache {
		t.Errorf("served %q %q", rec.Body.String(), rec.Header().Get("Cache-Control"))
	}
}

// A static mount inside a Group serves and versions under the group's full
// prefix. chi leaves r.URL.Path whole for sub-routers, so stripping the
// mount's own prefix alone would answer 404 for every file.
func TestGroupStaticMountServesAndResolves(t *testing.T) {
	app := assetApp(t)
	dir := t.TempDir()
	writeAssetFile(t, filepath.Join(dir, "css", "app.css"), "disk", time.Now().Add(-time.Hour))
	app.Group("/admin", func(g *HyperChi) {
		if err := g.StaticFS("/static/*", fstest.MapFS{"app.css": {Data: []byte("a")}}, StaticOptions{}); err != nil {
			t.Fatal(err)
		}
		g.Group("/v2/", func(inner *HyperChi) {
			if err := inner.StaticWithOptions("/files/*", dir, StaticOptions{}); err != nil {
				t.Fatal(err)
			}
		})
	})

	for target, want := range map[string]string{
		"/admin/static/app.css":       "a",
		"/admin/v2/files/css/app.css": "disk",
	} {
		rec := getAsset(app, http.MethodGet, target)
		if rec.Code != http.StatusOK || rec.Body.String() != want {
			t.Errorf("GET %s = %d %q, want 200 %q", target, rec.Code, rec.Body.String(), want)
		}
		if rec := getAsset(app, http.MethodHead, target); rec.Code != http.StatusOK {
			t.Errorf("HEAD %s = %d", target, rec.Code)
		}
	}
	for _, miss := range []string{"/admin/static/missing.css", "/admin/static/", "/admin/static/.env", "/admin/v2/files/css/", "/static/app.css"} {
		if rec := getAsset(app, http.MethodGet, miss); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", miss, rec.Code)
		}
	}

	got, err := app.AssetURL("/admin/static/app.css")
	if err != nil || got != "/admin/static/app.css?v="+assetVersion("a") {
		t.Fatalf("AssetURL = %q, %v", got, err)
	}
	if rec := getAsset(app, http.MethodGet, got); rec.Body.String() != "a" || rec.Header().Get("Cache-Control") != staticImmutableCache {
		t.Errorf("versioned group asset: %q %q", rec.Body.String(), rec.Header().Get("Cache-Control"))
	}
	if got, err := app.AssetURL("/admin/v2/files/css/app.css"); err != nil || got != "/admin/v2/files/css/app.css?v="+assetVersion("disk") {
		t.Errorf("nested group AssetURL = %q, %v", got, err)
	}
	for _, p := range []string{"/static/app.css", "/admin/static/missing.css"} {
		if got, err := app.AssetURL(p); err == nil {
			t.Errorf("AssetURL(%q) = %q, want an error", p, got)
		}
	}
}

// The handler strips a literal prefix, so a group with a URL parameter cannot
// hold a static mount; it is refused at setup rather than 404ing forever.
func TestGroupStaticMountRefusesParameterPrefix(t *testing.T) {
	app := assetApp(t)
	app.Group("/u/{id}", func(g *HyperChi) {
		if err := g.StaticFS("/static/*", fstest.MapFS{"a.css": {Data: []byte("a")}}, StaticOptions{}); err == nil {
			t.Error("StaticFS under a parameter group succeeded, want an error")
		}
	})
}

func TestAssetURLConcurrentWithRequests(t *testing.T) {
	dir := t.TempDir()
	writeAssetFile(t, filepath.Join(dir, "app.css"), strings.Repeat("c", 4096), time.Now().Add(-time.Hour))
	app := assetApp(t)
	if err := app.StaticWithOptions("/static/*", dir, StaticOptions{}); err != nil {
		t.Fatal(err)
	}
	want := "/static/app.css?v=" + assetVersion(strings.Repeat("c", 4096))
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			for range 20 {
				got, err := app.AssetURL("/static/app.css")
				if err != nil || got != want {
					t.Errorf("AssetURL = %q, %v", got, err)
					return
				}
				if rec := getAsset(app, http.MethodGet, want); rec.Header().Get("Cache-Control") != staticImmutableCache {
					t.Errorf("Cache-Control = %q", rec.Header().Get("Cache-Control"))
					return
				}
			}
		})
	}
	wg.Wait()
}
