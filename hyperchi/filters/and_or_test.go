package filters

import (
	"html/template"
	"strings"
	"testing"
)

// The registered and/or replace text/template's builtins, so they must return
// what the builtins return: an argument, not a bool. {{or .x "default"}} is
// the common fallback idiom and printed "true" while or returned a bool.
func TestAndOrReturnArgumentsLikeTheBuiltins(t *testing.T) {
	cases := []struct {
		tmpl string
		data map[string]any
		want string
	}{
		{`{{or .x "fallback"}}`, map[string]any{"x": ""}, "fallback"},
		{`{{or .x "fallback"}}`, map[string]any{"x": "set"}, "set"},
		{`{{or .x .y}}`, map[string]any{"x": 0, "y": 0}, "0"},
		{`{{or .missing "fallback"}}`, map[string]any{}, "fallback"},
		{`{{and .x .y}}`, map[string]any{"x": "a", "y": "b"}, "b"},
		{`{{and .x .y}}`, map[string]any{"x": "", "y": "b"}, ""},
		{`{{and .x .y}}`, map[string]any{"x": 3, "y": 0}, "0"},
		{`{{if or .x .y}}yes{{else}}no{{end}}`, map[string]any{"x": "", "y": []int{1}}, "yes"},
		{`{{if and .x .y}}yes{{else}}no{{end}}`, map[string]any{"x": "a", "y": []int{}}, "no"},
		{`{{if not (or .x .y)}}none{{end}}`, map[string]any{"x": "", "y": nil}, "none"},
	}
	for _, tc := range cases {
		tmpl := template.Must(template.New("t").Funcs(FuncMap()).Parse(tc.tmpl))
		var b strings.Builder
		if err := tmpl.Execute(&b, tc.data); err != nil {
			t.Fatalf("%s: %v", tc.tmpl, err)
		}
		if got := b.String(); got != tc.want {
			t.Errorf("%s with %v = %q, want %q", tc.tmpl, tc.data, got, tc.want)
		}
	}
}
