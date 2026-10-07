---
title: Errors
section: Core
order: 70
next: sessions
description: Return an error from a handler and HyperChi answers with the right status, in the right format for whoever asked. One pipeline handles handler errors, router misses, middleware refusals, and panics.
---

## Return it, do not write it {#overview}

A handler is `func(c *hyperchi.Context) error`. When something goes wrong, return an error instead of writing a response by hand. HyperChi turns it into a status code and a body, and it picks the body for the client that made the request.

The constructors in `hyperchi` each carry a status and a message that is safe to show:

| Constructor | Status |
| --- | --- |
| `BadRequest(msg)` | 400 |
| `Unauthorized(msg)` | 401 |
| `Forbidden(msg)` | 403 |
| `NotFound(msg)` | 404 |
| `Conflict(msg)` | 409 |
| `Unprocessable(msg, fields)` | 422, with per-field messages |
| `NewHTTPError(code, msg)` | any code |
| `Errorf(code, format, args...)` | any code, with a formatted message |

An empty message becomes the status text. Any error that is not an `*HTTPError` is a 500 with the generic text "Internal Server Error". Its real text is logged and never sent to the client.

`DefaultError` is what renders the error unless you replace it:

- **An API client** (an `Accept` header with `application/json` and without `text/html`) gets `{"error": "No such user."}`, plus a `"fields"` object for a validation failure. Put `hyperchi.JSONErrors` on a route, a group, or the app to force JSON whatever the client asks for.
- **An htmx request** gets your `error` template as a fragment, or when the app has none, a small `<div class="hc-error" role="alert">` with the message. A fragment keeps a failed request from replacing a target with a whole page.
- **A browser** gets the `error` template as a page, or a built-in page when the app has none. In development, a 500 with an underlying cause shows that cause in full. Production never does.

Every error response is sent with `Cache-Control: no-store` and `X-Robots-Tag: noindex`. If the handler had already started writing, the error is only logged, because a second response cannot replace the first.

## A handler that fails well {#example}

```go
app.Get("/users/{id}", func(c *hyperchi.Context) error {
    id, err := c.ParamInt("id")
    if err != nil {
        return hyperchi.BadRequest("The user id must be a number.")
    }
    user, err := db.Find(id)
    switch {
    case errors.Is(err, sql.ErrNoRows):
        return hyperchi.NotFound("No such user.")
    case err != nil:
        return hyperchi.Errorf(http.StatusServiceUnavailable, "could not load user %d: %w", id, err)
    }
    return c.View("user", hyperchi.H{"user": user})
})
```

Here `db.Find` is whatever lookup your app uses. The last case shows how `Errorf` keeps secrets out of responses. The `%w` operand becomes the logged cause and is left out of the message, so the client reads "could not load user 42" and your logs hold the database error. Format a value with `%v` or `%s` only when the client may read it.

For a status with no constructor, or to attach a cause to a message you wrote, use `NewHTTPError` and `Wrap`:

```go
app.Get("/report", func(c *hyperchi.Context) error {
    if _, err := os.ReadFile("report.csv"); err != nil {
        return hyperchi.NewHTTPError(http.StatusBadGateway, "The report is unavailable.").Wrap(err)
    }
    return nil
})
```

`errors.Is` and `errors.As` see through `Wrap`, so a middleware or an `OnError` hook can still inspect the cause. A bare `return err` from a handler is also fine: it is a 500, and the text stays in the log.

## A branded error page {#branded}

`app.OnError` replaces the renderer. Your function receives the context and the error, and it can call `c.App().DefaultError(c, err)` for the cases it does not handle. This one draws browser errors with the site's own template and leaves JSON and htmx clients on the default:

```go
app.OnError(func(c *hyperchi.Context, err error) {
    var he *hyperchi.HTTPError
    if !errors.As(err, &he) {
        he = hyperchi.NewHTTPError(http.StatusInternalServerError, "")
    }
    if c.IsHTMX() || wantsJSON(c) {
        c.App().DefaultError(c, err)
        return
    }
    data := hyperchi.H{"status": he.Code, "message": he.Message}
    if rerr := c.Status(he.Code).Page("error-page", data); rerr != nil {
        log.Printf("error page: %v", rerr)
        c.App().DefaultError(c, err)
    }
})

func wantsJSON(c *hyperchi.Context) bool {
    accept := c.Header("Accept")
    return strings.Contains(accept, "application/json") && !strings.Contains(accept, "text/html")
}
```

Two details matter. The message comes from `he.Message`, never from `err.Error()`, so a wrapped cause cannot reach the page. And if rendering the error page itself fails, the hook falls back to `DefaultError` instead of answering with an empty body. A `{{define "error-page"}}` template can read `.status` and `.message`. If you only want the framework's own page restyled, define a template named `error` instead and skip `OnError`: `DefaultError` uses it for browsers and as the htmx fragment, with `.status`, `.message`, and `.fields`.

## One pipeline for everything {#pipeline}

Errors that never pass through your handlers still come out the same way, so one `error` template or one `OnError` brands all of them:

- **Router misses.** An unknown path is a 404 and a known path with the wrong method is a 405.
- **Static misses.** A missing file under `StaticFS` or `StaticWithOptions` is a 404 from the same renderer.
- **Middleware refusals.** The framework's own middleware, such as a failed CSRF check (403) or `app.BasicAuth` (401), answers through the pipeline.
- **Recovered panics.** `Middleware.Recovery()` turns a panic into a 500. It is part of `AutoMiddleware` and `Production`, and `app.EnableErrorRecovery()` installs it on its own. A panic after the response has started aborts the connection instead of appending an error page to a half-sent body.

Your own `net/http` middleware joins in with `app.RespondStatus(w, r, code, err)`. It takes the status and an optional cause, and it renders through `OnError` or `DefaultError` like any other denial:

```go
func requireKey(app *hyperchi.HyperChi) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            if r.Header.Get("X-Api-Key") == "" {
                app.RespondStatus(w, r, http.StatusUnauthorized, nil)
                return
            }
            next.ServeHTTP(w, r)
        })
    }
}
```

Pass `nil` when the status says it all. Pass an error to log the cause: it is shown only if it is an `*HTTPError` with the same code, and otherwise stays in the log with the status text going to the client. Calling `http.Error` here instead would skip your branded page, the htmx fragment, and the JSON shape.

## What clients never see {#safe}

The rule is the same everywhere: the client reads `Message`, the log reads `Err`. A database error, a file path, or a stack trace should never be the message. Build messages from what the visitor can act on, wrap the cause with `Wrap` or `%w`, and let the 5xx log line carry the detail. In development the default page shows the cause so you can debug; set `ENVIRONMENT=production` for every deployment and it stops.

## Keep going {#next}

Validation failures are errors too, with a `fields` map; [Forms and validation](/docs/forms) shows how to render them beside each input. The [security page](/docs/security) covers the middleware that refuses requests, and the [routing guide](/docs/routing) shows the handlers that return all of this.
