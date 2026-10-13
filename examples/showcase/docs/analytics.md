---
title: Analytics hooks
section: Built-ins
order: 109
next: realtime
description: Connect completed requests and explicitly enabled browser navigation events to your own analytics provider without collecting visitor identifiers by default.
---

## Opt in to collection {#overview}

Analytics hooks expose two separate signals: completed server requests and browser navigation. The framework supplies no analytics vendor, storage, cookies, visitor IDs, or network transport. You choose which signal to use and connect it to your own recorder. Do not add server requests and browser navigations together as page views: an HTMX fragment, failed request, or API call is still a server request.

The server event contains the HTTP method, registered route pattern, status, bytes written, duration, and HTMX/aborted/hijacked flags. It excludes raw paths, query strings, headers, referrers, IP addresses, cookies, and bodies. When no pattern is available the value is empty; a mounted router may retain its registered prefix on a miss. Requests for arbitrary missing URLs do not create new analytics labels.

## Record completed requests {#example}

```go
import (
    "context"
    "log/slog"
    "net/http"

    "github.com/regiellis/hyperchi/hyperchi/analytics"
)

err := app.EnableAnalytics(analytics.Config{
    Recorder: analytics.RecorderFunc(func(ctx context.Context, event analytics.RequestEvent) error {
        // Replace this local example with your provider adapter.
        slog.InfoContext(ctx, "request completed",
            "method", event.Method,
            "route", event.Route,
            "status", event.Status,
            "duration", event.Duration,
        )
        return nil
    }),
    Include: func(r *http.Request) bool {
        // Add your application's consent decision here when appropriate.
        return r.URL.Path != "/health"
    },
    OnError: func(err error) {
        slog.Warn("analytics recorder failed", "error", err)
    },
})
if err != nil {
    return err
}
```

Call `EnableAnalytics` once on the root application, before registering routes or enabling features that register routes. It can precede both `EnableDevToolbar` and `EnablePrefetch`. Invalid or repeated setup returns an error. Framework JavaScript and developer endpoints are excluded. Other assets and API routes are included unless your `Include` callback excludes them.

For a standalone chi router, construct `analytics.New(config)` and call `router.Use(collector.Middleware)` before registering routes. Install it inside chi, where the matched route context is available. Outside a router, route patterns stay empty. Routes rejected by earlier middleware never reach this collector.

### Delivery and failures

The recorder runs synchronously after the handler returns. It must be safe for concurrent requests and return promptly. Its latency delays completion of the middleware call, though event duration excludes recorder time. The supplied context is the request context and may already be canceled. Avoid retaining that context or extracting visitor identity from its values.

A provider adapter owns its transport timeouts and any bounded queue, retries, batching, or shutdown flush. The framework creates no background goroutines and provides no delivery guarantee or persistence. Recorder errors go to `OnError`; without it they are discarded. Recorder panics become generic errors without panic payloads. Errors or panics in the error callback do not replace the application response. A panicking inclusion callback skips collection and reports an error.

Returned HyperChi handler errors are recorded after the framework renders their status. A panic records `Aborted: true` and continues to the surrounding recovery middleware. Its status is the response committed before the panic, or zero if none was committed; an outer recovery's eventual 500 is not visible at this point. Streaming requests produce their event only when the handler ends. Hijacked connections are flagged; subsequent WebSocket frames are not counted. Bytes represent writes at the hook's position, not guaranteed wire bytes. `Duration` is a Go `time.Duration` and encodes as nanoseconds in JSON.

## Browser navigation hooks {#browser}

Include the runtime in your full-page layout, followed by your own deferred setup script:

```html
{{analyticsScript}}
<script defer src="/assets/analytics-setup.js"></script>
```

Mark the current page with one explicit, stable label. Put it inside the content that HTMX replaces so the label changes with the page:

```html
<main data-hc-analytics-page="article">
  <!-- Page content -->
</main>
```

Labels must start with a letter and contain only letters, digits, underscores, dots, colons, or hyphens, up to 80 characters. Use categories such as `home`, `article`, or `checkout`; never interpolate account IDs, email addresses, or content titles. The first page marker is used. No label is inferred from the URL.

In `/assets/analytics-setup.js`:

```js
document.addEventListener('hyperchi:analytics', (event) => {
  // event.detail is an immutable {type: 'navigation', page, source} value.
  // Connect your provider here, subject to your application's policy.
  console.log(event.detail);
});

// Call only after your application has decided collection is permitted.
window.HyperChiAnalytics.setEnabled(true);

// On consent withdrawal:
// window.HyperChiAnalytics.setEnabled(false);
```

Loading the script alone emits nothing. Activation reports the currently labeled page; repeated activation does not duplicate it. The runtime observes HTMX 4's `htmx:after:swap`, including history restoration, and back/forward-cache `pageshow` restoration. Repeated swaps at the same path and query do not produce another navigation. Hash-only changes are ignored. Path and query are held only in memory for deduplication and are never included in event payloads.

Custom routers can call `HyperChiAnalytics.navigation()` after updating the URL and page marker. `isEnabled()` reports whether collection is currently permitted. Manual calls also deduplicate. Disabling drops subsequent events and resets deduplication; there is no queue of missed pages to replay. Re-enabling reports only the current page.

Browser events stay local to the document. The runtime does not patch history methods, intercept navigation, send beacons, fetch a collector endpoint, or load a third-party script. Provider integrations own their network behavior.

## Privacy defaults {#privacy}

Server hooks skip `DNT: 1`, `Sec-GPC: 1`, and `Purpose`/`Sec-Purpose` prefetch requests before calling `Include`. Browser hooks recheck Do Not Track and Global Privacy Control before every event, even after explicit activation. Application callbacks cannot override these exclusions. No user/session identity is generated or correlated across requests.

These defaults do not implement an application's consent policy. Use the server `Include` callback and browser `setEnabled` gate for that decision. Browser activation does not automatically authorize server collection, and server inclusion does not enable the browser runtime. Keep registered route patterns and explicit page labels free of private information. Adapters can add fields, but doing so is an application decision outside these defaults.

## Next steps {#next}

Use [smart prefetching](/docs/prefetch) without counting speculative requests, and [internationalization](/docs/i18n) to keep navigation labels stable across translated pages. The [developer toolbar](/docs/devtools) offers local request inspection during development.
