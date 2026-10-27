package hyperchi

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func decodeBody(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %q: %v", w.Body.String(), err)
	}
	return body
}

func TestParamInt64AndUUID(t *testing.T) {
	app := New()
	app.Get("/n/{id}", func(c *Context) error {
		id, err := c.ParamInt64("id")
		if err != nil {
			return err
		}
		return c.JSON(H{"id": id})
	})
	app.Get("/u/{id}", func(c *Context) error {
		id, err := c.ParamUUID("id")
		if err != nil {
			return err
		}
		return c.Text(id)
	})

	w := serve(app, httptest.NewRequest(http.MethodGet, "/n/9007199254740993", nil))
	if !strings.Contains(w.Body.String(), "9007199254740993") {
		t.Fatalf("int64 beyond int32 and float precision: %q", w.Body.String())
	}
	if w = serve(app, httptest.NewRequest(http.MethodGet, "/n/1x", nil)); w.Code != http.StatusBadRequest {
		t.Fatalf("non-integer: %d", w.Code)
	}

	w = serve(app, httptest.NewRequest(http.MethodGet, "/u/0F8FAD5B-D9CB-469F-A165-70867728950E", nil))
	if w.Code != http.StatusOK || w.Body.String() != "0f8fad5b-d9cb-469f-a165-70867728950e" {
		t.Fatalf("uuid: %d %q", w.Code, w.Body.String())
	}
	for _, bad := range []string{
		"0f8fad5b-d9cb-469f-a165-70867728950",    // short
		"0f8fad5bd9cb-469f-a165-70867728950ee",   // hyphen moved
		"0f8fad5b-d9cb-469f-a165-70867728950g",   // not hex
		"{0f8fad5b-d9cb-469f-a165-70867728950e}", // braces
	} {
		if w = serve(app, httptest.NewRequest(http.MethodGet, "/u/"+url.PathEscape(bad), nil)); w.Code != http.StatusBadRequest {
			t.Errorf("%q: status %d, want 400", bad, w.Code)
		}
	}
}

func TestQueryTypedReaders(t *testing.T) {
	app := New()
	app.Get("/q", func(c *Context) error {
		return c.JSON(H{
			"big":   c.QueryInt64("big", -1),
			"price": c.QueryFloat("price", -1),
			"nan":   c.QueryFloat("nan", -1),
			"on":    c.QueryBool("on", false),
			"no":    c.QueryBool("no", true),
			"junk":  c.QueryBool("junk", true),
			"none":  c.QueryBool("none", true),
		})
	})

	w := serve(app, httptest.NewRequest(http.MethodGet, "/q?big=5000000000&price=9.5&nan=NaN&on=on&no=false&junk=maybe", nil))
	body := decodeBody(t, w)
	want := map[string]any{"big": float64(5000000000), "price": 9.5, "nan": float64(-1), "on": true, "no": false, "junk": true, "none": true}
	for k, v := range want {
		if body[k] != v {
			t.Errorf("%s = %v, want %v", k, body[k], v)
		}
	}
}

