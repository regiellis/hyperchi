package main

import (
	"context"
	"regexp"
	"strings"
	"testing"
)

func TestMatchExpr(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"   ", ""},
		{"nginx", `"nginx"*`},
		{"nginx ", `"nginx"`},
		{"nginx reload", `"nginx" "reload"*`},
		{"backu* now ", `"backu"* "now"`},
		{`"reload the proxy"`, `"reload the proxy"`},
		{`"reload the"*`, `"reload the"*`},
		{`"unclosed phrase`, `"unclosed phrase"`},
		{`nginx.conf`, `"nginx conf"*`},
		{`docker-compose up `, `"docker compose" "up"`},
		// Operators, column filters, grouping, and NEAR are plain words.
		{"a OR b ", `"a" "OR" "b"`},
		{"NOT secret ", `"NOT" "secret"`},
		{"title:nginx ", `"title nginx"`},
		{"(a AND b) ", `"a" "AND" "b"`},
		{"NEAR(a b, 3) ", `"NEAR a" "b" "3"`},
		{"^start ", `"start"`},
		{`""`, ""},
		{`"""`, ""},
		{"***", ""},
		{"-", ""},
		{`a"b"c `, `"a" "b" "c"`},
		{"café ", `"café"`},
		{"one two three four five six seven eight nine ten ", `"one" "two" "three" "four" "five" "six" "seven" "eight"`},
	}
	for _, c := range cases {
		if got := matchExpr(c.in); got != c.want {
			t.Errorf("matchExpr(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestBoundQuery(t *testing.T) {
	if got := boundQuery("  nginx   reload  "); got != "nginx reload " {
		t.Errorf("got %q", got)
	}
	long := strings.Repeat("x", maxQueryRunes+50)
	if got := boundQuery(long); len([]rune(got)) != maxQueryRunes {
		t.Errorf("long query kept %d runes", len([]rune(got)))
	}
}

// safeExpr is the only shape matchExpr may produce: quoted runs of letters,
// digits, and single spaces, each optionally followed by *, separated by
// spaces. Nothing in it can be an FTS5 operator, column filter, or group.
var safeExpr = regexp.MustCompile(`^("[\p{L}\p{N}]+( [\p{L}\p{N}]+)*"\*?( "[\p{L}\p{N}]+( [\p{L}\p{N}]+)*"\*?)*)?$`)

var hostileQueries = []string{
	`"`, `""`, `"""`, `"a`, `a"`, `*`, `**`, `a**`, `"*"`, `* a`, `-a`, `+a`, `a + b`, `^a`, `a:`,
	`:a`, `title:`, `{title path}: x`, `body:"x"`, `(`, `)`, `()`, `(a`, `a)`, `((a)`, `AND`, `OR`, `NOT`,
	`a AND`, `OR b`, `NOT NOT`, `NEAR`, `NEAR(`, `NEAR(a b`, `NEAR(a b, x)`, `a NEAR/2 b`, `'`, `''`,
	`'; DROP TABLE documents; --`, `\`, `\"`, `%`, `_`, `$`, "\x00", "\x01x\x02", "a\tb\nc",
	`<script>alert(1)</script>`, `../../etc/passwd`, `𝔘𝔫𝔦𝔠𝔬𝔡𝔢`, `日本語 検索`, `👍🏽`, `٣٤٥`,
	strings.Repeat(`"(`, 300), strings.Repeat("a ", 300), strings.Repeat("é", 500),
}

// TestMatchExprNeverInjectsSyntax checks every hostile input yields a safe
// expression, and that SQLite accepts each one: no input may cause an error.
func TestMatchExprNeverInjectsSyntax(t *testing.T) {
	s := testSite(t, map[string]string{"a.md": "# A\n\nsome text about nginx and near things"})
	for _, q := range hostileQueries {
		expr := matchExpr(q)
		if !safeExpr.MatchString(expr) {
			t.Errorf("matchExpr(%q) = %q, which is not a safe expression", q, expr)
		}
		for _, typed := range []string{q, q + " "} {
			if _, err := s.ix.Search(context.Background(), SearchParams{Query: typed}); err != nil {
				t.Errorf("Search(%q): %v", typed, err)
			}
		}
	}
}

// FuzzMatchExpr runs its seeds under go test; go test -fuzz=FuzzMatchExpr
// explores further.
func FuzzMatchExpr(f *testing.F) {
	for _, q := range hostileQueries {
		f.Add(q)
	}
	f.Fuzz(func(t *testing.T, q string) {
		if expr := matchExpr(q); !safeExpr.MatchString(expr) {
			t.Fatalf("matchExpr(%q) = %q", q, expr)
		}
	})
}
