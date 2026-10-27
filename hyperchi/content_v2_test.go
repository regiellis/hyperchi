package hyperchi

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/regiellis/hyperchi/hyperchi/content"
)

func TestContentManagerTypedSchemasAndRelations(t *testing.T) {
	app := contentApp(t, true)
	type post struct {
		Title  string `json:"title" content:"required"`
		Author string `json:"author" content:"required,ref=authors"`
	}
	schema, err := content.SchemaFor[post]("Post")
	if err != nil {
		t.Fatal(err)
	}
	files := fstest.MapFS{
		"posts/hello.md":  {Data: []byte("---\ntitle: Hello\nauthor: authors-regi\n---\nHello")},
		"authors/regi.md": {Data: []byte("---\nname: Regi\n---\nRegi")},
	}
	if err := app.Content.LoadFS(files, ".", WithContentCollection(content.CollectionConfig{Name: "posts", Schema: schema})); err != nil {
		t.Fatal(err)
	}
	item, err := app.Content.Get("posts", "hello")
	if err != nil {
		t.Fatal(err)
	}
	typed, err := content.DecodeData[post](item.Metadata)
	if err != nil || typed.Title != "Hello" || typed.Author != "authors-regi" {
		t.Fatalf("decoded item: %+v %v", typed, err)
	}
	// Retrying a source with broken references must preserve the installed items.
	delete(files, "authors/regi.md")
	err = app.Content.LoadFS(files, ".", WithContentCollection(content.CollectionConfig{Name: "posts", Schema: schema}))
	if err == nil {
		t.Fatal("accepted broken replacement")
	}
	retained, err := app.Content.Get("posts", "hello")
	if err != nil || retained.Title != "Hello" {
		t.Fatalf("lost good application snapshot: %v", err)
	}
	broken := contentApp(t, true)
	delete(files, "authors/regi.md")
	err = broken.Content.LoadFS(files, ".", WithContentCollection(content.CollectionConfig{Name: "posts", Schema: schema}))
	if err == nil || !strings.Contains(err.Error(), "authors") {
		t.Fatalf("missing relation: %v", err)
	}
	if _, err := broken.Content.Get("posts", "hello"); err == nil {
		t.Fatal("published broken relation")
	}
}
