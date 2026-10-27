package hyperchi

import (
	"bytes"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newTemplateApp loads a layout, a page, and an optional error template from a
// temp dir so the render paths run against real files.
func newTemplateApp(t *testing.T, withErrorTemplate bool) *HyperChi {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"layout.tmpl":  `{{define "layout-base"}}<html><title>{{.site}}</title>{{.content}}</html>{{end}}`,
		"page.tmpl":    `{{define "page"}}<p>{{.msg}}</p>{{end}}`,
		"broken.tmpl":  `{{define "broken"}}<p>start</p>{{template "missing-partial" .}}{{end}}`,
		"globals.tmpl": `{{define "globals"}}site={{.site}} dev={{.development}} msg={{.msg}}{{end}}`,
	}
	if withErrorTemplate {
		files["error.tmpl"] = `{{define "error"}}<section class="err">{{.status}}: {{.message}}</section>{{end}}`
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	app := New()
	t.Cleanup(func() {
		if err := app.CloseDB(); err != nil {
			t.Errorf("close db: %v", err)
		}
	})
	if err := app.LoadTemplates(filepath.Join(dir, "*.tmpl")); err != nil {
		t.Fatal(err)
	}
	app.SetGlobal("site", "Demo")
	return app
}

func serve(app *HyperChi, req *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	app.ServeHTTP(w, req)
	return w
}

func htmxReq(method, target string) *http.Request {
	req := httptest.NewRequest(method, target, nil)
	req.Header.Set("HX-Request", "true")
	req.Header.Set("HX-Request-Type", "partial")
	return req
}

func TestViewNegotiatesPageAndFragment(t *testing.T) {
	app := newTemplateApp(t, false)
	app.Get("/", func(c *Context) error { return c.View("page", H{"msg": "hi"}) })

	cases := []struct {
		name       string
		headers    map[string]string
		wantLayout bool
	}{
		{"plain request", nil, true},
		{"htmx partial", map[string]string{"HX-Request": "true", "HX-Request-Type": "partial"}, false},
		{"htmx without request type", map[string]string{"HX-Request": "true"}, false},
		{"boosted navigation", map[string]string{"HX-Request": "true", "HX-Boosted": "true"}, true},
		{"history restore", map[string]string{"HX-Request": "true", "HX-History-Restore-Request": "true"}, true},
		{"htmx full request", map[string]string{"HX-Request": "true", "HX-Request-Type": "full"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}
			w := serve(app, req)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, body %q", w.Code, w.Body.String())
			}
			hasLayout := strings.Contains(w.Body.String(), "<html>")
			if hasLayout != tc.wantLayout {
				t.Fatalf("layout = %v, want %v; body %q", hasLayout, tc.wantLayout, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), "<p>hi</p>") {
				t.Fatalf("page content missing: %q", w.Body.String())
			}
			if ct := w.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
				t.Fatalf("content type = %q", ct)
			}
		})
	}
}

func TestPageAndFragmentIgnoreRequestKind(t *testing.T) {
	app := newTemplateApp(t, false)
	app.Get("/page", func(c *Context) error { return c.Page("page", H{"msg": "p"}) })
	app.Get("/frag", func(c *Context) error { return c.Fragment("page", H{"msg": "f"}) })
	app.Get("/nolayout", func(c *Context) error { return c.Layout("none").Page("page", H{"msg": "n"}) })

	if body := serve(app, htmxReq(http.MethodGet, "/page")).Body.String(); !strings.Contains(body, "<html>") {
		t.Fatalf("Page on htmx request lost its layout: %q", body)
	}
	if body := serve(app, httptest.NewRequest(http.MethodGet, "/frag", nil)).Body.String(); body != "<p>f</p>" {
		t.Fatalf("Fragment = %q", body)
	}
	if body := serve(app, httptest.NewRequest(http.MethodGet, "/nolayout", nil)).Body.String(); body != "<p>n</p>" {
		t.Fatalf(`Layout("none").Page = %q`, body)
	}
}

