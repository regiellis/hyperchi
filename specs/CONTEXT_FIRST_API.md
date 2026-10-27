# Context-first API

Status: approved direction, in progress on `feat/context-first-api`.

## Problem

HyperChi's primary handler is a plain `func(w http.ResponseWriter, r *http.Request)`. A page
handler expresses template data by JSON-encoding it to the writer. `HX`/`Page` buffer the
response, sniff the first byte, parse the JSON back, then render (`CODE_REVIEW.md` §6 A9).
Routes have many spellings: `Get(pattern, template, h)` with a magic empty template,
`GetC`, `PageGet`, `FragmentGet`, `HandleRoute`, `API`, `Quick`, `JSONRoute`, `Form`, `Modal`,
`Turbo` (A10).

Two costs follow:

1. **Writing feel.** Rendering a page by writing JSON is surprising, untyped, and verbose.
   The scaffold repeats `development` and `vite_dev_server` in every handler.
2. **Agents write plain Go.** A handler that receives `(w, r)` gives an agent nothing that
   says "framework". Measured at `93b2359`:

   | Area | `(w, r)` handlers | `*hyperchi.Context` handlers |
   | --- | --- | --- |
   | `examples/showcase` | 10 | 7 |
   | `examples/streamer` | 7 | 0 |
   | `examples/streamer-vite` | 5 | 0 |
   | `cmd/hyperchi/_scaffold` | 3 | 0 |

   The scaffold also serves with `http.ListenAndServe`, skipping `Serve` (port fallback,
   graceful shutdown, integration startup).

Every error returned from a Context handler renders as a 500: nothing carries a status.

## Goals

- One way to write a route: `func(c *hyperchi.Context) error`.
- The handler says what it returns (`c.View`, `c.Fragment`, `c.JSON`, `c.Redirect`), and the
  framework handles HTMX negotiation, layouts, and error rendering.
- Shapes agents already know from Echo, Fiber, and Gin, so the framework is their first guess.
- Plain `net/http` stays reachable through one clearly named escape hatch.

Raw throughput is not a goal. This design is still expected to be faster than today because
it removes the JSON encode/parse round trip on every render.

## Decisions

| Question | Decision |
| --- | --- |
| Handler shape | `type Handler func(*Context) error`, response chosen explicitly |
| Old `(w, r)` route API | Clean break. Pre-1.0, and compile errors guide each call site |
| Agent guidance | `hyperchi new` writes `AGENTS.md` into generated projects |

## API

### Routing

```go
app.Get("/", home)                       // Handler, optional per-route middleware
app.Post("/todos", createTodo, auth)
app.Put / Patch / Delete / Head / Options
app.Group("/admin", func(g *hyperchi.HyperChi) { g.Use(auth); g.Get("/", dash) })
app.Use(mw ...)                          // stdlib middleware, unchanged

app.Resource("/posts", postsController)  // ResourceController methods are Handlers
app.RESTful("/api/posts", postsAPI)      // RESTController, JSON

app.Handle(method, pattern string, h http.Handler)  // escape hatch, plain net/http
app.Router()                                         // raw chi, unchanged
app.ToHTTP(h Handler) http.HandlerFunc               // mount a Handler elsewhere
hyperchi.WrapHTTP(h http.Handler) Handler            // adapt a stdlib handler
```

Removed: `GetC`/`PostC`/`PutC`/`DeleteC`/`FragmentC`, `PageGet`, `FragmentGet`,
`*WithValidation`, `HandleRoute`, `HandleFunc`, `API`, `Quick`, `JSONRoute`, `Form`, `Modal`,
`Turbo`, `CRUD`/`CRUDHandlers`, `Scaffold`, the exported `HX`/`Page` wrappers with
`responseCapture`, `FuncRenderer`/`app.Func`, `app.Fragment`, `app.TemplateWithLayout`,
`IntegrationRenderer`, and the app-level `(w, r)` helpers (`app.JSON`, `app.HTML`,
`app.Error`, `app.Redirect`, `app.NotFound`/`BadRequest`/`InternalError`/`Unauthorized`/
`Forbidden`, `ErrorHandlers`, `JSONWithStatus`, `DebugError`). The `app.WebSocket`/`WS`/
`WebSocketBroadcast` stubs, which only ever answered 501, are replaced by the working socket
routes below. `RedirectRoute`, `FileServer`, `Static`,
`Mount`, and `ContentRoutes` stay; `ContentRouteConfig` handlers take a `*Context`.

