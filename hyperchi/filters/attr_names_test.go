package filters

import (
	"strings"
	"testing"
)

func TestDataAndAriaAttrsRejectUnsafeNames(t *testing.T) {
	for _, key := range []string{`x onclick=alert(1) y`, `id"><script>`, `a=b`, ``} {
		if out, err := dataAttrs(map[string]any{key: "v"}); err == nil {
			t.Errorf("dataAttrs accepted key %q: %s", key, out)
		}
		if out, err := ariaAttrs(map[string]any{key: "v"}); err == nil {
			t.Errorf("ariaAttrs accepted key %q: %s", key, out)
		}
	}
	// A bare prefix names no attribute.
	if out, err := dataAttrs(map[string]any{"data-": "v"}); err == nil {
		t.Errorf("dataAttrs accepted key data-: %s", out)
	}
	if out, err := ariaAttrs(map[string]any{"aria-": "v"}); err == nil {
		t.Errorf("ariaAttrs accepted key aria-: %s", out)
	}
}

func TestDataAndAriaAttrsRender(t *testing.T) {
	out, err := dataAttrs(map[string]any{"user-id": 7, "data-role": `a"b`})
	if err != nil {
		t.Fatalf("dataAttrs: %v", err)
	}
	if want := `data-role="a&#34;b" data-user-id="7"`; string(out) != want {
		t.Fatalf("dataAttrs = %s, want %s", out, want)
	}
	out, err = ariaAttrs(map[string]any{"label": "Close", "aria-hidden": true})
	if err != nil {
		t.Fatalf("ariaAttrs: %v", err)
	}
	if want := `aria-hidden="true" aria-label="Close"`; string(out) != want {
		t.Fatalf("ariaAttrs = %s, want %s", out, want)
	}
}

// hostileNames would each add an event handler if written as an attribute
// name: html.EscapeString leaves spaces, = and parentheses alone.
var hostileNames = []string{
	`click x onclick=alert(1) y`,
	`x onclick=alert(1)`,
	`a"><script>alert(1)</script>`,
	`a/onclick=alert(1)`,
	"a\tonclick=alert(1)",
}

// Every filter that builds an attribute name from its input refuses a name
// that is not a safe attribute name, so the render stops instead of writing
// the handler.
func TestFiltersRejectHostileAttributeNames(t *testing.T) {
	templates := map[string]string{
		"hxOn":       `<div {{ hxOn .name "go()" }}></div>`,
		"hxStatus":   `<div {{ hxStatus .name "swap:none" }}></div>`,
		"attr":       `<div {{ attr .name "v" }}></div>`,
		"htmxAttrs":  `<div {{ htmxAttrs .htmx }}></div>`,
		"inputAttrs": `<input {{ inputAttrs .input }}>`,
		"htmxButton": `{{ htmxButton .button }}`,
		"htmxForm":   `{{ htmxForm .form }}`,
		"htmxLink":   `{{ htmxLink "home" "/" .link }}`,
		"jsonAttr":   `<div {{ jsonAttr .name .link }}></div>`,
	}
	for _, name := range hostileNames {
		data := map[string]any{
			"name":   name,
			"htmx":   HTMXAttrs{On: map[string]string{name: "go()"}},
			"input":  InputOptions{Name: "q", Attrs: map[string]string{name: "v"}},
			"button": HTMXButtonOptions{Text: "Go", URL: "/go", Attrs: map[string]string{name: "v"}},
			"form":   HTMXFormOptions{Action: "/save", Attrs: map[string]string{name: "v"}},
			"link":   map[string]string{name: "v"},
		}
		for filter, src := range templates {
			out, err := renderFilter(t, src, data)
			if err == nil {
				t.Errorf("%s accepted attribute name %q: %s", filter, name, out)
			}
			if strings.Contains(out, "onclick") || strings.Contains(out, "<script") {
				t.Errorf("%s wrote the hostile name %q: %s", filter, name, out)
			}
		}
	}
}

// Real attribute names still render, including htmx's colon and dash forms.
func TestFiltersAcceptAttributeNames(t *testing.T) {
	cases := []struct {
		src  string
		data any
		want string
	}{
		{`<div {{ hxOn "click" "go()" }}>`, nil, `<div hx-on:click="go()">`},
		{`<div {{ hxOn ":after-request" "go()" }}>`, nil, `<div hx-on::after-request="go()">`},
		{`<div {{ hxOn "htmx:after:swap" "go()" }}>`, nil, `<div hx-on:htmx:after:swap="go()">`},
		{`<div {{ hxStatus "5xx" "swap:none" }}>`, nil, `<div hx-status:5xx="swap:none">`},
		{`<div {{ attr "data-id" "7" }}>`, nil, `<div data-id="7">`},
		{`<div {{ attr "title" "" }}>`, nil, `<div >`},
		{`<input {{ inputAttrs .i }}>`, map[string]any{"i": InputOptions{Name: "q", Attrs: map[string]string{"autocomplete": "off"}}}, `<input name="q" autocomplete="off">`},
		{`{{ htmxLink "Home" "/" .o }}`, map[string]any{"o": map[string]string{"aria-current": "page"}}, `<a href="/" hx-get="/" hx-push-url="true" aria-current="page">Home</a>`},
		{`<div {{ jsonAttr "data-cfg" .o }}>`, map[string]any{"o": map[string]int{"n": 1}}, `<div data-cfg='{&#34;n&#34;:1}'>`},
	}
	for _, tc := range cases {
		out, err := renderFilter(t, tc.src, tc.data)
		if err != nil {
			t.Errorf("%s: %v", tc.src, err)
			continue
		}
		if out != tc.want {
			t.Errorf("%s = %s, want %s", tc.src, out, tc.want)
		}
	}
}

// An empty event or status pattern names no attribute.
func TestFiltersRejectEmptyNameSuffix(t *testing.T) {
	if out, err := hxOn("", "go()"); err == nil {
		t.Errorf("hxOn accepted an empty event: %s", out)
	}
	if out, err := hxStatus("", "swap:none"); err == nil {
		t.Errorf("hxStatus accepted an empty pattern: %s", out)
	}
}
