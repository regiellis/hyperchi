package content

import (
	"reflect"
	"testing"
	"time"
)

var epoch = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

// postsFixture builds a schema-less collection of five posts with mixed data.
func postsFixture(t *testing.T) *Collection {
	t.Helper()
	c := NewCollection(CollectionConfig{Name: "posts"})
	for i, p := range []struct {
		id   string
		body string
		data map[string]any
	}{
		{"p1", "Intro to Go", map[string]any{"title": "Go Basics", "author": "ann", "published": true, "price": 10, "tags": []string{"go", "intro"}}},
		{"p2", "Web servers", map[string]any{"title": "HTTP in Go", "author": "bob", "published": true, "price": 9.5, "tags": []string{"go", "web"}}},
		{"p3", "Drafting", map[string]any{"title": "Draft Ideas", "author": "ann", "published": false, "price": 12, "tags": []string{"misc"}}},
		{"p4", "Templates", map[string]any{"title": "htmx Patterns", "author": "cy", "published": true, "price": 2.25}},
		{"p5", "Nothing", map[string]any{"author": "bob", "published": true}},
	} {
		if err := c.AddContent(&Content{
			ID: p.id, Slug: "slug-" + p.id, Path: "posts/" + p.id + ".md", Body: p.body,
			CreatedAt: epoch.Add(time.Duration(i) * time.Hour), UpdatedAt: epoch.Add(time.Duration(5-i) * time.Hour),
			Data: p.data,
		}); err != nil {
			t.Fatal(err)
		}
	}
	return c
}

func ids(contents []*Content) []string {
	out := make([]string, len(contents))
	for i, c := range contents {
		out[i] = c.ID
	}
	return out
}

func TestQueryFilterSearchSort(t *testing.T) {
	for _, tc := range []struct {
		name  string
		query ContentQuery
		want  []string
	}{
		{"no criteria returns all in ID order", ContentQuery{}, []string{"p1", "p2", "p3", "p4", "p5"}},
		{"where equality", ContentQuery{Where: map[string]any{"author": "ann"}}, []string{"p1", "p3"}},
		{"where combines with AND", ContentQuery{Where: map[string]any{"author": "bob", "published": true}}, []string{"p2", "p5"}},
		{"where contains is case-insensitive", ContentQuery{Where: map[string]any{"title": "*GO*"}}, []string{"p1", "p2"}},
		{"where prefix", ContentQuery{Where: map[string]any{"title": "draft*"}}, []string{"p3"}},
		{"where suffix", ContentQuery{Where: map[string]any{"title": "*patterns"}}, []string{"p4"}},
		{"wildcard does not match non-strings", ContentQuery{Where: map[string]any{"price": "*1*"}}, []string{}},
		{"where on missing field", ContentQuery{Where: map[string]any{"missing": "x"}}, []string{}},
		{"where nil matches missing field", ContentQuery{Where: map[string]any{"title": nil}}, []string{"p5"}},
		{"where tag intersection", ContentQuery{Where: map[string]any{"tags": []string{"web", "misc"}}}, []string{"p2", "p3"}},
		{"search body", ContentQuery{Search: "SERVERS"}, []string{"p2"}},
		{"search string fields", ContentQuery{Search: "htmx"}, []string{"p4"}},
		{"search string slices", ContentQuery{Search: "intro"}, []string{"p1"}},
		{"search with filter", ContentQuery{Search: "go", Where: map[string]any{"author": "bob"}}, []string{"p2"}},
		{"sort string asc", ContentQuery{Sort: []SortCriteria{{Field: "title", Order: "asc"}}}, []string{"p5", "p3", "p1", "p2", "p4"}},
		{"sort mixed int and float desc", ContentQuery{Sort: []SortCriteria{{Field: "price", Order: "desc"}}}, []string{"p3", "p1", "p2", "p4", "p5"}},
		{"sort mixed int and float asc", ContentQuery{Sort: []SortCriteria{{Field: "price"}}}, []string{"p5", "p4", "p2", "p1", "p3"}},
		{"sort createdAt desc", ContentQuery{Sort: []SortCriteria{{Field: "createdAt", Order: "desc"}}}, []string{"p5", "p4", "p3", "p2", "p1"}},
		{"sort updatedAt asc", ContentQuery{Sort: []SortCriteria{{Field: "updatedAt"}}}, []string{"p5", "p4", "p3", "p2", "p1"}},
		{"sort by system fields", ContentQuery{Sort: []SortCriteria{{Field: "slug", Order: "desc"}}}, []string{"p5", "p4", "p3", "p2", "p1"}},
		{"multi-key sort keeps ties stable", ContentQuery{Sort: []SortCriteria{{Field: "author"}, {Field: "published", Order: "desc"}}}, []string{"p1", "p3", "p2", "p5", "p4"}},
		{"sort id desc", ContentQuery{Sort: []SortCriteria{{Field: "id", Order: "desc"}}}, []string{"p5", "p4", "p3", "p2", "p1"}},
		{"sort path asc", ContentQuery{Sort: []SortCriteria{{Field: "path"}}}, []string{"p1", "p2", "p3", "p4", "p5"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, err := postsFixture(t).Query(tc.query)
			if err != nil {
				t.Fatal(err)
			}
			if got := ids(res.Contents); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("IDs = %v, want %v", got, tc.want)
			}
			if res.Total != len(tc.want) {
				t.Errorf("Total = %d, want %d", res.Total, len(tc.want))
			}
		})
	}
}