func TestStatusChainingAndBodylessResponses(t *testing.T) {
	app := newTemplateApp(t, false)
	app.Post("/created", func(c *Context) error { return c.Status(http.StatusCreated).Fragment("page", H{"msg": "new"}) })
	app.Delete("/gone", func(c *Context) error { return c.NoContent() })
	app.Head("/head", func(c *Context) error { return c.Text("ignored body") })
	app.Options("/allow", func(c *Context) error { return c.SetHeader("Allow", "GET").Empty() })

	w := serve(app, httptest.NewRequest(http.MethodPost, "/created", nil))
	if w.Code != http.StatusCreated || w.Body.String() != "<p>new</p>" {
		t.Fatalf("created: %d %q", w.Code, w.Body.String())
	}
	w = serve(app, httptest.NewRequest(http.MethodDelete, "/gone", nil))
	if w.Code != http.StatusNoContent || w.Body.Len() != 0 {
		t.Fatalf("no content: %d %q", w.Code, w.Body.String())
	}
	w = serve(app, httptest.NewRequest(http.MethodHead, "/head", nil))
	if w.Code != http.StatusOK || w.Body.Len() != 0 {
		t.Fatalf("head: %d %q", w.Code, w.Body.String())
	}
	w = serve(app, httptest.NewRequest(http.MethodOptions, "/allow", nil))
	if w.Code != http.StatusOK || w.Body.Len() != 0 || w.Header().Get("Allow") != "GET" {
		t.Fatalf("empty: %d %q %v", w.Code, w.Body.String(), w.Header())
	}
}

func TestHXHeadersChain(t *testing.T) {
	app := newTemplateApp(t, false)
	app.Post("/save", func(c *Context) error {
		return c.HX().Trigger("saved").Trigger("refresh-list").Retarget("#list").Reswap("beforeend").Fragment("page", H{"msg": "row"})
	})
	app.Post("/detail", func(c *Context) error {
		return c.HX().Trigger("saved", H{"id": 7}).Empty()
	})

	w := serve(app, htmxReq(http.MethodPost, "/save"))
	if got := w.Header().Get("HX-Trigger"); got != "saved,refresh-list" {
		t.Fatalf("HX-Trigger = %q", got)
	}
	if w.Header().Get("HX-Retarget") != "#list" || w.Header().Get("HX-Reswap") != "beforeend" {
		t.Fatalf("retarget/reswap headers: %v", w.Header())
	}
	if w.Body.String() != "<p>row</p>" {
		t.Fatalf("body = %q", w.Body.String())
	}

	w = serve(app, htmxReq(http.MethodPost, "/detail"))
	var events map[string]map[string]int
	if err := json.Unmarshal([]byte(w.Header().Get("HX-Trigger")), &events); err != nil {
		t.Fatalf("HX-Trigger with detail is not JSON: %q (%v)", w.Header().Get("HX-Trigger"), err)
	}
	if events["saved"]["id"] != 7 || w.Code != http.StatusOK || w.Body.Len() != 0 {
		t.Fatalf("detail trigger: %v %d %q", events, w.Code, w.Body.String())
	}
}

func TestRedirectDependsOnRequestKind(t *testing.T) {
	app := newTemplateApp(t, false)
	app.Post("/login", func(c *Context) error { return c.Redirect("/home") })

	w := serve(app, httptest.NewRequest(http.MethodPost, "/login", nil))
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/home" {
		t.Fatalf("plain redirect: %d %q", w.Code, w.Header().Get("Location"))
	}
	w = serve(app, htmxReq(http.MethodPost, "/login"))
	if w.Code != http.StatusOK || w.Header().Get("HX-Redirect") != "/home" || w.Header().Get("Location") != "" {
		t.Fatalf("htmx redirect: %d %v", w.Code, w.Header())
	}
}

