---
title: Routing & responses
section: Core
order: 30
next: templates
description: A URL is a useful interface. Give it a Go handler, then decide whether the browser needs a whole page or a small fragment.
---

## Pages and fragments {#overview}

Every route is a `Handler`: a function that takes a `*hyperchi.Context`, answers through it, and returns an error. `c.View` renders a page template: the whole page for a browser, and only the template for an htmx partial request. For a small piece of HTML, return `c.HTML`, or render a fragment template with `c.Fragment`.

HyperChi builds on chi. Existing `net/http` middleware fits naturally into the application, and `app.Handle` mounts an existing `net/http` handler. New endpoints are [context handlers](/docs/helpers), with request helpers, rendering, JSON responses, and returned-error handling.

## A button with a round trip {#example}

Register this route before starting the server. It needs no extra imports:

```go
app.Get("/hello", func(c *hyperchi.Context) error {
    return c.HTML("<p>Hello from the server.</p>")
})
```

Load htmx 4.0.0 and add a button to your template. The browser requests `/hello` and puts the HTML response inside `#greeting`.

```html
<script src="https://cdn.jsdelivr.net/npm/htmx.org@4.0.0/dist/htmx.min.js"></script>

<button hx-get="/hello" hx-target="#greeting">
  Say hello
</button>
<div id="greeting" aria-live="polite"></div>
```

> For user-provided content, prefer a Go HTML template so values are escaped automatically. Never concatenate unescaped input into an HTML response.

## Methods and patterns {#routes}

`Get`, `Post`, `Put`, `Patch`, `Delete`, `Head`, and `Options` each take a pattern and a handler. Patterns are chi patterns: `{name}` captures one path segment, `{name:regexp}` restricts it, and a trailing `*` captures the rest of the path. A request that matches no pattern, or fails a regexp, goes through the same error pipeline as any other miss. Every `Get` route also answers `HEAD` with the same status and headers and no body, so uptime checks and link previews work; register `app.Head` for the same pattern when HEAD needs its own answer.

```go
app.Get("/items", listItems)
app.Post("/items", createItem)
app.Get("/items/{id:[0-9]+}", showItem)
app.Put("/items/{id}", updateItem)
app.Patch("/items/{id}", updateItem)
app.Delete("/items/{id}", deleteItem)
app.Get("/files/*", serveFile)
```

`app.Resource("/posts", controller)` registers the seven conventional HTML routes for a struct with `Index`, `New`, `Create`, `Show`, `Edit`, `Update`, and `Delete` handlers. `app.RESTful("/api/posts", controller)` does the same for a JSON API and forces JSON errors.

## Reading the request {#request}

Route parameters come from `c.Param`. `c.ParamInt` converts one to an `int`, and a value that is not a number comes back as a 400 error you can return as it is.

```go
func showItem(c *hyperchi.Context) error {
    id, err := c.ParamInt("id")
    if err != nil {
        return err // 400: id must be an integer
    }
    found, err := store.Find(c.Ctx(), id)
    if err != nil {
        return hyperchi.NotFound("No such item.")
    }
    return c.View("item", hyperchi.H{"item": found})
}
```

Query strings have three typed readers, so no handler needs `strconv`:

```go
func searchHandler(c *hyperchi.Context) error {
    q := c.Query("q")                        // first value, "" when absent
    page := c.QueryInt("page", 1)            // 1 when absent or not a number
    sort := c.QueryDefault("sort", "newest") // fallback when absent or empty
    tags := c.Queries("tag")                 // every ?tag=... value
    return c.View("search", hyperchi.H{"q": q, "page": page, "sort": sort, "tags": tags})
}
```

`c.FormValue`, `c.Header`, `c.Cookie`, `c.Method`, `c.Path`, and `c.Ctx` cover the rest of the common reads. To decode and validate a whole form or JSON body into a struct, use `hyperchi.Bind[T](c)`; the [forms page](/docs/forms) covers it.

## Groups and middleware {#groups}

Middleware is plain `func(http.Handler) http.Handler`, so any chi or `net/http` middleware works. `app.Use` applies it to every route, and it must run before the first route is registered (chi panics otherwise; `app.UseGlobal` has no such limit). Pass middleware after the handler to scope it to one route. `app.Group` gives a prefix its own middleware, and `app.Mount` attaches any `http.Handler`, such as another chi router.

```go
app.Use(app.Middleware.Recovery())

app.Group("/admin", func(r *hyperchi.HyperChi) {
    r.Use(requireAdmin)
    r.Get("/", listItems)
    r.Post("/users", createItem)
})

app.Get("/reports", listItems, requireLogin, app.Middleware.NoCache())

api := chi.NewRouter()
api.Get("/ping", func(w http.ResponseWriter, r *http.Request) {})
app.Mount("/api/v2", api)
```

