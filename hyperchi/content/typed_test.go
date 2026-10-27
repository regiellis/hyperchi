package content

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

type typedPost struct {
	Title    string         `json:"title" content:"required"`
	Date     time.Time      `json:"date"`
	Count    int            `json:"count"`
	Draft    bool           `json:"draft"`
	Author   string         `json:"author" content:"required,ref=authors"`
	Related  []string       `json:"related" content:"ref=posts"`
	Tags     []string       `json:"tags"`
	Rating   *float64       `json:"rating"`
	Metadata map[string]any `json:"metadata"`
}

func TestTypedSchemaAndDecode(t *testing.T) {
	t.Parallel()
	schema, err := SchemaFor[typedPost]("Post", func(post typedPost) error {
		if len(post.Title) < 3 {
			return errors.New("title too short")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	raw := map[string]any{"title": "Hello", "author": "authors-regi", "date": "2026-10-08", "tags": []string{"go"}, "count": 2}
	item := &Content{ID: "posts-hello", Path: "posts/hello.md", Data: raw}
	if err := item.ValidateSchema(schema); err != nil {
		t.Fatal(err)
	}
	result, err := Decode[typedPost](item)
	if err != nil {
		t.Fatal(err)
	}
	if result.Data.Date.Year() != 2026 || result.Data.Count != 2 || result.Data.Rating != nil || result.ID != item.ID {
		t.Fatalf("decoded: %+v", result)
	}
	result.Data.Tags[0] = "changed"
	if raw["tags"].([]string)[0] != "go" {
		t.Fatal("typed data aliases source")
	}
	if schema.Fields["author"].Reference != "authors" || schema.Fields["tags"].ElementType != FieldTypeString {
		t.Fatalf("schema: %+v", schema)
	}
	item.Data["title"] = "x"
	if err := item.ValidateSchema(schema); err == nil || !strings.Contains(err.Error(), "too short") {
		t.Fatalf("validator: %v", err)
	}
}

func TestTypedDecodeFailures(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		data    map[string]any
		message string
	}{
		{"missing", map[string]any{"author": "a"}, "title"},
		{"unknown", map[string]any{"title": "Hello", "author": "a", "typo": true}, "typo"},
		{"wrong scalar", map[string]any{"title": "Hello", "author": "a", "count": "two"}, "count"},
		{"fractional", map[string]any{"title": "Hello", "author": "a", "count": 1.5}, "count"},
		{"null", map[string]any{"title": nil, "author": "a"}, "null"},
		{"date", map[string]any{"title": "Hello", "author": "a", "date": "10/08/2026"}, "date"},
		{"slice element", map[string]any{"title": "Hello", "author": "a", "tags": []any{"ok", 42}}, "string"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := DecodeData[typedPost](tc.data)
			if err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("got %v", err)
			}
		})
	}
	type small struct {
		N int8 `json:"n"`
	}
	if _, err := DecodeData[small](map[string]any{"n": 128}); err == nil {
		t.Fatal("integer overflow accepted")
	}
	if _, err := DecodeData[typedPost](map[string]any{"title": "ok", "Title": "bad", "author": "a"}); err == nil {
		t.Fatal("case-insensitive alias accepted")
	}
}
func TestTypedModelsRejectUnsupportedDefinitions(t *testing.T) {
	t.Parallel()
	type nested struct{ Nested struct{ Title string } }
	type recursive []recursive
	type recursiveModel struct{ Items recursive }
	collision := reflect.StructOf([]reflect.StructField{
		{Name: "A", Type: reflect.TypeFor[string](), Tag: `json:"same"`},
		{Name: "B", Type: reflect.TypeFor[string](), Tag: `json:"same"`},
	})
	type badRef struct {
		Count int `content:"ref=authors"`
	}
	for _, typ := range []reflect.Type{
		reflect.TypeFor[string](), reflect.TypeFor[*typedPost](), reflect.TypeFor[nested](),
		collision, reflect.TypeFor[badRef](), reflect.TypeFor[recursiveModel](),
	} {
		if _, err := modelFields(typ); err == nil {
			t.Errorf("accepted %s", typ)
		}
	}
}
func TestTypedQueryPreservesPagination(t *testing.T) {
	t.Parallel()
	type model struct {
		Title string `json:"title" content:"required"`
	}
	col := NewCollection(CollectionConfig{Name: "posts"})
	for _, id := range []string{"b", "a"} {
		if err := col.AddContent(&Content{ID: id, Data: map[string]any{"title": id}}); err != nil {
			t.Fatal(err)
		}
	}
	entries, total, err := QueryTyped[model](col, ContentQuery{Limit: 1, Offset: 1})
	if err != nil || total != 2 || len(entries) != 1 || entries[0].ID != "b" {
		t.Fatalf("query: %+v %d %v", entries, total, err)
	}
	col.Contents["b"].Data["title"] = 123
	if entries, _, err := QueryTyped[model](col, ContentQuery{}); err == nil || entries != nil {
		t.Fatal("returned partial typed query")
	}
}

