package validator_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/regiellis/hyperchi/hyperchi/validator"
)

func ExampleNewSchema() {
	schema := validator.NewSchema().
		Field("name", validator.String().MinLength(2).MaxLength(50).Required()).
		Field("email", validator.String().Email().Required()).
		Field("age", validator.Number().Min(18).Max(120).Integer())

	data := map[string]any{
		"name":  "John Doe",
		"email": "john@example.com",
		"age":   25,
	}

	result := schema.Validate(data)
	fmt.Printf("Valid: %t\n", result.Valid)
	fmt.Printf("Name: %s\n", result.Data["name"])
	fmt.Printf("Email: %s\n", result.Data["email"])
	fmt.Printf("Age: %v\n", result.Data["age"])
	// Output:
	// Valid: true
	// Name: John Doe
	// Email: john@example.com
	// Age: 25
}

func ExampleString() {
	validator := validator.String().
		MinLength(3).
		MaxLength(20).
		Trim().
		ToLower().
		Required()

	// Valid input with transformation
	result := validator.Validate("  HELLO WORLD  ")
	fmt.Printf("Valid: %t\n", result == nil)
	transformed := validator.Transform("  HELLO WORLD  ")
	fmt.Printf("Transformed: %s\n", transformed)

	// Invalid input
	result = validator.Validate("hi")
	fmt.Printf("Error: %s\n", result.Error())

	// Output:
	// Valid: true
	// Transformed: hello world
	// Error: string must be at least 3 characters
}

func ExampleNumber() {
	validator := validator.Number().
		Min(0).
		Max(100).
		Integer().
		Required()

	// Valid input
	result := validator.Validate(42)
	fmt.Printf("Valid: %t\n", result == nil)

	// Invalid input (not integer)
	result = validator.Validate(42.5)
	fmt.Printf("Error: %s\n", result.Error())

	// Invalid input (out of range)
	result = validator.Validate(150)
	fmt.Printf("Error: %s\n", result.Error())

	// Output:
	// Valid: true
	// Error: value must be an integer
	// Error: number must be at most 100
}

func ExampleStringValidator_Email() {
	validator := validator.String().Email().Required()

	// Valid email
	result := validator.Validate("user@example.com")
	fmt.Printf("Valid email: %t\n", result == nil)

	// Invalid email
	result = validator.Validate("invalid-email")
	fmt.Printf("Invalid email: %t\n", result != nil)

	// Output:
	// Valid email: true
	// Invalid email: true
}

func ExampleArray() {
	validator := validator.Array(validator.String().MinLength(2)).
		MinItems(1).
		MaxItems(5).
		Unique()

	// Valid array
	result := validator.Validate([]any{"apple", "banana", "cherry"})
	fmt.Printf("Valid: %t\n", result == nil)

	// Invalid array (duplicate items)
	result = validator.Validate([]any{"apple", "apple"})
	fmt.Printf("Error: %s\n", result.Error())

	// Output:
	// Valid: true
	// Error: array items must be unique
}

func ExampleDate() {
	validator := validator.Date().
		After(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)).
		Before(time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC))

	// Valid date
	result := validator.Validate(time.Date(2025, 6, 15, 0, 0, 0, 0, time.UTC))
	fmt.Printf("Valid: %t\n", result == nil)

	// Invalid date (too early)
	result = validator.Validate(time.Date(2019, 6, 15, 0, 0, 0, 0, time.UTC))
	fmt.Printf("Error: %s\n", result.Error())

	// Output:
	// Valid: true
	// Error: date must be after 2020-01-01
}