func TestFormTypedReaders(t *testing.T) {
	app := New()
	app.Post("/f", func(c *Context) error {
		return c.JSON(H{
			"tags":      c.FormValues("tag"),
			"qty":       c.FormInt("qty", 1),
			"badQty":    c.FormInt("bad", 1),
			"subscribe": c.FormBool("subscribe"),
			"terms":     c.FormBool("terms"),
			"absent":    c.FormBool("absent"),
			"custom":    c.FormBool("custom"),
		})
	})

	// The hidden "false" before the checked box is the Rails pattern: last wins.
	form := url.Values{"tag": {"go", "htmx"}, "qty": {"3"}, "bad": {"x"}, "subscribe": {"false", "on"}, "terms": {"false"}, "custom": {"newsletter"}}
	req := httptest.NewRequest(http.MethodPost, "/f", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	body := decodeBody(t, serve(app, req))
	if tags, ok := body["tags"].([]any); !ok || len(tags) != 2 || tags[0] != "go" || tags[1] != "htmx" {
		t.Errorf("tags = %v", body["tags"])
	}
	want := map[string]any{"qty": float64(3), "badQty": float64(1), "subscribe": true, "terms": false, "absent": false, "custom": true}
	for k, v := range want {
		if body[k] != v {
			t.Errorf("%s = %v, want %v", k, body[k], v)
		}
	}
}

func TestFormReadersParseMultipart(t *testing.T) {
	app := New()
	app.Post("/f", func(c *Context) error {
		return c.JSON(H{"tags": c.FormValues("tag"), "qty": c.FormInt("qty", 0)})
	})

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for _, f := range [][2]string{{"tag", "a"}, {"tag", "b"}, {"qty", "7"}} {
		if err := mw.WriteField(f[0], f[1]); err != nil {
			t.Fatal(err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/f", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	body := decodeBody(t, serve(app, req))
	if tags, ok := body["tags"].([]any); !ok || len(tags) != 2 || body["qty"] != float64(7) {
		t.Fatalf("multipart: %v", body)
	}
}

func TestWantsJSON(t *testing.T) {
	app := New()
	app.Get("/n", func(c *Context) error { return c.JSON(H{"json": c.WantsJSON()}) })

	for accept, want := range map[string]bool{
		"application/json":                 true,
		"text/html,application/xhtml+xml":  false,
		"text/html, application/json;q=.9": false,
		"":                                 false,
	} {
		req := httptest.NewRequest(http.MethodGet, "/n", nil)
		req.Header.Set("Accept", accept)
		if got := decodeBody(t, serve(app, req))["json"]; got != want {
			t.Errorf("Accept %q: %v, want %v", accept, got, want)
		}
	}
}

func TestAttachment(t *testing.T) {
	app := New()
	app.Get("/dl", func(c *Context) error {
		return c.Attachment("report 2026.csv", "text/csv", []byte("a,b\n"))
	})
	app.Get("/dl-utf8", func(c *Context) error {
		return c.Attachment("résumé.pdf", "", []byte("%PDF"))
	})

	w := serve(app, httptest.NewRequest(http.MethodGet, "/dl", nil))
	if got := w.Header().Get("Content-Disposition"); got != `attachment; filename="report 2026.csv"` {
		t.Errorf("disposition = %q", got)
	}
	if w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("Content-Type") != "text/csv" || w.Body.String() != "a,b\n" {
		t.Errorf("headers %v body %q", w.Header(), w.Body.String())
	}

	w = serve(app, httptest.NewRequest(http.MethodGet, "/dl-utf8", nil))
	if got := w.Header().Get("Content-Disposition"); !strings.Contains(got, "filename*=utf-8''r%C3%A9sum%C3%A9.pdf") {
		t.Errorf("non-ASCII disposition = %q", got)
	}
	if got := w.Header().Get("Content-Type"); got != "application/octet-stream" {
		t.Errorf("default type = %q", got)
	}
}

func TestBackStaysOnSite(t *testing.T) {
	app := New()
	app.Post("/save", func(c *Context) error { return c.Back("/home") })

	cases := map[string]string{
		"http://example.com/items?page=2": "/items?page=2",
		"https://EXAMPLE.com/a":           "/a",
		"https://evil.com/phish":          "/home",
		"https://example.com//evil.com/x": "/home",
		"javascript:alert(1)":             "/home",
		"":                                "/home",
	}
	for referer, want := range cases {
		req := httptest.NewRequest(http.MethodPost, "http://example.com/save", nil)
		if referer != "" {
			req.Header.Set("Referer", referer)
		}
		w := serve(app, req)
		if got := w.Header().Get("Location"); w.Code != http.StatusSeeOther || got != want {
			t.Errorf("Referer %q: %d %q, want %q", referer, w.Code, got, want)
		}
	}

	req := htmxReq(http.MethodPost, "http://example.com/save")
	req.Header.Set("Referer", "http://example.com/list")
	if w := serve(app, req); w.Header().Get("HX-Redirect") != "/list" {
		t.Errorf("htmx Back: %v", w.Header())
	}
}

func TestETagFreshNotModified(t *testing.T) {
	app := New()
	page := func(c *Context) error {
		c.ETag("v42")
		if c.Fresh() {
			return c.NotModified()
		}
		return c.Text("body")
	}
	app.Get("/p", page)
	app.Post("/p", page)

	cases := []struct {
		method, inm string
		code        int
	}{
		{http.MethodGet, "", http.StatusOK},
		{http.MethodGet, `"v42"`, http.StatusNotModified},
		{http.MethodGet, `"v1", W/"v42"`, http.StatusNotModified},
		{http.MethodGet, `*`, http.StatusNotModified},
		{http.MethodGet, `"v41"`, http.StatusOK},
		{http.MethodPost, `"v42"`, http.StatusOK},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(tc.method, "/p", nil)
		if tc.inm != "" {
			req.Header.Set("If-None-Match", tc.inm)
		}
		w := serve(app, req)
		if w.Code != tc.code || w.Header().Get("ETag") != `"v42"` {
			t.Errorf("%s If-None-Match %q: %d etag %q, want %d", tc.method, tc.inm, w.Code, w.Header().Get("ETag"), tc.code)
		}
		if tc.code == http.StatusNotModified && w.Body.Len() != 0 {
			t.Errorf("304 carried a body: %q", w.Body.String())
		}
	}

	app.Get("/weak", func(c *Context) error { return c.ETag(`W/"x"`).Text("w") })
	if got := serve(app, httptest.NewRequest(http.MethodGet, "/weak", nil)).Header().Get("ETag"); got != `W/"x"` {
		t.Errorf("weak tag rewritten to %q", got)
	}
}
