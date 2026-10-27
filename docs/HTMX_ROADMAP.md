# HTMX Version Support

HyperChi targets **htmx 4.0.0 stable**, released on 2026-08-28. Frontend,
scaffold, and streamer-vite package manifests pin `htmx.org` to `4.0.0` exactly.
The standalone examples vendor the same runtime; the framework's asset bundler
uses its versioned CDN URL. SSE and WebSocket scripts come from that release's
`dist/ext` directory and are copied into the embedded runtime assets.

Verified against the [official release announcement](https://four.htmx.org/announcements/2026-08-28-htmx-4.0.0-is-released)
and npm package on 2026-09-16. npm's `latest` tag still points to htmx 2, so use
an explicit version when installing or refreshing assets.

## Where htmx 4 shows up in the framework

- **Embedded extension scripts.** The SSE and WebSocket bridge scripts (`hx-sse.js`,
  `hx-ws.js`) are compiled for htmx 4 and embedded in the framework binary, served at
  `/hyperchi/js/*`. No CDN or filesystem lookup.
- **htmx 4 event names.** The client runtime uses the `htmx:<phase>:<system>` scheme
  (for example `htmx:after:swap`, which the `hc-*` directive plugin hooks to re-walk
  swapped content).
- **WS/SSE swap shape.** `SSEHub.BroadcastSwap` and `WebSocketHub.BroadcastSwap` send the
  `content`/unnamed-message shape that htmx 4 auto-swaps.
- **Selector-string swap targets.** The client bridges resolve htmx 4's selector-string
  swap target before re-walking swapped content for `hc-*` directives.
- **View transitions are opt-in per swap.** Do not enable transitions globally in
  `htmx-config`; use `hx-swap="... transition:true"` on deliberate, low-frequency swaps
  only. Global transitions repaint the whole document on every swap and break live
  SSE/WS regions.

## Server-side surface

The server-side contract is unchanged from htmx 2: request detection via `HX-Request`,
response headers (`HX-Trigger`, `HX-Redirect`, `HX-Retarget`, ...), and the template
attribute helpers (`hxGet`, `hxPost`, ...) all carry over.

## Integration notes

- Attribute inheritance is explicit. Use `hx-boost:inherited` when enabling
  boosted links on a containing element.
- Extensions register when their scripts load. `hx-ext` markers are unnecessary
  and have been removed from the showcase.
- Load core before extension scripts. htmx 4 docs live at https://four.htmx.org.
- `docs/HTMX.md` is a historical htmx 2 reference, not the current integration guide.

## Verification

Build `frontend` and `examples/streamer-vite` with `npm run build`, and run the
root Go tests. The release upgrade checker can scan templates as well as scripts:

```bash
node frontend/node_modules/htmx.org/dist/scripts/upgrade-check.js --ext .tmpl cmd/hyperchi/_scaffold/templates examples/showcase/templates examples/streamer/templates examples/streamer-vite/templates
```

The showcase was checked in headless Chrome with htmx 4.0.0: fragment swaps, SSE
metrics, and WebSocket activity broadcasts work without JavaScript exceptions.
