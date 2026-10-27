---
title: Example apps
section: Reference
order: 240
next: start
description: Four complete applications built the way HyperChi is meant to be used: a multiplayer game, a studio website, an internal mailing tool, and a homelab search engine.
---

## Pick the app closest to yours {#overview}

Each app is its own Go module under `examples/` in the repository, with tests, a README that explains which framework call does which job, and no Node build. Run any of them with `go run .` from its directory. Every handler is a `func(c *hyperchi.Context) error`; an automated grader checks that none of them falls back to plain `net/http`.

| App | What it is | What it shows |
| --- | --- | --- |
| [Dice Arena](https://github.com/regiellis/hyperchi/tree/main/examples/dice-arena) | A multiplayer dice game with a live lobby | Two-way WebSockets, rooms, validated socket messages, reconnecting to your seat |
| [Studio](https://github.com/regiellis/hyperchi/tree/main/examples/studio) | A small game studio's website | Markdown content, pre-rendered pages, a devlog with htmx filtering, feeds, a newsletter form |
| [Mailgrid](https://github.com/regiellis/hyperchi/tree/main/examples/mailgrid) | An internal tool that renders letters for a list of recipients | gomponents views, CSV import, background jobs with live SSE progress, team login |
| [Homelab Search](https://github.com/regiellis/hyperchi/tree/main/examples/homelab-search) | Search across a folder of manuals, notes, and configs | SQLite full-text search, active search with htmx, a live index over SSE |

## What each app teaches {#example}

### Dice Arena: realtime with WebSockets

Players open a table, share a five-letter code, and take turns rolling. The server rolls the dice and owns the game state; the browser only sends intentions such as "roll" or "bank". Every message is checked with `hyperchi.BindMessage`, the shared board goes to everyone with `app.Room(code).Swap`, and each player's own view goes back on their socket with `s.Swap`. A player who drops keeps their seat for a short grace period and gets it back on reconnect. Identity comes from a cookie signed with `c.SetSignedCookie` and read through `SetIdentifyUser`, never from what the client sends.

### Studio: a content site

Games, devlog posts, and pages are Markdown files with front matter, embedded in the binary and loaded once at startup with `app.Content.LoadFS`, a schema per collection, and the [Markdown module](#modules) as the renderer (see [Markdown content](/docs/content)). Static files are linked with `{{asset}}` for year-long caching. Fixed pages are rendered once with `app.PreRender`. The devlog filters by tag and pages through htmx, sending a fragment to htmx and a full page to everyone else from the same `c.View` call. The newsletter form uses `app.ProtectForm`, `hyperchi.Bind`, and `database.IsTakenContext`, and says plainly that no email is sent.

### Mailgrid: an internal tool

Every screen is built with [gomponents](#modules) instead of templates. Import recipients from CSV, write a letter with merge fields, preview it against a real recipient, and run a send-out: a background job renders each letter into an outbox table while progress streams to the page over SSE. A reload picks the progress up from the database, and a rerun only renders what is missing. Letters go to the outbox and are never emailed; the app says so before you start.

### Homelab Search: search over your own files

Point it at a directory and it indexes Markdown, text, and config files into SQLite FTS5, ranked with bm25. Search updates as you type, results are linkable, and the keyboard moves through them. Your input is always quoted before it reaches FTS5, documents open through `os.Root` so a crafted path cannot escape the library, and a status panel shows the index updating live as files change.

## Optional modules {#modules}

The core framework keeps its dependencies small. Rendering engines and services live in separate modules under `modules/`, each with its own `go.mod`, so you only download what you use.

- [Markdown](https://github.com/regiellis/hyperchi/tree/main/modules/markdown): goldmark with GitHub Flavored Markdown, heading IDs, and front matter. Returns safe HTML, the raw source, headings, and plain text. Used by Studio, Homelab Search, and this documentation.
- [gomponents](https://github.com/regiellis/hyperchi/tree/main/modules/gomponents): HTML components written in plain Go, rendered with `c.RenderWith`, plus typed helpers for htmx and `hc-*` attributes. Used by Mailgrid.
- [templ](https://github.com/regiellis/hyperchi/tree/main/modules/templ) and [Jet](https://github.com/regiellis/hyperchi/tree/main/modules/jet): typed generated components and runtime templates. See the [integration examples](/docs/integrations).
- [PocketBase](https://github.com/regiellis/hyperchi/tree/main/modules/pocketbase): an HTTP client for record CRUD, pagination, and authentication.
- [Performance](https://github.com/regiellis/hyperchi/tree/main/modules/performance): Badger storage, a job queue, and rate limiting.

## Run one {#next}

```sh
git clone https://github.com/regiellis/hyperchi
cd hyperchi/examples/dice-arena
go run .
```

The app prints the addresses it is listening on. Each README lists the environment variables it reads, what is stored where, and which parts of the framework it uses and why. Start from the one closest to what you are building, then read the [integration examples](/docs/integrations) if you want a different renderer.
