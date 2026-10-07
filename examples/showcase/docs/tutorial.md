---
title: Build a live guestbook
section: Learn
order: 20
next: routing
description: Build one small app end to end. A page, a form that validates on the server, htmx swaps, and new entries that appear in every open browser.
---

## What you will build {#overview}

A guestbook in one Go file and two templates. Visitors read the entries, sign with a name and a message, and see a validation message beside the field they got wrong. When someone signs, every open browser shows the new entry without a reload.

Along the way you use the pieces most HyperChi apps share:

- a context handler that renders a page with `c.View`
- `hyperchi.Bind` to decode and validate a form
- `hyperchi.FieldErrors` and a 422 fragment to show what was wrong
- `app.WebSocket` and `app.Room` to push HTML to everyone who is watching

You need Go 1.26 or later and nothing else: no Node, no C compiler, no database.

```sh
mkdir guestbook
cd guestbook
go mod init example.com/guestbook
go get github.com/regiellis/hyperchi
mkdir -p templates/layouts templates/pages
```

## The layout {#layout}

Every page renders inside `layout-base`, which receives the page as `.content`. Load htmx 4.0.0 and the WebSocket extension, which HyperChi serves at `/hyperchi/js/`. Save this as `templates/layouts/base.tmpl`:

```html
{{define "layout-base"}}<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Guestbook</title>
  <script src="https://cdn.jsdelivr.net/npm/htmx.org@4.0.0/dist/htmx.min.js"></script>
  <script src="/hyperchi/js/hx-ws.js"></script>
</head>
<body>
  {{.content}}
</body>
</html>{{end}}
```

## The page and its fragments {#example}

One file can define several templates. `guestbook` is the page. `entry` and `guestbook-form` are fragments: the page includes them, and handlers also render them on their own for htmx. Save this as `templates/pages/guestbook.tmpl`:

```html
{{define "guestbook"}}
<main>
  <h1>Guestbook</h1>
  <ul id="entries" {{wsConnect "/live"}}>
    {{range .entries}}{{template "entry" .}}{{end}}
  </ul>
  {{template "guestbook-form" .}}
</main>
{{end}}

{{define "entry"}}
<li><strong>{{.Name}}</strong>: {{.Message}} <small>{{.At.Format "15:04"}}</small></li>
{{end}}

{{define "guestbook-form"}}
<form hx-post="/sign" hx-swap="outerHTML">
  <label>Name <input name="name" value="{{with .in}}{{.Name}}{{end}}"></label>
  {{range .errors.name}}<p class="error">Name {{.}}.</p>{{end}}
  <label>Message <textarea name="message">{{with .in}}{{.Message}}{{end}}</textarea></label>
  {{range .errors.message}}<p class="error">Message {{.}}.</p>{{end}}
  <button>Sign</button>
</form>
{{end}}
```

Three details matter here:

- `{{wsConnect "/live"}}` writes the `hx-ws:connect` attribute, escaped. The list connects to the server's socket when the page loads.
- The `entry` fragment reads an `Entry`'s fields. Inside the page's `range`, dot is the entry itself; when a handler or a broadcast renders `entry` with an `Entry` value, HyperChi exposes the struct's exported fields as top-level keys. So one template serves the page, the htmx response, and the broadcast.
- The form swaps itself (`hx-swap="outerHTML"`). The server answers with a fresh form after a success, or the same form with its errors after a failure.

## The server {#server}

Save this as `main.go`:

