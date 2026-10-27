package hyperchi

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/regiellis/hyperchi/hyperchi/validator"
)

func TestValidateSchemaUsesRegisteredSchema(t *testing.T) {
	app := newTemplateApp(t, false)
	app.Schemas.Register("signup", validator.NewSchema().Field("email", validator.String().Required().Email()))
	app.Post("/signup", func(c *Context) error {
		data, err := c.ValidateSchema("signup")
		if err != nil {
			return err
		}
		return c.JSON(data)
	})
	app.Post("/missing", func(c *Context) error {
		_, err := c.ValidateSchema("nope")
		return err
	})

	post := func(path string, form url.Values) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Accept", "application/json")
		return serve(app, req)
	}
	if w := post("/signup", url.Values{"email": {"ada@example.com"}}); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "ada@example.com") {
		t.Fatalf("valid: %d %q", w.Code, w.Body.String())
	}
	w := post("/signup", url.Values{"email": {"nope"}})
	if w.Code != http.StatusUnprocessableEntity || len(errorFields(t, w)["email"]) == 0 {
		t.Fatalf("invalid: %d %q", w.Code, w.Body.String())
	}
	if w := post("/missing", url.Values{}); w.Code != http.StatusInternalServerError {
		t.Fatalf("unregistered schema: %d", w.Code)
	}
	if app.Schemas.Len() != 1 {
		t.Fatalf("Len = %d", app.Schemas.Len())
	}
}

func TestContentCollectionSchemaValidatesCreate(t *testing.T) {
	app := newTemplateApp(t, false)
	app.Content.Collection("posts", t.TempDir(), validator.NewSchema().Field("title", validator.String().Required()))

	err := app.Content.Create("posts", &ContentItem{Slug: "untitled", Metadata: map[string]any{}})
	var he *HTTPError
	if !errors.As(err, &he) || he.Code != http.StatusUnprocessableEntity || len(he.Fields["title"]) == 0 {
		t.Fatalf("missing title accepted or wrong error: %v", err)
	}
	if err := app.Content.Create("posts", &ContentItem{Slug: "hello", Metadata: map[string]any{"title": "Hello"}}); err != nil {
		t.Fatalf("valid item rejected: %v", err)
	}
}
