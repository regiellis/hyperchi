// Package markdown renders Markdown documents with front matter for HyperChi
// templates. It wraps goldmark with GitHub Flavored Markdown, heading IDs, and a
// flat front matter parser, and keeps the raw Markdown and a plain-text version
// of each document for search indexes and LLM-facing endpoints.
package markdown

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html/template"
	"io"
	"regexp"
	"strconv"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
)

// Heading is one heading of a document, with the ID its HTML anchor carries.
type Heading struct {
	Level int
	ID    string
	Text  string
}

// Section is the plain text under one heading. Text before the first heading
// belongs to a section whose Heading has Level 0.
type Section struct {
	Heading Heading
	Text    string
}

// Document is a parsed Markdown file.
type Document struct {
	// Meta holds the front matter as flat string values.
	Meta map[string]string
	// Raw is the Markdown body with the front matter removed.
	Raw string
	// HTML is the rendered body. Raw HTML in the source is omitted and
	// dangerous link schemes are dropped, so it is safe to put in a template.
	HTML template.HTML
	// Headings lists every heading in document order.
	Headings []Heading
	// Sections holds the plain text of the body, split at each heading.
	Sections []Section
}

// Text returns the whole document as plain text: headings and their sections
// separated by blank lines.
func (d *Document) Text() string {
	var b strings.Builder
	for _, s := range d.Sections {
		for _, part := range []string{s.Heading.Text, s.Text} {
			if part == "" {
				continue
			}
			if b.Len() > 0 {
				b.WriteString("\n\n")
			}
			b.WriteString(part)
		}
	}
	return b.String()
}

// Renderer parses and renders Markdown. It is safe for concurrent use.
type Renderer struct{ md goldmark.Markdown }

// New returns a Renderer with GitHub Flavored Markdown (tables, task lists,
// strikethrough, autolinks), automatic heading IDs, and explicit heading IDs
// written as attributes: "## Overview {#overview}". The HTML renderer stays in
// goldmark's safe mode.
func New() *Renderer {
	return &Renderer{md: goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithParserOptions(parser.WithAutoHeadingID(), parser.WithAttribute()),
	)}
}

// Validate rejects an uninitialized Renderer when it is registered as a
// HyperChi integration.
func (r *Renderer) Validate() error {
	if r == nil || r.md == nil {
		return errors.New("markdown: renderer not initialized; use markdown.New")
	}
	return nil
}

// Parse splits off the front matter, renders the body to HTML, and extracts
// its headings and plain text.
func (r *Renderer) Parse(src []byte) (*Document, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	meta, body, err := ParseFrontMatter(src)
	if err != nil {
		return nil, err
	}
	root := r.md.Parser().Parse(text.NewReader(body))
	var html bytes.Buffer
	if err := r.md.Renderer().Render(&html, body, root); err != nil {
		return nil, fmt.Errorf("markdown: render: %w", err)
	}
	doc := &Document{Meta: meta, Raw: string(body), HTML: template.HTML(html.String())}
	if err := collect(doc, root, body); err != nil {
		return nil, err
	}
	return doc, nil
}

// Render implements HyperChi's integrations.Renderer: it writes the HTML of a
// Markdown string, byte slice, or parsed *Document. Register it with
// app.Integrations().Register and call c.RenderWith(name, source).
func (r *Renderer) Render(ctx context.Context, w io.Writer, data any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var doc *Document
	switch v := data.(type) {
	case *Document:
		if v == nil {
			return errors.New("markdown: nil document")
		}
		doc = v
	case string:
		parsed, err := r.Parse([]byte(v))
		if err != nil {
			return err
		}
		doc = parsed
	case []byte:
		parsed, err := r.Parse(v)
		if err != nil {
			return err
		}
		doc = parsed
	default:
		return fmt.Errorf("markdown: expected string, []byte, or *markdown.Document, got %T", data)
	}
	_, err := io.WriteString(w, string(doc.HTML))
	return err
}

