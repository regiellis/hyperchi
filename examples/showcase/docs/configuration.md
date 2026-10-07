---
title: Configuration
section: Production
order: 130
next: security
description: Build an app from defaults, the environment, or a config struct, and learn what changes between development and production.
---

## One config, several constructors {#overview}

Every app is built from a `*config.Config`. The constructors differ in where that config comes from:

| Constructor | Config | On a bad config |
| --- | --- | --- |
| `hyperchi.New()` | `config.Default()` | panics |
| `hyperchi.NewFromEnv()` | `config.FromEnv()` | panics |
| `hyperchi.NewProduction()` | `config.ProductionFromEnv()` | panics |
| `hyperchi.NewWithConfigE(cfg)` | the config you pass | returns an error |
| `hyperchi.NewWithConfig(cfg)` | the config you pass | panics |

The config package has four builders:

- `config.Default()` is development: `0.0.0.0:8080`, SQLite at `hyperchi.db`, hot reload and debug on, CSRF and CORS on, rate limiting off.
- `config.Production()` is `Default` with the environment set to `production`, minified assets, reloading and debug off, and log level `warn`. It reads no environment variables.
- `config.ProductionFromEnv()` is `FromEnv` with the environment forced to `production` and the same switches, so `SECRET_KEY`, `DB_PATH`, and the rest still apply.
- `config.FromEnv()` starts from `Default` and applies the environment variables below. A production environment then applies the same switches as `Production`.
- `config.Minimal()` is `FromEnv` with the database and cache disabled and reloading off, for a templates-and-assets site. Without a `CSP_POLICY` it sets `default-src 'self'; base-uri 'self'; frame-ancestors 'none'; form-action 'self'`.

`NewWithConfigE` validates the config first and works on a copy, so changing `cfg` later does not reach the running app. Prefer it wherever the process must not crash on a bad value. The other constructors panic with the same error.

`NewProduction` reads the environment like `NewFromEnv` and always runs in production mode, whatever `ENVIRONMENT` says.

## Build the app from the environment {#example}

This is the shape the scaffold uses. It reads the environment, validates it, builds the app, and serves the configured address.

```go
func run() error {
    cfg := config.FromEnv()
    cfg.Database.Disabled = true // set to false, and DB_PATH, when the app needs SQLite
    if err := cfg.Validate(); err != nil {
        return err
    }
    app, err := hyperchi.NewWithConfigE(cfg)
    if err != nil {
        return err
    }
    defer func() {
        if err := app.Close(); err != nil {
            log.Printf("close: %v", err)
        }
    }()

    app.AutoMiddleware()
    app.SetGlobal("siteName", "Example")
    app.Get("/", func(c *hyperchi.Context) error {
        return c.View("index", hyperchi.H{"title": "Home"})
    })
    return app.Serve(cfg.Server.Address())
}
```

`FromEnv` does not validate, so call `Validate` yourself, or let `NewWithConfigE` do it. `Validate` rejects an environment other than `development` or `production`, a negative limit, a database type other than `sqlite`, a trusted proxy that is not a CIDR prefix, a port outside 1 to 65535, an empty asset directory, and a secret key shorter than 32 bytes. An unset secret key is valid.

`app.Serve(addr)` binds exactly the address you give it; an empty address serves on `Config.Server`'s host and port, which `FromEnv` fills from `HOST` and `PORT`. `app.Run()` serves on the configured host and `$PORT`, or the configured port when `PORT` is unset.

`FromEnv` reads the process environment. It does not load `.env` files, so export the variables or have your shell, Compose file, or process manager load them first.

### Environment variables {#variables}

