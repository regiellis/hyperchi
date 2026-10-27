package hyperchi

import (
	"strings"
	"testing"

	"github.com/regiellis/hyperchi/hyperchi/config"
)

// Query.SortBy must order every item. The old insertion sort compared
// items[i] after shifting overwrote it, so reversed input came back unsorted.
func TestContentQuerySortByMetadata(t *testing.T) {
	cm := noDBApp(t).Content
	cm.Collection("posts", "posts", nil)
	for _, order := range []string{"d", "c", "b", "a", "e"} {
		if err := cm.Create("posts", &ContentItem{Slug: order, Metadata: map[string]any{"order": order}}); err != nil {
			t.Fatalf("create %s: %v", order, err)
		}
	}

	slugs := func(items []*ContentItem) string {
		var b strings.Builder
		for _, item := range items {
			b.WriteString(item.Slug)
		}
		return b.String()
	}

	asc, err := cm.Query("posts").SortBy("order", "asc").All()
	if err != nil {
		t.Fatal(err)
	}
	if got := slugs(asc); got != "abcde" {
		t.Errorf("ascending order = %q, want %q", got, "abcde")
	}

	desc, err := cm.Query("posts").OrderBy("order", "desc").All()
	if err != nil {
		t.Fatal(err)
	}
	if got := slugs(desc); got != "edcba" {
		t.Errorf("descending order = %q, want %q", got, "edcba")
	}
}

// Redefining a collection empties it, cache included: Get must not return an
// item from the collection it replaced.
func TestContentCollectionReplaceClearsCache(t *testing.T) {
	cm := noDBApp(t).Content
	cm.Collection("posts", "posts", nil)
	if err := cm.Create("posts", &ContentItem{Slug: "hello", Title: "old"}); err != nil {
		t.Fatal(err)
	}
	if _, err := cm.Get("posts", "hello"); err != nil {
		t.Fatal(err)
	}
	cm.Collection("pages", "pages", nil)
	if err := cm.Create("pages", &ContentItem{Slug: "about", Title: "kept"}); err != nil {
		t.Fatal(err)
	}

	cm.Collection("posts", "posts-v2", nil)
	if item, err := cm.Get("posts", "hello"); err == nil {
		t.Fatalf("Get(posts, hello) after replacing posts = %q, want not found", item.Title)
	}
	if item, err := cm.Get("pages", "about"); err != nil || item.Title != "kept" {
		t.Fatalf("Get(pages, about) = %v, %v; replacing posts must not touch pages", item, err)
	}
}

// Cache keys must not collide across collections: collection "a" with slug
// "b:c" and collection "a:b" with slug "c" are different items.
func TestContentCacheKeysDoNotCollide(t *testing.T) {
	cfg := config.Default()
	cm := newContentManager(newCacheManager(&cfg.Cache), newEventBus())
	cm.Collection("a", "", nil)
	cm.Collection("a:b", "", nil)
	if err := cm.Create("a", &ContentItem{Slug: "b:c", Title: "from a"}); err != nil {
		t.Fatal(err)
	}
	if err := cm.Create("a:b", &ContentItem{Slug: "c", Title: "from a:b"}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ collection, slug, want string }{{"a", "b:c", "from a"}, {"a:b", "c", "from a:b"}} {
		item, err := cm.Get(tc.collection, tc.slug)
		if err != nil || item.Title != tc.want {
			t.Fatalf("Get(%q, %q) = %+v, %v; want %q", tc.collection, tc.slug, item, err, tc.want)
		}
	}
	// Replacing "a" must not drop "a:b"'s cached item, and vice versa.
	cm.Collection("a", "", nil)
	if _, err := cm.Get("a", "b:c"); err == nil {
		t.Fatal("replaced collection still serves its old item")
	}
	if item, err := cm.Get("a:b", "c"); err != nil || item.Title != "from a:b" {
		t.Fatalf("replacing a dropped a:b's item: %+v, %v", item, err)
	}
}
