package tests

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/regiellis/hyperchi/hyperchi"
	"github.com/regiellis/hyperchi/hyperchi/helpers"
	"github.com/regiellis/hyperchi/hyperchi/validator"
)

// setupTestEnvironment returns a temp directory and an app with AutoMiddleware
// and the test templates loaded from it.
func setupTestEnvironment(t *testing.T) (string, *hyperchi.HyperChi) {
	tmpDir := t.TempDir()

	templatesDir := filepath.Join(tmpDir, "templates")

	if err := os.MkdirAll(templatesDir, 0755); err != nil {
		t.Fatalf("Failed to create templates directory: %v", err)
	}

	createTestTemplates(t, templatesDir)

	app := newFormApp()

	app.AutoMiddleware()

	if err := app.LoadTemplates(filepath.Join(templatesDir, "*.tmpl")); err != nil {
		t.Fatalf("Failed to load templates: %v", err)
	}

	return tmpDir, app
}

func createTestTemplates(t *testing.T, dir string) {
	layoutBaseTemplate := `{{define "layout-base"}}<!DOCTYPE html>
<html>
<head>
    <title>{{.title}}</title>
</head>
<body>
    {{template "content" .}}
</body>
</html>{{end}}`

	homeTemplate := `{{define "home"}}{{template "layout-base" .}}{{end}}
{{define "content"}}<h1>{{.title}}</h1>
<p>Welcome to HyperChi!</p>{{end}}`

	formTemplate := `{{define "form"}}{{template "layout-base" .}}{{end}}
{{define "content"}}<form hx-post="/contact" hx-target="#result">
    <input type="text" name="name" required>
    <input type="email" name="email" required>
    <button type="submit">Submit</button>
</form>
<div id="result"></div>{{end}}`

	listTemplate := `{{define "list"}}{{template "layout-base" .}}{{end}}
{{define "content"}}<ul>
{{range .items}}
    <li>{{.}}</li>
{{end}}
</ul>{{end}}`

	templates := map[string]string{
		"layout-base.tmpl": layoutBaseTemplate,
		"home.tmpl":        homeTemplate,
		"form.tmpl":        formTemplate,
		"list.tmpl":        listTemplate,
	}

	for name, content := range templates {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatalf("Failed to write template %s: %v", name, err)
		}
	}
}

func TestBasicRouting(t *testing.T) {
	_, app := setupTestEnvironment(t)

	app.Get("/", func(c *hyperchi.Context) error {
		return c.View("home", hyperchi.H{"title": "Home Page"})
	})

	app.Get("/about", func(c *hyperchi.Context) error {
		return c.JSON(hyperchi.H{"message": "About page"})
	})

	app.Post("/api/data", func(c *hyperchi.Context) error {
		return c.Status(http.StatusCreated).JSON(hyperchi.H{"status": "created"})
	})

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("GET / returned %d, expected 200", rec.Code)
	}

	if !strings.Contains(rec.Body.String(), "Home Page") {
		t.Error("Response should contain 'Home Page'")
	}

	req = httptest.NewRequest("GET", "/about", nil)
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("GET /about returned %d, expected 200", rec.Code)
	}

	if !strings.Contains(rec.Body.String(), `"message":"About page"`) {
		t.Error("Response should contain JSON message")
	}

	req = httptest.NewRequest("POST", "/api/data", nil)
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Errorf("POST /api/data returned %d, expected 201", rec.Code)
	}
}

func TestHTMXHandling(t *testing.T) {
	_, app := setupTestEnvironment(t)

	app.Get("/fragment", func(c *hyperchi.Context) error {
		if c.IsHTMX() {
			return c.HTML("<div>HTMX Fragment</div>")
		}
		return c.JSON(hyperchi.H{"htmx": false})
	})

	// Test with HTMX header
	req := httptest.NewRequest("GET", "/fragment", nil)
	req.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("GET /fragment with HTMX returned %d, expected 200", rec.Code)
	}

	if !strings.Contains(rec.Body.String(), "HTMX Fragment") {
		t.Error("Response should contain HTMX fragment")
	}

	// Test without HTMX header
	req = httptest.NewRequest("GET", "/fragment", nil)
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	if !strings.Contains(rec.Body.String(), `"htmx":false`) {
		t.Error("Response should contain JSON with htmx:false")
	}
}

