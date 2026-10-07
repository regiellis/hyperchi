# Driftlamp: a studio site on HyperChi

The website of Driftlamp, a fictional three-person game studio. It is the
reference for content-driven marketing sites on HyperChi: games, a devlog, a
press kit, and careers from Markdown, pre-rendered at startup, plus one live
form backed by SQLite. The studio, its people, and its games are invented.

## Run it

Go 1.26 or newer. No Node and no C compiler.

```sh
go run .                 # http://localhost:8080, or the next free port
air                      # hot reload, or `task dev`
task test                # race tests with coverage
task lint                # gofmt, go vet, golangci-lint
```

Per the repository rule, do not `go build` in this directory. Air builds into
the ignored `./tmp`. Templates, content, and static files are embedded, so the
program does not depend on its working directory.

Configuration comes from `config.FromEnv()`:

| Variable | Default | Meaning |
| --- | --- | --- |
| `ENVIRONMENT` | development | `production` turns off detailed errors, open CORS, port fallback, and asset revalidation, and turns on CSP and HSTS |
| `HOST`, `PORT` | `0.0.0.0`, `8080` | Listen address. Development tries the next port when one is taken |
| `PUBLIC_ORIGIN` | `http://localhost:PORT` | Origin for canonical URLs, Open Graph tags, the feed, and the sitemap. Required, and HTTPS, in production |
| `DB_PATH` | `data/studio.db` | SQLite file for newsletter signups |

```sh
ENVIRONMENT=production PUBLIC_ORIGIN=https://driftlamp.example DB_PATH=/data/studio.db go run .
```

## What it shows

| Route | How |
| --- | --- |
| `/`, `/games`, `/games/{slug}`, `/devlog/{slug}`, `/press`, `/about`, `/careers`, `/careers/{slug}`, `/privacy`, `/newsletter` | Rendered once at startup with `app.PreRenderedPage`: ETag, conditional GET, and HEAD for free |
| `/devlog?tag=&page=` | `c.View`: the full page for a visit, the `devlog` template alone for the htmx tag filter and pager, which swap `#main` and push the URL. An unknown tag or page is a 404 |
| `/devlog/feed.xml` | Atom 1.0, built at startup with `encoding/xml`, sent with `c.Blob("application/atom+xml; charset=utf-8", ...)` |
| `/sitemap.xml`, `/robots.txt` | Sitemap of every indexable page, with post dates as `lastmod` |
| `POST /newsletter` | `app.ProtectForm` (same-origin check, per-client token bucket on `hyperchi.ClientIP`, 2 KiB URL-encoded body), then `hyperchi.Bind[Signup]` with `validate` tags |
| `/static/*` | `app.StaticFS` over the embedded files: content-hash ETags and 304s. Templates link files with the built-in `{{asset "/static/css/site.css"}}`, which adds a `?v=` content version; that URL is cached for a year (`immutable`) and anything else for an hour |
| anything else | A branded 404, pre-rendered and served from `app.OnError` |

### The newsletter

- `Bind` rejects a bad address or list with 422, and the handler re-renders the
  form with the visitor's input, one message per field, `aria-invalid`, and
  focus on the first bad field. htmx swaps the fragment in place, and a plain
  form post gets the whole newsletter page.
- Duplicates are caught twice: `database.IsTakenContext` on the request's
  context first, then
  `INSERT ... ON CONFLICT (email) DO NOTHING` on the unique column for a race
  between two posts. Either way the answer is 422 with a field error.
- The table comes from `database.Table(...).Build()` and `app.Migrate`, run on
  every boot.
- A honeypot field is answered as success and stored nowhere.
- 403, 413, 415, and 429 from `app.ProtectForm` go through `OnError`. For htmx,
  `c.HX().Retarget("#newsletter-status")` puts the message under the form
  instead of replacing it.
- No email is sent. The confirmation and the privacy page say so.

### Content model

Everything under `content/` is Markdown with flat front matter, embedded with
`go:embed` and loaded once with `app.Content.LoadFS`. Each directory is a
collection declared with `hyperchi.WithContentCollection` and a
`content.NewSchema` schema (required fields, `int` and date types, the keys a
file may carry), and `hyperchi.WithMarkdown` renders bodies with
`modules/markdown`. `LoadLibrary` then builds typed games, posts, pages, and
roles from `app.Content` and checks what spans files. A missing field, an
unknown key, a bad date, or a missing image fails the boot and the tests, and
`TestEveryContentFileParses` also runs each file through `modules/markdown`'s
parser on its own, so a failure names the file and the field.

| Directory | Front matter |
| --- | --- |
| `games/` | `title`, `tagline`, `summary`, `genre`, `status`, `status_kind` (`released`, `early-access`, `in-development`), `order`, `platforms` (comma list), optional `flagship: true` (exactly one), `cover` and `screen_N` (`img/path.svg \| alt text`), `store_steam`/`store_eshop`/`store_gog`/`store_itch`, `fact_*` (press facts) |
| `devlog/` | `title`, `date` (YYYY-MM-DD), `author`, `tags` (comma list of slugs), `summary`, optional `game` (a game slug) |
| `pages/` | `title`, `description`, optional `fact_*`; `about`, `press`, and `privacy` are required |
| `careers/` | `title`, `location`, `kind`, `posted`, `summary` |

Post pages get heading anchors (added to goldmark's `h2` and `h3` ids) and a
table of contents from the document's `Headings`. `WithMarkdown` hands the
loader HTML only, so `LoadLibrary` parses each post body a second time for its
headings and reading time. The file name is the slug.

### Frontend

- htmx 4.0.0 (`static/js/htmx.min.js`, SHA-256
  `e484d9171a9db30a39c8f16e3d709d4137f3211c659f8e6125816635033d593f`) and the
  HyperChi directives runtime, vendored. The one `hc-data` scope owns the
  theme toggle (`hc-on:click`, `hc-bind:aria-pressed`), and `theme.js` applies
  a stored choice before first paint.
- Hand-written CSS with `light-dark()` tokens. Fraunces (variable, OFL,
  `@fontsource-variable/fraunces` 5.3.0, checked 2026-10-05) for display type,
  system fonts otherwise.
- The art is original SVG, generated once and committed. `task og` re-renders
  the 1200x630 Open Graph PNGs from it with headless Chrome.
- Production sends a CSP with no inline script or style. It allows
  `'unsafe-eval'` only because the directives compile expressions.

## Layout

```
main.go        config, app construction, embed
site.go        page metadata, routes, the devlog index, OnError
content.go     content schemas, LoadFS, and the typed content model
newsletter.go  the signup form and its storage
feed.go        Atom feed and sitemap
assets.go      the StaticFS mount and extra MIME types
templates/     layout, pages, and fragments, each with {{define}}
content/       games, devlog, pages, careers
static/        CSS, JS, font, images
art/           the source of the studio Open Graph image
```