func ExampleFormValidator_ValidateHTMXForm() {
	schema := validator.NewSchema().
		Field("name", validator.String().MinLength(2).Required()).
		Field("email", validator.String().Email().Required())

	formValidator := validator.NewFormValidator(schema)

	form := url.Values{}
	form.Add("name", "John Doe")
	form.Add("email", "john@example.com")

	req := httptest.NewRequest("POST", "/submit", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")

	w := httptest.NewRecorder()
	response := formValidator.ValidateHTMXForm(w, req)

	fmt.Printf("Valid: %t\n", response.Valid)
	fmt.Printf("Headers: %v\n", response.Headers)
	fmt.Printf("Data: %v\n", response.Data)

	// Output:
	// Valid: true
	// Headers: map[HX-Trigger:form:validated]
	// Data: map[email:john@example.com name:John Doe]
}

func ExampleLoginSchema() {
	data := map[string]any{
		"email":    "user@example.com",
		"password": "securepassword123",
	}

	result := validator.LoginSchema.Validate(data)
	fmt.Printf("Valid: %t\n", result.Valid)
	fmt.Printf("Email: %s\n", result.Data["email"])

	// Output:
	// Valid: true
	// Email: user@example.com
}

func ExampleStringValidator_Message() {
	validator := validator.String().
		MinLength(8).
		Message("Password must be at least 8 characters long")

	result := validator.Validate("short")
	fmt.Printf("Error: %s\n", result.Error())

	// Output:
	// Error: Password must be at least 8 characters long
}

func ExampleStringValidator_OneOf() {
	validator := validator.String().OneOf("active", "inactive", "pending")

	// Valid value
	result := validator.Validate("active")
	fmt.Printf("Valid: %t\n", result == nil)

	// Invalid value
	result = validator.Validate("unknown")
	fmt.Printf("Error: %s\n", result.Error())

	// Output:
	// Valid: true
	// Error: value must be one of: active, inactive, pending
}

func ExampleSchema_complex() {
	userSchema := validator.NewSchema().
		Field("name", validator.String().MinLength(2).MaxLength(50).Required()).
		Field("email", validator.String().Email().Required()).
		Field("age", validator.Number().Min(18).Max(120).Integer()).
		Field("tags", validator.Array(validator.String().MinLength(2)).MaxItems(5)).
		Field("active", validator.Boolean().Default(true))

	data := map[string]any{
		"name":  "Jane Smith",
		"email": "jane@example.com",
		"age":   28,
		"tags":  []any{"developer", "golang", "htmx"},
	}

	result := userSchema.Validate(data)
	fmt.Printf("Valid: %t\n", result.Valid)
	fmt.Printf("Name: %s\n", result.Data["name"])
	fmt.Printf("Active: %t\n", result.Data["active"]) // Default value applied
	fmt.Printf("Tags: %v\n", result.Data["tags"])

	// Output:
	// Valid: true
	// Name: Jane Smith
	// Active: true
	// Tags: [developer golang htmx]
}

func ExampleTemplateHelpers() {
	helpers := validator.NewTemplateHelpers()

	errors := []validator.ValidationError{
		{Field: "name", Message: "Name is required"},
		{Field: "email", Message: "Email format is invalid"},
	}

	hasError := helpers.HasFieldError(errors, "name")
	fmt.Printf("Name has error: %t\n", hasError)

	fieldError := helpers.GetFieldError(errors, "email")
	fmt.Printf("Email error: %s\n", fieldError.Message)

	// Output:
	// Name has error: true
	// Email error: Email format is invalid
}

func BenchmarkSchemaValidation(b *testing.B) {
	schema := validator.NewSchema().
		Field("name", validator.String().MinLength(2).MaxLength(50).Required()).
		Field("email", validator.String().Email().Required()).
		Field("age", validator.Number().Min(18).Max(120).Integer())

	data := map[string]any{
		"name":  "John Doe",
		"email": "john@example.com",
		"age":   25,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result := schema.Validate(data)
		if !result.Valid {
			b.Fatal("Expected valid result")
		}
	}
}

func BenchmarkStringValidation(b *testing.B) {
	validator := validator.String().
		MinLength(3).
		MaxLength(50).
		Email().
		Required()

	email := "test@example.com"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result := validator.Validate(email)
		if result != nil {
			b.Fatal("Expected valid result")
		}
	}
}

func TestValidationHelpers(t *testing.T) {
	if num, err := validator.ParseNumber("123.45"); err != nil || num != 123.45 {
		t.Errorf("ParseNumber failed: %v, %f", err, num)
	}

	if val, err := validator.ParseBoolean("true"); err != nil || !val {
		t.Errorf("ParseBoolean failed: %v, %t", err, val)
	}

	if _, err := validator.ParseTime("2023-01-01"); err != nil {
		t.Errorf("ParseTime failed: %v", err)
	}
}

func TestValidationErrors(t *testing.T) {
	schema := validator.NewSchema().
		Field("name", validator.String().MinLength(10).Required()).
		Field("email", validator.String().Email().Required())

	data := map[string]any{
		"name":  "short",
		"email": "invalid",
	}

	result := schema.Validate(data)
	if result.Valid {
		t.Error("Expected validation to fail")
	}

	if len(result.Errors) != 2 {
		t.Errorf("Expected 2 errors, got %d", len(result.Errors))
	}

	nameError := result.GetError("name")
	if nameError == nil {
		t.Error("Expected name error")
	}

	emailErrors := result.GetErrors("email")
	if len(emailErrors) != 1 {
		t.Errorf("Expected 1 email error, got %d", len(emailErrors))
	}
}

func TestFormValidationMiddleware(t *testing.T) {
	schema := validator.NewSchema().
		Field("name", validator.String().MinLength(2).Required())

	middleware := validator.FormValidationMiddleware(schema)

	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("success")); err != nil {
			t.Errorf("failed to write response: %v", err)
		}
	}))

	form := url.Values{}
	form.Add("name", "John Doe")
	req := httptest.NewRequest("POST", "/test", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	invalidForm := url.Values{}
	invalidForm.Add("name", "X") // Too short
	req = httptest.NewRequest("POST", "/test", strings.NewReader(invalidForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("Expected status 422, got %d", w.Code)
	}
}
