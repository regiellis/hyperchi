---
title: Integration manager
section: Integrations
order: 180
next: templ
description: Register optional capabilities once. Let HyperChi manage startup, rendering, readiness checks, and shutdown while each integration keeps its own typed API.
---

## One manager per application {#overview}

`app.Integrations()` owns a named registry shared by route groups. It is separate from `app.Extensions()`, which handles htmx browser extensions such as SSE and WebSockets. The core manager has no templ or Jet dependency; those live in optional Go modules.

```go
import templadapter "github.com/regiellis/hyperchi/modules/templ"

helpers.Must(app.Integrations().Register("views", templadapter.New()))
app.Get("/", func(c *hyperchi.Context) error {
    return c.RenderWith("views", HomePage(data))
})
helpers.Must(app.Serve("127.0.0.1:8080"))
```

`HomePage` is your generated templ component. `RenderWith` buffers the HTML, forwards the request context, and sets the response content type. Unknown integrations, wrong data types, and rendering failures return errors before any HTML is committed.

## Configuration belongs to the adapter {#example}

The manager validates registration and optional `Validate()` hooks. Each adapter constructor takes its own typed configuration. For Jet, initialize a template set, preload the views you allow, and pass a `jetadapter.View` per request:

```go
views, err := jetadapter.New(templateSet, "home.jet", "cards.jet")
helpers.Must(err)
helpers.Must(app.Integrations().Register("views", views))

app.Get("/cards", func(c *hyperchi.Context) error {
    return c.RenderWith("views", jetadapter.View{
        Name: "cards.jet",
        Data: catalog,
    })
})
```

The [Jet guide](/docs/jet) shows a complete loader setup. Duplicate names and typed nil registrations are errors. Registration freezes at startup; this API does not hot-swap live dependencies.

### Lifecycle and ownership

Capabilities implement only the interfaces they need, from `hyperchi/integrations`:

```go
type Renderer interface {
    Render(context.Context, io.Writer, any) error
}
type Starter interface { Start(context.Context) error }
type Closer interface { Close(context.Context) error }
type HealthChecker interface { Health(context.Context) error }
type Validator interface { Validate() error }
```

`app.Serve` starts integrations in registration order before accepting requests, with a 30-second startup deadline. After requests drain, it closes integrations in reverse order before database and KV cleanup, with a 10-second deadline. Listener failures also close integrations.

If startup fails, all registered closers are attempted in reverse order, including partially initialized and not-yet-started integrations. Cleanup gets a fresh 10-second deadline. Closers must tolerate partial initialization. Errors are combined; a failed or closed manager cannot restart. Repeated successful `Start` and repeated `Close` calls do not rerun hooks.

Hooks must honor their contexts. The manager cannot forcibly stop a blocking hook. Startup contexts bound initialization, not background worker lifetime. Lifecycle hooks can inspect the registry but must not recursively call its `Start` or `Close`.

### Manage an existing service

`integrations.Hooks` adapts service functions without requiring a wrapper type. Keep the actual typed client in your handler closure. For an already-open `*sql.DB` named `db`:

```go
helpers.Must(app.Integrations().Register("catalog", integrations.Hooks{
    StartFunc:  db.PingContext,
    HealthFunc: db.PingContext,
    CloseFunc: func(context.Context) error { return db.Close() },
}))
```

Only transfer ownership if the manager should close this resource. Do not also register a second closer for the same client. The manager does not expose its configuration or credentials through an HTTP endpoint.

### Use a typed rendering function

```go
renderer := integrations.TypedRenderer(func(ctx context.Context, w io.Writer, page Page) error {
    return page.WriteHTML(ctx, w)
})
helpers.Must(app.Integrations().Register("reports", renderer))
```

`Page` and `WriteHTML` belong to your application. The adapter checks the data type and returns an error on a mismatch. Your renderer is responsible for escaping HTML. Renderers and native service clients must support concurrent requests.

The optional `github.com/regiellis/hyperchi/modules/markdown` module is a renderer too. `markdown.New()` registers like any adapter, and `c.RenderWith(name, source)` renders a Markdown string or a parsed document. These documentation pages are Markdown files rendered by that module.

### Inspect and check readiness

```go
info := app.Integrations().List()
checks := app.Integrations().Health(ctx)
client, err := integrations.Get[*MyClient](app.Integrations(), "catalog")
```

`List` returns names and capability flags. `Health` returns one error per integration; nil means ready. Integrations without a health hook are ready once the manager starts. Give checks a deadline and decide which details are safe to expose publicly. `Get` performs a checked type assertion; it does not start the integration.

### Embedding in another HTTP server

When using `app.ServeHTTP` or `httptest` directly, explicitly call `app.Integrations().Start(ctx)` before handling requests and `Close(ctx)` after draining them. `RenderWith` rejects use before startup or after shutdown. Do not close integrations while requests or health checks still use them.

### Render from any handler

`c.RenderWith(name, data)` is how a handler renders through an integration. Pass the value the adapter expects, such as a templ component or a `jetadapter.View`; the data reaches the adapter as the same Go value, with no conversion on the way. A page route and its fragment route can call the same adapter.

## Try the real integrations {#next}

[Form & Field](/examples/templ) and [Relay](/examples/jet) use registered adapters today. [Outpost](/examples/alpine) uses core rendering, collection helpers, and JSON; it needs no integration registration. See [Handlers & helpers](/docs/helpers) for the general framework APIs.
