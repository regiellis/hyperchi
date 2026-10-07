package main

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/regiellis/hyperchi/hyperchi"
	"github.com/regiellis/hyperchi/hyperchi/config"
)

const testOrigin = "https://hyperchi.example"

func testDocs(t *testing.T) *docSet {
	t.Helper()
	slots, err := docSlots()
	if err != nil {
		t.Fatal(err)
	}
	docs, err := loadDocs(docsFiles, slots)
	if err != nil {
		t.Fatal(err)
	}
	return docs
}

func docsApp(t *testing.T, origin string) *hyperchi.HyperChi {
	t.Helper()
	cfg := config.Production()
	cfg.Database.Name = filepath.Join(t.TempDir(), "docs.db")
	app := hyperchi.NewWithConfig(cfg)
	t.Cleanup(func() {
		if err := app.CloseDB(); err != nil {
			t.Error(err)
		}
	})
	if err := app.LoadTemplates("templates/*.tmpl"); err != nil {
		t.Fatal(err)
	}
	if err := setupRoutes(app, origin); err != nil {
		t.Fatal(err)
	}
	return app
}

func get(t *testing.T, app http.Handler, target string, htmx bool) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, target, nil)
	if htmx {
		r.Header.Set("HX-Request", "true")
	}
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	return w
}

func TestEveryDocsTopicRenders(t *testing.T) {
	docs := testDocs(t)
	files, err := fs.Glob(docsFiles, "docs/*.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(docs.pages) != len(files) {
		t.Fatalf("got %d topics from %d files", len(docs.pages), len(files))
	}
	app := docsApp(t, testOrigin)
	for _, page := range docs.pages {
		t.Run(page.Slug, func(t *testing.T) {
			w := get(t, app, page.Path, false)
			body := w.Body.String()
			if len(page.TOC) < 3 {
				t.Errorf("table of contents has %d entries", len(page.TOC))
			}
			for _, h := range page.TOC {
				if !strings.Contains(body, `<a href="#`+h.ID+`">`) {
					t.Errorf("table of contents misses #%s", h.ID)
				}
			}
			for _, other := range docs.pages {
				if !strings.Contains(body, `<a href="`+other.Path+`" hc-show=`) {
					t.Errorf("sidebar does not link %s", other.Path)
				}
			}
			if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
				t.Fatalf("status %d, type %s", w.Code, w.Header().Get("Content-Type"))
			}
			title := strings.NewReplacer("&", "&amp;", "+", "&#43;").Replace(page.Title)
			for _, want := range []string{
				"<h1>" + title + "</h1>", `<p class="lede">`, `id="overview"`, `id="example"`, `id="next"`,
				`href="` + page.NextPath + `"`, `aria-current="page"`, `<link rel="alternate" type="text/markdown" href="` + page.MarkdownPath + `">`,
			} {
				if !strings.Contains(body, want) {
					t.Errorf("missing %q", want)
				}
			}
			if strings.Contains(body, "—") {
				t.Error("visible em-dash in docs copy")
			}
			if strings.Contains(body, "hyperchi-docs-slot-") || strings.Contains(body, "raw HTML omitted") {
				t.Error("unresolved slot or dropped markup in page")
			}
			if pre, buttons := strings.Count(body, "<pre>"), strings.Count(body, `class="copy-code"`); pre == 0 || pre != buttons {
				t.Errorf("%d code blocks, %d copy buttons", pre, buttons)
			}
		})
	}
	// Anchors that other pages and the filters table of contents link to.
	filters := get(t, app, "/docs/filters", false).Body.String()
	for _, id := range []string{"arguments", "collections", "html", "custom", "errors", "reference"} {
		if !strings.Contains(filters, `id="`+id+`"`) {
			t.Errorf("filters page lost anchor #%s", id)
		}
	}
	directives := get(t, app, "/docs/directives", false).Body.String()
	if !strings.Contains(directives, `<div class="docs-demo">`) {
		t.Error("directives demo missing")
	}
	if got := get(t, app, "/docs/data", false).Body.String(); !strings.Contains(got, `href="/docs/forms">Forms and validation →`) {
		t.Error("data page lost its next label")
	}
}

