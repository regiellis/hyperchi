---
title: Templates
section: Core
order: 40
next: data
description: HTML stays HTML. Go templates add data, reusable pieces, and the composition you need as your application grows.
---

## Load once, render per request {#overview}

Load templates at startup before registering your page routes. `**` matches directories at any depth, and what follows it is matched against file names, so this loads every `.tmpl` file under `templates/`. A pattern that matches nothing is an error, not an empty site.

```go
helpers.Must(app.LoadTemplates("templates/**/*.tmpl"))
```

Name reusable templates explicitly with `define`. That keeps the name stable when you reorganize files. Without a `define`, a template is named after its file, extension included (`home.tmpl`), and a lookup for `home` still finds it; an explicit `define` always wins. Templates are parsed once, so restart the server to see edits (the scaffold's `task dev:air` does this for you).

To ship one binary, embed the folder and load it from the file system. The pattern uses slash-separated paths and follows the same rules:

```go
//go:embed templates
var templateFiles embed.FS

helpers.Must(app.LoadTemplatesFS(templateFiles, "templates/**/*.tmpl"))
```

## Compose a page {#example}

A page has three parts: a component, a layout that wraps every page, and the page itself. In `templates/components/header.tmpl`:

```html
{{define "header"}}
  <header>
    <a href="/">My application</a>
  </header>
{{end}}
```

In `templates/layouts/base.tmpl`, a template named `layout-base` receives the rendered page as `.content`:

```html
{{define "layout-base"}}
  <!doctype html>
  <html lang="en">
    <head><title>{{ .title }}</title></head>
    <body>
      {{template "header" .}}
      {{ .content }}
    </body>
  </html>
{{end}}
```

In `templates/pages/home.tmpl`:

```html
{{define "home"}}
  <main><h1>{{ .title }}</h1></main>
{{end}}
```

Reference the declared name from your route:

```go
app.Get("/", func(c *hyperchi.Context) error {
    return c.View("home", hyperchi.H{"title": "Welcome home"})
})
```

## Layouts {#layouts}

`c.View` and `c.Page` wrap the page in `layout-base`. `c.Layout("admin")` picks `layout-admin` instead, and `c.Layout("none")` renders the page bare.

```go
app.Get("/admin", func(c *hyperchi.Context) error {
    return c.Layout("admin").View("dashboard", hyperchi.H{"title": "Dashboard"})
})
```

A layout can build on another one, because it is just a template:

```html
{{define "layout-admin"}}{{template "layout-base" .}}{{end}}
```

`layout-base` is optional: with no template of that name the page renders bare. Naming any other layout that does not exist is an error, so a typo shows up as a 500 instead of an unstyled page.

## Fragments for htmx {#fragments}

A fragment is a template rendered without a layout, so htmx can swap it into a page. Two methods produce one:

- `c.Fragment(name, data)` never adds a layout. Use it for endpoints that only answer htmx.
- `c.View(name, data)` adds the layout for a browser or boosted navigation and omits it for an htmx partial request. One handler then serves both the full page and the swap.

```go
app.Get("/items/{id}", func(c *hyperchi.Context) error {
    found, err := store.Find(c.Ctx(), c.Param("id"))
    if err != nil {
        return hyperchi.NotFound("No such item.")
    }
    return c.View("item", hyperchi.H{"item": found}) // full page, or just "item" for hx-get
})

app.Post("/items", func(c *hyperchi.Context) error {
    created, err := store.Add(c.Ctx(), c.FormValue("name"))
    if err != nil {
        return err
    }
    return c.Status(http.StatusCreated).Fragment("item-row", created)
})
```

```html
<form hx-post="/items" hx-target="#items" hx-swap="beforeend">
  <input name="name" required>
  <button>Add</button>
</form>
<ul id="items"></ul>
```

## What a template can read {#data}

A template receives one map. Keys merge in this order, later winning: route and query parameters and middleware values (`hyperchi.WithContext`), then `app.SetGlobal` values, then the handler's data. A global therefore beats a query string, and a handler beats a global.

```go
app.SetGlobal("siteName", "Acme") // once, at startup, before Serve

app.Get("/items/{id}", func(c *hyperchi.Context) error {
    return c.View("item", hyperchi.H{"title": "Item"})
})
```

In the `item` template, `{{ .siteName }}`, `{{ .id }}`, and `{{ .title }}` all resolve. A route parameter beats a query parameter of the same name, so `/items/7?id=99` still renders item 7, as `c.Param("id")` reports.

Pass `hyperchi.H` (or any `map[string]any`) to get top-level keys. Any other value is available as `.Result`, and a struct also adds its exported fields as top-level keys, so a template written for one item (`{{ .Name }}`) renders it whether a page ranges over a list or a handler passes the item to `c.Fragment` on its own. The request is always `.Request`. Flash messages appear as `.flashes` when the request has any.

## Render a template to a string {#compose}

`app.RenderToString(name, data)` runs a loaded template without a layout and returns safe HTML, for building a page from blocks. Values inside the template are still escaped; only the returned wrapper is trusted.

```go
app.Get("/dashboard", func(c *hyperchi.Context) error {
    card, err := app.RenderToString("card", hyperchi.H{"title": "Hello"})
    if err != nil {
        return err
    }
    return c.View("dashboard", hyperchi.H{"card": card}) // {{ .card }} is not escaped again
})
```

The name resolves like a render name, so both a `define` and a bare `card.tmpl` work. Register custom template functions with `app.RegisterFilter` before loading templates; the [filters page](/docs/filters) lists the built-ins.

## Link static files {#assets}

Mount your static files once at startup, then link them from templates with the `asset` function. With `embed`, `io/fs`, and the `hyperchi` and `helpers` packages imported:

```go
//go:embed static
var staticFiles embed.FS

static, err := fs.Sub(staticFiles, "static")
helpers.Must(err)
helpers.Must(app.StaticFS("/static/*", static, hyperchi.StaticOptions{}))
```

`app.StaticWithOptions("/static/*", "static", hyperchi.StaticOptions{})` serves a folder on disk instead. Both refuse directories, dotfiles, and paths that leave the folder, and send a miss through the application error handler.

```html
<link rel="stylesheet" href="{{asset "/static/css/app.css"}}">
<script src="{{asset "/static/js/app.js"}}" defer></script>
```

`asset` adds a version taken from the file's content, such as `/static/css/app.css?v=3f2a9c1b7e4d`. A request carrying the current version gets `Cache-Control: public, max-age=31536000, immutable`, so the browser keeps the file for a year and fetches a new address when the file changes. Other requests are cached for an hour. A path that no mount serves stops the render with an error, so a typo shows up in tests instead of as a broken page. In development a changed file gets a new version on the next render.

Mount the files before anything renders, including pages pre-rendered at startup. Go code that needs the same address, for an Open Graph image or a preload header, calls `app.AssetURL("/static/img/og.png")`.

### Compress responses {#compression}

`app.AutoMiddleware()` gzips responses in production. To build your own stack, add `app.Middleware.Compress()` inside `Recovery`, so a panic still gets a clean error page:

```go
app.Use(app.Middleware.Recovery(), app.Middleware.Compress())
```

It uses the standard library's gzip and skips bodies under 1 KiB, server-sent event streams, WebSocket upgrades, `HEAD` requests, and formats that are already compressed (images other than SVG, fonts, audio, video, archives). `hyperchi.CompressWithOptions(hyperchi.CompressOptions{Level: 6, MinSize: 2048})` sets the level and threshold.

Compressing a page over HTTPS that both reflects request input and contains a per-user secret can expose that secret to the BREACH attack. HyperChi's CSRF token is masked differently on every render, so it is safe. Keep other secrets, such as API keys, off compressed pages that echo input.

## Keep the small pieces reusable {#next}

The same composition works for navigation, forms, and the fragments returned to htmx. HyperChi also includes template filters for strings, dates, numbers, and htmx attributes.

[Load Markdown content into collections](/docs/content), [explore the available filters](/docs/filters), or [add local interaction with directives](/docs/directives).
