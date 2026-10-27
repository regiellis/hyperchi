# HyperChi Validator

A lightweight, type-safe validation system for HyperChi applications, inspired by Zod. This package provides chainable validation rules that integrate seamlessly with HTMX forms and server-side rendering.

## Features

- **Type-Safe Validation**: Strongly typed validation rules with method chaining
- **HTMX Integration**: Built-in support for HTMX form validation and error rendering
- **Template Helpers**: Ready-to-use template functions for rendering validation errors
- **HyperChi Middleware**: Seamless integration with HyperChi routing and rendering
- **Flexible Error Handling**: Support for multiple CSS frameworks and custom error rendering
- **Common Schemas**: Pre-built validation schemas for typical use cases

## Quick Start

### Basic Usage

```go
package main

import (
    "github.com/regiellis/hyperchi/hyperchi/validator"
    "github.com/regiellis/hyperchi/hyperchi"
)

func main() {
    // Create a validation schema
    schema := validator.NewSchema().
        Field("name", validator.String().MinLength(2).MaxLength(50).Required()).
        Field("email", validator.String().Email().Required()).
        Field("age", validator.Number().Min(18).Max(120).Integer())

    // Validate data
    data := map[string]interface{}{
        "name":  "John Doe",
        "email": "john@example.com",
        "age":   25,
    }

    result := schema.Validate(data)
    if result.Valid {
        fmt.Println("Validation passed!")
        fmt.Printf("Validated data: %+v\n", result.Data)
    } else {
        fmt.Println("Validation failed:")
        for _, err := range result.Errors {
            fmt.Printf("- %s: %s\n", err.Field, err.Message)
        }
    }
}
```

### HTMX Form Integration

For input with a fixed shape, `hyperchi.Bind[T](c)` with `validate` tags on a struct is the
shorter path. Use a schema from this package when the shape is only known at runtime: register
it on `app.Schemas` and check a request with `c.ValidateSchema`.

```go
package main

import (
    "log"

    "github.com/regiellis/hyperchi/hyperchi"
    "github.com/regiellis/hyperchi/hyperchi/validator"
)

func main() {
    app := hyperchi.New()
    app.Schemas.Register("contact", validator.NewSchema().
        Field("name", validator.String().MinLength(2).MaxLength(100).Required()).
        Field("email", validator.String().Email().Required()).
        Field("message", validator.String().MinLength(10).MaxLength(1000).Required()))

    app.Post("/contact", func(c *hyperchi.Context) error {
        data, err := c.ValidateSchema("contact") // 422 with per-field messages when invalid
        if err != nil {
            return err
        }
        return c.Fragment("contact-success", data)
    })
    log.Fatal(app.Serve(":8080"))
}
```

## Validation Types

### String Validation

```go
validator.String().
    MinLength(3).
    MaxLength(50).
    Pattern(`^[a-z-]+$`). // custom regular expression
    OneOf("active", "inactive", "pending").
    Trim().               // Transform: trim whitespace
    ToLower().            // Transform: convert to lowercase
    Required().
    Default("default value").
    Message("Custom error message")

validator.String().Email() // also Phone(), URL(), and UUID()
```

Lengths count characters (runes), not bytes: `MaxLength(4)` accepts `"café"`.

`Pattern`, `Regex`, `Email`, `Phone`, `URL`, and `UUID` each add a rule, and the string must
match every one, so ``Pattern(`^a`).Email()`` accepts only email addresses starting with `a`.

Without `Required`, an empty or whitespace-only string passes with no other check: an HTML form
submits an untouched optional input as `""`. `Schema.Validate` keeps that string in `Data`.

### Number Validation

```go
validator.Number().
    Min(0).
    Max(100).
    Range(18, 65).
    Integer().            // Must be an integer
    Positive().           // Must be positive
    Negative().           // Must be negative
    MultipleOf(5).        // Must be multiple of 5
    Required().
    Default(0).
    Message("Custom error message")
```

`NaN` and infinities are rejected: `ParseFloat` accepts the strings `"NaN"` and `"Inf"`, and NaN
would otherwise pass every bound. `MultipleOf` accepts decimal steps (`0.3` is a multiple of `0.1`)
and panics on a zero or non-finite step, which is a schema bug.

