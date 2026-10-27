# Integration examples

Three complete marketing pages demonstrate external renderers and browser data passing inside the showcase application. Each page includes navigation, multiple content sections, and a working interaction backed by Go. They are server-rendered single-page experiences with selective updates, without a client-side router.

| Page | Stack | Working interaction | Guide |
| --- | --- | --- | --- |
| `/examples/templ` — Form & Field | templ + htmx | Project scope updates an estimate fragment | `/docs/templ` |
| `/examples/jet` — Relay | Jet v6 + htmx | Billing period updates pricing HTML | `/docs/jet` |
| `/examples/alpine` — Outpost | Go HTML templates + Alpine.js + JSON | Terrain selection filters trip cards | `/docs/alpine` |

All businesses and catalog data are fictional. These examples do not persist data, book trips, sell plans, or submit inquiries. The [integration overview](/docs/integrations) and [data passing guide](/docs/data) explain the shared approach when viewed on the running site.

## Run locally

The showcase requires Go 1.26.8; no C compiler is needed. The root HyperChi library requires Go 1.26.0. Integration versions are templ v0.3.1100, Jet v6.3.3, and Alpine.js 3.15.8.

From the repository root:

```sh
cd examples/showcase
go run .
```

Open `http://localhost:9999/examples/templ`, `/examples/jet`, and `/examples/alpine`. Set `PORT` to choose another port; check the startup log for the actual address if the requested port is occupied. The showcase binds to loopback by default. See [README.md](README.md) for hosting configuration.

Templates, JavaScript, styles, and fixture data ship with the application. Node is not required to run the checked-in examples. The generated templ Go file is committed, so the generator is only needed when editing `.templ` source.

## Source map

| File | Responsibility |
| --- | --- |
| `integrations.go` | Route adapters, fixture models, query handling, HTML rendering, and JSON responses |
| `marketing.templ` | Studio page and estimate component source |
| `marketing_templ.go` | Generated Go implementation; regenerate instead of editing |
| `marketing/relay.jet`, `marketing/pricing.jet` | Relay page and shared pricing markup |
| `templates/outpost.tmpl` | Outpost server-rendered HTML template |
| `static/js/outpost.js` | Alpine trip finder, JSON fetch, loading, errors, and retry |
| `docs/integrations.md`, `docs/templ.md`, `docs/jet.md`, `docs/alpine.md`, `docs/data.md` | Integration and data passing documentation (Markdown, rendered into `templates/docs.tmpl`) |

Jet templates and static assets are embedded and require rebuilding. Outpost uses HyperChi’s loaded template directory, which is copied into the Docker image. Editing either template format does not require running the templ generator.

## Regenerate templ

Run the generator version matching the module's pinned runtime dependency:

```sh
# From examples/showcase:
go generate ./...
go test ./...
```

The directive runs `go run github.com/a-h/templ/cmd/templ@v0.3.1100 generate -f marketing.templ`, matching the pinned runtime. Air also regenerates templ before rebuilding and watches `.templ` and `.jet` edits.

Commit `marketing.templ` and the updated `marketing_templ.go` together.

## The adapter boundary

The examples register their routes with `app.Get`. Each handler receives a
`*hyperchi.Context` and returns an error. HyperChi handles method routing,
middleware, and returned errors. Query inputs use `c.Query` and
`c.QueryDefault`.

Outpost uses `c.View("outpost.tmpl", demoTrips)` for its page; the typed slice
is exposed as `.Result`. Its JSON endpoint uses `helpers.FromSlice(...).Filter`,
`trips.Len()`, `hyperchi.H`, and `c.JSON`. Invalid terrain gets a JSON 400 through
`c.Status(http.StatusBadRequest).JSON(...)`. Empty matches are normalized
with `.OrEmpty()` so the API consistently returns an array.

templ and Jet use optional modules from `modules/templ` and `modules/jet`,
registered through `app.Integrations().Register`. Handlers call `c.RenderWith`;
the manager buffers output and HyperChi writes the HTML response. `app.Serve`
starts and closes registered resources automatically. When using `ServeHTTP`
in tests or another HTTP server, explicitly start and close the manager.

See `/docs/integration-manager` for lifecycle, health checks, native typed access,
`integrations.Hooks`, `TypedRenderer`, and rendering with `c.RenderWith`. No third-party
renderer dependencies were added to the root module. Showcase local replaces
resolve the adapters from this checkout.

The full-page and fragment paths remain explicit: `Accept: application/json`
does not turn an HTML route into JSON. A fragment route is an ordinary
`app.Get` route; the adapter its handler calls determines the actual output. For
built-in templates, `c.Fragment` renders without a layout.

### templ: typed values to HTML

1. The browser requests `/examples/templ` and receives the complete studio page.
2. A project-scope control sends `GET /examples/templ/estimate?scope=identity`.
3. Go selects fixture data for `launch`, `identity`, or `complete`.
4. A typed templ component renders the estimate HTML.
5. htmx swaps that HTML into the estimate region.

