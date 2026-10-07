package validator

import "testing"

// A required string field rejects a blank or whitespace-only submission. HTML
// forms submit an empty input as "" instead of omitting it, so without this
// String().Required() would accept an empty form.
func TestRequiredRejectsEmptyString(t *testing.T) {
	schema := NewSchema().Field("name", String().Required())

	res := schema.Validate(map[string]any{"name": ""})
	if res.Valid {
		t.Fatal("required string field accepted a blank submission")
	}
}

func TestRequiredRejectsWhitespaceOnly(t *testing.T) {
	schema := NewSchema().Field("name", String().Required())

	res := schema.Validate(map[string]any{"name": "   "})
	if res.Valid {
		t.Fatal("required string field accepted a whitespace-only submission")
	}
}

func TestRequiredAcceptsNonEmptyString(t *testing.T) {
	schema := NewSchema().Field("name", String().Required())

	res := schema.Validate(map[string]any{"name": "Ada"})
	if !res.Valid {
		t.Fatalf("required field rejected a valid value: %+v", res.Errors)
	}
}

func TestOptionalEmptyStringStillValid(t *testing.T) {
	schema := NewSchema().Field("nickname", String())

	res := schema.Validate(map[string]any{"nickname": ""})
	if !res.Valid {
		t.Fatalf("optional empty string should be valid: %+v", res.Errors)
	}
}
