package filters

import (
	"strings"
	"testing"
)

// The URL-building helpers return template.HTML, which bypasses the
// auto-escaper, so they must reject non-allowlisted URL schemes themselves.
func TestURLFiltersRejectUnsafeSchemes(t *testing.T) {
	unsafe := []string{
		"javascript:alert(1)",
		"JAVASCRIPT:alert(1)",
		" javascript:alert(1)",
		"data:text/html,<script>alert(1)</script>",
		"vbscript:msgbox(1)",
		"java\tscript:alert(1)",
	}

	for _, u := range unsafe {
		t.Run(u, func(t *testing.T) {
			outputs := map[string]string{
				"link":   string(link("click", u)),
				"img":    string(img(u, "alt")),
				"script": string(script(u)),
				"style":  string(style(u)),
			}
			for name, out := range outputs {
				if !strings.Contains(out, unsafeURLPlaceholder) {
					t.Errorf("%s(%q) did not neutralize the URL: %s", name, u, out)
				}
				if strings.Contains(strings.ToLower(out), "script:") {
					t.Errorf("%s(%q) leaked the scheme into output: %s", name, u, out)
				}
			}
		})
	}
}

func TestURLFiltersAllowSafeURLs(t *testing.T) {
	safe := []string{
		"https://example.com/page?q=1",
		"http://example.com",
		"/relative/path",
		"relative.html",
		"//cdn.example.com/lib.js",
		"mailto:user@example.com",
		"tel:+15551234567",
	}

	for _, u := range safe {
		t.Run(u, func(t *testing.T) {
			out := string(link("click", u))
			if strings.Contains(out, unsafeURLPlaceholder) {
				t.Errorf("link(%q) was wrongly neutralized: %s", u, out)
			}
		})
	}
}

func TestLinkStillEscapesHTMLInURL(t *testing.T) {
	out := string(link("x", `https://example.com/?a="><script>`))
	if strings.Contains(out, "<script>") {
		t.Errorf("link did not escape HTML metacharacters: %s", out)
	}
}
