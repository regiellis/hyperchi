package validator

import (
	"encoding/json"
	"fmt"
	stdhtml "html"
	"html/template"
	"log"
	"net/http"
	"strings"
)

// FormValidator validates request bodies against a Schema and renders the
// errors for htmx.
type FormValidator struct {
	schema *Schema
}

// NewFormValidator returns a FormValidator for schema.
func NewFormValidator(schema *Schema) *FormValidator {
	return &FormValidator{schema: schema}
}

// ValidateForm parses r's form (query and body) and validates it. A field
// with one value is a string, and one with several is a []string. A parse failure
// returns a single "parse_error" on the field "form".
func (fv *FormValidator) ValidateForm(r *http.Request) ValidationResult {
	if err := r.ParseForm(); err != nil {
		return ValidationResult{
			Valid: false,
			Errors: []ValidationError{
				{
					Field:   "form",
					Message: "failed to parse form data",
					Code:    "parse_error",
				},
			},
		}
	}

	data := make(map[string]any)
	for key, values := range r.Form {
		if len(values) == 1 {
			data[key] = values[0]
		} else if len(values) > 1 {
			data[key] = values
		}
	}

	return fv.schema.Validate(data)
}

// ValidateJSON decodes r's body as a JSON object and validates it. Numbers
// arrive as float64 and arrays as []any. A decode failure returns a single
// "parse_error" on the field "json".
func (fv *FormValidator) ValidateJSON(r *http.Request) ValidationResult {
	var data map[string]any
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		return ValidationResult{
			Valid: false,
			Errors: []ValidationError{
				{
					Field:   "json",
					Message: "failed to parse JSON data",
					Code:    "parse_error",
				},
			},
		}
	}

	return fv.schema.Validate(data)
}

