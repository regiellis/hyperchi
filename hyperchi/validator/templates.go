package validator

import (
	"fmt"
	stdhtml "html"
	"html/template"
	"strings"
)

// TemplateHelpers holds the template functions for showing a
// []ValidationError. It has no state. The Render methods emit Bootstrap
// classes.
type TemplateHelpers struct{}

// NewTemplateHelpers returns a TemplateHelpers.
func NewTemplateHelpers() *TemplateHelpers {
	return &TemplateHelpers{}
}

// GetTemplateFunctions returns the helpers as a FuncMap, under lower-camel
// names such as hasFieldError and renderFieldErrors.
func (th *TemplateHelpers) GetTemplateFunctions() template.FuncMap {
	return template.FuncMap{
		"hasValidationErrors":    th.HasValidationErrors,
		"getValidationErrors":    th.GetValidationErrors,
		"getFieldError":          th.GetFieldError,
		"getFieldErrors":         th.GetFieldErrors,
		"renderValidationErrors": th.RenderValidationErrors,
		"renderFieldErrors":      th.RenderFieldErrors,
		"hasFieldError":          th.HasFieldError,
		"validationErrorClass":   th.ValidationErrorClass,
		"fieldErrorClass":        th.FieldErrorClass,
	}
}

// HasValidationErrors reports whether errors is non-empty.
func (th *TemplateHelpers) HasValidationErrors(errors []ValidationError) bool {
	return len(errors) > 0
}

// GetValidationErrors returns errors unchanged.
func (th *TemplateHelpers) GetValidationErrors(errors []ValidationError) []ValidationError {
	return errors
}

// GetFieldError returns a copy of the first error for field, or nil.
func (th *TemplateHelpers) GetFieldError(errors []ValidationError, field string) *ValidationError {
	for _, err := range errors {
		if err.Field == field {
			return &err
		}
	}
	return nil
}

// GetFieldErrors returns every error for field.
func (th *TemplateHelpers) GetFieldErrors(errors []ValidationError, field string) []ValidationError {
	var fieldErrors []ValidationError
	for _, err := range errors {
		if err.Field == field {
			fieldErrors = append(fieldErrors, err)
		}
	}
	return fieldErrors
}

// HasFieldError reports whether field has any error.
func (th *TemplateHelpers) HasFieldError(errors []ValidationError, field string) bool {
	for _, err := range errors {
		if err.Field == field {
			return true
		}
	}
	return false
}

// RenderValidationErrors renders every error as a Bootstrap alert list, with
// field names and messages HTML-escaped. It returns "" for no errors.
func (th *TemplateHelpers) RenderValidationErrors(errors []ValidationError) template.HTML {
	if len(errors) == 0 {
		return ""
	}

	var html strings.Builder
	html.WriteString(`<div class="validation-errors alert alert-danger">`)
	html.WriteString(`<h5 class="alert-heading">Please fix the following errors:</h5>`)
	html.WriteString(`<ul class="mb-0">`)

	for _, err := range errors {
		fmt.Fprintf(&html, `<li><strong>%s:</strong> %s</li>`, stdhtml.EscapeString(err.Field), stdhtml.EscapeString(err.Message))
	}

	html.WriteString(`</ul></div>`)
	return template.HTML(html.String())
}

// RenderFieldErrors renders field's messages, HTML-escaped and separated by
// <br>, in a div.invalid-feedback. It returns "" when the field has none.
func (th *TemplateHelpers) RenderFieldErrors(errors []ValidationError, field string) template.HTML {
	fieldErrors := th.GetFieldErrors(errors, field)
	if len(fieldErrors) == 0 {
		return ""
	}

	var html strings.Builder
	html.WriteString(`<div class="field-errors invalid-feedback">`)

	for i, err := range fieldErrors {
		if i > 0 {
			html.WriteString(`<br>`)
		}
		html.WriteString(stdhtml.EscapeString(err.Message))
	}

	html.WriteString(`</div>`)
	return template.HTML(html.String())
}

// ValidationErrorClass returns "has-validation-errors" when errors is
// non-empty, else "".
func (th *TemplateHelpers) ValidationErrorClass(errors []ValidationError) string {
	if len(errors) > 0 {
		return "has-validation-errors"
	}
	return ""
}

// FieldErrorClass returns "is-invalid" when field has an error, else "".
func (th *TemplateHelpers) FieldErrorClass(errors []ValidationError, field string) string {
	if th.HasFieldError(errors, field) {
		return "is-invalid"
	}
	return ""
}