func TestRenderFailureSendsNoPartialPage(t *testing.T) {
	app := newTemplateApp(t, false)
	app.Get("/broken", func(c *Context) error { return c.Fragment("broken", nil) })

	req := httptest.NewRequest(http.MethodGet, "/broken", nil)
	req.Header.Set("Accept", "application/json")
	w := serve(app, req)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", w.Code)
	}
	if strings.Contains(w.Body.String(), "<p>start</p>") {
		t.Fatalf("partial template output reached the client: %q", w.Body.String())
	}
	if strings.Contains(w.Body.String(), "missing-partial") {
		t.Fatalf("internal error detail leaked: %q", w.Body.String())
	}
}

func TestErrorRenderingPerClient(t *testing.T) {
	handler := func(c *Context) error { return NotFound("No such <user>.") }

	t.Run("json client", func(t *testing.T) {
		app := newTemplateApp(t, false)
		app.Get("/u", handler)
		req := httptest.NewRequest(http.MethodGet, "/u", nil)
		req.Header.Set("Accept", "application/json")
		w := serve(app, req)
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if w.Code != http.StatusNotFound || body["error"] != "No such <user>." {
			t.Fatalf("%d %v", w.Code, body)
		}
	})
	t.Run("htmx without error template", func(t *testing.T) {
		app := newTemplateApp(t, false)
		app.Get("/u", handler)
		w := serve(app, htmxReq(http.MethodGet, "/u"))
		if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), `role="alert"`) ||
			!strings.Contains(w.Body.String(), "No such &lt;user&gt;.") {
			t.Fatalf("%d %q", w.Code, w.Body.String())
		}
	})
	t.Run("browser without error template", func(t *testing.T) {
		app := newTemplateApp(t, false)
		app.Get("/u", handler)
		w := serve(app, httptest.NewRequest(http.MethodGet, "/u", nil))
		if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "<h1>404 Not Found</h1>") {
			t.Fatalf("%d %q", w.Code, w.Body.String())
		}
	})
	t.Run("error template as fragment and page", func(t *testing.T) {
		app := newTemplateApp(t, true)
		app.Get("/u", handler)
		w := serve(app, htmxReq(http.MethodGet, "/u"))
		if w.Body.String() != `<section class="err">404: No such &lt;user&gt;.</section>` {
			t.Fatalf("fragment = %q", w.Body.String())
		}
		w = serve(app, httptest.NewRequest(http.MethodGet, "/u", nil))
		if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "<html>") || !strings.Contains(w.Body.String(), `class="err"`) {
			t.Fatalf("page = %d %q", w.Code, w.Body.String())
		}
	})
	t.Run("plain error is a 500 without its text", func(t *testing.T) {
		app := newTemplateApp(t, false)
		app.Get("/u", func(c *Context) error { return errors.New("db password rejected") })
		req := httptest.NewRequest(http.MethodGet, "/u", nil)
		req.Header.Set("Accept", "application/json")
		w := serve(app, req)
		if w.Code != http.StatusInternalServerError || strings.Contains(w.Body.String(), "password") {
			t.Fatalf("%d %q", w.Code, w.Body.String())
		}
		w = serve(app, htmxReq(http.MethodGet, "/u"))
		if w.Code != http.StatusInternalServerError || strings.Contains(w.Body.String(), "password") {
			t.Fatalf("htmx: %d %q", w.Code, w.Body.String())
		}
	})
	t.Run("OnError replaces the default", func(t *testing.T) {
		app := newTemplateApp(t, false)
		app.OnError(func(c *Context, err error) {
			if err := c.Status(http.StatusTeapot).Text("custom: " + err.Error()); err != nil {
				t.Error(err)
			}
		})
		app.Get("/u", handler)
		w := serve(app, httptest.NewRequest(http.MethodGet, "/u", nil))
		if w.Code != http.StatusTeapot || !strings.HasPrefix(w.Body.String(), "custom: 404") {
			t.Fatalf("%d %q", w.Code, w.Body.String())
		}
	})
}

