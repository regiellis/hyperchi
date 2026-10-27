# Markdown renderer

An optional module that renders Markdown files with front matter for HyperChi
templates. The package name is `markdown`.

```go
md := markdown.New()
doc, err := md.Parse(source) // source is a []byte, often from an embed.FS
if err != nil {
    return err
}
app.Get("/guide", func(c *hyperchi.Context) error {
    return c.View("guide.tmpl", hyperchi.H{
        "title": doc.Meta["title"],
        "body":  doc.HTML, // template.HTML, safe to print with {{.body}}
    })
})
```

Import `github.com/regiellis/hyperchi/modules/markdown`. It wraps
[goldmark](https://github.com/yuin/goldmark) **v1.8.6** (the latest release on
the Go module proxy, checked 2026-10-05) with:

- GitHub Flavored Markdown: tables, task lists, strikethrough, and autolinks.
- Heading IDs. Every heading gets one generated from its text
  (`## Read inputs` becomes `id="read-inputs"`); write `## Overview {#overview}`
  to pin an ID that must not change when the wording does.
- Front matter: an optional block that opens and closes with `---` lines and
  holds flat `key: value` pairs. Values may be double-quoted (Go string syntax)
  or single-quoted. Nested values, lists, and duplicate keys are errors, so a
  file never parses to something other than what it says.

A `Document` carries the front matter (`Meta`), the body without front matter
(`Raw`), the rendered `HTML`, every heading with its ID (`Headings`), and the
body's plain text split at each heading (`Sections`, or `Text()` for all of it).
`Raw` suits endpoints that serve Markdown to people and LLMs; `Sections` suits a
search index that links results to `page#heading-id`.

Rendering stays in goldmark's safe mode: raw HTML in the source is omitted and
`javascript:` links are dropped. Markdown text and code are escaped. Do not
mark the output as trusted in some other way and then add unescaped input.

`Renderer` is safe for concurrent use. It also implements HyperChi's
integration renderer, so it can be registered and used through `c.RenderWith`
with a Markdown string, a byte slice, or a parsed `*Document`:

```go
helpers.Must(app.Integrations().Register("markdown", markdown.New()))
app.Get("/note", func(c *hyperchi.Context) error {
    return c.RenderWith("markdown", "# Notes\n\nRendered on request.")
})
```

Parse files once at startup when they are embedded; `RenderWith` parses on every
request.

Run `go test -race ./...` in this directory. The showcase uses a local replace
for its documentation pages. Modules must be released with their own
module-path version tags before consumers can fetch those versions.
