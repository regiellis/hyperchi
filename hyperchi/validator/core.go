// Package validator checks runtime-shaped input against a Schema of chainable
// field rules, in the style of Zod:
//
//	schema := validator.NewSchema().
//		Field("email", validator.String().Email().Required()).
//		Field("age", validator.Int().Min(13))
//	result := schema.Validate(data)
//
// In a HyperChi app, request input with a fixed shape goes through
// hyperchi.Bind and struct tags instead. A Schema suits input whose shape is
// known only at runtime: register it on app.Schemas and check it with
// c.ValidateSchema. The package also has form and JSON helpers, middleware
// that renders 422 errors, and template functions for showing errors.
package validator

import (
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ValidationError is one field's failure. Code is "required",
// "validation_failed", or "parse_error".
type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
	Code    string `json:"code"`
	// Value holds the submitted value for code that needs it. It is never
	// serialized: echoing raw input (a failed password, say) into a JSON
	// error response leaks it to logs and clients.
	Value any `json:"-"`
}

// Error returns "validation failed for field '<field>': <message>".
func (e ValidationError) Error() string {
	return fmt.Sprintf("validation failed for field '%s': %s", e.Field, e.Message)
}

// ValidationResult is the outcome of Schema.Validate. Data holds the
// transformed values of the fields that passed, plus defaults for absent
// optional fields.
type ValidationResult struct {
	Valid  bool              `json:"valid"`
	Errors []ValidationError `json:"errors,omitempty"`
	Data   map[string]any    `json:"data,omitempty"`
}

// HasErrors reports whether the result has any errors.
func (r ValidationResult) HasErrors() bool {
	return len(r.Errors) > 0
}

// GetError returns a copy of the first error for field, or nil.
func (r ValidationResult) GetError(field string) *ValidationError {
	for _, err := range r.Errors {
		if err.Field == field {
			return &err
		}
	}
	return nil
}

// GetErrors returns every error for field.
func (r ValidationResult) GetErrors(field string) []ValidationError {
	var errors []ValidationError
	for _, err := range r.Errors {
		if err.Field == field {
			errors = append(errors, err)
		}
	}
	return errors
}

// Schema maps field names to validators. Build it once, before use: Field is
// not safe to call while another goroutine validates.
type Schema struct {
	fields map[string]FieldValidator
}

// NewSchema returns an empty Schema.
func NewSchema() *Schema {
	return &Schema{
		fields: make(map[string]FieldValidator),
	}
}

// Field sets the validator for name, replacing any earlier one, and returns s
// for chaining.
func (s *Schema) Field(name string, validator FieldValidator) *Schema {
	s.fields[name] = validator
	return s
}

// Validate checks data against every field in the schema. A missing or nil
// field fails when required and otherwise takes its default, if any. A
// required string that is blank or whitespace fails as "required". Other
// values go to the field's Validate, and passing values are stored in
// Result.Data after Transform. Fields not in the schema are dropped. Errors
// come out in map order, which varies between calls.
func (s *Schema) Validate(data map[string]any) ValidationResult {
	result := ValidationResult{
		Valid: true,
		Data:  make(map[string]any),
	}

	for fieldName, validator := range s.fields {
		value, exists := data[fieldName]

		if !exists || value == nil {
			if validator.IsRequired() {
				result.Valid = false
				result.Errors = append(result.Errors, ValidationError{
					Field:   fieldName,
					Message: requiredMessage(validator),
					Code:    "required",
				})
				continue
			}
			if defaultValue := validator.GetDefault(); defaultValue != nil {
				result.Data[fieldName] = defaultValue
			}
			continue
		}

		// HTML forms submit an empty input as "" instead of omitting it, so a
		// required field must treat a blank or whitespace-only string as
		// missing. Only strings: false and 0 stay valid for required bool and
		// number fields.
		if validator.IsRequired() {
			if s, ok := value.(string); ok && strings.TrimSpace(s) == "" {
				result.Valid = false
				result.Errors = append(result.Errors, ValidationError{
					Field:   fieldName,
					Message: requiredMessage(validator),
					Code:    "required",
				})
				continue
			}
		}

		if err := validator.Validate(value); err != nil {
			result.Valid = false
			result.Errors = append(result.Errors, ValidationError{
				Field:   fieldName,
				Message: err.Error(),
				Code:    "validation_failed",
				Value:   value,
			})
		} else {
			transformedValue := validator.Transform(value)
			result.Data[fieldName] = transformedValue
		}
	}

	return result
}

