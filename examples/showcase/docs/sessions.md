---
title: Sessions and cookies
section: Built-ins
order: 80
next: assets
description: Keep state between requests with KV-backed sessions, signed cookies, and flash messages, and set the secret key that signs them.
---

## State between requests {#overview}

HyperChi gives you three tools for remembering something about a visitor, and they differ in where the data lives:

- **Sessions** keep the data on the server in a key-value store. The browser holds only a random session ID. Use them for a login, a cart, or a wizard's answers.
- **Signed cookies** keep a small value in the browser, with a signature that makes it tamper-evident. Use them for a player ID or a preference the server must be able to trust.
- **Flash messages** carry a one-time notice, such as "Saved.", to the next request. They ride in a signed cookie, or in the session when one exists.

Signed cookies and flash messages need an application secret. Sessions do not: the ID is 32 random bytes from `crypto/rand`, and everything else stays in the store.

## A session in one handler {#example}

A session needs a registered KV store, `app.UseSessions` to create the session store on it, and `app.SessionMiddleware()` to load and save the session around each request. Register the middleware before any route, because chi refuses `Use` after the first route.

```go
app := hyperchi.NewFromEnv()
app.AutoMiddleware()

if err := app.UseKVStoreMemoryAdvanced("sessions"); err != nil {
    return err
}
if err := app.UseSessions(hyperchi.KVSessionConfig{
    Backend:         "sessions",
    MaxAge:          2 * time.Hour,
    InsecureCookies: app.IsDevelopment(),
}); err != nil {
    return err
}
app.Use(app.SessionMiddleware())

app.Get("/visits", func(c *hyperchi.Context) error {
    s := c.Session()
    n := s.GetInt("visits") + 1
    s.Set("visits", n)
    return c.Text(fmt.Sprintf("Visit %d", n))
})
```

`c.Session()` returns the request's `*hyperchi.KVSession`, or nil outside the middleware. It has `Get`, `GetString`, `GetInt`, `GetBool`, `Set`, `Delete`, `Has`, and `Clear`. A value must survive a JSON round trip, because the session is stored as JSON. The middleware saves a changed session before the first byte of the response, so a handler never calls `Save` itself.

`KVSessionConfig` defaults to a `session_id` cookie, 24 hours, and the key prefix `session:`. The cookie is `HttpOnly`, `Path=/`, `SameSite=Strict`, and `Secure`. Secure is the default for a reason: a browser drops it on plain http. The showcase sets `InsecureCookies` for its loopback preview only. Set it for local development over http, and never in production.

`SessionMiddleware` creates a session and sets a cookie on every request it wraps. To keep the cookie off pages that do not need one, install it on a group instead of the whole app.

### Login and logout

Issue a fresh ID after a privilege change, so an attacker cannot plant a known ID before the visitor signs in. `Regenerate` keeps the data and replaces the ID. `Destroy` deletes the record and clears the cookie.

```go
func login(c *hyperchi.Context) error {
    if err := c.Session().Regenerate(); err != nil {
        return err
    }
    c.Session().Set("user", "ada")
    return c.Redirect("/account")
}

func logout(c *hyperchi.Context) error {
    if err := c.Session().Destroy(); err != nil {
        return err
    }
    return c.Redirect("/")
}
```

## Choosing a store {#stores}

A session store sits on any registered `KVStore`. Prefer the "Advanced" variants for sessions: they support per-key TTLs, so an expired session disappears from the store without a sweep.

| Registration | Survives a restart | Use it for |
| --- | --- | --- |
| `app.UseKVStoreMemoryAdvanced(name)` | No | Development, tests, and sites that can lose sessions on deploy |
| `app.UseKVStoreBBoltAdvanced(name, path, bucket)` | Yes | A single-process app that keeps sessions on disk |
| `app.AutoSetupKVStores()` | Memory: no. bbolt: yes | Registers `memory` and `bbolt` (at `data/hyperchi.db`) in one call |

```go
if err := app.UseKVStoreBBoltAdvanced("sessions", "data/sessions.db", "sessions"); err != nil {
    return err
}
if err := app.UseSessions(hyperchi.KVSessionConfig{Backend: "sessions"}); err != nil {
    return err
}
```

bbolt locks its file, so one process can open it. `app.Close` closes every registered store.

The same registry backs a cache. `app.UseKVCache` builds a `KVCache` on a named store, and `app.KVCache()` returns it with `Get`, `GetString`, `Set`, `SetTTL`, `SetWithTags`, and `InvalidateByTag`. This is separate from `app.Cache`, the in-process cache that `config.Cache` sizes.

