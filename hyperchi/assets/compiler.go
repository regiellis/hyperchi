// The compiler copies CSS and JavaScript into OutputDir, minifying with
// github.com/tdewolff/minify when minification is on and rewriting
// "@tailwind" directives into an import of the bundled Tailwind CSS. It does
// not resolve imports, transpile, or write source maps, so Sass (.scss,
// .sass) and TypeScript (.ts) sources are rejected with an error rather than
// copied out uncompiled. Compile them with their own tools (sass, tsc, or
// Vite) and hand the compiler the resulting .css or .js.
//
//	compiler := NewCompiler(&Config{OutputDir: "static", EnableMinify: true})
//	err := compiler.CompileCSS("assets/css/app.css") // writes static/css/app.min.css

package assets

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tdewolff/minify/v2"
	"github.com/tdewolff/minify/v2/css"
	"github.com/tdewolff/minify/v2/js"
)

const (
	cssMediaType = "text/css"
	jsMediaType  = "text/javascript"
)

// Compiler copies and optionally minifies CSS and JavaScript files.
type Compiler struct {
	config *Config
	min    *minify.M
}

// NewCompiler returns a Compiler with CSS and JS minifiers registered.
func NewCompiler(config *Config) *Compiler {
	m := minify.New()
	m.AddFunc(cssMediaType, css.Minify)
	m.AddFunc(jsMediaType, js.Minify)

	return &Compiler{
		config: config,
		min:    m,
	}
}

// CompileCSS writes inputPath to OutputDir/css/<name>.css (or <name>.min.css
// when minifying), rewriting "@tailwind" directives when EnableTailwind is set.
// A .scss or .sass file is an error: there is no Sass compiler behind it.
func (c *Compiler) CompileCSS(inputPath string) error {
	if err := rejectUncompiled(inputPath, ".scss", ".sass"); err != nil {
		return err
	}
	content, err := os.ReadFile(inputPath)
	if err != nil {
		return fmt.Errorf("failed to read CSS file %s: %w", inputPath, err)
	}

	processed := c.processCSS(string(content))

	outputPath := c.getOutputPath(inputPath, "css")

	if c.config.minify() {
		minified, err := c.minifyCSS(processed)
		if err != nil {
			return fmt.Errorf("failed to minify CSS file %s: %w", inputPath, err)
		}
		processed = minified
	}

	if err := c.writeFile(outputPath, processed); err != nil {
		return fmt.Errorf("failed to write CSS file %s: %w", outputPath, err)
	}

	return nil
}

// CompileJS writes inputPath to OutputDir/js/<name>.js (or <name>.min.js when
// minifying). A .ts file is an error: there is no TypeScript compiler behind
// it.
func (c *Compiler) CompileJS(inputPath string) error {
	if err := rejectUncompiled(inputPath, ".ts"); err != nil {
		return err
	}
	content, err := os.ReadFile(inputPath)
	if err != nil {
		return fmt.Errorf("failed to read JS file %s: %w", inputPath, err)
	}

	processed := c.processJS(string(content))

	outputPath := c.getOutputPath(inputPath, "js")

	if c.config.minify() {
		minified, err := c.minifyJS(processed)
		if err != nil {
			return fmt.Errorf("failed to minify JS file %s: %w", inputPath, err)
		}
		processed = minified
	}

	if err := c.writeFile(outputPath, processed); err != nil {
		return fmt.Errorf("failed to write JS file %s: %w", outputPath, err)
	}

	return nil
}

// rejectUncompiled returns an error naming inputPath when its extension is
// one of exts: a source language this package has no compiler for, whose
// copy would reach browsers as uncompiled source under a .css or .js name.
func rejectUncompiled(inputPath string, exts ...string) error {
	ext := strings.ToLower(filepath.Ext(inputPath))
	for _, e := range exts {
		if ext == e {
			return fmt.Errorf("cannot compile %s: %s files need their own compiler (sass, tsc, or Vite); pass the compiled .css or .js output instead", inputPath, ext)
		}
	}
	return nil
}

// processCSS rewrites Tailwind directives. Comment and whitespace stripping is
// left to the minifier (see minifyCSS).
func (c *Compiler) processCSS(content string) string {
	processed := content

	if c.config.EnableTailwind && strings.Contains(processed, "@tailwind") {
		processed = c.processTailwindDirectives(processed)
	}

	return processed
}

// processJS returns content unchanged. Comment stripping and minification
// are left to the minifier (see minifyJS).
func (c *Compiler) processJS(content string) string {
	return content
}

// processTailwindDirectives replaces "@tailwind base;" with an import of the
// bundled Tailwind stylesheet. The components and utilities directives become
// comments, since that stylesheet already contains them.
func (c *Compiler) processTailwindDirectives(content string) string {
	replacements := map[string]string{
		"@tailwind base;":       "@import '/static/css/tailwind.min.css';",
		"@tailwind components;": "/* Tailwind components included in main bundle */",
		"@tailwind utilities;":  "/* Tailwind utilities included in main bundle */",
	}

	processed := content
	for old, new := range replacements {
		processed = strings.ReplaceAll(processed, old, new)
	}

	return processed
}

// minifyCSS minifies CSS with a parser-based minifier, which preserves string
// literals, url(), and calc() while stripping comments and whitespace.
func (c *Compiler) minifyCSS(content string) (string, error) {
	return c.min.String(cssMediaType, content)
}

// minifyJS minifies JavaScript with a parser-based minifier, which understands
// string literals, automatic semicolon insertion, and comments.
func (c *Compiler) minifyJS(content string) (string, error) {
	return c.min.String(jsMediaType, content)
}

// getOutputPath returns OutputDir/<fileType>/<name>.<fileType>, with ".min"
// before the extension when minifying.
func (c *Compiler) getOutputPath(inputPath, fileType string) string {
	filename := filepath.Base(inputPath)
	ext := filepath.Ext(filename)
	name := strings.TrimSuffix(filename, ext)

	if c.config.minify() {
		name += ".min"
	}

	outputFilename := name + "." + fileType
	return filepath.Join(c.config.OutputDir, fileType, outputFilename)
}

// writeFile writes content to path, creating its directory.
func (c *Compiler) writeFile(path, content string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write file %s: %w", path, err)
	}

	return nil
}

// GetCompiledPath returns the file path CompileCSS or CompileJS writes for
// originalPath.
func (c *Compiler) GetCompiledPath(originalPath, fileType string) string {
	return c.getOutputPath(originalPath, fileType)
}
