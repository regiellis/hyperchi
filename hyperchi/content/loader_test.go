package content

import (
	"errors"
	"html/template"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// writeFile creates root/rel with data, making parent directories as needed.
func writeFile(t *testing.T, root, rel, data string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestParseFrontmatter(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		fm   map[string]any
		body string
	}{
		{
			name: "typed scalars and list",
			in:   "---\ntitle: \"Hello World\"\ndraft: false\npublished: true\norder: 3\nscore: 4.5\ntags: [\"intro\", blog, ]\n# a comment\n\n---\n\nBody text\n",
			fm: map[string]any{
				"title": "Hello World", "draft": false, "published": true,
				"order": 3, "score": 4.5, "tags": []string{"intro", "blog"},
			},
			body: "Body text\n",
		},
		{
			name: "dashes inside a value do not close the header",
			in:   "---\ntitle: before --- after\nn: 1\n---\nbody",
			fm:   map[string]any{"title": "before --- after", "n": 1},
			body: "body",
		},
		{
			name: "quoted values stay strings",
			in:   "---\nzip: \"02134\"\nversion: '1.0'\nflag: \"true\"\n---\n",
			fm:   map[string]any{"zip": "02134", "version": "1.0", "flag": "true"},
			body: "",
		},
		{
			name: "value keeps everything after the first colon",
			in:   "---\npublishedAt: 2024-01-15T10:00:00Z\n---\nx",
			fm:   map[string]any{"publishedAt": "2024-01-15T10:00:00Z"},
			body: "x",
		},
		{
			name: "CRLF line endings",
			in:   "---\r\ntitle: win\r\n---\r\nbody\r\n",
			fm:   map[string]any{"title": "win"},
			body: "body\r\n",
		},
		{
			name: "empty header",
			in:   "---\n---\nbody",
			fm:   map[string]any{},
			body: "body",
		},
		{
			name: "no front matter",
			in:   "# Title\n\ntext",
			fm:   map[string]any{},
			body: "# Title\n\ntext",
		},
		{
			name: "unterminated header is body",
			in:   "---\ntitle: x\nno close",
			fm:   map[string]any{},
			body: "---\ntitle: x\nno close",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fm, body, err := parseFrontmatter(tc.in)
			if err != nil {
				t.Fatalf("parseFrontmatter: %v", err)
			}
			if !reflect.DeepEqual(fm, tc.fm) {
				t.Errorf("front matter = %#v, want %#v", fm, tc.fm)
			}
			if body != tc.body {
				t.Errorf("body = %q, want %q", body, tc.body)
			}
		})
	}
}

