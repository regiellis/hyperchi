package markdown

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

const sample = `---
title: Routing & responses
order: 2
next: "helpers"
quote: 'it''s fine'
# a comment
---

Intro with ` + "`code`" + ` and a [link](/docs/helpers).

## Pages and fragments {#overview}

| Method | Renders |
| --- | --- |
| c.View | page or fragment |

### Read inputs

` + "```go" + `
app.Get("/", home)
` + "```" + `

## Pages and fragments
`

func TestParseFrontMatterAndBody(t *testing.T) {
	doc, err := New().Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"title": "Routing & responses", "order": "2", "next": "helpers", "quote": "it's fine"}
	if len(doc.Meta) != len(want) {
		t.Fatalf("meta = %v", doc.Meta)
	}
	for k, v := range want {
		if doc.Meta[k] != v {
			t.Errorf("meta[%s] = %q, want %q", k, doc.Meta[k], v)
		}
	}
	if strings.Contains(doc.Raw, "title:") || !strings.HasPrefix(doc.Raw, "Intro with") {
		t.Errorf("raw keeps front matter or lost the body: %q", doc.Raw)
	}
	if !strings.Contains(doc.Raw, "## Pages and fragments {#overview}") {
		t.Error("raw Markdown changed")
	}
}

func TestGFMTable(t *testing.T) {
	doc, err := New().Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"<table>", "<th>Method</th>", "<td>c.View</td>", "<td>page or fragment</td>"} {
		if !strings.Contains(string(doc.HTML), want) {
			t.Errorf("HTML missing %q:\n%s", want, doc.HTML)
		}
	}
}

func TestHeadingIDs(t *testing.T) {
	doc, err := New().Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	want := []Heading{
		{2, "overview", "Pages and fragments"},
		{3, "read-inputs", "Read inputs"},
		{2, "pages-and-fragments", "Pages and fragments"},
	}
	if len(doc.Headings) != len(want) {
		t.Fatalf("headings = %+v", doc.Headings)
	}
	for i, h := range want {
		if doc.Headings[i] != h {
			t.Errorf("heading %d = %+v, want %+v", i, doc.Headings[i], h)
		}
	}
	for _, id := range []string{`<h2 id="overview">`, `<h3 id="read-inputs">`, `<h2 id="pages-and-fragments">`} {
		if !strings.Contains(string(doc.HTML), id) {
			t.Errorf("HTML missing %s", id)
		}
	}
}

func TestTextSections(t *testing.T) {
	doc, err := New().Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Sections) != 4 {
		t.Fatalf("sections = %+v", doc.Sections)
	}
	if doc.Sections[0].Heading.Level != 0 || doc.Sections[0].Text != "Intro with code and a link." {
		t.Errorf("intro section = %+v", doc.Sections[0])
	}
	if !strings.Contains(doc.Sections[1].Text, "c.View") || !strings.Contains(doc.Sections[1].Text, "page or fragment") {
		t.Errorf("table text missing: %q", doc.Sections[1].Text)
	}
	if doc.Sections[2].Text != `app.Get("/", home)` {
		t.Errorf("code text = %q", doc.Sections[2].Text)
	}
	text := doc.Text()
	for _, want := range []string{"Intro with code", "Pages and fragments", "Read inputs", `app.Get("/", home)`} {
		if !strings.Contains(text, want) {
			t.Errorf("Text() missing %q", want)
		}
	}
	for _, unwanted := range []string{"<", "{#overview}", "](", "```", "title:"} {
		if strings.Contains(text, unwanted) {
			t.Errorf("Text() contains markup %q:\n%s", unwanted, text)
		}
	}
}

func TestUnsafeMarkupIsNotRendered(t *testing.T) {
	src := "<script>alert(1)</script>\n\nHi <b onclick=x>there</b> [x](javascript:alert(1)) `<i>`\n"
	doc, err := New().Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	html := string(doc.HTML)
	for _, bad := range []string{"<script", "onclick", "javascript:", "<i>"} {
		if strings.Contains(html, bad) {
			t.Errorf("unsafe output %q in %s", bad, html)
		}
	}
	if !strings.Contains(html, "&lt;i&gt;") {
		t.Errorf("code span not escaped: %s", html)
	}
	if strings.Contains(doc.Text(), "alert(1)</script>") {
		t.Errorf("raw HTML leaked into text: %q", doc.Text())
	}
}

func TestFrontMatterErrors(t *testing.T) {
	for name, src := range map[string]string{
		"unclosed":  "---\ntitle: x\n",
		"no colon":  "---\ntitle\n---\n",
		"duplicate": "---\na: 1\na: 2\n---\n",
		"nested":    "---\na:\n  b: 1\n---\n",
		"list":      "---\ntags: [a, b]\n---\n",
		"bad quote": "---\na: \"open\n---\n",
	} {
		if _, _, err := ParseFrontMatter([]byte(src)); err == nil {
			t.Errorf("%s: accepted %q", name, src)
		}
	}
	meta, body, err := ParseFrontMatter([]byte("\ufeffNo front matter.\r\n"))
	if err != nil || len(meta) != 0 || string(body) != "No front matter.\n" {
		t.Fatalf("plain document: %v %q %v", meta, body, err)
	}
	meta, body, err = ParseFrontMatter([]byte("---\r\ntitle: CRLF\r\n---\r\nBody\r\n"))
	if err != nil || meta["title"] != "CRLF" || string(body) != "Body\n" {
		t.Fatalf("CRLF document: %v %q %v", meta, body, err)
	}
}

func TestRenderIntegration(t *testing.T) {
	r := New()
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := r.Render(context.Background(), &out, "# Hi"); err != nil || out.String() != "<h1 id=\"hi\">Hi</h1>\n" {
		t.Fatalf("string render = %q, %v", out.String(), err)
	}
	doc, err := r.Parse([]byte("*a*"))
	if err != nil {
		t.Fatal(err)
	}
	for _, data := range []any{[]byte("*a*"), doc} {
		out.Reset()
		if err := r.Render(context.Background(), &out, data); err != nil || out.String() != "<p><em>a</em></p>\n" {
			t.Fatalf("render %T = %q, %v", data, out.String(), err)
		}
	}
	var nilDoc *Document
	for _, data := range []any{42, nilDoc, "---\nbroken"} {
		if err := r.Render(context.Background(), &out, data); err == nil {
			t.Errorf("accepted %T %v", data, data)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := r.Render(ctx, &out, "x"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled render: %v", err)
	}
	var zero *Renderer
	if err := zero.Validate(); err == nil {
		t.Fatal("nil renderer validated")
	}
	if _, err := (&Renderer{}).Parse([]byte("x")); err == nil {
		t.Fatal("zero renderer parsed")
	}
}

func TestConcurrentParse(t *testing.T) {
	r := New()
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if _, err := r.Parse([]byte(sample)); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
}
