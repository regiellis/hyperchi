package content

import (
	"errors"
	"html/template"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"testing/fstest"
)

func siteFS() fstest.MapFS {
	return fstest.MapFS{
		"site/blog/hello.md":       {Data: []byte("---\ntitle: Hello\norder: 2\n---\n# Hi\n\nbody")},
		"site/blog/2024/recap.md":  {Data: []byte("---\ntitle: Recap\n---\nrecap")},
		"site/pages/about.html":    {Data: []byte("<p>about</p>")},
		"site/stray.md":            {Data: []byte("no collection")},
		"elsewhere/blog/nope.md":   {Data: []byte("outside the root")},
		"site/blog/.hidden/x.json": {Data: []byte("{}")},
	}
}

func TestLoadContentFromFS(t *testing.T) {
	cl := NewContentLayer(&ContentConfig{FS: siteFS(), BaseDir: "site", WatchForChanges: true})
	if err := cl.LoadContent(); err != nil {
		t.Fatal(err)
	}
	// An FS is never watched, even with WatchForChanges set.
	if cl.watchCancel != nil {
		t.Fatal("a layer reading an FS started a watcher")
	}

	all := cl.GetAllCollections()
	names := make([]string, 0, len(all))
	for name := range all {
		names = append(names, name)
	}
	sort.Strings(names)
	if strings.Join(names, ",") != "blog,pages" {
		t.Fatalf("collections = %v, want blog and pages", names)
	}

	blog := all["blog"]
	if blog.Path != "site/blog" {
		t.Errorf("collection path = %q, want site/blog", blog.Path)
	}
	hello, ok := blog.GetContent("blog-hello")
	if !ok {
		t.Fatalf("blog-hello missing: %v", keys(blog.Contents))
	}
	if hello.Path != "site/blog/hello.md" || hello.Slug != "hello" {
		t.Errorf("path/slug = %q/%q", hello.Path, hello.Slug)
	}
	if hello.GetString("title") != "Hello" || hello.GetInt("order") != 2 {
		t.Errorf("front matter = %v", hello.Data)
	}
	if !strings.Contains(string(hello.HTML), "<h1>Hi</h1>") {
		t.Errorf("HTML = %q", hello.HTML)
	}
	if !hello.UpdatedAt.IsZero() {
		t.Errorf("a MapFS file without a time loaded UpdatedAt %v", hello.UpdatedAt)
	}
	recap, ok := blog.GetContent("blog-2024-recap")
	if !ok || recap.Slug != "recap" || recap.Path != "site/blog/2024/recap.md" {
		t.Fatalf("nested file = %+v, %v", recap, ok)
	}
}

func TestLoadContentFromFSRoot(t *testing.T) {
	fsys, err := fs.Sub(siteFS(), "site")
	if err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{"", ".", "./"} {
		cl := NewContentLayer(&ContentConfig{FS: fsys, BaseDir: root})
		if err := cl.LoadContent(); err != nil {
			t.Fatalf("root %q: %v", root, err)
		}
		blog, ok := cl.GetCollection("blog")
		if !ok || len(blog.Contents) != 2 {
			t.Fatalf("root %q: blog = %v", root, blog)
		}
		if c, ok := blog.GetContent("blog-hello"); !ok || c.Path != "blog/hello.md" {
			t.Fatalf("root %q: hello = %+v", root, c)
		}
	}
}

func TestLoadContentFromFSRejectsInvalidRoot(t *testing.T) {
	cl := NewContentLayer(&ContentConfig{FS: siteFS(), BaseDir: "../site"})
	if err := cl.LoadContent(); err == nil || !strings.Contains(err.Error(), "invalid root") {
		t.Fatalf("LoadContent = %v, want an invalid root error", err)
	}
}

// Reading the same tree from disk and through os.DirFS yields the same IDs,
// slugs, data, and HTML.
func TestLoadContentDiskAndFSAgree(t *testing.T) {
	disk := NewContentLayer(&ContentConfig{BaseDir: filepath.Join("testdata", "site")})
	viaFS := NewContentLayer(&ContentConfig{FS: os.DirFS("testdata"), BaseDir: "site"})
	for _, cl := range []*ContentLayer{disk, viaFS} {
		if err := cl.LoadContent(); err != nil {
			t.Fatal(err)
		}
	}
	for name, dc := range disk.GetAllCollections() {
		fc, ok := viaFS.GetCollection(name)
		if !ok || len(fc.Contents) != len(dc.Contents) || fc.Type != dc.Type {
			t.Fatalf("collection %s differs: %v vs %v", name, dc, fc)
		}
		for id, d := range dc.Contents {
			f, ok := fc.Contents[id]
			if !ok {
				t.Fatalf("%s missing from the FS load", id)
			}
			if f.Slug != d.Slug || f.HTML != d.HTML || f.Body != d.Body || len(f.Data) != len(d.Data) {
				t.Errorf("%s differs:\n disk %+v\n fs   %+v", id, d, f)
			}
			if filepath.ToSlash(d.Path) != "testdata/"+f.Path {
				t.Errorf("%s path: disk %q, fs %q", id, d.Path, f.Path)
			}
		}
	}
}

func TestMarkdownHook(t *testing.T) {
	fsys := fstest.MapFS{
		"notes/a.md":   {Data: []byte("---\ntitle: A\n---\n*a*")},
		"notes/b.txt":  {Data: []byte("<b>")},
		"notes/bad.md": {Data: []byte("fail")},
	}
	render := func(body string) (template.HTML, error) {
		if body == "fail" {
			return "", errors.New("renderer refused")
		}
		return template.HTML("<em>" + template.HTMLEscapeString(strings.Trim(body, "*")) + "</em>"), nil
	}
	cl := NewContentLayer(&ContentConfig{FS: fsys, Markdown: render})
	if err := cl.LoadContent(); err != nil {
		t.Fatal(err)
	}
	notes, _ := cl.GetCollection("notes")
	if a := notes.Contents["notes-a"]; a.HTML != "<em>a</em>" || a.GetString("title") != "A" {
		t.Errorf("a = %q, %v", a.HTML, a.Data)
	}
	// Other types keep the built-in rendering.
	if b := notes.Contents["notes-b"]; b.HTML != "<pre>&lt;b&gt;</pre>" {
		t.Errorf("b = %q", b.HTML)
	}
	// A render failure is kept with the error, like an unreadable file.
	bad := notes.Contents["notes-bad"]
	if msg, _ := bad.Get("_error").(string); !strings.Contains(msg, "renderer refused") || !strings.Contains(msg, "notes/bad.md") {
		t.Errorf("_error = %q", msg)
	}
}

// A schema's own validator error is reported with the file it rejected.
func TestValidatorErrorNamesTheFile(t *testing.T) {
	fsys := fstest.MapFS{"posts/a.md": {Data: []byte("a")}}
	errNope := errors.New("nope")
	schema := NewSchema("s").Validate(func(*Content) error { return errNope }).Build()
	cl := NewContentLayer(&ContentConfig{FS: fsys})
	cl.DefineCollection(CollectionConfig{Name: "posts", Schema: schema})
	err := cl.LoadContent()
	if !errors.Is(err, errNope) || !strings.Contains(err.Error(), "posts/a.md: nope") {
		t.Fatalf("LoadContent = %v, want the validator error naming posts/a.md", err)
	}
}