func TestDocsCopyAndAgentMarkup(t *testing.T) {
	body := get(t, docsApp(t, testOrigin), "/docs/routing", false).Body.String()
	for _, want := range []string{
		`<button type="button" class="copy-code" aria-label="Copy code" hc-on:click="copyCode($event)">Copy</button><pre>`,
		`data-markdown="/docs/routing.md" hc-on:click="copyPage($event)">Copy page</button>`,
		`<a href="/docs/routing.md">View as Markdown</a>`,
		`href="https://claude.ai/new?q=Read%20https%3A%2F%2Fhyperchi.example%2Fdocs%2Frouting.md%20`,
		`href="https://chatgpt.com/?q=Read%20https%3A%2F%2Fhyperchi.example%2Fdocs%2Frouting.md%20`,
		`target="_blank" rel="noopener noreferrer">Open in Claude`,
		`target="_blank" rel="noopener noreferrer">Open in ChatGPT`,
		`aria-live="polite" hc-text="copyStatus"`,
		`hx-get="/docs/search"`, `hx-trigger="input changed delay:250ms, search"`, `hc-on:keydown="shortcut($event)"`,
		`maxlength="80"`, `action="/docs/search"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %s", want)
		}
	}
	if strings.Contains(body, "onclick=") {
		t.Error("inline handler in docs markup")
	}
	// Without PUBLIC_ORIGIN, links stay relative.
	dev := get(t, docsApp(t, ""), "/docs/routing", false).Body.String()
	if !strings.Contains(dev, `https://claude.ai/new?q=Read%20%2Fdocs%2Frouting.md%20`) {
		t.Error("development prompt is not relative")
	}
}

func TestLLMSIndexListsEveryTopic(t *testing.T) {
	docs := testDocs(t)
	w := get(t, docsApp(t, testOrigin), "/llms.txt", false)
	body := w.Body.String()
	if w.Code != 200 || w.Header().Get("Content-Type") != "text/plain; charset=utf-8" {
		t.Fatalf("status %d, type %s", w.Code, w.Header().Get("Content-Type"))
	}
	if !strings.HasPrefix(body, "# HyperChi\n\n> ") || !strings.Contains(body, "\n## Docs\n\n") || !strings.Contains(body, "\n## Optional\n\n") {
		t.Fatalf("not llms.txt shaped:\n%s", body)
	}
	if strings.Count(body, "\n# ") != 0 || strings.Count(body, "\n## ") != 2 {
		t.Error("llms.txt has headings beyond the H1 and two H2 sections")
	}
	for _, page := range docs.pages {
		link := "- [" + page.Title + "](" + testOrigin + "/docs/" + page.Slug + ".md): "
		if !strings.Contains(body, link) {
			t.Errorf("llms.txt missing %s", link)
		}
	}
	if !strings.Contains(body, "("+testOrigin+"/llms-full.txt)") {
		t.Error("llms.txt does not link llms-full.txt")
	}
	dev := get(t, docsApp(t, ""), "/llms.txt", false).Body.String()
	if !strings.Contains(dev, "- [Routing & responses](/docs/routing.md): ") {
		t.Error("development llms.txt is not relative")
	}
}

