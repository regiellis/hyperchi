package content_test

import (
	"fmt"
	"log"
	"time"

	"github.com/regiellis/hyperchi/hyperchi/content"
)

// Load a directory of content files, validate one collection against a
// schema, and query it.
func ExampleContentLayer_LoadContent() {
	layer := content.NewContentLayer(&content.ContentConfig{BaseDir: "testdata/site"})
	layer.DefineCollection(content.CollectionConfig{Name: "blog", Schema: content.BlogPostSchema()})
	if err := layer.LoadContent(); err != nil {
		log.Fatal(err)
	}

	blog, _ := layer.GetCollection("blog")
	page, err := blog.Query(content.ContentQuery{
		Where: map[string]any{"published": true},
		Sort:  []content.SortCriteria{{Field: "publishedAt", Order: "desc"}},
	})
	if err != nil {
		log.Fatal(err)
	}
	for _, post := range page.Contents {
		fmt.Println(post.GetTime("publishedAt").Format("2006-01-02"), post.GetString("title"), post.GetStringSlice("tags"))
	}

	pages, _ := layer.GetCollection("pages")
	fmt.Println("pages:", pages.Type, len(pages.Contents))
	// Output:
	// 2024-04-10 Second Post [go]
	// 2024-03-01 Hello, World [go htmx]
	// pages: html 3
}

// Reload the layer whenever a content file changes. LoadContent starts the
// watcher, and Close stops it.
func ExampleContentLayer_Close() {
	layer := content.NewContentLayer(&content.ContentConfig{
		BaseDir:         "content",
		WatchForChanges: true,
		WatchInterval:   time.Second,
		OnReload: func(changed []string, err error) {
			if err != nil {
				log.Printf("content reload: %v", err)
				return
			}
			log.Printf("reloaded after %d changed files", len(changed))
		},
	})
	if err := layer.LoadContent(); err != nil {
		log.Fatal(err)
	}
	defer func() {
		if err := layer.Close(); err != nil {
			log.Print(err)
		}
	}()
}

// Query filters, searches, sorts, and pages a collection.
func ExampleCollection_Query() {
	notes := content.NewCollection(content.CollectionConfig{Name: "notes"})
	for i, title := range []string{"Go generics", "HTMX swaps", "Go modules", "CSS layers"} {
		note := &content.Content{ID: fmt.Sprintf("n%d", i), Slug: fmt.Sprintf("note-%d", i)}
		note.Set("title", title)
		note.Set("order", i)
		if err := notes.AddContent(note); err != nil {
			log.Fatal(err)
		}
	}

	page, err := notes.Query(content.ContentQuery{
		Where: map[string]any{"title": "go*"}, // prefix match, case-insensitive
		Sort:  []content.SortCriteria{{Field: "order", Order: "desc"}},
		Limit: 1,
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(page.Contents[0].GetString("title"), page.Total, page.TotalPages, page.HasNext)

	found, err := notes.Query(content.ContentQuery{Search: "swap"})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(found.Contents[0].Slug)
	// Output:
	// Go modules 2 2 true
	// note-1
}

// A schema checks field types and constraints, fills defaults, and runs
// custom validators.
func ExampleNewSchema() {
	schema := content.NewSchema("Recipe").
		Field("title", content.FieldTypeString).Required().MinLength(3).
		Field("servings", content.FieldTypeInt).Min(1).Default(2).
		Field("cookedOn", content.FieldTypeTime).
		Build().
		Build()

	ok := &content.Content{Data: map[string]any{"title": "Soup", "cookedOn": "2024-01-15"}}
	if err := ok.ValidateSchema(schema); err != nil {
		log.Fatal(err)
	}
	fmt.Println(ok.GetInt("servings"), ok.GetTime("cookedOn").Weekday())

	bad := &content.Content{Path: "recipes/x.md", Data: map[string]any{"title": "Ox"}}
	fmt.Println(bad.ValidateSchema(schema))
	// Output:
	// 2 Monday
	// validation error in recipes/x.md: constraint violation for field title: constraint error: value too short
}

func ExampleParseTime() {
	for _, s := range []string{"2024-01-15", "2024-01-15 10:30", "2024-01-15T10:30:00+02:00", "01/02/2024"} {
		t, ok := content.ParseTime(s)
		fmt.Println(t.UTC().Format(time.RFC3339), ok)
	}
	// Output:
	// 2024-01-15T00:00:00Z true
	// 2024-01-15T10:30:00Z true
	// 2024-01-15T08:30:00Z true
	// 0001-01-01T00:00:00Z false
}
