---
title: Getting started
section: Learn
order: 10
next: tutorial
description: An idea, a Go handler, and a page in the browser. Start with the smallest useful HyperChi application.
---

## Before you begin {#overview}

Use Go 1.26 or later. HyperChi's SQLite driver is pure Go (`modernc.org/sqlite`), so you need no C compiler, and applications build and cross-compile with `CGO_ENABLED=0`.

```sh
mkdir hello-hyperchi
cd hello-hyperchi
go mod init example.com/hello-hyperchi
go get github.com/regiellis/hyperchi
mkdir templates
```

## Your first page {#example}

Create `main.go`. Load your templates, register a page route, and start the server.

```go
package main

import (
    "github.com/regiellis/hyperchi/hyperchi"
    "github.com/regiellis/hyperchi/hyperchi/helpers"
)

func main() {
    app := hyperchi.New()
    helpers.Must(app.LoadTemplates("templates/*.tmpl"))

    app.Get("/", func(c *hyperchi.Context) error {
        return c.View("hello.tmpl", hyperchi.H{
            "message": "Hello from HyperChi.",
        })
    })

    helpers.Must(app.Serve("127.0.0.1:8080"))
}
```

Create `templates/hello.tmpl`. The handler’s data is available through standard Go template expressions.

```html
<!doctype html>
<html lang="en">
  <head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <title>Hello, HyperChi</title>
  </head>
  <body>
    <h1>{{ .message }}</h1>
  </body>
</html>
```

Start the app, then open `http://localhost:8080`.

```sh
go mod tidy
go run .
```

> Every route is a `func(c *hyperchi.Context) error`. `c.View` renders the named template with the handler’s data and returns any render error to HyperChi, which answers with an error page instead of half a response.

Prefer a generated project? Install the CLI with `go install github.com/regiellis/hyperchi/cmd/hyperchi@latest`, then run `hyperchi new my-app` for a project with templates, a Taskfile, and agent guidance already in place.

## How a request flows {#flow}

Every HyperChi app follows the same path, so once you know it you can read any handler:

1. **A route matches.** HyperChi builds on chi, so patterns, URL parameters, and `net/http` middleware work as they do there. Each route is a context handler: `func(c *hyperchi.Context) error`.
2. **The handler reads through `c`.** `c.Param`, `c.Query`, `c.FormValue`, and `hyperchi.Bind[T]` cover input; `c.Session()`, `c.SignedCookie`, and `c.ClientIP()` cover the visitor.
3. **The handler answers once.** `c.View` renders a page for a browser and only the page template for an htmx request. `c.Fragment`, `c.JSON`, `c.Text`, `c.Blob`, and `c.Redirect` cover the other shapes, and `c.HX()` adds htmx response headers.
4. **Errors are values.** Return `hyperchi.NotFound(...)`, a `Bind` validation error, or any other error, and HyperChi renders it for whoever asked: JSON for an API client, a small alert for htmx, an error page for a browser. Your own error page plugs in with `app.OnError`.

Template data merges three layers, later ones winning: the route and query parameters, the values you set once with `app.SetGlobal`, and the handler's own data.

## Where to go next {#next}

- [Build a live guestbook](/docs/tutorial): one small app that uses a page, a validated form, htmx swaps, and a WebSocket room. The best place to start.
- [Routing and responses](/docs/routing): every way a handler can answer.
- [Forms and validation](/docs/forms) and [Errors](/docs/errors): the two things every real app needs first.
- [Deploying](/docs/deploying): one binary, `CGO_ENABLED=0`, and a production checklist.
