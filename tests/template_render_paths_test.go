// Package tests holds integration tests that drive a whole app through
// ServeHTTP. The render-path tests pin the status, headers, and body that
// each Context render method (View, Page, Fragment, JSON, HTML, a returned
// error) produces for different template data, template layouts, and request
// headers.
package tests

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/regiellis/hyperchi/hyperchi"
)

// withTemplates writes the given files (relative paths) to a temp dir,
// chdirs into it, and returns a fresh app with templates loaded.
//
// useLayoutLoader=true uses the directory-aware loader (LoadTemplatesWithLayout),
// which depends on cwd; otherwise the absolute glob loader is used.
//
// Tests using this helper must NOT call t.Parallel (chdir is process-global).
func withTemplates(t *testing.T, files map[string]string, useLayoutLoader bool) *hyperchi.HyperChi {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(cwd); err != nil {
			t.Errorf("restore cwd: %v", err)
		}
	})

	app := hyperchi.New()
	var loadErr error
	if useLayoutLoader {
		loadErr = app.LoadTemplatesWithLayout()
	} else {
		loadErr = app.LoadTemplates(filepath.Join(dir, "templates", "*.tmpl"))
	}
	if loadErr != nil {
		t.Fatalf("load templates: %v", loadErr)
	}
	return app
}

// do sends a request with headers through app and returns the recording.
func do(app *hyperchi.HyperChi, method, path string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	return rec
}

func acceptHTML() map[string]string { return map[string]string{"Accept": "text/html"} }
func acceptJSON() map[string]string { return map[string]string{"Accept": "application/json"} }
func acceptAny() map[string]string  { return map[string]string{"Accept": "*/*"} }

// Data passed to c.View: map keys merge into the template root; any other
// value lands under .Result.

func TestRender_MapData_TopLevelKeys(t *testing.T) {
	app := withTemplates(t, map[string]string{
		"templates/index.tmpl": `[{{.title}}]`,
	}, true)
	app.Get("/", func(c *hyperchi.Context) error {
		return c.View("index", hyperchi.H{"title": "Hi"})
	})

	rec := do(app, "GET", "/", acceptHTML())
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%q", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); !strings.Contains(got, "[Hi]") {
		t.Errorf("expected [Hi]; got %q", got)
	}
}

