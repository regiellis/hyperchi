package tests

import (
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/regiellis/hyperchi/hyperchi"
	"github.com/regiellis/hyperchi/hyperchi/validator"
)

func TestEndToEndScenario(t *testing.T) {
	tmpDir := t.TempDir()
	templatesDir := filepath.Join(tmpDir, "templates")
	if err := os.MkdirAll(templatesDir, 0755); err != nil {
		t.Fatalf("Failed to create templates directory: %v", err)
	}

	createE2ETemplates(t, templatesDir)

	app := newFormApp()
	app.AutoMiddleware()

	app.Use(app.Middleware.RateLimit(10, time.Minute))

	if err := app.LoadTemplates(filepath.Join(templatesDir, "*.tmpl")); err != nil {
		t.Fatalf("Failed to load templates: %v", err)
	}

	setupE2ERoutes(app)

	t.Run("CompleteUserFlow", func(t *testing.T) {
		// 1. Visit home page
		req := httptest.NewRequest("GET", "/", nil)
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("GET / returned %d, expected 200", rec.Code)
		}

		// 2. Submit contact form with HTMX
		form := url.Values{}
		form.Add("name", "Jane Doe")
		form.Add("email", "jane@example.com")
		form.Add("message", "This is a test message")

		req = httptest.NewRequest("POST", "/contact", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("HX-Request", "true")
		rec = httptest.NewRecorder()
		app.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("POST /contact returned %d, expected 200", rec.Code)
		}

		if !strings.Contains(rec.Body.String(), "Thank you") {
			t.Error("Response should contain thank you message")
		}

		// 3. Search functionality
		req = httptest.NewRequest("GET", "/search?q=test", nil)
		req.Header.Set("HX-Request", "true")
		rec = httptest.NewRecorder()
		app.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("GET /search returned %d, expected 200", rec.Code)
		}

		// 4. API endpoint
		req = httptest.NewRequest("GET", "/api/status", nil)
		rec = httptest.NewRecorder()
		app.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("GET /api/status returned %d, expected 200", rec.Code)
		}

		if !strings.Contains(rec.Body.String(), `"status":"ok"`) {
			t.Error("API should return ok status")
		}
	})

	t.Run("RateLimiting", func(t *testing.T) {
		rateLimitApp := hyperchi.New()
		rateLimitApp.AutoMiddleware()
		rateLimitApp.Use(rateLimitApp.Middleware.RateLimit(10, time.Minute))

		rateLimitApp.Get("/api/status", func(c *hyperchi.Context) error {
			return c.JSON(hyperchi.H{
				"status":  "ok",
				"version": "1.0.0",
			})
		})

		// Make 15 requests (limit is 10 per minute)
		for i := 0; i < 15; i++ {
			req := httptest.NewRequest("GET", "/api/status", nil)
			rec := httptest.NewRecorder()
			rateLimitApp.ServeHTTP(rec, req)

			if i < 10 {
				if rec.Code != http.StatusOK {
					t.Errorf("Request %d returned %d, expected 200", i+1, rec.Code)
				}
			} else {
				if rec.Code != http.StatusTooManyRequests {
					t.Errorf("Request %d returned %d, expected 429", i+1, rec.Code)
				}
			}
		}
	})
}

func createE2ETemplates(t *testing.T, dir string) {
	layoutBaseTemplate := `{{define "layout-base"}}<!DOCTYPE html>
<html>
<head>
    <title>{{.title}}</title>
    <script src="https://unpkg.com/htmx.org@4.0.0/dist/htmx.min.js"></script>
</head>
<body>
    <nav>
        <a href="/">Home</a>
        <a href="/about">About</a>
        <a href="/contact">Contact</a>
    </nav>
    <main>
        {{template "content" .}}
    </main>
</body>
</html>{{end}}`

	homeTemplate := `{{define "home"}}{{template "layout-base" .}}{{end}}
{{define "content"}}<h1>{{.title}}</h1>
<p>{{.message}}</p>
<div id="search-container">
    <input type="text"
           name="q"
           placeholder="Search..."
           hx-get="/search"
           hx-trigger="keyup changed delay:500ms"
           hx-target="#search-results">
    <div id="search-results"></div>
</div>{{end}}`

	contactTemplate := `{{define "contact"}}{{template "layout-base" .}}{{end}}
{{define "content"}}<h1>Contact Us</h1>
<form hx-post="/contact" hx-target="#form-result">
    <input type="text" name="name" placeholder="Name" required>
    <input type="email" name="email" placeholder="Email" required>
    <textarea name="message" placeholder="Message" required></textarea>
    <button type="submit">Send</button>
</form>
<div id="form-result"></div>{{end}}`

	templates := map[string]string{
		"layout-base.tmpl": layoutBaseTemplate,
		"home.tmpl":        homeTemplate,
		"contact.tmpl":     contactTemplate,
	}

	for name, content := range templates {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatalf("Failed to write template %s: %v", name, err)
		}
	}
}

