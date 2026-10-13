package hyperchi

import (
	"bytes"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/regiellis/hyperchi/hyperchi/content"
)

func writeContentFile(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// contentApp returns an app in development (the default) or production that
// is closed when the test ends.
func contentApp(t *testing.T, production bool) *HyperChi {
	t.Helper()
	app := noDBApp(t)
	if production {
		app.Configure().Env("production")
	}
	t.Cleanup(func() {
		if err := app.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})
	return app
}

// postSchema requires a title and an order of at least 1, and types "date".
func postSchema() *content.ContentSchema {
	return content.NewSchema("post").
		Field("title", content.FieldTypeString).Required().
		Field("order", content.FieldTypeInt).Min(1).
		Field("date", content.FieldTypeTime).
		Build().
		Build()
}

func TestLoadDirLoadsNestedCollections(t *testing.T) {
	root := t.TempDir()
	writeContentFile(t, root, "blog/hello.md", "---\n"+
		"title: Hello\n"+
		"description: First post\n"+
		"draft: false\n"+
		"order: 3\n"+
		"rating: 4.5\n"+
		"zip: \"02134\"\n"+
		"tags: [go, htmx]\n"+
		"---\n# Hi\n\nbody text")
	writeContentFile(t, root, "blog/2024/recap.md", "---\ntitle: Recap\n---\nrecap")
	writeContentFile(t, root, "pages/about.html", "<p>about</p>")
	writeContentFile(t, root, "notes/plain.txt", "<b>raw</b>")
	writeContentFile(t, root, "stray.md", "no collection")
	writeContentFile(t, root, "blog/cover.png", "not content")

	app := contentApp(t, true)
	if err := app.Content.LoadDir(root); err != nil {
		t.Fatal(err)
	}
	if n := app.Content.collectionCount(); n != 3 {
		t.Fatalf("collections = %d, want blog, pages, notes", n)
	}

	hello, err := app.Content.Get("blog", "hello")
	if err != nil {
		t.Fatal(err)
	}
	if hello.Title != "Hello" || hello.Description != "First post" {
		t.Errorf("title/description = %q/%q", hello.Title, hello.Description)
	}
	if hello.Path != filepath.Join(root, "blog", "hello.md") {
		t.Errorf("path = %q", hello.Path)
	}
	if !strings.Contains(string(hello.HTML), "<h1>Hi</h1>") || !strings.Contains(string(hello.HTML), "<p>body text</p>") {
		t.Errorf("HTML = %q", hello.HTML)
	}
	if strings.Contains(hello.Content, "title:") || !strings.Contains(hello.Content, "body text") {
		t.Errorf("Content should be the body without front matter: %q", hello.Content)
	}
	for _, stamp := range []string{hello.CreatedAt, hello.UpdatedAt} {
		if _, err := time.Parse(time.RFC3339, stamp); err != nil {
			t.Errorf("date %q is not RFC 3339: %v", stamp, err)
		}
	}
	want := map[string]any{"draft": false, "order": 3, "rating": 4.5, "zip": "02134", "title": "Hello"}
	for key, value := range want {
		if got := hello.Metadata[key]; got != value {
			t.Errorf("Metadata[%q] = %#v (%T), want %#v (%T)", key, got, got, value, value)
		}
	}
	if tags, ok := hello.Metadata["tags"].([]string); !ok || strings.Join(tags, ",") != "go,htmx" {
		t.Errorf("tags = %#v", hello.Metadata["tags"])
	}

	// A file in a subdirectory joins its collection under its file name.
	recap, err := app.Content.Get("blog", "recap")
	if err != nil || recap.Title != "Recap" {
		t.Fatalf("nested recap = %+v, %v", recap, err)
	}
	if about, err := app.Content.Get("pages", "about"); err != nil || about.HTML != "<p>about</p>" {
		t.Errorf("about = %+v, %v", about, err)
	}
	if plain, err := app.Content.Get("notes", "plain"); err != nil || plain.HTML != "<pre>&lt;b&gt;raw&lt;/b&gt;</pre>" {
		t.Errorf("plain = %+v, %v", plain, err)
	}
	if _, err := app.Content.Get("stray", "stray"); err == nil {
		t.Error("a file directly under the directory was loaded")
	}

	// Items are ordered by path: blog/2024/recap before blog/hello.
	items, err := app.Content.Query("blog").All()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].Slug != "recap" || items[1].Slug != "hello" {
		t.Fatalf("blog items = %v", slugList(items))
	}
}

