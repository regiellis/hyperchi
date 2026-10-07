package content

import "testing"

func TestQuerySliceFilters(t *testing.T) {
	for _, tc := range []struct {
		name             string
		actual, expected any
		match            bool
	}{
		{"strings overlap", []string{"go", "web"}, []string{"go"}, true},
		{"strings disjoint", []string{"go"}, []string{"other"}, false},
		{"any overlap", []any{"go", 1}, []any{1}, true},
		{"nested map", []any{map[string]any{"key": "value"}}, []any{map[string]any{"key": "value"}}, true},
		{"scalar", true, true, true},
		{"wildcard", "Golang", "*lang", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := NewCollection(CollectionConfig{Name: "posts"})
			if err := c.AddContent(&Content{ID: "1", Data: map[string]any{"tags": tc.actual}}); err != nil {
				t.Fatal(err)
			}
			result, err := c.Query(ContentQuery{Where: map[string]any{"tags": tc.expected}})
			if err != nil {
				t.Fatal(err)
			}
			if (result.Total == 1) != tc.match {
				t.Fatalf("total=%d want match=%t", result.Total, tc.match)
			}
		})
	}
}