func relationLayer(t *testing.T) (*ContentLayer, fstest.MapFS) {
	t.Helper()
	files := fstest.MapFS{
		"authors/regi.md": {Data: []byte("---\nname: Regi\n---\nAuthor")},
		"posts/hello.md":  {Data: []byte("---\ntitle: Hello\nauthor: authors-regi\nrelated: [posts-next]\n---\nHello")},
		"posts/next.md":   {Data: []byte("---\ntitle: Next\nauthor: authors-regi\nrelated: [posts-hello]\n---\nNext")},
	}
	schema, err := SchemaFor[typedPost]("Post")
	if err != nil {
		t.Fatal(err)
	}
	layer := NewContentLayer(&ContentConfig{FS: files})
	layer.DefineCollection(CollectionConfig{Name: "posts", Schema: schema})
	if err := layer.LoadContent(); err != nil {
		t.Fatal(err)
	}
	return layer, files
}
func TestRelationsResolveCyclesAndRejectBrokenReload(t *testing.T) {
	t.Parallel()
	layer, files := relationLayer(t)
	snapshot := layer.GetAllCollections()
	post := snapshot["posts"].Contents["posts-hello"]
	targets, err := ResolveReferences(snapshot, post, "related")
	if err != nil || len(targets) != 1 || targets[0].ID != "posts-next" {
		t.Fatalf("resolve: %+v %v", targets, err)
	}
	authors, err := ResolveReferences(snapshot, post, "author")
	if err != nil || authors[0].ID != "authors-regi" {
		t.Fatalf("author: %+v %v", authors, err)
	}
	delete(files, "authors/regi.md")
	err = layer.LoadContent()
	if err == nil || !strings.Contains(err.Error(), "authors") {
		t.Fatalf("missing target: %v", err)
	}
	current := layer.GetAllCollections()
	if current["posts"] != snapshot["posts"] || current["authors"] != snapshot["authors"] {
		t.Fatal("broken reload replaced good snapshot")
	}
	files["authors/regi.md"] = &fstest.MapFile{Data: []byte("---\nname: Updated\n---\nAuthor")}
	if err := layer.LoadContent(); err != nil {
		t.Fatal(err)
	}
	current = layer.GetAllCollections()
	if current["authors"] == snapshot["authors"] {
		t.Fatal("valid reload not installed")
	}
	if snapshot["authors"].Contents["authors-regi"].GetString("name") != "Regi" {
		t.Fatal("old snapshot mutated")
	}
}
func TestRelationDiagnosticsAndElementValidation(t *testing.T) {
	t.Parallel()
	layer, _ := relationLayer(t)
	snapshot := layer.GetAllCollections()
	post := snapshot["posts"].Contents["posts-hello"]
	post.Data["related"] = []string{"posts-next", "posts-next"}
	if err := ValidateRelations(snapshot); err == nil || !strings.Contains(err.Error(), "duplicate reference") {
		t.Fatal(err)
	}
	post.Data["related"] = []string{"not-a-slug"}
	first := ValidateRelations(snapshot)
	if first == nil || !strings.Contains(first.Error(), "posts/hello.md") || !strings.Contains(first.Error(), "not-a-slug") {
		t.Fatal(first)
	}
	for range 5 {
		if next := ValidateRelations(snapshot); next.Error() != first.Error() {
			t.Fatal("unstable diagnostics")
		}
	}
	schema := NewSchema("Tags").Field("tags", FieldTypeSlice).Elements(FieldTypeString).Build().Build()
	if err := (&Content{Data: map[string]any{"tags": []any{"go", 42}}}).ValidateSchema(schema); err == nil {
		t.Fatal("accepted wrong slice element")
	}
}

func TestRelationIDCollisionsRejectReload(t *testing.T) {
	t.Parallel()
	layer, files := relationLayer(t)
	old := layer.GetAllCollections()
	files["authors/Regi.md"] = &fstest.MapFile{Data: []byte("---\nname: Other\n---\nOther")}
	err := layer.LoadContent()
	if err == nil || !strings.Contains(err.Error(), "collides") {
		t.Fatalf("collision not rejected: %v", err)
	}
	if layer.GetAllCollections()["authors"] != old["authors"] {
		t.Fatal("collision replaced old snapshot")
	}
}
