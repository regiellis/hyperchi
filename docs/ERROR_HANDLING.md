# HyperChi Error Handling

A HyperChi handler reports failure by returning an error. The framework picks the status from
the error, renders it in the form the client asked for, and logs what needs logging. Handlers
never write error responses themselves.

```go
app.Get("/todos/{id}", func(c *hyperchi.Context) error {
    id, err := c.ParamInt("id") // a non-integer id comes back as a 400
    if err != nil {
        return err
    }
    todo, err := store.Find(c.Ctx(), id)
    if errors.Is(err, database.ErrNoRows) {
        return hyperchi.NotFound("No such todo.").Wrap(err)
    }
    if err != nil {
        return err // a 500; logged in full, never shown to the client
    }
    return c.View("todo", todo)
})
```

## HTTPError

`*hyperchi.HTTPError` is an error with a status:

```go
type HTTPError struct {
    Code    int                 // HTTP status
    Message string              // shown to the client
    Err     error               // the cause: logged and reachable with errors.Is/As, never shown
    Fields  map[string][]string // per-field messages for a validation failure
}
```

Constructors:

| Call | Status |
| --- | --- |
| `hyperchi.BadRequest(msg)` | 400 |
| `hyperchi.Unauthorized(msg)` | 401 |
| `hyperchi.Forbidden(msg)` | 403 |
| `hyperchi.NotFound(msg)` | 404 |
| `hyperchi.Conflict(msg)` | 409 |
| `hyperchi.Unprocessable(msg, fields)` | 422 with per-field messages |
| `hyperchi.NewHTTPError(code, msg)` | any status; an empty message becomes the status text |
| `hyperchi.Errorf(code, format, args...)` | any status, formatted; a `%w` verb keeps the wrapped error as `Err` and leaves its text out of the message |

`.Wrap(err)` attaches a cause to any of them: `hyperchi.NotFound("No such todo.").Wrap(err)`.
`HTTPError` implements `Unwrap`, so `errors.Is` and `errors.As` see through it.

An error that is not an `*HTTPError`, such as a database failure returned as is, is a 500. Its
text is logged and never reaches the client.

Framework methods return `HTTPError`s already: `c.ParamInt` returns a 400 for a non-integer,
`hyperchi.Bind` returns a 400 for an unreadable body and a 422 with `Fields` for failed
validation, and `c.ValidateSchema` returns a 422. A handler returns these as is.

## What each client sees

`app.DefaultError` renders the error for whoever sent the request:

| Client | Response |
| --- | --- |
| API client (`Accept: application/json` without `text/html`) | `{"error": message}`, plus `"fields"` when the error has them |
| htmx request (`HX-Request`) | the `error` template as a fragment, or a small escaped alert (`<div class="hc-error" role="alert">`) when the app has no `error` template |
| Browser, `HTTPError` | the `error` template as a full page in its layout, or a minimal built-in page |
| Browser, any other error | the error page handler: a detailed page in development, a generic page with an error ID in production |

Every response carries the error's status. Errors with a status of 500 or above are logged with
the method, path, and full error, including the wrapped cause.

### The `error` template

Define a template named `error` to style errors for htmx and browsers. It receives:

| Key | Value |
| --- | --- |
| `.status` | the HTTP status code |
| `.message` | `HTTPError.Message` |
| `.fields` | `HTTPError.Fields`, or nil |

```html
{{define "error"}}
<div class="error" role="alert">
  <h2>{{ .status }}</h2>
  <p>{{ .message }}</p>
  {{ with .fields }}
  <ul>
    {{ range $name, $msgs := . }}{{ range $msgs }}<li>{{ $name }}: {{ . }}</li>{{ end }}{{ end }}
  </ul>
  {{ end }}
</div>
{{end}}
```

For a browser the template renders inside the default layout (`layout-base`), as `c.Page` would.

### htmx and error statuses

htmx 4 swaps every response status except 204 and 304, and fires `htmx:response:error` for 4xx
and 5xx. An error fragment therefore lands in the request's target with no client
configuration. Use `hx-status:<code>` on an element to send a given status somewhere else:

```html
<form hx-post="/todos" hx-status:422="target:#form-errors">
```

## Validation errors and forms

`hyperchi.Bind` returns a 422 `HTTPError` whose `Fields` maps each field name to its messages.
Returning it as is gives the default rendering above. To re-render the form with the user's
input and inline messages, read the messages with `hyperchi.FieldErrors(err)`, which returns
nil for any other error, and answer with a fragment:

