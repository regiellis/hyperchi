package response_targets

import (
	"html/template"
	"strings"
	"testing"

	"github.com/regiellis/hyperchi/hyperchi/extensions"
)

func responseTargetsFunc(t *testing.T) func(ResponseTargetConfig) (template.HTMLAttr, error) {
	t.Helper()
	r := extensions.NewRegistry()
	if err := r.Register(NewResponseTargetsExtension()); err != nil {
		t.Fatalf("Register: %v", err)
	}
	fn, ok := r.GetTemplateFuncs()["responseTargets"].(func(ResponseTargetConfig) (template.HTMLAttr, error))
	if !ok {
		t.Fatalf("responseTargets has type %T, want func(ResponseTargetConfig) (template.HTMLAttr, error)", r.GetTemplateFuncs()["responseTargets"])
	}
	return fn
}

// A status key becomes part of an attribute name, which cannot be escaped, so a
// key that is not a status pattern fails instead of reaching the markup.
func TestResponseTargetsRejectsUnsafeStatusKeys(t *testing.T) {
	fn := responseTargetsFunc(t)
	for _, key := range []string{
		`404 onmouseover=alert(1) x`,
		`4xx"><script>alert(1)</script>`,
		`error`,
		`4`,
		`4040`,
		``,
	} {
		for name, cfg := range map[string]ResponseTargetConfig{
			"Targets": {Targets: map[string]string{key: "#box"}},
			"Swaps":   {Swaps: map[string]string{key: "innerHTML"}},
		} {
			out, err := fn(cfg)
			if err == nil {
				t.Errorf("%s key %q rendered %q, want an error", name, key, out)
			}
		}
	}
}

func TestResponseTargetsRendersStatusPatterns(t *testing.T) {
	fn := responseTargetsFunc(t)
	out, err := fn(ResponseTargetConfig{
		Target:  "#ok",
		Targets: map[string]string{"404": "#missing", "4*": `#e"x`, "50x": "#server", "5XX": "#all"},
		Swaps:   map[string]string{"422": "outerHTML"},
	})
	if err != nil {
		t.Fatalf("responseTargets: %v", err)
	}
	// Keys sort as written: "4*" before "404".
	want := `hx-target="#ok" hx-status:4xx="target:#e&#34;x" hx-status:404="target:#missing" ` +
		`hx-status:50x="target:#server" hx-status:5xx="target:#all" hx-status:422="swap:outerHTML"`
	if string(out) != want {
		t.Fatalf("responseTargets =\n%s\nwant\n%s", out, want)
	}
	if strings.Contains(string(out), `"x"`) {
		t.Fatalf("value not escaped: %s", out)
	}
}