### Request side

```go
c.Param("id")  c.ParamInt("id")
c.Query("q")   c.QueryDefault("page", "1")  c.QueryInt("page", 1)  c.Queries("tag")
c.FormValue("name")  c.Header("X-Foo")  c.Cookie("name")
c.Ctx() context.Context   c.Request() *http.Request   c.Response() http.ResponseWriter
c.Value(key)  c.Session()  c.UserID()  c.CSRFToken()
c.IsHTMX() bool           // HX-Request
c.HX().Target()  .Source()  .Boosted()  .CurrentURL()

in, err := hyperchi.Bind[CreateTodo](c)  // form, multipart, or JSON by Content-Type,
                                         // then struct-tag validation; error is a 422 HTTPError
c.Bind(&in)                              // non-generic form
data, err := c.ValidateSchema("signup")  // a validator.Schema registered on app.Schemas
```

Validation tags: `required`, `min=N`, `max=N` (length for strings and slices, value for
numbers), `email`, `url`, `oneof=a b c`. A struct with a `Validate() error` method runs it
after the tags pass. An unknown rule is a programming error (500), not a 422.

`Writer` and `Request` stop being exported fields. The accessors replace them so the
surface an agent sees through completion is the framework's own.

### Response side

Every method returns `error` so `return c.View(...)` reads as one line.

```go
c.View("todos", data)         // full page with layout. On an HTMX request (non-boosted),
                              // the fragment of the same template. The common case.
c.Page("todos", data)         // always full page
c.Fragment("todo-row", data)  // never a layout
c.Layout("admin").Page("todos", data)  // layout-admin; Layout("none") renders bare
c.JSON(data)  c.Text(s)  c.HTML(safe template.HTML)
c.Redirect("/todos")          // HX-Redirect on HTMX requests, 303 otherwise
c.NoContent()
c.Status(201).View(...)       // chainable status
c.HX().Trigger("todo-added").Retarget("#list").Reswap("beforeend").Fragment("todo-row", t)
c.RenderWith("templ-card", data)   // integrations, unchanged
```

Renders buffer before writing headers, so a template error never sends a half page.

**Template data.** Keys merge in this order, later winning: route and query parameters and
middleware values from the request, then app globals set once with `app.SetGlobal`
(`development`, `vite_dev_server`, a site name), then the handler's data. A query string can
never override a global, and handlers never repeat globals.

### WebSockets

```go
app.WebSocket("/chat/{room}", func(s *hyperchi.Socket) error {
	room := s.Context().Param("room")   // handshake request: params, session, user
	s.Join(room)
	for {
		msg, err := s.Receive()          // htmx 4 ws message: form values + HX headers
		if err != nil {
			return err                   // matches ErrSocketClosed when the client leaves
		}
		line, err := hyperchi.BindMessage[ChatLine](msg)   // Bind's tags and rules
		if err != nil {
			return err
		}
		if err := app.Room(room).Swap("#feed", "beforeend", "chat-line", line); err != nil {
			return err
		}
	}
})
```

`s.Swap`, `s.SendHTML`, and `s.SendJSON` answer one client; `app.Room(name)` broadcasts from
any handler. A push-only socket joins its rooms and calls `s.Wait()`. Sockets run on the app's
existing hub, so they share rooms, origin policy, server-side identity, and keepalive with
`Extensions().EnableWebSocket`. A handler error other than `ErrSocketClosed` closes the socket
with status 1011.

### Errors

```go
return hyperchi.NotFound("no such todo")
return hyperchi.BadRequest("missing title")
return hyperchi.Errorf(http.StatusConflict, "already exists: %s", name)
return err                                  // plain error: 500, logged with details
```

