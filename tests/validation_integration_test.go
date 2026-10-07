package tests

import (
	"bytes"
	"encoding/json"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/regiellis/hyperchi/hyperchi"
	"github.com/regiellis/hyperchi/hyperchi/validator"
)

func TestValidationIntegration(t *testing.T) {
	app := newFormApp()
	app.AutoMiddleware()

	userSchema := validator.NewSchema()
	userSchema.Field("name", validator.String().Required().MinLength(2).MaxLength(50))
	userSchema.Field("email", validator.String().Required().Email())
	userSchema.Field("age", validator.Number().Min(18).Max(100))

	app.Post("/api/users", func(c *hyperchi.Context) error {
		var data map[string]any
		if err := json.NewDecoder(c.Request().Body).Decode(&data); err != nil {
			return hyperchi.BadRequest("Invalid JSON").Wrap(err)
		}

		result := userSchema.Validate(data)
		if !result.Valid {
			return c.Status(http.StatusBadRequest).JSON(hyperchi.H{"errors": result.Errors})
		}

		return c.JSON(hyperchi.H{
			"status": "success",
			"user":   data,
		})
	})

	t.Run("ValidUser", func(t *testing.T) {
		user := map[string]any{
			"name":  "John Doe",
			"email": "john@example.com",
			"age":   25,
		}

		body := mustJSON(t, user)
		req := httptest.NewRequest("POST", "/api/users", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		app.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d", rec.Code)
		}

		var resp map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("Unmarshal response failed: %v", err)
		}

		if resp["status"] != "success" {
			t.Error("Expected success status")
		}
	})

	t.Run("InvalidEmail", func(t *testing.T) {
		user := map[string]any{
			"name":  "John Doe",
			"email": "invalid-email",
			"age":   25,
		}

		body := mustJSON(t, user)
		req := httptest.NewRequest("POST", "/api/users", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		app.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("Expected status 400, got %d", rec.Code)
		}

		var resp map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("Unmarshal response failed: %v", err)
		}

		if _, ok := resp["errors"]; !ok {
			t.Error("Expected errors in response")
		}
	})

	t.Run("MissingRequiredField", func(t *testing.T) {
		user := map[string]any{
			"email": "john@example.com",
			"age":   25,
		}

		body := mustJSON(t, user)
		req := httptest.NewRequest("POST", "/api/users", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		app.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("Expected status 400, got %d", rec.Code)
		}
	})

	t.Run("InvalidAge", func(t *testing.T) {
		user := map[string]any{
			"name":  "John Doe",
			"email": "john@example.com",
			"age":   15, // Below minimum
		}

		body := mustJSON(t, user)
		req := httptest.NewRequest("POST", "/api/users", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		app.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("Expected status 400, got %d", rec.Code)
		}
	})
}

func TestLoginFormValidation(t *testing.T) {
	app := newFormApp()
	app.AutoMiddleware()

	loginSchema := validator.NewSchema()
	loginSchema.Field("username", validator.String().Required().MinLength(3))
	loginSchema.Field("password", validator.String().Required().MinLength(8))

	app.Post("/login", func(c *hyperchi.Context) error {
		r := c.Request()
		if err := r.ParseForm(); err != nil {
			return hyperchi.BadRequest("Invalid form data").Wrap(err)
		}

		formData := make(map[string]any)
		for key, values := range r.Form {
			if len(values) > 0 {
				formData[key] = values[0]
			}
		}

		result := loginSchema.Validate(formData)
		if !result.Valid {
			return c.Status(http.StatusBadRequest).JSON(hyperchi.H{"errors": result.Errors})
		}

		return c.JSON(hyperchi.H{"status": "logged in"})
	})

	t.Run("ValidLogin", func(t *testing.T) {
		form := url.Values{}
		form.Add("username", "johndoe")
		form.Add("password", "securepassword123")

		req := httptest.NewRequest("POST", "/login", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()

		app.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d", rec.Code)
		}
	})

	t.Run("ShortPassword", func(t *testing.T) {
		form := url.Values{}
		form.Add("username", "johndoe")
		form.Add("password", "short")

		req := httptest.NewRequest("POST", "/login", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()

		app.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("Expected status 400, got %d", rec.Code)
		}
	})
}

