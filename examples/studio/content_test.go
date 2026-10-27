package main

import (
	"io/fs"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/regiellis/hyperchi/hyperchi"
	"github.com/regiellis/hyperchi/hyperchi/config"
	"github.com/regiellis/hyperchi/modules/markdown"
)

// requiredKeys is the front matter each content directory must carry.
var requiredKeys = map[string][]string{
	"games":   {"title", "tagline", "summary", "genre", "status", "status_kind", "order", "platforms", "cover", "screen_1"},
	"devlog":  {"title", "date", "author", "tags", "summary"},
	"pages":   {"title", "description"},
	"careers": {"title", "location", "kind", "posted", "summary"},
}

// TestEveryContentFileParses reads each embedded Markdown file on its own, so
// a failure names the file and the field, independent of the loader.
func TestEveryContentFileParses(t *testing.T) {
	md := markdown.New()
	files := 0
	err := fs.WalkDir(contentFS, "content", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		files++
		t.Run(name, func(t *testing.T) {
			if path.Ext(name) != ".md" {
				t.Fatalf("only Markdown belongs under content/")
			}
			src, err := fs.ReadFile(contentFS, name)
			if err != nil {
				t.Fatal(err)
			}
			doc, err := md.Parse(src)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			dir := path.Base(path.Dir(name))
			keys, ok := requiredKeys[dir]
			if !ok {
				t.Fatalf("unexpected content directory %q", dir)
			}
			for _, k := range keys {
				if strings.TrimSpace(doc.Meta[k]) == "" {
					t.Errorf("front matter %q is missing", k)
				}
			}
			for _, k := range []string{"date", "posted"} {
				if v, ok := doc.Meta[k]; ok {
					if _, err := time.Parse(dateLayout, v); err != nil {
						t.Errorf("%s %q is not a YYYY-MM-DD date", k, v)
					}
				}
			}
			if strings.ContainsRune(string(src), '\u2014') {
				t.Error("content contains an em-dash; the project voice forbids them")
			}
			if len(doc.Headings) == 0 && dir == "devlog" {
				t.Error("a devlog post needs headings for its table of contents")
			}
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if files == 0 {
		t.Fatal("no content files embedded")
	}
}

// contentManager returns a fresh app's content manager.
func contentManager(t *testing.T) *hyperchi.ContentManager {
	t.Helper()
	cfg := config.Production()
	cfg.Database.Name = filepath.Join(t.TempDir(), "content.db")
	app, err := hyperchi.NewWithConfigE(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := app.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})
	return app.Content
}

func TestLibraryLoads(t *testing.T) {
	lib, err := LoadLibrary(contentManager(t), contentFS)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(lib.Games); got != 3 {
		t.Errorf("games = %d, want 3", got)
	}
	if lib.Flagship() == nil || lib.Flagship().Slug != "lamplit-crossing" {
		t.Errorf("flagship = %+v", lib.Flagship())
	}
	if len(lib.Posts) < 2*postsPerPage+1 {
		t.Errorf("need more than two pages of posts for pagination, have %d", len(lib.Posts))
	}
	for i, p := range lib.Posts {
		if i > 0 && p.Date.After(lib.Posts[i-1].Date) {
			t.Errorf("posts out of order at %s", p.Slug)
		}
		if (i > 0) != (p.Newer != nil) || (i < len(lib.Posts)-1) != (p.Older != nil) {
			t.Errorf("%s: newer/older links wrong", p.Slug)
		}
		if len(p.TOC) == 0 || p.ReadMinutes < 1 {
			t.Errorf("%s: toc %d, minutes %d", p.Slug, len(p.TOC), p.ReadMinutes)
		}
	}
	for _, slug := range requiredPages {
		if lib.Pages[slug] == nil {
			t.Errorf("page %s missing", slug)
		}
	}
	if len(lib.Roles) == 0 || len(lib.Tags) == 0 {
		t.Errorf("roles %d, tags %d", len(lib.Roles), len(lib.Tags))
	}
	for _, g := range lib.Games {
		if len(g.Stores) == 0 || len(g.Facts) == 0 || len(g.Screens) == 0 {
			t.Errorf("%s: stores %d, facts %d, screens %d", g.Slug, len(g.Stores), len(g.Facts), len(g.Screens))
		}
	}
}

// TestLoaderRejectsBadContent proves the loader fails loudly on each kind of
// broken edit instead of rendering a half-empty page.
func TestLoaderRejectsBadContent(t *testing.T) {
	valid := map[string]string{
		"content/games/one.md":     "---\ntitle: One\ntagline: t\nsummary: s\ngenre: g\nstatus: Out\nstatus_kind: released\norder: 1\nflagship: true\nplatforms: Linux\ncover: img/a.svg | alt\nscreen_1: img/b.svg | alt\n---\nBody\n",
		"content/devlog/post.md":   "---\ntitle: Post\ndate: 2026-01-02\nauthor: A\ntags: design\nsummary: s\n---\n## Heading\n\nText\n",
		"content/pages/about.md":   "---\ntitle: About\ndescription: d\n---\nx\n",
		"content/pages/press.md":   "---\ntitle: Press\ndescription: d\n---\nx\n",
		"content/pages/privacy.md": "---\ntitle: Privacy\ndescription: d\n---\nx\n",
		"content/careers/role.md":  "---\ntitle: Role\nlocation: Remote\nkind: Contract\nposted: 2026-01-02\nsummary: s\n---\nx\n",
	}
	build := func(file, src string) fstest.MapFS {
		fsys := fstest.MapFS{}
		for name, body := range valid {
			fsys[name] = &fstest.MapFile{Data: []byte(body)}
		}
		if file != "" {
			fsys[file] = &fstest.MapFile{Data: []byte(src)}
		}
		return fsys
	}
	if _, err := LoadLibrary(contentManager(t), build("", "")); err != nil {
		t.Fatalf("valid fixture rejected: %v", err)
	}
	cases := []struct{ name, file, src, want string }{
		{"bad date", "content/devlog/post.md", strings.Replace(valid["content/devlog/post.md"], "2026-01-02", "Jan 2", 1), "expected time.Time or a date string"},
		{"missing title", "content/pages/about.md", "---\ndescription: d\n---\nx\n", "required field missing: title"},
		{"unknown key", "content/pages/about.md", "---\ntitle: A\ndescription: d\ntitel: typo\n---\nx\n", `unknown front matter key "titel"`},
		{"unknown game", "content/devlog/post.md", strings.Replace(valid["content/devlog/post.md"], "tags:", "game: nope\ntags:", 1), `game "nope" does not exist`},
		{"bad status", "content/games/one.md", strings.Replace(valid["content/games/one.md"], "status_kind: released", "status_kind: soon", 1), "status_kind must be"},
		{"bad slug", "content/devlog/Bad_Name.md", valid["content/devlog/post.md"], "lowercase slug"},
		{"image without alt", "content/games/one.md", strings.Replace(valid["content/games/one.md"], "img/b.svg | alt", "img/b.svg", 1), "alternative text"},
		{"nested front matter", "content/pages/about.md", "---\ntitle: A\ndescription: d\nlist:\n  - one\n---\nx\n", `"- one" is not a "key: value" line`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadLibrary(contentManager(t), build(tc.file, tc.src))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestHeadingAnchors(t *testing.T) {
	got := string(withHeadingAnchors(`<h2 id="save-small">Save <em>small</em></h2><h4 id="x">Deep</h4>`))
	want := `<h2 id="save-small">Save <em>small</em> <a class="heading-anchor" href="#save-small" aria-label="Link to this section">#</a></h2>`
	if !strings.HasPrefix(got, want) {
		t.Errorf("got %s", got)
	}
	if strings.Contains(got, `href="#x"`) {
		t.Error("only h2 and h3 get anchors")
	}
}