func slugList(items []*ContentItem) []string {
	out := make([]string, len(items))
	for i, item := range items {
		out[i] = item.Slug
	}
	return out
}

func TestLoadDirListsEveryFailureAndLoadsValidFiles(t *testing.T) {
	root := t.TempDir()
	writeContentFile(t, root, "blog/good.md", "---\ntitle: Good\norder: 2\ndate: 2024-03-01\n---\nok")
	writeContentFile(t, root, "blog/untitled.md", "---\norder: 2\n---\nno title")
	writeContentFile(t, root, "blog/zero.md", "---\ntitle: Zero\norder: 0\n---\nbad order")
	writeContentFile(t, root, "blog/2023/good.md", "---\ntitle: Again\norder: 1\n---\nsame slug")
	writeContentFile(t, root, "pages/about.md", "no schema here")

	app := contentApp(t, true)
	err := app.Content.LoadDir(root, WithContentCollection(content.CollectionConfig{Name: "blog", Schema: postSchema()}))
	var loadErr *ContentLoadError
	if !errors.As(err, &loadErr) {
		t.Fatalf("LoadDir = %v, want a *ContentLoadError", err)
	}
	if loadErr.Source != root || len(loadErr.Errs) != 3 {
		t.Fatalf("failures = %d %v, want 3", len(loadErr.Errs), loadErr.Errs)
	}
	msg := err.Error()
	for _, name := range []string{"untitled.md", "zero.md", filepath.Join("2023", "good.md")} {
		if !strings.Contains(msg, name) {
			t.Errorf("error does not name %s:\n%s", name, msg)
		}
	}
	if !strings.Contains(msg, "already used by "+filepath.Join(root, "blog", "good.md")) {
		t.Errorf("the duplicate slug is not reported:\n%s", msg)
	}

	// The valid files loaded; the failures did not.
	good, err := app.Content.Get("blog", "good")
	if err != nil || good.Title != "Good" {
		t.Fatalf("good = %+v, %v", good, err)
	}
	if date, ok := good.Metadata["date"].(time.Time); !ok || !date.Equal(time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("a time-typed schema field should load as time.Time, got %#v", good.Metadata["date"])
	}
	for _, slug := range []string{"untitled", "zero"} {
		if _, err := app.Content.Get("blog", slug); err == nil {
			t.Errorf("%s failed its schema but was loaded", slug)
		}
	}
	if _, err := app.Content.Get("pages", "about"); err != nil {
		t.Errorf("a collection without failures should load: %v", err)
	}
}

func TestLoadDirRejectsMissingDirectory(t *testing.T) {
	app := contentApp(t, true)
	if err := app.Content.LoadDir(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("a missing directory loaded without error")
	}
	file := filepath.Join(t.TempDir(), "file.md")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := app.Content.LoadDir(file); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("LoadDir(file) = %v", err)
	}
}

func TestLoadDirWithMarkdownRenderer(t *testing.T) {
	root := t.TempDir()
	writeContentFile(t, root, "docs/a.md", "see [link](/x)")
	writeContentFile(t, root, "docs/b.md", "FAIL")
	app := contentApp(t, true)
	err := app.Content.LoadDir(root, WithMarkdown(func(body string) (template.HTML, error) {
		if body == "FAIL" {
			return "", errors.New("cannot render")
		}
		return template.HTML(`<p><a href="/x">link</a></p>`), nil
	}))
	if err == nil || !strings.Contains(err.Error(), "cannot render") {
		t.Fatalf("LoadDir = %v, want the render failure", err)
	}
	if a, err := app.Content.Get("docs", "a"); err != nil || a.HTML != `<p><a href="/x">link</a></p>` {
		t.Errorf("a = %+v, %v", a, err)
	}
	if _, err := app.Content.Get("docs", "b"); err == nil {
		t.Error("a file the renderer failed on was loaded")
	}
}

func TestQueryOverLoadedContent(t *testing.T) {
	root := t.TempDir()
	writeContentFile(t, root, "blog/a.md", "---\ntitle: Alpha\ndraft: false\nposition: b\n---\na")
	writeContentFile(t, root, "blog/b.md", "---\ntitle: Beta\ndraft: true\nposition: a\n---\nb")
	writeContentFile(t, root, "blog/c.md", "---\ntitle: Gamma\ndraft: false\nposition: c\n---\nc")
	app := contentApp(t, true)
	if err := app.Content.LoadDir(root); err != nil {
		t.Fatal(err)
	}
	published, err := app.Content.Query("blog").Where("draft", false).SortBy("position", "desc").All()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(slugList(published), ","); got != "c,a" {
		t.Errorf("published = %s, want c,a", got)
	}
	first, err := app.Content.Query("blog").SortBy("position", "asc").First()
	if err != nil || first.Slug != "b" {
		t.Errorf("first = %+v, %v", first, err)
	}
}

