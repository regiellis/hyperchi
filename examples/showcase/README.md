# HyperChi website

The marketing site and documentation run in the same HyperChi application. The homepage includes real HTML/JSON requests, SSE metrics, a WebSocket room, live search, upload validation, and a multi-step form.

## Run

With Go 1.26.8 or newer installed. No C compiler is needed: HyperChi's SQLite driver is pure Go, so `CGO_ENABLED=0` builds work.

```sh
task run PORT=9999
```

Open `http://localhost:9999/` for the homepage and `/docs` for the getting-started guide. Use `task dev PORT=9999` for Air hot reload. `task build` compiles the application; `go vet ./...` checks its Go code.

The documentation covers getting started, routing, templates, template filters, handlers and helpers, realtime connections, directives, integrations, and data passing. `/docs/filters` includes live recipes and all 225 registered filters grouped by category. The sidebar topic filter matches navigation labels; the search box above each article searches titles, headings, and text across every page.

## Documentation pages

Each topic is a Markdown file in `docs/` with front matter (`title`, `order`,
`next`, an optional `nextLabel`, and `description`, which is the lede and the
page's llms.txt note). The files are embedded and rendered at startup by the
optional `modules/markdown` module (goldmark with GFM and heading IDs).
`templates/docs.tmpl` supplies the layout, sidebar, and table of contents.
Explicit heading IDs (`## Pages and fragments {#overview}`) keep the anchors other
pages link to. A `<!-- slot:name -->` line places content generated from Go: the
filter recipes and registry reference (`filters_docs.go`) and the directives demo.

The same Markdown feeds four routes:

- `/docs/<topic>.md` serves a page's Markdown as `text/markdown` (`/docs/start.md`
  and `/docs/index.md` for `/docs`).
- `/llms.txt` lists every page's Markdown URL in the llmstxt.org format, and
  `/llms-full.txt` concatenates all pages in reading order.
- `/docs/search?q=` searches titles, headings, and text: an htmx request gets the
  results fragment, a browser gets a results page.

With `PUBLIC_ORIGIN` set, llms.txt and the "Open in Claude" and "Open in ChatGPT"
prompts use absolute URLs; without it they use relative paths. Every code block
gets a copy button, and each page has Copy page, View as Markdown, and assistant
links.

## Integration marketing pages

Open `/docs/integrations` for the new guides and these complete examples:

- `/examples/templ`: Form & Field, a studio with a live project estimate.
- `/examples/jet`: Relay, a collaboration product with server-rendered billing options.
- `/examples/alpine`: Outpost, an outdoor club with a JSON-powered trip finder.

Every route, here and in the main site, is a `func(c *hyperchi.Context) error`
registered with `app.Get` or `app.Post`. The examples use request helpers, typed
collection filtering, `c.JSON`, and `c.Status`. Outpost uses `c.View`; templ and Jet
keep only their external rendering adapters. `/docs/helpers` explains these choices.

See [INTEGRATIONS.md](INTEGRATIONS.md) for source maps, regeneration, data contracts,
and browser checks. The showcase requires Go 1.26.8; the core library requires
Go 1.26.0. No external services or credentials are needed.

## Frontend assets

Static assets are checked in, so running the site does not require Node or a CDN. The application uses native CSS, HyperChi directives for local controls, Anime.js for text and request feedback, and Three.js for the full-width torus network behind the hero. The docs do not load Three.js. Mobile uses the static artwork until the visitor chooses “Play motion”; reduced-motion visits do not load the 3D library.

To refresh the pinned Three.js, Space Grotesk, and Alpine.js files:

```sh
npm ci
npm run vendor
```

`package-lock.json` pins the dependencies. `scripts/vendor.mjs` minifies the Three.js modules and copies the font and licenses into `static/`. Anime.js and the htmx runtime use the repository's existing standalone bundles. The directives bundle comes from `frontend/dist/hyperchi-directives.js`; rebuild it with `npm run build:lib` in `frontend/` before copying it here.

The hero's `static/img/network.webp` (and smaller `network-mobile.webp`) is a transparent browser capture of the actual Three.js canvas with its connected nodes and packet trails. It remains visible while WebGL loads, when WebGL is unavailable, or when JavaScript is disabled. Render `static/img/og-card.html` at 1200 × 630 to regenerate `static/img/og.png`.

## Browser verification

With the server running and the Python `playwright` package plus Chrome/Chromium installed:

```sh
python scripts/smoke.py
python scripts/security_smoke.py
```

The script defaults to `http://localhost:9999`; set `SHOWCASE_URL` or `CHROME_PATH` when needed. It exercises the actual application and writes screenshots under the ignored `tmp/screenshots/` directory.

## Interaction checks

Check both light and dark themes, narrow screens, and reduced motion:

- Send HTML and JSON requests in the playground; check the live SSE metrics.
- Open the WebSocket experiment in two tabs and send a message.
- Search the changelog, validate a text upload, and advance through the form.
- Copy the install command and follow the getting-started link.
- Filter documentation topics and use the counter on `/docs/directives`.
- Search the docs (press `/` or Ctrl+K), copy a code block, and copy a page as Markdown.
- Pause the network; check that it also stops offscreen and in a background tab.

Content and navigation work without JavaScript. Interactive demos require JavaScript. The sculpture honors reduced motion and falls back to its static image if WebGL cannot initialize.

## Public hosting and security

The server now binds **127.0.0.1** by default and always uses production behavior,
including when Air rebuilds it. It has no development reload endpoint or debug
metadata endpoint. Static assets are embedded at build time and served by
`app.StaticFS`, with no directory listing or access to the server filesystem.
Pages link them with `{{asset "/static/..."}}`, so each URL carries a content
version and is cached for a year. Rebuild after changing static assets.

