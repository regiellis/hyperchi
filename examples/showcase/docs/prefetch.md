---
title: Smart prefetching
section: Built-ins
order: 107
next: i18n
description: Warm the browser cache for explicitly marked links on hover, keyboard focus, or viewport entry, with cancellation and reduced-data awareness.
---

## Fetch before a navigation {#overview}

Smart prefetching starts a speculative GET when a marked link receives sustained hover or keyboard focus, or becomes visible. It warms the browser's HTTP cache. A later native or HTMX navigation can reuse that response when its cache policy and request headers allow it.

No separate JavaScript response cache is used. `Cache-Control: no-store` still prevents reuse, `no-cache` still requires revalidation, and `Vary` still separates representations. Prefetching never changes the DOM, pushes history, or executes a response's scripts. Only mark URLs whose GET is safe to run before a user clicks.

## Enable and mark links {#example}

Enable on the root app during setup, after your middleware and before serving traffic. The call is idempotent and works before or after other routes have been registered:

```go
app := hyperchi.NewFromEnv()
app.Use(app.Middleware.Default()...)
if err := app.EnablePrefetch(); err != nil {
    log.Fatal(err)
}
```

Put the helper after htmx in the full-page layout. It emits a deferred external script at `/hyperchi/js/prefetch.js`:

```html
<script defer src="/static/htmx.js"></script>
{{prefetchScript}}

<a href="/guides" data-hc-prefetch="hover">Guides</a>
<a href="/about" data-hc-prefetch="viewport">About</a>
<a href="/articles" data-hc-prefetch="both">Articles</a>
```

`hover` includes keyboard focus. `viewport` starts when the link actually enters the viewport, without a look-ahead margin. `both` enables either strategy. Each waits 100 milliseconds before fetching. An unmarked link is never prefetched, and `data-hc-prefetch="off"` on a link or ancestor opts that subtree out. Other strategy values are ignored.

Go renderers can call `app.PrefetchScript()`. Without `EnablePrefetch`, the helper is empty and no prefetch middleware or endpoint is installed. The runtime can also be served directly from `frontend/assets/scripts/hyperchi-prefetch.js`; in that case your server must provide the appropriate `Vary` headers itself.

## Cache policy and HTMX {#cache}

The server must opt responses into useful caching. For content safe to reuse for one minute in the current browser, a handler can set:

```go
app.Get("/guides", func(c *hyperchi.Context) error {
    c.SetHeader("Cache-Control", "private, max-age=60")
    return c.View("guides", hyperchi.H{"title": "Guides"})
})
```

Choose freshness according to your content. An account page that must reflect a recent mutation may need `no-cache` or `no-store`. The runtime clears its speculative queue when HTMX starts a non-GET/HEAD request; it cannot invalidate the browser's HTTP cache after a write. Do not make cacheable responses depend on stale session state.

Ordinary HTMX 4 GET links are supported:

```html
<a href="/guides" hx-get="/guides" hx-target="#content"
   data-hc-prefetch="hover">Guides</a>
<div id="content"></div>

<nav hx-boost:inherited="true">
  <a href="/articles" data-hc-prefetch="viewport">Articles</a>
</nav>
```

The runtime sends the standard HTMX request headers for these links, including full/partial request type and source/target identifiers. It supports boolean boost, simple `#id` or `body` targets, default `data-hx-` aliases, and explicit `:inherited` attributes. It also follows htmx's `implicitInheritance` setting. The actual navigation remains entirely under HTMX's control.

`EnablePrefetch` adds `Vary` for `HX-Request`, `HX-Request-Type`, `HX-Boosted`, `HX-Target`, `HX-Source`, and `HX-Current-URL`. Those values are retained even if an inner middleware replaces `Vary` before writing. Handlers must still declare any additional request headers they vary on, such as `Accept` or custom authorization/context headers. Different sources, targets, or current URLs can therefore require distinct cached responses.

Links with dynamic/custom request attributes such as `hx-headers`, `hx-vals`, `hx-include`, `hx-config`, `hx-confirm`, non-GET methods, appended inheritance, or inline HTMX event handlers are skipped. Custom HTMX prefixes/meta characters and extended target expressions are not supported. Global application request hooks cannot be predicted: opt out links whose requests depend on them. See the official [HTMX event guide](https://four.htmx.org/docs/htmx-events-guide) for the request lifecycle.

## Budgets and cancellation {#limits}

The runtime uses these fixed limits:

- Two simultaneous speculative fetches, with at most 64 delayed/queued candidates.
- A five-second timeout and a one-MiB body limit per request.
- A 30-second attempt-deduplication window with at most 128 remembered request keys.
- At most 512 viewport candidates observed per scan.

Only same-origin HTTP(S) links are eligible. Downloads, links with `ping`, non-self targets, credential-bearing URLs, fragment-only links, the current page, and pages with a targeted `<base>` are skipped. Redirects are rejected, and only successful HTML responses are drained into the browser cache. Speculative requests use same-origin credentials, low fetch priority where supported, and `Purpose: prefetch`.

Leaving hover/focus or the viewport cancels work when no other strategy still wants that element. Clicking cancels that element's pending fetch so normal navigation can proceed. Removing a link, going offline, hiding the page, or requesting reduced data cancels pending work. An aborted or failed attempt is not retried immediately; the deduplication window prevents repeated speculative failures.

Prefetching is disabled for `Save-Data`, reported 2G/slow-2G connections, and `prefers-reduced-data: reduce`. Where the browser does not report connection preferences, those signals are unavailable. Without `IntersectionObserver`, hover and focus still work. New links inserted by HTMX swaps or ordinary DOM changes are scanned automatically.

For explicit lifecycle control:

```js
window.HyperChiPrefetch.refresh(); // rescan links
window.HyperChiPrefetch.clear();   // cancel speculation and forget attempts
window.HyperChiPrefetch.stop();    // permanently stop this page's runtime
```

`clear()` does not clear the browser's HTTP cache. The runtime does not poll. Its observers and event handlers exist only after the script is included.

## Keep going {#next}

The [realtime guide](/docs/realtime) covers updates after navigation. The [developer toolbar](/docs/devtools) helps inspect request patterns and timings while developing locally.
