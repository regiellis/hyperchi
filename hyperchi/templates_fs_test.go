package hyperchi

import (
	"strings"
	"testing"
	"testing/fstest"
)

// Apps that embed their templates (go:embed) need to load them from an fs.FS,
// with the same "**" and naming rules as LoadTemplates.
func TestLoadTemplatesFSLoadsNestedEmbeddedTemplates(t *testing.T) {
	fsys := fstest.MapFS{
		"templates/page.tmpl":             {Data: []byte(`{{define "page"}}<main>{{template "card" .}}</main>{{end}}`)},
		"templates/parts/deep/card.tmpl":  {Data: []byte(`{{define "card"}}<p>{{.Title}}</p>{{end}}`)},
		"templates/parts/deep/ignore.txt": {Data: []byte(`not a template`)},
		"templates/plain.tmpl":            {Data: []byte(`plain {{.Title}}`)},
	}
	app := New()
	if err := app.LoadTemplatesFS(fsys, "templates/**/*.tmpl"); err != nil {
		t.Fatalf("LoadTemplatesFS: %v", err)
	}
	out, err := app.RenderToString("page", map[string]any{"Title": "<hi>"})
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "<main><p>&lt;hi&gt;</p></main>" {
		t.Fatalf("rendered %q", out)
	}
	// Without a define, the name is the base filename, found with or without
	// its extension.
	if out, err := app.RenderToString("plain", map[string]any{"Title": "x"}); err != nil || string(out) != "plain x" {
		t.Fatalf("plain = %q, %v", out, err)
	}

	if err := New().LoadTemplatesFS(fsys, "templates/*.nope"); err == nil || !strings.Contains(err.Error(), "no templates matched") {
		t.Fatalf("empty match: err = %v", err)
	}
	broken := fstest.MapFS{"t/bad.tmpl": {Data: []byte(`{{if}}`)}}
	if err := New().LoadTemplatesFS(broken, "t/*.tmpl"); err == nil || !strings.Contains(err.Error(), "t/bad.tmpl") {
		t.Fatalf("parse error should name the file: %v", err)
	}
}