func TestFormValidation(t *testing.T) {
	_, app := setupTestEnvironment(t)

	contactSchema := validator.NewSchema()
	contactSchema.Field("name", validator.String().Required().MinLength(2).MaxLength(50))
	contactSchema.Field("email", validator.String().Required().Email())

	app.Post("/contact", func(c *hyperchi.Context) error {
		data := map[string]any{
			"name":  c.FormValue("name"),
			"email": c.FormValue("email"),
		}

		result := contactSchema.Validate(data)
		if !result.Valid {
			return c.Status(http.StatusBadRequest).JSON(hyperchi.H{"errors": result.Errors})
		}

		return c.JSON(hyperchi.H{"status": "success"})
	})

	form := strings.NewReader("name=John+Doe&email=john@example.com")
	req := httptest.NewRequest("POST", "/contact", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("POST /contact with valid data returned %d, expected 200", rec.Code)
	}

	// Test invalid form submission (missing email)
	form = strings.NewReader("name=John")
	req = httptest.NewRequest("POST", "/contact", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("POST /contact with invalid data returned %d, expected 400", rec.Code)
	}
}

func TestTemplateRendering(t *testing.T) {
	_, app := setupTestEnvironment(t)

	app.Get("/list", func(c *hyperchi.Context) error {
		return c.View("list", hyperchi.H{
			"title": "My List",
			"items": []string{"Item 1", "Item 2", "Item 3"},
		})
	})

	req := httptest.NewRequest("GET", "/list", nil)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("GET /list returned %d, expected 200", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "Item 1") || !strings.Contains(body, "Item 2") || !strings.Contains(body, "Item 3") {
		t.Error("Response should contain all list items")
	}
}

func TestMiddleware(t *testing.T) {
	_, app := setupTestEnvironment(t)

	callCount := 0
	app.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			callCount++
			next.ServeHTTP(w, r)
		})
	})

	app.Get("/middleware-test", func(c *hyperchi.Context) error {
		return c.JSON(hyperchi.H{"status": "ok"})
	})

	req := httptest.NewRequest("GET", "/middleware-test", nil)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	if callCount != 1 {
		t.Errorf("Middleware called %d times, expected 1", callCount)
	}

	if rec.Code != http.StatusOK {
		t.Errorf("GET /middleware-test returned %d, expected 200", rec.Code)
	}
}

func TestErrorHandling(t *testing.T) {
	_, app := setupTestEnvironment(t)

	app.Get("/error", func(c *hyperchi.Context) error {
		return errors.New("something went wrong")
	})

	req := httptest.NewRequest("GET", "/error", nil)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("GET /error returned %d, expected 500", rec.Code)
	}
}

func TestJSONHelpers(t *testing.T) {
	_, app := setupTestEnvironment(t)

	app.Get("/json-builder", func(c *hyperchi.Context) error {
		data := helpers.NewJSON().
			Set("name", "HyperChi").
			Set("version", "1.0.0").
			Set("features", []string{"routing", "templates", "validation"})

		return c.JSON(data)
	})

	req := httptest.NewRequest("GET", "/json-builder", nil)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("GET /json-builder returned %d, expected 200", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, `"name":"HyperChi"`) {
		t.Error("Response should contain name field")
	}
	if !strings.Contains(body, `"version":"1.0.0"`) {
		t.Error("Response should contain version field")
	}
}

func TestParamExtraction(t *testing.T) {
	_, app := setupTestEnvironment(t)

	app.Get("/users/{id}", func(c *hyperchi.Context) error {
		return c.JSON(hyperchi.H{"userId": c.Param("id")})
	})

	req := httptest.NewRequest("GET", "/users/123", nil)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("GET /users/123 returned %d, expected 200", rec.Code)
	}

	if !strings.Contains(rec.Body.String(), `"userId":"123"`) {
		t.Error("Response should contain userId")
	}
}

func TestStaticFileServing(t *testing.T) {
	tmpDir, app := setupTestEnvironment(t)

	staticDir := filepath.Join(tmpDir, "static")
	if err := os.MkdirAll(staticDir, 0755); err != nil {
		t.Fatalf("Failed to create static directory: %v", err)
	}

	testContent := "test static content"
	if err := os.WriteFile(filepath.Join(staticDir, "test.txt"), []byte(testContent), 0644); err != nil {
		t.Fatalf("Failed to write static test file: %v", err)
	}

	app.Static("/static/*", staticDir)

	req := httptest.NewRequest("GET", "/static/test.txt", nil)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("GET /static/test.txt returned %d, expected 200", rec.Code)
	}

	if rec.Body.String() != testContent {
		t.Error("Static file content mismatch")
	}
}
