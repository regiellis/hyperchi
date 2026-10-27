---
title: templ + htmx
section: Integrations
order: 190
next: jet
description: Use Go types for your page inputs and return HTML for interactions. The Form & Field studio page connects compiled templ components to HyperChi routes.
---

## From a typed model to a component {#overview}

[Open the full studio page](/examples/templ). Its initial response includes the marketing content and an estimate. Choosing a scope requests `/examples/templ/estimate?scope=launch`, `identity`, or `complete`. The response is HTML for the estimate area; the surrounding page stays in place.

templ compiles `.templ` files into Go functions returning `templ.Component`. Pass explicit values into those functions rather than a loosely shaped global map. The [upstream component guide](https://templ.guide/core-concepts/components/) describes the component interface and composition.

## A minimal render adapter {#example}

This smaller example shows the same boundary as the showcase. Create `hello.templ` in a Go package with templ installed:

```templ
package main

templ greeting(name string) {
    <main>
        <h1>Hello, { name }</h1>
    </main>
}
```

Generate the Go file, then register the component handler. The optional `github.com/regiellis/hyperchi/modules/templ` module supplies the adapter. Register it before starting the server:

```go
// Import the optional module as templadapter.
helpers.Must(app.Integrations().Register("views", templadapter.New()))

app.Get("/hello", func(c *hyperchi.Context) error {
    name := c.QueryDefault("name", "friend")
    return c.RenderWith("views", greeting(name))
})
```

`c.RenderWith` asks the manager to buffer the component output, then sets the HTML content type and writes the completed response. Render errors return to HyperChi without committing partial HTML. The request context reaches templ unchanged. `app.Serve` starts registered integrations automatically; see [the manager guide](/docs/integration-manager) when embedding HyperChi in another HTTP server.

### HTML over the wire

The studio registers the page and the estimate with `app.Get`, both calling the registered templ adapter through `c.RenderWith`. Its `studioEstimate` model has `Scope`, `Price`, `Timeline`, and `Includes` string fields. An htmx control can target that fragment route directly:

```html
<button hx-get="/examples/templ/estimate?scope=identity"
        hx-target="#estimate-result" hx-swap="innerHTML">
    Brand identity
</button>
<div id="estimate-result" aria-live="polite">
    <!-- Render the initial estimate here on the server. -->
</div>
```

Load htmx before using these attributes. The showcase supplies its existing local htmx 4 bundle. The handler selects a known scope and renders the corresponding model; it does not trust a client-supplied price. HTML fragments have an explicit route, so a direct request returns the same fragment without needing an `HX-Request` header.

The actual page uses a GET form with an htmx fragment target. Without JavaScript, it submits to `/examples/templ?scope=identity#estimate` and renders the complete page with the selected option preserved.

### Edit source, regenerate, run

In the showcase, edit `marketing.templ` and regenerate `marketing_templ.go` with `go generate ./...` from `examples/showcase`. The directive runs `go run github.com/a-h/templ/cmd/templ@v0.3.1100 generate -f marketing.templ`; see [the integration source guide](https://github.com/regiellis/hyperchi/blob/main/examples/showcase/INTEGRATIONS.md). Commit both files. Checked-in generated code lets a normal `go run .` work without installing the generator.

Ordinary templ text expressions escape their values. Keep user input in those expressions; do not bypass escaping with raw HTML. The Go compiler checks component arguments, but validation of incoming query parameters remains the handler’s job.

## Change the data source {#next}

Replace the fixture selection with a service call accepting `c.Ctx()`, then construct the same component inputs. The component does not need to know whether its estimate came from a constant, SQL, or an HTTP API. Read [data passing](/docs/data) for how to keep that boundary explicit.