func TestSecondResponseIsRejected(t *testing.T) {
	app := newTemplateApp(t, false)
	var second error
	app.Get("/twice", func(c *Context) error {
		if err := c.Text("first"); err != nil {
			return err
		}
		second = c.JSON(H{"second": true})
		return nil
	})
	w := serve(app, httptest.NewRequest(http.MethodGet, "/twice", nil))
	if !errors.Is(second, errResponseWritten) || w.Body.String() != "first" {
		t.Fatalf("second write: %v, body %q", second, w.Body.String())
	}
}

func TestGlobalsPrecedence(t *testing.T) {
	app := newTemplateApp(t, false)
	app.SetGlobal("development", true)
	app.Get("/g", func(c *Context) error { return c.Fragment("globals", H{"msg": "handler"}) })
	app.Get("/override", func(c *Context) error { return c.Fragment("globals", H{"site": "Mine", "msg": "x"}) })

	// A query parameter must not override an app global.
	w := serve(app, httptest.NewRequest(http.MethodGet, "/g?site=evil&development=false&msg=query", nil))
	if w.Body.String() != "site=Demo dev=true msg=handler" {
		t.Fatalf("query overrode a global or handler data: %q", w.Body.String())
	}
	w = serve(app, httptest.NewRequest(http.MethodGet, "/override", nil))
	if w.Body.String() != "site=Mine dev=true msg=x" {
		t.Fatalf("handler data should win over globals: %q", w.Body.String())
	}
}

func TestRequestAccessors(t *testing.T) {
	app := newTemplateApp(t, false)
	app.Get("/items/{id}", func(c *Context) error {
		id, err := c.ParamInt("id")
		if err != nil {
			return err
		}
		return c.JSON(H{"id": id, "page": c.QueryInt("page", 1), "q": c.QueryDefault("q", "all"), "htmx": c.IsHTMX(), "path": c.Path()})
	})

	w := serve(app, httptest.NewRequest(http.MethodGet, "/items/42?page=x", nil))
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["id"] != float64(42) || body["page"] != float64(1) || body["q"] != "all" || body["htmx"] != false || body["path"] != "/items/42" {
		t.Fatalf("accessors: %v", body)
	}
	req := httptest.NewRequest(http.MethodGet, "/items/abc", nil)
	req.Header.Set("Accept", "application/json")
	if w := serve(app, req); w.Code != http.StatusBadRequest {
		t.Fatalf("non-integer id: %d", w.Code)
	}
}

func TestPerRouteMiddlewareAndEscapeHatches(t *testing.T) {
	app := newTemplateApp(t, false)
	tag := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Tagged", "yes")
			next.ServeHTTP(w, r)
		})
	}
	app.Get("/tagged", func(c *Context) error { return c.Text("t") }, tag)
	app.Get("/plain", func(c *Context) error { return c.Text("p") })
	app.Handle(http.MethodGet, "/raw", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	app.Get("/wrapped", WrapHTTP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := w.(http.Flusher); !ok {
			t.Error("Flusher is not reachable through the Context writer")
		}
		w.WriteHeader(http.StatusAlreadyReported)
	})))

	if w := serve(app, httptest.NewRequest(http.MethodGet, "/tagged", nil)); w.Header().Get("X-Tagged") != "yes" {
		t.Fatal("route middleware did not run")
	}
	if w := serve(app, httptest.NewRequest(http.MethodGet, "/plain", nil)); w.Header().Get("X-Tagged") != "" {
		t.Fatal("route middleware leaked to another route")
	}
	if w := serve(app, httptest.NewRequest(http.MethodGet, "/raw", nil)); w.Code != http.StatusAccepted {
		t.Fatalf("Handle: %d", w.Code)
	}
	if w := serve(app, httptest.NewRequest(http.MethodGet, "/wrapped", nil)); w.Code != http.StatusAlreadyReported {
		t.Fatalf("WrapHTTP: %d", w.Code)
	}
}