```go
type NewTodo struct {
    Title string `form:"title" validate:"required,max=120"`
}

app.Post("/todos", func(c *hyperchi.Context) error {
    in, err := hyperchi.Bind[NewTodo](c)
    if fields := hyperchi.FieldErrors(err); fields != nil {
        return c.Status(http.StatusUnprocessableEntity).Fragment("todo-form", hyperchi.H{
            "title":  c.FormValue("title"),
            "errors": fields,
        })
    }
    if err != nil {
        return err
    }
    todo, err := store.Add(c.Ctx(), in.Title)
    if err != nil {
        return err
    }
    return c.Status(http.StatusCreated).HX().Trigger("todo-added").Fragment("todo-row", todo)
})
```

A `Validate() error` method on the bound struct runs after the tag rules pass. Returning an
`*HTTPError` from it keeps that status and message; any other error becomes a 422 with the
error's text as the message.

## Replacing the default

`app.OnError` installs a function that handles every error a handler returns. Call
`app.DefaultError` from it for the cases it does not handle:

```go
app.OnError(func(c *hyperchi.Context, err error) {
    if errors.Is(err, database.ErrNoRows) {
        app.DefaultError(c, hyperchi.NotFound("Not found.").Wrap(err))
        return
    }
    var he *hyperchi.HTTPError
    if !errors.As(err, &he) || he.Code >= 500 {
        reportToTracker(c.Request(), err) // the application's own reporting
    }
    app.DefaultError(c, err)
})
```

The function has the type `hyperchi.ErrorFunc`, `func(c *Context, err error)`, and answers
with the same `Context` response methods a handler uses.

### Errors after the response started

A handler that returns an error after it already wrote a response (for example, a streaming
handler that fails halfway) cannot change the status. The error is logged and nothing more is
written.

## WebSocket handlers

A `SocketHandler` registered with `app.WebSocket` has no HTTP response to render an error
into. Returning `hyperchi.ErrSocketClosed`, or the error `s.Receive()` returned when the client
left, ends the socket normally. Any other error is logged with the path and connection ID and
closes the socket with an internal-error status. To show a validation failure, send it to the
client instead of returning it: `hyperchi.FieldErrors` works on `BindMessage` errors too, and
`s.Swap` re-renders the form.

## Panics

A panic is not a returned error. Install a recovery middleware so a panic becomes a 500 rather
than a dropped connection:

- `app.EnableErrorRecovery()` adds the error page handler's recovery middleware, which renders
  a panic through the error page handler described below.
- `app.Middleware.Recovery()`, part of `app.Middleware.Default()`, logs the panic and stack and
  answers with a plain 500 (with the panic text in development).

Like any `app.Use` middleware, add either before registering routes.

## The error page handler

`app.ErrorPageHandler` renders errors that carry no status for browsers, and panics caught by
`EnableErrorRecovery`. It captures an `ErrorDetails` for each error:

```go
type ErrorDetails struct {
    ID          string            // "err_<unix nanoseconds>", for correlating reports and logs
    Type        string            // the error's Go type
    Message     string
    Time        time.Time
    Request     *RequestInfo      // method, URL, remote address, user agent, headers, query, form
    StackTrace  []StackFrame      // development only
    SourceCode  *SourceContext    // development only: five lines either side of the failing line
    Environment map[string]string // Go version, OS, architecture, goroutines, memory
    Headers     map[string]string
}
```

- **Development** shows the error type, error ID, timestamp, message, and stack trace.
- **Production** shows a generic "Internal Server Error" page with the error ID and nothing
  else.
- Both log `ERROR [<id>] <type>: <message>`, the top stack frame, and the request line.

Development mode is on when `ENVIRONMENT` (or, if unset, `GO_ENV`) is empty, `development`, or
`dev`. Any other value, such as `production`, selects production behavior.

`app.SetErrorTemplate(tmpl)` replaces the development page with a custom `html/template`
source string that receives the `ErrorDetails`.

### What is captured

- Form fields whose names contain `password`, `passwd`, `secret`, `token`, `key`, `auth`, or
  `credential` are stored as `[REDACTED]`.
- Environment variables are captured by allowlist only: `ENVIRONMENT`, `GO_ENV`, `APP_ENV`,
  `PORT`, `TZ`, and `LANG`. Anything else, including credentials with custom names, is never
  copied.
- Request headers are copied as sent. Treat `ErrorDetails.Request.Headers` as sensitive when
  forwarding it to a reporting service.

## Testing error responses

```go
func TestMissingTodo(t *testing.T) {
    app := hyperchi.New()
    app.Get("/todos/{id}", showTodo)

    req := httptest.NewRequest(http.MethodGet, "/todos/999", nil)
    req.Header.Set("Accept", "application/json")
    w := httptest.NewRecorder()
    app.ServeHTTP(w, req)

    if w.Code != http.StatusNotFound {
        t.Fatalf("status = %d, want 404", w.Code)
    }
    // body: {"error":"No such todo."}
}
```

Set `HX-Request: true` instead of the `Accept` header to test the fragment branch.