func TestCustomValidators(t *testing.T) {
	app := newFormApp()
	app.AutoMiddleware()

	productSchema := validator.NewSchema()
	productSchema.Field("name", validator.String().Required())
	productSchema.Field("price", validator.Number().Required().Min(0))
	productSchema.Field("sku", validator.String().Required().Pattern(`^[A-Z]{3}-\d{4}$`))

	app.Post("/api/products", func(c *hyperchi.Context) error {
		var product map[string]any
		if err := json.NewDecoder(c.Request().Body).Decode(&product); err != nil {
			return hyperchi.BadRequest("Invalid JSON").Wrap(err)
		}

		result := productSchema.Validate(product)
		if !result.Valid {
			return c.Status(http.StatusBadRequest).JSON(hyperchi.H{"errors": result.Errors})
		}

		return c.JSON(hyperchi.H{
			"status":  "created",
			"product": product,
		})
	})

	t.Run("ValidProduct", func(t *testing.T) {
		product := map[string]any{
			"name":  "Test Product",
			"price": 29.99,
			"sku":   "ABC-1234",
		}

		body := mustJSON(t, product)
		req := httptest.NewRequest("POST", "/api/products", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		app.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d", rec.Code)
		}
	})

	t.Run("InvalidSKU", func(t *testing.T) {
		product := map[string]any{
			"name":  "Test Product",
			"price": 29.99,
			"sku":   "invalid-sku",
		}

		body := mustJSON(t, product)
		req := httptest.NewRequest("POST", "/api/products", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		app.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("Expected status 400, got %d", rec.Code)
		}
	})
}

func TestValidationWithHTMX(t *testing.T) {
	app := newFormApp()
	app.AutoMiddleware()

	commentSchema := validator.NewSchema()
	commentSchema.Field("author", validator.String().Required().MinLength(2))
	commentSchema.Field("content", validator.String().Required().MinLength(10).MaxLength(500))

	app.Post("/comments", func(c *hyperchi.Context) error {
		author := c.FormValue("author")
		content := c.FormValue("content")
		formData := map[string]any{
			"author":  author,
			"content": content,
		}

		result := commentSchema.Validate(formData)

		if !c.IsHTMX() {
			if !result.Valid {
				return c.Status(http.StatusBadRequest).JSON(hyperchi.H{"errors": result.Errors})
			}
			return c.JSON(hyperchi.H{"status": "comment added"})
		}

		if !result.Valid {
			html := "<div class='errors'>"
			for _, err := range result.Errors {
				html += "<p class='error'>" + template.HTMLEscapeString(err.Field) + ": " + template.HTMLEscapeString(err.Message) + "</p>"
			}
			html += "</div>"
			return c.Status(http.StatusBadRequest).HTML(template.HTML(html))
		}

		return c.HTML(template.HTML(`<div class="comment">
				<strong>` + template.HTMLEscapeString(author) + `</strong>
				<p>` + template.HTMLEscapeString(content) + `</p>
			</div>`))
	})

	t.Run("HTMXValidComment", func(t *testing.T) {
		form := url.Values{}
		form.Add("author", "Jane Doe")
		form.Add("content", "This is a great article!")

		req := httptest.NewRequest("POST", "/comments", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("HX-Request", "true")
		rec := httptest.NewRecorder()

		app.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d", rec.Code)
		}

		if !strings.Contains(rec.Body.String(), "Jane Doe") {
			t.Error("Response should contain author name")
		}
	})

	t.Run("HTMXInvalidComment", func(t *testing.T) {
		form := url.Values{}
		form.Add("author", "J")      // Too short
		form.Add("content", "Short") // Too short

		req := httptest.NewRequest("POST", "/comments", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("HX-Request", "true")
		rec := httptest.NewRecorder()

		app.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("Expected status 400, got %d", rec.Code)
		}

		if !strings.Contains(rec.Body.String(), "class='errors'") {
			t.Error("Response should contain error HTML")
		}
	})
}

// mustJSON encodes v for a request body, failing the test on error.
func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	body, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	return body
}
