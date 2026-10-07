package validator

import (
	"context"
)

// contextKey is private so this package's context keys cannot collide with
// others.
type contextKey string

const (
	validationResultKey contextKey = "validation_result"
)

// SetValidationResult returns ctx carrying result. FormValidationMiddleware
// calls it for every request it validates.
func SetValidationResult(ctx context.Context, result ValidationResult) context.Context {
	return context.WithValue(ctx, validationResultKey, result)
}

// GetValidationResult returns the result stored by SetValidationResult, with
// false when there is none.
func GetValidationResult(ctx context.Context) (ValidationResult, bool) {
	result, ok := ctx.Value(validationResultKey).(ValidationResult)
	return result, ok
}

// HasValidationErrors reports whether ctx carries a result with errors.
func HasValidationErrors(ctx context.Context) bool {
	if result, ok := GetValidationResult(ctx); ok {
		return result.HasErrors()
	}
	return false
}

// GetValidationErrors returns the errors of the stored result, or nil.
func GetValidationErrors(ctx context.Context) []ValidationError {
	if result, ok := GetValidationResult(ctx); ok {
		return result.Errors
	}
	return nil
}

// GetValidatedData returns the transformed data of the stored result, or nil.
func GetValidatedData(ctx context.Context) map[string]any {
	if result, ok := GetValidationResult(ctx); ok {
		return result.Data
	}
	return nil
}