```go
package main

import (
	"log"
	"slices"
	"sync"
	"time"

	"github.com/regiellis/hyperchi/hyperchi"
)

// Entry is one signature in the guestbook.
type Entry struct {
	Name    string
	Message string
	At      time.Time
}

// Guestbook keeps entries in memory, safe for concurrent requests.
type Guestbook struct {
	mu      sync.Mutex
	entries []Entry
}

func (g *Guestbook) Add(e Entry) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.entries = append(g.entries, e)
}

func (g *Guestbook) All() []Entry {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.entries)
}

// Signing is what the form posts. Bind decodes and validates it.
type Signing struct {
	Name    string `form:"name" validate:"required,max=40"`
	Message string `form:"message" validate:"required,max=280"`
}

func main() {
	app := hyperchi.New()
	if err := app.LoadTemplates("templates/**/*.tmpl"); err != nil {
		log.Fatal(err)
	}
	app.Extensions().EnableExtensionAssets()
	book := &Guestbook{}

	app.Get("/", func(c *hyperchi.Context) error {
		return c.View("guestbook", hyperchi.H{"entries": book.All()})
	})

	app.Post("/sign", func(c *hyperchi.Context) error {
		in, err := hyperchi.Bind[Signing](c)
		if fields := hyperchi.FieldErrors(err); fields != nil {
			return c.Status(422).Fragment("guestbook-form", hyperchi.H{"in": in, "errors": fields})
		}
		if err != nil {
			return err
		}
		entry := Entry{Name: in.Name, Message: in.Message, At: time.Now()}
		book.Add(entry)
		if err := app.Room("guestbook").Swap("#entries", "beforeend", "entry", entry); err != nil {
			return err
		}
		return c.Fragment("guestbook-form", hyperchi.H{})
	})

	app.WebSocket("/live", func(s *hyperchi.Socket) error {
		s.Join("guestbook")
		return s.Wait()
	})

	if err := app.Serve("0.0.0.0:8080"); err != nil {
		log.Fatal(err)
	}
}
```

Run it and open `http://localhost:8080` in two browser windows:

```sh
go mod tidy
go run .
```

## What happens on a request {#flow}

**Reading the page.** `GET /` returns `c.View("guestbook", ...)`. A browser navigation gets the page inside `layout-base`; an htmx request for the same URL would get the page template alone. You never branch on the request type yourself.

**Signing with a mistake.** Submit the form with an empty name. `Bind[Signing]` decodes the form fields named by the `form` tags and checks the `validate` rules. It returns an error that carries per-field messages, and `FieldErrors` pulls them out. The handler answers `422` with the form fragment, the values the visitor typed, and the messages. htmx 4 swaps every status except 204 and 304, so the form with its errors replaces the old one and the message the visitor wrote is still there.

If you return the error from `Bind` directly instead, HyperChi still answers 422 with the field messages, as JSON for an API client or a small alert for htmx. Re-rendering your own fragment is how you put the messages next to the fields. The [forms guide](/docs/forms) covers the validation rules, custom `Validate` methods, CSRF, and uploads.

**Signing successfully.** The entry is stored, then `app.Room("guestbook").Swap(...)` renders the `entry` fragment once and sends it to every socket in the room. The htmx WebSocket extension in each browser appends it to `#entries`. The poster gets a fresh, empty form as the HTTP response.

**The socket.** `app.WebSocket("/live", ...)` accepts the connection with the app's origin policy, then runs your handler. `s.Join("guestbook")` puts the socket in the room and `s.Wait()` holds it open until the browser leaves. The hub owns writes and pings, so the handler has nothing else to do. A socket that only listens never needs to read; when you want browsers to send messages, see [Realtime](/docs/realtime).

> Entries live in memory, so they reset when the process restarts. For storage that survives, use the SQLite layer from [Data and KV](/docs/data), or your own store behind the same `Add` and `All` methods.

## Before you put it online {#next}

This app is complete, but a public guestbook needs a few more things, each covered in its own guide:

- **CSRF protection.** Call `app.AutoMiddleware()` before the routes. It turns on CSRF protection, so put `hx-headers:inherited` with `.csrfToken` on the `body`, as the [forms guide](/docs/forms) shows.
- **A production configuration.** Set `ENVIRONMENT=production`, which tightens WebSocket origins and error detail. See [Configuration](/docs/configuration) and [Security](/docs/security).
- **One binary.** Embed the templates with `LoadTemplatesFS` and build with `CGO_ENABLED=0`, as [Deploying](/docs/deploying) describes.
- **Tests.** Drive the handlers through `httptest`, including the 422 path. See [Testing](/docs/testing).

Next, the [routing guide](/docs/routing) covers every way a handler can answer.
