package security

import (
	"fmt"
	"html"
	"html/template"
	"reflect"
	"regexp"
	"strings"
)

// htmxAttrRe matches a whole hx-* or data-hx-* attribute, with a double-quoted,
// single-quoted, unquoted, or missing value. It requires leading whitespace so
// text that merely contains "hx-" is left alone.
var htmxAttrRe = regexp.MustCompile(`(?i)\s(?:data-)?hx-[a-z0-9:_-]+(?:\s*=\s*(?:"[^"]*"|'[^']*'|[^\s"'>]+))?`)

// inlineEventAttrRe matches inline event handler attributes (onclick, onerror,
// and the rest). They are not htmx-specific, but they run script, so scrubbed
// HTML must not keep them.
var inlineEventAttrRe = regexp.MustCompile(`(?i)\son[a-z]+\s*=\s*(?:"[^"]*"|'[^']*'|[^\s"'>]+)`)

// scriptTagRe matches <script>...</script> blocks and self-closing <script/>.
var scriptTagRe = regexp.MustCompile(`(?is)<script\b[^>]*>.*?</script\s*>|<script\b[^>]*/>`)

// ScrubHTMX removes hx-* and data-hx-* attributes, inline on* event handlers,
// and <script> blocks from s.
//
// Use it when third-party HTML must be injected into an htmx page. htmx's own
// security guidance says such content must lose its hx- and data-hx-
// attributes and inline scripts, or the injected markup can issue requests
// and run code.
//
// It is a second line of defense. The first is not to inject raw untrusted
// HTML at all: html/template escapes by default, and the hx-ignore attribute
// fences off untrusted regions of a page. This scrubber is regex-based, not an
// HTML parser. For allowlist sanitizing use github.com/microcosm-cc/bluemonday.
func ScrubHTMX(s string) string {
	s = scriptTagRe.ReplaceAllString(s, "")
	s = htmxAttrRe.ReplaceAllString(s, "")
	s = inlineEventAttrRe.ReplaceAllString(s, "")
	return s
}

// EscapeHTML escapes <, >, &, ', and " with html.EscapeString, for writing
// text into HTML outside html/template.
func EscapeHTML(content string) string {
	return html.EscapeString(content)
}

// EscapeTemplateData returns a deep copy of data with every reachable string
// HTML-escaped, through maps, slices, arrays, structs, pointers, and
// interfaces. Unexported struct fields come back zero. Use it for output that
// does not go through html/template: passing the result to html/template
// escapes the strings twice. It panics if data is nil.
func EscapeTemplateData(data any) any {
	return escapeValue(reflect.ValueOf(data)).Interface()
}

// escapeValue returns a copy of v with its strings escaped. Kinds it does not
// handle (numbers, bools, funcs, channels) come back unchanged.
func escapeValue(v reflect.Value) reflect.Value {
	if !v.IsValid() {
		return v
	}

	switch v.Kind() {
	case reflect.String:
		escaped := html.EscapeString(v.String())
		return reflect.ValueOf(escaped)

	case reflect.Map:
		newMap := reflect.MakeMap(v.Type())
		for _, key := range v.MapKeys() {
			value := v.MapIndex(key)
			escapedValue := escapeValue(value)
			newMap.SetMapIndex(key, escapedValue)
		}
		return newMap

	case reflect.Slice, reflect.Array:
		newSlice := reflect.MakeSlice(v.Type(), v.Len(), v.Cap())
		for i := 0; i < v.Len(); i++ {
			element := v.Index(i)
			escapedElement := escapeValue(element)
			newSlice.Index(i).Set(escapedElement)
		}
		return newSlice

	case reflect.Struct:
		newStruct := reflect.New(v.Type()).Elem()
		for i := 0; i < v.NumField(); i++ {
			field := v.Field(i)
			if field.CanInterface() {
				escapedField := escapeValue(field)
				if newStruct.Field(i).CanSet() {
					newStruct.Field(i).Set(escapedField)
				}
			}
		}
		return newStruct

	case reflect.Pointer:
		if v.IsNil() {
			return v
		}
		elem := v.Elem()
		escapedElem := escapeValue(elem)
		newPtr := reflect.New(elem.Type())
		newPtr.Elem().Set(escapedElem)
		return newPtr

	case reflect.Interface:
		if v.IsNil() {
			return v
		}
		actualValue := v.Elem()
		escapedValue := escapeValue(actualValue)
		return escapedValue

	default:
		return v
	}
}

