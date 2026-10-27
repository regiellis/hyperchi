package validator

import "net/http"

// ValidateFormData validates a submitted form against schema. In a HyperChi
// handler prefer hyperchi.Bind with struct tags, or c.ValidateSchema with a
// schema registered on app.Schemas.
func ValidateFormData(r *http.Request, schema *Schema) ValidationResult {
	return NewFormValidator(schema).ValidateForm(r)
}

// ValidateJSONData validates a JSON body against schema.
func ValidateJSONData(r *http.Request, schema *Schema) ValidationResult {
	return NewFormValidator(schema).ValidateJSON(r)
}