The group's `r` is a copy of the app, so every service (database, sessions, templates, content) works inside it.

## Choosing a response {#responses}

Every response method returns an error, so a handler ends in one line: `return c.View(...)`. A handler answers once; a second response method writes nothing and returns an error.

| Method | Sends |
| --- | --- |
| `c.View(name, data)` | The page inside its layout for a browser or boosted navigation; the bare template for an htmx partial request. The default for page routes. |
| `c.Page(name, data)` | The page inside its layout, whatever sent the request. |
| `c.Fragment(name, data)` | The template alone, never a layout. For endpoints that only answer htmx swaps. |
| `c.HTML(markup)` | Trusted `template.HTML` your code built. Never pass visitor input. |
| `c.JSON(v)`, `c.Text(s)` | `application/json` or `text/plain`. |
| `c.Blob(type, bytes)` | Any other content type, such as CSV or Markdown. |
| `c.Redirect(url)` | `303 See Other`, or an `HX-Redirect` header for htmx so the browser navigates. |
| `c.NoContent()` | `204`, which htmx does not swap. |
| `c.Empty()` | Headers only, with the status you set. |

`c.Status`, `c.SetHeader`, and `c.Layout` chain in front of any of these:

```go
return c.Status(http.StatusCreated).Fragment("item-row", item{})
return c.SetHeader("Cache-Control", "no-store").JSON(hyperchi.H{"ok": true})
return c.Layout("admin").Page("dashboard", data) // wraps in layout-admin
```

Rendering finishes in a buffer before any header is sent, so a template error never leaves half a page on the wire. The [templates page](/docs/templates) explains layouts and fragments in full.

## Status codes and errors {#errors}

Return an error to choose the status. `hyperchi.NotFound`, `BadRequest`, `Forbidden`, `Conflict`, and `Unprocessable` each build an `*HTTPError`; `hyperchi.Errorf(code, ...)` takes any code. Any other error becomes a 500 whose text is logged and never shown to the visitor. The app renders the error for whoever asked: JSON for an API client, a small alert for htmx, and an error page for a browser. htmx 4 swaps every status except 204 and 304, so a 422 form fragment lands in place. [Error handling](/docs/errors) covers `app.OnError` and `hyperchi.JSONErrors`.

## Steering htmx from the server {#htmx-headers}

`c.HX()` reads the htmx request headers (`Target`, `Source`, `CurrentURL`, `Boosted`) and sets response headers. Setters chain and end in a response method.

```go
return c.HX().
    Trigger("item-saved").
    Retarget("#list").
    Reswap("beforeend").
    Fragment("item-row", item{})
```

| Setter | Header | Effect |
| --- | --- | --- |
| `Trigger(event, detail...)` | `HX-Trigger` | Fires a browser event after the swap; call it again to fire several. |
| `Retarget(selector)` | `HX-Retarget` | Swaps into a different element. |
| `Reswap(style)` | `HX-Reswap` | Changes the swap style, such as `outerHTML`. |
| `Reselect(selector)` | `HX-Reselect` | Swaps only part of the response. |
| `PushURL(url)`, `ReplaceURL(url)` | `HX-Push-Url`, `HX-Replace-Url` | Updates browser history. |
| `Location(url)` | `HX-Location` | Navigates without a full reload. |
| `Refresh()` | `HX-Refresh` | Reloads the page. |

A trigger can carry data: `c.HX().Trigger("toast", hyperchi.H{"message": "Saved"})`. When only the headers matter, finish with `Empty()` or `NoContent()`.

## When you already have net/http code {#net-http}

Application routes use `Handler`. Code that already speaks `net/http` reaches the router through three named doors, so the rest of the app keeps the context API:

```go
app.Handle(http.MethodGet, "/metrics", metrics)  // mount an http.Handler
app.Get("/legacy", hyperchi.WrapHTTP(legacy))    // an http.Handler as a Handler
mux.Handle("/inner", app.ToHTTP(listItems))      // a Handler inside another mux
```

`app.Router()` returns the chi router for anything else. Do not write new endpoints with `http.ResponseWriter` signatures: they skip htmx negotiation, the error pipeline, and validation.

## Next steps {#next}

Use the [Context API reference](https://pkg.go.dev/github.com/regiellis/hyperchi/hyperchi#Context) for the complete surface, or [try a real request in the playground](/#flow). Pages and fragments both depend on templates, so [read that page next](/docs/templates) if layouts are unfamiliar.
