package hyperchi_test

import (
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/regiellis/hyperchi/hyperchi"
)

// exampleApp returns an app with a layout and two templates loaded from a
// temporary directory, standing in for a project's templates/ folder.
func exampleApp() (*hyperchi.HyperChi, func()) {
	dir, err := os.MkdirTemp("", "hyperchi-example")
	if err != nil {
		log.Fatal(err)
	}
	files := map[string]string{
		"layout.tmpl": `{{define "layout-base"}}<html>{{.content}}</html>{{end}}`,
		"users.tmpl":  `{{define "users"}}<ul>{{range .users}}<li>{{.}}</li>{{end}}</ul>{{end}}`,
		"row.tmpl":    `{{define "row"}}<li>{{.name}}</li>{{end}}`,
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			log.Fatal(err)
		}
	}
	app := hyperchi.New()
	if err := app.LoadTemplates(filepath.Join(dir, "*.tmpl")); err != nil {
		log.Fatal(err)
	}
	return app, func() {
		if err := app.CloseDB(); err != nil {
			log.Print(err)
		}
		if err := os.RemoveAll(dir); err != nil {
			log.Print(err)
		}
	}
}

func do(app *hyperchi.HyperChi, req *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	app.ServeHTTP(w, req)
	return w
}

// A page route renders its template inside the layout for a browser, and the
// template alone for an htmx partial request.
func ExampleContext_View() {
	app, done := exampleApp()
	defer done()

	app.Get("/users", func(c *hyperchi.Context) error {
		return c.View("users", hyperchi.H{"users": []string{"Ada", "Grace"}})
	})

	fmt.Println(do(app, httptest.NewRequest(http.MethodGet, "/users", nil)).Body.String())

	req := httptest.NewRequest(http.MethodGet, "/users", nil)
	req.Header.Set("HX-Request", "true")
	fmt.Println(do(app, req).Body.String())
	// Output:
	// <html><ul><li>Ada</li><li>Grace</li></ul></html>
	// <ul><li>Ada</li><li>Grace</li></ul>
}

type newUser struct {
	Name  string `form:"name" validate:"required,max=40"`
	Email string `form:"email" validate:"required,email"`
}

// Bind decodes and validates a form. A failed check comes back as a 422 error
// with per-field messages, which the handler returns as is.
func ExampleBind() {
	app, done := exampleApp()
	defer done()

	app.Post("/users", func(c *hyperchi.Context) error {
		in, err := hyperchi.Bind[newUser](c)
		if err != nil {
			return err
		}
		return c.Status(http.StatusCreated).Fragment("row", hyperchi.H{"name": in.Name})
	})

	post := func(form url.Values) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Accept", "application/json")
		return do(app, req)
	}

	w := post(url.Values{"name": {"Ada"}, "email": {"ada@example.com"}})
	fmt.Println(w.Code, w.Body.String())

	w = post(url.Values{"name": {"Ada"}, "email": {"nope"}})
	fmt.Println(w.Code, strings.TrimSpace(w.Body.String()))
	// Output:
	// 201 <li>Ada</li>
	// 422 {"error":"Please correct the highlighted fields.","fields":{"email":["must be a valid email address"]}}
}

// Return an HTTPError to pick the status. The error handler renders it for the
// client that asked: here, JSON for an API client.
func ExampleNotFound() {
	app, done := exampleApp()
	defer done()

	app.Get("/users/{id}", func(c *hyperchi.Context) error {
		id, err := c.ParamInt("id")
		if err != nil {
			return err
		}
		return hyperchi.NotFound(fmt.Sprintf("No user %d.", id))
	})

	req := httptest.NewRequest(http.MethodGet, "/users/7", nil)
	req.Header.Set("Accept", "application/json")
	w := do(app, req)
	fmt.Println(w.Code, strings.TrimSpace(w.Body.String()))
	// Output:
	// 404 {"error":"No user 7."}
}

// HX sets htmx response headers and finishes with a response method.
func ExampleContext_HX() {
	app, done := exampleApp()
	defer done()

	app.Post("/users", func(c *hyperchi.Context) error {
		return c.HX().Trigger("user-added").Retarget("#users").Reswap("beforeend").
			Fragment("row", hyperchi.H{"name": "Grace"})
	})

	w := do(app, httptest.NewRequest(http.MethodPost, "/users", nil))
	fmt.Println(w.Header().Get("HX-Trigger"), w.Header().Get("HX-Retarget"), w.Header().Get("HX-Reswap"))
	fmt.Println(w.Body.String())
	// Output:
	// user-added #users beforeend
	// <li>Grace</li>
}

// Redirect sends HX-Redirect to htmx and a 303 to everything else.
func ExampleContext_Redirect() {
	app, done := exampleApp()
	defer done()

	app.Post("/login", func(c *hyperchi.Context) error { return c.Redirect("/home") })

	w := do(app, httptest.NewRequest(http.MethodPost, "/login", nil))
	fmt.Println(w.Code, w.Header().Get("Location"))

	req := httptest.NewRequest(http.MethodPost, "/login", nil)
	req.Header.Set("HX-Request", "true")
	w = do(app, req)
	fmt.Println(w.Code, w.Header().Get("HX-Redirect"))
	// Output:
	// 303 /home
	// 200 /home
}