func TestLLMSFullContainsEveryPage(t *testing.T) {
	docs := testDocs(t)
	w := get(t, docsApp(t, testOrigin), "/llms-full.txt", false)
	body := w.Body.String()
	if w.Code != 200 || w.Header().Get("Content-Type") != "text/plain; charset=utf-8" {
		t.Fatalf("status %d, type %s", w.Code, w.Header().Get("Content-Type"))
	}
	last := -1
	for _, page := range docs.pages {
		at := strings.Index(body, page.Markdown)
		if at < 0 {
			t.Errorf("llms-full.txt missing %s", page.Slug)
			continue
		}
		if at < last {
			t.Errorf("%s is out of reading order", page.Slug)
		}
		last = at
	}
	if !strings.Contains(body, "Source: "+testOrigin+"/docs/routing") {
		t.Error("llms-full.txt lacks page sources")
	}
}

func TestMarkdownSourceRoutes(t *testing.T) {
	app := docsApp(t, testOrigin)
	w := get(t, app, "/docs/routing.md", false)
	body := w.Body.String()
	if w.Code != 200 || w.Header().Get("Content-Type") != "text/markdown; charset=utf-8" {
		t.Fatalf("status %d, type %s", w.Code, w.Header().Get("Content-Type"))
	}
	for _, want := range []string{"# Routing & responses\n\nA URL is a useful interface.", "## Pages and fragments {#overview}", "```go\napp.Get(\"/hello\""} {
		if !strings.Contains(body, want) {
			t.Errorf("routing.md missing %q", want)
		}
	}
	if strings.Contains(body, "title:") || strings.Contains(body, "<h2") || strings.Contains(body, `<p class=`) {
		t.Error("routing.md leaks front matter or HTML")
	}
	start := get(t, app, "/docs/start.md", false).Body.String()
	if index := get(t, app, "/docs/index.md", false).Body.String(); index != start || !strings.HasPrefix(start, "# Getting started") {
		t.Error("/docs/index.md does not serve the getting started page")
	}
	directives := get(t, app, "/docs/directives.md", false).Body.String()
	if strings.Contains(directives, "slot") || strings.Contains(directives, "\n\n\n") {
		t.Error("directives.md shows a slot marker or a gap")
	}
	if get(t, app, "/docs/missing.md", false).Code != http.StatusNotFound {
		t.Error("unknown Markdown page did not 404")
	}
}

func TestDocsSearch(t *testing.T) {
	app := docsApp(t, testOrigin)
	frag := get(t, app, "/docs/search?q=Register+a+custom+filter", true)
	body := frag.Body.String()
	if frag.Code != 200 || strings.Contains(body, "<html") {
		t.Fatalf("status %d, fragment %q", frag.Code, body)
	}
	first := strings.Index(body, `<li><a href="`)
	if first < 0 || !strings.HasPrefix(body[first:], `<li><a href="/docs/filters#custom">`) {
		t.Fatalf("known heading is not the top result:\n%s", body)
	}
	if !strings.Contains(body, "<mark>Register</mark>") {
		t.Error("match is not highlighted")
	}
	page := get(t, app, "/docs/search?q=bind", false).Body.String()
	if !strings.Contains(page, "<html") || !strings.Contains(page, `href="/docs/helpers#`) || !strings.Contains(page, "<mark>Bind</mark>") {
		t.Error("full-page search for bind lacks the layout or the helpers result")
	}
	if fragment := get(t, app, "/docs/search?q=bind", true).Body.String(); strings.Contains(fragment, "<html") || !strings.Contains(fragment, `role="status"`) {
		t.Error("htmx search for bind is not a fragment with a status")
	}

	hostile := get(t, app, "/docs/search?q=%3Cscript%3Ealert(1)%3C/script%3E", true).Body.String()
	if strings.Contains(hostile, "<script>alert") || !strings.Contains(hostile, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Errorf("hostile query not escaped: %s", hostile)
	}
	code := get(t, app, "/docs/search?q=Hello+from+the+server", true).Body.String()
	if strings.Contains(code, "<p>Hello") || !strings.Contains(code, "&lt;p&gt;") {
		t.Errorf("snippet markup not escaped: %s", code)
	}
	if empty := get(t, app, "/docs/search?q=", true); empty.Code != 200 || empty.Body.Len() != 0 {
		t.Errorf("empty htmx query: %d %q", empty.Code, empty.Body.String())
	}
	if hint := get(t, app, "/docs/search", false).Body.String(); !strings.Contains(hint, `class="search-hint"`) {
		t.Error("empty full-page search has no hint")
	}
	if none := get(t, app, "/docs/search?q=zzqxv", true).Body.String(); !strings.Contains(none, `class="search-empty"`) {
		t.Error("no-result state missing")
	}
	if got := []rune(boundQuery(strings.Repeat("é", 500))); len(got) != maxQueryRunes {
		t.Errorf("bounded query has %d characters", len(got))
	}
	docs := testDocs(t)
	a, b := docs.search("template filters"), docs.search("template filters")
	if len(a) == 0 || len(a) != len(b) {
		t.Fatal("search is empty or unstable")
	}
	for i := range a {
		if a[i].URL != b[i].URL {
			t.Fatal("search order is not deterministic")
		}
	}
	if a[0].URL != "/docs/filters" {
		t.Errorf("title match does not rank first: %s", a[0].URL)
	}
}

