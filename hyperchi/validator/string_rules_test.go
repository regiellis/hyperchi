package validator

import (
	"regexp"
	"testing"
)

// HTML forms submit an untouched input as "". An optional field must accept
// that instead of running its format check on it.
func TestProfileSchemaAcceptsEmptyOptionalFields(t *testing.T) {
	res := ProfileSchema.Validate(map[string]any{
		"name":    "Ada",
		"email":   "ada@example.com",
		"bio":     "",
		"website": "",
		"phone":   "",
	})
	if !res.Valid {
		t.Fatalf("empty optional fields rejected: %+v", res.Errors)
	}
	if res.Data["website"] != "" || res.Data["phone"] != "" {
		t.Fatalf("Data = %v, want the empty strings kept", res.Data)
	}

	res = ProfileSchema.Validate(map[string]any{
		"name": "Ada", "email": "ada@example.com", "website": "not a url",
	})
	if res.Valid {
		t.Fatal("a non-empty invalid website passed")
	}
}

func TestOptionalStringSkipsRulesOnlyWhenEmpty(t *testing.T) {
	v := String().MinLength(3).Email()
	if err := v.Validate(""); err != nil {
		t.Fatalf("optional empty string: %v", err)
	}
	if err := v.Validate("  "); err != nil {
		t.Fatalf("optional blank string: %v", err)
	}
	if err := v.Validate("x"); err == nil {
		t.Fatal("optional non-empty string skipped its rules")
	}
	if err := String().MinLength(3).Required().Validate(""); err == nil {
		t.Fatal("a required empty string passed MinLength")
	}
}

// Pattern rules used to share one slot, so the last call silently replaced
// the earlier ones. Every pattern rule now has to match.
func TestPatternRulesCompose(t *testing.T) {
	v := String().Pattern(`^a`).Email()
	if err := v.Validate("bob@example.com"); err == nil {
		t.Fatal("Email replaced the earlier Pattern rule")
	}
	if err := v.Validate("ann@example.com"); err != nil {
		t.Fatalf("a value matching both rules failed: %v", err)
	}

	v = String().Email().Regex(regexp.MustCompile(`\.org$`))
	if err := v.Validate("ann@example.com"); err == nil {
		t.Fatal("Regex replaced the earlier Email rule")
	}
	if err := v.Validate("not-an-email.org"); err == nil {
		t.Fatal("Regex replaced the earlier Email rule")
	}
	if err := v.Validate("ann@example.org"); err != nil {
		t.Fatalf("a value matching both rules failed: %v", err)
	}
}
