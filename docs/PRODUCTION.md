# Production configuration and site APIs

Create the app with `NewWithConfigE` and handle its error before accepting traffic.
`NewWithConfig` retains its single return value and panics on initialization errors.
Use `Database.Disabled = true` for an app without persistence. This disables opening
SQLite; the pure-Go driver is still linked, so the binary needs no C toolchain either way.

```go
cfg := config.FromEnv()
cfg.Database.Name = "/data/site.db" // FromEnv reads DB_PATH into this field
cfg.Database.Timeout = 5 * time.Second
cfg.Security.CSPPolicy = "default-src 'self'; frame-ancestors 'none'; form-action 'self'"
cfg.Security.HSTS = "max-age=31536000"
app, err := hyperchi.NewWithConfigE(cfg)
if err != nil {
    return err
}
defer app.Close()
```

Configure before serving. The constructor snapshots the supplied config, including
proxy ranges. `config.Production()` works without any environment variable.
`FromEnv` resolves `ENVIRONMENT`, then `GO_ENV`, then `PRODUCTION=true`; otherwise it
selects development. `New()` intentionally remains a development constructor.
`config.Minimal()` loads the environment and disables optional database, cache, and
watching services. Its default CSP permits same-origin assets without inline code
or eval. An explicit `CSP_POLICY` is retained. The full frontend preset permits
features required by HyperChi directives; choose a stricter policy when those
features are unused. HSTS defaults to this host only; an empty `Security.HSTS`
disables it. Subdomains and preload are explicit application choices.

## Client identity and rate limits

`AutoMiddleware` preserves `Request.RemoteAddr`. With no trusted proxies it derives
client identity from that peer. Set `Security.TrustedProxies` to actual proxy CIDRs
only when needed. A direct untrusted peer cannot select a client IP with a header.
Trusted chains are examined from right to left; unverified prefixes never take
precedence over the first untrusted address. Invalid CIDRs fail construction.

Core and `hyperchi/middleware` rate-limit entry points share one token-bucket
implementation. Compatibility `RequestsPerWindow` and `WindowDuration` configure
the refill rate; `Burst` and `RefillInterval` provide explicit token-bucket control.
This changes boundary behavior from the previous fixed-window implementation.
There are no cleanup goroutines. Keys are capped by `MaxKeys` (default 10,000);
when full, inactive buckets are reclaimed and new keys are denied until capacity
is available. Existing buckets continue to work. Limits are process-local.

```go
limiter, err := hyperchi.NewRateLimiterE(&hyperchi.RateLimitConfig{
    Burst: 20,
    RefillInterval: 3 * time.Second,
    MaxKeys: 1,
    KeyGenerator: func(*http.Request) string { return "signups" },
})
```

`Retry-After` is always set on denial, rounded up to a positive number of seconds.
Forwarding headers are never trusted by default, including from loopback. Use a
trusted-proxy configuration or a server-owned key function. No paths or methods
are exempt unless explicitly configured.

## Documents and errors

`TemplateRenderer` returns layout execution failures in both environments.
An explicitly selected missing layout is an error. A missing implicit base layout
still permits a complete standalone page template.

```go
if err := app.LoadTemplates("templates/**/*.tmpl"); err != nil {
    return err
}
page, err := app.PreRender("index", hyperchi.H{"title": "Home"},
    hyperchi.PreRenderOptions{Layout: "base"})
if err != nil {
    return err
}
app.Get("/", page) // PreRender returns a Handler
app.Head("/", page)
```

`PreRenderedPage` combines rendering and GET/HEAD registration. `PreRender` accepts
only 200 or 400-599 statuses. Success documents have a weak ETag and default
`no-cache`; HEAD and conditional requests use the same immutable content. Error
documents always carry their real status, `no-store`, and `X-Robots-Tag: noindex`,
and do not become 304 responses. Layout data is a map or `hyperchi.H`; other values
are available through `.Result`. App globals (`app.SetGlobal`) are rendered in.

Every error goes through one pipeline: a `Handler`'s returned error, router 404 and 405,
static-file misses, middleware denials such as the rate limiter's 429, and panics caught
by `Middleware.Recovery`. `DefaultError` renders JSON for API clients (or every client of
a route wrapped in `hyperchi.JSONErrors`), the `error` template as a fragment for htmx,
and the `error` template or a built-in page for browsers. Replace it with `app.OnError`
before serving; `no-store` and `X-Robots-Tag: noindex` are set first either way. A
pre-rendered error page makes a fully branded, request-independent status page:

```go
notFound, err := app.PreRender("error", hyperchi.H{"code": 404}, hyperchi.PreRenderOptions{Status: 404})
if err != nil {
    return err
}
app.OnError(func(c *hyperchi.Context, err error) {
    var he *hyperchi.HTTPError
    if errors.As(err, &he) && he.Code == http.StatusNotFound {
        if perr := notFound(c); perr != nil {
            log.Printf("404 page: %v", perr)
        }
        return
    }
    c.App().DefaultError(c, err)
})
```

Keep internal errors in logs; render only public copy. In development a 5xx with an
underlying cause shows that cause in full. `SetErrorTemplate` also works in production,
with sanitized details.
Recovery rethrows `http.ErrAbortHandler`. A panic after headers have been committed
aborts the connection; it cannot change an already-sent status to 500. Context
errors after a write are logged without appending a second document.

## Published files

`NewStaticHandler(root, options)` returns a standalone handler. `StaticWithOptions`
mounts it at `/prefix/*` and returns setup errors. The compatibility `Static` and
`FileServer` methods panic on invalid setup because their signatures cannot return
errors. Roots must exist at startup.

Only regular files and GET/HEAD are allowed. Directories, dotfiles, traversal paths,
and symlinks escaping the root are rejected. `os.Root` confines the open operation,
including during filesystem changes. Byte ranges and Last-Modified validation are
handled by `http.ServeContent`. `StaticOptions` accepts a cache policy and an error
callback; development forces `no-cache`. Rejected publications use no-store/noindex.

## Browser forms

`ProtectForm` uses `net/http.CrossOriginProtection`, then an optional rate limiter,
then bounded URL-encoded parsing. It supplies `Request.PostForm` to the handler.
Set `OnError` for the same branded response across origin, rate, media-type, and
body errors. It accepts only `application/x-www-form-urlencoded`; use a separate
upload handler for multipart forms. The default cap is 4 KiB and oversized bodies
return 413; `TooLargeStatus: 400` preserves an existing application contract.

This browser-origin check allows clients with neither Origin nor Fetch Metadata.
It is not authentication or bot detection. Honeypots, identity, field validation,
address deduplication, and redirects remain application concerns.

## Serving and scaffolds

`Serve` owns signal handling. `ServeContext` lets a parent runtime own cancellation.
Both drain requests and close app services. `Close` is idempotent. Server config
includes ReadHeaderTimeout, MaxHeaderBytes, ShutdownTimeout, and the existing
read/write/idle deadlines. The defaults are 5-second headers, 32 KiB headers, and
10-second shutdown. Streaming apps must deliberately choose their write deadline.
Production always binds the specified port. Set `Server.PortFallback` explicitly
for development-only fallback.

`hyperchi new example --template site` creates templates, static files, branded
errors, and a liveness route without Node tooling or an initialized database.
`--template full` retains Vite and HTMX. Both use the hardened server path and
support `--framework-path` while developing against a local checkout.