func TestNilHandlerPanicsAtRegistration(t *testing.T) {
	app := newTemplateApp(t, false)
	defer func() {
		if recover() == nil {
			t.Fatal("registering a nil Handler did not panic")
		}
	}()
	app.Get("/nil", nil)
}

type signup struct {
	Name     string   `form:"name" json:"name" validate:"required,max=10"`
	Email    string   `form:"email" json:"email" validate:"required,email"`
	Age      *int     `form:"age" json:"age" validate:"min=13"`
	Plan     string   `form:"plan" json:"plan" validate:"oneof=free pro"`
	Tags     []string `form:"tag" json:"tags" validate:"max=2"`
	Site     string   `form:"site" json:"site" validate:"url"`
	Agree    bool     `form:"agree" json:"agree"`
	Internal string   `form:"-" json:"-"`
}

func bindRoute(app *HyperChi) {
	app.Post("/signup", func(c *Context) error {
		in, err := Bind[signup](c)
		if err != nil {
			return err
		}
		return c.JSON(in)
	})
}

func postForm(app *HyperChi, form url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/signup", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	return serve(app, req)
}

func errorFields(t *testing.T, w *httptest.ResponseRecorder) map[string][]string {
	t.Helper()
	var body struct {
		Fields map[string][]string `json:"fields"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %q: %v", w.Body.String(), err)
	}
	return body.Fields
}

func TestBindForm(t *testing.T) {
	app := newTemplateApp(t, false)
	bindRoute(app)

	w := postForm(app, url.Values{
		"name": {"Ada"}, "email": {"ada@example.com"}, "age": {"36"}, "plan": {"pro"},
		"tag": {"a", "b"}, "site": {"https://example.com"}, "agree": {"on"}, "Internal": {"x"},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("%d %q", w.Code, w.Body.String())
	}
	var got signup
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Name != "Ada" || got.Age == nil || *got.Age != 36 || len(got.Tags) != 2 || !got.Agree || got.Internal != "" {
		t.Fatalf("decoded %+v", got)
	}

	// Optional fields left empty pass; required ones do not.
	w = postForm(app, url.Values{"name": {"Ada"}, "email": {"ada@example.com"}})
	if w.Code != http.StatusOK {
		t.Fatalf("optional fields: %d %q", w.Code, w.Body.String())
	}
}

func TestBindValidationFailures(t *testing.T) {
	app := newTemplateApp(t, false)
	bindRoute(app)

	w := postForm(app, url.Values{
		"name": {"Bartholomew the Long"}, "email": {"not-an-email"}, "age": {"9"},
		"plan": {"gold"}, "tag": {"a", "b", "c"}, "site": {"ftp://x"},
	})
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d %q", w.Code, w.Body.String())
	}
	fields := errorFields(t, w)
	want := map[string]string{
		"name":  "must be at most 10 characters",
		"email": "must be a valid email address",
		"age":   "must be at least 13",
		"plan":  "must be one of: free, pro",
		"tag":   "must be at most 2 items",
		"site":  "must be a valid URL",
	}
	for field, msg := range want {
		if len(fields[field]) != 1 || fields[field][0] != msg {
			t.Errorf("%s: got %v, want %q", field, fields[field], msg)
		}
	}

	w = postForm(app, url.Values{"email": {"ada@example.com"}, "age": {"twelve"}})
	fields = errorFields(t, w)
	if w.Code != http.StatusUnprocessableEntity || fields["name"][0] != "is required" || fields["age"][0] != "must be a whole number" {
		t.Fatalf("required and conversion: %d %v", w.Code, fields)
	}
}

func TestBindJSON(t *testing.T) {
	app := newTemplateApp(t, false)
	bindRoute(app)

	req := httptest.NewRequest(http.MethodPost, "/signup", strings.NewReader(`{"name":"Ada","email":"ada@example.com","tags":["x"]}`))
	req.Header.Set("Content-Type", "application/json")
	w := serve(app, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"name":"Ada"`) {
		t.Fatalf("%d %q", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/signup", strings.NewReader(`{"name":`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if w := serve(app, req); w.Code != http.StatusBadRequest {
		t.Fatalf("malformed JSON: %d", w.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/signup", strings.NewReader(`{"name":"Ada"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	w = serve(app, req)
	if w.Code != http.StatusUnprocessableEntity || errorFields(t, w)["email"][0] != "is required" {
		t.Fatalf("JSON validation: %d %q", w.Code, w.Body.String())
	}
}

type upload struct {
	Title string                `form:"title" validate:"required"`
	File  *multipart.FileHeader `form:"file"`
}

func TestBindMultipart(t *testing.T) {
	app := newTemplateApp(t, false)
	app.Post("/upload", func(c *Context) error {
		in, err := Bind[upload](c)
		if err != nil {
			return err
		}
		if in.File == nil {
			return BadRequest("no file")
		}
		return c.JSON(H{"title": in.Title, "file": in.File.Filename, "size": in.File.Size})
	})

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if err := mw.WriteField("title", "Report"); err != nil {
		t.Fatal(err)
	}
	part, err := mw.CreateFormFile("file", "report.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := serve(app, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"file":"report.txt"`) || !strings.Contains(w.Body.String(), `"size":5`) {
		t.Fatalf("%d %q", w.Code, w.Body.String())
	}
}

type withRule struct {
	A string `form:"a"`
	B string `form:"b"`
}

func (w withRule) Validate() error {
	if w.A == w.B {
		return errors.New("a and b must differ")
	}
	return nil
}

type badTag struct {
	A string `form:"a" validate:"requird"`
}

func TestBindValidateMethodAndBadTags(t *testing.T) {
	app := newTemplateApp(t, false)
	app.Post("/rule", func(c *Context) error {
		if _, err := Bind[withRule](c); err != nil {
			return err
		}
		return c.NoContent()
	})
	app.Post("/bad", func(c *Context) error {
		if _, err := Bind[badTag](c); err != nil {
			return err
		}
		return c.NoContent()
	})

	req := httptest.NewRequest(http.MethodPost, "/rule", strings.NewReader("a=x&b=x"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	w := serve(app, req)
	if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "a and b must differ") {
		t.Fatalf("Validate method: %d %q", w.Code, w.Body.String())
	}

	// A typo in a rule is a programming error: a 500, not a user-facing 422.
	req = httptest.NewRequest(http.MethodPost, "/bad", strings.NewReader("a=x"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if w := serve(app, req); w.Code != http.StatusInternalServerError {
		t.Fatalf("unknown rule: %d %q", w.Code, w.Body.String())
	}
}

func TestBindNeedsStructPointer(t *testing.T) {
	app := newTemplateApp(t, false)
	c := app.NewContext(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	var s string
	if err := c.Bind(&s); err == nil {
		t.Fatal("Bind into *string succeeded")
	}
	if err := c.Bind(signup{}); err == nil {
		t.Fatal("Bind into a non-pointer succeeded")
	}
}

type resource struct{ calls []string }

func (r *resource) record(name string) Handler {
	return func(c *Context) error {
		r.calls = append(r.calls, name+":"+c.Param("id"))
		return c.NoContent()
	}
}
func (r *resource) Index(c *Context) error  { return r.record("index")(c) }
func (r *resource) New(c *Context) error    { return r.record("new")(c) }
func (r *resource) Create(c *Context) error { return r.record("create")(c) }
func (r *resource) Show(c *Context) error   { return r.record("show")(c) }
func (r *resource) Edit(c *Context) error   { return r.record("edit")(c) }
func (r *resource) Update(c *Context) error { return r.record("update")(c) }
func (r *resource) Delete(c *Context) error { return r.record("delete")(c) }

func TestResourceRoutes(t *testing.T) {
	app := newTemplateApp(t, false)
	rc := &resource{}
	app.Resource("/posts", rc)
	for _, req := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/posts", nil),
		httptest.NewRequest(http.MethodGet, "/posts/new", nil),
		httptest.NewRequest(http.MethodPost, "/posts", nil),
		httptest.NewRequest(http.MethodGet, "/posts/3", nil),
		httptest.NewRequest(http.MethodGet, "/posts/3/edit", nil),
		httptest.NewRequest(http.MethodPatch, "/posts/3", nil),
		httptest.NewRequest(http.MethodDelete, "/posts/3", nil),
	} {
		if w := serve(app, req); w.Code != http.StatusNoContent {
			t.Fatalf("%s %s: %d", req.Method, req.URL.Path, w.Code)
		}
	}
	want := "index: new: create: show:3 edit:3 update:3 delete:3"
	if got := strings.Join(rc.calls, " "); got != want {
		t.Fatalf("calls = %q, want %q", got, want)
	}
}

func TestBindMap(t *testing.T) {
	app := newTemplateApp(t, false)
	app.Post("/m", func(c *Context) error {
		m, err := Bind[map[string]any](c)
		if err != nil {
			return err
		}
		return c.JSON(m)
	})

	req := httptest.NewRequest(http.MethodPost, "/m", strings.NewReader(`{"a":1,"b":["x"]}`))
	req.Header.Set("Content-Type", "application/json")
	if w := serve(app, req); w.Body.String() != "{\"a\":1,\"b\":[\"x\"]}\n" {
		t.Fatalf("JSON map: %q", w.Body.String())
	}
	req = httptest.NewRequest(http.MethodPost, "/m?q=1", strings.NewReader("tag=a&tag=b&name=Ada"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if w := serve(app, req); w.Body.String() != "{\"name\":\"Ada\",\"q\":\"1\",\"tag\":[\"a\",\"b\"]}\n" {
		t.Fatalf("form map: %q", w.Body.String())
	}
}

type usersAPI struct{}

func (usersAPI) Index(c *Context) error  { return c.JSON([]string{}) }
func (usersAPI) Create(c *Context) error { return BadRequest("No name.") }
func (usersAPI) Show(c *Context) error   { return NotFound("No such user.") }
func (usersAPI) Update(c *Context) error { return c.NoContent() }
func (usersAPI) Delete(c *Context) error { return c.NoContent() }

func TestJSONErrorsForcesJSONWhateverTheClientAccepts(t *testing.T) {
	app := newTemplateApp(t, false)
	app.Get("/api/thing", func(c *Context) error { return NotFound("No such thing.") }, JSONErrors)
	app.Get("/page", func(c *Context) error { return NotFound("No such page.") })
	app.Group("/v1", func(api *HyperChi) {
		api.Use(JSONErrors)
		api.Get("/thing", func(c *Context) error { return Forbidden("Nope.") })
	})
	app.RESTful("/users", usersAPI{})

	cases := []struct {
		name, path string
		code       int
		json       bool
	}{
		{"route opt-in", "/api/thing", http.StatusNotFound, true},
		{"group opt-in", "/v1/thing", http.StatusForbidden, true},
		{"RESTful by default", "/users/7", http.StatusNotFound, true},
		{"other routes negotiate", "/page", http.StatusNotFound, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			req.Header.Set("Accept", "text/html")
			req.Header.Set("HX-Request", "true")
			w := serve(app, req)
			isJSON := strings.HasPrefix(w.Header().Get("Content-Type"), "application/json")
			if w.Code != tc.code || isJSON != tc.json {
				t.Fatalf("%s: %d json=%v body %q", tc.path, w.Code, isJSON, w.Body.String())
			}
		})
	}
}
