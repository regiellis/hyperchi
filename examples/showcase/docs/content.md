---
title: Markdown content
section: Built-ins
order: 100
next: content-v2
description: Keep posts, guides, and pages as Markdown files. HyperChi loads each folder into a collection you can check, query, and serve.
---

## A folder per collection {#overview}

`app.Content.LoadDir("content")` treats each folder under `content/` as a collection and every `.md`, `.mdx`, `.html`, `.htm`, or `.txt` file inside it as an item. The file name is the item's slug, its front matter becomes `Metadata` (with `title` and `description` also in `Title` and `Description`), and its body is rendered to `HTML`.

```text
content/
  blog/
    hello-world.md
    shipping-v1.md
  guides/
    install.md
```

```markdown
---
title: Hello, world
date: 2026-10-01
draft: false
---

The first post.
```

Front matter is flat `key: value` lines; a nested value or a repeated key fails the file with the line named. A value becomes a bool, an int, a float, a list for `[a, b]`, or a string; quote it to keep it a string.

## Load, check, and serve {#example}

With the core `hyperchi` package, `hyperchi/content`, `hyperchi/helpers`, and the optional `modules/markdown` renderer imported:

```go
md := markdown.New()
render := func(body string) (template.HTML, error) {
    doc, err := md.Parse([]byte(body))
    if err != nil {
        return "", err
    }
    return doc.HTML, nil
}

posts := content.NewSchema("blog").
    Field("title", content.FieldTypeString).Required().MinLength(1).
    Field("date", content.FieldTypeTime).Required().
    Field("draft", content.FieldTypeBool).Default(false).
    Build().
    Build()

helpers.Must(app.Content.LoadDir("content",
    hyperchi.WithMarkdown(render),
    hyperchi.WithContentCollection(content.CollectionConfig{Name: "blog", Schema: posts}),
))

app.ContentRoutes("/blog", "blog", hyperchi.ContentRouteConfig{
    IndexTemplate:  "blog-index",
    ItemTemplate:   "blog-post",
    DefaultFilters: map[string]any{"draft": false},
    SortBy:         "date",
    SortOrder:      "desc",
})
```

`ContentRoutes` serves the index at `/blog` and each item at `/blog/{slug}`, through `c.View`, so an htmx request gets the template alone. The index template receives `.items`, and the item template `.item`:

```html
{{define "blog-index"}}
  {{range .items}}<a href="/blog/{{.Slug}}">{{.Title}}</a>{{end}}
{{end}}

{{define "blog-post"}}
  <article><h1>{{.item.Title}}</h1>{{.item.HTML}}</article>
{{end}}
```

### What the loader checks

- `WithMarkdown` swaps the built-in renderer (headings, code blocks, lists, and paragraphs, no inline formatting) for your own; `modules/markdown` adds GitHub Flavored Markdown and heading IDs in safe mode. Its output is trusted as HTML, so the renderer must escape what it does not mean to keep.
- `WithContentCollection` declares a collection's schema: required fields, types, `MinLength`, `Min`, and `Max` constraints, defaults, and validators. A `FieldTypeTime` field accepts a date string and stores a `time.Time`, so `SortBy("date", "desc")` orders by date.
- A file that fails its schema, cannot be rendered, or repeats a slug is left out, and the rest still load. `LoadDir` returns a `*hyperchi.ContentLoadError` that lists every such file, so `helpers.Must` stops the boot on a broken edit.
- In development `LoadDir` watches the folder and reloads a collection when a file changes; a request sees the old collection or the new one, never a mix. Production loads once.
- `DefaultFilters` narrows the index only. A draft is still served at its own address unless an `ItemHandler` refuses it.

For files compiled into the binary, use `LoadFS` with an embedded folder. It takes the same options and loads once. An embedded file has no modification time, so keep dates in front matter.

```go
//go:embed content
var contentFiles embed.FS

helpers.Must(app.Content.LoadFS(contentFiles, "content", hyperchi.WithMarkdown(render)))
```

Outside `ContentRoutes`, read the same collections in any handler with `app.Content.Get("blog", slug)` or `app.Content.Query("blog").Where("draft", false).SortBy("date", "desc").Limit(5).All()`.

## Format what you load {#next}

Front matter values reach templates as plain Go values. [Format dates, numbers, and text with template filters](/docs/filters), or see the [studio example](/docs/apps), which builds its games, devlog, and press kit this way.

Typed models, references between collections, and model generation are covered in [typed content collections](/docs/content-v2).
