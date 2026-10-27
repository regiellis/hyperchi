package filters

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"strings"
)

func registerUtilityFilters() {
	Register("default", defaultValue)
	Register("fallback", fallback)
	Register("ternary", ternary)
	Register("coalesce", coalesce)
	Register("empty", empty)
	Register("notEmpty", notEmpty)
	Register("isNil", isNil)
	Register("notNil", notNil)
	Register("typeOf", typeOf)
	Register("kindOf", kindOf)
	Register("eq", eq)
	Register("ne", ne)
	Register("lt", lt)
	Register("le", le)
	Register("gt", gt)
	Register("ge", ge)
	Register("and", and)
	Register("or", or)
	Register("not", not)
	Register("base64Encode", base64Encode)
	Register("base64Decode", base64Decode)
	Register("urlEncode", urlEncode)
	Register("urlDecode", urlDecode)
	Register("pathJoin", pathJoin)
	Register("pathBase", pathBase)
	Register("pathDir", pathDir)
	Register("pathExt", pathExt)
	Register("regexMatch", regexMatch)
	Register("regexReplace", regexReplace)
	Register("regexSplit", regexSplit)
	Register("uuid", generateUUID)
	Register("env", getEnv)
	Register("dict", dict)
	Register("list", list)
	Register("printf", fmt.Sprintf)
	Register("print", fmt.Sprint)
	Register("println", fmt.Sprintln)
}

func defaultValue(value, defaultVal any) any {
	if empty(value) {
		return defaultVal
	}
	return value
}

func fallback(values ...any) any {
	for _, v := range values {
		if !empty(v) {
			return v
		}
	}
	return nil
}

func ternary(condition bool, trueVal, falseVal any) any {
	if condition {
		return trueVal
	}
	return falseVal
}

func coalesce(values ...any) any {
	for _, v := range values {
		if v != nil {
			return v
		}
	}
	return nil
}

func empty(v any) bool {
	if v == nil {
		return true
	}

	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.String:
		return rv.String() == ""
	case reflect.Array, reflect.Slice, reflect.Map:
		return rv.Len() == 0
	case reflect.Bool:
		return !rv.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return rv.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return rv.Uint() == 0
	case reflect.Float32, reflect.Float64:
		return rv.Float() == 0
	case reflect.Interface, reflect.Pointer:
		return rv.IsNil()
	}

	return false
}

func notEmpty(v any) bool {
	return !empty(v)
}

func isNil(v any) bool {
	return v == nil
}

func notNil(v any) bool {
	return v != nil
}

func typeOf(v any) string {
	if v == nil {
		return "nil"
	}
	return fmt.Sprintf("%T", v)
}

func kindOf(v any) string {
	if v == nil {
		return "invalid"
	}
	return reflect.ValueOf(v).Kind().String()
}

// eq reports whether a equals any of bs. It replaces the template builtin, so
// it compares basic kinds by value as the builtin does (a named string type
// equals its literal; integer types compare across sizes and signedness) and,
// unlike the builtin, also compares integers with floats. Other values fall back
// to reflect.DeepEqual.
func eq(a any, bs ...any) bool {
	for _, b := range bs {
		if equalValues(a, b) {
			return true
		}
	}
	return false
}

// ne reports whether a and b differ, by the rules of eq.
func ne(a, b any) bool {
	return !equalValues(a, b)
}

func equalValues(a, b any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	av, bv := reflect.ValueOf(a), reflect.ValueOf(b)
	switch {
	case av.Kind() == reflect.String && bv.Kind() == reflect.String:
		return av.String() == bv.String()
	case av.Kind() == reflect.Bool && bv.Kind() == reflect.Bool:
		return av.Bool() == bv.Bool()
	case isInt(av) && isInt(bv):
		return av.Int() == bv.Int()
	case isUint(av) && isUint(bv):
		return av.Uint() == bv.Uint()
	case isInt(av) && isUint(bv):
		return av.Int() >= 0 && uint64(av.Int()) == bv.Uint()
	case isUint(av) && isInt(bv):
		return bv.Int() >= 0 && av.Uint() == uint64(bv.Int())
	case isNumber(av) && isNumber(bv):
		return toFloat(av) == toFloat(bv)
	}
	return reflect.DeepEqual(a, b)
}