func TestContentRoutesServeLoadedContent(t *testing.T) {
	root := t.TempDir()
	writeContentFile(t, root, "blog/hello.md", "---\ntitle: Hello <there>\ndraft: false\n---\n# Heading")
	writeContentFile(t, root, "blog/draft.md", "---\ntitle: Draft\ndraft: true\n---\nhidden")
	app := contentApp(t, true)
	templates := fstest.MapFS{
		"t/index.tmpl": {Data: []byte(`{{define "blog/index"}}<ul>{{range .items}}<li>{{.Title}}</li>{{end}}</ul>{{end}}`)},
		"t/item.tmpl":  {Data: []byte(`{{define "blog/item"}}<h1>{{.item.Title}}</h1>{{.item.HTML}}{{end}}`)},
	}
	if err := app.LoadTemplatesFS(templates, "t/*.tmpl"); err != nil {
		t.Fatal(err)
	}
	if err := app.Content.LoadDir(root); err != nil {
		t.Fatal(err)
	}
	app.ContentRoutes("/blog", "blog", ContentRouteConfig{DefaultFilters: map[string]any{"draft": false}})

	get := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("HX-Request", "true")
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		return rec
	}
	if rec := get("/blog"); rec.Code != http.StatusOK || rec.Body.String() != "<ul><li>Hello &lt;there&gt;</li></ul>" {
		t.Errorf("index = %d %q", rec.Code, rec.Body.String())
	}
	if rec := get("/blog/hello"); rec.Code != http.StatusOK || rec.Body.String() != "<h1>Hello &lt;there&gt;</h1><h1>Heading</h1>\n" {
		t.Errorf("item = %d %q", rec.Code, rec.Body.String())
	}
	if rec := get("/blog/missing"); rec.Code != http.StatusNotFound {
		t.Errorf("missing item = %d, want 404", rec.Code)
	}
}

func TestLoadFSLoadsEmbeddedContent(t *testing.T) {
	embedded := fstest.MapFS{
		"site/content/blog/hello.md":     {Data: []byte("---\ntitle: Hello\norder: 1\n---\nhi")},
		"site/content/blog/bad.md":       {Data: []byte("---\norder: 1\n---\nno title")},
		"site/content/pages/about.html":  {Data: []byte("<p>about</p>")},
		"site/content/pages/deep/faq.md": {Data: []byte("---\ntitle: FAQ\n---\nq")},
	}
	app := contentApp(t, false) // development: an FS is still loaded once
	err := app.Content.LoadFS(embedded, "site/content",
		WithContentCollection(content.CollectionConfig{Name: "blog", Schema: postSchema()}))
	var loadErr *ContentLoadError
	if !errors.As(err, &loadErr) || len(loadErr.Errs) != 1 || !strings.Contains(err.Error(), "site/content/blog/bad.md") {
		t.Fatalf("LoadFS = %v, want one failure for bad.md", err)
	}
	hello, err := app.Content.Get("blog", "hello")
	if err != nil || hello.Path != "site/content/blog/hello.md" || hello.Title != "Hello" {
		t.Fatalf("hello = %+v, %v", hello, err)
	}
	if hello.CreatedAt != "" || hello.UpdatedAt != "" {
		t.Errorf("a file without a modification time got dates %q/%q", hello.CreatedAt, hello.UpdatedAt)
	}
	if faq, err := app.Content.Get("pages", "faq"); err != nil || faq.Title != "FAQ" {
		t.Errorf("faq = %+v, %v", faq, err)
	}
	if len(app.Integrations().List()) != 0 {
		t.Error("LoadFS registered a watcher")
	}

	// fs.Sub and an empty root read the same tree.
	sub, err := fs.Sub(embedded, "site/content")
	if err != nil {
		t.Fatal(err)
	}
	other := contentApp(t, true)
	if err := other.Content.LoadFS(sub, ""); err != nil {
		t.Fatal(err)
	}
	if about, err := other.Content.Get("pages", "about"); err != nil || about.Path != "pages/about.html" {
		t.Errorf("about = %+v, %v", about, err)
	}

	if err := other.Content.LoadFS(embedded, "nope"); err == nil {
		t.Error("a missing root loaded without error")
	}
	if err := other.Content.LoadFS(nil, "."); err == nil {
		t.Error("a nil FS loaded without error")
	}
}

