package content

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestContentAccessors(t *testing.T) {
	when := time.Date(2025, 5, 1, 0, 0, 0, 0, time.UTC)
	c := &Content{Data: map[string]any{
		"title": "T", "count": 3, "on": true, "at": when,
		"anys": []any{"a", 1, "b"}, "strs": []string{"x", "y"}, "ints": []int{1, 2},
		"arr": [2]string{"p", "q"}, "scalar": "not a slice",
	}}

	if c.GetString("title") != "T" || c.GetString("count") != "" {
		t.Error("GetString")
	}
	if c.GetInt("count") != 3 || c.GetInt("title") != 0 {
		t.Error("GetInt")
	}
	if !c.GetBool("on") || c.GetBool("title") {
		t.Error("GetBool")
	}
	if !c.GetTime("at").Equal(when) || !c.GetTime("title").IsZero() {
		t.Error("GetTime")
	}

	for field, want := range map[string][]any{
		"anys":    {"a", 1, "b"},
		"strs":    {"x", "y"},
		"ints":    {1, 2},
		"arr":     {"p", "q"},
		"scalar":  nil,
		"missing": nil,
	} {
		if got := c.GetSlice(field); !reflect.DeepEqual(got, want) {
			t.Errorf("GetSlice(%q) = %#v, want %#v", field, got, want)
		}
	}

	for field, want := range map[string][]string{
		"strs":   {"x", "y"},
		"anys":   {"a", "b"},
		"scalar": nil,
	} {
		if got := c.GetStringSlice(field); !reflect.DeepEqual(got, want) {
			t.Errorf("GetStringSlice(%q) = %#v, want %#v", field, got, want)
		}
	}

	empty := &Content{}
	if empty.Has("x") || empty.Get("x") != nil {
		t.Error("nil Data reported a field")
	}
	empty.Set("x", 1)
	if !empty.Has("x") || empty.Get("x") != 1 {
		t.Error("Set on nil Data did not store the value")
	}
}

func TestSchemaBuilder(t *testing.T) {
	check := func(any) error { return nil }
	tr := func(c *Content) *Content { return c }
	v := func(*Content) error { return nil }

	s := NewSchema("post").
		Field("title", FieldTypeString).Required().MinLength(2).MaxLength(10).Description("Title").
		Field("score", FieldTypeInt).Min(1).Max(5).Default(3).Validate(check).
		Build().
		Required("title").
		Validate(v).
		Transform(tr).
		Build()

	if s.Name != "post" || !reflect.DeepEqual(s.Required, []string{"title"}) || len(s.Validators) != 1 || s.Transformer == nil {
		t.Fatalf("schema = %+v", s)
	}
	title := s.Fields["title"]
	if title.Type != FieldTypeString || !title.Required || title.Description != "Title" ||
		!reflect.DeepEqual(title.Constraints, map[string]any{"minLength": 2, "maxLength": 10}) {
		t.Errorf("title = %+v", title)
	}
	score := s.Fields["score"]
	if score.Type != FieldTypeInt || score.Required || score.Default != 3 || score.Validator == nil ||
		!reflect.DeepEqual(score.Constraints, map[string]any{"min": 1, "max": 5}) {
		t.Errorf("score = %+v", score)
	}
}