The shared `static/js/marketing.js` uses htmx 4's `htmx:before:request`,
`htmx:before:response`, and `htmx:error` events. It cancels error-response
swaps, retains the last useful result, and exposes a retryable message. Both
HTML examples bound requests to ten seconds. See the [htmx 4 events guide](https://four.htmx.org/docs/htmx-events-guide).

The initial page and the fragment use the same component logic. The `studioEstimate` model contains the string fields `Scope`, `Price`, `Timeline`, and `Includes`. Component parameters provide a compiler-checked boundary. The GET form also works without JavaScript by requesting the full page with `scope` in the query and preserving the selected option. Keep visitor strings in ordinary escaped templ expressions; bypassing escaping is not a substitute for validating HTML.

[templ component documentation](https://templ.guide/core-concepts/components/) covers the generated component interface and buffering when a render can fail.

### Jet: runtime views to HTML

1. The browser requests `/examples/jet` and receives the complete Relay page.
2. The billing control sends `GET /examples/jet/pricing?billing=annual`.
3. Go chooses `monthly` or `annual` fixture pricing.
4. Jet renders pricing markup using request-local values.
5. htmx replaces the pricing region.

The Jet template set is initialized at startup. Each request passes a `relayPricing` value as the third argument to `Execute`, with `nil` variables. The views read `.Billing`, `.Note`, and `.Plans`; each plan contains `Name`, `Price`, and `Description`. Never mutate shared data with visitor-specific values. The GET form works without JavaScript by requesting the full page with `billing` in the query and preserving the selection. Keep Jet's HTML escaping enabled. Shared pricing markup prevents the initial page and later fragments from diverging.

The [Jet wiki](https://github.com/CloudyKit/jet/wiki) describes template composition. The showcase imports `github.com/CloudyKit/jet/v6`.

### Alpine: JSON to browser state

1. The browser requests `/examples/alpine` and receives a Go-rendered marketing page.
2. When its form is submitted, the trip finder requests `GET /examples/alpine/trips?terrain=coast`.
3. Go filters fixtures by `all`, `coast`, `mountain`, or `forest`.
4. The endpoint returns an object with `trips` and `count`.
5. Alpine updates its state and renders the matching cards.

The initial HTML contains all four trips and performs no startup fetch. `outpost.js` prevents overlapping submissions and aborts a fetch after ten seconds. A successful fetch replaces the initial cards with Alpine-rendered cards; a failed fetch shows an error and retry control.

Each trip has `name` (string), `terrain` (string), `days` (number), `price` (number), and `description` (string). `count` is the number of returned trips. The values demonstrate a public response contract; they are not live inventory or checkout quotes.

Use `x-text` for API strings rather than `x-html`. JSON encoding protects JSON syntax, not arbitrary HTML insertion. Disable controls while loading, cancel superseded requests, or ignore stale results when filters can be changed rapidly. Keep a visible loading state and an error with a way to retry.

See Alpine's [state scope guide](https://alpinejs.dev/directives/data) and [text binding guide](https://alpinejs.dev/directives/text). The example uses a pinned local Alpine runtime.

## Query validation

Omitted or empty values default to `scope=launch`, `billing=monthly`, and `terrain=all`. Unknown nonempty values return HTTP 400. The templ and Jet routes return `hyperchi.BadRequest`, which HyperChi renders for the client: an alert fragment for htmx, an error page for a browser, and a JSON `error` field for `Accept: application/json`. The trip endpoint always answers with a JSON `error` field.

## Inspect the responses

With the server running:

```sh
curl -i 'http://localhost:9999/examples/templ/estimate?scope=launch'
curl -i 'http://localhost:9999/examples/jet/pricing?billing=monthly'
curl -i 'http://localhost:9999/examples/alpine/trips?terrain=forest'
```

The first two responses contain HTML fragments. The third contains a JSON envelope. In a browser, change every scope, billing period, and terrain filter; check that the surrounding page remains in place and that the response updates the intended region. Check keyboard use and narrow screens as well as pointer interaction. With JavaScript disabled, the templ and Jet GET forms should reload their full pages with the chosen option preserved. Outpost should show its complete four-trip collection; filtering requires JavaScript.

Run the integration browser checks from `examples/showcase` with the server running and Python Playwright installed:

```sh
python3 scripts/integrations_smoke.py
```

The script exercises the integrations and captures screenshots. It defaults to `http://localhost:9999`; use `SHOWCASE_URL` for another address.

## Replace fixture data

Keep data access behind the handler. For example, a trip service can expose `ListTrips(ctx context.Context, terrain string) ([]Trip, error)`. The handler validates the query, passes `c.Ctx()` to the service, handles failures, and constructs a response model with only public fields.

A SQL implementation can use parameterized queries with `QueryContext`; an HTTP-backed implementation can use a request carrying the same context. Neither requires changing the browser contract or importing a database package into a template. No database adapter is implemented by these examples yet.

For a write operation, add a separate endpoint with the application's authentication, authorization, validation, and cross-origin protections. The existing marketing interactions are read-only and do not imply a working subscription, booking, or contact system.

## Image sources

The example pages serve local copies of these Unsplash images. The brand names and business content are fictional and do not imply an association with the photographers or pictured spaces.

| Local image | Original source |
| --- | --- |
| `static/img/examples/studio.webp` | [Studio image](https://images.unsplash.com/photo-1494438639946-1ebd1d20bf85) |
| `static/img/examples/workspace.webp` | [Workspace image](https://images.unsplash.com/photo-1497366811353-6870744d04b2) |
| `static/img/examples/mountain.webp` | [Mountain image](https://images.unsplash.com/photo-1464822759023-fed622ff2c3b) |
