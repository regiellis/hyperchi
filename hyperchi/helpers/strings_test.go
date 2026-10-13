package helpers

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestStrPredicates(t *testing.T) {
	tests := []struct {
		in                              string
		empty, blank, digits, alpha, an bool
	}{
		{"", true, true, false, false, false},
		{"   \t\n", false, true, false, false, false},
		{"123", false, false, true, false, true},
		{"abc", false, false, false, true, true},
		{"abc123", false, false, false, false, true},
		{"héllo", false, false, false, true, true},
		{"١٢٣", false, false, true, false, true}, // Arabic-Indic digits
		{"a b", false, false, false, false, false},
	}
	for _, tc := range tests {
		s := S(tc.in)
		if s.IsEmpty() != tc.empty || s.IsNotEmpty() == tc.empty {
			t.Errorf("%q empty = %v", tc.in, s.IsEmpty())
		}
		if s.IsBlank() != tc.blank || s.IsNotBlank() == tc.blank {
			t.Errorf("%q blank = %v", tc.in, s.IsBlank())
		}
		if s.IsDigits() != tc.digits {
			t.Errorf("%q IsDigits = %v", tc.in, s.IsDigits())
		}
		if s.IsAlpha() != tc.alpha {
			t.Errorf("%q IsAlpha = %v", tc.in, s.IsAlpha())
		}
		if s.IsAlphaNumeric() != tc.an {
			t.Errorf("%q IsAlphaNumeric = %v", tc.in, s.IsAlphaNumeric())
		}
	}
}

func TestStrTransforms(t *testing.T) {
	tests := []struct {
		name string
		got  Str
		want string
	}{
		{"trim", S("  hi  ").Trim(), "hi"},
		{"trim left", S(" \thi ").TrimLeft(), "hi "},
		{"trim right", S(" hi \n").TrimRight(), " hi"},
		{"trim prefix", S("prefix-x").TrimPrefix("prefix-"), "x"},
		{"trim prefix absent", S("x").TrimPrefix("p"), "x"},
		{"trim suffix", S("file.go").TrimSuffix(".go"), "file"},
		{"lower", S("HéLLO").Lower(), "héllo"},
		{"upper", S("héllo").Upper(), "HÉLLO"},
		{"title", S("hello wide-world 2nd").Title(), "Hello Wide-World 2nd"},
		{"title unicode", S("élan vital").Title(), "Élan Vital"},
		{"title empty", S("").Title(), ""},
		{"capitalize", S("élan").Capitalize(), "Élan"},
		{"capitalize empty", S("").Capitalize(), ""},
		{"replace", S("a-b-c").Replace("-", "+"), "a+b+c"},
		{"replaceN", S("a-b-c").ReplaceN("-", "+", 1), "a+b-c"},
		{"join", S(", ").Join([]string{"a", "b"}), "a, b"},
		{"repeat", S("ab").Repeat(3), "ababab"},
		{"repeat zero", S("ab").Repeat(0), ""},
		{"repeat negative", S("ab").Repeat(-2), ""},
		{"reverse unicode", S("héllo").Reverse(), "olléh"},
		{"reverse empty", S("").Reverse(), ""},
		{"remove whitespace", S(" a \t b\nc ").RemoveWhitespace(), "abc"},
		{"collapse whitespace", S("  a \t\n b  ").CollapseWhitespace(), "a b"},
	}
	for _, tc := range tests {
		if tc.got.String() != tc.want {
			t.Errorf("%s = %q, want %q", tc.name, tc.got, tc.want)
		}
	}
}