`HTTPError{Code, Message, Err, Fields}` implements `error` and `Unwrap`; `Err` is logged,
never shown. `app.DefaultError` renders by client: `Accept: application/json` gets
`{"error", "fields"}`, an htmx request gets the `error` template as a fragment (or a small
escaped alert when the app has none), a browser gets the `error` template as a page (or a
minimal page). A plain error is a 500 whose text never reaches the client; in a browser it
falls back to the error page handler, which shows detail in development. `app.OnError(fn)`
replaces the default, and `fn` can call `app.DefaultError` for cases it does not handle.
`Bind` failures carry `Fields` for re-rendering a form fragment with
`c.Status(422).Fragment`. htmx 4 swaps every status except 204 and 304 (`noSwap`), firing
`htmx:response:error` for 4xx and 5xx, so a 422 form fragment or an error fragment lands in
place without configuration. Use `hx-status:<code>` on an element to change that per status.

## Code layout

New code lives outside `hyperchi.go` (A1): `context.go` (Context and request side),
`respond.go` (response side), `router.go` (route registration), `httperror.go`, `bind.go`.
Removed code is deleted from `hyperchi.go`, not wrapped.

## Phases

Each phase ends green on: `go build ./... && go vet ./... && go test -count=1 ./...`, plus
`go test -race` for touched concurrent packages, plus `golangci-lint run`.

1. **Core** (sequential, design-sensitive). `Handler`, `Context` request and response sides,
   `HTTPError` and the central error handler, `Bind`, template-data globals, router with the
   new signatures, removals. Tests for: View negotiation (plain, HTMX, boosted), status
   chaining, HX header chain, Redirect on HTMX vs plain, buffered render failure, error
   rendering per Accept/HTMX, Bind for each content type plus validation failure.
2. **Migrate** (parallel, mechanical, split by area):
   a. `hyperchi/` internal callers (`patterns`, `content`, `validator`, CRUD) and unit tests
   b. `tests/` integration suite
   c. `examples/showcase`
   d. `examples/streamer`, `examples/streamer-vite`, `cmd/hyperchi/_scaffold` (serve via `app.Serve`)
3. **Guidance.**
   - `Example*` tests for every route and response method, so they show in `go doc`.
   - README and `docs/` rewritten to the new API.
   - Scaffold `AGENTS.md`, written by `hyperchi new`.
   - CHANGELOG breaking-change entry. `CODE_REVIEW.md` A9 and A10 closed.

Exit gate for the whole change: the idiom count above reads zero `(w, r)` handlers in
`examples/` and `cmd/hyperchi/_scaffold` outside an explicit `Handle`/`WrapHTTP` escape hatch, and a
scaffolded project builds and serves via `task scaffold:dry`.

## Scaffold AGENTS.md (outline)

Short, a table first:

| Do | Not |
| --- | --- |
| `app.Get("/x", func(c *hyperchi.Context) error {...})` | `func(w http.ResponseWriter, r *http.Request)` |
| `return c.View("x", data)` | `json.NewEncoder(w)` / `WriteJSON` to render |
| `c.QueryDefault`, `c.Param`, `hyperchi.Bind[T](c)` | `r.URL.Query()`, `r.ParseForm()` |
| `return hyperchi.NotFound(...)` | `http.Error(w, ...)` |
| `c.HX().Trigger(...)` | `w.Header().Set("HX-Trigger", ...)` |
| `hc-on:click`, `hc-data` | Alpine, inline `onclick` |
| `app.Serve(addr)` / `app.Run()` | `http.ListenAndServe` |

Then: where templates live, how `define` names work, dev ports, `task dev`.

## Risks

- **Breadth.** Roughly 300 route call sites across `tests/`, `examples/`, and internal
  packages. Mitigation: the compiler finds every one, and phase 2 splits by area.
- **Template field names.** Removing the JSON round trip means structs reach templates as Go
  values, so templates must use Go field names instead of `json` tag names. The compiler does
  not catch this; each migrated template has to be rendered once. Documented in the changelog
  migration guide.
- **Implicit request data.** Resolved in phase 1: request values stay at the top level so
  templates keep working, but globals and handler data now outrank them.
- **Middleware that relied on `responseCapture`.** Resolved: nothing outside the removed
  `HX`/`Page` wrappers used it.