// syncBuffer is a log destination the watcher goroutine and the test share.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// waitForContent consumes content:loaded events until ok reports true. The
// timeout only bounds a broken run; a passing run waits for events, never a
// fixed delay.
func waitForContent(t *testing.T, events <-chan map[string]any, what string, ok func() bool) map[string]any {
	t.Helper()
	timeout := time.After(10 * time.Second)
	for {
		select {
		case event := <-events:
			if ok() {
				return event
			}
		case <-timeout:
			t.Fatalf("no reload delivered %s", what)
			return nil
		}
	}
}

func subscribeContentLoaded(app *HyperChi) <-chan map[string]any {
	events := make(chan map[string]any, 64)
	app.Events.On("content:loaded", func(data any) {
		if event, ok := data.(map[string]any); ok {
			events <- event
		}
	})
	return events
}

func TestLoadDirReloadsInDevelopment(t *testing.T) {
	root := t.TempDir()
	writeContentFile(t, root, "blog/a.md", "---\ntitle: A\norder: 1\n---\nv1")
	writeContentFile(t, root, "pages/about.md", "about")

	app := contentApp(t, false)
	logs := &syncBuffer{}
	app.Helpers.Logger.SetOutput(logs)
	err := app.Content.LoadDir(root,
		WithContentWatchInterval(5*time.Millisecond),
		WithContentCollection(content.CollectionConfig{Name: "blog", Schema: postSchema()}))
	if err != nil {
		t.Fatal(err)
	}
	events := subscribeContentLoaded(app)
	title := func(slug string) string {
		item, err := app.Content.Get("blog", slug)
		if err != nil {
			return ""
		}
		return item.Title
	}

	// An edit is served after the reload, and the cached item is replaced.
	if title("a") != "A" {
		t.Fatal("initial load missing")
	}
	writeContentFile(t, root, "blog/a.md", "---\ntitle: A, second version\norder: 1\n---\nv2")
	waitForContent(t, events, "the edit", func() bool { return title("a") == "A, second version" })

	// A new file appears.
	writeContentFile(t, root, "blog/b.md", "---\ntitle: B\norder: 2\n---\nb")
	waitForContent(t, events, "the new file", func() bool { return title("b") == "B" })

	// A file that breaks its schema is logged and left out; the rest stays.
	writeContentFile(t, root, "blog/b.md", "---\norder: 2\n---\nlost its title")
	event := waitForContent(t, events, "the schema failure", func() bool { return title("b") == "" })
	if event["error"] == nil {
		t.Error("the content:loaded event carries no error")
	}
	if title("a") != "A, second version" {
		t.Error("a valid file disappeared when another failed")
	}
	if logged := logs.String(); !strings.Contains(logged, "content: reload") || !strings.Contains(logged, "b.md") {
		t.Errorf("reload failure not logged: %q", logged)
	}

	// Removing a collection's directory removes the collection.
	if err := os.RemoveAll(filepath.Join(root, "pages")); err != nil {
		t.Fatal(err)
	}
	waitForContent(t, events, "the removed collection", func() bool {
		_, err := app.Content.Get("pages", "about")
		return err != nil && strings.Contains(err.Error(), "collection pages not found")
	})
}

// waitForWatchers waits until want watcher goroutines are running. A watcher
// starts on its own goroutine, which may not have been scheduled yet when
// LoadDir returns, so this yields until it shows up or a deadline passes.
func waitForWatchers(t *testing.T, want int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		n := watchersRunning()
		if n == want {
			return
		}
		if n > want || time.Now().After(deadline) {
			t.Fatalf("watchers running = %d, want %d", n, want)
		}
		runtime.Gosched()
	}
}

func watchersRunning() int {
	buf := make([]byte, 1<<20)
	n := runtime.Stack(buf, true)
	return strings.Count(string(buf[:n]), "content.(*FileWatcher).Watch(")
}