func TestQueryPagination(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		limit, offset         int
		want                  []string
		page, pageSize, pages int
		hasNext, hasPrev      bool
	}{
		{"first page", 2, 0, []string{"p1", "p2"}, 1, 2, 3, true, false},
		{"middle page", 2, 2, []string{"p3", "p4"}, 2, 2, 3, true, true},
		{"last partial page", 2, 4, []string{"p5"}, 3, 2, 3, false, true},
		{"offset past end", 2, 10, []string{}, 3, 2, 3, false, true},
		{"negative offset clamps to start", 2, -3, []string{"p1", "p2"}, 1, 2, 3, true, false},
		{"no limit returns everything", 0, 0, []string{"p1", "p2", "p3", "p4", "p5"}, 1, 5, 1, false, false},
		{"exact fit", 5, 0, []string{"p1", "p2", "p3", "p4", "p5"}, 1, 5, 1, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, err := postsFixture(t).Query(ContentQuery{Limit: tc.limit, Offset: tc.offset})
			if err != nil {
				t.Fatal(err)
			}
			if got := ids(res.Contents); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("IDs = %v, want %v", got, tc.want)
			}
			if res.Total != 5 || res.Page != tc.page || res.PageSize != tc.pageSize || res.TotalPages != tc.pages ||
				res.HasNext != tc.hasNext || res.HasPrev != tc.hasPrev {
				t.Errorf("result = total %d page %d size %d pages %d next %t prev %t",
					res.Total, res.Page, res.PageSize, res.TotalPages, res.HasNext, res.HasPrev)
			}
		})
	}
}

// Pages taken without a sort must partition the result set: map iteration
// order must not leak into the result and make pages overlap or skip items.
func TestQueryUnsortedPagesAreDeterministic(t *testing.T) {
	c := postsFixture(t)
	for run := 0; run < 20; run++ {
		var seen []string
		for offset := 0; offset < 5; offset += 2 {
			res, err := c.Query(ContentQuery{Limit: 2, Offset: offset})
			if err != nil {
				t.Fatal(err)
			}
			seen = append(seen, ids(res.Contents)...)
		}
		if want := []string{"p1", "p2", "p3", "p4", "p5"}; !reflect.DeepEqual(seen, want) {
			t.Fatalf("run %d: pages = %v, want %v", run, seen, want)
		}
	}
}

