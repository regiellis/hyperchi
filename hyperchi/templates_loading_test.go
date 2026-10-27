package hyperchi

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTemplate creates a template file, making its directory as needed.
func writeTemplate(t *testing.T, dir, rel, body string) {
	t.Helper()

	full := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", rel, err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

// chdir moves into dir for the duration of the test, because the template
// patterns under test are relative paths.
func chdir(t *testing.T, dir string) {
	t.Helper()

	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(prev); err != nil {
			t.Errorf("restore wd: %v", err)
		}
	})
}

// A "**" pattern has to reach every level. filepath.Glob has no "**" operator
// and treats it as a single "*", which would silently drop anything nested
// more than one directory deep.
func TestLoadTemplatesMatchesEveryDepth(t *testing.T) {
	dir := t.TempDir()
	writeTemplate(t, dir, "templates/top.tmpl", `{{define "top"}}TOP{{end}}`)
	writeTemplate(t, dir, "templates/pages/index.tmpl", `{{define "index"}}INDEX{{end}}`)
	writeTemplate(t, dir, "templates/a/b/deep.tmpl", `{{define "deep"}}DEEP{{end}}`)
	writeTemplate(t, dir, "templates/a/b/c/deeper.tmpl", `{{define "deeper"}}DEEPER{{end}}`)
	writeTemplate(t, dir, "templates/ignored.txt", `not a template`)
	chdir(t, dir)

	app := New()
	if err := app.LoadTemplates(filepath.Join("templates", "**", "*.tmpl")); err != nil {
		t.Fatalf("LoadTemplates: %v", err)
	}

	for _, name := range []string{"top", "index", "deep", "deeper"} {
		if app.LookupTemplate(name) == nil {
			t.Errorf("template %q was not loaded", name)
		}
	}

	// The non-matching extension must stay out.
	if app.LookupTemplate("ignored.txt") != nil {
		t.Error("a file outside the pattern was loaded")
	}
}

func TestLoadTemplatesWithoutDoubleStarStillWorks(t *testing.T) {
	dir := t.TempDir()
	writeTemplate(t, dir, "templates/pages/index.tmpl", `{{define "index"}}INDEX{{end}}`)
	writeTemplate(t, dir, "templates/a/b/deep.tmpl", `{{define "deep"}}DEEP{{end}}`)
	chdir(t, dir)

	app := New()
	if err := app.LoadTemplates(filepath.Join("templates", "*", "*.tmpl")); err != nil {
		t.Fatalf("LoadTemplates: %v", err)
	}

	if app.LookupTemplate("index") == nil {
		t.Error("single-level pattern should load the one-level template")
	}
	// A plain "*/*" is exactly one level, and must stay that way.
	if app.LookupTemplate("deep") != nil {
		t.Error("single-level pattern must not reach two levels deep")
	}
}

// A pattern matching nothing is an error at load time, not an empty template
// set that fails later with a confusing render error.
func TestLoadTemplatesReportsAnEmptyMatch(t *testing.T) {
	dir := t.TempDir()
	writeTemplate(t, dir, "templates/pages/index.tmpl", `{{define "index"}}INDEX{{end}}`)
	chdir(t, dir)

	app := New()
	err := app.LoadTemplates(filepath.Join("templates", "**", "*.nope"))
	if err == nil {
		t.Fatal("expected an error when no template matches the pattern")
	}
	if !strings.Contains(err.Error(), "no templates matched") {
		t.Errorf("error should say nothing matched, got: %v", err)
	}
}

func TestRenderToString(t *testing.T) {
	dir := t.TempDir()
	writeTemplate(t, dir, "templates/parts/card.tmpl",
		`{{define "card"}}<article>{{.Title}}</article>{{end}}`)
	chdir(t, dir)

	app := New()
	if err := app.LoadTemplates(filepath.Join("templates", "**", "*.tmpl")); err != nil {
		t.Fatalf("LoadTemplates: %v", err)
	}

	out, err := app.RenderToString("card", map[string]any{"Title": "Hello"})
	if err != nil {
		t.Fatalf("RenderToString: %v", err)
	}
	if string(out) != "<article>Hello</article>" {
		t.Errorf("got %q", out)
	}
}

func TestRenderToStringEscapesData(t *testing.T) {
	dir := t.TempDir()
	writeTemplate(t, dir, "templates/parts/card.tmpl",
		`{{define "card"}}<article>{{.Title}}</article>{{end}}`)
	chdir(t, dir)

	app := New()
	if err := app.LoadTemplates(filepath.Join("templates", "**", "*.tmpl")); err != nil {
		t.Fatalf("LoadTemplates: %v", err)
	}

	// The return type is template.HTML, so the data must still be escaped by
	// html/template on the way in.
	out, err := app.RenderToString("card", map[string]any{
		"Title": `<script>alert(1)</script>`,
	})
	if err != nil {
		t.Fatalf("RenderToString: %v", err)
	}
	if strings.Contains(string(out), "<script>") {
		t.Errorf("data was not escaped: %q", out)
	}
}

func TestRenderToStringErrors(t *testing.T) {
	dir := t.TempDir()
	writeTemplate(t, dir, "templates/parts/card.tmpl", `{{define "card"}}ok{{end}}`)
	chdir(t, dir)

	t.Run("no templates loaded", func(t *testing.T) {
		app := New()
		if _, err := app.RenderToString("card", nil); err == nil {
			t.Error("expected an error when no templates are loaded")
		}
	})

	t.Run("unknown template", func(t *testing.T) {
		app := New()
		if err := app.LoadTemplates(filepath.Join("templates", "**", "*.tmpl")); err != nil {
			t.Fatalf("LoadTemplates: %v", err)
		}
		_, err := app.RenderToString("nope", nil)
		if err == nil {
			t.Fatal("expected an error for an unknown template")
		}
		if !strings.Contains(err.Error(), "nope") {
			t.Errorf("error should name the template, got: %v", err)
		}
	})
}

// Files parsed by name are registered with their extension, while callers write
// the name without it. Both spellings have to resolve, and an explicit
// {{define}} must keep winning over a filename.
func TestTemplateNameResolution(t *testing.T) {
	dir := t.TempDir()
	writeTemplate(t, dir, "templates/pages/bare.tmpl", `BARE-BODY`)
	writeTemplate(t, dir, "templates/pages/named.tmpl", `{{define "named"}}NAMED-BODY{{end}}`)
	chdir(t, dir)

	app := New()
	if err := app.LoadTemplates(filepath.Join("templates", "**", "*.tmpl")); err != nil {
		t.Fatalf("LoadTemplates: %v", err)
	}

	cases := map[string]string{
		"bare":       "BARE-BODY",  // resolved by appending .tmpl
		"bare.tmpl":  "BARE-BODY",  // the literal parsed name
		"named":      "NAMED-BODY", // an explicit define
		"named.tmpl": "",           // the file body is just the define, so empty
	}

	for name, want := range cases {
		out, err := app.RenderToString(name, nil)
		if err != nil {
			t.Errorf("RenderToString(%q): %v", name, err)
			continue
		}
		if strings.TrimSpace(string(out)) != want {
			t.Errorf("RenderToString(%q) = %q, want %q", name, strings.TrimSpace(string(out)), want)
		}
	}
}