func TestContentWatcherStopsOnClose(t *testing.T) {
	if n := watchersRunning(); n != 0 {
		t.Fatalf("%d watchers left running by an earlier test", n)
	}
	root := t.TempDir()
	writeContentFile(t, root, "blog/a.md", "a")

	app := contentApp(t, false) // the cleanup's second Close is a no-op
	logs := &syncBuffer{}
	app.Helpers.Logger.SetOutput(logs)
	if err := app.Content.LoadDir(root, WithContentWatchInterval(time.Hour)); err != nil {
		t.Fatal(err)
	}
	// Loading the same directory again replaces the first watcher.
	if err := app.Content.LoadDir(root, WithContentWatchInterval(time.Hour)); err != nil {
		t.Fatal(err)
	}
	waitForWatchers(t, 1)
	infos := app.Integrations().List()
	if len(infos) != 1 || infos[0].Name != contentIntegrationName || !infos[0].Lifecycle {
		t.Fatalf("integrations = %+v", infos)
	}

	if err := app.Close(); err != nil {
		t.Fatal(err)
	}
	// Close waits for the watcher goroutine to return.
	if n := watchersRunning(); n != 0 {
		t.Fatalf("watchers running after Close = %d", n)
	}

	// After Close, LoadDir still loads but starts no watcher.
	writeContentFile(t, root, "blog/b.md", "b")
	if err := app.Content.LoadDir(root); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Content.Get("blog", "b"); err != nil {
		t.Error(err)
	}
	if n := watchersRunning(); n != 0 {
		t.Fatalf("LoadDir after Close started %d watchers", n)
	}
	if !strings.Contains(logs.String(), "not watching") {
		t.Errorf("not-watching notice missing: %q", logs.String())
	}
}

func TestLoadDirLoadsOnceInProduction(t *testing.T) {
	root := t.TempDir()
	writeContentFile(t, root, "blog/a.md", "a")
	app := contentApp(t, true)
	if err := app.Content.LoadDir(root, WithContentWatchInterval(time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if n := watchersRunning(); n != 0 {
		t.Fatalf("production started %d watchers", n)
	}
	if len(app.Integrations().List()) != 0 {
		t.Error("production registered a watcher integration")
	}
}

// Readers running during reloads see a whole collection, old or new, never a
// mix or a missing one. Run with -race.
func TestContentReloadSwapsAtomically(t *testing.T) {
	const files = 5
	root := t.TempDir()
	writeVersion := func(v int) {
		for i := range files {
			writeContentFile(t, root, fmt.Sprintf("posts/p%d.md", i), fmt.Sprintf("---\ntitle: P%d\nversion: %d\n---\nbody", i, v))
		}
	}
	writeVersion(0)
	app := contentApp(t, true)
	if err := app.Content.LoadDir(root); err != nil {
		t.Fatal(err)
	}

	var stop atomic.Bool
	var wg sync.WaitGroup
	problems := make(chan string, 8)
	report := func(format string, args ...any) {
		select {
		case problems <- fmt.Sprintf(format, args...):
		default:
		}
	}
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				items, err := app.Content.Query("posts").All()
				if err != nil || len(items) != files {
					report("query saw %d items, %v", len(items), err)
					continue
				}
				for _, item := range items[1:] {
					if item.Metadata["version"] != items[0].Metadata["version"] {
						report("query mixed versions %v and %v", items[0].Metadata["version"], item.Metadata["version"])
					}
				}
				if _, err := app.Content.Get("posts", "p3"); err != nil {
					report("get: %v", err)
				}
			}
		}()
	}
	for v := 1; v <= 25; v++ {
		writeVersion(v)
		if err := app.Content.LoadDir(root); err != nil {
			t.Fatal(err)
		}
	}
	stop.Store(true)
	wg.Wait()
	close(problems)
	for p := range problems {
		t.Error(p)
	}
	item, err := app.Content.Get("posts", "p0")
	if err != nil || item.Metadata["version"] != 25 {
		t.Fatalf("final = %+v, %v", item, err)
	}
}

// A file whose front matter does not parse is reported with that error, even
// in a collection whose schema the empty placeholder would fail.
func TestLoadFSReportsFrontMatterErrorsUnderASchema(t *testing.T) {
	app := New()
	fsys := fstest.MapFS{
		"content/pages/about.md": {Data: []byte("---\ntitle: A\ndescription: d\nlist:\n  - one\n---\nx\n")},
		"content/pages/ok.md":    {Data: []byte("---\ntitle: B\ndescription: d\n---\ny\n")},
	}
	schema := &content.ContentSchema{Name: "pages", Required: []string{"title", "description"}}
	err := app.Content.LoadFS(fsys, "content", WithContentCollection(content.CollectionConfig{Name: "pages", Schema: schema}))
	if err == nil || !strings.Contains(err.Error(), `"- one" is not a "key: value" line`) {
		t.Fatalf("err = %v, want the front matter error", err)
	}
	if _, err := app.Content.Get("pages", "ok"); err != nil {
		t.Fatalf("the valid file did not load: %v", err)
	}
}
