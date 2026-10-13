package assets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These tests pin the minifier on inputs a naive one corrupts: "//" inside
// string literals, statements that need a separator for automatic semicolon
// insertion, and CSS strings, url(), and calc().

func newMinCompiler() *Compiler {
	return NewCompiler(&Config{EnableMinify: true})
}

func TestMinifyJSPreservesURLInsideString(t *testing.T) {
	c := newMinCompiler()
	out, err := c.minifyJS(`const u = "https://example.com/a//b"; console.log(u);`)
	if err != nil {
		t.Fatalf("minifyJS: %v", err)
	}
	if !strings.Contains(out, "https://example.com/a//b") {
		t.Fatalf("URL inside a string literal was corrupted: %q", out)
	}
}

func TestMinifyJSDoesNotJoinStatements(t *testing.T) {
	c := newMinCompiler()
	out, err := c.minifyJS("let a = 1\nlet b = 2\n")
	if err != nil {
		t.Fatalf("minifyJS: %v", err)
	}
	// "let a=1let b=2" would be a syntax error: the statements need a
	// separator.
	if strings.Contains(out, "1let") {
		t.Fatalf("statements joined without a separator (ASI broken): %q", out)
	}
}

func TestMinifyJSStripsComments(t *testing.T) {
	c := newMinCompiler()
	out, err := c.minifyJS("// line comment\nconst x = 1;\n/* block comment */\n")
	if err != nil {
		t.Fatalf("minifyJS: %v", err)
	}
	if strings.Contains(out, "comment") {
		t.Fatalf("comments were not stripped: %q", out)
	}
}

func TestMinifyJSInvalidReturnsError(t *testing.T) {
	c := newMinCompiler()
	if _, err := c.minifyJS("const = = =;"); err == nil {
		t.Fatal("expected an error for invalid JS, got nil (broken assets would ship silently)")
	}
}

func TestMinifyCSSPreservesURLAndCalc(t *testing.T) {
	c := newMinCompiler()
	out, err := c.minifyCSS(`.box { background: url("https://example.com/i.png"); width: calc(100% - 20px); }`)
	if err != nil {
		t.Fatalf("minifyCSS: %v", err)
	}
	if !strings.Contains(out, "https://example.com/i.png") {
		t.Fatalf("url() content corrupted: %q", out)
	}
	if !strings.Contains(out, "calc(") || !strings.Contains(out, "100%") {
		t.Fatalf("calc() corrupted: %q", out)
	}
}

func TestCompileJSWritesMinifiedFile(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "app.js")
	if err := os.WriteFile(in, []byte("function add(a, b) {\n    return a + b; // sum\n}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	c := NewCompiler(&Config{EnableMinify: true, OutputDir: dir})
	if err := c.CompileJS(in); err != nil {
		t.Fatalf("CompileJS: %v", err)
	}

	out, err := os.ReadFile(c.GetCompiledPath(in, "js"))
	if err != nil {
		t.Fatalf("read compiled output: %v", err)
	}
	got := string(out)
	if strings.Contains(got, "sum") {
		t.Fatalf("comment survived minification: %q", got)
	}
	if !strings.Contains(got, "function add") {
		t.Fatalf("function body lost during minification: %q", got)
	}
}

// EnableMinify writes a .min file with the minified CSS.
func TestEnableMinifyWritesMinifiedCSS(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "site.css")
	if err := os.WriteFile(in, []byte("/* note */\nbody {\n  margin: 0;\n}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	c := NewCompiler(&Config{EnableMinify: true, OutputDir: dir})
	if err := c.CompileCSS(in); err != nil {
		t.Fatal(err)
	}
	out := c.GetCompiledPath(in, "css")
	if filepath.Base(out) != "site.min.css" {
		t.Errorf("output = %s, want site.min.css", out)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "body{margin:0}" {
		t.Errorf("minified CSS = %q", got)
	}
}

func TestCompileCSSTailwindAndErrors(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "app.css")
	src := "@tailwind base;\n@tailwind components;\n@tailwind utilities;\n.a{color:red}"
	if err := os.WriteFile(in, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}

	plain := NewCompiler(&Config{OutputDir: dir})
	if err := plain.CompileCSS(in); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(dir, "css", "app.css")); err != nil || string(got) != src {
		t.Errorf("without Tailwind the CSS must pass through: %q, %v", got, err)
	}

	tw := NewCompiler(&Config{OutputDir: dir, EnableTailwind: true})
	if err := tw.CompileCSS(in); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "css", "app.css"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "@tailwind") || !strings.Contains(string(got), "@import '/static/css/tailwind.min.css';") {
		t.Errorf("Tailwind directives not rewritten: %q", got)
	}

	if err := plain.CompileCSS(filepath.Join(dir, "missing.css")); err == nil {
		t.Error("missing CSS accepted")
	}
	if err := plain.CompileJS(filepath.Join(dir, "missing.js")); err == nil {
		t.Error("missing JS accepted")
	}
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := NewCompiler(&Config{OutputDir: blocker}).CompileCSS(in); err == nil {
		t.Error("an output directory under a file was accepted")
	}
	bad := filepath.Join(dir, "bad.js")
	if err := os.WriteFile(bad, []byte("const = = =;"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := NewCompiler(&Config{OutputDir: dir, EnableMinify: true}).CompileJS(bad); err == nil {
		t.Error("invalid JS minified without error")
	}
}

// Sass and TypeScript sources have no compiler here; copied out as .css and
// .js, browsers would receive uncompiled source. They are rejected with an
// error naming the file, and nothing is written.
func TestCompilerRejectsSourcesItCannotCompile(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, "out")
	c := NewCompiler(&Config{OutputDir: out})
	cases := map[string]func(string) error{
		"app.scss": c.CompileCSS,
		"app.sass": c.CompileCSS,
		"APP.SCSS": c.CompileCSS,
		"app.ts":   c.CompileJS,
	}
	for name, compile := range cases {
		in := filepath.Join(root, name)
		if err := os.WriteFile(in, []byte("$x: 1;\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		err := compile(in)
		if err == nil || !strings.Contains(err.Error(), in) {
			t.Errorf("compile %s: err = %v, want an error naming the file", name, err)
		}
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Errorf("rejected sources produced output (stat %s: %v)", out, err)
	}
}
