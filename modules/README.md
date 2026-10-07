# Optional HyperChi modules

Modules have their own `go.mod`, keeping their dependencies out of the core
framework. The showcase uses local replacements so it builds with the adapters
in this checkout. Publish each module under its own module-path version tag
before consumers can fetch the new version.

| Module | Purpose | API |
| --- | --- | --- |
| [templ](templ/README.md) | Generated typed HTML components | `templadapter.New()`, then `c.RenderWith` |
| [Jet](jet/README.md) | Preloaded runtime templates | `jetadapter.New(set, names...)`, then `c.RenderWith` |
| [gomponents](gomponents/README.md) | HTML components written in Go, with typed htmx and `hc-*` attributes | `gomponentsadapter.New()`, then `c.RenderWith` |
| [Markdown](markdown/README.md) | goldmark with GFM, heading IDs, and front matter; raw, HTML, and plain-text output | `markdown.New()`, then `Parse` or `c.RenderWith` |
| [PocketBase](pocketbase/README.md) | HTTP record CRUD, pagination, and authentication | Existing `Module.Init(app)` and typed client |
| [Performance](performance/README.md) | Badger storage and backups, job queue, and rate limiting | `Module.Install(app)`, which registers it as the `performance` integration |

## Managed integrations

The renderer modules use the core integration manager:

```go
helpers.Must(app.Integrations().Register("views", templadapter.New()))
app.Get("/", func(c *hyperchi.Context) error {
    return c.RenderWith("views", HomePage(data))
})
helpers.Must(app.Serve("127.0.0.1:8080"))
```

`app.Serve` starts registered capabilities and closes them after requests drain.
Use `integrations.Hooks` to adapt an existing service lifecycle without replacing
its native typed API. `app.Extensions()` continues to manage the browser-side
htmx extensions. General request, collection, and response helpers are core APIs.

PocketBase keeps its own `Module.Init(app)` and is not registered with the
manager: it holds no resources to close. Performance's `Module.Install(app)`
registers the module with the manager itself. The job queue's workers start
with the app's integrations, and `app.Close` stops them before closing the
Badger store. When rate limit rules are configured, `Install` adds their
middleware with `app.Use`, so call it before registering routes. Avoid
registering the same resource with two cleanup owners. PocketBase realtime
subscriptions and file uploads are not implemented; reads are retried when
`RetryAttempts` is set. There is no shipped Redis/Memcached adapter here.

Queue workers using built-in atomic stores can claim pending jobs atomically.
Among due jobs, higher `Priority` runs first, and failed jobs retry with a
doubling delay. That does not provide crash recovery or exactly-once processing. See the module
source and tests before configuring production queue or rate-limit policies.

The runnable site at `/docs/integration-manager` documents registration, lifecycle,
health checks, function adapters, and the existing `HTMLRenderer` bridge.