func TestSimpleMarkdown(t *testing.T) {
	for _, tc := range []struct {
		name, in, want string
	}{
		{"headings", "# One\n## Two\n### Three\n#### Four\n##### Five\n###### Six",
			"<h1>One</h1>\n<h2>Two</h2>\n<h3>Three</h3>\n<h4>Four</h4>\n<h5>Five</h5>\n<h6>Six</h6>\n"},
		{"paragraph lines join", "first line\nsecond line\n\nnext para",
			"<p>first line second line</p>\n<p>next para</p>\n"},
		{"html is escaped", "<script>x</script> & co",
			"<p>&lt;script&gt;x&lt;/script&gt; &amp; co</p>\n"},
		{"list", "intro\n- a\n- b\n\nafter",
			"<p>intro</p>\n<ul>\n<li>a</li>\n<li>b</li>\n</ul>\n<p>after</p>\n"},
		{"indented list items", "  - one\n  - two",
			"<ul>\n<li>one</li>\n<li>two</li>\n</ul>\n"},
		{"heading closes an open list", "- a\n# H",
			"<ul>\n<li>a</li>\n</ul>\n<h1>H</h1>\n"},
		{"text after a list keeps source order", "- a\ntext\n- b",
			"<ul>\n<li>a</li>\n</ul>\n<p>text</p>\n<ul>\n<li>b</li>\n</ul>\n"},
		{"fence closes an open list", "- a\n```\ncode\n```",
			"<ul>\n<li>a</li>\n</ul>\n<pre><code>code\n</code></pre>\n"},
		{"code is escaped and verbatim", "para\n```go\n# not a heading\n<b>\n```",
			"<p>para</p>\n<pre><code># not a heading\n&lt;b&gt;\n</code></pre>\n"},
		{"unterminated fence is closed", "```\ncode",
			"<pre><code>code\n</code></pre>\n"},
		{"empty input", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := simpleMarkdown(tc.in); got != tc.want {
				t.Errorf("simpleMarkdown(%q)\n got %q\nwant %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestRenderToHTMLByExtension(t *testing.T) {
	for _, tc := range []struct {
		ext, body string
		want      template.HTML
	}{
		{".html", "<b>raw</b>", "<b>raw</b>"},
		{".HTM", "<i>raw</i>", "<i>raw</i>"},
		{".txt", "a < b", "<pre>a &lt; b</pre>"},
		{".md", "# T", "<h1>T</h1>\n"},
		{".mdx", "x", "<p>x</p>\n"},
		{".rst", "<x>", "&lt;x&gt;"},
	} {
		if got := renderToHTML(tc.ext, tc.body); got != tc.want {
			t.Errorf("renderToHTML(%q, %q) = %q, want %q", tc.ext, tc.body, got, tc.want)
		}
	}
}

func TestExtToType(t *testing.T) {
	for ext, want := range map[string]ContentType{
		".md": ContentTypeMarkdown, ".MDX": ContentTypeMDX, ".html": ContentTypeHTML,
		".htm": ContentTypeHTML, ".txt": ContentTypeText, ".other": ContentTypeMarkdown,
	} {
		if got := extToType(ext); got != want {
			t.Errorf("extToType(%q) = %q, want %q", ext, got, want)
		}
	}
}

func TestSanitizeID(t *testing.T) {
	sep := string(os.PathSeparator)
	if got, want := sanitizeID("Posts"+sep+"My Post_v2.md"), "posts-my-post-v2"; got != want {
		t.Errorf("sanitizeID = %q, want %q", got, want)
	}
}

func TestLoadAllBuildsCollectionsFromSubdirectories(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "posts/hello-world.md", "---\ntitle: Hello\ntags: [go, web]\n---\n# Hello\n\nBody.")
	writeFile(t, root, "posts/nested/deep.mdx", "---\ntitle: Deep\n---\ntext")
	writeFile(t, root, "pages/about.html", "<p>About</p>")
	writeFile(t, root, "pages/notes.txt", "plain <text>")
	writeFile(t, root, "pages/image.png", "not content")
	writeFile(t, root, "pages/Makefile", "no extension")
	writeFile(t, root, "top-level.md", "files directly under the base are not in a collection")

	for name, base := range map[string]string{
		"clean base":       root,
		"trailing slash":   root + string(os.PathSeparator),
		"unclean relative": filepath.Join(root, "posts", ".."),
	} {
		t.Run(name, func(t *testing.T) {
			got, err := (&ContentLoader{baseDir: base}).LoadAll()
			if err != nil {
				t.Fatalf("LoadAll: %v", err)
			}
			if len(got) != 2 {
				t.Fatalf("collections = %v, want posts and pages", keys(got))
			}

			posts := got["posts"]
			// One .md and one .mdx file tie, and a tie goes to Markdown; pages tie
			// .html with .txt, and HTML wins.
			if posts.Type != ContentTypeMarkdown || got["pages"].Type != ContentTypeHTML {
				t.Errorf("types = %q/%q", posts.Type, got["pages"].Type)
			}
			ids := keys(posts.Contents)
			if want := []string{"posts-hello-world", "posts-nested-deep"}; !reflect.DeepEqual(ids, want) {
				t.Fatalf("post IDs = %v, want %v", ids, want)
			}
			hello := posts.Contents["posts-hello-world"]
			if hello.Slug != "hello-world" || hello.Collection != "posts" {
				t.Errorf("slug/collection = %q/%q", hello.Slug, hello.Collection)
			}
			if hello.GetString("title") != "Hello" {
				t.Errorf("title = %q", hello.GetString("title"))
			}
			if tags := hello.GetStringSlice("tags"); !reflect.DeepEqual(tags, []string{"go", "web"}) {
				t.Errorf("tags = %v", tags)
			}
			if hello.Body != "# Hello\n\nBody." {
				t.Errorf("body = %q", hello.Body)
			}
			if hello.HTML != "<h1>Hello</h1>\n<p>Body.</p>\n" {
				t.Errorf("html = %q", hello.HTML)
			}
			if hello.UpdatedAt.IsZero() || !hello.CreatedAt.Equal(hello.UpdatedAt) {
				t.Errorf("timestamps not taken from the file: %v / %v", hello.CreatedAt, hello.UpdatedAt)
			}

			pages := got["pages"]
			if ids := keys(pages.Contents); !reflect.DeepEqual(ids, []string{"pages-about", "pages-notes"}) {
				t.Fatalf("page IDs = %v", ids)
			}
			if pages.Contents["pages-about"].HTML != "<p>About</p>" {
				t.Errorf("html page = %q", pages.Contents["pages-about"].HTML)
			}
			if pages.Contents["pages-notes"].HTML != "<pre>plain &lt;text&gt;</pre>" {
				t.Errorf("text page = %q", pages.Contents["pages-notes"].HTML)
			}
		})
	}
}

func TestLoadAllWithoutBaseDirectory(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	file := filepath.Join(t.TempDir(), "file.md")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, l := range map[string]*ContentLoader{
		"nil loader":   nil,
		"empty base":   {},
		"missing base": {baseDir: missing},
		"base is file": {baseDir: file},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := l.LoadAll()
			if err != nil || len(got) != 0 {
				t.Fatalf("LoadAll = %v, %v; want empty, nil", got, err)
			}
		})
	}
}

func TestLoadAllKeepsUnreadableFileAsErrorEntry(t *testing.T) {
	root := t.TempDir()
	// A dangling symlink is listed by the walk but cannot be read.
	if err := os.MkdirAll(filepath.Join(root, "posts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "nowhere.md"), filepath.Join(root, "posts", "broken.md")); err != nil {
		t.Fatal(err)
	}
	got, err := (&ContentLoader{baseDir: root}).LoadAll()
	if err != nil {
		t.Fatal(err)
	}
	c, ok := got["posts"].GetContent("posts-broken")
	if !ok {
		t.Fatalf("broken file missing from collection: %v", keys(got["posts"].Contents))
	}
	if msg, _ := c.Get("_error").(string); !strings.Contains(msg, "broken.md") {
		t.Errorf("_error = %q, want a read error naming the file", msg)
	}
	if c.Slug != "broken" || c.Collection != "posts" {
		t.Errorf("slug/collection = %q/%q", c.Slug, c.Collection)
	}
}

func TestContentLayerLoadContent(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "blog/a.md", "---\ntitle: A\nauthor: ann\n---\nalpha")
	writeFile(t, root, "blog/b.md", "---\ntitle: B\n---\nbeta")
	writeFile(t, root, "docs/intro.md", "---\ntitle: Intro\n---\nhi")

	cl := NewContentLayer(&ContentConfig{BaseDir: root})
	blogSchema := NewSchema("blog").
		Field("author", FieldTypeString).Required().
		Field("status", FieldTypeString).Default("draft").
		Build().
		Transform(func(c *Content) *Content {
			c.Set("titleUpper", strings.ToUpper(c.GetString("title")))
			return c
		}).
		Build()
	cl.DefineCollection(CollectionConfig{Name: "blog", Path: "custom/blog", Schema: blogSchema})

	err := cl.LoadContent()
	var cerr *ContentError
	if !errors.As(err, &cerr) {
		t.Fatalf("LoadContent error = %v, want the schema violation for b.md", err)
	}
	if cerr.Type != "validation" || !strings.HasSuffix(cerr.Path, "b.md") || !strings.Contains(err.Error(), "author") {
		t.Errorf("validation error = %+v", cerr)
	}

	blog, ok := cl.GetCollection("blog")
	if !ok {
		t.Fatal("blog collection missing")
	}
	if blog.Schema != blogSchema || blog.Path != "custom/blog" {
		t.Errorf("declared configuration lost: schema=%p path=%q", blog.Schema, blog.Path)
	}
	if ids := keys(blog.Contents); !reflect.DeepEqual(ids, []string{"blog-a"}) {
		t.Fatalf("blog IDs = %v, want only the valid file", ids)
	}
	a := blog.Contents["blog-a"]
	if a.GetString("status") != "draft" || a.GetString("titleUpper") != "A" {
		t.Errorf("schema default/transform not applied: %v", a.Data)
	}

	docs, ok := cl.GetCollection("docs")
	if !ok || len(docs.Contents) != 1 || docs.Schema != nil {
		t.Errorf("undeclared docs collection = %+v", docs)
	}
	if len(cl.GetAllCollections()) != 2 {
		t.Errorf("collections = %v", keys(cl.GetAllCollections()))
	}
}

func TestContentLayerLoadContentNoDirectory(t *testing.T) {
	for name, cfg := range map[string]*ContentConfig{
		"empty base dir":   {},
		"missing base dir": {BaseDir: filepath.Join(t.TempDir(), "absent")},
	} {
		t.Run(name, func(t *testing.T) {
			cl := NewContentLayer(cfg)
			if err := cl.LoadContent(); err != nil {
				t.Fatalf("LoadContent: %v", err)
			}
			if n := len(cl.GetAllCollections()); n != 0 {
				t.Errorf("collections = %d, want 0", n)
			}
		})
	}
}

func TestContentLayerLoadContentWithWatchFlag(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "posts/a.md", "a")
	cl := NewContentLayer(&ContentConfig{BaseDir: root, WatchForChanges: true})
	if err := cl.LoadContent(); err != nil {
		t.Fatalf("LoadContent: %v", err)
	}
	t.Cleanup(func() {
		if err := cl.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, ok := cl.GetCollection("posts"); !ok {
		t.Fatal("posts not loaded")
	}
}

func TestReloadContentPicksUpChanges(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "posts/a.md", "---\ntitle: First\n---\n")
	cl := NewContentLayer(&ContentConfig{BaseDir: root})
	if err := cl.LoadContent(); err != nil {
		t.Fatal(err)
	}

	writeFile(t, root, "posts/a.md", "---\ntitle: Edited\n---\n")
	writeFile(t, root, "posts/b.md", "new")
	if err := cl.reloadContent(); err != nil {
		t.Fatal(err)
	}

	posts, _ := cl.GetCollection("posts")
	if ids := keys(posts.Contents); !reflect.DeepEqual(ids, []string{"posts-a", "posts-b"}) {
		t.Fatalf("IDs after reload = %v", ids)
	}
	if got := posts.Contents["posts-a"].GetString("title"); got != "Edited" {
		t.Errorf("title after reload = %q", got)
	}

	// A base directory that does not exist reloads to nothing.
	empty := NewContentLayer(&ContentConfig{BaseDir: filepath.Join(root, "absent")})
	if err := empty.reloadContent(); err != nil {
		t.Fatal(err)
	}
	if len(empty.GetAllCollections()) != 0 {
		t.Error("reload of a missing directory created collections")
	}
}

func TestDefaultContentConfig(t *testing.T) {
	cl := NewContentLayer(nil)
	cfg := cl.config
	if cfg.BaseDir != "content" || !cfg.WatchForChanges || cfg.WatchInterval != 0 || cfg.OnReload != nil {
		t.Errorf("default config = %+v", cfg)
	}
}

// keys returns a map's keys in sorted order.
func keys[V any](m map[string]V) []string {
	return slices.Sorted(maps.Keys(m))
}

// A header line that is not "key: value", or a repeated key, fails the file
// instead of being dropped or overwritten.
func TestParseFrontmatterRejectsBadLines(t *testing.T) {
	for name, in := range map[string]string{
		"no colon":      "---\ntitle: x\njust text\n---\nbody",
		"empty key":     "---\n: orphan\n---\nbody",
		"duplicate key": "---\ntitle: a\ntitle: b\n---\nbody",
	} {
		if _, _, err := parseFrontmatter(in); err == nil {
			t.Errorf("%s: parseFrontmatter accepted %q", name, in)
		}
	}
}
