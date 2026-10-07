package validator

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUniqueArrayRejectsInvalidJSONItems(t *testing.T) {
	schema := NewSchema().Field("tags", Array(String()).Unique())
	for _, body := range []string{`{"tags":[{}]}`, `{"tags":[[]]}`} {
		t.Run(body, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/", strings.NewReader(body))
			if result := NewFormValidator(schema).ValidateJSON(r); result.Valid {
				t.Fatal("invalid element accepted")
			}
		})
	}
}

func TestUniqueArrayValues(t *testing.T) {
	for _, tc := range []struct {
		name  string
		v     *ArrayValidator
		items any
		valid bool
	}{
		{"strings", Array(String()).Unique(), []string{"a", "b"}, true},
		{"duplicate strings", Array(String()).Unique(), []string{"a", "a"}, false},
		{"nested arrays", Array(Array(String())).Unique(), []any{[]any{"a"}, []any{"b"}}, true},
		{"duplicate nested arrays", Array(Array(String())).Unique(), []any{[]any{"a"}, []any{"a"}}, false},
		{"maps", Array(nil).Unique(), []any{map[string]any{"x": 1}, map[string]any{"x": 2}}, true},
		{"duplicate maps", Array(nil).Unique(), []any{map[string]any{"x": 1}, map[string]any{"x": 1}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.v.Validate(tc.items)
			if (err == nil) != tc.valid {
				t.Fatalf("err=%v want valid=%t", err, tc.valid)
			}
		})
	}
}