func TestDocsRoutesStayBehindSitePolicy(t *testing.T) {
	policy, err := newSitePolicy("127.0.0.1", "9999", "")
	if err != nil {
		t.Fatal(err)
	}
	app := docsApp(t, "")
	site := policy.middleware(app)(app)
	for _, path := range []string{"/llms.txt", "/llms-full.txt", "/docs/routing.md", "/docs/search?q=bind"} {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.Host = "evil.example:9999"
		w := httptest.NewRecorder()
		site.ServeHTTP(w, r)
		if w.Code != http.StatusMisdirectedRequest {
			t.Errorf("%s from a foreign host: %d", path, w.Code)
		}
		r = httptest.NewRequest(http.MethodGet, path, nil)
		r.Host = "localhost:9999"
		w = httptest.NewRecorder()
		site.ServeHTTP(w, r)
		if w.Code != http.StatusOK || w.Header().Get("Content-Security-Policy") == "" {
			t.Errorf("%s through the policy: %d", path, w.Code)
		}
	}
}

func TestLoadDocsRejectsBrokenFiles(t *testing.T) {
	good := "---\ntitle: A\nsection: Core\norder: 1\nnext: a\ndescription: d\n---\n\n## X {#overview}\n"
	for name, files := range map[string]fstest.MapFS{
		"no title":        {"docs/a.md": {Data: []byte("---\norder: 1\nnext: a\ndescription: d\n---\n")}},
		"bad order":       {"docs/a.md": {Data: []byte("---\ntitle: A\norder: one\nnext: a\ndescription: d\n---\n")}},
		"broken next":     {"docs/a.md": {Data: []byte(strings.Replace(good, "next: a", "next: zz", 1))}},
		"unknown slot":    {"docs/a.md": {Data: []byte(good + "\n<!-- slot:nope -->\n")}},
		"inline slot":     {"docs/a.md": {Data: []byte(good + "\ntext\n<!-- slot:x -->\n")}},
		"no section":      {"docs/a.md": {Data: []byte(strings.Replace(good, "section: Core\n", "", 1))}},
		"unknown section": {"docs/a.md": {Data: []byte(strings.Replace(good, "section: Core", "section: Misc", 1))}},
		"quoted title":    {"docs/a.md": {Data: []byte(strings.Replace(good, "title: A", "title: A's", 1))}},
		"dup order":       {"docs/a.md": {Data: []byte(good)}, "docs/b.md": {Data: []byte(strings.Replace(good, "next: a", "next: b", 1))}},
		"empty":           {},
	} {
		if _, err := loadDocs(files, map[string]docSlot{"x": {HTML: "<p>x</p>"}}); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := loadDocs(fstest.MapFS{"docs/a.md": {Data: []byte(good)}}, nil); err != nil {
		t.Fatal(err)
	}
}
