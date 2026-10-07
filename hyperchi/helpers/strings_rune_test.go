package helpers

import (
	"testing"
	"unicode/utf8"
)

// Every Str offset and width counts runes, so the helpers agree with each
// other and never split a multi-byte character.
func TestStrOffsetsCountRunes(t *testing.T) {
	tests := []struct {
		name string
		got  Str
		want string
	}{
		{"slice", S("héllo").Slice(1, 3), "él"},
		{"slice negative", S("日本語テキスト").Slice(-4, -1), "テキス"},
		{"left", S("héllo").Left(2), "hé"},
		{"left all", S("日本語").Left(3), "日本語"},
		{"right", S("日本語").Right(1), "語"},
		{"right two", S("café").Right(2), "fé"},
		{"center", S("é").Center(3, "*"), "*é*"},
		{"center multibyte fill", S("ab").Center(4, "·"), "·ab·"},
		{"pad left", S("é").PadLeft(3, "0"), "00é"},
		{"pad right", S("日本").PadRight(4, "."), "日本.."},
		{"pad uses the first rune of fill", S("7").PadLeft(3, "ab"), "aa7"},
		{"pad empty fill means space", S("7").PadRight(2, ""), "7 "},
		{"left of index", S("日本:80").Left(S("日本:80").Index(":")), "日本"},
		{"right of last index", S("ü/ö/ä").Slice(S("ü/ö/ä").LastIndex("/")+1, S("ü/ö/ä").Len()), "ä"},
	}
	for _, tc := range tests {
		if tc.got.String() != tc.want {
			t.Errorf("%s = %q, want %q", tc.name, tc.got, tc.want)
		}
		if !utf8.ValidString(tc.got.String()) {
			t.Errorf("%s produced invalid UTF-8 %q", tc.name, tc.got)
		}
	}

	if got := S("日本:80").Index(":"); got != 2 {
		t.Errorf("Index = %d, want rune offset 2", got)
	}
	if got := S("é/é/é").LastIndex("/"); got != 3 {
		t.Errorf("LastIndex = %d, want rune offset 3", got)
	}
	if S("é").Index("x") != -1 || S("é").LastIndex("x") != -1 {
		t.Error("a missing substring is -1")
	}
}

// Slugify and the case converters transliterate accented Latin letters
// instead of dropping them.
func TestSlugifyTransliteratesLatin(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Crème Brûlée à la carte", "creme-brulee-a-la-carte"},
		{"Straße", "strasse"},
		{"STRAẞE", "strasse"},
		{"Smørrebrød", "smorrebrod"},
		{"Ærø Æsir", "aero-aesir"},
		{"Œuvre", "oeuvre"},
		{"Þingvellir", "thingvellir"},
		{"Łódź Kraków", "lodz-krakow"},
		{"İstanbul ıspanak", "istanbul-ispanak"},
		{"Ångström Ĳssel", "angstrom-ijssel"},
		{"Façade naïve", "facade-naive"},
		{"Țară Șir", "tara-sir"},
		{"Nǚ lǜ", "nu-lu"},
		{"café au lait", "cafe-au-lait"}, // decomposed accent
		{"日本 test Привет", "test"},        // no transliteration: dropped
	}
	for _, tc := range tests {
		if got := Slugify(tc.in); got != tc.want {
			t.Errorf("Slugify(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if got := CamelCase("crème brûlée"); got != "cremeBrulee" {
		t.Errorf("CamelCase = %q", got)
	}
	if got := PascalCase("straße café"); got != "StrasseCafe" {
		t.Errorf("PascalCase = %q", got)
	}
	if got := SnakeCase("Øresund Brücke"); got != "oresund_brucke" {
		t.Errorf("SnakeCase = %q", got)
	}
}

// Slugify splits words only at separators. Case changes inside a word are not
// boundaries in a slug, unlike in the case converters.
func TestSlugifyIgnoresCase(t *testing.T) {
	for in, want := range map[string]string{
		"MiXeD CaSe 42":    "mixed-case-42",
		"iPhone 15 Pro":    "iphone-15-pro",
		"userID":           "userid",
		"HTTPServer Guide": "httpserver-guide",
		"ÉCOLE Française":  "ecole-francaise",
	} {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

// The case converters split acronyms at their boundary instead of
// lowercasing "userID" to "userid".
func TestCaseConvertersKeepAcronymBoundaries(t *testing.T) {
	tests := []struct {
		in                          string
		camel, pascal, snake, kebab string
	}{
		{"userID", "userId", "UserId", "user_id", "user-id"},
		{"HTTPServer", "httpServer", "HttpServer", "http_server", "http-server"},
		{"XMLHttpRequest", "xmlHttpRequest", "XmlHttpRequest", "xml_http_request", "xml-http-request"},
		{"parseURLForID", "parseUrlForId", "ParseUrlForId", "parse_url_for_id", "parse-url-for-id"},
		{"API_KEY", "apiKey", "ApiKey", "api_key", "api-key"},
		{"version2Update", "version2Update", "Version2Update", "version2_update", "version2-update"},
		{"ID", "id", "Id", "id", "id"},
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
}

// The whitespace helpers reuse one compiled regexp instead of compiling
// per call. Compiling `\s+` costs dozens of allocations, while a replace on a
// short string costs a handful.
func TestWhitespaceHelpersDoNotCompilePerCall(t *testing.T) {
	s := S(" a \t b\nc ")
	if allocs := testing.AllocsPerRun(100, func() { s.RemoveWhitespace() }); allocs > 10 {
		t.Errorf("RemoveWhitespace allocates %.0f times per call; the regexp is being recompiled", allocs)
	}
	if allocs := testing.AllocsPerRun(100, func() { s.CollapseWhitespace() }); allocs > 10 {
		t.Errorf("CollapseWhitespace allocates %.0f times per call; the regexp is being recompiled", allocs)
	}
	if allocs := testing.AllocsPerRun(100, func() { SnakeCase("helloWorldAgain") }); allocs > 20 {
		t.Errorf("SnakeCase allocates %.0f times per call; a regexp is being compiled", allocs)
	}
}
