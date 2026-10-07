---
title: Integrations
section: Integrations
order: 170
next: integration-manager
description: Bring your template engine and choose how data reaches the browser. Three complete marketing pages show the same HTTP boundary with different rendering tools.
---

## Choose an example by its data flow {#overview}

Each example has a complete landing page, multiple content sections, navigation, and a working server interaction. These are server-rendered single-page experiences; they do not install a client-side router.

- [Form & Field (templ + htmx)](/examples/templ): an editorial creative studio. Select a project scope and receive a rendered estimate fragment. Start here if you want typed Go component parameters.
- [Relay (Jet + htmx)](/examples/jet): a project collaboration product. Switch billing periods and receive new pricing HTML. Start here if you want runtime templates with shared partials.
- [Outpost (Alpine.js + JSON)](/examples/alpine): an outdoor club. Filter trips using a JSON endpoint while Alpine owns the browser state. Start here if your UI needs structured data.

The businesses, plans, prices, and trips are fictional fixtures. These examples do not accept bookings, create subscriptions, or write to a database.

The showcase module requires Go 1.26.8. Its integrations use templ v0.3.1070, Jet v6.3.3, and Alpine.js 3.15.8. The HyperChi library itself requires Go 1.26.0.

## Start with HyperChi handlers {#example}

Register a GET handler with `app.Get`. The handler receives a `*hyperchi.Context` and returns an error. Read inputs with `c.Query` and `c.Param`, render a Go template with `c.View`, or return JSON with `c.JSON`. A returned error goes to HyperChi’s error handler. A fragment route is an ordinary `app.Get` route whose handler returns a fragment.

```go
app.Get("/status", func(c *hyperchi.Context) error {
    return c.JSON(hyperchi.H{"ready": true})
})
```

This snippet needs only the core HyperChi import. Middleware still applies. Each handler selects its response explicitly: a template, HTML, or JSON. `app.Handle` remains an interoperability option for an existing `net/http` handler. See [Handlers & helpers](/docs/helpers) for the framework APIs used throughout these examples.

### Run the complete pages

```sh
# From the repository root:
cd examples/showcase
go run .

# In another terminal (default PORT is 9999):
curl http://localhost:9999/examples/templ
curl 'http://localhost:9999/examples/jet/pricing?billing=annual'
curl 'http://localhost:9999/examples/alpine/trips?terrain=coast'
```

Open `/examples/templ`, `/examples/jet`, and `/examples/alpine` in a browser. Check the server’s startup address if port 9999 is occupied. JavaScript bundles, templates, and styles are included with the showcase; ordinary runs do not need Node, a C compiler, or a frontend build.

### Find the implementation

`integrations.go` owns the adapters, route registration, and fixture data. `marketing.templ` defines the typed page and estimate component; `marketing_templ.go` is generated Go. `marketing/*.jet` contains the Jet views. `templates/outpost.tmpl` is rendered by HyperChi for the Alpine page. Jet templates are embedded; the Docker image includes HyperChi’s template directory.

## Follow a complete request {#next}

Read the [templ guide](/docs/templ), [Jet guide](/docs/jet), or [Alpine guide](/docs/alpine) for the rendering path. The [data passing guide](/docs/data) compares query parameters, typed view models, HTML fragments, and JSON contracts.