### Boolean Validation

```go
validator.Boolean().
    Required().
    Default(false).
    Message("Custom error message")
```

### Date Validation

```go
validator.Date().
    After(time.Now()).
    Before(time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC)).
    Format("2006-01-02").  // Expected date format
    Required().
    Default(time.Now()).
    Message("Custom error message")
```

Without `Format`, dates parse through `ParseTime`, which accepts only ISO 8601 forms: RFC 3339,
`2006-01-02T15:04:05`, `2006-01-02T15:04` (a `datetime-local` input), `2006-01-02 15:04:05`, and
`2006-01-02`. Slash dates are rejected because `05/03/2026` is March 5 in the US and May 3 almost
everywhere else. Accept one explicitly with `Format("01/02/2006")` (month first) or
`Format("02/01/2006")` (day first).

### Array Validation

```go
validator.Array(validator.String().MinLength(2)).
    MinItems(1).
    MaxItems(10).
    Length(5).            // Exact length
    Unique().             // All items must be unique
    Required().
    Default([]interface{}{}).
    Message("Custom error message")
```

## Common Schemas

The package includes pre-built schemas for common use cases:

```go
// Login form validation
result := validator.LoginSchema.Validate(data)

// Registration form validation
result := validator.RegisterSchema.Validate(data)

// Contact form validation
result := validator.ContactSchema.Validate(data)

// Profile update validation
result := validator.ProfileSchema.Validate(data)

// Custom user schema
userSchema := validator.CreateUserSchema()
result := userSchema.Validate(data)
```

## HTMX Integration

### Manual Form Validation

```go
func handleContactForm(c *hyperchi.Context) error {
    // Validate form data
    result := validator.ValidateFormData(c.Request(), validator.ContactSchema)
    if !result.Valid {
        // htmx 4 swaps a 422, so the errors land in the form's target
        return c.Status(http.StatusUnprocessableEntity).Fragment("contact-errors", hyperchi.H{
            "errors": result.Errors,
        })
    }

    // Process form with validated data
    if err := processContactForm(c.Ctx(), result.Data); err != nil {
        return err
    }
    return c.Fragment("contact-success", result.Data)
}
```

For struct-shaped input, `hyperchi.Bind[T](c)` with `validate` tags is the shorter path, and
`c.ValidateSchema(name)` runs a schema registered on `app.Schemas`. Both return a 422
`*hyperchi.HTTPError` with per-field messages that a handler can return as is.

### Template Error Rendering

```go
// Register the error-rendering helpers before loading templates
for name, fn := range validator.NewTemplateHelpers().GetTemplateFunctions() {
    app.RegisterFilter(name, fn)
}
```

## Template Usage

### Basic Error Display

```html
<!-- Display all validation errors -->
{{if hasValidationErrors .errors}}
<div class="alert alert-danger">
    <h5>Please fix the following errors:</h5>
    <ul>
        {{range .errors}}
        <li><strong>{{.Field}}:</strong> {{.Message}}</li>
        {{end}}
    </ul>
</div>
{{end}}

<!-- Display field-specific errors -->
<div class="form-group">
    <label for="email">Email</label>
    <input type="email" 
           class="form-control {{fieldErrorClass .errors "email"}}" 
           name="email" 
           value="{{.data.email}}">
    {{renderFieldErrors .errors "email"}}
</div>
```

### Using Template Helpers

```html
<!-- Check if field has errors -->
{{if hasFieldError .errors "email"}}
    <div class="error-message">{{(getFieldError .errors "email").Message}}</div>
{{end}}

<!-- Render all errors for a field -->
{{renderFieldErrors .errors "email"}}

<!-- Render all validation errors -->
{{renderValidationErrors .errors}}
```

## Advanced Usage

### Custom Validation Schema

