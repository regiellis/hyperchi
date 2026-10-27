package main

import (
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"net/url"
	"path"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/regiellis/hyperchi/hyperchi"
	"github.com/regiellis/hyperchi/modules/markdown"
)

// The documentation is Markdown in docs/, embedded so the binary needs no
// content directory at runtime. Each file's front matter holds title, section,
// order, next, an optional nextLabel, and description (the lede, also used as
// its llms.txt note).
//
//go:embed docs/*.md
var docsFiles embed.FS

// docSlot is content generated from Go and placed where a Markdown file has
// a "<!-- slot:name -->" line: the filter recipes and reference, which read
// the live filter registry, and the directives counter demo. Each slot gives
// its page HTML, its Markdown for the .md and llms endpoints, and its search
// text.
type docSlot struct {
	HTML     template.HTML
	Markdown string
	Text     string
}

// docSections is the sidebar's group order. Every page names one in its
// front matter; pages within a group follow their order.
var docSections = []string{"Learn", "Core", "Built-ins", "Production", "Integrations", "Reference"}

// docSection is one sidebar group.
type docSection struct {
	Name  string
	Pages []*docPage
}

// docPage is one rendered documentation topic.
type docPage struct {
	Slug, Title, Section, Path, MarkdownPath string
	Description                              string        // Markdown, for llms.txt notes
	Lede                                     template.HTML // the description rendered inline
	Order                                    int
	NextPath, NextLabel                      string
	Body                                     template.HTML
	Markdown                                 string             // the page as served at MarkdownPath
	TOC                                      []markdown.Heading // the page's ## headings, for "On this page"
	sections                                 []markdown.Section
	ledeText                                 string
}

// docSet is the loaded documentation plus its search index.
type docSet struct {
	pages    []*docPage // reading order
	sections []docSection
	bySlug   map[string]*docPage
	index    []searchEntry
}

var slotLine = regexp.MustCompile(`(?m)^<!-- slot:([a-z0-9-]+) -->$`)

const slotToken = "hyperchi-docs-slot-"

// loadDocs parses every docs/*.md file once at startup. A missing field, a
// broken next link, or an unknown slot is an error, so a bad edit fails the
// build's tests and the server's boot rather than a visitor's request.
func loadDocs(files fs.FS, slots map[string]docSlot) (*docSet, error) {
	names, err := fs.Glob(files, "docs/*.md")
	if err != nil {
		return nil, err
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("docs: no Markdown files embedded")
	}
	md := markdown.New()
	set := &docSet{bySlug: make(map[string]*docPage, len(names))}
	orders := map[int]string{}
	next := map[*docPage]string{}
	for _, name := range names {
		src, err := fs.ReadFile(files, name)
		if err != nil {
			return nil, err
		}
		slug := strings.TrimSuffix(path.Base(name), ".md")
		page, nextSlug, err := parseDocPage(md, slug, src, slots)
		if err != nil {
			return nil, fmt.Errorf("docs/%s.md: %w", slug, err)
		}
		if other, dup := orders[page.Order]; dup {
			return nil, fmt.Errorf("docs/%s.md: order %d already used by %s", slug, page.Order, other)
		}
		orders[page.Order] = slug
		set.pages = append(set.pages, page)
		set.bySlug[slug] = page
		next[page] = nextSlug
	}
	sort.Slice(set.pages, func(i, j int) bool { return set.pages[i].Order < set.pages[j].Order })
	for page, slug := range next {
		target, ok := set.bySlug[slug]
		if !ok {
			return nil, fmt.Errorf("docs/%s.md: next page %q does not exist", page.Slug, slug)
		}
		page.NextPath = target.Path
		if page.NextLabel == "" {
			page.NextLabel = target.Title
		}
	}
	for _, name := range docSections {
		section := docSection{Name: name}
		for _, page := range set.pages {
			if page.Section == name {
				section.Pages = append(section.Pages, page)
			}
		}
		if len(section.Pages) > 0 {
			set.sections = append(set.sections, section)
		}
	}
	set.index = buildSearchIndex(set.pages)
	return set, nil
}

