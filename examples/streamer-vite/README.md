# Streamer + Vite

The Streamer podcast catalog with a Vite and Tailwind build for its JavaScript and CSS, and a
HyperChi KV store holding the episodes. It shows how a HyperChi app takes a frontend toolchain
without a proxy: Go renders the pages, Vite only supplies assets.

## What it demonstrates

- Context handlers: `c.View("index", data)` for the page, `c.Fragment` for the htmx details and
  modal bodies, `c.JSON` for the search and stats routes. Failures are returned errors
  (`hyperchi.NotFound`, `hyperchi.BadRequest`, `hyperchi.Errorf(500, ...)`), rendered by the
  framework for whoever asked.
- Built-in assets: `app.Static` serves `static/` and `dist/`, and the layout links the icon,
  cover images, and the built `style.css` and `main.js` with `{{asset}}`, which adds a content
  version and long-lived caching.
- The Vite split: in development the browser loads pages from Go on `:8080` and JS and CSS, with
  hot module replacement, from Vite on `:3000`. In production the same layout links the built
  files in `dist/`. `app.SetGlobal` carries `development` and `vite_dev_server` to the
  templates once, at startup.
- A KV store as the database: `app.UseKVStoreBBolt` (or memory) with the catalog stored as JSON
  and seeded on first start.
- `hc-*` directives for the theme, the expanded row, and the modal, registered from
  `src/main.js`, and an animation preset (`RegisterPreset`) for the cards.

## Run it

Needs Go 1.26.8 or newer (the `go` line in `go.mod`) and Node with npm for the frontend.

```bash
cd examples/streamer-vite
npm install
npm run dev:all    # Go backend, then Vite after a short delay
```

Open the Go server at `http://localhost:8080`, not Vite. The same, step by step:

```bash
npm run backend    # go run . on :8080
npm run dev        # Vite on :3000, in a second terminal
task dev           # both through Task; task dev:air adds Air hot reload for the Go side
```

Examples are for live development: use `go run` or Air, never `go build .` in this directory.
Go output from Air goes to the ignored `tmp/`.

### Where the pages get their assets

`VITE_DEV_SERVER` decides, and `.env` is read when present:

| Value | Pages load JS and CSS from |
| --- | --- |
| unset or empty | the built files in `dist/` |
| `auto` | Vite on port 3000 of whatever host the browser used, so LAN and SSH clients work |
| a full URL, such as `http://192.168.1.5:3000` | that Vite server |

With it unset, the app needs `dist/`: run `npm run build` first. The server refuses to start
without `dist/` and without `VITE_DEV_SERVER`; with `VITE_DEV_SERVER` set it only logs a warning.

## Production-style run

```bash
npm run build                          # writes dist/
ENVIRONMENT=production go run .        # VITE_DEV_SERVER unset: pages load dist/
```

The pages come from the Go server, so `npm run preview` (Vite serving `dist/` alone) does not
run this app. To ship something like it, scaffold a project with `hyperchi new`.

## Configuration

| Variable | Read by | Effect |
| --- | --- | --- |
| `PORT`, `HOST` | the app | Listen address, default `0.0.0.0:8080`. |
| `VITE_DEV_SERVER` | the app | Asset source, as above. |
| `KV_BACKEND` | the app | `bbolt` (default, file `./data/podcasts.db`) or `memory`. |
| `APP_NAME`, `APP_VERSION` | the app | Shown in the log and by `/admin/stats`. |
| `ENABLE_AIR` | the app | `true` writes `.air.toml` and `dev.sh` when the project has none. |
| `ENVIRONMENT` | `config.FromEnv` | `production` turns off detailed error pages and open CORS. Read from the process environment, not from `.env`. |

## Routes

| Route | Answer |
| --- | --- |
| `GET /` | The catalog, newest first. |
| `GET /podcast/{id}/details`, `/modal` | Fragments for one episode. |
| `GET /api/search?q=` | JSON results; an empty `q` returns every episode. |
| `GET /admin/stats` | JSON counts by category and app info. |
| `GET /health`, `/health/ready`, `/health/live` | Health checks. |
| `GET /static/*`, `/dist/*` | Static files and built assets. |

## Files

```
streamer-vite/
├── main.go             configuration, routes, handlers, seed data
├── podcast_store.go    KV-backed episode storage
├── seed_test.go        the seed data decodes; malformed entries are rejected
├── templates/          layouts/base.tmpl, index, podcast-details, podcast-modal
├── src/                main.js (entry), htmx-bootstrap.js, style.css (Tailwind)
├── static/             images, favicon, htmx animation scripts
├── dist/               built assets (generated, ignored)
├── vite.config.js, postcss.config.js, tailwind.config.js, package.json
└── Taskfile.yml, .air.toml, dev.sh
```

`src/main.js` imports the directives runtime and view-transition script from the repository's
`frontend/assets/scripts/`, so this example builds only from inside the HyperChi checkout.

## Tests

```bash
go test -race -count=1 ./...
```
