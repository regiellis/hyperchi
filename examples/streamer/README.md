# Streamer

A Swiss-design podcast catalog, and the smallest HyperChi example: one Go file, three
templates, no database. It converts a Lexington Themes Astro template to a server-rendered app
with htmx fragments and `hc-*` directives for local state.

## What it demonstrates

- Context handlers: every route is `func(c *hyperchi.Context) error`. `c.Page` renders the
  catalog, `c.Fragment` answers the htmx detail and modal requests, `c.JSON` answers the search
  and health routes.
- Returned errors: an unknown or non-numeric podcast ID is `hyperchi.NotFound`, a missing one
  `hyperchi.BadRequest`. The framework renders them as JSON, an htmx alert, or an error page,
  depending on who asked.
- Built-in assets: `app.Static` serves `static/`, and the templates link scripts, the icon, and
  cover images with `{{asset "/static/..."}}`, which adds a content version and a year of
  immutable caching.
- `hc-data`, `hc-on`, `hc-show`, `hc-persist`, and `hc-effect` hold the theme, the expanded row,
  and the open modal. Details and modal bodies load once through `hx-trigger="... once"`.
- `helpers.S` string chains for ID parsing and search matching, and `app.Helpers.Env` for
  configuration read from `.env`.
- Explicit `HEAD` and `OPTIONS` routes on `/health` (`c.SetHeader(...).Empty()`). In development the open CORS middleware answers `OPTIONS` with a 204 before these routes run.

## Run it

```bash
cd examples/streamer
go run .          # http://localhost:8080
./dev.sh          # the same with Air hot reload (needs air on PATH)
```

The server binds `0.0.0.0` and takes its port from `PORT` (default `8080`). Examples are for
live development: use `go run` or Air, never `go build .` in this directory. To ship something
like it, scaffold a project with `hyperchi new` and use `hyperchi deploy` there.

The page loads Tailwind from its CDN, and fonts from Google Fonts and rsms.me, so a browser
needs internet access. `streamer-vite` is the same app with a local Tailwind build.

## Configuration

Go 1.26.8 or newer (the `go` line in `go.mod`).

| Variable | Read by | Effect |
| --- | --- | --- |
| `PORT` | the app | Listen port, default `8080`. |
| `PLATFORM_SPOTIFY_URL` | the app | Passed to the page as `.spotify`. |
| `ENVIRONMENT` | `config.FromEnv` | `production` turns off development behavior: detailed error pages and open CORS. Read from the process environment, not from `.env`. |

`app.LoadEnv` reads an optional `.env` in the working directory. Other keys in it are not used.

## Routes

| Route | Answer |
| --- | --- |
| `GET /` | The catalog, always as a full page. |
| `GET /podcast/{id}/details` | Fragment: description, topics, sponsors, links. |
| `GET /podcast/{id}/modal` | Fragment: the modal body with the transcript. |
| `GET /api/search?q=` | JSON: podcasts whose title, category, or description contains `q`. |
| `GET`, `HEAD`, `OPTIONS /health` | JSON status, headers only, and the allowed methods. |
| `OPTIONS /test-options` | A JSON body from an `OPTIONS` route (when CORS does not answer first). |
| `GET /static/*` | Files from `static/`. |

## Files

```
streamer/
├── main.go                 routes, handlers, and the built-in podcast data
├── templates/
│   ├── index.tmpl            full page: layout, list, app component
│   ├── podcast-details.tmpl  fragment for an expanded row
│   └── podcast-modal.tmpl    fragment for the modal
├── static/                 CSS, htmx and directives scripts, favicon, 27 cover images
├── .air.toml, dev.sh       Air hot reload
└── go.mod
```

## Data

`getPodcastData` returns 27 episodes about Swiss design: date, duration, category, topics,
sponsors, links, listening platforms, and a transcript. Edit it in `main.go`.
