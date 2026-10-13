# Homelab search

A self-hosted search over a directory of documents: manuals, notes, configs, and runbooks.
Point it at a folder, and every file in it becomes searchable from a browser on your LAN,
ranked by relevance with the matching words highlighted. It is read-only: it never writes to
the library.

It is also a HyperChi example. It shows active search with htmx 4, an SQLite FTS5 index kept
in step with the disk by the framework's `FileWatcher`, a status panel streamed over the
framework's SSE hub, and keyboard navigation with HyperChi `hc-*` directives. No Node build,
no CGO.

## Run it

```bash
cd examples/homelab-search
go run .          # or: task dev (Air hot reload), task run
```

Open the URL it logs (it binds `0.0.0.0:8080` and logs every LAN address). In development a
busy port moves it to the next free one. The seeded `library/` holds a few runbooks, notes,
configs, a backup script, and one image, all written for this example.

Keys: `/` focuses search, `j`/`k` or the arrow keys move through results, `Enter` opens one
(in the search box it opens the first result), `Esc` leaves the search box.

## Point it at your library

```bash
LIBRARY_DIR=/srv/docs DB_PATH=/var/lib/homelab-search/index.db go run .
```

| Variable | Default | Meaning |
| --- | --- | --- |
| `LIBRARY_DIR` | `./library` | The directory to index. It must exist. |
| `DB_PATH` | `./homelab-search.db` | The index. It is derived data: delete it and it is rebuilt from the library. |
| `MAX_FILE_BYTES` | `1048576` (1 MiB) | Larger files are listed but their contents are not indexed. |
| `MAX_RAW_BYTES` | `33554432` (32 MiB) | Larger files cannot be downloaded through the app (413). |
| `WATCH_INTERVAL` | `2s` | How often the library is polled for changes. |
| `BASIC_AUTH_USER`, `BASIC_AUTH_PASSWORD` | unset | Require one login for every request. Set both or neither. |
| `ENVIRONMENT` | `development` | `production` binds exactly `HOST:PORT`, sends CSP and security headers, and hides error details. |
| `HOST`, `PORT` | `0.0.0.0`, `8080` | Listen address. |
| `DB_LOG_QUERIES` | unset | `true` logs every SQL statement with its arguments. Indexing writes whole files, so leave it off unless you are debugging a query. |

### What gets indexed

- **Markdown** (`.md`, `.markdown`): rendered with `modules/markdown`. The title comes from
  front matter `title`, then the first `#` heading, then the file name. Front matter `tags` may
  be `a, b` or `[a, b]`. Front matter that uses other YAML structure (nested maps, multi-line
  values) is set aside and the body is still indexed.
- **Text** (`.txt`, `.rst`, `.adoc`, `.csv`, `.tsv`), **config** (`.conf`, `.ini`, `.toml`,
  `.yaml`, `.json`, `.xml`, `.hcl`, `.tf`, `.nix`, systemd units, plus `Dockerfile`,
  `Caddyfile`, `Makefile`, `hosts`, `fstab`, and similar names), and **script** (`.sh`, `.py`,
  `.ps1`, and similar): indexed as plain text and shown with line numbers.
- **Everything else**, and text files that contain a NUL byte, are not valid UTF-8, or exceed
  `MAX_FILE_BYTES`: listed and findable by file name, folder, and tags, with a download link.
  Their contents are not indexed.
- Every folder a file sits in becomes a tag (`runbooks/network/dns.md` is tagged `runbooks`
  and `network`).
- Names that start with a dot (`.git`, `.env`, editor swap files) are skipped entirely, files
  and directories alike. Symlinks are followed only while they stay inside the library;
  symlinked directories are not descended into.

### Search syntax

Words match anywhere and all must match. The last word matches as a prefix while you are still
typing it. `"quoted phrases"` match in order, a trailing `*` makes a prefix (`backu*`), and
punctuation inside a word makes a phrase (`nginx.conf`, `docker-compose`). Nothing typed is
passed to SQLite as query syntax: every word is quoted, so `OR`, `NOT`, `NEAR`, `title:`, and
parentheses are plain words, and malformed input still searches. Type and tag filters narrow
the results; each search has a shareable URL (`/?q=nginx&type=config`).

## Expose it on a LAN

```bash
ENVIRONMENT=production BASIC_AUTH_USER=me BASIC_AUTH_PASSWORD='long passphrase' \
  LIBRARY_DIR=/srv/docs DB_PATH=/var/lib/homelab-search/index.db go run .
```

Basic credentials are only encoded, so put it behind a TLS reverse proxy if the network is not
trusted. Without `BASIC_AUTH_USER` in production the app logs a warning: anyone who can reach
the port can read the library.

What keeps the library safe to expose:

- **Read-only.** No route writes, renames, or deletes a library file. `POST /rescan` only
  re-reads the directory, and it accepts htmx requests only (a cross-site form cannot send
  `HX-Request`).
- **No path traversal.** `/doc/*` and `/raw/*` serve only paths already in the index, after
  rejecting anything that is not a clean relative path, and every read goes through an
  `os.Root` opened on the library, so `..`, encoded separators, absolute paths, and symlinks
  that leave the library all answer 404.
