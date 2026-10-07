---
title: Security
section: Production
order: 140
next: deploying
description: What HyperChi protects by default, what you switch on yourself, and the few places where input must never be trusted: client IPs, WebSockets, HTML, uploads, and secrets.
---

## What is on by default {#overview}

`app.AutoMiddleware()` installs the stack most apps want, and it must run before the first route. It adds request IDs, the client address (see below), `GetHead`, then `app.Middleware.Default()`: panic recovery, request logging, htmx headers, CORS, and the security headers. Outside development it also adds gzip, which is safe for CSRF tokens because they are masked on every render.

`Middleware.Security()` always sends `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, and a `Referrer-Policy`. When `ENVIRONMENT=production` it adds a `Content-Security-Policy`, a `Permissions-Policy` that turns off camera, microphone, and location, and `Strict-Transport-Security` (one year, this host only, unless you change `HSTS`). The default CSP allows `'unsafe-eval'` because `hc-*` directives and some htmx features compile expressions. An app that avoids them can set `CSP_POLICY` to something stricter. `config.Minimal()` does that for a templates-and-assets site: same-origin only, no inline script.

Two protections are **not** in the default stack, because they need a decision from you:

- **CSRF.** `app.Middleware.CSRF()` is double-submit cookie protection. Unsafe methods must send the token back or get a 403 through your error pipeline.
- **Rate limiting.** `app.Middleware.RateLimit(n, window)` keys on the client IP and answers 429 with `Retry-After`. Limits live in process memory, so each instance counts on its own. `app.AutoMiddleware()` reads three switches from the config: `Security.CSRF` (on by default) installs CSRF protection, `Security.RateLimit` (off by default) adds a limit of 100 requests a minute per client, and `Security.CORS` (on by default) sends the CORS headers for `CORS_ORIGINS`. Turn the rate limit on only after `TRUSTED_PROXIES` names your proxy, or every visitor shares the proxy's budget.

CORS sends no headers in production unless you set `CORS_ORIGINS` to one origin.

## Wire it up {#example}

```go
cfg := config.FromEnv() // ENVIRONMENT, TRUSTED_PROXIES, CSP_POLICY, HSTS, SECRET_KEY
cfg.Security.CSPPolicy = "default-src 'self'; frame-ancestors 'none'; form-action 'self'"

app, err := hyperchi.NewWithConfigE(cfg)
if err != nil {
    return err
}
app.AutoMiddleware()
app.Use(
    app.Middleware.RateLimit(120, time.Minute),
    app.Middleware.CSRF(),
)
```

Put the token in every form with `c.CSRFToken()`, and send it as a header from htmx. Each call returns a freshly masked value, so call it once per render:

```go
app.Get("/contact", func(c *hyperchi.Context) error {
    return c.View("contact", hyperchi.H{"csrf": c.CSRFToken()})
})
```

```html
<form method="post" action="/contact">
  <input type="hidden" name="csrf_token" value="{{.csrf}}">
  ...
</form>

