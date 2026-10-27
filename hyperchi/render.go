package hyperchi

import (
	"bytes"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"reflect"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/regiellis/hyperchi/hyperchi/helpers"
	"github.com/regiellis/hyperchi/hyperchi/security"
)

// HTMLRenderer renders a response body from the handler's data, the values
// taken from the request (route and query parameters, WithContext values), and
// the request itself. TemplateRenderer implements it. Route handlers render
// through Context.View, Page, and Fragment rather than calling a renderer.
//
// A minimal implementation:
//
//	type CustomRenderer struct {
//		template string
//	}
//
//	func (cr *CustomRenderer) Render(result any, context map[string]any, request *http.Request) (string, error) {
//		data := result.(map[string]any)
//		return fmt.Sprintf("<h1>%s</h1>", data["title"]), nil
//	}
type HTMLRenderer interface {
	Render(result any, context map[string]any, request *http.Request) (string, error)
}

// TemplateRenderer is the html/template HTMLRenderer behind Context.View,
// Page, and Fragment. Use it directly only to render outside a route, such as
// in a background job:
//
//	tmpl := template.Must(template.ParseFiles("user.tmpl"))
//	renderer := &hyperchi.TemplateRenderer{
//		Template: tmpl,
//		Name:     "user",
//	}
//
//	html, err := renderer.Render(hyperchi.H{"user": user}, nil, req)
type TemplateRenderer struct {
	Template *template.Template // The parsed template set
	Name     string             // The template to execute; see resolveNameIn
	Data     map[string]any     // App data files, exposed to templates as .Data
	Layout   string             // "" for layout-base, "none" for no layout, or X for layout-X
}

// Render executes the template with one data map: the context values, then
// .Data, then the result's keys when result is a map (H, map[string]any, or
// helpers.JSON), later keys winning. A result of any other type is .Result,
// and a struct also adds its exported fields as keys (see mergeStructFields).
// .Request is the request. security.SafeHTML values in a result map are passed
// as template.HTML.
//
// The page is then wrapped in its layout, which reads the page as .content. A
// missing layout-base is skipped; any other missing layout is an error.
func (tr *TemplateRenderer) Render(result any, context map[string]any, request *http.Request) (string, error) {
	if tr.Template == nil {
		return "", fmt.Errorf("no templates loaded - call app.LoadTemplates() or ensure template '%s' exists in templates/, views/, or current directory", tr.Name)
	}

	data := make(map[string]any)
	for k, v := range context {
		data[k] = v
	}

	if tr.Data != nil {
		data["Data"] = tr.Data
	}

	// html/template escapes at render time; escaping here as well would
	// double-escape ("<a>" to "&lt;a&gt;" to "&amp;lt;a&amp;gt;").
	var resultMap map[string]any
	isMap := true
	switch v := result.(type) {
	case map[string]any:
		resultMap = v
	case H:
		resultMap = v
	case helpers.JSON:
		resultMap = v
	default:
		isMap = false
	}
	if isMap {
		for k, v := range resultMap {
			if safeHTML, isSafe := v.(security.SafeHTML); isSafe {
				data[k] = safeHTML.HTML()
			} else {
				data[k] = v
			}
		}
	} else {
		data["Result"] = result
		mergeStructFields(data, result)
	}
	data["Request"] = request

	// The name is resolved rather than used verbatim: ParseFiles and ParseGlob
	// name a template after its filename with the extension, while callers
	// write Template("index"). An explicit {{define "index"}} still wins,
	// because the bare name is tried first.
	name := resolveNameIn(tr.Template, tr.Name)
	if name == "" {
		return "", fmt.Errorf("no template named %q; loaded templates: %s",
			tr.Name, strings.Join(templateNames(tr.Template), ", "))
	}

	var pageBuf bytes.Buffer
	if err := tr.Template.ExecuteTemplate(&pageBuf, name, data); err != nil {
		return "", err
	}

	layoutName := tr.getLayoutName()
	if layoutName != "" && tr.hasTemplate(layoutName) {
		data["content"] = template.HTML(pageBuf.String())

		var layoutBuf bytes.Buffer
		if err := tr.Template.ExecuteTemplate(&layoutBuf, layoutName, data); err != nil {
			return "", fmt.Errorf("layout %q failed: %w", layoutName, err)
		}
		return layoutBuf.String(), nil
	}

	if tr.Layout != "" && tr.Layout != "none" {
		return "", fmt.Errorf("layout %q not found", layoutName)
	}
	return pageBuf.String(), nil
}

// mergeStructFields adds the exported fields of a struct (or a non-nil pointer
// to one) as top-level keys, beside .Result. A template written for one item,
// which a page reaches with {{template "entry" .}} inside a range, then also
// renders that item when a handler or broadcast passes it on its own. Fields
// win over context values, as handler data does.
func mergeStructFields(data map[string]any, result any) {
	v := reflect.ValueOf(result)
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return
	}
	for _, field := range reflect.VisibleFields(v.Type()) {
		if !field.IsExported() {
			continue
		}
		fv, err := v.FieldByIndexErr(field.Index)
		if err != nil {
			continue // promoted through a nil embedded pointer
		}
		data[field.Name] = fv.Interface()
	}
}

