package content

import (
	"bytes"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"strings"
	"testing"
)

func TestGenerateTypesDeterministicAndCompiles(t *testing.T) {
	t.Parallel()
	post := NewSchema("Post").
		Field("title", FieldTypeString).Required().
		Field("published_at", FieldTypeTime).
		Field("author_id", FieldTypeString).References("authors").
		Field("related", FieldTypeSlice).References("posts").
		Field("tags", FieldTypeSlice).Elements(FieldTypeString).
		Field("metadata", FieldTypeMap).
		Build().Build()
	author := NewSchema("Author").Field("name", FieldTypeString).Required().Build().Build()
	schemas := map[string]*ContentSchema{"Post": post, "Author": author}
	generated, err := GenerateTypes("models", schemas)
	if err != nil {
		t.Fatal(err)
	}
	for range 5 {
		again, err := GenerateTypes("models", map[string]*ContentSchema{"Author": author, "Post": post})
		if err != nil || !bytes.Equal(generated, again) {
			t.Fatal("nondeterministic generator", err)
		}
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "content_gen.go", generated, parser.AllErrors)
	if err != nil {
		t.Fatal(err)
	}
	cfg := types.Config{Importer: importer.Default()}
	pkg, err := cfg.Check("models", fset, []*ast.File{file}, nil)
	if err != nil {
		t.Fatalf("generated code does not compile: %v\n%s", err, generated)
	}
	st := pkg.Scope().Lookup("Post").Type().Underlying().(*types.Struct)
	fields := map[string]string{}
	tags := map[string]string{}
	for i := 0; i < st.NumFields(); i++ {
		fields[st.Field(i).Name()] = st.Field(i).Type().String()
		tags[st.Field(i).Name()] = st.Tag(i)
	}
	if fields["Related"] != "[]string" || fields["Tags"] != "[]string" || fields["PublishedAt"] != "time.Time" || !strings.Contains(tags["AuthorID"], "ref=authors") {
		t.Fatalf("generated fields: %+v %+v", fields, tags)
	}
}
func TestGenerateTypesRejectsInvalidSchemas(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, pkg string
		schemas   map[string]*ContentSchema
	}{
		{"keyword package", "type", nil},
		{"invalid type", "models", map[string]*ContentSchema{"lowercase": {}}},
		{"nil schema", "models", map[string]*ContentSchema{"Post": nil}},
		{"field collision", "models", map[string]*ContentSchema{"Post": {Fields: map[string]FieldDefinition{"a-b": {Type: FieldTypeString}, "a_b": {Type: FieldTypeString}}}}},
		{"unknown type", "models", map[string]*ContentSchema{"Post": {Fields: map[string]FieldDefinition{"title": {Type: "invalid"}}}}},
		{"missing required", "models", map[string]*ContentSchema{"Post": {Required: []string{"missing"}}}},
		{"invalid reference", "models", map[string]*ContentSchema{"Post": {Fields: map[string]FieldDefinition{"author": {Type: FieldTypeInt, Reference: "authors"}}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if output, err := GenerateTypes(tc.pkg, tc.schemas); err == nil || output != nil {
				t.Fatalf("accepted invalid schema: %s %v", output, err)
			}
		})
	}
}