func TestStrSearch(t *testing.T) {
	s := S("go gopher go")
	if !s.Contains("gopher") || s.Contains("rust") {
		t.Error("Contains")
	}
	if !s.ContainsAny("xyz!p") || s.ContainsAny("xyz") {
		t.Error("ContainsAny")
	}
	if !s.StartsWith("go ") || !s.EndsWith(" go") || s.StartsWith("x") {
		t.Error("StartsWith/EndsWith")
	}
	if s.Count("go") != 3 || s.Index("gopher") != 3 || s.LastIndex("go") != 10 || s.Index("x") != -1 {
		t.Errorf("Count/Index: %d %d %d", s.Count("go"), s.Index("gopher"), s.LastIndex("go"))
	}
	if s.Len() != 12 || S("é").Len() != 1 || S("日本語").Len() != 3 {
		t.Error("Len counts runes")
	}
	if ok, err := S("abc123").Matches(`^[a-z]+\d+$`); !ok || err != nil {
		t.Errorf("Matches = %v, %v", ok, err)
	}
	if ok, err := S("abc").Matches(`\d`); ok || err != nil {
		t.Errorf("Matches without a match = %v, %v", ok, err)
	}
	if ok, err := S("abc").Matches(`(`); ok || err == nil {
		t.Errorf("Matches with an invalid pattern = %v, %v; want an error", ok, err)
	}
	if got, err := S("a1b22c333").FindAll(`\d+`); err != nil || !reflect.DeepEqual(got, []string{"1", "22", "333"}) {
		t.Errorf("FindAll = %v, %v", got, err)
	}
	if got, err := S("x").FindAll(`(`); err == nil || got != nil {
		t.Errorf("FindAll invalid pattern = %#v, %v; want nil and an error", got, err)
	}
	if got, err := S("a1b22").RegexReplace(`\d+`, "#"); err != nil || got != "a#b#" {
		t.Errorf("RegexReplace = %q, %v", got, err)
	}
	if got, err := S("a1").RegexReplace(`(`, "#"); err == nil || got != "a1" {
		t.Errorf("RegexReplace invalid pattern = %q, %v; want unchanged and an error", got, err)
	}
}

func TestStrSplitting(t *testing.T) {
	if got := S("a,b,,c").Split(","); !reflect.DeepEqual(got, []string{"a", "b", "", "c"}) {
		t.Errorf("Split = %v", got)
	}
	if got := S("a=b=c").SplitN("=", 2); !reflect.DeepEqual(got, []string{"a", "b=c"}) {
		t.Errorf("SplitN = %v", got)
	}
	if got := S("l1\nl2").SplitLines(); !reflect.DeepEqual(got, []string{"l1", "l2"}) {
		t.Errorf("SplitLines = %v", got)
	}
	if got := S("  a \t b\n").SplitWhitespace(); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("SplitWhitespace = %v", got)
	}
	if got := S("").SplitWhitespace(); len(got) != 0 {
		t.Errorf("SplitWhitespace empty = %v", got)
	}
}

func TestStrSlicing(t *testing.T) {
	tests := []struct {
		name string
		got  Str
		want string
	}{
		{"slice", S("hello").Slice(1, 3), "el"},
		{"slice negative", S("hello").Slice(-3, -1), "ll"},
		{"slice clamps", S("hello").Slice(-10, 10), "hello"},
		{"slice inverted", S("hello").Slice(4, 2), ""},
		{"slice empty", S("").Slice(0, 5), ""},
		{"left", S("hello").Left(2), "he"},
		{"left over", S("hello").Left(10), "hello"},
		{"left zero", S("hello").Left(0), ""},
		{"left negative", S("hello").Left(-1), ""},
		{"right", S("hello").Right(3), "llo"},
		{"right over", S("hello").Right(9), "hello"},
		{"right negative", S("hello").Right(-1), ""},
		// Left pairs with the rune offsets Index returns.
		{"left of index", S("héllo:80").Left(S("héllo:80").Index(":")), "héllo"},
	}
	for _, tc := range tests {
		if tc.got.String() != tc.want {
			t.Errorf("%s = %q, want %q", tc.name, tc.got, tc.want)
		}
	}
}