// CommonTemplateSnippets holds Bootstrap form and error snippets by name. Each
// parses with html/template and the functions from GetTemplateFunctions alone,
// and executes against a map[string]any with the keys it reads (errors is a
// []ValidationError).
var CommonTemplateSnippets = map[string]string{
	"validation-errors": `
{{- if hasValidationErrors .errors -}}
<div class="validation-errors alert alert-danger" role="alert">
	<h5 class="alert-heading">Please fix the following errors:</h5>
	<ul class="mb-0">
		{{- range .errors -}}
		<li><strong>{{.Field}}:</strong> {{.Message}}</li>
		{{- end -}}
	</ul>
</div>
{{- end -}}
`,

	"field-errors": `
{{- $field := .field -}}
{{- $errors := .errors -}}
{{- if hasFieldError $errors $field -}}
<div class="field-errors invalid-feedback">
	{{- range $i, $err := getFieldErrors $errors $field -}}
	{{if $i}}<br>{{end}}{{$err.Message}}
	{{- end -}}
</div>
{{- end -}}
`,

	"form-field-with-errors": `
<div class="form-group">
	<label for="{{.name}}" class="form-label">{{.label}}</label>
	<input type="{{.type}}" 
		   class="form-control {{fieldErrorClass .errors .name}}" 
		   id="{{.name}}" 
		   name="{{.name}}" 
		   value="{{.value}}"
		   {{if .required}}required{{end}}
		   {{if .placeholder}}placeholder="{{.placeholder}}"{{end}}>
	{{renderFieldErrors .errors .name}}
</div>
`,

	"textarea-field-with-errors": `
<div class="form-group">
	<label for="{{.name}}" class="form-label">{{.label}}</label>
	<textarea class="form-control {{fieldErrorClass .errors .name}}" 
			  id="{{.name}}" 
			  name="{{.name}}" 
			  rows="{{with .rows}}{{.}}{{else}}3{{end}}"
			  {{if .required}}required{{end}}
			  {{if .placeholder}}placeholder="{{.placeholder}}"{{end}}>{{.value}}</textarea>
	{{renderFieldErrors .errors .name}}
</div>
`,

	"select-field-with-errors": `
<div class="form-group">
	<label for="{{.name}}" class="form-label">{{.label}}</label>
	<select class="form-control {{fieldErrorClass .errors .name}}" 
			id="{{.name}}" 
			name="{{.name}}"
			{{if .required}}required{{end}}>
		{{if .placeholder}}<option value="">{{.placeholder}}</option>{{end}}
		{{range .options}}
		<option value="{{.value}}" {{if eq .value $.value}}selected{{end}}>{{.label}}</option>
		{{end}}
	</select>
	{{renderFieldErrors .errors .name}}
</div>
`,

	"checkbox-field-with-errors": `
<div class="form-group">
	<div class="form-check">
		<input type="checkbox" 
			   class="form-check-input {{fieldErrorClass .errors .name}}" 
			   id="{{.name}}" 
			   name="{{.name}}" 
			   value="{{with .value}}{{.}}{{else}}1{{end}}"
			   {{if .checked}}checked{{end}}
			   {{if .required}}required{{end}}>
		<label class="form-check-label" for="{{.name}}">{{.label}}</label>
	</div>
	{{renderFieldErrors .errors .name}}
</div>
`,

	"htmx-form-errors": `
<div id="validation-errors" class="validation-errors">
	{{- if hasValidationErrors .errors -}}
	<div class="alert alert-danger" role="alert">
		<h5 class="alert-heading">Please fix the following errors:</h5>
		<ul class="mb-0">
			{{- range .errors -}}
			<li><strong>{{.Field}}:</strong> {{.Message}}</li>
			{{- end -}}
		</ul>
	</div>
	{{- end -}}
</div>
`,

	"htmx-field-errors": `
<div class="field-errors" id="{{.field}}-errors">
	{{renderFieldErrors .errors .field}}
</div>
`,
}

// ErrorRenderingOptions configures a CustomErrorRenderer. ErrorClass and
// FieldErrorClass are written into class attributes unescaped, so they must
// come from code, never from user input.
type ErrorRenderingOptions struct {
	ShowFieldNames  bool
	CSSFramework    string // "bootstrap", "tailwind", or anything else for generic markup
	ErrorClass      string
	FieldErrorClass string
}

