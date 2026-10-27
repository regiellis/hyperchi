package security

import (
	"html/template"
	"strings"
	"testing"
)

func TestEscapeHTML(t *testing.T) {
	testCases := []struct {
		input    string
		expected string
	}{
		{"<script>alert('xss')</script>", "&lt;script&gt;alert(&#39;xss&#39;)&lt;/script&gt;"},
		{"Hello & goodbye", "Hello &amp; goodbye"},
		{"<img src=x onerror=alert(1)>", "&lt;img src=x onerror=alert(1)&gt;"},
		{"Normal text", "Normal text"},
		{"", ""},
	}

	for _, tc := range testCases {
		t.Run(tc.input, func(t *testing.T) {
			result := EscapeHTML(tc.input)
			if result != tc.expected {
				t.Errorf("EscapeHTML(%q) = %q, expected %q", tc.input, result, tc.expected)
			}
		})
	}
}

func TestEscapeTemplateData(t *testing.T) {
	data := map[string]any{
		"title":       "<script>alert('xss')</script>",
		"description": "Safe text",
		"count":       42,
		"active":      true,
		"user": map[string]any{
			"name":  "<img src=x onerror=alert(1)>",
			"email": "user@example.com",
		},
		"tags": []string{"<script>", "safe-tag", "another<tag>"},
	}

	escaped := EscapeTemplateData(data)
	escapedMap := escaped.(map[string]any)

	if escapedMap["title"].(string) != "&lt;script&gt;alert(&#39;xss&#39;)&lt;/script&gt;" {
		t.Error("String not properly escaped")
	}

	if escapedMap["count"].(int) != 42 {
		t.Error("Non-string value changed")
	}
	if escapedMap["active"].(bool) != true {
		t.Error("Boolean value changed")
	}

	user := escapedMap["user"].(map[string]any)
	if user["name"].(string) != "&lt;img src=x onerror=alert(1)&gt;" {
		t.Error("Nested string not properly escaped")
	}

	tags := escapedMap["tags"].([]string)
	if tags[0] != "&lt;script&gt;" {
		t.Error("Slice element not properly escaped")
	}
}

func TestSafeHTML(t *testing.T) {
	content := "<b>Bold text</b>"
	safe := MarkSafe(content)

	if safe.String() != content {
		t.Error("SafeHTML.String() should return original content")
	}

	html := safe.HTML()
	if string(html) != content {
		t.Error("SafeHTML.HTML() should return template.HTML")
	}
}

func TestStripTags(t *testing.T) {
	testCases := []struct {
		input    string
		expected string
	}{
		{"<p>Hello <b>world</b></p>", "Hello world"},
		{"<script>alert('xss')</script>", "alert('xss')"},
		{"No tags here", "No tags here"},
		{"<img src='test.jpg' alt='test'>", ""},
		{"", ""},
	}

	for _, tc := range testCases {
		t.Run(tc.input, func(t *testing.T) {
			result := StripTags(tc.input)
			if result != tc.expected {
				t.Errorf("StripTags(%q) = %q, expected %q", tc.input, result, tc.expected)
			}
		})
	}
}

func TestSanitizeInput(t *testing.T) {
	testCases := []struct {
		input       string
		shouldClean bool
	}{
		{"<script>alert('xss')</script>", true},
		{"javascript:alert(1)", true},
		{"onclick=alert(1)", true},
		{"Normal text", false},
		{"Hello & goodbye", true}, // Should escape &
	}

	for _, tc := range testCases {
		t.Run(tc.input, func(t *testing.T) {
			result := SanitizeInput(tc.input)

			if tc.shouldClean && result == tc.input {
				t.Errorf("SanitizeInput should have modified %q", tc.input)
			}

			lower := strings.ToLower(result)
			dangerous := []string{"<script", "javascript:", "onclick="}
			for _, danger := range dangerous {
				if strings.Contains(lower, danger) {
					t.Errorf("SanitizeInput left dangerous content: %s in %q", danger, result)
				}
			}
		})
	}
}

func TestValidateUserInput(t *testing.T) {
	testCases := []struct {
		input       string
		shouldError bool
	}{
		{"<script>alert('xss')</script>", true},
		{"javascript:alert(1)", true},
		{"onclick=alert(1)", true},
		{"onerror=alert(1)", true},
		{"Normal text", false},
		{"Hello world", false},
		{"<p>Paragraph</p>", false}, // Safe HTML tags are allowed for content-driven apps
	}

	for _, tc := range testCases {
		t.Run(tc.input, func(t *testing.T) {
			err := ValidateUserInput(tc.input)

			if tc.shouldError && err == nil {
				t.Errorf("ValidateUserInput should have returned error for %q", tc.input)
			}
			if !tc.shouldError && err != nil {
				t.Errorf("ValidateUserInput should not have returned error for %q: %v", tc.input, err)
			}
		})
	}
}