- **Nothing from the library runs in the page.** Results, snippets, and titles are escaped
  before highlighting, Markdown renders in goldmark's safe mode (raw HTML dropped), and raw
  downloads carry `Content-Security-Policy: sandbox` and `nosniff`. HTML, SVG, and other
  active types download as `application/octet-stream`.

## How it works

| Piece | Where | Framework or module call |
| --- | --- | --- |
| Config from the environment | `main.go` `loadConfig` | `config.FromEnv()`, `Config.Server.PortFallback` |
| App, middleware, routes | `site.go` | `hyperchi.NewWithConfigE`, `app.Use(app.Middleware.Recovery(), ...)`, `app.Get`, `app.Post`, `app.SetGlobal`, `app.LoadTemplatesFS` over an `embed.FS` |
| Active search, page or fragment | `site.go` `search` | `c.Query`, `c.HX().PushURL(...).View("results", data)` |
| Document view | `site.go` `document` | `c.Param("*")`, `c.View("doc", data)`, `hyperchi.NotFound` |
| Raw download | `site.go` `raw` | `c.SetHeader`, `c.Blob(contentType, body)`, `hyperchi.Errorf(413, ...)` |
| Markdown | `library.go` `parseMarkdown` | `markdown.New().Parse` (`HTML`, `Headings`, `Text()`, `Meta`) |
| Index | `index.go` | `app.DB` (`database.DB`): `TxContext`, `QueryContext`; SQLite FTS5 with `bm25`, `highlight`, `snippet` |
| Live index | `live.go` | `content.NewFileWatcher(dir)`, `Scan`, `Watch(ctx, interval, onChange)`; the watcher skips hidden entries and `node_modules` |
| Status stream | `site.go` `publishStatus`, `templates/layout.tmpl` | `app.Extensions().GetSSEHub()`, `hub.HTTPHandler()`, `hub.BroadcastSwap`, `{{sseConnect ...}}`, `EnableExtensionAssets` (serves `/hyperchi/js/hx-sse.js`) |
| Basic auth | `site.go` `newSite` | `app.BasicAuth(user, password)` |
| Static assets | `site.go` `routes`, `web.go` | `app.StaticFS("/static/*", fs.Sub(assets, "static"), ...)` over an `embed.FS` |
| Keyboard and theme | `static/js/app.js` | `HyperChi.registerComponent`, `hc-data`, `hc-on:keydown`, `hc-bind` |

The index schema (`index.go`):

```sql
documents      (id, path UNIQUE, title, type, tags, size, mtime, searchable, reason)
document_tags  (doc_id -> documents.id ON DELETE CASCADE, tag)  -- the tag filter
documents_fts  USING fts5(title, path, tags, body,
               tokenize = 'unicode61 remove_diacritics 2', prefix = '2 3')  -- rowid = documents.id
```

Ranking is `bm25(documents_fts, 10.0, 4.0, 6.0, 1.0)`: a title match outweighs a tag, path, or
body match. Highlights come from FTS5 `highlight` and `snippet` with control characters as
markers; indexing strips those characters from every file, so the output is escaped first and
only then are the markers turned into `<mark>`. The schema version lives in
`PRAGMA user_version`; a mismatch drops and rebuilds the index.

At startup the watcher records a baseline, then a full scan reconciles the index with the disk
(new, changed, and deleted files), then the watcher polls every `WATCH_INTERVAL`. Each pass
pushes the rendered status panel to every open page. "Rescan now" runs the same full scan.

One route uses plain `net/http` on purpose, through `app.Handle`: the SSE hub's stream handler.
The hub lifts the server's write deadline for each stream and pings open streams every 30
seconds, so the status panel stays connected under the framework's server timeouts. Templates
and static files are embedded, so the app runs from any working directory.

One workaround remains, for an upstream htmx issue: the htmx 4.0.0 SSE extension's `cleanup()`
leaves an `AbortError` unhandled when a swap removes an open stream (pressing Back does it).
`static/js/app.js` marks that one rejection handled and nothing else.

The frontend is vendored: htmx 4.0.0 (`static/js/htmx.min.js`, SHA-256
`e484d9171a9db30a39c8f16e3d709d4137f3211c659f8e6125816635033d593f`, pinned by a test) and the
HyperChi directives runtime from `cmd/hyperchi/assets/hyperchi-directives.js`. The SSE extension
is the copy the framework embeds and serves.

## Tests

```bash
task test     # go test -race -count=1 -cover ./...
task lint     # gofmt, go vet, golangci-lint
task fuzz     # fuzz the query sanitizer
```

They cover FTS5 in the pure-Go driver, indexing (types, tags, size cap, binary files, hidden
files, symlinks), the query sanitizer against hostile input, ranking, escaping, filters, path
traversal on both file routes, the watcher (add, change, delete), the SSE status stream, basic
auth, production configuration, and full page versus htmx fragment. Every database and library
lives in `t.TempDir()`.
