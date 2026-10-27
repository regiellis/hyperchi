package main

import (
	"strings"
	"testing"

	"github.com/regiellis/hyperchi/hyperchi/filters"
)

func TestFilterDocumentation(t *testing.T) {
	registry := filters.FuncMap()
	seen := map[string]bool{}
	for _, group := range filterReference() {
		for _, entry := range group.Entries {
			if seen[entry.Name] {
				t.Errorf("duplicate filter %s", entry.Name)
			}
			seen[entry.Name] = true
			if _, ok := registry[entry.Name]; !ok {
				t.Errorf("unknown filter %s", entry.Name)
			}
		}
	}
	for name := range registry {
		if !seen[name] {
			t.Errorf("undocumented filter %s", name)
		}
	}
	docs := testDocs(t)
	page := docs.bySlug["filters"]
	for _, want := range []string{"Field Notebook", "$1,234.50", "12.5%", "1.50 KB", "Oct 5, 2026", "Anonymous", "go html htmx", "Morgan", `id="filter-hxGet"`} {
		if !strings.Contains(string(page.Body), want) {
			t.Errorf("rendered filters page missing %q", want)
		}
	}
	// The generated recipes and reference reach the Markdown and search text too.
	for _, want := range []string{"Result: `$1,234.50`", "- `hxGet`: `func(string) template.HTMLAttr`", "### Strings ("} {
		if !strings.Contains(page.Markdown, want) {
			t.Errorf("filters Markdown missing %q", want)
		}
	}
	var text strings.Builder
	for _, s := range page.sections {
		text.WriteString(s.Text)
	}
	for _, want := range []string{"Field Notebook", "hxGet func(string)"} {
		if !strings.Contains(text.String(), want) {
			t.Errorf("filters search text missing %q", want)
		}
	}
}
