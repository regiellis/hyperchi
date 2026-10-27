package filters

import (
	"bytes"
	"html/template"
	"strings"
	"testing"
)

// The element filters splice their trailing attributes into markup, so they
// accept only trusted attributes: literals the template author wrote. A plain
// string from data must not reach the markup.
func TestElementFiltersTakeOnlyTrustedAttributes(t *testing.T) {
	render := func(src string, data any) (string, error) {
		tpl, err := template.New("t").Funcs(FuncMap()).Parse(src)
		if err != nil {
			return "", err
		}
		var out bytes.Buffer
		err = tpl.Execute(&out, data)
		return out.String(), err
	}

	cases := []string{
		`{{link "Docs" "/docs" .Attr}}`,
		`{{img "/a.png" "A" .Attr}}`,
		`{{script "/a.js" .Attr}}`,
		`{{style "/a.css" .Attr}}`,
		`{{formField "text" "q" "" .Attr}}`,
		`{{checkbox "ok" "1" true .Attr}}`,
		`{{radio "ok" "1" true .Attr}}`,
		`{{textarea "body" "" 3 20 .Attr}}`,
	}
	hostile := map[string]any{"Attr": `onclick="alert(1)"`}
	for _, src := range cases {
		out, err := render(src, hostile)
		if err == nil || strings.Contains(out, "onclick") {
			t.Errorf("%s with data attributes = %q, %v; want an error and no onclick", src, out, err)
		}
	}

	out, err := render(`{{link "Docs" "/docs" "class=\"btn\""}}`, nil)
	if err != nil || out != `<a href="/docs" class="btn">Docs</a>` {
		t.Fatalf("literal attribute = %q, %v", out, err)
	}
}