func TestValidateSchema(t *testing.T) {
	errOdd := errors.New("must be even")
	errRule := errors.New("schema rule")
	schema := NewSchema("s").
		Field("title", FieldTypeString).MinLength(2).MaxLength(5).
		Field("count", FieldTypeInt).Min(1).Max(10).Validate(func(v any) error {
		if n, ok := v.(int); ok && n%2 == 1 {
			return errOdd
		}
		return nil
	}).
		Field("price", FieldTypeFloat).Min(0.5).Max(9.5).
		Field("tags", FieldTypeSlice).MaxLength(2).
		Field("meta", FieldTypeMap).
		Field("on", FieldTypeBool).
		Field("at", FieldTypeTime).
		Field("anything", FieldTypeAny).
		Field("status", FieldTypeString).Default("draft").
		Field("owner", FieldTypeString).Required().
		Build().
		Required("title").
		Validate(func(c *Content) error {
			if c.GetString("title") == "nope" {
				return errRule
			}
			return nil
		}).
		Build()

	valid := func() map[string]any {
		return map[string]any{"title": "héllo", "owner": "me"}
	}

	for _, tc := range []struct {
		name    string
		edit    func(map[string]any)
		wantMsg string
		wantErr error
	}{
		{name: "minimal valid", edit: func(map[string]any) {}},
		{name: "all fields valid", edit: func(d map[string]any) {
			d["count"], d["price"], d["tags"], d["meta"] = 4, 9, []string{"a"}, map[string]any{}
			d["on"], d["at"], d["anything"] = true, time.Now(), struct{}{}
		}},
		{name: "float field accepts an int", edit: func(d map[string]any) { d["price"] = 2 }},
		{name: "float32 is a float", edit: func(d map[string]any) { d["price"] = float32(1.5) }},
		{name: "schema-level required", edit: func(d map[string]any) { delete(d, "title") }, wantMsg: "required field missing: title"},
		{name: "field-level required", edit: func(d map[string]any) { delete(d, "owner") }, wantMsg: "required field missing: owner"},
		{name: "wrong string type", edit: func(d map[string]any) { d["title"] = 5 }, wantMsg: "invalid type for field title: type error: expected string"},
		{name: "wrong int type", edit: func(d map[string]any) { d["count"] = "4" }, wantMsg: "expected int"},
		{name: "wrong float type", edit: func(d map[string]any) { d["price"] = "1" }, wantMsg: "expected float"},
		{name: "wrong bool type", edit: func(d map[string]any) { d["on"] = "yes" }, wantMsg: "expected bool"},
		{name: "text that is not a date is not a time", edit: func(d map[string]any) { d["at"] = "next tuesday" }, wantMsg: "expected time.Time"},
		{name: "date string for a time field is accepted", edit: func(d map[string]any) { d["at"] = "2024-01-01" }},
		{name: "wrong slice type", edit: func(d map[string]any) { d["tags"] = "a" }, wantMsg: "expected slice"},
		{name: "nil slice value is rejected, not a panic", edit: func(d map[string]any) { d["tags"] = nil }, wantMsg: "expected slice"},
		{name: "wrong map type", edit: func(d map[string]any) { d["meta"] = []string{} }, wantMsg: "expected map"},
		{name: "nil map value is rejected, not a panic", edit: func(d map[string]any) { d["meta"] = nil }, wantMsg: "expected map"},
		{name: "string too short", edit: func(d map[string]any) { d["title"] = "é" }, wantMsg: "value too short"},
		{name: "length counts characters not bytes", edit: func(d map[string]any) { d["title"] = "ééééé" }},
		{name: "string too long", edit: func(d map[string]any) { d["title"] = "toolong" }, wantMsg: "value too long"},
		{name: "slice too long", edit: func(d map[string]any) { d["tags"] = []any{1, 2, 3} }, wantMsg: "constraint violation for field tags"},
		{name: "int below min", edit: func(d map[string]any) { d["count"] = 0 }, wantMsg: "value too small"},
		{name: "int above max", edit: func(d map[string]any) { d["count"] = 12 }, wantMsg: "value too large"},
		{name: "int below float min", edit: func(d map[string]any) { d["price"] = 0 }, wantMsg: "value too small"},
		{name: "int above float max", edit: func(d map[string]any) { d["price"] = 10 }, wantMsg: "value too large"},
		{name: "field validator", edit: func(d map[string]any) { d["count"] = 3 }, wantMsg: "validation failed for field count: must be even"},
		{name: "schema validator error is returned as is", edit: func(d map[string]any) { d["title"] = "nope" }, wantErr: errRule},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := valid()
			tc.edit(d)
			c := &Content{Path: "posts/x.md", Data: d}
			err := c.ValidateSchema(schema)

			switch {
			case tc.wantErr != nil:
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
			case tc.wantMsg != "":
				var cerr *ContentError
				if !errors.As(err, &cerr) {
					t.Fatalf("err = %v, want a *ContentError", err)
				}
				if cerr.Type != "validation" || cerr.Path != "posts/x.md" || !strings.Contains(err.Error(), tc.wantMsg) {
					t.Fatalf("err = %q, want validation error in posts/x.md containing %q", err, tc.wantMsg)
				}
			default:
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if c.GetString("status") != "draft" {
					t.Errorf("default not applied: status = %v", c.Get("status"))
				}
			}
		})
	}
}

