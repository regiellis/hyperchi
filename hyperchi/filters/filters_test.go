package filters

import (
	"html/template"
	"strings"
	"testing"
	"time"
)

func TestStringFilters(t *testing.T) {
	tests := []struct {
		name     string
		filter   string
		input    any
		args     []any
		expected any
	}{
		{"truncate", "truncate", "Hello World", []any{5}, "He..."},
		{"upper", "upper", "hello", nil, "HELLO"},
		{"lower", "lower", "HELLO", nil, "hello"},
		{"capitalize", "capitalize", "hello", nil, "Hello"},
		{"slug", "slug", "Hello World!", nil, "hello-world"},
		{"camelCase", "camelCase", "hello-world", nil, "helloWorld"},
		{"snakeCase", "snakeCase", "helloWorld", nil, "hello_world"},
		{"pluralize", "pluralize", "item", []any{5}, "items"},
		{"pluralize singular", "pluralize", "item", []any{1}, "item"},
	}

	funcMap := FuncMap()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fn, ok := funcMap[tt.filter]
			if !ok {
				t.Fatalf("filter %s not found", tt.filter)
			}

			var result any
			switch f := fn.(type) {
			case func(string) string:
				result = f(tt.input.(string))
			case func(string, int, ...string) string:
				if len(tt.args) > 0 {
					result = f(tt.input.(string), tt.args[0].(int))
				}
			case func(string, ...int) string:
				if len(tt.args) > 0 {
					result = f(tt.input.(string), tt.args[0].(int))
				}
			default:
				t.Fatalf("unexpected function signature for %s", tt.filter)
			}

			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestHTMLFilters(t *testing.T) {
	funcMap := FuncMap()

	safeFn := funcMap["safe"].(func(string) template.HTML)
	result := safeFn("<b>Bold</b>")
	if string(result) != "<b>Bold</b>" {
		t.Errorf("safe filter failed")
	}

	escapeFn := funcMap["escape"].(func(string) string)
	escaped := escapeFn("<script>alert('xss')</script>")
	if !strings.Contains(escaped, "&lt;script&gt;") {
		t.Errorf("escape filter failed")
	}

	classNamesFn := funcMap["classNames"].(func(...any) string)
	classes := classNamesFn("btn", "active", "", "primary")
	expected := "btn active primary"
	if classes != expected {
		t.Errorf("classNames filter: expected %s, got %s", expected, classes)
	}
}

func TestNumberFilters(t *testing.T) {
	funcMap := FuncMap()

	roundFn := funcMap["round"].(func(any, ...int) float64)
	rounded := roundFn(3.14159, 2)
	if rounded != 3.14 {
		t.Errorf("round filter: expected 3.14, got %f", rounded)
	}

	currencyFn := funcMap["currency"].(func(any, ...string) string)
	currency := currencyFn(1234.56)
	if currency != "$1,234.56" {
		t.Errorf("currency filter: expected $1,234.56, got %s", currency)
	}

	percentFn := funcMap["percent"].(func(any, ...int) string)
	percent := percentFn(0.875, 1)
	if percent != "87.5%" {
		t.Errorf("percent filter: expected 87.5%%, got %s", percent)
	}
}

func TestDateFilters(t *testing.T) {
	funcMap := FuncMap()

	nowFn := funcMap["now"].(func() time.Time)
	now := nowFn()
	if now.IsZero() {
		t.Errorf("now filter returned zero time")
	}

	dateFn := funcMap["date"].(func(any, ...string) string)
	testTime := time.Date(2024, 1, 15, 14, 30, 0, 0, time.UTC)
	formatted := dateFn(testTime, "2006-01-02")
	if formatted != "2024-01-15" {
		t.Errorf("date filter: expected 2024-01-15, got %s", formatted)
	}
}

func TestCollectionFilters(t *testing.T) {
	funcMap := FuncMap()

	lenFn := funcMap["len"].(func(any) int)
	length := lenFn([]string{"a", "b", "c"})
	if length != 3 {
		t.Errorf("len filter: expected 3, got %d", length)
	}

	firstFn := funcMap["first"].(func(any) any)
	first := firstFn([]string{"a", "b", "c"})
	if first != "a" {
		t.Errorf("first filter: expected 'a', got %v", first)
	}

	reverseFn := funcMap["reverse"].(func(any) []any)
	reversed := reverseFn([]int{1, 2, 3})
	if len(reversed) != 3 || reversed[0] != 3 || reversed[2] != 1 {
		t.Errorf("reverse filter failed")
	}
}

func TestUtilityFilters(t *testing.T) {
	funcMap := FuncMap()

	defaultFn := funcMap["default"].(func(any, any) any)
	result := defaultFn("", "default value")
	if result != "default value" {
		t.Errorf("default filter: expected 'default value', got %v", result)
	}

	result = defaultFn("has value", "default value")
	if result != "has value" {
		t.Errorf("default filter: expected 'has value', got %v", result)
	}

	ternaryFn := funcMap["ternary"].(func(bool, any, any) any)
	result = ternaryFn(true, "yes", "no")
	if result != "yes" {
		t.Errorf("ternary filter: expected 'yes', got %v", result)
	}

	result = ternaryFn(false, "yes", "no")
	if result != "no" {
		t.Errorf("ternary filter: expected 'no', got %v", result)
	}
}

func TestJSONFilters(t *testing.T) {
	funcMap := FuncMap()

	toJSONFn := funcMap["toJSON"].(func(any) (string, error))
	data := map[string]string{"name": "test", "value": "123"}
	json, err := toJSONFn(data)
	if err != nil {
		t.Errorf("toJSON filter error: %v", err)
	}
	if !strings.Contains(json, `"name":"test"`) {
		t.Errorf("toJSON filter: unexpected output %s", json)
	}

	fromJSONFn := funcMap["fromJSON"].(func(string) (any, error))
	parsed, err := fromJSONFn(`{"test": "value"}`)
	if err != nil {
		t.Errorf("fromJSON filter error: %v", err)
	}
	if parsed == nil {
		t.Errorf("fromJSON filter: expected non-nil result")
	}
}
func TestHTMX4Filters(t *testing.T) {
	cases := []struct {
		name string
		got  template.HTMLAttr
		want string
	}{
		{"hxAction", hxAction("/save"), `hx-action="/save"`},
		{"hxMethod", hxMethod("post"), `hx-method="post"`},
		{"hxOptimistic", hxOptimistic("#row-template"), `hx-optimistic="#row-template"`},
		{"hxPreload", hxPreload("mouseover"), `hx-preload="mouseover"`},
		{"hxIgnore", hxIgnore(), `hx-ignore`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if string(tc.got) != tc.want {
				t.Errorf("%s = %q, want %q", tc.name, tc.got, tc.want)
			}
		})
	}

	status, err := hxStatus("422", "swap:innerHTML target:#errors")
	if err != nil {
		t.Fatalf("hxStatus: %v", err)
	}
	if want := `hx-status:422="swap:innerHTML target:#errors"`; string(status) != want {
		t.Errorf("hxStatus = %q, want %q", status, want)
	}

	// The JSON is HTML-escaped inside a double-quoted attribute, with keys in
	// json.Marshal's sorted order.
	cfg := hxConfig(map[string]any{"timeout": 5000, "credentials": "include"})
	if want := `hx-config="{&#34;credentials&#34;:&#34;include&#34;,&#34;timeout&#34;:5000}"`; string(cfg) != want {
		t.Errorf("hxConfig = %s, want %s", cfg, want)
	}
}

// min and max skip every non-numeric value, a leading one included, as sum
// does: {{min "n/a" 5 3}} is 3, not 0.
func TestMinMaxSkipNonNumeric(t *testing.T) {
	fm := FuncMap()
	minFn, ok := fm["min"].(func(...any) float64)
	if !ok {
		t.Fatalf("min filter has type %T", fm["min"])
	}
	maxFn, ok := fm["max"].(func(...any) float64)
	if !ok {
		t.Fatalf("max filter has type %T", fm["max"])
	}
	cases := []struct {
		name     string
		fn       func(...any) float64
		in       []any
		expected float64
	}{
		{"min leading text", minFn, []any{"n/a", 5, 3}, 3},
		{"max leading text", maxFn, []any{"n/a", -5, -3}, -3},
		{"min numbers", minFn, []any{4, 2.5, "7"}, 2.5},
		{"max numbers", maxFn, []any{4, 2.5, "7"}, 7},
		{"min none numeric", minFn, []any{"a", "b"}, 0},
		{"max empty", maxFn, nil, 0},
	}
	for _, tc := range cases {
		if got := tc.fn(tc.in...); got != tc.expected {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.expected)
		}
	}
}