// DefaultErrorRenderingOptions returns Bootstrap markup with field names shown.
func DefaultErrorRenderingOptions() ErrorRenderingOptions {
	return ErrorRenderingOptions{
		ShowFieldNames:  true,
		CSSFramework:    "bootstrap",
		ErrorClass:      "alert alert-danger",
		FieldErrorClass: "is-invalid",
	}
}

// CustomErrorRenderer renders validation errors as Bootstrap, Tailwind, or
// generic markup. Field names and messages are HTML-escaped.
type CustomErrorRenderer struct {
	options ErrorRenderingOptions
}

// NewCustomErrorRenderer returns a renderer using options.
func NewCustomErrorRenderer(options ErrorRenderingOptions) *CustomErrorRenderer {
	return &CustomErrorRenderer{options: options}
}

// RenderErrors renders every error as a list in the configured markup. It
// returns "" for no errors.
func (cer *CustomErrorRenderer) RenderErrors(errors []ValidationError) template.HTML {
	if len(errors) == 0 {
		return ""
	}

	var html strings.Builder

	switch cer.options.CSSFramework {
	case "bootstrap":
		html.WriteString(`<div class="` + cer.options.ErrorClass + `">`)
		html.WriteString(`<h5 class="alert-heading">Please fix the following errors:</h5>`)
		html.WriteString(`<ul class="mb-0">`)

		for _, err := range errors {
			if cer.options.ShowFieldNames {
				fmt.Fprintf(&html, `<li><strong>%s:</strong> %s</li>`, stdhtml.EscapeString(err.Field), stdhtml.EscapeString(err.Message))
			} else {
				fmt.Fprintf(&html, `<li>%s</li>`, stdhtml.EscapeString(err.Message))
			}
		}

		html.WriteString(`</ul></div>`)

	case "tailwind":
		html.WriteString(`<div class="bg-red-100 border border-red-400 text-red-700 px-4 py-3 rounded">`)
		html.WriteString(`<p class="font-bold">Please fix the following errors:</p>`)
		html.WriteString(`<ul class="mt-2 list-disc list-inside">`)

		for _, err := range errors {
			if cer.options.ShowFieldNames {
				fmt.Fprintf(&html, `<li><span class="font-medium">%s:</span> %s</li>`, stdhtml.EscapeString(err.Field), stdhtml.EscapeString(err.Message))
			} else {
				fmt.Fprintf(&html, `<li>%s</li>`, stdhtml.EscapeString(err.Message))
			}
		}

		html.WriteString(`</ul></div>`)

	default:
		html.WriteString(`<div class="` + cer.options.ErrorClass + `">`)
		html.WriteString(`<strong>Please fix the following errors:</strong>`)
		html.WriteString(`<ul>`)

		for _, err := range errors {
			if cer.options.ShowFieldNames {
				fmt.Fprintf(&html, `<li><strong>%s:</strong> %s</li>`, stdhtml.EscapeString(err.Field), stdhtml.EscapeString(err.Message))
			} else {
				fmt.Fprintf(&html, `<li>%s</li>`, stdhtml.EscapeString(err.Message))
			}
		}

		html.WriteString(`</ul></div>`)
	}

	return template.HTML(html.String())
}

// RenderFieldError renders field's messages in the configured markup. It
// returns "" when the field has none.
func (cer *CustomErrorRenderer) RenderFieldError(errors []ValidationError, field string) template.HTML {
	fieldErrors := make([]ValidationError, 0)
	for _, err := range errors {
		if err.Field == field {
			fieldErrors = append(fieldErrors, err)
		}
	}

	if len(fieldErrors) == 0 {
		return ""
	}

	var html strings.Builder

	switch cer.options.CSSFramework {
	case "bootstrap":
		html.WriteString(`<div class="invalid-feedback">`)
		for i, err := range fieldErrors {
			if i > 0 {
				html.WriteString(`<br>`)
			}
			html.WriteString(stdhtml.EscapeString(err.Message))
		}
		html.WriteString(`</div>`)

	case "tailwind":
		html.WriteString(`<p class="mt-2 text-sm text-red-600">`)
		for i, err := range fieldErrors {
			if i > 0 {
				html.WriteString(` `)
			}
			html.WriteString(stdhtml.EscapeString(err.Message))
		}
		html.WriteString(`</p>`)

	default:
		html.WriteString(`<div class="` + cer.options.FieldErrorClass + `">`)
		for i, err := range fieldErrors {
			if i > 0 {
				html.WriteString(`<br>`)
			}
			html.WriteString(stdhtml.EscapeString(err.Message))
		}
		html.WriteString(`</div>`)
	}

	return template.HTML(html.String())
}