func TestValidateSchemaKeepsExistingValueOverDefault(t *testing.T) {
	schema := NewSchema("s").Field("status", FieldTypeString).Default("draft").Build().Build()
	c := &Content{Data: map[string]any{"status": "live"}}
	if err := c.ValidateSchema(schema); err != nil {
		t.Fatal(err)
	}
	if c.GetString("status") != "live" {
		t.Errorf("status = %q, default overwrote the given value", c.GetString("status"))
	}
}

func TestContentErrorMessage(t *testing.T) {
	if got := (&ContentError{Type: "parse", Message: "bad", Path: "a.md"}).Error(); got != "parse error in a.md: bad" {
		t.Errorf("with path: %q", got)
	}
	if got := (&ContentError{Type: "parse", Message: "bad"}).Error(); got != "parse error: bad" {
		t.Errorf("without path: %q", got)
	}
}

func TestAddContentValidatesThenTransforms(t *testing.T) {
	schema := NewSchema("s").
		Field("title", FieldTypeString).Required().
		Build().
		Transform(func(c *Content) *Content {
			return &Content{ID: c.ID + "-t", Data: map[string]any{"title": strings.ToUpper(c.GetString("title"))}}
		}).
		Build()
	coll := NewCollection(CollectionConfig{Name: "posts", Schema: schema})

	if err := coll.AddContent(&Content{ID: "bad", Data: map[string]any{}}); err == nil {
		t.Fatal("invalid content was accepted")
	}
	if len(coll.Contents) != 0 {
		t.Fatal("invalid content was stored")
	}

	if err := coll.AddContent(&Content{ID: "ok", Data: map[string]any{"title": "hi"}}); err != nil {
		t.Fatal(err)
	}
	got, ok := coll.GetContent("ok-t")
	if !ok || got.GetString("title") != "HI" || got.Collection != "posts" {
		t.Fatalf("transformed content = %+v, %t", got, ok)
	}
}