func TestRender_NilData_BlankSubstitution(t *testing.T) {
	app := withTemplates(t, map[string]string{
		"templates/index.tmpl": `[{{.title}}]`,
	}, true)
	app.Get("/", func(c *hyperchi.Context) error {
		return c.View("index", nil)
	})

	rec := do(app, "GET", "/", acceptHTML())
	t.Logf("status=%d body=%q", rec.Code, rec.Body.String())
	// Nil data renders with blank values rather than failing.
	if rec.Code != 200 {
		t.Errorf("status=%d, expected 200 (silent blank); body=%q", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); !strings.Contains(got, "[]") {
		t.Errorf("expected blank substitution [], got %q", got)
	}
}

func TestRender_HTMLResponse_BypassesTemplate(t *testing.T) {
	// Contract: c.HTML writes the given markup as the response. No template
	// renders, even when one with a matching name is loaded.
	app := withTemplates(t, map[string]string{
		"templates/index.tmpl": `[{{.title}}]`,
	}, true)
	app.Get("/", func(c *hyperchi.Context) error {
		return c.HTML("<div>raw html, not json</div>")
	})

	rec := do(app, "GET", "/", acceptHTML())
	body := rec.Body.String()
	t.Logf("body=%q", body)
	if rec.Code != 200 {
		t.Errorf("status=%d", rec.Code)
	}
	if !strings.Contains(body, "raw html") {
		t.Errorf("expected raw HTML to reach the wire; got %q", body)
	}
	if strings.Contains(body, "[]") {
		t.Errorf("template should NOT have rendered; got %q", body)
	}
}

func TestRender_NonMapData_UnderResult(t *testing.T) {
	// A non-map value (here a slice) is not merged into the root; the template
	// reaches it through .Result.
	app := withTemplates(t, map[string]string{
		"templates/index.tmpl": `top={{.title}};result={{range .Result}}<i>{{.}}</i>{{end}}`,
	}, true)
	app.Get("/", func(c *hyperchi.Context) error {
		return c.View("index", []string{"a", "b"})
	})

	rec := do(app, "GET", "/", acceptHTML())
	body := rec.Body.String()
	t.Logf("body=%q", body)
	if !strings.Contains(body, "<i>a</i>") || !strings.Contains(body, "<i>b</i>") {
		t.Errorf("expected array under .Result; got %q", body)
	}
}

func TestRender_StringValueIsSingleEscaped(t *testing.T) {
	// html/template escapes at render time, so the framework must not escape
	// first, or "&" would come out as "&amp;amp;".
	app := withTemplates(t, map[string]string{
		"templates/index.tmpl": `[{{.title}}]`,
	}, true)
	app.Get("/", func(c *hyperchi.Context) error {
		return c.View("index", hyperchi.H{"title": "<script>x</script>"})
	})

	rec := do(app, "GET", "/", acceptHTML())
	body := rec.Body.String()
	t.Logf("body=%q", body)
	if strings.Contains(body, "<script>") {
		t.Errorf("expected escaped script tag (security); got %q", body)
	}
	if strings.Contains(body, "&amp;lt;") {
		t.Errorf("DOUBLE-ESCAPE bug — got %q", body)
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Errorf("expected single-escape &lt;script&gt;; got %q", body)
	}
}

// Accept header: c.View and c.Page render HTML whatever it says.

func TestRender_AcceptOnlyJSON_StillRendersTemplate(t *testing.T) {
	// Accept is not a switch. JSON APIs call c.JSON (see
	// TestRender_JSONResponse_ReturnsRawJSON).
	app := withTemplates(t, map[string]string{
		"templates/index.tmpl": `[{{.title}}]`,
	}, true)
	app.Get("/", func(c *hyperchi.Context) error {
		return c.View("index", hyperchi.H{"title": "Hi"})
	})

	rec := do(app, "GET", "/", acceptJSON())
	body := rec.Body.String()
	t.Logf("body=%q ct=%q", body, rec.Header().Get("Content-Type"))
	if !strings.Contains(body, "[Hi]") {
		t.Errorf("template should have rendered regardless of Accept; got %q", body)
	}
}

func TestRender_AcceptStarSlashStar_RendersHTML(t *testing.T) {
	// htmx sends Accept: */* by default, which must get HTML.
	app := withTemplates(t, map[string]string{
		"templates/index.tmpl": `[{{.title}}]`,
	}, true)
	app.Get("/", func(c *hyperchi.Context) error {
		return c.View("index", hyperchi.H{"title": "Hi"})
	})

	rec := do(app, "GET", "/", acceptAny())
	body := rec.Body.String()
	t.Logf("body=%q", body)
	if !strings.Contains(body, "[Hi]") {
		t.Errorf("expected HTML render under */*, got %q", body)
	}
}

// Layout wrapping.

func TestRender_LayoutBaseWrapsPage(t *testing.T) {
	app := withTemplates(t, map[string]string{
		"templates/layouts/base.tmpl": `<html><body>LAYOUT[{{.content}}]</body></html>`,
		"templates/page.tmpl":         `<h1>{{.title}}</h1>`,
	}, true)
	app.Get("/", func(c *hyperchi.Context) error {
		return c.View("page", hyperchi.H{"title": "Hi"})
	})

	rec := do(app, "GET", "/", acceptHTML())
	body := rec.Body.String()
	t.Logf("body=%q", body)
	if !strings.Contains(body, "LAYOUT[") {
		t.Errorf("expected layout wrap; got %q", body)
	}
	if !strings.Contains(body, "<h1>Hi</h1>") {
		t.Errorf("expected page inside layout; got %q", body)
	}
}

func TestRender_LayoutFailure_DevMode_500(t *testing.T) {
	// In development (the default), a layout failure is a 500: a silent
	// fallback would hide it behind a blank page.
	app := withTemplates(t, map[string]string{
		"templates/layouts/base.tmpl": `<html>{{template "component-missing" .}}{{.content}}</html>`,
		"templates/page.tmpl":         `<h1>{{.title}}</h1>`,
	}, true)
	app.Get("/", func(c *hyperchi.Context) error {
		return c.View("page", hyperchi.H{"title": "Hi"})
	})

	rec := do(app, "GET", "/", acceptHTML())
	t.Logf("status=%d body=%q", rec.Code, rec.Body.String())
	if rec.Code != 500 {
		t.Errorf("expected 500 in dev mode; got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "layout") {
		t.Errorf("expected error message to mention 'layout'; got %q", rec.Body.String())
	}
}

func TestRender_LayoutFailure_ProductionMode_ReturnsError(t *testing.T) {
	// A broken layout is a 500 in production too: no partial page, not cached.
	t.Setenv("ENVIRONMENT", "production")

	app := withTemplates(t, map[string]string{
		"templates/layouts/base.tmpl": `<html>{{template "component-missing" .}}{{.content}}</html>`,
		"templates/page.tmpl":         `<h1>{{.title}}</h1>`,
	}, true)
	app.Get("/", func(c *hyperchi.Context) error {
		return c.View("page", hyperchi.H{"title": "Hi"})
	})

	rec := do(app, "GET", "/", acceptHTML())
	t.Logf("body=%q", rec.Body.String())
	if rec.Code != 500 {
		t.Errorf("expected 500 for broken layout; got %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "<h1>Hi</h1>") {
		t.Errorf("partial page leaked: %q", rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Error("error response is cacheable")
	}
}

func TestRender_NoLayoutDirectory_PageRendersDirectly(t *testing.T) {
	// A flat templates/ directory with no layouts/ (the streamer-vite layout):
	// each page template is a whole document and renders as-is.
	app := withTemplates(t, map[string]string{
		"templates/index.tmpl": `<!DOCTYPE html><html><body>[{{.title}}]</body></html>`,
	}, true)
	app.Get("/", func(c *hyperchi.Context) error {
		return c.View("index", hyperchi.H{"title": "Hi"})
	})

	rec := do(app, "GET", "/", acceptHTML())
	body := rec.Body.String()
	t.Logf("body=%q", body)
	if !strings.HasPrefix(body, "<!DOCTYPE html>") {
		t.Errorf("expected raw page; got %q", body)
	}
	if !strings.Contains(body, "[Hi]") {
		t.Errorf("expected title; got %q", body)
	}
}

// Fragments.

func TestRender_Fragment_SkipsLayout(t *testing.T) {
	// c.Fragment never applies a layout, even when layout-base exists. htmx
	// swaps the response into a target, and a whole document there would
	// nest <html> inside the page.
	app := withTemplates(t, map[string]string{
		"templates/layouts/base.tmpl":   `<html><body>LAYOUT[{{.content}}]</body></html>`,
		"templates/fragments/frag.tmpl": `<div>frag</div>`,
	}, true)
	app.Get("/frag", func(c *hyperchi.Context) error {
		return c.Fragment("fragment-frag", hyperchi.H{})
	})

	rec := do(app, "GET", "/frag", acceptHTML())
	body := rec.Body.String()
	t.Logf("body=%q", body)
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%q", rec.Code, body)
	}
	if strings.Contains(body, "LAYOUT[") {
		t.Errorf("Fragment must skip layout; got %q", body)
	}
	if strings.Contains(body, "<html>") {
		t.Errorf("fragment must not contain layout HTML; got %q", body)
	}
	if !strings.Contains(body, "<div>frag</div>") {
		t.Errorf("fragment content missing; got %q", body)
	}
}

// Returned HTTP errors.

func TestRender_HTTPError_StatusAndMessage(t *testing.T) {
	// A returned *HTTPError sets its status and its message reaches the
	// client; no page template renders.
	app := withTemplates(t, map[string]string{
		"templates/index.tmpl": `[{{.title}}]`,
	}, true)
	app.Get("/", func(c *hyperchi.Context) error {
		return hyperchi.BadRequest("nope")
	})

	rec := do(app, "GET", "/", acceptHTML())
	t.Logf("status=%d body=%q", rec.Code, rec.Body.String())
	if rec.Code != 400 {
		t.Errorf("expected 400; got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "nope") {
		t.Errorf("expected error message in body; got %q", rec.Body.String())
	}
}

// c.JSON writes JSON without a template.

func TestRender_JSONResponse_ReturnsRawJSON(t *testing.T) {
	app := withTemplates(t, map[string]string{
		"templates/index.tmpl": `unused`,
	}, true)
	app.Get("/api/x", func(c *hyperchi.Context) error {
		return c.JSON(hyperchi.H{"ok": true})
	})

	rec := do(app, "GET", "/api/x", acceptHTML())
	body := rec.Body.String()
	t.Logf("body=%q ct=%q", body, rec.Header().Get("Content-Type"))
	if !strings.Contains(body, `"ok":true`) {
		t.Errorf("expected raw JSON pass-through; got %q", body)
	}
	if !strings.Contains(rec.Header().Get("Content-Type"), "application/json") {
		t.Errorf("expected application/json; got %q", rec.Header().Get("Content-Type"))
	}
}

// A missing template.

func TestRender_TemplateNameDoesNotExist_500(t *testing.T) {
	app := withTemplates(t, map[string]string{
		"templates/exists.tmpl": `ok`,
	}, true)
	app.Get("/", func(c *hyperchi.Context) error {
		return c.View("doesntexist", hyperchi.H{})
	})

	rec := do(app, "GET", "/", acceptHTML())
	t.Logf("status=%d body=%q", rec.Code, rec.Body.String())
	if rec.Code != 500 {
		t.Errorf("expected 500 for missing template; got %d", rec.Code)
	}
}

// c.Page renders the full page whatever the Accept header says.

func TestRender_Page_AcceptOnlyJSON_StillRendersHTML(t *testing.T) {
	// c.Page always renders the full page, whatever Accept says.
	app := withTemplates(t, map[string]string{
		"templates/index.tmpl": `[{{.title}}]`,
	}, true)
	app.Get("/", func(c *hyperchi.Context) error {
		return c.Page("index", hyperchi.H{"title": "Hi"})
	})

	rec := do(app, "GET", "/", acceptJSON())
	body := rec.Body.String()
	t.Logf("body=%q ct=%q", body, rec.Header().Get("Content-Type"))
	if !strings.Contains(body, "[Hi]") {
		t.Errorf("Page should ignore Accept and render HTML; got %q", body)
	}
}

// Components referenced from a page template.

func TestRender_ComponentInclude(t *testing.T) {
	app := withTemplates(t, map[string]string{
		"templates/components/footer.tmpl": `<footer>FOOTER</footer>`,
		"templates/page.tmpl":              `<h1>{{.title}}</h1>{{template "component-footer" .}}`,
	}, true)
	app.Get("/", func(c *hyperchi.Context) error {
		return c.View("page", hyperchi.H{"title": "Hi"})
	})

	rec := do(app, "GET", "/", acceptHTML())
	body := rec.Body.String()
	t.Logf("body=%q", body)
	if !strings.Contains(body, "FOOTER") {
		t.Errorf("expected component-footer to be included; got %q", body)
	}
}
