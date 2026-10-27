---
title: Developer toolbar
section: Built-ins
order: 106
next: prefetch
description: Inspect DOM elements, browse registered routes, and review recent local requests with an opt-in developer toolbar.
---

## Inspect a local application {#overview}

The developer toolbar adds three views to a page: completed requests, registered routes, and an inspector for request details or DOM elements. It is opt-in, has no JavaScript dependencies, and does not change rendered response bodies. Put the template helper in your full-page layout and open **HC · Dev tools** at the bottom right.

The toolbar runs only when the application's configured environment is `development`. In production, enabling it is a no-op: no routes or middleware are installed, and the template helper returns empty HTML.

## Enable before registering routes {#example}

Install application middleware first, enable the toolbar on the root app, then register routes and load templates:

```go
app := hyperchi.NewFromEnv()
app.Use(app.Middleware.Default()...)
if err := app.EnableDevToolbar(hyperchi.DevToolbarOptions{
    MaxRequests: 100,
}); err != nil {
    log.Fatal(err)
}
if err := app.LoadTemplates("templates/*.tmpl"); err != nil {
    log.Fatal(err)
}
app.Get("/", func(c *hyperchi.Context) error {
    return c.View("index", hyperchi.H{"title": "Home"})
})
```

Add this once, near the end of the full-page layout:

```html
{{devToolbar}}
</body>
```

Go renderers can use `app.DevToolbar()` instead. Both return an external script tag when enabled in development. Partial responses need no toolbar markup. The browser prevents duplicate toolbar instances if the script runs again.

Call `EnableDevToolbar` only once, before any route registration, including static mounts and extension routes. It returns an error for late or duplicate setup, a route group, or an invalid retention limit. `MaxRequests` defaults to 100 and accepts 1–1000.

## Requests and routes {#requests}

Open **Requests** to see the newest completed requests first. Click a request to inspect its method, registered route pattern, status, response-writer byte count, start time, duration in milliseconds, and HTMX flag. Click **Refresh** after making requests to update the snapshot. There is no polling or background goroutine.

The bounded log keeps completed local requests in memory. Old entries are overwritten. Active SSE streams and WebSocket connections appear only when their handler returns. The wrapper preserves flushing and hijacking support. Application recovery responses and middleware rejections are recorded too. If a panic escapes the application middleware entirely, its record has `aborted: true`; status is zero if no response had started when the request unwound. That record does not claim to know any recovery response outside the app.

**Routes** lists methods and patterns registered with chi, including route groups and raw HTTP registrations. GET routes also answer HEAD even where there is no separate HEAD registration. An opaque mounted handler can only expose its mount pattern. Finish registering routes before serving traffic.

The logs contain route patterns such as `/users/{id}`, not actual parameter values. Query strings, headers, cookies, bodies, and application log messages are not collected. Unmatched paths and requests rejected before routing appear as `(unmatched)`. Developer endpoints are excluded from the log. Route literals themselves are visible, so keep secrets out of route definitions.

This first increment provides request logs. Capturing arbitrary application logs and their attributes is separate work because those records need an explicit redaction policy.

## Inspect an element {#inspector}

Choose **Inspector**, then **Pick element**, and click an element on the page. The inspector shows its tag, ID, classes, role, dimensions, display mode, font size, padding, and margin. The pick cancels that click's normal action. Press Escape to cancel picking, or Escape again to close the toolbar. Element details stay in the browser; text content and form values are not collected.

The inspector describes the rendered DOM. It does not map an element back to a Go template or component source. The floating panel uses Shadow DOM to isolate its styles and fits narrow screens. Scripts and styles are served as separate same-origin assets, so it works with `script-src 'self'` and `style-src 'self'` without allowing inline scripts or styles.

## Local access only {#access}

The reserved endpoints live under `/__hyperchi/devtools/`. Access requires a loopback network peer and a Host of `localhost` or a loopback IP. Browser requests must be same-origin. Remote clients, nonlocal hosts, and cross-origin browser requests receive 404. The access check runs before application middleware can rewrite the peer address; forwarded headers do not grant access.

Use a direct connection such as `http://localhost:8080`. Development through a remote preview, custom hostname, or proxy is not supported by this increment. Responses use `Cache-Control: no-store`. Enable the toolbar during application setup and treat configuration as immutable while serving.

## Keep going {#next}

The [smart prefetching guide](/docs/prefetch) covers fetching safe links before navigation. The [testing guide](/docs/testing) covers handler tests without starting a server.