// getLayoutName returns the layout template to wrap the page in, or "" for
// none.
func (tr *TemplateRenderer) getLayoutName() string {
	if tr.Layout != "" {
		if tr.Layout == "none" {
			return ""
		}
		return "layout-" + tr.Layout
	}
	return "layout-base"
}

// hasTemplate reports whether the set holds a template called name.
func (tr *TemplateRenderer) hasTemplate(name string) bool {
	if tr.Template == nil {
		return false
	}
	return tr.Template.Lookup(name) != nil
}

// WithLayout renders inside layout-<layout> ("none" for no layout).
func (tr *TemplateRenderer) WithLayout(layout string) *TemplateRenderer {
	tr.Layout = layout
	return tr
}

// WithFragment renders without a layout.
func (tr *TemplateRenderer) WithFragment() *TemplateRenderer {
	tr.Layout = "none"
	return tr
}

// isHTMXRequest reports whether r carries HX-Request: true. A nil r is not.
func isHTMXRequest(r *http.Request) bool {
	if r == nil {
		return false
	}
	return r.Header.Get("HX-Request") == "true"
}

// extractContext collects r's template values: query parameters (a repeated
// one as a []string), then chi route parameters, then values added with
// WithContext, later ones winning. A route parameter beats a query parameter
// of the same name, so ?id=99 cannot change the id in /users/{id}.
func extractContext(r *http.Request) map[string]any {
	context := make(map[string]any)

	for key, values := range r.URL.Query() {
		if len(values) == 1 {
			context[key] = values[0]
		} else {
			context[key] = values
		}
	}

	rctx := chi.RouteContext(r.Context())
	if rctx != nil {
		for i, key := range rctx.URLParams.Keys {
			if i < len(rctx.URLParams.Values) {
				context[key] = rctx.URLParams.Values[i]
			}
		}
	}

	if ctxValues := r.Context().Value(hxValuesCtxKey); ctxValues != nil {
		if values, ok := ctxValues.(map[string]any); ok {
			for key, value := range values {
				context[key] = value
			}
		}
	}

	return context
}

// LookupTemplate returns the named template from the set loaded via LoadTemplates,
// or nil if none is loaded or the name is unknown. It satisfies the optional
// lookup capability the patterns package uses so pattern routes can render
// application templates, not only ones passed to RegisterTemplate.
func (g *HyperChi) LookupTemplate(name string) *template.Template {
	if g.templates == nil {
		return nil
	}
	return g.templates.Lookup(name)
}

// RenderToString executes a loaded template with data, without a layout, and
// returns its HTML, for composing a page from blocks or components. Data is
// still escaped by html/template; only the returned wrapper is trusted.
//
// The name is resolved the same way Template resolves it, so both an explicit
// {{define "card"}} and a bare card.tmpl file work.
func (g *HyperChi) RenderToString(name string, data any) (template.HTML, error) {
	if g.templates == nil {
		return "", fmt.Errorf("hyperchi: no templates loaded; call LoadTemplates before rendering %q", name)
	}

	resolved := g.resolveTemplateName(name)
	if resolved == "" {
		return "", fmt.Errorf("hyperchi: no template named %q", name)
	}

	var buf bytes.Buffer
	if err := g.templates.ExecuteTemplate(&buf, resolved, data); err != nil {
		return "", fmt.Errorf("hyperchi: rendering %q: %w", name, err)
	}
	return template.HTML(buf.String()), nil
}

// resolveNameIn finds the loaded name for a caller-supplied one: the name
// itself, then the name with .tmpl, .html, .htm, or .gohtml. It returns "" when
// nothing matches. ParseFiles and ParseGlob name a template after its file,
// extension included, while callers write the bare name; trying the bare name
// first keeps an explicit {{define}} winning.
func resolveNameIn(set *template.Template, name string) string {
	if set == nil || name == "" {
		return ""
	}
	if set.Lookup(name) != nil {
		return name
	}
	for _, ext := range []string{".tmpl", ".html", ".htm", ".gohtml"} {
		if set.Lookup(name+ext) != nil {
			return name + ext
		}
	}
	return ""
}

// templateNames lists the loaded template names, sorted, for "no such
// template" errors: the usual cause is a name that differs from what was
// loaded, and the list shows it.
func templateNames(set *template.Template) []string {
	if set == nil {
		return nil
	}
	var names []string
	for _, t := range set.Templates() {
		if t.Name() != "" {
			names = append(names, t.Name())
		}
	}
	sort.Strings(names)
	return names
}

// resolveTemplateName applies resolveNameIn to the app's templates.
func (g *HyperChi) resolveTemplateName(name string) string {
	return resolveNameIn(g.templates, name)
}

// Template returns a renderer for the named template. When no templates are
// loaded it first looks for the file in templates/, views/, and the working
// directory; a failed lookup is logged and the render reports it.
func (g *HyperChi) Template(name string) *TemplateRenderer {
	if g.templates == nil && name != "" {
		if err := g.autoDiscoverTemplate(name); err != nil {
			log.Printf("hyperchi: template auto-discovery: %v", err)
		}
	}

	return &TemplateRenderer{
		Template: g.templates,
		Name:     name,
		Data:     g.Data,
	}
}