func TestCreateSecureTemplateData(t *testing.T) {
	data := map[string]any{
		"title":   "<script>alert('xss')</script>",
		"content": MarkSafe("<p>This is safe HTML</p>"),
		"count":   42,
		"user": map[string]string{
			"name": "<img src=x onerror=alert(1)>",
		},
	}

	secure := CreateSecureTemplateData(data)

	if secure["title"].(string) != "&lt;script&gt;alert(&#39;xss&#39;)&lt;/script&gt;" {
		t.Error("String not properly escaped in secure data")
	}

	if _, ok := secure["content"].(template.HTML); !ok {
		t.Error("SafeHTML not preserved as template.HTML")
	}

	if secure["count"].(int) != 42 {
		t.Error("Non-string value changed in secure data")
	}
}

func TestTemplateFunctions(t *testing.T) {
	funcs := TemplateFunctions()

	escapeFunc := funcs["escape"].(func(string) string)
	result := escapeFunc("<script>")
	if result != "&lt;script&gt;" {
		t.Error("Template escape function not working")
	}

	safeFunc := funcs["safe"].(func(string) template.HTML)
	safeResult := safeFunc("<b>bold</b>")
	if string(safeResult) != "<b>bold</b>" {
		t.Error("Template safe function not working")
	}

	stripFunc := funcs["strip"].(func(string) string)
	stripResult := stripFunc("<p>text</p>")
	if stripResult != "text" {
		t.Error("Template strip function not working")
	}

	sanitizeFunc := funcs["sanitize"].(func(string) string)
	sanitizeResult := sanitizeFunc("<script>alert(1)</script>")
	if strings.Contains(strings.ToLower(sanitizeResult), "<script") {
		t.Error("Template sanitize function not working")
	}
}
func TestScrubHTMX(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string // substrings that MUST NOT appear after scrubbing
		keep string // substring that SHOULD survive (sanity check)
	}{
		{
			name: "strips hx-* attributes",
			in:   `<button hx-post="/danger" hx-target="#out" class="ok">Click</button>`,
			want: "hx-",
			keep: `class="ok"`,
		},
		{
			name: "strips data-hx-* attributes",
			in:   `<div data-hx-get="/leak" data-hx-trigger="load">x</div>`,
			want: "data-hx-",
			keep: ">x</div>",
		},
		{
			name: "strips inline event handlers",
			in:   `<img src="x" onerror="alert(1)">`,
			want: "onerror",
			keep: `src="x"`,
		},
		{
			name: "strips script tags",
			in:   `before<script>alert(1)</script>after`,
			want: "<script",
			keep: "beforeafter",
		},
		{
			name: "leaves ordinary HTML alone",
			in:   `<p class="lead">Hello <strong>world</strong></p>`,
			want: "DOES_NOT_APPEAR",
			keep: `<strong>world</strong>`,
		},
		{
			name: "no false-positive on hx- inside text",
			in:   `<p>The hx- prefix denotes htmx attributes.</p>`,
			want: "DOES_NOT_APPEAR",
			keep: "hx- prefix",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ScrubHTMX(tc.in)
			if tc.want != "DOES_NOT_APPEAR" && strings.Contains(strings.ToLower(got), strings.ToLower(tc.want)) {
				t.Errorf("ScrubHTMX did not remove %q from %q (got %q)", tc.want, tc.in, got)
			}
			if !strings.Contains(got, tc.keep) {
				t.Errorf("ScrubHTMX dropped expected content %q from %q (got %q)", tc.keep, tc.in, got)
			}
		})
	}
}

// The denylist used to remove only lowercase matches, and one removal pass
// could reassemble a marker from its pieces.
func TestSanitizeInputIgnoresCaseAndNesting(t *testing.T) {
	for _, input := range []string{
		"JavaScript:alert(1)",
		"VBSCRIPT:msgbox",
		"Data:text/html,x",
		"x OnError=alert(1)",
		"oneonerror=rror=alert(1)",
		"javajavascript:script:alert(1)",
		"jAvAsCrIpT:alert(1)",
	} {
		t.Run(input, func(t *testing.T) {
			lower := strings.ToLower(SanitizeInput(input))
			for _, danger := range []string{"javascript:", "vbscript:", "data:", "onerror="} {
				if strings.Contains(lower, danger) {
					t.Fatalf("SanitizeInput(%q) = %q, still contains %s", input, SanitizeInput(input), danger)
				}
			}
		})
	}
}