func TestStrPadding(t *testing.T) {
	tests := []struct {
		name string
		got  Str
		want string
	}{
		{"center", S("ab").Center(6), "  ab  "},
		{"center odd", S("ab").Center(5, "*"), "*ab**"},
		{"center narrow", S("abc").Center(2), "abc"},
		{"pad left", S("7").PadLeft(3, "0"), "007"},
		{"pad left default", S("7").PadLeft(2), " 7"},
		{"pad left narrow", S("123").PadLeft(2, "0"), "123"},
		{"pad right", S("7").PadRight(3, "."), "7.."},
		{"pad right default", S("7").PadRight(2), "7 "},
		{"pad right narrow", S("123").PadRight(1), "123"},
	}
	for _, tc := range tests {
		if tc.got.String() != tc.want {
			t.Errorf("%s = %q, want %q", tc.name, tc.got, tc.want)
		}
	}
}

func TestStrConversions(t *testing.T) {
	if n, err := S("42").ToInt(); n != 42 || err != nil {
		t.Errorf("ToInt(42) = %d, %v", n, err)
	}
	for _, in := range []string{"x", " 4", "", "3.5"} {
		if _, err := S(in).ToInt(); err == nil {
			t.Errorf("ToInt(%q) returned no error", in)
		}
	}
	if S("-3").ToIntOr(9) != -3 || S("3.5").ToIntOr(9) != 9 || S("").ToIntOr(9) != 9 {
		t.Error("ToIntOr")
	}
	if f, err := S("2.5").ToFloat(); f != 2.5 || err != nil {
		t.Errorf("ToFloat(2.5) = %v, %v", f, err)
	}
	if _, err := S("x").ToFloat(); err == nil {
		t.Error("ToFloat(x) returned no error")
	}
	if S("1e3").ToFloatOr(1) != 1000 || S("abc").ToFloatOr(1.5) != 1.5 {
		t.Error("ToFloatOr")
	}
	for _, in := range []string{"true", "TRUE", " 1 ", "yes", "On"} {
		if !S(in).ToBool() {
			t.Errorf("ToBool(%q) = false", in)
		}
	}
	for _, in := range []string{"", "false", "0", "no", "off", "2", "yes please"} {
		if S(in).ToBool() {
			t.Errorf("ToBool(%q) = true", in)
		}
	}
}

func TestJoinHelpersAndWords(t *testing.T) {
	if Join("-", "a", "b") != "a-b" || Join("-") != "" {
		t.Error("Join")
	}
	if JoinLines("a", "b") != "a\nb" || JoinSpaces("a", "b") != "a b" {
		t.Error("JoinLines/JoinSpaces")
	}
	if got := Words("  one two\tthree\n"); !reflect.DeepEqual(got, []string{"one", "two", "three"}) {
		t.Errorf("Words = %v", got)
	}
	if WordCount("") != 0 || WordCount(" a  b ") != 2 {
		t.Error("WordCount")
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		name   string
		s      string
		max    int
		suffix []string
		want   string
	}{
		{"fits", "hello", 5, nil, "hello"},
		{"cuts with default suffix", "hello world", 8, nil, "hello..."},
		{"custom suffix", "hello world", 6, []string{"~"}, "hello~"},
		{"empty suffix", "hello world", 5, []string{""}, "hello"},
		{"max shorter than suffix", "hello", 2, nil, "he"},
		{"zero", "hello", 0, nil, ""},
		{"negative", "hello", -1, nil, ""},
		{"empty input", "", 3, nil, ""},
		{"multibyte kept whole", "héllo wörld", 6, nil, "hél..."},
		{"multibyte short max", "日本語テキスト", 2, nil, "日本"},
		{"multibyte fits by runes", "日本語", 3, nil, "日本語"},
		{"multibyte suffix", "abcdef", 4, []string{"…"}, "abc…"},
	}
	for _, tc := range tests {
		got := Truncate(tc.s, tc.max, tc.suffix...)
		if got != tc.want {
			t.Errorf("%s: Truncate(%q, %d) = %q, want %q", tc.name, tc.s, tc.max, got, tc.want)
		}
		if !utf8.ValidString(got) {
			t.Errorf("%s: invalid UTF-8 %q", tc.name, got)
		}
	}
}