// requiredMessage is the validator's custom Message when it has one (every
// validator embedding BaseValidator does), else "field is required".
func requiredMessage(v FieldValidator) string {
	if m, ok := v.(interface{ GetErrorMessage(string) string }); ok {
		return m.GetErrorMessage("field is required")
	}
	return "field is required"
}

// ValidateStruct validates the exported fields of a struct, or a pointer to
// one, keyed by their json tag name or else the Go field name. Fields tagged
// json:"-" are skipped.
func (s *Schema) ValidateStruct(data any) ValidationResult {
	dataMap := structToMap(data)
	return s.Validate(dataMap)
}

// FieldValidator is one field's rules. Validate checks a present value,
// Transform converts a value that passed, and IsRequired and GetDefault tell
// Schema.Validate how to treat a missing one.
type FieldValidator interface {
	Validate(value any) error
	Transform(value any) any
	IsRequired() bool
	GetDefault() any
}

// BaseValidator holds the required flag, default, and custom message shared
// by every validator type. Embed it to write a custom FieldValidator.
type BaseValidator struct {
	required     bool
	defaultValue any
	customError  string
}

// IsRequired reports whether Required was called.
func (b BaseValidator) IsRequired() bool {
	return b.required
}

// GetDefault returns the value set by Default, or nil.
func (b BaseValidator) GetDefault() any {
	return b.defaultValue
}

// Required marks the field as required.
func (b *BaseValidator) Required() *BaseValidator {
	b.required = true
	return b
}

// Default sets the value Schema.Validate uses when the field is absent.
func (b *BaseValidator) Default(value any) *BaseValidator {
	b.defaultValue = value
	return b
}

// Message replaces every failure message of this validator with msg.
func (b *BaseValidator) Message(msg string) *BaseValidator {
	b.customError = msg
	return b
}

// GetErrorMessage returns the message set by Message, or defaultMsg.
func (b BaseValidator) GetErrorMessage(defaultMsg string) string {
	if b.customError != "" {
		return b.customError
	}
	return defaultMsg
}

// NewValidationError returns an error with the custom message, or with
// defaultMsg when none is set.
func (b BaseValidator) NewValidationError(defaultMsg string) error {
	return fmt.Errorf("%s", b.GetErrorMessage(defaultMsg))
}

// Transform returns value unchanged.
func (b BaseValidator) Transform(value any) any {
	return value
}

// Validate accepts every value.
func (b BaseValidator) Validate(value any) error {
	return nil
}

// structToMap returns a struct's exported fields keyed by json tag name or Go
// field name, skipping json:"-". A non-struct gives an empty map.
func structToMap(data any) map[string]any {
	result := make(map[string]any)

	v := reflect.ValueOf(data)
	if v.Kind() == reflect.Pointer {
		v = v.Elem()
	}

	if v.Kind() != reflect.Struct {
		return result
	}

	t := v.Type()
	for i := 0; i < v.NumField(); i++ {
		field := t.Field(i)
		value := v.Field(i)

		if !value.CanInterface() {
			continue
		}

		name := field.Name
		if jsonTag := field.Tag.Get("json"); jsonTag != "" {
			if jsonTag == "-" {
				continue
			}
			if idx := strings.Index(jsonTag, ","); idx != -1 {
				name = jsonTag[:idx]
			} else {
				name = jsonTag
			}
		}

		result[name] = value.Interface()
	}

	return result
}