```go
if err := app.UseKVStoreMemoryAdvanced("cache"); err != nil {
    return err
}
if err := app.UseKVCache(hyperchi.KVCacheConfig{Backend: "cache", DefaultTTL: 10 * time.Minute}); err != nil {
    return err
}
if err := app.KVCache().Set("report", "cached text"); err != nil {
    return err
}
```

## The secret key {#secret-key}

`SECRET_KEY` signs cookies and flash messages with HMAC-SHA256. It must be 32 bytes or longer: `config.Validate` rejects a shorter one, and `NewWithConfigE` fails. Generate one with `openssl rand -base64 32`.

- **Development without a key** signs with a random per-process key and logs that signed values will not survive a restart.
- **Production without a key** starts normally, and the app logs once on first use. Every signing and verifying call, including `c.Flash`, returns `hyperchi.ErrNoSecretKey`, which a handler that returns it turns into a 500. An app that signs nothing needs no key and sees no warning.

Rotate a key without logging anyone out. Move the old value to `SECRET_KEY_PREVIOUS` (comma-separated for several) and set a new `SECRET_KEY`. New values are signed with the current key only, and values signed by a previous key still verify. Remove the old key after the longest cookie lifetime has passed. Setting `SECRET_KEY_PREVIOUS` without `SECRET_KEY` is a validation error.

`hyperchi.NewProduction()` reads the environment, so it picks up `SECRET_KEY`. `config.Production()` on its own does not: it is a fixed set of production defaults, so a config built from it carries no key unless you set one.

## Signed cookies {#signed-cookies}

`c.SetSignedCookie` signs the value together with the cookie name and the time of signing. A copy moved to another cookie name fails to verify. `c.SignedCookie(name, maxAge)` checks the signature against the current and every previous key, and a `maxAge` above zero also rejects an older value.

```go
app.Get("/player", func(c *hyperchi.Context) error {
    id, err := c.SignedCookie("player", 30*24*time.Hour)
    if errors.Is(err, hyperchi.ErrNoSecretKey) {
        return err // misconfiguration, not a missing cookie
    }
    if err != nil {
        id = rand.Text() // missing, tampered, or too old: start over
        ck := &http.Cookie{Name: "player", Value: id, MaxAge: 30 * 24 * 60 * 60}
        if err := c.SetSignedCookie(ck); err != nil {
            return err
        }
    }
    return c.Text("player " + id)
})
```

The sent cookie gets `Path=/`, `SameSite=Lax` when unset, `HttpOnly`, and `Secure` in production. Failures wrap `ErrCookieMissing`, `ErrCookieInvalid`, `ErrCookieExpired`, or `ErrCookieTooLarge` (past 4096 bytes), so test them with `errors.Is`. The value is signed, not encrypted: it is readable base64url, so never put a secret in it. Set the cookie before the response body is written, or the call fails.

## Flash messages {#flash}

`c.Flash(kind, message)` stores a notice for the next request. The current response does not show it. That pairs with post, redirect, get: the POST saves and flashes, the redirect lands on a GET, and the GET renders the notice once.

```go
type NewNote struct {
    Title string `form:"title" validate:"required,max=80"`
}

var notes []string

app.Get("/notes", func(c *hyperchi.Context) error {
    return c.View("notes", hyperchi.H{"notes": notes})
})

app.Post("/notes", func(c *hyperchi.Context) error {
    in, err := hyperchi.Bind[NewNote](c)
    if err != nil {
        return err
    }
    notes = append(notes, in.Title)
    if err := c.Flash("success", "Saved "+in.Title+"."); err != nil {
        return err
    }
    return c.Redirect("/notes")
})
```

Templates rendered with `c.View`, `c.Page`, or `c.Fragment` receive the messages as `.flashes`. Reading them clears them, whether or not the template prints them.

```html
{{define "notes"}}
  {{range .flashes}}<p class="flash flash-{{.Kind}}" role="status">{{.Message}}</p>{{end}}
  <ul>{{range .notes}}<li>{{.}}</li>{{end}}</ul>
{{end}}
```

`Kind` is your own label, usually turned into a class. Messages are limited to 10 at a time (the oldest drops first), 32 bytes for a kind, and 1024 for a message. Without a session they travel in the signed `hyperchi_flash` cookie, which expires after an hour. With `SessionMiddleware` on the route they live in the session instead. Because the cookie is signed and not encrypted, keep secrets out of messages. `c.Redirect` answers an htmx request with `HX-Redirect`, so the same handler works for a boosted form.

## Where to go next {#next}

Sessions also hold the answers of a multi-step wizard, which the [forms page](/docs/forms) covers. The [security page](/docs/security) covers CSRF, rate limits, and client IPs, and [configuration](/docs/configuration) lists every environment variable, including `SECRET_KEY`.
