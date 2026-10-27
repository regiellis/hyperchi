package content

import (
	"strings"
	"testing"
	"time"
)

func TestParseTime(t *testing.T) {
	utc := func(y int, m time.Month, d, h, min, s int) time.Time {
		return time.Date(y, m, d, h, min, s, 0, time.UTC)
	}
	for _, tc := range []struct {
		in   string
		want time.Time
		ok   bool
	}{
		{"2024-01-15T10:00:00Z", utc(2024, 1, 15, 10, 0, 0), true},
		{"2024-01-15T10:00:00.5Z", utc(2024, 1, 15, 10, 0, 0).Add(500 * time.Millisecond), true},
		{"2024-01-15T12:00:00+02:00", utc(2024, 1, 15, 10, 0, 0), true},
		{"2024-01-15", utc(2024, 1, 15, 0, 0, 0), true},
		{" 2024-01-15 ", utc(2024, 1, 15, 0, 0, 0), true},
		{"2024-01-15 10:30", utc(2024, 1, 15, 10, 30, 0), true},
		{"2024-01-15T10:30", utc(2024, 1, 15, 10, 30, 0), true},
		{"2024-01-15 10:30:45", utc(2024, 1, 15, 10, 30, 45), true},
		{"2024-01-15T10:30:45", utc(2024, 1, 15, 10, 30, 45), true},
		{"01/02/2024", time.Time{}, false}, // day/month order is ambiguous
		{"15 Jan 2024", time.Time{}, false},
		{"2024-13-01", time.Time{}, false},
		{"", time.Time{}, false},
	} {
		got, ok := ParseTime(tc.in)
		if ok != tc.ok || !got.Equal(tc.want) {
			t.Errorf("ParseTime(%q) = %v, %t; want %v, %t", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestGetTimeParsesDateStrings(t *testing.T) {
	c := &Content{Data: map[string]any{"a": "2024-01-15", "b": "soon", "c": 5}}
	if got := c.GetTime("a"); !got.Equal(time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("GetTime(date string) = %v", got)
	}
	for _, field := range []string{"b", "c", "missing"} {
		if got := c.GetTime(field); !got.IsZero() {
			t.Errorf("GetTime(%q) = %v, want zero", field, got)
		}
	}
}

// Front matter has no date type. Loaded through LoadContent, time-typed schema
// fields hold time.Time while other fields keep their text.
func TestLoadContentParsesDatesForTimeFields(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "blog/new.md", "---\ntitle: Hello World\nauthor: ann\npublished: true\npublishedAt: 2024-03-01T09:00:00Z\ncategory: 2024-01-01\nslugDate: \"2024-01-01\"\n---\nBody")
	writeFile(t, root, "blog/old.md", "---\ntitle: Older Post\nauthor: ann\npublished: true\npublishedAt: \"2023-12-24\"\n---\nBody")
	writeFile(t, root, "blog/bad.md", "---\ntitle: Bad Date\nauthor: ann\npublishedAt: someday\n---\nBody")
	writeFile(t, root, "projects/hyper.md", "---\nname: Hyper\ndescription: A framework project\nstartDate: 2024-01-01\nendDate: \"2024-06-01 09:30\"\n---\n")
	writeFile(t, root, "events/launch.md", "---\ntitle: Launch Party\norganizer: me\nstartTime: 2999-01-01 18:00\nendTime: 2999-01-01T20:00:00Z\n---\n")

	cl := NewContentLayer(&ContentConfig{BaseDir: root})
	cl.DefineCollection(CollectionConfig{Name: "blog", Schema: BlogPostSchema()})
	cl.DefineCollection(CollectionConfig{Name: "projects", Schema: ProjectSchema()})
	cl.DefineCollection(CollectionConfig{Name: "events", Schema: EventSchema()})

	err := cl.LoadContent()
	if err == nil || !strings.Contains(err.Error(), "bad.md") || !strings.Contains(err.Error(), "publishedAt") {
		t.Fatalf("LoadContent error = %v, want only bad.md's publishedAt rejected", err)
	}
	if n := strings.Count(err.Error(), "validation error"); n != 1 {
		t.Fatalf("%d files rejected, want 1: %v", n, err)
	}

	blog, _ := cl.GetCollection("blog")
	post, ok := blog.GetContent("blog-new")
	if !ok {
		t.Fatalf("blog/new.md was rejected; loaded %v", keys(blog.Contents))
	}
	if at, ok := post.Get("publishedAt").(time.Time); !ok || !at.Equal(time.Date(2024, 3, 1, 9, 0, 0, 0, time.UTC)) {
		t.Errorf("publishedAt = %#v, want a time.Time", post.Get("publishedAt"))
	}
	if post.Get("category") != "2024-01-01" || post.Get("slugDate") != "2024-01-01" {
		t.Errorf("non-time fields changed: category %#v, slugDate %#v", post.Get("category"), post.Get("slugDate"))
	}

	// Parsed dates sort as times.
	page, err := blog.Query(ContentQuery{Sort: []SortCriteria{{Field: "publishedAt", Order: "desc"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Contents) != 2 || page.Contents[0].Slug != "new" || page.Contents[1].Slug != "old" {
		t.Errorf("sorted by publishedAt = %v", page.Contents)
	}

	projects, _ := cl.GetCollection("projects")
	hyper, ok := projects.GetContentBySlug("hyper")
	if !ok {
		t.Fatal("project rejected")
	}
	if got := hyper.GetTime("endDate"); !got.Equal(time.Date(2024, 6, 1, 9, 30, 0, 0, time.UTC)) {
		t.Errorf("endDate = %v", got)
	}

	events, _ := cl.GetCollection("events")
	launch, ok := events.GetContentBySlug("launch")
	if !ok {
		t.Fatal("event rejected")
	}
	if got := launch.GetTime("startTime"); !got.Equal(time.Date(2999, 1, 1, 18, 0, 0, 0, time.UTC)) {
		t.Errorf("startTime = %v", got)
	}
}