func TestToString(t *testing.T) {
	when := time.Date(2024, 2, 3, 4, 5, 6, 0, time.UTC)
	for _, tc := range []struct {
		in   any
		want string
	}{
		{"s", "s"}, {7, "7"}, {1.5, "1.500000"}, {true, "true"}, {false, "false"},
		{when, "2024-02-03T04:05:06Z"}, {[]int{1}, ""},
	} {
		if got := toString(tc.in); got != tc.want {
			t.Errorf("toString(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestBlogPostSchema(t *testing.T) {
	coll := ExampleBlogSetup()
	if coll.Name != "blog" || coll.Type != ContentTypeMarkdown || coll.Schema == nil || coll.Schema.Name != "BlogPost" {
		t.Fatalf("blog setup = %+v", coll)
	}

	post := func(data map[string]any, body string) *Content {
		return &Content{ID: "p", Body: body, Data: data}
	}

	if err := coll.AddContent(post(map[string]any{"title": "Hi", "author": "a"}, "")); err == nil {
		t.Error("two-character title accepted")
	}
	if err := coll.AddContent(post(map[string]any{"title": "Hello", "author": "a", "published": true}, "")); err == nil ||
		!strings.Contains(err.Error(), "publishedAt") {
		t.Errorf("published without publishedAt: err = %v", err)
	}

	long := strings.Repeat("é", 250)
	p := post(map[string]any{"title": "Hello", "author": "a"}, long)
	if err := coll.AddContent(p); err != nil {
		t.Fatal(err)
	}
	if p.GetBool("published") || !p.Has("published") {
		t.Error("published default not applied")
	}
	excerpt := p.GetString("excerpt")
	if !utf8.ValidString(excerpt) || excerpt != strings.Repeat("é", 200)+"..." {
		t.Errorf("excerpt = %q (valid UTF-8: %t), want the first 200 characters", excerpt, utf8.ValidString(excerpt))
	}

	short := post(map[string]any{"title": "Hello", "author": "a", "published": true, "publishedAt": time.Now()}, "short")
	if err := coll.AddContent(short); err != nil {
		t.Fatal(err)
	}
	if short.Has("excerpt") {
		t.Error("excerpt generated for a short body")
	}
}

func TestProductSchema(t *testing.T) {
	coll := ExampleProductSetup()
	for _, tc := range []struct {
		name    string
		data    map[string]any
		wantErr string
	}{
		{"valid with integer price from front matter", map[string]any{"name": "Mug", "price": 10, "sku": "MUG-1"}, ""},
		{"valid float rating", map[string]any{"name": "Mug", "price": 4.5, "sku": "MUG-1", "rating": 4.5}, ""},
		{"integer rating above max", map[string]any{"name": "Mug", "price": 1.0, "sku": "MUG-1", "rating": 7}, "value too large"},
		{"negative price", map[string]any{"name": "Mug", "price": -1, "sku": "MUG-1"}, "value too small"},
		{"short sku", map[string]any{"name": "Mug", "price": 1.0, "sku": "M-"}, "at least 3"},
		{"sku without hyphen", map[string]any{"name": "Mug", "price": 1.0, "sku": "MUG1"}, "hyphen"},
		{"missing sku", map[string]any{"name": "Mug", "price": 1.0}, "sku"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &Content{ID: tc.name, Data: tc.data}
			err := coll.AddContent(c)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatal(err)
				}
				if c.GetBool("inStock") != true {
					t.Error("inStock default not applied")
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestDocumentationSchema(t *testing.T) {
	coll := ExampleDocsSetup()
	if coll.Type != ContentTypeMDX {
		t.Errorf("docs type = %q", coll.Type)
	}
	ok := &Content{ID: "d", Data: map[string]any{"title": "Getting Started", "section": "Intro"}}
	if err := coll.AddContent(ok); err != nil {
		t.Fatal(err)
	}
	if ok.GetString("level") != "beginner" || ok.GetInt("order") != 0 {
		t.Errorf("defaults = level %v order %v", ok.Get("level"), ok.Get("order"))
	}
	for name, data := range map[string]map[string]any{
		"unknown level":  {"title": "Getting Started", "section": "Intro", "level": "expert"},
		"negative order": {"title": "Getting Started", "section": "Intro", "order": -1},
		"short title":    {"title": "Go", "section": "Intro"},
	} {
		if err := coll.AddContent(&Content{ID: name, Data: data}); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestProjectSchema(t *testing.T) {
	s := ProjectSchema()
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	ok := &Content{Data: map[string]any{"name": "Hyper", "description": "A framework project", "startDate": start, "endDate": start.AddDate(0, 1, 0)}}
	if err := ok.ValidateSchema(s); err != nil {
		t.Fatal(err)
	}
	if ok.GetString("status") != "in-progress" || ok.GetBool("featured") {
		t.Errorf("defaults = %v", ok.Data)
	}
	backwards := &Content{Data: map[string]any{"name": "Hyper", "description": "A framework project", "startDate": start, "endDate": start.AddDate(0, -1, 0)}}
	if err := backwards.ValidateSchema(s); err == nil || !strings.Contains(err.Error(), "end date") {
		t.Errorf("end before start: err = %v", err)
	}
	badStatus := &Content{Data: map[string]any{"name": "Hyper", "description": "A framework project", "status": "done"}}
	if err := badStatus.ValidateSchema(s); err == nil {
		t.Error("unknown status accepted")
	}
}

func TestEventSchema(t *testing.T) {
	s := EventSchema()
	future := time.Now().Add(24 * time.Hour)
	event := func(start, end time.Time) *Content {
		return &Content{Data: map[string]any{"title": "Launch Party", "organizer": "me", "startTime": start, "endTime": end}}
	}
	ok := event(future, future.Add(time.Hour))
	if err := ok.ValidateSchema(s); err != nil {
		t.Fatal(err)
	}
	if ok.GetBool("registrationRequired") {
		t.Error("registrationRequired default should be false")
	}
	if err := event(future, future.Add(-time.Hour)).ValidateSchema(s); err == nil || !strings.Contains(err.Error(), "end time") {
		t.Errorf("end before start: err = %v", err)
	}
	past := time.Now().Add(-48 * time.Hour)
	if err := event(past, past.Add(time.Hour)).ValidateSchema(s); err == nil || !strings.Contains(err.Error(), "future") {
		t.Errorf("past event: err = %v", err)
	}
}

func TestRecipeSchema(t *testing.T) {
	s := RecipeSchema()
	recipe := func(ingredients, instructions any) *Content {
		return &Content{Data: map[string]any{
			"title": "Pancakes", "prepTime": 5, "servings": 2,
			"ingredients": ingredients, "instructions": instructions,
		}}
	}
	// Front matter lists parse as []string; they must count as ingredients.
	ok := recipe([]string{"flour", "egg"}, []any{"mix", "fry"})
	if err := ok.ValidateSchema(s); err != nil {
		t.Fatalf("[]string ingredients rejected: %v", err)
	}
	if ok.GetString("difficulty") != "easy" {
		t.Errorf("difficulty default = %v", ok.Get("difficulty"))
	}
	if err := recipe([]string{}, []string{"mix"}).ValidateSchema(s); err == nil || !strings.Contains(err.Error(), "ingredient") {
		t.Errorf("empty ingredients: err = %v", err)
	}
	if err := recipe([]string{"egg"}, []string{}).ValidateSchema(s); err == nil || !strings.Contains(err.Error(), "instruction") {
		t.Errorf("empty instructions: err = %v", err)
	}
	tooMany := recipe([]string{"egg"}, []string{"mix"})
	tooMany.Set("servings", 51)
	if err := tooMany.ValidateSchema(s); err == nil {
		t.Error("51 servings accepted")
	}
}

// A recipe written as a markdown file loads through its declared schema.
func TestRecipeLoadsFromFrontMatter(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "recipes/pancakes.md",
		"---\ntitle: Pancakes\nprepTime: 5\nservings: 4\ningredients: [flour, egg, milk]\ninstructions: [mix, fry]\ncuisine: \"French\"\n---\nServe warm.")
	cl := NewContentLayer(&ContentConfig{BaseDir: root})
	cl.DefineCollection(CollectionConfig{Name: "recipes", Schema: RecipeSchema()})
	if err := cl.LoadContent(); err != nil {
		t.Fatalf("LoadContent: %v", err)
	}
	recipes, _ := cl.GetCollection("recipes")
	got, ok := recipes.GetContentBySlug("pancakes")
	if !ok {
		t.Fatal("pancakes not loaded")
	}
	if got.GetInt("servings") != 4 || len(got.GetSlice("ingredients")) != 3 || got.GetString("cuisine") != "French" {
		t.Errorf("data = %v", got.Data)
	}
}

func TestExampleCompleteSetup(t *testing.T) {
	cl := ExampleCompleteSetup()
	want := map[string]string{"blog": "BlogPost", "products": "Product", "docs": "Documentation", "projects": "Project"}
	all := cl.GetAllCollections()
	if len(all) != len(want) {
		t.Fatalf("collections = %v", keys(all))
	}
	for name, schemaName := range want {
		c, ok := cl.GetCollection(name)
		if !ok || c.Schema == nil || c.Schema.Name != schemaName {
			t.Errorf("%s = %+v", name, c)
		}
	}
}