func isInt(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return true
	}
	return false
}

func isUint(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return true
	}
	return false
}

func isNumber(v reflect.Value) bool {
	return isInt(v) || isUint(v) || v.Kind() == reflect.Float32 || v.Kind() == reflect.Float64
}

func toFloat(v reflect.Value) float64 {
	switch {
	case isInt(v):
		return float64(v.Int())
	case isUint(v):
		return float64(v.Uint())
	}
	return v.Float()
}

func lt(a, b any) bool {
	aFloat, aErr := toFloat64(a)
	bFloat, bErr := toFloat64(b)

	if aErr == nil && bErr == nil {
		return aFloat < bFloat
	}

	return fmt.Sprint(a) < fmt.Sprint(b)
}

func le(a, b any) bool {
	return lt(a, b) || eq(a, b)
}

func gt(a, b any) bool {
	return !le(a, b)
}

func ge(a, b any) bool {
	return !lt(a, b)
}

// and returns its first empty argument, or the last one, as text/template's
// builtin does, so {{if and .a .b}} and {{and .a .b}} both behave as Go
// documents. Unlike the builtin, every argument is evaluated first.
func and(first any, rest ...any) any {
	if empty(first) {
		return first
	}
	for _, v := range rest {
		if empty(v) {
			return v
		}
	}
	if len(rest) == 0 {
		return first
	}
	return rest[len(rest)-1]
}

// or returns its first non-empty argument, or the last one, as text/template's
// builtin does, so {{or .title "Untitled"}} prints a fallback.
func or(first any, rest ...any) any {
	if notEmpty(first) {
		return first
	}
	for _, v := range rest {
		if notEmpty(v) {
			return v
		}
	}
	if len(rest) == 0 {
		return first
	}
	return rest[len(rest)-1]
}

func not(v any) bool {
	return empty(v)
}

func base64Encode(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}

func base64Decode(s string) (string, error) {
	data, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func urlEncode(s string) string {
	return url.QueryEscape(s)
}

func urlDecode(s string) (string, error) {
	return url.QueryUnescape(s)
}

func pathJoin(parts ...string) string {
	return joinPath(parts...)
}

func pathBase(path string) string {
	parts := strings.Split(path, "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return ""
}

func pathDir(path string) string {
	lastSlash := strings.LastIndex(path, "/")
	if lastSlash < 0 {
		return "."
	}
	if lastSlash == 0 {
		return "/"
	}
	return path[:lastSlash]
}

func pathExt(path string) string {
	lastDot := strings.LastIndex(path, ".")
	if lastDot < 0 || lastDot < strings.LastIndex(path, "/") {
		return ""
	}
	return path[lastDot:]
}

func regexMatch(pattern, s string) (bool, error) {
	return regexp.MatchString(pattern, s)
}

func regexReplace(pattern, replacement, s string) (string, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", err
	}
	return re.ReplaceAllString(s, replacement), nil
}

func regexSplit(pattern, s string) ([]string, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	return re.Split(s, -1), nil
}

// generateUUID returns a random version 4 UUID from crypto/rand.
func generateUUID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("uuid: %w", err)
	}

	// Set version (4) and variant bits
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80

	return fmt.Sprintf("%x-%x-%x-%x-%x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}

func getEnv(key string, defaultVal ...string) string {
	value := os.Getenv(key)
	if value == "" && len(defaultVal) > 0 {
		return defaultVal[0]
	}
	return value
}

func dict(pairs ...any) map[string]any {
	result := make(map[string]any)

	for i := 0; i < len(pairs)-1; i += 2 {
		key, ok := pairs[i].(string)
		if ok {
			result[key] = pairs[i+1]
		}
	}

	return result
}

func list(items ...any) []any {
	return items
}
