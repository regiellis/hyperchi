# HyperChi Context System

Every HyperChi route is a `hyperchi.Handler`:

```go
type Handler func(c *hyperchi.Context) error
```

The handler reads the request through `c`, answers with one of `c`'s response methods, and
returns. A returned error goes to the app's error handler, which renders it for the client
that asked (see [Error Handling](ERROR_HANDLING.md)). There is one route shape; the
`(w http.ResponseWriter, r *http.Request)` form is reserved for the explicit net/http escape
hatch described at the end of this page.

```go
app.Get("/users/{id}", func(c *hyperchi.Context) error {
    id, err := c.ParamInt("id") // a non-integer id is already a 400
    if err != nil {
        return err
    }
    user, err := users.Find(c.Ctx(), id)
    if err != nil {
        return hyperchi.NotFound("No such user.")
    }
    return c.View("user", hyperchi.H{"user": user})
})
```

## Routes

```go
app.Get("/", home)                       // Handler, then optional per-route middleware
app.Post("/todos", createTodo, auth)
app.Put("/todos/{id}", updateTodo)       // also Patch, Delete, Head, Options
app.Group("/admin", func(g *hyperchi.HyperChi) {
    g.Use(auth)
    g.Get("/", dashboard)
})
app.Use(mw)                              // func(http.Handler) http.Handler, before any route

app.Resource("/posts", postsController)  // ResourceController: Index, New, Create, Show, Edit, Update, Delete
app.RESTful("/api/posts", postsAPI)      // RESTController: Index, Create, Show, Update, Delete
```

`ResourceController` and `RESTController` methods are `Handler`s, so a controller is a struct
with methods of the form `func (p *Posts) Show(c *hyperchi.Context) error`.

## Request side

| Method | Returns |
| --- | --- |
| `c.Method()`, `c.Path()` | request method and URL path |
| `c.Param("id")` | a route parameter such as `{id}` |
| `c.ParamInt("id")` | the parameter as an `int`, or a 400 error to return as is |
| `c.Query("q")` | the first query value |
| `c.QueryDefault("page", "1")` | the query value, or the fallback when absent or empty |
| `c.QueryInt("page", 1)` | the query value as an `int`, or the fallback |
| `c.Queries("tag")` | every value of a query parameter |
| `c.FormValue("name")` | one submitted form field |
| `c.Header("X-Foo")`, `c.Cookie("name")` | a request header or cookie value |
| `c.Ctx()` | the request's `context.Context`, for cancellation and deadlines |
| `c.Value("key")` | a value middleware attached with `hyperchi.WithContext` |
| `c.Session()` | the request's `*KVSession`, or nil without `UseSessions` |
| `c.UserID()` | the authenticated user's ID, or `""` |
| `c.CSRFToken()` | the CSRF token for forms that post back |
| `c.IsHTMX()` | whether htmx sent the request |
| `c.HX().Target()`, `.Source()`, `.Boosted()`, `.CurrentURL()` | htmx request headers |
| `c.Request()`, `c.Response()`, `c.App()` | the underlying request, writer, and app |

### Binding input

`hyperchi.Bind[T](c)` decodes the request into a new `T` and validates it. JSON bodies decode
by `json` tags; forms, multipart forms, and query strings decode by `form` tags.

```go
type Signup struct {
    Email string `form:"email" validate:"required,email"`
    Plan  string `form:"plan" validate:"required,oneof=free pro"`
    Age   *int   `form:"age" validate:"min=13"`
}

app.Post("/signup", func(c *hyperchi.Context) error {
    in, err := hyperchi.Bind[Signup](c)
    if err != nil {
        return err // 400 for an unreadable body, 422 with per-field messages
    }
    // ...
    return c.Redirect("/welcome")
})
```

- Rules: `required`, `min=N`, `max=N` (length for strings and slices, value for numbers),
  `email`, `url`, `oneof=a b c`. An unknown rule is a programming error (500), not a 422.
- A struct with a `Validate() error` method runs it after the tag rules pass.
- `c.Bind(&v)` is the non-generic form. `hyperchi.Bind[map[string]any](c)` returns the raw JSON
  object or form values without validation.
- `c.ValidateSchema("signup")` checks the input against a schema registered on
  `app.Schemas` and returns the cleaned `map[string]any`, or a 422.

## Response side

Every response method returns `error`, so a handler ends in one line: `return c.View(...)`.

| Method | Sends |
| --- | --- |
| `c.View(name, data)` | the full page inside its layout for a browser or a boosted navigation; the template alone for an htmx partial request |
| `c.Page(name, data)` | the full page inside its layout, always |
| `c.Fragment(name, data)` | the template alone, never a layout |
| `c.JSON(v)` | `application/json` |
| `c.Text(s)` | `text/plain` |
| `c.HTML(markup)` | trusted `template.HTML` the application built itself |
| `c.Redirect(url)` | `HX-Redirect` for htmx, `303 See Other` otherwise |
| `c.NoContent()` | `204 No Content` |
| `c.RenderWith(name, data)` | output from a named integration, such as a templ or Jet adapter |

These chain in front of a response:

```go
return c.Status(http.StatusCreated).Fragment("todo-row", todo)
return c.Layout("admin").Page("dashboard", data)  // layout-admin; Layout("none") renders bare
return c.SetHeader("Cache-Control", "no-store").JSON(stats)
return c.HX().Trigger("todo-added").Retarget("#list").Reswap("beforeend").Fragment("todo-row", todo)
```

