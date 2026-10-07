package patterns

import (
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// stubRouter captures the single handler registered on a pattern so it can be
// invoked directly with httptest.
type stubRouter struct{ handler http.HandlerFunc }

func (s *stubRouter) HandleFunc(_ string, h http.HandlerFunc) { s.handler = h }

// lookupRouter also satisfies the templateLookup capability, standing in for a
// host app whose templates were loaded via LoadTemplates rather than
// RegisterTemplate. It exercises the renderTemplate fallback.
type lookupRouter struct {
	stubRouter
	set *template.Template
}

func (l *lookupRouter) LookupTemplate(name string) *template.Template {
	if l.set == nil {
		return nil
	}
	return l.set.Lookup(name)
}

func postForm(t *testing.T, h http.HandlerFunc, target string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h(w, req)
	return w
}

// AutoSave parses the form, so SaveFunc receives the submitted fields rather
// than an empty map.
func TestAutoSaveParsesFormData(t *testing.T) {
	r := &stubRouter{}
	pm := NewPatternManager(r, nil)

	var saved map[string]any
	pm.AutoSave("/autosave", AutoSaveConfig{
		DraftKey: "draft-1",
		SaveFunc: func(data map[string]any) error { saved = data; return nil },
	})

	w := postForm(t, r.handler, "/autosave", url.Values{
		"title": {"Hello"},
		"body":  {"World"},
	})

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if saved["title"] != "Hello" || saved["body"] != "World" {
		t.Fatalf("SaveFunc received %v, expected the parsed form fields", saved)
	}
}

// Pattern routes render templates loaded with the app's LoadTemplates, not
// only those passed to RegisterTemplate, when the app supports
// LookupTemplate.
func TestRenderTemplateFallsBackToAppTemplates(t *testing.T) {
	r := &lookupRouter{set: template.Must(template.New("results.html").Parse("HIT {{.query}}"))}
	pm := NewPatternManager(r, nil)

	// The template is not registered on pm; only the app knows it.
	pm.LiveSearch("/search", LiveSearchConfig{
		SearchFunc:  func(q string, limit int) (any, error) { return []string{q}, nil },
		Template:    "results.html",
		MinQueryLen: 1,
	})

	req := httptest.NewRequest(http.MethodGet, "/search?q=ok", nil)
	w := httptest.NewRecorder()
	r.handler(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (app template should resolve via fallback)", w.Code)
	}
	if !strings.Contains(w.Body.String(), "HIT ok") {
		t.Fatalf("body = %q, want the app template rendered", w.Body.String())
	}
}

// InfiniteScroll accepts a nil HasMoreFunc and derives has_more from the
// returned page size.
func TestInfiniteScrollNilHasMoreDoesNotPanic(t *testing.T) {
	r := &stubRouter{}
	pm := NewPatternManager(r, nil)
	pm.templates["rows"] = template.Must(template.New("rows").Parse("{{.has_more}}:{{len .data}}"))

	pm.InfiniteScroll("/rows", InfiniteScrollConfig{
		DataFunc: func(page, size int) (any, error) { return make([]int, size), nil },
		Template: "rows",
		PageSize: 5,
		// HasMoreFunc intentionally nil.
	})

	req := httptest.NewRequest(http.MethodGet, "/rows?page=1", nil)
	w := httptest.NewRecorder()
	r.handler(w, req) // must not panic
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	// A full page (len == pageSize) means has_more defaults to true.
	if !strings.Contains(w.Body.String(), "true:5") {
		t.Fatalf("body = %q, want has_more=true for a full page", w.Body.String())
	}
}