// HTMXValidationResponse is what ValidateHTMXForm returns: the result with
// one message per field, and the HX-* headers it set.
type HTMXValidationResponse struct {
	Valid   bool              `json:"valid"`
	Errors  map[string]string `json:"errors,omitempty"`
	Data    map[string]any    `json:"data,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

// ValidateHTMXForm validates r's form and sets htmx headers on w: HX-Trigger
// "form:validated" on success, or HX-Retarget "#validation-errors" with
// HX-Reswap "innerHTML" on failure. When a field has several errors, Errors
// keeps the last. It writes no body and no status.
func (fv *FormValidator) ValidateHTMXForm(w http.ResponseWriter, r *http.Request) HTMXValidationResponse {
	result := fv.ValidateForm(r)

	response := HTMXValidationResponse{
		Valid:   result.Valid,
		Data:    result.Data,
		Headers: make(map[string]string),
	}

	if result.HasErrors() {
		response.Errors = make(map[string]string)
		for _, err := range result.Errors {
			response.Errors[err.Field] = err.Message
		}
	}

	if result.Valid {
		response.Headers["HX-Trigger"] = "form:validated"
		w.Header().Set("HX-Trigger", "form:validated")
	} else {
		response.Headers["HX-Retarget"] = "#validation-errors"
		response.Headers["HX-Reswap"] = "innerHTML"
		w.Header().Set("HX-Retarget", "#validation-errors")
		w.Header().Set("HX-Reswap", "innerHTML")
	}

	return response
}

// RenderValidationErrors renders every error as a div.validation-errors list.
// Field names and messages are HTML-escaped. It returns "" when there are no
// errors.
func (fv *FormValidator) RenderValidationErrors(result ValidationResult) template.HTML {
	if !result.HasErrors() {
		return ""
	}

	var html strings.Builder
	html.WriteString(`<div class="validation-errors">`)

	for _, err := range result.Errors {
		fmt.Fprintf(&html, `
			<div class="error-item" data-field="%s">
				<span class="error-field">%s:</span>
				<span class="error-message">%s</span>
			</div>
		`, stdhtml.EscapeString(err.Field), stdhtml.EscapeString(err.Field), stdhtml.EscapeString(err.Message))
	}

	html.WriteString(`</div>`)
	return template.HTML(html.String())
}

// RenderFieldErrors renders field's errors as a div.field-errors list, with
// messages HTML-escaped. It returns "" when the field has none.
func (fv *FormValidator) RenderFieldErrors(result ValidationResult, field string) template.HTML {
	errors := result.GetErrors(field)
	if len(errors) == 0 {
		return ""
	}

	var html strings.Builder
	html.WriteString(`<div class="field-errors">`)

	for _, err := range errors {
		fmt.Fprintf(&html, `
			<div class="field-error">%s</div>
		`, stdhtml.EscapeString(err.Message))
	}

	html.WriteString(`</div>`)
	return template.HTML(html.String())
}

// FormValidationMiddleware returns middleware that validates each request's
// form against schema and stores the result in the context (see
// GetValidationResult). A valid request continues. An invalid one gets a 422:
// rendered error HTML for an htmx request, the result as JSON otherwise.
//
// GET, HEAD, and OPTIONS requests pass through unvalidated: they carry no form
// body, and failing a CORS preflight or a HEAD probe with 422 breaks the route.
func FormValidationMiddleware(schema *Schema) func(http.Handler) http.Handler {
	validator := NewFormValidator(schema)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isBodylessMethod(r.Method) {
				next.ServeHTTP(w, r)
				return
			}

			result := validator.ValidateForm(r)

			ctx := r.Context()
			ctx = SetValidationResult(ctx, result)
			r = r.WithContext(ctx)

			if result.Valid {
				next.ServeHTTP(w, r)
				return
			}

			if IsHTMXRequest(r) {
				w.Header().Set("Content-Type", "text/html")
				w.WriteHeader(http.StatusUnprocessableEntity)

				html := validator.RenderValidationErrors(result)
				if _, err := w.Write([]byte(html)); err != nil {
					log.Printf("validator: write 422 response: %v", err)
				}
				return
			}

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnprocessableEntity)
			if err := json.NewEncoder(w).Encode(result); err != nil {
				log.Printf("validator: write 422 response: %v", err)
			}
		})
	}
}

// isBodylessMethod reports whether requests with method carry no form body to
// validate.
func isBodylessMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	return false
}

// IsHTMXRequest reports whether r carries "HX-Request: true". A nil r is not.
func IsHTMXRequest(r *http.Request) bool {
	if r == nil {
		return false
	}
	return r.Header.Get("HX-Request") == "true"
}

// Ready-made schemas for common forms. They are shared package values: use
// them as they are, since adding fields changes them for every caller.
var (
	// LoginSchema requires an email and a password of at least 6 characters.
	LoginSchema = NewSchema().
			Field("email", String().Email().Required()).
			Field("password", String().MinLength(6).Required())

	// RegisterSchema requires a name, an email, a password of at least 8
	// characters, and password_confirm. It does not check that the two
	// passwords match.
	RegisterSchema = NewSchema().
			Field("name", String().MinLength(2).MaxLength(50).Required()).
			Field("email", String().Email().Required()).
			Field("password", String().MinLength(8).Required()).
			Field("password_confirm", String().Required())

	// ContactSchema requires a name, an email, a subject, and a message.
	ContactSchema = NewSchema().
			Field("name", String().MinLength(2).MaxLength(100).Required()).
			Field("email", String().Email().Required()).
			Field("subject", String().MinLength(5).MaxLength(200).Required()).
			Field("message", String().MinLength(10).MaxLength(1000).Required())

	// ProfileSchema requires a name and an email. Bio, website, and phone are
	// optional: absent or empty, they pass without a format check.
	ProfileSchema = NewSchema().
			Field("name", String().MinLength(2).MaxLength(50).Required()).
			Field("email", String().Email().Required()).
			Field("bio", String().MaxLength(500)).
			Field("website", String().URL()).
			Field("phone", String().Phone())
)

// CreateUserSchema returns a new schema for a user: name, email, age (13 to
// 120), status (active, inactive, or pending), and terms.
func CreateUserSchema() *Schema {
	return NewSchema().
		Field("name", String().MinLength(2).MaxLength(50).Required()).
		Field("email", String().Email().Required()).
		Field("age", Number().Min(13).Max(120).Integer()).
		Field("status", String().OneOf("active", "inactive", "pending")).
		Field("terms", Boolean().Required())
}

// CreatePostSchema returns a new schema for a post: title, content, up to 5
// tags, published (default false), and publish_date.
func CreatePostSchema() *Schema {
	return NewSchema().
		Field("title", String().MinLength(3).MaxLength(200).Required()).
		Field("content", String().MinLength(10).Required()).
		Field("tags", Array(String().MinLength(2).MaxLength(20)).MaxItems(5)).
		Field("published", Boolean().Default(false)).
		Field("publish_date", Date())
}