| Variable | Sets | Notes |
| --- | --- | --- |
| `ENVIRONMENT`, `GO_ENV` | `Server.Environment` | `development`, `dev`, or `production`. `ENVIRONMENT` wins. Any other value fails `Validate`. |
| `PRODUCTION` | `Server.Environment` | `true` means production, used only when neither variable above is set. |
| `HOST`, `PORT` | `Server.Host`, `Server.Port` | Defaults `0.0.0.0` and `8080`. An unparseable `PORT` is ignored. |
| `SECRET_KEY` | `Security.SecretKey` | 32 bytes or more. Signs cookies and flash messages. |
| `SECRET_KEY_PREVIOUS` | `Security.SecretKeyPrevious` | Comma-separated retired keys that still verify. Needs `SECRET_KEY`. |
| `TRUSTED_PROXIES` | `Security.TrustedProxies` | Comma-separated CIDR prefixes whose forwarding headers are believed. |
| `HSTS` | `Security.HSTS` | The `Strict-Transport-Security` value. Set to empty to send none. |
| `CORS_ORIGINS` | `Security.CORSOrigins` | One origin or `*`, sent verbatim as `Access-Control-Allow-Origin`. |
| `CSP_POLICY` | `Security.CSPPolicy` | A `Content-Security-Policy` value. Empty uses the framework default. |
| `DB_DISABLED` | `Database.Disabled` | `true` skips the database. |
| `DB_TYPE` | `Database.Type` | Only `sqlite` is supported. |
| `DB_PATH`, `DB_NAME` | `Database.Name` | The SQLite file. `DB_PATH` wins, and `DB_NAME` is read only when it is unset. |
| `DB_LOG_QUERIES` | `Database.LogQueries` | `true` logs every statement with its arguments. Off by default. |
| `ASSETS_DIR`, `STATIC_DIR` | `Assets.SourceDir`, `Assets.OutputDir` | Defaults `assets` and `static`. |
| `HOT_RELOAD` | `Assets.HotReload` | Forced off in production. |
| `DEBUG` | `Development.Debug` | Forced off in production. |
| `LOG_LEVEL` | `Development.LogLevel` | Production changes the default `info` to `warn`, and keeps any other value. |

## Development and production {#environments}

An unset environment means development. Set `ENVIRONMENT=production` in every deployment: the app does not guess from the host or the port. Development turns on conveniences that are wrong on a public server:

| Behavior | Development | Production |
| --- | --- | --- |
| Error pages | Detailed 500 pages with source and stack | A generic page, with the error logged |
| WebSocket origins | Relaxed, so a remote browser through Vite or SSH connects | Same-origin unless you call `SetOriginPatterns` |
| CORS | `*` when `CORS_ORIGINS` is empty | No CORS headers when empty |
| Security headers | `nosniff`, frame, and referrer headers | Those, plus CSP, `Permissions-Policy`, and HSTS |
| Compression | Off in `AutoMiddleware` | On |
| Cookies | Signed cookies and flashes without `SECRET_KEY` use a random per-process key | `Secure` flag set. Without `SECRET_KEY`, signing fails with `ErrNoSecretKey` |
| Static files and assets | `app.Static` sends `no-cache`, and asset versions are rechecked on every call | Hashed once, and a versioned URL is cached for a year |
| `/_debug` endpoints | Served | 404 |
| Content collections | `LoadDir` watches the folder and reloads | Loads once |
| Port in use | With `Server.PortFallback`, takes the next free port | Fails |

`app.IsDevelopment()` and `app.IsProduction()` report the mode, and `config.IsDevelopment()` does the same on a config. To switch a built app, `app.Production()` sets the environment, turns off debug and hot reload, and installs the security and recovery middleware, so call it before registering routes.

Behind a reverse proxy, list the proxy's CIDRs in `TRUSTED_PROXIES`. Without them `c.ClientIP()` reports the TCP peer.

## Skip the database {#database}

The database is SQLite through a pure-Go driver, opened when the app is built. A site that serves templates and assets needs none. Set `DB_DISABLED=true`, or set `cfg.Database.Disabled` in code, and `app.DB` is nil. Use `config.Minimal()` to disable the database and the cache together:

```go
site := hyperchi.NewWithConfig(config.Minimal())
```

Guard any handler that uses `app.DB` in an app that may run without one, and note that a disabled database also skips its health check.

## Values for every template {#globals}

`app.SetGlobal(key, value)` makes a value available to every template rendered through a `Context`, so a handler does not repeat a site name or an asset server. Set globals once during startup, before `Serve`. Handler data wins over a global with the same key, and a global wins over a query parameter with the same key.

```go
app.SetGlobal("development", cfg.IsDevelopment())
app.SetGlobal("siteName", "Example")
```

The scaffold sets `development` and `vite_dev_server` this way, as the [assets page](/docs/assets) shows.

## Next steps {#next}

Move on to the [security page](/docs/security) for CSRF, rate limits, and the headers listed above, and to the [deploying page](/docs/deploying) for the production checklist. The [sessions page](/docs/sessions) explains `SECRET_KEY` and key rotation in full.
