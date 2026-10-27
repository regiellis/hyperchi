package hyperchi

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/regiellis/hyperchi/hyperchi/config"
)

type renderEntry struct {
	Name string
	At   time.Time
	note string
}

func structApp(t *testing.T) *HyperChi {
	t.Helper()
	dir := t.TempDir()
	tmpl := `{{define "entry"}}<li>{{.Name}} {{.At.Year}} {{.site}}</li>{{end}}` +
		`{{define "list"}}{{range .Result}}{{.Name}};{{end}}{{.site}}{{end}}`
	if err := os.WriteFile(filepath.Join(dir, "entry.tmpl"), []byte(tmpl), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Database.Disabled = true
	app := NewWithConfig(cfg)
	if err := app.LoadTemplates(filepath.Join(dir, "*.tmpl")); err != nil {
		t.Fatal(err)
	}
	app.SetGlobal("site", "Demo")
	return app
}

// A struct handed to a fragment, a page, or a broadcast exposes its exported
// fields as top-level keys, so the template that ranges over items
// ({{template "entry" .}}) also renders one item passed on its own.
func TestStructDataFieldsReachTemplate(t *testing.T) {
	app := structApp(t)
	at := time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC)

	for name, data := range map[string]any{
		"value":   renderEntry{Name: "Mai", At: at, note: "hidden"},
		"pointer": &renderEntry{Name: "Mai", At: at},
	} {
		got, err := app.renderFragment(nil, "entry", data)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if want := "<li>Mai 2026 Demo</li>"; got != want {
			t.Errorf("%s: broadcast render %q, want %q", name, got, want)
		}
	}

	r := httptest.NewRequest("GET", "/", nil)
	got, err := app.renderFragment(r, "entry", renderEntry{Name: "Mai", At: at})
	if err != nil || !strings.Contains(got, "Mai 2026") {
		t.Errorf("request render %q, %v", got, err)
	}

	// A slice still arrives as .Result; globals still merge beside it.
	got, err = app.renderFragment(nil, "list", []renderEntry{{Name: "a"}, {Name: "b"}})
	if err != nil || got != "a;b;Demo" {
		t.Errorf("slice render %q, %v", got, err)
	}

	// A nil pointer renders without fields instead of failing.
	if _, err := app.renderFragment(nil, "entry", (*renderEntry)(nil)); err != nil {
		t.Errorf("nil pointer: %v", err)
	}
}
