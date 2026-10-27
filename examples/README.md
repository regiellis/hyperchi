# HyperChi Examples

Seven example applications, each with its own `go.mod` that resolves the framework from this
checkout via a `replace` directive. They are meant for live development: run them with `air` or
`go run`, and never `go build` inside `dice-arena/`, `homelab-search/`, `mailgrid/`, `streamer/`,
`streamer-vite/`, or `studio/` (the showcase is the one example that builds). The docs site's
[Example apps](showcase/docs/apps.md) page compares the four application examples.

## dice-arena/

The WebSocket example: a realtime multiplayer dice game. A live lobby of open tables, two to four
players per table, spectators, server-side dice, and seats held through a disconnect. Every move
goes over `app.WebSocket`, is validated with `hyperchi.BindMessage`, and comes back as
server-rendered HTML pushed with `Socket.Swap` and `app.Room(...).Swap`. No Node, no database.
See [its README](dice-arena/README.md) for the rules and the message protocol.

```bash
cd dice-arena
go run .     # or: task dev (Air)
task test    # race-enabled tests, including real WebSocket clients
```

## homelab-search/

Search across a folder of manuals, notes, and configs. Files are indexed into SQLite FTS5 and
ranked with bm25; search updates as you type through htmx, results are linkable, and the keyboard
moves through them. User input is always quoted before it reaches FTS5, documents open through
`os.Root`, and an index status panel streams over SSE as the content `FileWatcher` picks up
changes. Read-only by design. Markdown previews render through `modules/markdown`.

```bash
cd homelab-search
LIBRARY_DIR=./library go run .   # point LIBRARY_DIR at your own directory
```

See [its README](homelab-search/README.md) for the index schema and configuration.

## mailgrid/

An internal tool a small team uses to send letters to a list of recipients. Every screen is
built with `modules/gomponents`. Import recipients from CSV, write letters with merge fields and
preview them live, and run a send-out: a background job renders each letter into an outbox
table while progress streams over SSE. Letters go to the outbox and are never emailed. Team
login with bcrypt, KV sessions, CSRF, and an audit log.

```bash
cd mailgrid
go run .   # the first run in development seeds a demo list and letter
```

See [its README](mailgrid/README.md) for the data model and the send-out lifecycle.

## showcase/

The HyperChi marketing and documentation site. The homepage pairs a full-width Three.js node network with a
live playground: SSE metrics, WebSocket messages, search, uploads, and multi-step forms use real
framework routes. `/docs` provides getting-started, routing, templates, realtime, directives,
handlers/helpers, integration, and data passing guides. Static assets are included; Node is only needed to refresh the pinned visual dependencies.
See [the website README](showcase/README.md) for asset and verification details.

Three full marketing pages demonstrate external modules with working Go endpoints:

| Example | Modules | Interaction |
| --- | --- | --- |
| `/examples/templ`: Form & Field creative studio | templ + htmx | Select project scope and swap an HTML estimate |
| `/examples/jet`: Relay collaboration software | Jet v6 + htmx | Toggle billing and swap pricing HTML |
| `/examples/alpine`: Outpost outdoor club | Alpine.js + Go JSON | Filter trip cards by terrain |

These pages use fictional fixture data. They include complete marketing content and live
read-only interactions; they do not create subscriptions or bookings. The pages are rendered
on the server and update selected regions without a client-side router. See the
[integration source guide](showcase/INTEGRATIONS.md) for data flows, template generation,
response contracts, and ways to replace fixtures with service data.

```bash
cd showcase
task dev     # hot reload via air
task run     # plain run
```

Serves on `127.0.0.1:9999` by default (`PORT` overrides). The showcase needs Go 1.26.8 or newer
and no C toolchain; it always runs with production behavior. See the
[hosting instructions](showcase/README.md#public-hosting-and-security) before choosing a public bind address.

## streamer/

The smallest example: a podcast catalog from one Go file with no database. Full-page and
fragment rendering (`c.Page`, `c.Fragment`), returned `hyperchi.NotFound` errors, content-versioned
assets with `{{asset}}`, and HyperChi `hc-*` directives for local state. It loads Tailwind from
its CDN. See [its README](streamer/README.md).

```bash
cd streamer
go run .   # or: air
```

## streamer-vite/

The same app with a Vite and Tailwind build, and a KV store (bbolt or memory) holding the
episodes. Two servers run together in development: the Go backend on `:8080` (the URL you open)
and the Vite dev server on `:3000`, which serves only JS/CSS with hot module replacement (no
proxy). Set `VITE_DEV_SERVER=auto` (or a full URL) so the page pulls assets from Vite; unset,
the page uses built `/dist` assets and the app needs `npm run build` first. See
[its README](streamer-vite/README.md).

```bash
cd streamer-vite
npm install
npm run dev:all    # starts the backend, then Vite after a short delay
                   # or: npm run backend in one terminal, npm run dev in another
```

## studio/

Driftlamp, the website of a fictional three-person game studio, and the reference for
content-driven marketing sites. Games, a devlog, a press kit, and careers come from embedded
Markdown (`modules/markdown`) and are pre-rendered at startup. The devlog has an htmx tag
filter and pager, heading anchors, a table of contents, an Atom feed, and a sitemap. The
newsletter form uses `ProtectForm`, `Bind`, and SQLite with a unique email, and says plainly
that it sends no email. No Node: htmx and the directives runtime are vendored.

```bash
cd studio
go run .   # or: air, or task dev
```

See [the studio README](studio/README.md) for configuration and the content model.

## Environment

`streamer` and `streamer-vite` build their configuration with `config.FromEnv()`, so they
honor the standard environment switch:

```bash
ENVIRONMENT=development go run .   # the default: detailed error pages, CORS open to any origin
ENVIRONMENT=production go run .    # generic error pages, CORS only for CORS_ORIGINS
```
