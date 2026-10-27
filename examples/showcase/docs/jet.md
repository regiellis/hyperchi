---
title: Jet + htmx
section: Integrations
order: 200
next: alpine
description: Keep templates in their own files and render them with a request-local view model. Relay demonstrates Jet pages and htmx pricing updates.
---

## A page and a pricing partial {#overview}

[Open Relay](/examples/jet) for the complete collaboration product page. Its pricing control requests `/examples/jet/pricing?billing=monthly` or `billing=annual`. The server calculates the display values from fixture plans and returns the pricing markup.

Jet templates stay outside the Go source and execute at runtime. The showcase embeds them, so the built binary does not require a template directory beside it. The [Jet wiki](https://github.com/CloudyKit/jet/wiki) documents its template syntax and composition; use the v6 Go import path for this example.

## Initialize once, execute per request {#example}

This standalone adapter uses an in-memory loader to keep the whole example visible. Import `github.com/CloudyKit/jet/v6`, the core HyperChi package, and `github.com/regiellis/hyperchi/modules/jet` as `jetadapter`. Put the setup before serving requests:

```go
loader := jet.NewInMemLoader()
loader.Set("hello.jet", "<h1>Hello, {{.Name}}</h1>")
views, err := jetadapter.New(jet.NewSet(loader), "hello.jet")
helpers.Must(err)
helpers.Must(app.Integrations().Register("views", views))

app.Get("/hello", func(c *hyperchi.Context) error {
    return c.RenderWith("views", jetadapter.View{
        Name: "hello.jet",
        Data: struct{ Name string }{Name: "friend"},
    })
})
```

`jetadapter.New` loads every allowed template at startup and fails on missing or invalid templates. `jetadapter.View` selects a preloaded name and request-local data; an optional `Vars` field accepts a Jet variable map. The manager buffers output and HyperChi writes the response. Do not mutate the configured loader, global variables, or shared data while serving requests.

### Share the pricing markup

The full Relay page in `marketing/relay.jet` includes `marketing/pricing.jet`, which the pricing endpoint also renders directly. That keeps plan descriptions and prices consistent between the first response and later swaps. An equivalent control looks like this:

```html
<button hx-get="/examples/jet/pricing?billing=annual"
        hx-target="#pricing-result" hx-swap="innerHTML">
    Pay annually
</button>
<div id="pricing-result" aria-live="polite">
    <!-- Include the pricing template in the initial page. -->
</div>
```

The query parameter chooses the billing option; the server remains responsible for plan prices. It returns `text/html` even if an API client sends an `Accept: application/json` header, because the context handler explicitly writes HTML.

The actual billing control is a GET form. Without JavaScript it submits to `/examples/jet?billing=annual#pricing`, renders a complete page, and preserves the selected billing option.

### Escaping and request context

Jet’s default HTML escaping should stay enabled for these HTML views. Do not mark request values as safe HTML. Load business data before rendering, using `c.Ctx()` for cancellable work; Jet’s `Execute` method itself accepts a writer, variables, and data rather than a request context.

Edit `marketing/*.jet` and restart the showcase to refresh the embedded files. Unlike templ, these changes do not require a template generation command. They still require a Go rebuild before the deployed binary changes.

## Choose the next boundary {#next}

Use [templ](/docs/templ) when you prefer compiled component calls, or [Alpine + JSON](/docs/alpine) when the browser should own a structured collection. All three can live in the same HyperChi application.