func TestQuerySelectFields(t *testing.T) {
	c := postsFixture(t)
	original, _ := c.GetContent("p1")
	original.HTML = "<p>x</p>"

	res, err := c.Query(ContentQuery{Where: map[string]any{"author": "ann"}, Select: []string{"title", "body", "html", "nope"}})
	if err != nil {
		t.Fatal(err)
	}
	got := res.Contents[0]
	if got == original {
		t.Fatal("selection returned the stored pointer, not a projection")
	}
	if got.ID != "p1" || got.Slug != "slug-p1" || got.Collection != "posts" || got.Path != "posts/p1.md" || !got.CreatedAt.Equal(epoch) {
		t.Errorf("system fields not carried: %+v", got)
	}
	if got.Body != "Intro to Go" || got.HTML != "<p>x</p>" {
		t.Errorf("body/html = %q/%q", got.Body, got.HTML)
	}
	if !reflect.DeepEqual(got.Data, map[string]any{"title": "Go Basics"}) {
		t.Errorf("data = %v, want only title", got.Data)
	}

	res, err = c.Query(ContentQuery{Where: map[string]any{"author": "ann"}, Select: []string{"author"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Contents[0].Body != "" || res.Contents[0].HTML != "" {
		t.Error("unselected body/html leaked into the projection")
	}
	if original.GetString("title") != "Go Basics" || original.Body == "" {
		t.Error("projection mutated the stored content")
	}
}

func TestCompareValues(t *testing.T) {
	c := &Collection{}
	later := epoch.Add(time.Minute)
	for _, tc := range []struct {
		name string
		a, b any
		want int
	}{
		{"nil nil", nil, nil, 0},
		{"nil first", nil, 1, -1},
		{"nil last", "a", nil, 1},
		{"strings", "a", "b", -1},
		{"equal strings", "x", "x", 0},
		{"times", later, epoch, 1},
		{"equal times", epoch, epoch, 0},
		{"ints", 2, 10, -1},
		{"int vs float", 10, 9.5, 1},
		{"float vs int", 2.5, 3, -1},
		{"equal int and float", 3, 3.0, 0},
		{"floats", 1.5, 1.25, 1},
		{"other numeric kinds", int64(5), uint8(7), -1},
		{"bools fall back to text", false, true, -1},
		{"mixed kinds fall back to text", "B", true, -1},
		{"unknown types compare equal", []int{1}, []int{2}, 0},
	} {
		if got := c.compareValues(tc.a, tc.b); got != tc.want {
			t.Errorf("%s: compareValues(%v, %v) = %d, want %d", tc.name, tc.a, tc.b, got, tc.want)
		}
	}
}

func TestCollectionLookups(t *testing.T) {
	c := postsFixture(t)
	if got, ok := c.GetContent("p3"); !ok || got.GetString("title") != "Draft Ideas" {
		t.Errorf("GetContent(p3) = %v, %t", got, ok)
	}
	if _, ok := c.GetContent("nope"); ok {
		t.Error("GetContent found a missing ID")
	}
	if got, ok := c.GetContentBySlug("slug-p4"); !ok || got.ID != "p4" {
		t.Errorf("GetContentBySlug = %v, %t", got, ok)
	}
	if _, ok := c.GetContentBySlug("nope"); ok {
		t.Error("GetContentBySlug found a missing slug")
	}
	c.RemoveContent("p4")
	if _, ok := c.GetContentBySlug("slug-p4"); ok {
		t.Error("RemoveContent left the item")
	}
	if got := ids(c.GetAllContent()); !reflect.DeepEqual(got, []string{"p1", "p2", "p3", "p5"}) {
		t.Errorf("GetAllContent = %v", got)
	}
}

func TestAddContentStampsCollectionName(t *testing.T) {
	c := NewCollection(CollectionConfig{Name: "notes"})
	item := &Content{ID: "n1", Collection: "elsewhere"}
	if err := c.AddContent(item); err != nil {
		t.Fatal(err)
	}
	if item.Collection != "notes" {
		t.Errorf("Collection = %q, want notes", item.Collection)
	}
}

func TestCollectionStats(t *testing.T) {
	schema := NewSchema("s").
		Field("author", FieldTypeString).
		Field("tags", FieldTypeSlice).
		Field("price", FieldTypeAny).
		Build().Build()
	c := NewCollection(CollectionConfig{Name: "posts", Schema: schema})
	for id, d := range map[string]map[string]any{
		"a": {"author": "ann", "tags": []string{"go", "web"}},
		"b": {"author": "ann", "tags": []string{"go"}},
		"c": {"author": "bob"},
		"d": {"author": ""},
	} {
		if err := c.AddContent(&Content{ID: id, Data: d}); err != nil {
			t.Fatal(err)
		}
	}

	stats := c.GetStats()
	if stats.TotalContent != 4 || stats.Collections != 1 || stats.LastUpdated.IsZero() {
		t.Errorf("stats = %+v", stats)
	}
	want := map[string]int{"author_count": 2, "author_max_count": 2, "tags_count": 2, "tags_max_count": 2}
	if !reflect.DeepEqual(stats.CustomStats, want) {
		t.Errorf("CustomStats = %v, want %v", stats.CustomStats, want)
	}

	if got := postsFixture(t).GetStats(); got.TotalContent != 5 || len(got.CustomStats) != 0 {
		t.Errorf("schema-less stats = %+v", got)
	}
}

func TestContentLayerCollections(t *testing.T) {
	cl := NewContentLayer(DefaultContentConfig())
	defined := cl.DefineCollection(CollectionConfig{Name: "posts", Type: ContentTypeMDX})
	got, ok := cl.GetCollection("posts")
	if !ok || got != defined || got.Type != ContentTypeMDX || got.Contents == nil {
		t.Fatalf("GetCollection = %+v, %t", got, ok)
	}
	if _, ok := cl.GetCollection("missing"); ok {
		t.Error("found an undefined collection")
	}
	if all := cl.GetAllCollections(); len(all) != 1 || all["posts"] != defined {
		t.Errorf("GetAllCollections = %v", all)
	}
}