// SafeHTML is markup the application trusts. CreateSecureTemplateData passes
// it through unescaped as template.HTML. Never build one from user input.
type SafeHTML string

// String returns the markup as a plain string.
func (s SafeHTML) String() string {
	return string(s)
}

// HTML returns the markup as template.HTML, which html/template does not
// escape.
func (s SafeHTML) HTML() template.HTML {
	return template.HTML(s)
}

// MarkSafe wraps trusted markup as SafeHTML. Use it only for markup the
// application built itself.
func MarkSafe(content string) SafeHTML {
	return SafeHTML(content)
}

// StripTags removes every "<...>" span from content. It does not parse HTML:
// the text of script and style elements stays, entities stay encoded, and a
// stray "<" with no closing ">" ends the stripping. It is not a sanitizer.
func StripTags(content string) string {
	result := content
	for {
		start := strings.Index(result, "<")
		if start == -1 {
			break
		}
		end := strings.Index(result[start:], ">")
		if end == -1 {
			break
		}
		result = result[:start] + result[start+end+1:]
	}
	return result
}

// sanitizeDenylistRe matches the substrings SanitizeInput deletes, in any
// case.
var sanitizeDenylistRe = regexp.MustCompile(`(?i)javascript:|data:|vbscript:|on(?:load|error|click|mouseover|focus|blur)=`)

// SanitizeInput HTML-escapes input, then deletes a short list of substrings
// (javascript:, data:, vbscript:, and a few on*= handlers) in any case,
// repeating until none is left, so "oneonerror=rror=" cannot reassemble.
//
// It is not an XSS defense, and passing text through it does not make the
// text safe anywhere. The escaping it does first is the only real protection,
// and only for HTML element content. The denylist misses entities, other
// handlers, and other URL schemes, and it changes harmless text. Render
// untrusted text with html/template, which escapes by context (element,
// attribute, URL, script). To keep some untrusted markup, use an allowlist
// sanitizer such as github.com/microcosm-cc/bluemonday.
func SanitizeInput(input string) string {
	out := html.EscapeString(input)
	for {
		next := sanitizeDenylistRe.ReplaceAllString(out, "")
		if next == out {
			return out
		}
		out = next
	}
}

// ValidateUserInput returns an error when input contains, ignoring case, one
// of a short list of script markers (<script, javascript:, data:text/html,
// vbscript:, and a few on*= handlers).
//
// It is not a security control. Attackers can encode around a denylist, so it
// never replaces html/template's contextual escaping or a real sanitizer such
// as bluemonday. Use it only to turn away the most obvious probes early.
func ValidateUserInput(input string) error {
	dangerous := []string{
		"<script", "</script>", "javascript:", "data:text/html",
		"vbscript:", "onload=", "onerror=", "onclick=",
	}

	lower := strings.ToLower(input)
	for _, danger := range dangerous {
		if strings.Contains(lower, danger) {
			return fmt.Errorf("potentially dangerous content detected: %s", danger)
		}
	}

	return nil
}

// CreateSecureTemplateData returns a copy of data with strings escaped as in
// EscapeTemplateData and SafeHTML values converted to template.HTML. A nil
// value panics. As with EscapeTemplateData, rendering the result through
// html/template escapes plain strings a second time.
func CreateSecureTemplateData(data map[string]any) map[string]any {
	secureData := make(map[string]any)

	for key, value := range data {
		switch v := value.(type) {
		case string:
			secureData[key] = html.EscapeString(v)
		case SafeHTML:
			secureData[key] = template.HTML(v)
		default:
			secureData[key] = EscapeTemplateData(value)
		}
	}

	return secureData
}

// TemplateFunctions returns the template helpers escape, safe, strip,
// sanitize, and scrubHTMX. Three need care under html/template: escape
// double-escapes, since html/template already escapes its output; safe turns
// off escaping, so use it only on markup the application built itself; and
// sanitize is not an XSS defense (see SanitizeInput).
func TemplateFunctions() template.FuncMap {
	return template.FuncMap{
		"escape":    EscapeHTML,
		"safe":      func(s string) template.HTML { return template.HTML(s) },
		"strip":     StripTags,
		"sanitize":  SanitizeInput,
		"scrubHTMX": func(s string) template.HTML { return template.HTML(ScrubHTMX(s)) },
	}
}
