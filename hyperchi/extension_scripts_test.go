package hyperchi

import (
	"strings"
	"testing"
)

// The browser-side htmx 4 extension scripts served at /hyperchi/js/* are
// embedded in the framework binary. These tests pin that the real extension
// code ships: with a stub in its place, neither the SSE nor the WebSocket
// extension registers in the browser and every live section stays dead, with
// no error on the server.

func TestLoadScriptAssetServesRealExtensionCode(t *testing.T) {
	cases := []struct {
		name    string
		marker  string // the registerExtension call unique to that script
		minSize int
	}{
		{"hx-sse.js", "registerExtension('sse'", 4000},
		{"hx-ws.js", "registerExtension('ws'", 4000},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			body := string(loadScriptAsset(c.name))
			if strings.Contains(body, "missing asset") {
				t.Fatalf("%s served the missing-asset stub, not the real extension", c.name)
			}
			if len(body) < c.minSize {
				t.Fatalf("%s is %d bytes, want >= %d (looks like a stub)", c.name, len(body), c.minSize)
			}
			if !strings.Contains(body, c.marker) {
				t.Fatalf("%s does not contain %q; the wrong file is embedded", c.name, c.marker)
			}
		})
	}
}

func TestLoadScriptAssetPanicsOnUnknownScript(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for an unknown embedded script, got none")
		}
	}()
	loadScriptAsset("does-not-exist.js")
}
