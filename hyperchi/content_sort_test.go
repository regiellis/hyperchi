package hyperchi

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// sortedSlugs creates one item per value (slug = name) and returns the slugs
// of SortBy("v", dir), joined by spaces.
func sortedSlugs(t *testing.T, dir string, values []struct {
	slug  string
	value any
}) string {
	t.Helper()
	cm := noDBApp(t).Content
	cm.Collection("items", "items", nil)
	for _, v := range values {
		meta := map[string]any{}
		if v.value != nil {
			meta["v"] = v.value
		}
		if err := cm.Create("items", &ContentItem{Slug: v.slug, Metadata: meta}); err != nil {
			t.Fatalf("create %s: %v", v.slug, err)
		}
	}
	items, err := cm.Query("items").SortBy("v", dir).All()
	if err != nil {
		t.Fatal(err)
	}
	slugs := make([]string, len(items))
	for i, item := range items {
		slugs[i] = item.Slug
	}
	return strings.Join(slugs, " ")
}

type sortCase = []struct {
	slug  string
	value any
}

// Text comparison put 10 before 9. Numbers of any Go type, json.Number, and
// numeric strings compare numerically.
func TestContentQuerySortByComparesNumbersNumerically(t *testing.T) {
	values := sortCase{
		{"ten", 10},
		{"nine", "9"},
		{"half", 0.5},
		{"hundred", json.Number("100")},
		{"neg", int64(-3)},
		{"eleven", uint8(11)},
		{"ninepointfive", "9.5"},
	}
	if got, want := sortedSlugs(t, "asc", values), "neg half nine ninepointfive ten eleven hundred"; got != want {
		t.Errorf("asc = %q, want %q", got, want)
	}
	if got, want := sortedSlugs(t, "desc", values), "hundred eleven ten ninepointfive nine half neg"; got != want {
		t.Errorf("desc = %q, want %q", got, want)
	}
}

func TestContentQuerySortByComparesTimesAsTimes(t *testing.T) {
	values := sortCase{
		// Text order would put "2026-10-05T01:00:00+05:00" after the
		// "2026-10-04T23:00:00Z" entry; as instants it is earlier.
		{"zoned", "2026-10-05T01:00:00+05:00"},
		{"utc", "2026-10-04T23:00:00Z"},
		{"value", time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)},
		{"dateonly", "2026-01-01"},
	}
	if got, want := sortedSlugs(t, "asc", values), "value dateonly zoned utc"; got != want {
		t.Errorf("asc = %q, want %q", got, want)
	}
}

// Text sorts as text; mixed kinds order numbers, then times, then text; items
// without the field come last either way; ties keep collection order.
func TestContentQuerySortByMixedKindsMissingAndStable(t *testing.T) {
	values := sortCase{
		{"b1", "beta"},
		{"none1", nil},
		{"two", 2},
		{"a", "alpha"},
		{"day", "2026-10-05"},
		{"b2", "beta"},
		{"none2", nil},
		{"one", "1"},
	}
	if got, want := sortedSlugs(t, "asc", values), "one two day a b1 b2 none1 none2"; got != want {
		t.Errorf("asc = %q, want %q", got, want)
	}
	if got, want := sortedSlugs(t, "desc", values), "b1 b2 a day two one none1 none2"; got != want {
		t.Errorf("desc = %q, want %q", got, want)
	}
}