// Patterns used by the Email, Phone, URL, and UUID rules. They check shape
// only: EmailRegex does not parse addresses, PhoneRegex accepts any run of
// digits, spaces, dashes, and parentheses with an optional leading +,
// URLRegex requires an http or https scheme, and UUIDRegex requires
// lowercase hex.
var (
	EmailRegex = regexp.MustCompile(`^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$`)
	PhoneRegex = regexp.MustCompile(`^\+?[\d\s\-\(\)]+$`)
	URLRegex   = regexp.MustCompile(`^https?://[^\s]+$`)
	UUIDRegex  = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

// timeLayouts are the layouts ParseTime tries, in order. They are all ISO
// 8601 forms, which read the same way everywhere. Slash dates such as
// 05/03/2026 are deliberately absent: they mean March 5 in the US and May 3
// in most other places, and guessing silently stores the wrong date. Accept
// them with an explicit layout, Date().Format("01/02/2006") or
// Date().Format("02/01/2006").
var timeLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02T15:04:05",
	"2006-01-02T15:04", // <input type="datetime-local">
	"2006-01-02 15:04:05",
	"2006-01-02",
}

// ParseTime parses a time.Time, or a string in an ISO 8601 layout: RFC 3339
// (with optional fractional seconds), 2006-01-02T15:04:05, 2006-01-02T15:04
// (an <input type="datetime-local"> value), 2006-01-02 15:04:05, or
// 2006-01-02. Strings without a zone are read as UTC. Ambiguous slash dates
// are rejected; see Date().Format to accept one layout explicitly.
func ParseTime(value any) (time.Time, error) {
	switch v := value.(type) {
	case time.Time:
		return v, nil
	case string:
		for _, layout := range timeLayouts {
			if t, err := time.Parse(layout, v); err == nil {
				return t, nil
			}
		}
		return time.Time{}, fmt.Errorf("invalid time format %q: use an ISO 8601 date such as 2006-01-02", v)
	default:
		return time.Time{}, fmt.Errorf("invalid time type")
	}
}

// ParseNumber converts float64, float32, int, int32, int64, or a numeric
// string to float64. Other types, including uint and int8, return an error.
// A string may be "NaN" or "Inf", which NumberValidator rejects.
func ParseNumber(value any) (float64, error) {
	switch v := value.(type) {
	case float64:
		return v, nil
	case float32:
		return float64(v), nil
	case int:
		return float64(v), nil
	case int32:
		return float64(v), nil
	case int64:
		return float64(v), nil
	case string:
		return strconv.ParseFloat(v, 64)
	default:
		return 0, fmt.Errorf("invalid number type")
	}
}

// ParseInteger converts int, int32, int64, float64, float32 (truncated), or a
// decimal string to int. Other types return an error.
func ParseInteger(value any) (int, error) {
	switch v := value.(type) {
	case int:
		return v, nil
	case int32:
		return int(v), nil
	case int64:
		return int(v), nil
	case float64:
		return int(v), nil
	case float32:
		return int(v), nil
	case string:
		return strconv.Atoi(v)
	default:
		return 0, fmt.Errorf("invalid integer type")
	}
}

// ParseBoolean converts a bool, an int or float64 (non-zero is true), or a
// string. Ignoring case, the strings "true", "1", "yes", and "on" are true,
// and "false", "0", "no", "off", and "" are false. Anything else is an error.
func ParseBoolean(value any) (bool, error) {
	switch v := value.(type) {
	case bool:
		return v, nil
	case string:
		switch strings.ToLower(v) {
		case "true", "1", "yes", "on":
			return true, nil
		case "false", "0", "no", "off", "":
			return false, nil
		default:
			return false, fmt.Errorf("invalid boolean value")
		}
	case int:
		return v != 0, nil
	case float64:
		return v != 0, nil
	default:
		return false, fmt.Errorf("invalid boolean type")
	}
}
