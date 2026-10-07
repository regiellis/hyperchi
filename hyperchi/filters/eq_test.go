package filters

import (
	"bytes"
	"testing"
	"text/template"
)

type roomStatus string

// eq and ne replace the template builtins, so they must agree with them on basic
// kinds: a named string type equals its literal, integer types compare by value,
// and eq takes several candidates.
func TestEqMatchesBuiltinSemanticsForBasicKinds(t *testing.T) {
	cases := []struct {
		name string
		a    any
		bs   []any
		want bool
	}{
		{"named string vs literal", roomStatus("open"), []any{"open"}, true},
		{"named string mismatch", roomStatus("open"), []any{"closed"}, false},
		{"int vs int64", 3, []any{int64(3)}, true},
		{"uint vs int", uint8(7), []any{7}, true},
		{"negative int vs uint", -1, []any{uint(1)}, false},
		{"int vs float", 2, []any{2.0}, true},
		{"any of several", "b", []any{"a", "b", "c"}, true},
		{"none of several", "z", []any{"a", "b"}, false},
		{"bools", true, []any{true}, true},
		{"structs fall back to deep equality", struct{ X int }{1}, []any{struct{ X int }{1}}, true},
		{"nil vs nil", nil, []any{nil}, true},
		{"no candidates", "a", nil, false},
	}
	for _, tc := range cases {
		if got := eq(tc.a, tc.bs...); got != tc.want {
			t.Errorf("%s: eq(%v, %v) = %v, want %v", tc.name, tc.a, tc.bs, got, tc.want)
		}
	}
	if !ne(roomStatus("open"), "closed") || ne(roomStatus("open"), "open") {
		t.Error("ne disagrees with eq on a named string")
	}

	var out bytes.Buffer
	tpl := template.Must(template.New("t").Funcs(template.FuncMap{"eq": eq, "ne": ne}).
		Parse(`{{if eq .S "x"}}yes{{end}}{{if eq .S "a" "x"}}!{{end}}`))
	if err := tpl.Execute(&out, map[string]any{"S": roomStatus("x")}); err != nil {
		t.Fatal(err)
	}
	if out.String() != "yes!" {
		t.Fatalf("template rendered %q, want yes!", out.String())
	}
}
