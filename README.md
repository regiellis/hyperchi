# HyperChi

[![Go Reference](https://pkg.go.dev/badge/github.com/regiellis/hyperchi.svg)](https://pkg.go.dev/github.com/regiellis/hyperchi)
[![Go Report Card](https://goreportcard.com/badge/github.com/regiellis/hyperchi)](https://goreportcard.com/report/github.com/regiellis/hyperchi)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

**An HTMX framework for Go.**

HyperChi is a full-stack HTMX framework for Go. It bundles chi routing, middleware, an HTML
template system with 100+ filters, HTMX request/response negotiation, Server-Sent Events and
WebSocket hubs, a SQLite data layer, and security helpers. The `hyperchi` CLI that scaffolds and
runs projects ships as a separate module, so apps importing the framework never download the
CLI's dependencies. Requires Go 1.26 or newer; build and deploy with the latest 1.26 patch
release, which carries the standard library security fixes. The SQLite driver
(`modernc.org/sqlite`) is pure Go, so no C toolchain is needed and `CGO_ENABLED=0` builds work.

What it gives you:

- Full HTMX attribute and response-header coverage.
- SSE and WebSocket hubs for real-time communication.
- Advanced patterns: live feeds, progress bars, infinite scroll, live search, multi-step forms,
  and auto-save.
- AnimeJS animation helpers.
- Built-in validation and content schemas.
- Routing, middleware, templates, and 100+ template filters.

## Documentation

The guides live in [`examples/showcase/docs`](examples/showcase/docs), the source of the docs
site. Run `cd examples/showcase && go run .` and open `http://localhost:9999/docs` to read them
with search, or start with these:

- [Getting started](examples/showcase/docs/start.md) and the
  [guestbook tutorial](examples/showcase/docs/tutorial.md), one small app end to end.
- Core: [routing and responses](examples/showcase/docs/routing.md),
  [templates](examples/showcase/docs/templates.md),
  [forms and validation](examples/showcase/docs/forms.md), [errors](examples/showcase/docs/errors.md).
- Built-ins: [sessions and cookies](examples/showcase/docs/sessions.md),
  [static files and assets](examples/showcase/docs/assets.md),
  [realtime](examples/showcase/docs/realtime.md), [directives](examples/showcase/docs/directives.md).
- Production: [configuration](examples/showcase/docs/configuration.md),
  [security](examples/showcase/docs/security.md), [deploying](examples/showcase/docs/deploying.md),
  [testing](examples/showcase/docs/testing.md).
- [Example apps](examples/showcase/docs/apps.md): four reference applications with their source.

## HTMX coverage

### HTMX attributes

Template helpers write htmx attributes. Each returns a `template.HTMLAttr`, so it sits in tag
position next to the others:

```gotemplate
{{ hxGet "/api/data" }}                         hx-get="/api/data"
{{ hxPost "/api/submit" }}                      hx-post="/api/submit"
{{ hxTarget "#content" }}                       hx-target="#content"
{{ hxSwap "innerHTML" }}                        hx-swap="innerHTML"
{{ hxTrigger "click" }}                         hx-trigger="click"
{{ hxEncoding "multipart/form-data" }}          hx-encoding="multipart/form-data"
{{ hxDisabledElt "#submit-btn" }}               hx-disable="#submit-btn"
{{ hxSync "closest form:abort" }}               hx-sync="closest form:abort"
{{ hxValidate true }}                           hx-validate="true"
{{ hxVals (dict "key" "value") }}               hx-vals='{"key":"value"}'
{{ hxInclude "#form-data" }}                    hx-include="#form-data"
{{ hxSelect "#response-part" }}                 hx-select="#response-part"
{{ hxSelectOOB "#sidebar" }}                    hx-select-oob="#sidebar"
{{ hxReplaceURL "true" }}                       hx-replace-url="true"
{{ hxStatus "422" "target:#form-errors" }}      hx-status:422="target:#form-errors"
```

### Response headers
```go
// htmx response headers chain on c.HX() and end in a response method
app.Post("/items", func(c *hyperchi.Context) error {
    item, err := saveItem(c.Ctx(), c.FormValue("name"))
    if err != nil {
        return err
    }
    return c.HX().
        Retarget("#items").                                    // HX-Retarget
        Reswap("beforeend").                                   // HX-Reswap
        PushURL("/items").                                     // HX-Push-Url
        Trigger("showToast", hyperchi.H{"message": "Saved"}).  // HX-Trigger with detail
        Fragment("item-row", item)
})
```

`c.HX()` also has `Reselect` (HX-Reselect), `ReplaceURL` (HX-Replace-Url), `Location`
(HX-Location), `Refresh` (HX-Refresh), and `Empty()` for a response that only carries headers.
`c.Redirect(url)` sends `HX-Redirect` to htmx and a 303 to any other client. The request side
reads the same way: `c.IsHTMX()`, `c.HX().Target()`, `c.HX().Source()`, `c.HX().Boosted()`,
`c.HX().CurrentURL()`. The `response` package sets these headers on a plain
`http.ResponseWriter` for code that stays on net/http.

### Server-Sent Events (SSE)
```go
// Enable SSE extension
app.Extensions().EnableSSE("/events")

// Get SSE hub for broadcasting
sseHub := app.Extensions().GetSSEHub()

// Broadcast to channels
sseHub.Broadcast("dashboard", sse.SSEMessage{
    Event: "data-update",
    Data: map[string]interface{}{
        "users_online": 1500,
        "sales_today": 50000,
    },
})

// Broadcast HTML for HTMX
sseHub.BroadcastHTML("notifications", "new-message", `
    <div class="notification">New message received!</div>
`)

// Channel management
sseHub.Subscribe(connectionID, "user-notifications")
sseHub.Unsubscribe(connectionID, "user-notifications")
```

### WebSocket
```go
type ChatLine struct {
    User string `form:"user" validate:"required,max=40"`
    Text string `form:"text" validate:"required,max=500"`
}

// A WebSocket route: one handler serves a connection for its whole life
app.WebSocket("/chat/{room}", func(s *hyperchi.Socket) error {
    room := s.Context().Param("room") // s.Context() is the handshake request
    s.Join(room)
    for {
        msg, err := s.Receive() // matches hyperchi.ErrSocketClosed when the client leaves
        if err != nil {
            return err
        }
        line, err := hyperchi.BindMessage[ChatLine](msg) // same form and validate tags as Bind
        if fields := hyperchi.FieldErrors(err); fields != nil {
            if err := s.Swap("#chat-form", "outerHTML", "chat-form", hyperchi.H{"errors": fields}); err != nil {
                return err
            }
            continue
        } else if err != nil {
            return err
        }
        // Render once, swap into every socket in the room
        if err := app.Room(room).Swap("#feed", "beforeend", "chat-line", line); err != nil {
            return err
        }
    }
})

// Broadcast to a room from any handler or goroutine
app.Post("/announce", func(c *hyperchi.Context) error {
    if err := app.Room("lobby").Swap("#feed", "afterbegin", "announcement", hyperchi.H{
        "text": c.FormValue("text"),
    }); err != nil {
        return err
    }
    return c.NoContent()
})
```

```html
<script src="/hyperchi/js/hx-ws.js"></script>
<div hx-ws:connect="/chat/lobby">
    <div id="feed"></div>
    <form id="chat-form" hx-ws:send>
        <input name="user"> <input name="text">
        <button type="submit">Send</button>
    </form>
</div>
```

- A `Socket` sends to its own client with `s.Swap(target, swap, template, data)`,
  `s.SendHTML(target, swap, html)`, and `s.SendJSON(event, data)`; `s.Leave(room)` leaves a
  room, and `s.Wait()` holds a push-only socket open until the client leaves.
- `app.Room(name)` broadcasts with `Swap`, `SendHTML`, `SendJSON`, `Reload`, and `Redirect`.
- A message from the htmx 4 ws extension carries the form's values and the htmx request
  headers: `msg.Value("text")`, `msg.Header("HX-Source")`, or `msg.Bind(&v)`.
- The handshake runs through per-route middleware like any GET route, so auth middleware can
  guard it: `app.WebSocket("/chat/{room}", chat, security.RequireAuth(authHelper))`.
- `app.Extensions().EnableExtensionAssets()` (or `EnableAllExtensions()`) serves
  `/hyperchi/js/hx-ws.js`.

Sockets share one hub with `app.Extensions().EnableWebSocket("/ws")`, the hub-routed endpoint
that pages join by room (`hx-ws:connect="/ws?rooms=lobby"`) without a handler of their own.
Authorize that handshake in HTTP middleware. A client cannot join other rooms by message until
the app sets `GetWebSocketHub().SetRoomAuthorizer(func(conn, room) bool)`, and a client can
post only to rooms it joined, under the identity `SetIdentifyUser` gives it.
Both kinds of connection see the same rooms, so `app.Room(name)` reaches either. The hub
itself stays available for lower-level work:

```go
wsHub := app.Extensions().GetWebSocketHub()
wsHub.BroadcastToRoom("chat-room", websocket.WebSocketMessage{
    Type:  "message",
    Event: "new-message",
    Data:  map[string]any{"user": "john_doe", "message": "Hello everyone!"},
})
wsHub.JoinRoom(connectionID, "general-chat")
wsHub.LeaveRoom(connectionID, "general-chat")
```

The WebSocket hub enforces same-origin by default (Cross-Site WebSocket Hijacking protection),
and relaxes this in development. For production, build the app from `config.FromEnv()` with
`ENVIRONMENT=production` set (`hyperchi.New()` always uses the development defaults) and
configure origin patterns explicitly. Connection identity is set server-side; client-supplied `?user_id=` and
`X-User-ID` are never trusted.

### Response status handling
```go
// Answer with a status and swap the response into a different element
return c.Status(http.StatusUnprocessableEntity).
    HX().Retarget("#validation-errors").
    Fragment("form-errors", hyperchi.H{"fields": fields})
```

htmx 4 swaps every status except 204 and 304, so a 422 fragment lands without configuration.
Use `hx-status:<code>` in a template to change where a status goes:

```html
<form hx-post="/submit" hx-status:422="target:#validation-errors">
    <input name="email" type="email">
    <button type="submit">Submit</button>
</form>
```

## Advanced HTMX patterns

### Live feed
```go
pm := app.Extensions().GetPatternManager()

// Real-time dashboard updates
pm.LiveFeed("/dashboard/live", patterns.LiveFeedConfig{
    Channel: "dashboard",
    DataFunc: func() (interface{}, error) {
        return getDashboardMetrics(), nil
    },
    Template: "dashboard-feed.html",
})
```

### Progress bar
```go
// Real-time progress tracking
pm.ProgressBar("/upload/progress", patterns.ProgressBarConfig{
    TaskID: "upload-123",
    Channel: "upload-progress",
    ProgressFunc: func(taskID string) (int, error) {
        return getUploadProgress(taskID), nil
    },
    Template: "progress-bar.html",
})
```

### Multi-step form
```go
// A wizard that keeps answers in the session (needs app.UseSessions and
// app.SessionMiddleware()). Each step is validated; Complete gets every answer.
pm.MultiStepForm("/onboarding", patterns.MultiStepFormConfig{
    Steps: []patterns.FormStep{
        {Name: "personal", Title: "Personal Info", Template: "step-personal.html",
            Fields: []string{"name", "email"}, Validate: patterns.SchemaStep(personalSchema)},
        {Name: "preferences", Title: "Preferences", Template: "step-preferences.html",
            Fields: []string{"theme"}},
        {Name: "confirmation", Title: "Confirm", Template: "step-confirmation.html"},
    },
    SuccessURL: "/dashboard",
    Complete: func(r *http.Request, answers url.Values) error {
        return createAccount(r.Context(), answers)
    },
})
// Step templates read {{.values.Get "email"}} and {{index .errors "email"}}.
```

### Infinite scroll
```go
// Paginated content loading
pm.InfiniteScroll("/posts", patterns.InfiniteScrollConfig{
    DataFunc: func(page int, pageSize int) (interface{}, error) {
        return getPaginatedPosts(page, pageSize), nil
    },
    Template: "posts-page.html",
    PageSize: 20,
    HasMoreFunc: func(page int) bool {
        return hasMorePosts(page)
    },
})
```

### Live search
```go
// Real-time search with debouncing
pm.LiveSearch("/search", patterns.LiveSearchConfig{
    SearchFunc: func(query string, limit int) (interface{}, error) {
        return searchContent(query, limit), nil
    },
    Template: "search-results.html",
    MinQueryLen: 2,
    MaxResults: 10,
})
```

### Auto-save
```go
// Automatic form draft saving
pm.AutoSave("/forms/auto-save", patterns.AutoSaveConfig{
    SaveFunc: func(data map[string]interface{}) error {
        return saveDraft(data)
    },
    DraftKey: "article_draft",
})
```

## Template system

### HTMX template filters
```html
<!-- Core HTMX attributes -->
<button {{ hxGet "/api/data" }} 
        {{ hxTarget "#content" }} 
        {{ hxSwap "innerHTML" }}
        {{ hxTrigger "click" }}
        {{ hxIndicator "#spinner" }}>
    Load Data
</button>

<!-- Advanced HTMX attributes -->
<form {{ hxPost "/submit" }}
      {{ hxEncoding "multipart/form-data" }}
      {{ hxDisabledElt "#submit-btn" }}
      {{ hxValidate true }}>
    
    <input name="name" {{ hxInclude "#email-field" }}>
    <input name="email" id="email-field">
    <input name="photo" type="file">
    
    <button type="submit" id="submit-btn">Submit</button>
</form>

<!-- Server-Sent Events: BroadcastSwap content lands in this element -->
<div hx-sse:connect="/events?channels=updates,notifications"
     hx-swap="innerHTML"
     id="live-content">
    Loading live updates...
</div>

<!-- WebSocket: a hub-routed connection that joins a room -->
<div hx-ws:connect="/ws?rooms=chat-general" id="chat-messages">
    Chat messages will appear here...
</div>
```

htmx 4 registers an extension when its script loads, so these need the
`/hyperchi/js/hx-sse.js` and `/hyperchi/js/hx-ws.js` scripts and no `hx-ext` marker.

### 100+ template filters

A pipeline passes its value as the last argument, and most multi-argument filters take the
value first, so call those directly. [Template Filters](hyperchi/filters/README.md) has the
calling conventions.

```html
<!-- Strings -->
<h1>{{ .title | title }}</h1>
<p>{{ truncate .content 100 }}</p>
<div>{{ slug .title }}</div>
<span>{{ pluralize "item" .count }}</span>

<!-- Dates: dateFormat takes the format first, so it pipes -->
<time>{{ .createdAt | dateFormat "Jan 2, 2006" }}</time>
<span>{{ timeAgo .updatedAt }}</span>

<!-- Numbers -->
<span class="price">{{ currency .price }}</span>
<span class="size">{{ bytes .fileSize }}</span>
<span class="count">{{ humanize .views }}</span>

<!-- Collections -->
{{ range take (reverse (sortBy .posts "createdAt")) 5 }}<li>{{ .title }}</li>{{ end }}
{{ range unique .tags }}<span class="tag">{{ . }}</span>{{ end }}
{{ range chunk .items 3 }}<div class="row">{{ range . }}{{ . }}{{ end }}</div>{{ end }}

<!-- JSON -->
<div data-settings="{{ toJSON .settings }}"></div>

<!-- HTML -->
<div class="{{ classNames "card" (ternary .active "active" "") }}">
    {{ nl2br .content }}
</div>
```

## Quick start

### Installation

Add the framework to an app:

```bash
go get github.com/regiellis/hyperchi
```

Install the CLI (its own module, `github.com/regiellis/hyperchi/cmd/hyperchi`):

```bash
go install github.com/regiellis/hyperchi/cmd/hyperchi@latest
hyperchi new my-app
```

No C toolchain is needed: the SQLite driver is pure Go, so `CGO_ENABLED=0` and cross-compiling
both work.

### Handlers and responses

Every route is a `func(c *hyperchi.Context) error`. The handler reads the request through `c`
and returns a response; HyperChi handles htmx negotiation, layouts, and error rendering.

```go
package main

import (
    "log"
    "net/http"
    "path/filepath"

    "github.com/regiellis/hyperchi/hyperchi"
)

type NewTodo struct {
    Title string `form:"title" validate:"required,max=120"`
}

func main() {
    app := hyperchi.New()
    if err := app.LoadTemplates(filepath.Join("templates", "**", "*.tmpl")); err != nil {
        log.Fatal(err)
    }
    app.SetGlobal("site_name", "Todos") // every template sees it; handlers never repeat it

    // A page: the full layout for a browser, the template alone for an htmx partial request.
    app.Get("/todos", func(c *hyperchi.Context) error {
        todos, err := store.List(c.Ctx())
        if err != nil {
            return err // a 500; the error text is logged, never shown
        }
        return c.View("todos", hyperchi.H{"todos": todos})
    })

    // A returned HTTPError picks the status.
    app.Get("/todos/{id}", func(c *hyperchi.Context) error {
        id, err := c.ParamInt("id") // a non-integer id is already a 400
        if err != nil {
            return err
        }
        todo, err := store.Find(c.Ctx(), id)
        if err != nil {
            return hyperchi.NotFound("No such todo.")
        }
        return c.View("todo", todo)
    })

    // Form input: Bind decodes and validates; c.HX() sets htmx response headers.
    app.Post("/todos", func(c *hyperchi.Context) error {
        in, err := hyperchi.Bind[NewTodo](c) // 400 if unreadable, 422 with field messages if invalid
        if err != nil {
            return err
        }
        todo, err := store.Add(c.Ctx(), in.Title)
        if err != nil {
            return err
        }
        return c.Status(http.StatusCreated).
            HX().Trigger("todo-added").
            Fragment("todo-row", todo)
    })

    log.Fatal(app.Serve(":8080"))
}
```

`store` stands in for the application's data layer.

- **Request:** `c.Param`, `c.ParamInt`, `c.Query`, `c.QueryDefault`, `c.QueryInt`,
  `c.Queries`, `c.FormValue`, `c.Header`, `c.Cookie`, `c.Ctx()`, `c.Session()`, `c.UserID()`,
  `c.IsHTMX()`. `hyperchi.Bind[T](c)` decodes JSON by `json` tags and forms, multipart forms,
  and query strings by `form` tags, then applies `validate` tags: `required`, `min=N`,
  `max=N`, `email`, `url`, `oneof=a b c`, plus an optional `Validate() error` method.
- **Response:** `c.View` (layout for pages, bare for htmx partials), `c.Page` (always the
  layout), `c.Fragment` (never the layout), `c.JSON`, `c.Text`, `c.HTML`, `c.Redirect`
  (HX-Redirect for htmx, 303 otherwise), `c.NoContent`. `c.Status`, `c.Layout`, and `c.HX()`
  chain in front of them. Renders finish before any header is sent, so a template error never
  leaves a half-written page.
- **Errors:** return `hyperchi.NotFound`, `BadRequest`, `Unauthorized`, `Forbidden`,
  `Conflict`, `Unprocessable`, or `hyperchi.Errorf(code, ...)`. The error renders for the
  client that asked: JSON for an API client, the `error` template (or a small alert) for htmx,
  an error page for a browser. Any other error is a 500. `app.OnError` replaces the default;
  see [Error Handling](docs/ERROR_HANDLING.md).
- **Middleware** is plain `func(http.Handler) http.Handler`: `app.Use(...)` for every route,
  `app.Group("/admin", func(g *hyperchi.HyperChi) { g.Use(auth); g.Get("/", dash) })` for a
  prefix, or pass it after the handler for one route: `app.Post("/upload", upload, auth)`.
- **WebSockets** are routes too: `app.WebSocket(pattern, func(s *hyperchi.Socket) error {...})`
  serves one connection, and `app.Room(name)` broadcasts to every socket in a room. See
  [WebSocket](#websocket).
- **Plain net/http** stays reachable for code that already speaks it:
  `app.Handle(method, pattern, http.Handler)`, `hyperchi.WrapHTTP(h)`, `app.ToHTTP(handler)`,
  and `app.Router()` for the chi router.

### HTMX example
```go
package main

import (
    "log"
    "net/http"
    "time"

    "github.com/regiellis/hyperchi/hyperchi"
    "github.com/regiellis/hyperchi/hyperchi/patterns"
    "github.com/regiellis/hyperchi/hyperchi/security"
)

type Contact struct {
    Name  string `form:"name" validate:"required,max=80"`
    Email string `form:"email" validate:"required,email"`
}

func main() {
    app := hyperchi.New()

    // Middleware first: chi rejects app.Use after routes exist
    app.Use(app.Middleware.Default()...)

    // Load templates with HTMX filters
    if err := app.LoadTemplates("templates/*.html"); err != nil {
        log.Fatal(err)
    }

    // SSE at /sse, WebSocket at /ws, and the extension scripts
    app.Extensions().EnableAllExtensions()

    // === Security & Authentication ===

    // File upload security
    uploadConfig := security.DefaultUploadConfig()
    uploadConfig.MaxFileSize = 5 * 1024 * 1024 // 5MB

    // RequireAuth takes a TokenValidator; *AuthHelper implements it.
    authHelper := security.NewAuthHelper()

    // Per-route middleware runs in order: authenticate, then validate the upload.
    app.Post("/upload", uploadHandler,
        security.RequireAuth(authHelper),
        security.UploadMiddleware(uploadConfig),
    )

    // === Server-Sent Events Example ===

    // Render the stats panel every two seconds and swap it into every page on the
    // "dashboard" channel
    sseHub := app.Extensions().GetSSEHub()
    go func() {
        ticker := time.NewTicker(2 * time.Second)
        defer ticker.Stop()

        for range ticker.C {
            html, err := app.RenderToString("live-stats", hyperchi.H{
                "users_online": 1500,
                "sales_today":  50000,
                "timestamp":    time.Now().Format("15:04:05"),
            })
            if err != nil {
                log.Printf("live-stats: %v", err)
                continue
            }
            sseHub.BroadcastSwap("dashboard", string(html))
        }
    }()

    // === WebSocket Example ===

    // Render the message once and swap it into every page in the "general" room
    app.Post("/chat/send", func(c *hyperchi.Context) error {
        if err := app.Room("general").Swap("#chat-messages", "beforeend", "chat-message", hyperchi.H{
            "username":  c.FormValue("username"),
            "message":   c.FormValue("message"),
            "timestamp": time.Now().Format("15:04:05"),
        }); err != nil {
            return err
        }
        c.HX().Trigger("showToast", hyperchi.H{"level": "success", "message": "Message sent"})
        return c.Text("Message sent")
    })

    // === Advanced Patterns ===

    pm := app.Extensions().GetPatternManager()

    // Live feed
    pm.LiveFeed("/dashboard/live", patterns.LiveFeedConfig{
        Channel: "dashboard",
        DataFunc: func() (any, error) {
            return getDashboardData(), nil
        },
        Template: "live-feed.html",
    })

    // Progress bar
    pm.ProgressBar("/upload/progress", patterns.ProgressBarConfig{
        TaskID:       "upload-123",
        Channel:      "upload-progress",
        ProgressFunc: getUploadProgress,
        Template:     "progress-bar.html",
    })

    // === Routes ===

    // Main page
    app.Get("/", func(c *hyperchi.Context) error {
        return c.View("index", hyperchi.H{
            "title": "HTMX Demo",
            "features": []string{
                "Server-Sent Events",
                "WebSocket Communication",
                "Response Targets",
                "Authentication & Sessions",
                "File Upload Security",
                "Content Management",
                "Live Feeds",
                "Progress Bars",
                "Multi-step Forms",
                "Infinite Scroll",
                "Live Search",
            },
        })
    })

    // Dashboard with real-time updates
    app.Get("/dashboard", func(c *hyperchi.Context) error {
        return c.View("dashboard", hyperchi.H{
            "title": "Live Dashboard",
            "initial_stats": hyperchi.H{
                "users_online": 1500,
                "sales_today":  50000,
            },
        })
    })

    // Chat room
    app.Get("/chat", func(c *hyperchi.Context) error {
        return c.View("chat", hyperchi.H{"title": "Real-time Chat", "room": "general"})
    })

    // Form with validation. A failed Bind returns a 422 that htmx swaps into
    // #validation-errors (see hx-status:422 in the template below).
    app.Post("/forms/submit", func(c *hyperchi.Context) error {
        in, err := hyperchi.Bind[Contact](c)
        if err != nil {
            return err
        }
        return c.HX().
            Retarget("#success-message").
            Trigger("showToast", hyperchi.H{"level": "success", "message": "Form submitted"}).
            Fragment("contact-thanks", in)
    })

    // API status endpoint
    app.Get("/api/status", func(c *hyperchi.Context) error {
        return c.JSON(hyperchi.H{
            "status":     "ok",
            "timestamp":  time.Now().Unix(),
            "extensions": app.Extensions().GetRegistry().GetInfo(),
        })
    })

    log.Fatal(app.Serve(":8080"))
}

// Files arrive already validated by UploadMiddleware.
func uploadHandler(c *hyperchi.Context) error {
    result := security.GetValidationResult(c.Request())
    if result == nil || !result.Valid {
        return hyperchi.BadRequest("Upload validation failed.")
    }
    return c.JSON(hyperchi.H{"uploaded": len(result.Files)})
}

func getDashboardData() map[string]any {
    return map[string]any{
        "active_users": 750,
        "new_orders":   15,
        "timestamp":    time.Now().Format("15:04:05"),
    }
}

func getUploadProgress(taskID string) (int, error) {
    return 75, nil // simulated progress
}
```

`index`, `dashboard`, `live-stats`, `chat`, `chat-message`, and `contact-thanks` are application templates; `c.View("dashboard", ...)`
finds `dashboard.html` because template lookups fall back across `.tmpl`, `.html`, `.htm`, and
`.gohtml`.

### Template example
```html
<!-- templates/dashboard.html -->
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>{{ .title }}</title>
    <script src="https://cdn.jsdelivr.net/npm/htmx.org@4.0.0/dist/htmx.min.js"></script>
    
    <!-- Load HTMX extensions -->
    <script src="/hyperchi/js/hx-sse.js"></script>
    <script src="/hyperchi/js/hx-ws.js"></script>
</head>
<body>
    <div class="dashboard">
        <h1>{{ .title }}</h1>
        
        <!-- Server-Sent Events Dashboard: each broadcast replaces the panel -->
        <div hx-sse:connect="/sse?channels=dashboard"
             hx-swap="innerHTML"
             id="live-stats">
            <div class="stats">
                <div class="stat">
                    <h3>Users Online</h3>
                    <span class="value">{{ .initial_stats.users_online }}</span>
                </div>
                <div class="stat">
                    <h3>Sales Today</h3>
                    <span class="value">{{ currency .initial_stats.sales_today }}</span>
                </div>
            </div>
        </div>
        
        <!-- WebSocket Chat -->
        <div class="chat-section">
            <h2>Live Chat</h2>
            <div hx-ws:connect="/ws?rooms=general"
                 id="chat-messages"
                 class="chat-messages">
                <!-- Messages appear here -->
            </div>
            
            <form {{ hxPost "/chat/send" }}
                  {{ hxTarget "#chat-status" }}>
                <input name="username" placeholder="Your name" required>
                <input name="message" placeholder="Type a message..." required>
                <button type="submit">Send</button>
            </form>
            <div id="chat-status"></div>
        </div>
        
        <!-- Live Feed -->
        <div class="live-feed">
            <h2>Live Updates</h2>
            <div {{ hxGet "/dashboard/live" }}
                 {{ hxTrigger "load, every 5s" }}
                 {{ hxSwap "innerHTML" }}
                 id="dashboard-feed">
                Loading live updates...
            </div>
        </div>
        
        <!-- Progress Bar -->
        <div class="upload-section">
            <h2>Upload Progress</h2>
            <div {{ hxGet "/upload/progress" }}
                 {{ hxTrigger "load, every 1s" }}
                 {{ hxSwap "innerHTML" }}
                 id="upload-progress">
                Loading progress...
            </div>
        </div>
        
        <!-- Advanced Form with Response Targets -->
        <div class="form-section">
            <h2>Contact Form</h2>
            
            <div id="validation-errors"></div>
            <div id="success-message"></div>
            
            <form {{ hxPost "/forms/submit" }}
                  {{ hxStatus "422" "target:#validation-errors" }}>
                <div class="field">
                    <label for="name">Name:</label>
                    <input type="text" id="name" name="name" required>
                </div>
                <div class="field">
                    <label for="email">Email:</label>
                    <input type="email" id="email" name="email" required>
                </div>
                <button type="submit">Submit</button>
            </form>
        </div>
    </div>
</body>
</html>
```

## Architecture

### Extensions system
```
┌─────────────────────────────────────────────────────────┐
│                 HyperChi Extensions                      │
├─────────────────────────────────────────────────────────┤
│  Server-Sent Events Extension                           │
│  ├─ Connection Management                               │
│  ├─ Channel Broadcasting                                │
│  ├─ Auto-reconnection                                   │
│  └─ Template Integration                                │
├─────────────────────────────────────────────────────────┤
│  WebSocket Extension                                    │
│  ├─ Room Management                                     │
│  ├─ Message Broadcasting                                │
│  ├─ Connection Lifecycle                                │
│  └─ HTMX Integration                                    │
├─────────────────────────────────────────────────────────┤
│  Response Targets Extension                             │
│  ├─ Dynamic Retargeting                                 │
│  ├─ Multiple Target Support                             │
│  └─ Template Helpers                                    │
├─────────────────────────────────────────────────────────┤
│  Advanced Patterns                                      │
│  ├─ Live Feeds                                          │
│  ├─ Progress Bars                                       │
│  ├─ Multi-step Forms                                    │
│  ├─ Infinite Scroll                                     │
│  ├─ Live Search                                         │
│  └─ Auto-save                                           │
└─────────────────────────────────────────────────────────┘
```

### What's included

- Core HTMX attributes and response headers.
- SSE broadcasting with channel management.
- Room-based WebSocket support.
- Dynamic response retargeting.
- 100+ HTMX-aware template filters.
- Advanced patterns: live feeds, progress bars, multi-step forms, infinite scroll, live search,
  auto-save.
- Session-based authentication with middleware.
- File upload validation.
- Content loading and collections.

## Security & authentication

### Session-based authentication
```go
// Configure authentication
userRepo := &MyUserRepository{}        // Implement UserRepository interface
authHelper := security.NewAuthHelper() // Issues and validates session tokens

// Apply authentication middleware to protected routes.
// RequireAuth takes a TokenValidator; *AuthHelper implements it. In production,
// back authHelper with a durable store via authHelper.SetSessionStore(...) so
// tokens issued at login resolve here.
app.Group("/dashboard", func(g *hyperchi.HyperChi) {
    g.Use(security.RequireAuth(authHelper))
    g.Get("/", dashboardHandler)
    g.Get("/profile", profileHandler)
})
app.Post("/login", loginHandler(userRepo, authHelper))

// In handlers, read the authenticated user
func dashboardHandler(c *hyperchi.Context) error {
    userID := c.UserID()
    if userID == "" {
        return hyperchi.Unauthorized("Please sign in.")
    }
    return c.View("dashboard", hyperchi.H{"user_id": userID})
}

// Login endpoint. The authenticator shares authHelper, so the tokens it issues
// are the ones RequireAuth(authHelper) accepts.
func loginHandler(userRepo security.UserRepository, authHelper *security.AuthHelper) hyperchi.Handler {
    auth := security.NewAuthenticator(userRepo, authHelper)
    return func(c *hyperchi.Context) error {
        result := auth.AuthenticateUser(security.ExtractLoginAttempt(c.Request()))
        if !result.Success {
            return hyperchi.Unauthorized(result.Error)
        }

        c.SetCookie(&http.Cookie{
            Name:     "session",
            Value:    result.Token,
            Path:     "/",
            HttpOnly: true,
            Secure:   true,
            SameSite: http.SameSiteStrictMode,
            MaxAge:   86400, // 24 hours
        })
        return c.Redirect("/dashboard") // HX-Redirect for htmx, 303 otherwise
    }
}
```

### File upload security
```go
// Configure secure file uploads
uploadConfig := &security.FileUploadConfig{
    MaxFileSize:       10 * 1024 * 1024, // 10MB
    AllowedTypes:      []string{"image/jpeg", "image/png", "application/pdf"},
    AllowedExtensions: []string{".jpg", ".png", ".pdf"},
    MaxFiles:          5,
    RequireAuth:       true,
}

// Reuse the AuthHelper that issues login tokens. Per-route middleware runs in
// order, so authentication establishes the user context upload validation requires.
app.Post("/upload", uploadHandler,
    security.RequireAuth(authHelper),
    security.UploadMiddleware(uploadConfig),
)

// Handle validated uploads
func uploadHandler(c *hyperchi.Context) error {
    // Files are already validated by the middleware
    result := security.GetValidationResult(c.Request())
    if result == nil || !result.Valid {
        return hyperchi.BadRequest("Upload validation failed.")
    }

    for _, file := range result.Files {
        log.Printf("validated file: %s (size: %d, type: %s)",
            file.SafeFilename, file.Size, file.ContentType)
        // Save file using file.SafeFilename
    }

    c.HX().Trigger("showToast", hyperchi.H{"level": "success", "message": "Upload complete"})
    return c.Text(fmt.Sprintf("Uploaded %d files", len(result.Files)))
}

// Manual validation (without middleware)
func customUploadHandler(c *hyperchi.Context) error {
    result := security.NewUploadValidator(uploadConfig).ValidateUpload(c.Request())
    if !result.Valid {
        return hyperchi.BadRequest(strings.Join(result.Errors, "; "))
    }
    // Process result.Files...
    return c.NoContent()
}
```

### Security helpers
```go
// Password hashing and validation
hashedPassword, err := security.HashPassword("user_password")
if err != nil {
    log.Fatal(err)
}

// Verify password
valid := security.CheckPasswordHash("user_password", hashedPassword)

// Generate secure tokens
token, err := security.GenerateSecureToken(32)
if err != nil {
    log.Fatal(err)
}

// Timing-safe string comparison
isValid := security.TimingSafeEqual(providedToken, expectedToken)

// Password strength validation
strength := security.CheckPasswordStrength("user_password")
if strength < security.StrengthMedium {
    fmt.Printf("Weak password: %s", strength)
}
```

## Content management

### Content loading
```go
// Initialize content layer
contentConfig := &content.ContentConfig{
    BaseDir:         "./content",
    WatchForChanges: true, // Auto-reload in development
}

contentLayer := content.NewContentLayer(contentConfig)

// Load content from filesystem
err := contentLayer.LoadContent()
if err != nil {
    log.Fatal("Failed to load content:", err)
}

// Query content in a handler
app.Get("/blog", func(c *hyperchi.Context) error {
    posts, ok := contentLayer.GetCollection("posts")
    if !ok {
        return hyperchi.NotFound("No posts collection.")
    }

    // Filter, sort, and paginate through the collection query API
    result, err := posts.Query(content.ContentQuery{
        Where:  map[string]any{"published": true},
        Sort:   []content.SortCriteria{{Field: "date", Order: "desc"}},
        Limit:  10,
        Offset: (c.QueryInt("page", 1) - 1) * 10,
    })
    if err != nil {
        return err
    }

    return c.View("blog", hyperchi.H{
        "posts": result.Contents,
        "total": result.Total,
        "page":  result.Page,
    })
})

// Use in templates (Content exposes Slug, Body, HTML, CreatedAt, and a Data map)
// {{ range .posts }}
//   <article>
//     <h2>{{ .Data.title }}</h2>
//     <time>{{ .CreatedAt | date "Jan 2, 2006" }}</time>
//     <div>{{ .HTML }}</div>
//   </article>
// {{ end }}
```

## Directory structure

```
hyperchi/
├── cmd/hyperchi/                 # hyperchi CLI: its own module (cobra; package main)
│   ├── go.mod                    # github.com/regiellis/hyperchi/cmd/hyperchi
│   ├── hyperchi-cli.go           # CLI entrypoint and subcommands
│   ├── _scaffold/, _site/        # Templates rendered by `hyperchi new`
│   └── assets/                   # Compiled JS bundles embedded in the CLI
├── hyperchi/                     # Framework library
│   ├── hyperchi.go               # HyperChi type and constructors
│   ├── routes.go, serve.go       # Route groups and middleware, listening and shutdown
│   ├── templates.go, render.go   # Template loading and filters, the render pipeline
│   ├── middleware_group.go       # Built-in middleware (app.Middleware)
│   ├── scheduler.go              # Schedule, Every, Daily
│   ├── handler.go, context.go    # Handler type, routes, request accessors
│   ├── respond.go, httperror.go  # Responses, HX headers, HTTPError, DefaultError
│   ├── bind.go                   # Bind: decode and validate request input
│   ├── integration.go            # Extensions wiring
│   ├── extensions/               # HTMX extensions
│   │   ├── sse/                  # Server-Sent Events hub
│   │   └── websocket/            # WebSocket hub
│   ├── patterns/                 # Live feeds, progress, forms, scroll, search
│   ├── filters/                  # Template filters
│   ├── response/                 # HTMX response helpers
│   ├── content/                  # Content management
│   ├── validator/                # Validation system
│   ├── security/                 # Auth, CSRF, upload validation, crypto
│   ├── database/                 # SQLite data layer
│   └── server/                   # Graceful shutdown / lifecycle
├── modules/                      # Separate Go modules (performance, pocketbase, jet, templ, markdown, gomponents)
├── frontend/                     # Frontend assets (HTMX extension scripts)
├── examples/                     # showcase (docs site), dice-arena, studio, mailgrid, homelab-search, streamer demos
├── tests/                        # Integration test suites
├── docs/                         # Documentation
└── README.md                     # This file
```

## Development

This repo is driven by `Taskfile.yml` via `task`; prefer it over raw `go`. Run `task --list` to
see everything. It holds two Go modules: the framework at the root and the CLI in
`cmd/hyperchi`. The CLI's `go.mod` replaces the framework with this checkout (`../..`), so a CLI
built here always uses the framework source next to it. Run Go commands for the CLI from its
directory (`go -C cmd/hyperchi test ./...`).

```bash
# Build the hyperchi CLI to bin/hyperchi (bakes in this checkout's path, so
# `hyperchi new` wires a local replace into generated projects)
task cli

# Install that development CLI to $GOBIN
task cli:install

# Scaffold and run a demo project
task scaffold

# Run both modules' tests
task test

# Build, vet, and race-test both modules; lint both
task check
task lint

# Production build (frontend assets + CLI binary)
task build
```

Run an example directly (never `go build` inside an example directory):

```bash
# Single-server example
cd examples/streamer
go run main.go

# Dual-server example (Go backend on :8080, Vite on :3000)
cd examples/streamer-vite
npm run dev:all
```

## Documentation

The [showcase integration examples](examples/showcase/INTEGRATIONS.md) include
three complete marketing pages: **templ + htmx** (studio project estimates),
**Jet + htmx** (SaaS pricing), and **Alpine.js + JSON** (an outdoor trip finder).
Run `cd examples/showcase && go run .`, then open
`http://localhost:9999/docs/integrations` for the guides and live pages.
The showcase pins Go 1.26.8; like the framework, it needs no C toolchain.

- [Template Filters](hyperchi/filters/README.md): calling conventions, recipes, and the live `/docs/filters` reference.
- [HTMX Version Support](docs/HTMX_ROADMAP.md): htmx 4 integration notes.
  [docs/HTMX.md](docs/HTMX.md) is an archived htmx 2 reference.
- [Fragments and Layouts](docs/FRAGMENTS_AND_LAYOUTS.md): fragment and layout rendering.
- [Context System](docs/CONTEXT_SYSTEM.md): request context and data flow.
- [Error Handling](docs/ERROR_HANDLING.md): error patterns.
- [Production](docs/PRODUCTION.md): configuration, security headers, rate limits, and serving.
- [Animation](docs/ANIMATION.md): AnimeJS integration.

### API reference

- [Core API](https://pkg.go.dev/github.com/regiellis/hyperchi/hyperchi)
- [Extensions API](https://pkg.go.dev/github.com/regiellis/hyperchi/hyperchi/extensions)
- [Patterns API](https://pkg.go.dev/github.com/regiellis/hyperchi/hyperchi/patterns)
- [Response API](https://pkg.go.dev/github.com/regiellis/hyperchi/hyperchi/response)

## Deployment

HyperChi apps build to a single static binary. The SQLite driver is pure Go, so no C
toolchain is needed and the binary cross-compiles with `GOOS`/`GOARCH`.

```bash
# Build your app
CGO_ENABLED=0 go build -o app .

# Run it
./app
```

Build production apps from `config.FromEnv()` with `NewWithConfigE`, as the scaffold does, and
set `ENVIRONMENT=production` wherever they run; [Production](docs/PRODUCTION.md) covers the
settings.

### Docker

Templates and static files load from disk at runtime, so the image carries them next to the
binary. For a scaffolded app, run `npm run build` first so `dist/` exists.

```dockerfile
FROM golang:1.26-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o app .

FROM alpine:3.24
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=builder /app/app .
COPY --from=builder /app/templates ./templates
COPY --from=builder /app/static ./static
COPY --from=builder /app/dist ./dist
ENV ENVIRONMENT=production
EXPOSE 8080
CMD ["./app"]
```

`hyperchi deploy init` writes a fuller Dockerfile (non-root user, health check) and compose,
systemd, and nginx files into a project; `hyperchi deploy docker` builds the image.

## Roadmap

Planned areas, not yet implemented:

- Image optimization pipeline (resize, format conversion, responsive srcset).
- Sitemap and RSS/Atom feed generation.
- Developer toolbar (inspector, route list, logs).
- Smart prefetching (viewport and hover strategies).
- Content collections v2 (typed schemas, relations, code generation).
- Internationalization, analytics hooks, and deployment adapters.

The framework is hardening ahead of a stable release; see `docs/CODE_REVIEW.md` for the
current known-issues report.

## Contributing

1. Fork the repository.
2. Create a feature branch (`git checkout -b feature/your-change`).
3. Write tests for your changes.
4. Ensure the suite passes (`task test`).
5. Update documentation.
6. Commit using Conventional Commits (`feat:`, `fix:`, `docs:`).
7. Push the branch and open a pull request.

## License

MIT. See the [LICENSE](LICENSE) file for details.

## Acknowledgments

- Built on [HTMX](https://htmx.org/) and the [Chi](https://github.com/go-chi/chi) router.
- WebSocket support via [coder/websocket](https://github.com/coder/websocket).
- SQLite via [modernc.org/sqlite](https://gitlab.com/cznic/sqlite), a pure-Go port.
