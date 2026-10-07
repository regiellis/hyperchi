# Performance module

An optional module that adds three things to a HyperChi app. The package name
is `performance`.

- A [BadgerDB](https://github.com/dgraph-io/badger) key-value store that
  implements the framework's `hyperchi.AdvancedKVStore`, so sessions and the
  cache can keep their data in it, with TTLs, backups, and restores.
- A rate limiter over any KV store: fixed window, sliding window, or token
  bucket, keyed by path, method, client IP, or user.
- A job queue stored in KV, with priorities, scheduled starts, and retries
  that wait longer after each failure.

Import `github.com/regiellis/hyperchi/modules/performance`. It is its own Go
module, so Badger stays out of apps that do not use it. It is built against
Badger v4.9.6, the latest on the Go module proxy (checked 2026-10-05), and
needs Go 1.26.0 or later.

## Install it into an app

```go
app := hyperchi.New()

config := performance.DefaultConfig()
config.Dir = "./data/performance"
config.RateLimitRules = []performance.RateLimitRule{
    {Path: "/login", Method: http.MethodPost, Rate: "5/minute", PerIP: true},
    {Path: "/api/*", Rate: "100/minute", PerUser: true},
}
perf := performance.New(config)
// Install adds the rate limit middleware with app.Use, so it runs
// before any route is registered.
if err := perf.Install(app); err != nil {
    log.Fatal(err)
}

perf.JobQueue().RegisterHandler("welcome-email", func(job *performance.Job) error {
    address, ok := job.Payload["email"].(string)
    if !ok {
        return fmt.Errorf("job %s: payload has no email", job.ID)
    }
    log.Printf("sending the welcome email to %s", address)
    return nil
})

app.Post("/signup", func(c *hyperchi.Context) error {
    payload := map[string]any{"email": c.FormValue("email")}
    if _, err := perf.JobQueue().Push("welcome-email", payload); err != nil {
        return err
    }
    return c.Text("Check your inbox")
})

if err := app.Serve("0.0.0.0:8080"); err != nil {
    log.Fatal(err)
}
```

`Install` does the following, in order:

1. Validates the rate limit rules, so a bad `Rate` fails at startup.
2. Opens the Badger store in `Dir` and registers it in the app's KV registry
   as `"badger"`.
3. Builds the rate limiter and the job queue that the config enables.
4. Registers the module with the app's integration manager as
   `"performance"` (`performance.IntegrationName`).
5. Adds the rate limit middleware with `app.Use` when there are rules.
   Install client IP and authentication middleware before calling `Install`:
   `PerIP` rules read the address `app.Middleware.ClientIP()` declared (or the
   TCP peer), and `PerUser` rules read the user ID that `security.RequireAuth`
   or `security.WithUserID` stored.

The app owns the lifecycle from there. `app.Serve` starts the integrations,
which starts the job workers. On shutdown, `app.Close` stops the workers,
waiting for running jobs until the app's ten-second close limit, and then
closes the store. Without `Serve`, call `app.Integrations().Start(ctx)` and
`app.Close()` yourself.

A refused request goes through the app's error pipeline, like every
framework denial: `app.OnError` when set, or `DefaultError`'s JSON, htmx
fragment, or error page. The error is a `*hyperchi.HTTPError` with code 429,
and the response keeps `Retry-After` (whole seconds, rounded up) and the
`X-RateLimit-Limit`, `X-RateLimit-Remaining`, and `X-RateLimit-Reset`
headers. A store failure during the check answers 500 the same way.

## Configuration

| Field | `DefaultConfig()` | Meaning |
| --- | --- | --- |
| `Dir` | `./data/performance` | Badger's data directory. `New` uses this when it is empty. |
| `SyncWrites` | `false` | fsync every write. Slower; survives a power loss. |
| `Workers` | `4` | Badger compaction goroutines. 0 uses 4. Badger needs at least 2, so `Install` rejects 1 and negative values. |
| `BadgerLogger` | nil | The `*slog.Logger` Badger's log lines go to. nil uses `slog.Default()`. |
| `BadgerLogLevel` | nil | The lowest level passed on, as a `slog.Leveler`. nil means `slog.LevelWarn`, which drops Badger's routine INFO lines. |
| `EnableRateLimit` | `true` | Build the rate limiter. |
| `RateLimitRules` | none | Rules `Install` applies to every request. With none, nothing is limited. |
| `RateLimitAlgorithm` | `FixedWindow` | `FixedWindow`, `SlidingWindow`, or `TokenBucket`. |
| `EnableJobQueue` | `true` | Build the job queue. |
| `JobWorkers` | `8` | Worker goroutines. |
| `RetryLimit` | `3` | Attempts per job unless `JobOptions.MaxAttempts` says otherwise. |

`performance.New(performance.Config{...})` starts from zero values, so the
limiter and queue are off until enabled.

A `RateLimitRule` matches on `Path` (exact, `"*"`, or a prefix ending in `*`)
and `Method` (empty matches all). The first matching rule counts the request.
`Rate` is `"requests/period"`: the period is `second`, `minute`, `hour`,
`day`, or any Go duration such as `30s`. `PerIP` and `PerUser` give each
client or user its own counter; a `PerUser` rule counts anonymous requests
per client IP. `Burst` sets a token bucket's capacity.

`perf.RateLimiter()` and `perf.JobQueue()` return what `Install` built, or nil
when it is disabled.

## Sessions and cache on Badger

```go
app := hyperchi.New()

perf := performance.New(performance.Config{Dir: "./data/performance"})
if err := perf.Install(app); err != nil {
    log.Fatal(err)
}

// Install registered the store in the app's KV registry as "badger".
if err := app.UseSessions(hyperchi.KVSessionConfig{Backend: "badger", MaxAge: 7 * 24 * time.Hour}); err != nil {
    log.Fatal(err)
}
if err := app.UseKVCache(hyperchi.KVCacheConfig{Backend: "badger", DefaultTTL: 10 * time.Minute}); err != nil {
    log.Fatal(err)
}
app.Use(app.SessionMiddleware())

app.Get("/", func(c *hyperchi.Context) error {
    session := c.Session()
    session.Set("visits", session.GetInt("visits")+1)
    if err := session.Save(); err != nil {
        return err
    }
    return c.Text(fmt.Sprintf("visit %d", session.GetInt("visits")))
})

if err := app.Serve("0.0.0.0:8080"); err != nil {
    log.Fatal(err)
}
```

`BadgerAdvancedStore` implements `hyperchi.KVStore`, `AdvancedKVStore`,
`AtomicTTLStore`, and `AtomicTakeStore`, and this package's `TTLStore`.
`performance.KVOperation` is the framework's `hyperchi.KVOperation`, and a
`Batch` applies all of its steps or none. Counters are stored as JSON numbers, so `hyperchi.KVHelper.GetInt` reads
them. `GetTTL` returns `hyperchi.ErrKeyNotFound` for a key with no expiry,
as the framework's memory and bbolt stores do.

Back up the whole store to one file, and restore it into a store that may
already hold data. Keys in the backup overwrite keys already there, and other
keys are kept:

```go
store, err := performance.NewBadgerAdvancedStore(performance.DefaultBadgerConfig("./data/performance"))
if err != nil {
    log.Fatal(err)
}
if err := store.Backup("./backups/performance.bak"); err != nil {
    log.Fatal(err)
}
if err := store.Close(); err != nil {
    log.Fatal(err)
}

restored, err := performance.NewBadgerAdvancedStore(performance.DefaultBadgerConfig("./data/restored"))
if err != nil {
    log.Fatal(err)
}
if err := restored.Restore("./backups/performance.bak"); err != nil {
    log.Fatal(err)
}
if err := restored.Close(); err != nil {
    log.Fatal(err)
}
```

`Backup` writes a temporary file and renames it into place, so a failed
backup leaves the previous one intact. Inside an app, reach the installed
store with `perf.GetStore("badger").(*performance.BadgerAdvancedStore)`.

## Job queue

`Push` queues a job to run as soon as a worker is free. `PushWithOptions`
sets a priority, a start time, and the number of attempts:

```go
registry := hyperchi.NewKVRegistry()
if err := registry.Register("memory", hyperchi.NewMemoryAdvancedStore()); err != nil {
    log.Fatal(err)
}
queue, err := performance.NewJobQueue(registry, performance.JobQueueConfig{Backend: "memory"})
if err != nil {
    log.Fatal(err)
}

tonight := time.Now().Add(6 * time.Hour)
job, err := queue.PushWithOptions("report", map[string]any{"month": "2026-09"}, performance.JobOptions{
    Priority:    10,
    ScheduleAt:  &tonight,
    MaxAttempts: 5,
})
if err != nil {
    log.Fatal(err)
}
fmt.Println(job.Status, job.MaxAttempts) // pending 5
```

Among jobs that are due, the highest `Priority` runs first, then the earliest
`ScheduledAt`. A generated job ID is the type, a dash, and 26 random
characters. A failed attempt is retried after `RetryDelay` (one minute by
default), doubled for each later attempt up to `MaxRetryDelay` (one hour).
`GetJob`, `GetJobResult`, and `GetStats` read the stored state.

The payload is stored as JSON, so a handler sees what JSON decodes: numbers
arrive as `float64` and structs as `map[string]any`.

## Rate limiter without an app

A limiter can run on any registered KV store. Without an app it answers a
refusal with plain text.

```go
registry := hyperchi.NewKVRegistry()
if err := registry.Register("memory", hyperchi.NewMemoryAdvancedStore()); err != nil {
    log.Fatal(err)
}
limiter, err := performance.NewRateLimiter(registry, performance.RateLimitConfig{Backend: "memory"})
if err != nil {
    log.Fatal(err)
}
rules, err := performance.PrepareRateLimitRules([]performance.RateLimitRule{
    {Path: "*", Rate: "60/minute", PerIP: true},
})
if err != nil {
    log.Fatal(err)
}

mux := http.NewServeMux()
mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
    if _, err := w.Write([]byte("ok")); err != nil {
        log.Print(err)
    }
})
log.Fatal(http.ListenAndServe("0.0.0.0:8080", limiter.RateLimitMiddleware(rules)(mux)))
```

Set `RateLimitConfig.App` to send refusals through an app's error pipeline
instead; `Install` does this for you. `limiter.Check(rule, identifier)` counts
one request directly and reports whether it is allowed.

## Limits

What is persisted. Everything lives in `Dir`: job records, pending and
retrying jobs, job results, rate limit counters, and token buckets. After a
restart, pending and retrying jobs run again and counters keep counting until
their window ends. Job results expire after 24 hours. Completed and failed job
records stay until you delete them, and `GetStats` reads every one, so its
cost grows with the history. With `SyncWrites` off, a power loss or kernel
crash can drop the most recent writes; a process crash does not.

One process per directory. Badger locks `Dir`, so a second process cannot
open it. Rate limits and queues are per instance: three app instances behind
a load balancer each allow the full rate and run their own queue. This is not
a distributed rate limiter, and there is no Redis or Memcached adapter.

Job delivery is at least once while the process runs, and not across a crash.

- A handler that returns an error runs again, up to `MaxAttempts`, so a job
  can run more than once. Write handlers that are safe to repeat.
- A job whose process dies while its handler runs is not retried. Claiming a
  job removes it from the pending queue, and its record stays `processing`.
  There is no crash recovery and no exactly-once processing.
- Workers poll once a second, so a job starts up to a second after it is due.
  Handlers get no context or timeout; a handler still running at shutdown
  holds the close until the app's close limit, then the store closes under it.
- Passing a `JobOptions.ID` that is already stored returns `ErrJobExists`.
  The check covers pushes through one `JobQueue`; two queues sharing a
  backend can still store the same ID.

Rate limiting. The sliding window counts in one-minute buckets, so it is
approximate below a minute. A token bucket's update is guarded by its
limiter's lock, so use one limiter per key prefix and backend. Badger and the
framework's advanced memory and bbolt stores expire counters themselves; on a
store that is not a `performance.TTLStore`, call `limiter.CleanupExpired()` now and then, and it
deletes each counter once its own window has ended.

## Tests

Run `go test -race ./...` in this directory. Examples in `example_test.go`
mirror the code above and are compiled by every test run.