For public hosting, put a TLS reverse proxy in front of the localhost backend and
set the exact browser origin (no trailing slash):

```sh
PUBLIC_ORIGIN=https://hyperchi.example PORT=9999 ./tmp/showcase
```

Configure the proxy to preserve the original Host, support WebSocket upgrades,
and stream SSE without buffering. Expose only the proxy's HTTPS port; keep port
9999 private. A non-loopback `HOST` is rejected unless `PUBLIC_ORIGIN` is set.
Run the site as a dedicated unprivileged user with no repository credentials,
SSH keys, Docker socket, or production database access. The showcase uses an
in-memory database and its demos do not persist uploads or form submissions.
The multi-step form keeps a visitor's answers in an in-memory session (only on
`/waitlist`, expiring after 30 minutes) until the last step, which discards them.

The public activity room is intentionally shared between visitors. WebSockets
are receive-only: only the server can generate broadcast markup. Same-origin
checks cover forms and realtime connections, and an exact Host allowlist guards
against DNS rebinding. Header values and messages are escaped. Public uploads
accept one file, at most 2 MiB, and are validated without storage: keeping
anonymous visitors' files would make the demo free file hosting, and the
container has no writable data volume. The docs show `SaveAll` for apps that
store uploads.

Resource budgets per process:

- Form bodies: 16 KiB; uploads: 2 MiB plus 64 KiB multipart overhead.
- Requests: 300/minute per TCP peer, 3,000/minute globally, excluding static assets.
- POST requests: 30/minute per TCP peer, 120/minute globally.
- Realtime connections: 6 per TCP peer, 64 globally; 4 concurrent uploads.
- Activity feed: the latest 50 entries in each browser.

Forwarding headers are deliberately **not trusted**. Behind a proxy, backend
per-peer limits apply collectively to that proxy. Add per-visitor rate limits,
connection limits, and request size limits at the trusted edge; do not enable
blind trust of `X-Forwarded-For`. These application budgets do not replace edge
protection against distributed traffic or slow connections.

The CSP disallows inline scripts and third-party scripts. It retains
`unsafe-eval` because the hc-* expression engine needs it, and inline styles for
the animation runtime. Consequently, escaping all visitor-controlled markup
remains essential; CSP is not a sanitizer.

Build using the pinned **Go 1.26.8 or a newer patched release**. Do not bypass the
toolchain pin with an older `GOTOOLCHAIN=local`. Security verification:

```sh
go test -race ./...
go vet ./...
govulncheck ./...
```

At the repository root, also run
`go test -race -tags=integration ./hyperchi/extensions/websocket ./hyperchi/extensions/sse`
and `govulncheck ./...`. Run `npm audit` in both `frontend/` and this directory.
The audit found and fixed a client-to-client WebSocket HTML injection, reflected
header HTML injection, permissive development exposure, and reachable dependency
advisories. The updated Go scan has no reachable/imported-package advisories;
it still reports advisories in unused packages in required modules. Deployment
infrastructure and other example applications were not penetration-tested.

## Dokploy on the Playlogic VPS

The hosting workflow mirrors `www-playlogic-go`: build on the development host,
push through an SSH tunnel to the VPS's loopback registry, then let Dokploy pull
the image. The registry is not exposed publicly. HyperChi uses its own service
`www-hyperchi-go` and image `localhost:5000/hyperchi/hyperchi-web`.

The Docker build context is the repository root because the showcase imports the
local framework module. This packages the framework and website from the same
source revision. No production build depends on another checkout.

```sh
# From the repository root, local image verification:
docker build -f examples/showcase/Dockerfile --build-arg RELEASE=preview -t hyperchi-web:preview .
```

`docker-compose.deploy.yml` has no host port mappings. Dokploy must route the
chosen HTTPS domain to service `web`, port `8080`, using Let's Encrypt. The
container deliberately binds all interfaces inside its private Docker network.
It runs as UID 10001, with a read-only root, a bounded temporary filesystem,
dropped capabilities, no privilege escalation, and CPU/memory/PID/log limits.

Credentials already available in the interactive shell are used by the scripts:
`DOKPLOY_HOST` and `DOKPLOY_API`. Never put them in Compose or an image. Configure
these non-secret values for the new HyperChi service:

```sh
export HC_DOMAIN=docs.example.com
export HC_RELEASE_VPS=deploy@your-vps
export HC_DOKPLOY_ENVIRONMENT_ID=YOUR_ENVIRONMENT_ID
# One-time creation, only after selecting the intended project/environment:
python3 scripts/dokploy.py create
# Save the returned service ID:
export HC_DOKPLOY_COMPOSE_ID=RETURNED_ID
```

After reviewing and committing the source, run from `examples/showcase` in the
interactive shell:

```sh
python3 scripts/release.py plan
python3 scripts/release.py stack
```

The release runs checks, builds, pushes through SSH, updates only the named
HyperChi service, and verifies the public homepage and `/healthz` with matching
`X-Release`. It refuses a dirty source tree or a busy/mismatched service. Roll
back by setting `HC_RELEASE_TAG` to a previously pushed, verified release and
running `python3 scripts/release.py deploy`. Other services and volumes on the
same Dokploy host are never deployment targets for these scripts.

DNS must point the selected hostname at the Dokploy host before public
verification. DNS credentials stay on the development host; the application
needs none.

For an explicitly enabled Tailscale preview on this development machine:

```sh
HOST=0.0.0.0 PORT=9999 PREVIEW_ORIGIN=http://100.64.0.10:9999 task run PORT=9999
```

`PREVIEW_ORIGIN` allows one exact HTTP Tailscale IP or `.ts.net` hostname with
its port. It preserves host and same-origin checks. Public hosting continues to
require an HTTPS `PUBLIC_ORIGIN`.