var frontMatterKey = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// ParseFrontMatter splits a document into its front matter and body. Front
// matter is optional; when present it opens the file with a "---" line, closes
// with another, and holds flat "key: value" lines. Values may be double-quoted
// (Go string syntax) or single-quoted; blank lines and "#" comments are
// skipped. Nested YAML, lists, and duplicate keys are errors rather than
// silently misread values.
func ParseFrontMatter(src []byte) (map[string]string, []byte, error) {
	src = bytes.TrimPrefix(src, []byte("\ufeff"))
	src = bytes.ReplaceAll(src, []byte("\r\n"), []byte("\n"))
	meta := map[string]string{}
	if !bytes.HasPrefix(src, []byte("---\n")) {
		return meta, src, nil
	}
	rest := src[len("---\n"):]
	lineNo := 1
	for len(rest) > 0 {
		line, next, _ := bytes.Cut(rest, []byte("\n"))
		rest = next
		lineNo++
		if string(line) == "---" {
			return meta, bytes.TrimLeft(rest, "\n"), nil
		}
		trimmed := strings.TrimSpace(string(line))
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if line[0] == ' ' || line[0] == '\t' {
			return nil, nil, fmt.Errorf("markdown: front matter line %d: nested values are not supported", lineNo)
		}
		key, value, ok := strings.Cut(trimmed, ":")
		key = strings.TrimSpace(key)
		if !ok || !frontMatterKey.MatchString(key) {
			return nil, nil, fmt.Errorf("markdown: front matter line %d: want \"key: value\"", lineNo)
		}
		if _, dup := meta[key]; dup {
			return nil, nil, fmt.Errorf("markdown: front matter line %d: duplicate key %q", lineNo, key)
		}
		parsed, err := frontMatterValue(strings.TrimSpace(value))
		if err != nil {
			return nil, nil, fmt.Errorf("markdown: front matter line %d: %w", lineNo, err)
		}
		meta[key] = parsed
	}
	return nil, nil, errors.New("markdown: front matter has no closing \"---\" line")
}

func frontMatterValue(v string) (string, error) {
	switch {
	case strings.HasPrefix(v, `"`):
		s, err := strconv.Unquote(v)
		if err != nil {
			return "", fmt.Errorf("invalid double-quoted value %s", v)
		}
		return s, nil
	case strings.HasPrefix(v, "'"):
		if len(v) < 2 || !strings.HasSuffix(v, "'") {
			return "", fmt.Errorf("unterminated single-quoted value %s", v)
		}
		return strings.ReplaceAll(v[1:len(v)-1], "''", "'"), nil
	case strings.HasPrefix(v, "[") || strings.HasPrefix(v, "{") || v == "|" || v == ">":
		return "", fmt.Errorf("value %s needs YAML structure, which is not supported", v)
	}
	return v, nil
}

// collect walks the parsed tree for headings and per-section plain text.
func collect(doc *Document, root ast.Node, src []byte) error {
	current := Section{}
	var b strings.Builder
	flush := func() {
		current.Text = tidy(b.String())
		if current.Heading.Level > 0 || current.Text != "" {
			doc.Sections = append(doc.Sections, current)
		}
		b.Reset()
	}
	err := ast.Walk(root, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		switch node := n.(type) {
		case *ast.Heading:
			if !entering {
				return ast.WalkContinue, nil
			}
			flush()
			h := Heading{Level: node.Level, Text: tidy(inlineText(node, src))}
			if id, ok := node.AttributeString("id"); ok {
				if raw, isBytes := id.([]byte); isBytes {
					h.ID = string(raw)
				}
			}
			doc.Headings = append(doc.Headings, h)
			current = Section{Heading: h}
			return ast.WalkSkipChildren, nil
		case *ast.FencedCodeBlock, *ast.CodeBlock:
			if entering {
				writeLines(&b, n, src)
				b.WriteString("\n")
			}
			return ast.WalkSkipChildren, nil
		case *ast.HTMLBlock, *ast.RawHTML:
			return ast.WalkSkipChildren, nil
		case *ast.Text:
			if entering {
				b.Write(node.Segment.Value(src))
				if node.SoftLineBreak() || node.HardLineBreak() {
					b.WriteString(" ")
				}
			}
		case *ast.String:
			if entering {
				b.Write(node.Value)
			}
		case *ast.AutoLink:
			if entering {
				b.Write(node.Label(src))
			}
			return ast.WalkSkipChildren, nil
		default:
			if !entering && n.Type() == ast.TypeBlock {
				b.WriteString("\n")
			}
		}
		return ast.WalkContinue, nil
	})
	if err != nil {
		return fmt.Errorf("markdown: extract text: %w", err)
	}
	flush()
	return nil
}

// inlineText returns the text of an inline container such as a heading.
func inlineText(n ast.Node, src []byte) string {
	var b strings.Builder
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		switch node := c.(type) {
		case *ast.Text:
			b.Write(node.Segment.Value(src))
			if node.SoftLineBreak() || node.HardLineBreak() {
				b.WriteString(" ")
			}
		case *ast.String:
			b.Write(node.Value)
		case *ast.RawHTML:
		default:
			b.WriteString(inlineText(c, src))
		}
	}
	return b.String()
}

func writeLines(b *strings.Builder, n ast.Node, src []byte) {
	lines := n.Lines()
	for i := 0; i < lines.Len(); i++ {
		seg := lines.At(i)
		b.Write(seg.Value(src))
	}
}

// tidy trims each line and collapses runs of blank lines to one.
func tidy(s string) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	blank := false
	for _, line := range lines {
		line = strings.TrimRight(line, " \t")
		if strings.TrimSpace(line) == "" {
			if !blank && len(out) > 0 {
				out = append(out, "")
			}
			blank = true
			continue
		}
		blank = false
		out = append(out, line)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}