func parseDocPage(md *markdown.Renderer, slug string, src []byte, slots map[string]docSlot) (*docPage, string, error) {
	used := map[string]bool{}
	var unknown string
	tokenized := slotLine.ReplaceAllFunc(src, func(line []byte) []byte {
		name := string(slotLine.FindSubmatch(line)[1])
		if _, ok := slots[name]; !ok && unknown == "" {
			unknown = name
		}
		used[name] = true
		return []byte(slotToken + name)
	})
	if unknown != "" {
		return nil, "", fmt.Errorf("unknown slot %q", unknown)
	}
	doc, err := md.Parse(tokenized)
	if err != nil {
		return nil, "", err
	}
	page := &docPage{
		Slug:         slug,
		Title:        doc.Meta["title"],
		Section:      doc.Meta["section"],
		Description:  doc.Meta["description"],
		NextLabel:    doc.Meta["nextLabel"],
		Path:         "/docs/" + slug,
		MarkdownPath: "/docs/" + slug + ".md",
	}
	if slug == "start" {
		page.Path = "/docs"
	}
	for _, field := range []string{"title", "section", "description", "order", "next"} {
		if doc.Meta[field] == "" {
			return nil, "", fmt.Errorf("front matter needs %q", field)
		}
	}
	if !slices.Contains(docSections, page.Section) {
		return nil, "", fmt.Errorf("section %q is not one of %v", page.Section, docSections)
	}
	// The sidebar filter quotes the title inside a script expression.
	if strings.ContainsAny(page.Title, `'"\`) {
		return nil, "", fmt.Errorf("title %q must not contain quotes or backslashes", page.Title)
	}
	if page.Order, err = strconv.Atoi(doc.Meta["order"]); err != nil {
		return nil, "", fmt.Errorf("order: %w", err)
	}
	lede, err := md.Parse([]byte(page.Description))
	if err != nil {
		return nil, "", fmt.Errorf("description: %w", err)
	}
	inline := strings.TrimSuffix(strings.TrimPrefix(string(lede.HTML), "<p>"), "</p>\n")
	page.Lede = template.HTML(inline) // goldmark safe-mode output
	page.ledeText = lede.Text()

	body := string(doc.HTML)
	raw := doc.Raw
	page.sections = doc.Sections
	for _, section := range doc.Sections {
		if section.Heading.Level == 2 && section.Heading.ID != "" {
			page.TOC = append(page.TOC, section.Heading)
		}
	}
	for name := range used {
		slot, token := slots[name], slotToken+name
		if n := strings.Count(body, "<p>"+token+"</p>\n"); n != 1 {
			return nil, "", fmt.Errorf("slot %q must stand alone in its own paragraph once, found %d", name, n)
		}
		body = strings.Replace(body, "<p>"+token+"</p>\n", string(slot.HTML), 1)
		if replacement := strings.TrimSpace(slot.Markdown); replacement != "" {
			raw = strings.Replace(raw, token, replacement, 1)
		} else {
			raw = strings.Replace(raw, token+"\n\n", "", 1) // a page-only slot leaves no gap
		}
		for i := range page.sections {
			page.sections[i].Text = strings.Replace(page.sections[i].Text, token, slot.Text, 1)
		}
	}
	page.Body = withCopyButtons(body)
	page.Markdown = "# " + page.Title + "\n\n" + page.Description + "\n\n" + strings.TrimSpace(raw) + "\n"
	return page, doc.Meta["next"], nil
}

// withCopyButtons gives every code block a copy button. Code text inside the
// HTML is escaped, so a literal "<pre>" only appears as real markup.
func withCopyButtons(body string) template.HTML {
	body = strings.ReplaceAll(body, "<pre>",
		`<div class="code-block"><button type="button" class="copy-code" aria-label="Copy code" hc-on:click="copyCode($event)">Copy</button><pre>`)
	body = strings.ReplaceAll(body, "</pre>", "</pre></div>")
	return template.HTML(body) // goldmark safe-mode output and Go-generated slots
}

// absoluteURL joins the configured public origin and a path. With no public
// origin (local development) it returns the path unchanged.
func absoluteURL(origin, p string) string {
	return origin + p
}

// assistantLinks builds chat links that ask an assistant to read the page's
// Markdown. The prompt is a query value, so it is fully percent-encoded.
func assistantLinks(origin string, page *docPage) (claude, chatgpt string) {
	prompt := "Read " + absoluteURL(origin, page.MarkdownPath) +
		" (the HyperChi documentation page \"" + page.Title + "\") and help me build with HyperChi, the htmx framework for Go."
	q := strings.ReplaceAll(url.QueryEscape(prompt), "+", "%20")
	return "https://claude.ai/new?q=" + q, "https://chatgpt.com/?q=" + q
}

const llmsSummary = "HyperChi is an htmx framework for Go. It bundles chi routing, context handlers, Go templates with more than 100 filters, htmx request and response helpers, SSE and WebSocket hubs, and a pure-Go SQLite data layer."

// llmsIndex renders /llms.txt in the llmstxt.org format: an H1, a blockquote
// summary, notes without headings, then H2 sections of Markdown links.
func (d *docSet) llmsIndex(origin string) string {
	var b strings.Builder
	b.WriteString("# HyperChi\n\n> " + llmsSummary + "\n\n")
	b.WriteString("Every route is a `func(c *hyperchi.Context) error` that answers with `c.View`, `c.Fragment`, `c.JSON`, or another response method, and returns errors such as `hyperchi.NotFound`. ")
	b.WriteString("Install the library with `go get github.com/regiellis/hyperchi` and the CLI with `go install github.com/regiellis/hyperchi/cmd/hyperchi@latest`. ")
	b.WriteString("Each link below is the Markdown source of one documentation page, in reading order.\n\n## Docs\n\n")
	for _, p := range d.pages {
		fmt.Fprintf(&b, "- [%s](%s): %s\n", p.Title, absoluteURL(origin, p.MarkdownPath), p.Description)
	}
	b.WriteString("\n## Optional\n\n")
	fmt.Fprintf(&b, "- [All documentation in one file](%s): every page above, concatenated in reading order.\n", absoluteURL(origin, "/llms-full.txt"))
	b.WriteString("- [Go API reference](https://pkg.go.dev/github.com/regiellis/hyperchi/hyperchi): generated reference for every exported identifier.\n")
	b.WriteString("- [Source code](https://github.com/regiellis/hyperchi): the framework, the CLI, the optional modules, and this site.\n")
	return b.String()
}

// llmsFull renders /llms-full.txt: every page's Markdown in reading order.
func (d *docSet) llmsFull(origin string) string {
	var b strings.Builder
	b.WriteString("# HyperChi documentation\n\n> " + llmsSummary + "\n\n")
	for _, p := range d.pages {
		fmt.Fprintf(&b, "---\n\nSource: %s\n\n%s\n", absoluteURL(origin, p.Path), p.Markdown)
	}
	return b.String()
}

// setupDocs registers the documentation pages, their Markdown sources,
// llms.txt, llms-full.txt, and search. publicOrigin is the validated
// PUBLIC_ORIGIN, or "" for relative links in local development.
func setupDocs(app *hyperchi.HyperChi, publicOrigin string) error {
	slots, err := docSlots()
	if err != nil {
		return err
	}
	docs, err := loadDocs(docsFiles, slots)
	if err != nil {
		return err
	}
	for _, page := range docs.pages {
		claude, chatgpt := assistantLinks(publicOrigin, page)
		app.Get(page.Path, func(c *hyperchi.Context) error {
			return c.View("docs.tmpl", hyperchi.H{
				"title":   page.Title + " | HyperChi",
				"current": "docs", "topic": page.Slug, "heading": page.Title,
				"page": page, "sections": docs.sections, "claudeURL": claude, "chatgptURL": chatgpt,
			})
		})
		markdownSource := func(c *hyperchi.Context) error {
			return c.Blob("text/markdown; charset=utf-8", []byte(page.Markdown))
		}
		app.Get(page.MarkdownPath, markdownSource)
		if page.Slug == "start" {
			// llmstxt.org maps a path without a file name to index.md.
			app.Get("/docs/index.md", markdownSource)
		}
	}
	index := docs.llmsIndex(publicOrigin)
	full := docs.llmsFull(publicOrigin)
	app.Get("/llms.txt", func(c *hyperchi.Context) error { return c.Text(index) })
	app.Get("/llms-full.txt", func(c *hyperchi.Context) error { return c.Text(full) })

	app.Get("/docs/search", func(c *hyperchi.Context) error {
		query := boundQuery(c.Query("q"))
		data := hyperchi.H{"query": query, "results": docs.search(query), "sections": docs.sections}
		if c.IsHTMX() && !c.HX().Boosted() {
			if query == "" {
				return c.HTML("") // an empty dropdown collapses
			}
			return c.Fragment("docs-search-results", data)
		}
		data["title"] = "Search the documentation | HyperChi"
		data["current"], data["topic"], data["heading"] = "docs", "search", "Search"
		return c.Page("docs.tmpl", data)
	})
	return nil
}
