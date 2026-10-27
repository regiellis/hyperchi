---
title: Deploying
section: Production
order: 150
next: deployment-adapters
description: Ship a HyperChi app as one static binary in a small container, with production settings, health checks, graceful shutdown, and a reverse proxy that is declared instead of trusted by accident.
---

## One binary, no toolchain {#overview}

A HyperChi app builds to a single Go binary. Templates and static files can live inside it with `go:embed`, so the container needs no source tree and no working directory layout. SQLite is pure Go (`modernc.org/sqlite`), so every build uses `CGO_ENABLED=0` and runs on a bare Alpine image with no C toolchain. (Go's race detector is the one exception: `go test -race` needs cgo, but your release build does not.)

Production is a mode, not a build. Set `ENVIRONMENT=production` and the framework turns on the CSP, HSTS, generic error pages, gzip, and same-origin WebSockets, and turns off hot reload and debug output. Left unset, the app runs in development with relaxed WebSocket origins, open CORS, and detailed error pages. Every deployment sets it explicitly.

For a generated binary-and-assets artifact with Docker or systemd runtime files, use the [deployment adapters](/docs/deployment-adapters).

## An embedded app and its Dockerfile {#example}

`LoadTemplatesFS` and `StaticFS` follow the same rules as their disk versions, including `**` patterns and `{{asset "/static/..."}}` URLs with immutable caching.

```go
//go:embed templates static
var files embed.FS

func run() error {
    cfg := config.FromEnv() // ENVIRONMENT, HOST, PORT, DB_PATH, TRUSTED_PROXIES, SECRET_KEY
    app, err := hyperchi.NewWithConfigE(cfg)
    if err != nil {
        return err
    }
    defer func() {
        if err := app.Close(); err != nil { // idempotent; Serve has already called it
            log.Printf("close: %v", err)
        }
    }()

    app.AutoMiddleware()
    if err := app.LoadTemplatesFS(files, "templates/**/*.tmpl"); err != nil {
        return err
    }
    static, err := fs.Sub(files, "static")
    if err != nil {
        return err
    }
    if err := app.StaticFS("/static/*", static, hyperchi.StaticOptions{}); err != nil {
        return err
    }

    app.CreateHealthEndpoints() // /health, /health/ready, /health/live
    app.Get("/", func(c *hyperchi.Context) error {
        return c.View("index", hyperchi.H{"title": "Home"})
    })
    return app.Serve(cfg.Server.Address())
}
```

A multi-stage Dockerfile keeps the compiler out of the final image. Pin the Go image to the patch release you build and test with, never `latest`. This is the shape of this site's own Dockerfile:

```dockerfile
FROM golang:1.26.8-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/app .

FROM alpine:3.22
RUN apk add --no-cache ca-certificates wget && adduser -D -u 10001 app
WORKDIR /app
COPY --from=builder /out/app ./app
USER app
ENV HOST=0.0.0.0 PORT=8080 ENVIRONMENT=production
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
  CMD wget -q -O /dev/null http://127.0.0.1:8080/health/live || exit 1
ENTRYPOINT ["./app"]
```

The user is not root, and the binary is the entry point in exec form, so it is PID 1 and receives the stop signal directly. With a database, point `DB_PATH` at a mounted volume the user can write (the default is `hyperchi.db` in the working directory, which a read-only image cannot create). With no database, set `DB_DISABLED=true`. Never bake `SECRET_KEY` into the image: pass it at run time.

### Shutdown and health

`app.Serve(addr)` handles SIGINT and SIGTERM: it stops accepting connections, drains in-flight requests for `Server.ShutdownTimeout` (10 seconds by default), then runs `app.Close`, which stops watchers and closes integrations, the database, and the KV stores. `Close` is safe to call twice. If a parent runtime owns cancellation, use `app.ServeContext(ctx, addr)` instead. Keep the container's stop grace period a little longer than `ShutdownTimeout`, or the runtime will kill the process mid-drain.

The server sets a header timeout (5 s), a 32 KiB header cap, and 10-second read and write deadlines. An app that streams over SSE or WebSockets should raise `cfg.Server.WriteTimeout` on purpose. Production binds exactly the port you give it; automatic port fallback is a development feature behind `Server.PortFallback`.

`CreateHealthEndpoints` adds three routes. `/health/live` answers `Alive` while the process runs, which suits the container check. `/health/ready` returns 200 only when the database answers and templates are loaded, which suits a load balancer. `/health` returns JSON with status, uptime, and per-service checks; failure details go to the log, never to the response. A site with no database can serve its own `/healthz` with `c.Text("ok")`.

### Behind Traefik or Caddy

A proxy terminates TLS and forwards requests, so every connection reaches your app from the proxy's address. Two things follow. First, tell the app which peers are proxies, or `c.ClientIP()` will report the proxy for every visitor and rate limits will count them all as one:

```text
TRUSTED_PROXIES=172.18.0.0/16
```

Use the CIDR of the Docker network the proxy shares with the app (`docker network inspect` shows it). Second, make sure the proxy itself does not pass a client's forwarded headers through. Caddy's `reverse_proxy` sets or appends `X-Forwarded-For` and ignores incoming `X-Forwarded-*` values unless the client is in `trusted_proxies`. Traefik does not trust incoming forwarded headers unless the entrypoint lists `forwardedHeaders.trustedIPs` (or `insecure`, which is for tests only). Leave both at their defaults unless another proxy sits in front. Both examples below follow the current documentation:

```text
app.example.com {
    reverse_proxy app:8080
}
```

```yaml
services:
  app:
    image: registry.example.com/app:1.4.2
    environment:
      ENVIRONMENT: production
      TRUSTED_PROXIES: 172.18.0.0/16
    labels:
      - traefik.http.routers.app.rule=Host(`app.example.com`)
      - traefik.http.routers.app.entrypoints=websecure
      - traefik.http.routers.app.tls.certresolver=letsencrypt
      - traefik.http.services.app.loadbalancer.server.port=8080
```

For WebSockets, the origin check still applies behind a proxy, so list your public host with `SetOriginPatterns` if the browser's `Origin` differs from the `Host` the app sees.

## Production checklist {#next}

- `ENVIRONMENT=production` is set in the container, and `/health` reports `"environment":"production"`.
- `CGO_ENABLED=0`, pinned image tags, a non-root user, and a multi-stage build.
- `SECRET_KEY` is 32 bytes or more and comes from the environment or a secret manager.
- `TRUSTED_PROXIES` lists the proxy's network, and a request with a forged `X-Forwarded-For` shows the real peer in your logs.
- CSRF and rate limiting are installed on the routes that need them, and `CSP_POLICY` is as strict as your pages allow.
- `HSTS` is what you want for the domain; the default is one year for this host only.
- `DB_PATH` is on a persistent volume, or `DB_DISABLED=true`.
- The orchestrator's stop grace period exceeds `ShutdownTimeout`.
- Health checks point at `/health/live` and `/health/ready`.

Run through [Security](/docs/security) for the reasons behind the middleware items, and [Testing](/docs/testing) to keep them true after the next change.