`c.SetCookie(&http.Cookie{...})` adds a cookie the same way. Renders and JSON encoding finish
in a buffer before any header is sent, so a template error never leaves a half-written page;
the error goes to the error handler instead.

A handler answers once. A second response method returns an error and writes nothing.

## Template data

A template receives one map. Keys merge in this order, later winning:

1. Route parameters, query parameters, and middleware values (`hyperchi.WithContext`).
2. App globals set once at startup with `app.SetGlobal("key", value)`.
3. The handler's data.

So a query string can never override a global, and handlers never repeat globals such as
`development` or `vite_dev_server`. `.Request` holds the `*http.Request`, and a layout receives
the rendered page as `.content`.

Pass `hyperchi.H` (or any `map[string]any`) to get top-level keys. Any other value, such as a
struct, is available as `.Result`:

```go
return c.View("user", hyperchi.H{"user": user}) // {{ .user.Name }}
return c.View("user", user)                      // {{ .Result.Name }}
```

## Errors

Return an `*hyperchi.HTTPError` to choose the status. Any other error is a 500 whose text is
logged and never shown to the client.

```go
return hyperchi.NotFound("No such todo.")
return hyperchi.BadRequest("Missing title.")
return hyperchi.Errorf(http.StatusConflict, "already exists: %s", name)
return err // 500
```

[Error Handling](ERROR_HANDLING.md) covers how each client sees an error and how to replace
the default with `app.OnError`.

## Middleware

Middleware is plain `func(http.Handler) http.Handler`, so any chi or net/http middleware works
with `app.Use`, `g.Use` inside a group, and the per-route arguments after a handler. Middleware
passes values to handlers and templates with `hyperchi.WithContext`:

```go
func withTenant(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        next.ServeHTTP(w, hyperchi.WithContext(r, "tenant", tenantFromHost(r.Host)))
    })
}

app.Get("/", func(c *hyperchi.Context) error {
    return c.View("home", hyperchi.H{"tenant_name": c.Value("tenant")})
}, withTenant)
```

## WebSocket routes

A WebSocket route takes a `SocketHandler`, `func(s *hyperchi.Socket) error`, which serves one
connection for its whole life. The handshake runs through per-route middleware like any GET
route, and `s.Context()` is the handshake request's `Context`, for route parameters, query,
session, and user.

```go
type ChatLine struct {
    Text string `form:"text" validate:"required,max=500"`
}

app.WebSocket("/chat/{room}", func(s *hyperchi.Socket) error {
    room := s.Context().Param("room")
    s.Join(room)
    for {
        msg, err := s.Receive() // matches hyperchi.ErrSocketClosed when the client leaves
        if err != nil {
            return err
        }
        line, err := hyperchi.BindMessage[ChatLine](msg)
        if fields := hyperchi.FieldErrors(err); fields != nil {
            if err := s.Swap("#chat-form", "outerHTML", "chat-form", hyperchi.H{"errors": fields}); err != nil {
                return err
            }
            continue
        } else if err != nil {
            return err
        }
        if err := app.Room(room).Swap("#feed", "beforeend", "chat-line", line); err != nil {
            return err
        }
    }
}, requireUser)
```

| Call | Does |
| --- | --- |
| `s.Receive()` | waits for the next message: the htmx 4 ws extension sends the form's values and htmx headers |
| `msg.Value(key)`, `msg.Header(key)`, `msg.Values()`, `msg.Text()` | read one message |
| `hyperchi.BindMessage[T](msg)`, `msg.Bind(&v)` | decode and validate with the same `form` and `validate` tags as `Bind` |
| `s.Swap(target, swap, name, data)` | render a template without a layout and swap it into this client's page |
| `s.SendHTML(target, swap, html)`, `s.SendJSON(event, data)` | send trusted markup or a data-only message to this client |
| `s.Join(room)`, `s.Leave(room)` | room membership |
| `s.Wait()` | hold a push-only socket open until the client leaves |
| `s.ID()`, `s.UserID()` | the connection's hub ID and server-side user identity |
| `app.Room(name).Swap(...)`, `.SendHTML`, `.SendJSON`, `.Reload`, `.Redirect` | broadcast to every socket in a room, from any handler or goroutine |

Returning `ErrSocketClosed`, or the error `Receive` gave, ends the socket normally. Any other
error is logged and closes the socket with an internal-error status. Sockets share the hub
behind `app.Extensions().EnableWebSocket`, including its rooms, origin policy, and server-side
identity.

## Plain net/http

Code that already speaks net/http (a third-party handler, a file server, a proxy) reaches the
router through named escape hatches. Application routes use `Handler`.

```go
app.Handle(http.MethodGet, "/metrics", promhttp.Handler()) // a plain http.Handler
app.Get("/legacy", hyperchi.WrapHTTP(legacyHandler))        // http.Handler as a Handler
mux.Handle("/hyperchi-route", app.ToHTTP(myHandler))        // a Handler mounted elsewhere
app.Router()                                                // the underlying chi router
```

`app.NewContext(w, r)` builds a `Context` outside a route, for tests and adapters.

## Testing

A route is tested through the app's `http.Handler`:

```go
func TestShowUser(t *testing.T) {
    app := hyperchi.New()
    app.Get("/users/{id}", showUser)

    req := httptest.NewRequest(http.MethodGet, "/users/7", nil)
    req.Header.Set("Accept", "application/json")
    w := httptest.NewRecorder()
    app.ServeHTTP(w, req)

    if w.Code != http.StatusNotFound {
        t.Fatalf("status = %d, want 404", w.Code)
    }
}
```

Set `HX-Request: true` on the request to exercise the htmx branch of `c.View` and of error
rendering.