func TestCaseConversions(t *testing.T) {
	tests := []struct {
		in                          string
		camel, pascal, snake, kebab string
	}{
		{"hello world", "helloWorld", "HelloWorld", "hello_world", "hello-world"},
		{"hello_world", "helloWorld", "HelloWorld", "hello_world", "hello-world"},
		{"_leading_sep", "leadingSep", "LeadingSep", "leading_sep", "leading-sep"},
		{"  spaced  out  ", "spacedOut", "SpacedOut", "spaced_out", "spaced-out"},
		{"HTTP Server 2", "httpServer2", "HttpServer2", "http_server_2", "http-server-2"},
		{"", "", "", "", ""},
		{"---", "", "", "", ""},
	}
	for _, tc := range tests {
		if got := CamelCase(tc.in); got != tc.camel {
			t.Errorf("CamelCase(%q) = %q, want %q", tc.in, got, tc.camel)
		}
		if got := PascalCase(tc.in); got != tc.pascal {
			t.Errorf("PascalCase(%q) = %q, want %q", tc.in, got, tc.pascal)
		}
		if got := SnakeCase(tc.in); got != tc.snake {
			t.Errorf("SnakeCase(%q) = %q, want %q", tc.in, got, tc.snake)
		}
		if got := KebabCase(tc.in); got != tc.kebab {
			t.Errorf("KebabCase(%q) = %q, want %q", tc.in, got, tc.kebab)
		}
	}
	if SnakeCase("userID") != "user_id" || KebabCase("userID") != "user-id" {
		t.Errorf("camel boundaries: %q %q", SnakeCase("userID"), KebabCase("userID"))
	}
	if got := Slugify("My Blog Post: Part 2!"); got != "my-blog-post-part-2" {
		t.Errorf("Slugify = %q", got)
	}
}

func TestRandomString(t *testing.T) {
	for _, n := range []int{-1, 0} {
		if got := RandomString(n); got != "" {
			t.Errorf("RandomString(%d) = %q", n, got)
		}
	}
	a, b := RandomString(32), RandomString(32)
	if len(a) != 32 || len(b) != 32 {
		t.Fatalf("lengths %d %d", len(a), len(b))
	}
	if a == b {
		t.Error("two 32-char random strings are equal")
	}
	for _, r := range a + b {
		if !strings.ContainsRune(string(letters), r) {
			t.Fatalf("character %q outside the alphabet", r)
		}
	}
}

func TestTemplate(t *testing.T) {
	if got := Template("Hi {{name}}, {{name}}! {{missing}}", map[string]string{"name": "Ana"}); got != "Hi Ana, Ana! {{missing}}" {
		t.Errorf("Template = %q", got)
	}
	if got := Template("plain", nil); got != "plain" {
		t.Errorf("Template nil vars = %q", got)
	}
	// A substituted value is never expanded again, whatever the map order.
	vars := map[string]string{"a": "{{b}}", "b": "secret"}
	for range 50 {
		if got := Template("{{a}}|{{b}}", vars); got != "{{b}}|secret" {
			t.Fatalf("Template re-expanded a value: %q", got)
		}
	}
}

func TestIndentDedent(t *testing.T) {
	if got := Indent("a\n\n  b", "> "); got != "> a\n\n>   b" {
		t.Errorf("Indent = %q", got)
	}
	if got := Dedent("    a\n      b\n\n    c"); got != "a\n  b\n\nc" {
		t.Errorf("Dedent = %q", got)
	}
	if got := Dedent("a\n  b"); got != "a\n  b" {
		t.Errorf("Dedent without common indent = %q", got)
	}
	if got := Dedent(""); got != "" {
		t.Errorf("Dedent empty = %q", got)
	}
	if got := Dedent("\t\tx\n\ty"); got != "\tx\ny" {
		t.Errorf("Dedent tabs = %q", got)
	}
}