<body hx-headers:inherited='{"X-CSRF-Token": "{{.csrf}}"}'>
```

For a plain form endpoint with no session, `app.ProtectForm` is a lighter option: it refuses cross-origin browser posts, caps the body (4 KiB by default), and requires a URL-encoded form. It is not authentication.

### Client IPs

Never read `X-Forwarded-For`, `X-Real-IP`, or `RemoteAddr` yourself for a rate limit or an audit log. Any client can send those headers. Use `c.ClientIP()`:

```go
app.Post("/signup", func(c *hyperchi.Context) error {
    app.Helpers.Logger.Info("signup from %s", c.ClientIP())
    return c.Status(http.StatusCreated).Text("ok")
})
```

With no configuration it is the TCP peer. Behind a reverse proxy, list the proxy's addresses in `TRUSTED_PROXIES` (or `cfg.Security.TrustedProxies`), for example `172.18.0.0/16`. Only a request whose peer is in that list has its `X-Forwarded-For` read, and the chain is walked from the right, stopping at the first address that is not trusted. The leftmost entry is client-controlled and is never believed. An invalid CIDR fails at startup. Do not install chi's `middleware.RealIP`. If you build your own stack without `AutoMiddleware`, declare the proxy with chi's `app.Use(middleware.ClientIPFromXFF("172.18.0.0/16"))`.

### WebSockets

Sockets are same-origin by default. In development, `EnableWebSocket` and `app.WebSocket` relax the origin check so a remote browser can connect through a dev server. In production the default stays same-origin, and an explicit policy always wins. Set it before registering the route:

```go
hub := app.Extensions().GetWebSocketHub()
hub.SetOriginPatterns([]string{"app.example.com"})
hub.SetIdentifyUser(func(r *http.Request) string {
    return security.GetUserIDFromContext(r.Context())
})
hub.SetRoomAuthorizer(func(conn *websocket.WebSocketConnection, room string) bool {
    return conn.UserID != "" && room == "user:"+conn.UserID
})
app.WebSocket("/ws", func(s *hyperchi.Socket) error {
    for {
        if _, err := s.Receive(); err != nil {
            return err
        }
    }
})
```

A connection's user ID comes only from `SetIdentifyUser`, never from `?user_id=`, a header, or a message. A client's `join_room` is refused until you install a room authorizer, and the authorizer also checks `?rooms=` at the handshake. Server code (`s.Join`, `JoinRoom`) skips it. For a page that only listens, `hub.SetReceiveOnly(true)` closes the connection on any client message.

### Escaping

Templates use `html/template`, so `{{.body}}` is escaped for its context. Render user input through a template, never by building markup:

```go
app.Post("/comments", func(c *hyperchi.Context) error {
    in, err := hyperchi.Bind[comment](c)
    if err != nil {
        return err
    }
    return c.Fragment("comment", hyperchi.H{"body": in.Body})
})
```

`c.HTML` takes a `template.HTML`, which is a promise that the markup is already safe. Writing `c.HTML(template.HTML("<p>" + in.Body + "</p>"))` breaks that promise, and it is a stored XSS hole. If you must assemble a string, pass every value through `html.EscapeString` first, or compose with `app.RenderToString(name, data)`, which escapes through the same templates.

### Uploads

`security.DefaultUploadConfig()` allows common image types, 10 MB, five files, and sets `RequireAuth`, which rejects an upload with no verified user in the request. Keep it on unless the endpoint is meant to be public. Validate first, then store:

```go
cfg := security.DefaultUploadConfig()
cfg.MaxFileSize = 2 << 20
cfg.MaxFiles = 1
validator := security.NewUploadValidator(cfg)

app.Post("/avatar", func(c *hyperchi.Context) error {
    res := validator.ValidateUpload(c.Request())
    if !res.Valid {
        return hyperchi.Unprocessable(strings.Join(res.Errors, "; "), nil)
    }
    saved, err := res.SaveAll("uploads", security.SaveOptions{})
    if err != nil {
        return err
    }
    return c.Status(http.StatusCreated).JSON(saved)
})
```

`SaveAll` names each file with random bytes and an extension chosen from the type it sniffed in the content, writes through `os.Root` so nothing escapes the directory, and refuses HTML, XML, SVG, and JavaScript even if you list them. Show `SavedFile.OriginalName` to people; never use it as a path. When you serve stored files, set the content type from the extension and send `X-Content-Type-Options: nosniff`.

### Secrets

- `SECRET_KEY` (32 bytes or more) signs `c.SetSignedCookie` and flash messages. Generate one with `openssl rand -base64 32`. Without it, production still starts, but signing calls fail with `ErrNoSecretKey`.
- To rotate, set the new key as `SECRET_KEY` and the old one in `SECRET_KEY_PREVIOUS` (comma separated). Old values still verify; new ones are signed with the new key.
- Printing a `Config` redacts both keys. A signed cookie is signed, not encrypted: never put a secret in one.
- Keep keys in the environment or a secret manager, not in the repository or the image. `DB_LOG_QUERIES` is off by default because statement arguments can be personal data; leave it off in production.

## Prove it {#next}

Each rule above has a test you can write in a few lines: a forged `X-Forwarded-For`, a POST without a token, an upload with the wrong type. [Testing](/docs/testing) shows how, and [Deploying](/docs/deploying) covers the proxy and production settings that make `ClientIP` and HSTS true in the real world.