```go
// Create a complex validation schema
orderSchema := validator.NewSchema().
    Field("customer_name", validator.String().MinLength(2).MaxLength(100).Required()).
    Field("email", validator.String().Email().Required()).
    Field("items", validator.Array(
        validator.NewSchema().
            Field("product_id", validator.String().UUID().Required()).
            Field("quantity", validator.Number().Min(1).Max(100).Integer().Required()).
            Field("price", validator.Number().Min(0).Required()),
    ).MinItems(1).MaxItems(50).Required()).
    Field("shipping_address", validator.NewSchema().
        Field("street", validator.String().MinLength(5).MaxLength(200).Required()).
        Field("city", validator.String().MinLength(2).MaxLength(100).Required()).
        Field("postal_code", validator.String().Pattern(`^\d{5}(-\d{4})?$`).Required()).
        Field("country", validator.String().OneOf("US", "CA", "UK", "DE").Required())).
    Field("total", validator.Number().Min(0).Required()).
    Field("terms_accepted", validator.Boolean().Required())
```

### Custom Error Rendering

```go
// Create custom error renderer
options := validator.ErrorRenderingOptions{
    ShowFieldNames:  true,
    CSSFramework:    "tailwind",
    ErrorClass:      "bg-red-100 border border-red-400 text-red-700 px-4 py-3 rounded",
    FieldErrorClass: "border-red-500 text-red-900",
}

renderer := validator.NewCustomErrorRenderer(options)
html := renderer.RenderErrors(result.Errors)
```

## Template Snippets

The package includes common template snippets for quick integration:

```go
// Available template snippets
snippets := validator.CommonTemplateSnippets

// Use in templates
{{template "validation-errors" .}}
{{template "field-errors" .}}
{{template "form-field-with-errors" .}}
{{template "htmx-form-errors" .}}
```

Each snippet parses with `html/template` and the functions from
`NewTemplateHelpers().GetTemplateFunctions()` alone. Execute them with a `map[string]any` holding the
keys they read, with `errors` as a `[]validator.ValidationError`.

`FormValidationMiddleware` validates every method except `GET`, `HEAD`, and `OPTIONS`, so CORS
preflights and `HEAD` probes reach the handler.

## Error Handling

### Validation Result Structure

```go
type ValidationResult struct {
    Valid  bool                    `json:"valid"`
    Errors []ValidationError       `json:"errors,omitempty"`
    Data   map[string]interface{}  `json:"data,omitempty"`
}

type ValidationError struct {
    Field   string      `json:"field"`
    Message string      `json:"message"`
    Code    string      `json:"code"`
    Value   interface{} `json:"value,omitempty"`
}
```

### Context Integration

```go
// Store validation result in request context
ctx := validator.SetValidationResult(r.Context(), result)
r = r.WithContext(ctx)

// Retrieve validation result from context
if result, ok := validator.GetValidationResult(r.Context()); ok {
    // Use validation result
}

// Check for errors in context
if validator.HasValidationErrors(r.Context()) {
    // Handle validation errors
}
```

## Best Practices

1. **Use Pre-built Schemas**: Start with common schemas and extend as needed
2. **Validate Early**: Use middleware to validate forms before processing
3. **Provide Clear Messages**: Use custom error messages for better user experience
4. **Handle Transformations**: Use transform functions to normalize data
5. **HTMX Integration**: Leverage HTMX-specific features for better UX
6. **Template Helpers**: Use template helpers for consistent error display
7. **Error Contexts**: Store validation results in request context for later use

## Integration with HyperChi

Use validation results in a HyperChi handler and answer with a fragment:

```go
func handleUserForm(c *hyperchi.Context) error {
    result := validator.ValidateFormData(c.Request(), validator.CreateUserSchema())
    if !result.Valid {
        // RenderValidationErrors returns escaped template.HTML
        errors := validator.NewTemplateHelpers().RenderValidationErrors(result.Errors)
        return c.Status(http.StatusUnprocessableEntity).HTML(errors)
    }
    return c.Fragment("user-card", hyperchi.H{
        "name":  result.Data["name"],
        "email": result.Data["email"],
    })
}
```

This validation system provides a robust, type-safe way to handle form validation in HyperChi applications while maintaining the framework's focus on server-side rendering and HTMX integration.