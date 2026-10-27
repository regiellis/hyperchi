package filters

import (
	"bytes"
	"encoding/json"
	"html"
	"html/template"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

const hostile = `x' onmouseover='alert(1)' y="z" <b>&`

// jsonAttrValue checks out is exactly name="<escaped JSON>" and returns the
// decoded JSON. A raw quote of either kind inside the value would let the input
// end the attribute.
func jsonAttrValue(t *testing.T, name string, out template.HTMLAttr) map[string]any {
	t.Helper()
	s := string(out)
	prefix := name + `="`
	if !strings.HasPrefix(s, prefix) || !strings.HasSuffix(s, `"`) {
		t.Fatalf("%s not written as a double-quoted attribute: %s", name, s)
	}
	inner := s[len(prefix) : len(s)-1]
	if strings.ContainsAny(inner, `"'<>`) {
		t.Fatalf("%s value carries an unescaped quote or bracket: %s", name, s)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(html.UnescapeString(inner)), &got); err != nil {
		t.Fatalf("%s value is not valid JSON once unescaped: %v (%s)", name, err, s)
	}
	return got
}

func TestJSONAttributesEscapeHostileValues(t *testing.T) {
	cases := []struct {
		name string
		out  template.HTMLAttr
	}{
		{"hx-config", hxConfig(map[string]any{"v": hostile})},
		{"hx-vals", hxVals(map[string]any{"v": hostile})},
		{"hx-headers", hxHeaders(map[string]string{"v": hostile})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := jsonAttrValue(t, tc.name, tc.out); got["v"] != hostile {
				t.Fatalf("%s round trip = %q, want %q", tc.name, got["v"], hostile)
			}
		})
	}
}

func TestHTMXAttrsEscapeHostileJSON(t *testing.T) {
	out, err := htmxAttrs(HTMXAttrs{
		Config:  map[string]any{"v": hostile},
		Vals:    map[string]any{"v": hostile},
		Headers: map[string]string{"v": hostile},
	})
	if err != nil {
		t.Fatalf("htmxAttrs: %v", err)
	}
	// Each attribute is name="value" with no raw quote inside, so splitting on
	// `" ` separates them.
	parts := strings.Split(string(out), `" `)
	if len(parts) != 3 {
		t.Fatalf("want three attributes, got %d: %s", len(parts), out)
	}
	for i, p := range parts {
		if i < len(parts)-1 {
			p += `"`
		}
		name := p[:strings.Index(p, "=")]
		if got := jsonAttrValue(t, name, template.HTMLAttr(p)); got["v"] != hostile {
			t.Errorf("%s round trip = %q", name, got["v"])
		}
	}
}

// hx-vals is JSON, so numbers and booleans keep their type.
func TestHxValsWritesJSON(t *testing.T) {
	got := jsonAttrValue(t, "hx-vals", hxVals(map[string]any{"n": 5, "ok": true}))
	if got["n"] != float64(5) || got["ok"] != true {
		t.Fatalf("hx-vals = %v, want typed JSON values", got)
	}
}

// renderFilter executes src with the registered filters and data.
func renderFilter(t *testing.T, src string, data any) (string, error) {
	t.Helper()
	tmpl, err := template.New("t").Funcs(FuncMap()).Parse(src)
	if err != nil {
		t.Fatalf("parse %s: %v", src, err)
	}
	var buf bytes.Buffer
	err = tmpl.Execute(&buf, data)
	return buf.String(), err
}

type status string

// One contains serves strings and collections: substring for a string,
// membership (by the rules of eq) for a slice or array.
func TestContainsHandlesStringsAndCollections(t *testing.T) {
	data := map[string]any{
		"tags":     []string{"go", "htmx"},
		"nums":     []int{1, 2, 3},
		"statuses": []status{"open", "closed"},
		"title":    status("hello world"),
	}
	cases := map[string]string{
		`{{contains "hello world" "world"}}`: "true",
		`{{contains "hello world" "moon"}}`:  "false",
		`{{contains .title "world"}}`:        "true",
		`{{contains "abc" 1}}`:               "false",
		`{{contains .tags "go"}}`:            "true",
		`{{contains .tags "rust"}}`:          "false",
		`{{contains .nums 2.0}}`:             "true",
		`{{contains .statuses "open"}}`:      "true",
		`{{contains .missing "x"}}`:          "false",
	}
	for src, want := range cases {
		got, err := renderFilter(t, src, data)
		if err != nil {
			t.Errorf("%s: %v", src, err)
			continue
		}
		if got != want {
			t.Errorf("%s = %s, want %s", src, got, want)
		}
	}
}

func TestJSONGetNegativeIndexIsMissing(t *testing.T) {
	data := map[string]any{"items": []any{"a", "b"}}
	for _, path := range []string{"items.-1", "items.2", "items.x"} {
		if got := jsonGet(data, path); got != nil {
			t.Errorf("jsonGet(%q) = %v, want nil", path, got)
		}
	}
	if got := jsonGet(data, "items.1"); got != "b" {
		t.Errorf("jsonGet(items.1) = %v, want b", got)
	}
}

var uuidV4 = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}`)

// uuid must not come from math/rand: with GODEBUG=randautoseed=0, math/rand
// starts from the same seed in every process, so two fresh processes would
// print the same UUID. The test runs itself twice as a child to compare.
func TestUUIDIsUnpredictable(t *testing.T) {
	if os.Getenv("HYPERCHI_FILTERS_UUID_CHILD") == "1" {
		out, err := renderFilter(t, `{{uuid}}`, nil)
		if err != nil {
			t.Fatalf("uuid: %v", err)
		}
		if _, err := os.Stdout.WriteString("UUID=" + out + "\n"); err != nil {
			t.Fatalf("write: %v", err)
		}
		return
	}
	run := func() string {
		cmd := exec.Command(os.Args[0], "-test.run=^TestUUIDIsUnpredictable$", "-test.count=1")
		cmd.Env = append(os.Environ(), "HYPERCHI_FILTERS_UUID_CHILD=1", "GODEBUG=randautoseed=0")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("child: %v\n%s", err, out)
		}
		id := uuidV4.FindString(string(out))
		if id == "" {
			t.Fatalf("child printed no version 4 UUID:\n%s", out)
		}
		return id
	}
	if a, b := run(), run(); a == b {
		t.Fatalf("two processes generated the same UUID %s: it is predictable", a)
	}
}
