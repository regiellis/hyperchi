package hyperchi

import (
	"path/filepath"
	"strings"
	"testing"
)

// The SSE and WebSocket helpers are documented as template functions, so a
// template using them must load and render them as attributes, with the values
// escaped.
func TestExtensionTemplateHelpersRenderEscapedAttributes(t *testing.T) {
	dir := t.TempDir()
	writeTemplate(t, dir, "templates/live.tmpl", `{{define "live"}}`+
		`<div {{sseConnect .Events}} {{sseSwap "tick"}}></div>`+
		`<div {{wsConnect .Socket}}><form {{wsSend "chat"}}></form></div>`+
		`{{end}}`)
	chdir(t, dir)

	app := New()
	if err := app.LoadTemplates(filepath.Join("templates", "*.tmpl")); err != nil {
		t.Fatalf("LoadTemplates: %v", err)
	}
	out, err := app.RenderToString("live", map[string]any{
		"Events": "/events?a=1&b=2",
		"Socket": `/ws" onmouseover="alert(1)`,
	})
	if err != nil {
		t.Fatalf("RenderToString: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		`hx-sse:connect="/events?a=1&amp;b=2"`,
		`hx-on:tick="htmx.swap(`,
		`hx-ws:connect="/ws&#34; onmouseover=&#34;alert(1)"`,
		`hx-ws:send="chat"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %s\n%s", want, got)
		}
	}
	if strings.Contains(got, `onmouseover="alert`) {
		t.Fatalf("attribute injection rendered:\n%s", got)
	}
}

// An event name becomes part of an attribute name, so anything outside a safe
// character set must fail the render instead of reaching the markup.
func TestExtensionTemplateHelpersRejectUnsafeEventNames(t *testing.T) {
	dir := t.TempDir()
	writeTemplate(t, dir, "templates/bad.tmpl", `{{define "bad"}}<div {{sseSwap .}}></div>{{end}}`)
	chdir(t, dir)

	app := New()
	if err := app.LoadTemplates(filepath.Join("templates", "*.tmpl")); err != nil {
		t.Fatalf("LoadTemplates: %v", err)
	}
	if _, err := app.RenderToString("bad", `x onclick=alert(1)`); err == nil {
		t.Fatal("unsafe event name rendered without an error")
	}
}