func setupE2ERoutes(app *hyperchi.HyperChi) {
	app.Get("/", func(c *hyperchi.Context) error {
		return c.View("home", hyperchi.H{
			"title":   "Welcome to HyperChi",
			"message": "A complete microframework for Go",
		})
	})

	contactSchema := validator.NewSchema()
	contactSchema.Field("name", validator.String().Required().MinLength(2))
	contactSchema.Field("email", validator.String().Required().Email())
	contactSchema.Field("message", validator.String().Required().MinLength(10))

	app.Post("/contact", func(c *hyperchi.Context) error {
		formData := map[string]any{
			"name":    c.FormValue("name"),
			"email":   c.FormValue("email"),
			"message": c.FormValue("message"),
		}

		result := contactSchema.Validate(formData)

		if c.IsHTMX() {
			if len(result.Errors) > 0 {
				html := "<div class='error'>Please fix the following errors:<ul>"
				for _, err := range result.Errors {
					html += "<li>" + template.HTMLEscapeString(err.Field) + ": " + template.HTMLEscapeString(err.Message) + "</li>"
				}
				html += "</ul></div>"
				return c.Status(http.StatusBadRequest).HTML(template.HTML(html))
			}

			return c.HTML(`<div class='success'>Thank you for your message! We'll get back to you soon.</div>`)
		}
		if len(result.Errors) > 0 {
			return c.Status(http.StatusBadRequest).JSON(hyperchi.H{"errors": result.Errors})
		}
		return c.JSON(hyperchi.H{"status": "message sent"})
	})

	app.Get("/search", func(c *hyperchi.Context) error {
		query := c.Query("q")

		results := []string{}
		if query != "" {
			results = []string{
				"Result 1 for " + query,
				"Result 2 for " + query,
				"Result 3 for " + query,
			}
		}

		if c.IsHTMX() {
			html := "<ul>"
			for _, result := range results {
				html += "<li>" + template.HTMLEscapeString(result) + "</li>"
			}
			html += "</ul>"
			return c.HTML(template.HTML(html))
		}
		return c.JSON(hyperchi.H{"results": results})
	})

	app.Get("/api/status", func(c *hyperchi.Context) error {
		return c.JSON(hyperchi.H{
			"status":  "ok",
			"version": "1.0.0",
		})
	})
}

func TestHTMXFeatures(t *testing.T) {
	app := newFormApp()
	app.AutoMiddleware()

	app.Post("/trigger-test", func(c *hyperchi.Context) error {
		c.HX().Trigger("customEvent")
		return c.JSON(hyperchi.H{"triggered": true})
	})

	// c.Redirect answers an htmx request with HX-Redirect instead of a 3xx.
	app.Post("/redirect-test", func(c *hyperchi.Context) error {
		return c.Redirect("/new-location")
	})

	app.Post("/refresh-test", func(c *hyperchi.Context) error {
		c.HX().Refresh()
		return c.JSON(hyperchi.H{"refreshed": true})
	})

	t.Run("HXTrigger", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/trigger-test", nil)
		req.Header.Set("HX-Request", "true")
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)

		if rec.Header().Get("HX-Trigger") != "customEvent" {
			t.Error("HX-Trigger header not set correctly")
		}
	})

	t.Run("HXRedirect", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/redirect-test", nil)
		req.Header.Set("HX-Request", "true")
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)

		if rec.Header().Get("HX-Redirect") != "/new-location" {
			t.Error("HX-Redirect header not set correctly")
		}
	})

	t.Run("HXRefresh", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/refresh-test", nil)
		req.Header.Set("HX-Request", "true")
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)

		if rec.Header().Get("HX-Refresh") != "true" {
			t.Error("HX-Refresh header not set correctly")
		}
	})
}

func TestSecurityFeatures(t *testing.T) {
	app := newFormApp()
	app.AutoMiddleware()

	app.Use(app.Middleware.CSRF())

	app.Post("/protected", func(c *hyperchi.Context) error {
		return c.JSON(hyperchi.H{"status": "success"})
	})

	t.Run("CSRFProtection", func(t *testing.T) {
		// Request without CSRF token should fail
		req := httptest.NewRequest("POST", "/protected", nil)
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Errorf("Expected 403 Forbidden without CSRF token, got %d", rec.Code)
		}
	})
}
