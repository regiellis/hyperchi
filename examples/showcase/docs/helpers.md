---
title: Handlers & helpers
section: Reference
order: 220
next: filters
description: Build with HyperChi’s handler, rendering, response, and collection APIs. Keep standard HTTP access available for the parts that actually need it.
---

## Choose the handler for the job {#overview}

- `app.Get`, `app.Post`, `app.Put`, `app.Patch`, and `app.Delete` take a `func(c *hyperchi.Context) error`. They register the HTTP method and pass returned errors to the application error handler. Optional middleware for that route follows the handler.
- The handler chooses the response. `c.View` renders the full page, or only the template for an htmx partial request. `c.Page` always renders the page and `c.Fragment` never adds a layout.
- `app.Handle(method, pattern, handler)` and `app.Mount` integrate existing `net/http` handlers or routers. They are interoperability APIs, not necessary boilerplate for a new endpoint.

The runnable [integration examples](/docs/integrations), the main showcase, and these documentation pages all use context handlers.

## A complete context handler {#example}

With the core `hyperchi` package imported and `app` initialized:

```go
app.Get("/hello/{name}", func(c *hyperchi.Context) error {
    greeting := c.QueryDefault("greeting", "Hello")
    return c.JSON(hyperchi.H{
        "message": greeting + ", " + c.Param("name"),
    })
})
```

A request to `/hello/Morgan?greeting=Welcome` returns a JSON object containing `"message": "Welcome, Morgan"`. You do not need to set a JSON content type or construct an encoder.

### Read inputs without repeating request plumbing

```go
name := c.Param("name")
query := c.Query("q")
terrain := c.QueryDefault("terrain", "all")
tags := c.Queries("tag")
isHTMX := c.IsHTMX()
```

These helpers read inputs; they do not validate or authorize them. Keep allowlists, schemas, and permission checks explicit. `c.Ctx()` returns the request context for cancellation, and `c.Request()` remains available for HTTP features without a framework helper.

To decode and check a whole form or JSON body, describe it as a struct and call `hyperchi.Bind`:

```go
type Signup struct {
    Email string `form:"email" validate:"required,email"`
}

in, err := hyperchi.Bind[Signup](c)
if err != nil {
    return err // 400 for an unreadable body, 422 when a check fails
}
```

### Render through HyperChi

```go
helpers.Must(app.LoadTemplates("templates/*.tmpl"))

app.Get("/trips", func(c *hyperchi.Context) error {
    return c.View("trips.tmpl", catalog)
})
app.Get("/trip-cards", func(c *hyperchi.Context) error {
    return c.Fragment("trip-cards.tmpl", catalog)
})
```

For a typed slice, the template reads the data through `.Result`:

```html
{{range .Result}}
  <article><h2>{{.Name}}</h2></article>
{{end}}
```

For top-level template keys, pass `hyperchi.H`, `helpers.JSON`, or `map[string]any`. All three merge keys into the template context, so `hyperchi.H{"Title": "Trips"}` is available as `.Title`. Strings still receive HTML escaping. A slice stays under `.Result`; a struct is under `.Result` too, and its exported fields are also top-level keys, so `c.Fragment("trip-card", trip)` renders a template that reads `{{.Name}}`. `c.Layout("admin").Page(...)` selects an explicit layout. Values set once at startup with `app.SetGlobal`, such as a site name, reach every template; handler data wins over a global, and a global wins over a query parameter. These helpers supply the registered [template filters](/docs/filters) and request context.

### Transform typed collections

Import `github.com/regiellis/hyperchi/hyperchi/helpers` for generic collection operations. Go’s method rules mean the typed helpers are package functions: `helpers.FromSlice` wraps an existing slice, and `helpers.NewSlice` builds one from values.

```go
trips := helpers.FromSlice(catalog).Filter(func(trip Trip) bool {
    return trip.Terrain == "coast"
}).OrEmpty()
count := trips.Len()
first, found := trips.First()
```

`Filter` produces a new result and preserves the element type. Chain `OrEmpty()` when JSON must contain `[]` instead of `null`. It normalizes a nil result without copying a non-nil slice. Use `helpers.Map` when the output type differs, and `helpers.Reduce` to aggregate values. See the [trip finder recipe](/docs/data) for these helpers in an actual handler.

### Build a response with status or htmx headers

Chain `c.Status` before a response method to change the status, and use `c.HX()` to set htmx response headers:

```go
return c.Status(http.StatusBadRequest).
    JSON(hyperchi.H{"error": "Unknown terrain"})
```

```go
c.HX().Retarget("#result").Reswap("innerHTML")
return c.HTML("<p>Ready.</p>")
```

The status example also imports `net/http` for its named constant. `c.HX()` also supports `Trigger`, `PushURL`, `Location`, and `Refresh`, and a chain can end in a response method: `View`, `Fragment`, `HTML`, `JSON`, or `Empty`. `c.Redirect` sends `HX-Redirect` to htmx and a 303 to any other client. Writing literal HTML is fine for application-owned markup; render visitor values through an escaping template.

### Return errors at request time

Use `helpers.Must` for startup requirements such as loading templates. Inside a handler, return `hyperchi.NotFound`, `hyperchi.BadRequest`, or `hyperchi.NewHTTPError` to answer with that status. HyperChi renders it for the client: JSON for `Accept: application/json`, an error fragment for htmx, and an error page for a browser. Any other error is a 500 whose text stays in the log. An endpoint that promises JSON to every client sets the status itself, as in the JSON 400 above.

```go
trip, ok := trips[c.Param("id")]
if !ok {
    return hyperchi.NotFound("No such trip")
}
return c.View("trip", trip)
```

`c.JSON` and the template methods finish rendering before committing the status or body. Encoding and template errors can therefore reach the application error handler. A network write can still fail after committing the response; do not attempt a second response then. External renderers also need buffering. The templ and Jet integrations supply that boundary. Register them once and use `c.RenderWith`; the manager handles buffering and HTML response headers.

`c.RenderWith` works in any handler, so a page and its fragment can share one adapter. The integration pages deliberately use explicit HTML and JSON endpoints, so each handler keeps its contract direct.

### Confirm a post with a flash message {#flash}

After a form post, redirect, and carry the confirmation to the next page with `c.Flash`:

```go
app.Post("/items", func(c *hyperchi.Context) error {
    in, err := hyperchi.Bind[NewItem](c)
    if err != nil {
        return err
    }
    if err := store.Add(c.Ctx(), in.Name); err != nil {
        return err
    }
    if err := c.Flash("success", "Item added."); err != nil {
        return err
    }
    return c.Redirect("/items")
})
```

The page the redirect lands on receives the messages as `.flashes` when it renders with `c.View`, `c.Page`, or `c.Fragment`:

```html
{{range .flashes}}<p class="flash flash-{{.Kind}}" role="status">{{.Message}}</p>{{end}}
```

Rendering reads the messages once, so a reload does not show them again. `c.Flashes()` reads them in Go. The messages travel in a signed cookie, or in the session when the route runs behind `app.SessionMiddleware()`. At most ten are kept, each up to 1024 bytes. The kind is your own label, usually turned into a class. This request's own render does not show a flash; pass the message in the template data for that.

### Sign a cookie {#signed-cookies}

`c.SetSignedCookie` signs a value so the browser can hold it but not change it, and `c.SignedCookie` checks the signature and the age:

```go
err := c.SetSignedCookie(&http.Cookie{Name: "theme", Value: "dark", MaxAge: 365 * 24 * 60 * 60})
if err != nil {
    return err
}

theme, err := c.SignedCookie("theme", 365*24*time.Hour)
if err != nil {
    theme = "light" // missing, tampered with, or older than a year
}
```

The signature (HMAC-SHA256) covers the cookie name, the value, and when it was signed, so a value cannot be edited, moved to another cookie, or replayed after `maxAge`. Test the error with `errors.Is` against `hyperchi.ErrCookieMissing`, `ErrCookieInvalid`, or `ErrCookieExpired`. The value is signed, not encrypted: anyone holding the cookie can read it, so never put a secret in it. The cookie is sent `HttpOnly`, `SameSite=Lax` unless you set another mode, and `Secure` in production.

Both signed cookies and flash messages use the application secret key. Set `SECRET_KEY` (or `config.Security.SecretKey`) to at least 32 bytes in production, for example the output of `openssl rand -base64 32`; without it they fail with `hyperchi.ErrNoSecretKey`. Development signs with a random key, so signed values do not survive a restart. To rotate the key, move the old one to `SECRET_KEY_PREVIOUS` (comma-separated): values it signed keep verifying, and new ones use the new key.

### Split a long form into steps {#wizards}

`MultiStepForm` serves a form over several pages and keeps the answers in the visitor's session until the last step. It needs sessions, and it needs them before the routes are registered:

```go
helpers.Must(app.UseKVStoreMemoryAdvanced("sessions"))
helpers.Must(app.UseSessions(hyperchi.KVSessionConfig{Backend: "sessions"}))
app.Use(app.SessionMiddleware())

pm := app.Extensions().GetPatternManager()
pm.MultiStepForm("/signup", patterns.MultiStepFormConfig{
    SuccessURL: "/signup/done",
    Steps: []patterns.FormStep{
        {
            Name: "account", Title: "Your account", Template: "signup-account",
            Fields: []string{"email"},
            Validate: patterns.SchemaStep(validator.NewSchema().
                Field("email", validator.String().Required().Email())),
        },
        {Name: "plan", Title: "Choose a plan", Template: "signup-plan", Fields: []string{"plan"}},
    },
    Complete: func(r *http.Request, answers url.Values) error {
        return accounts.Create(r.Context(), answers.Get("email"), answers.Get("plan"))
    },
})
```

This also imports `net/http`, `net/url`, and the `patterns` and `validator` packages, and assumes your `accounts` store. Each step's template posts its number in a `step` field and reads `.values` (the answers so far) and `.errors` (messages per field):

```html
{{define "signup-account"}}
<form hx-post="/signup" hx-target="this" hx-swap="outerHTML">
  <input type="hidden" name="step" value="{{.step}}">
  <label>Email <input name="email" type="email" value="{{.values.Get "email"}}"></label>
  {{range index .errors "email"}}<p class="field-error">{{.}}</p>{{end}}
  <button>Continue</button>
</form>
{{end}}
```

- A `GET /signup?step=2` renders that step with the stored answers, so going back shows what the visitor entered. A step cannot be reached before the ones ahead of it are answered.
- A post keeps only the step's `Fields`, then runs its `Validate`. A rejected step renders again with status 422, its messages, and the submitted values, and nothing is stored. An accepted step is stored, and the response is the next step, with its URL pushed.
- After the last step, `Complete` receives every answer, the stored answers are cleared, and the visitor is redirected to `SuccessURL`. If `Complete` returns an error, the answers are kept for another try.
- `patterns.SchemaStep` adapts a `validator.Schema`; any `func(url.Values) map[string][]string` works as `Validate`.
- Without session middleware every wizard request fails with a 500 whose cause is `patterns.ErrNoWizardStore`.

### Store an upload {#uploads}

Validate an upload, then store it with `SaveAll`, which names each file itself:

```go
uploads := security.NewUploadValidator(security.DefaultUploadConfig())

app.Post("/photos", func(c *hyperchi.Context) error {
    result := uploads.ValidateUpload(c.Request())
    if !result.Valid {
        return hyperchi.Unprocessable("That file was not accepted.", map[string][]string{"file": result.Errors})
    }
    saved, err := result.SaveAll("data/uploads", security.SaveOptions{})
    if err != nil {
        return err
    }
    return c.View("photo-saved", hyperchi.H{"files": saved})
})
```

A stored file's name is 128 random bits plus an extension chosen from the type sniffed from its content, never from the visitor's file name or declared type. Files are written through an `os.Root`, so nothing lands outside the folder, and an existing file is never overwritten. `SaveAll` stores every file or none. HTML, SVG, XML, and JavaScript are refused even when listed. `security.DefaultSaveExtensions()` returns the default type-to-extension map; copy it and add a type to store more. For one file, `security.SaveUpload(dir, fileHeader, opts)` does the same. Show `SavedFile.OriginalName` to the visitor, but never use it as a path.

`DefaultUploadConfig` sets `RequireAuth`, which rejects an upload unless the request context carries a verified user (`security.WithUserID`). Serve stored files with their content type and `X-Content-Type-Options: nosniff`.

### Call another service with request cancellation

Configure one HTTP client at startup. The `With*` configuration methods mutate it, so finish configuration before concurrent requests. Inside handlers, use `GetWithContext`, `PostWithContext`, `PutWithContext`, `PatchWithContext`, `DeleteWithContext`, or `PostFormWithContext` to carry cancellation and deadlines.

```go
client := helpers.NewHTTPClient().
    WithBaseURL("https://api.example.com").
    WithTimeout(5 * time.Second).
    WithJSONHeaders()

app.Get("/inventory", func(c *hyperchi.Context) error {
    res, err := client.GetWithContext(c.Ctx(), "/items")
    if err != nil { return err }
    defer res.Close()
    if !res.IsSuccess() {
        return fmt.Errorf("inventory service returned %d", res.StatusCode())
    }
    var items []Item
    if err := res.Parse(&items); err != nil { return err }
    return c.JSON(helpers.FromSlice(items).OrEmpty())
})
```

This recipe also imports `fmt` and `time` and assumes your `Item` model. HTTP 4xx and 5xx responses do not produce a transport error: check the status. `Parse`, `Text`, and `JSON` consume and close the body; defer `Close` to cover early returns. The shorter HTTP methods use a background context and the configured client timeout (30 seconds by default). Strings are sent raw; other non-nil request bodies are JSON encoded. Form requests set their own content type.

### JSON objects and reliable defaults

```go
options := helpers.NewJSON().
    Set("limit", "24").
    Set("enabled", false)
limit := options.GetIntOr("limit", 12)
enabled := options.GetBoolOr("enabled", true)
```

Defaults apply to absent or invalid values; valid zero and false values are preserved. Integer access accepts ints, float64 values (truncated), and integer strings. `GetJSON` accepts both plain maps and nested `helpers.JSON` values. `FromJSON("null")` returns an empty object. Retain the returned map when calling `Set` or `Merge` on a possibly nil object. Prefer typed structs for stable service contracts.

### Choose intentional error handling

```go
value, err := strconv.Atoi(configuredPort)
port := helpers.Default(value, err, 8080)
```

`Default` takes three arguments: value, error, and fallback. Use it only when a fallback is acceptable. Return errors from request handlers, and reserve `helpers.Must` for startup requirements. String JSON conveniences such as `helpers.ToJSON` and `JSON.ToString` return `{}` on encoding errors; use `c.JSON` or `json.Marshal` when errors must be reported.

### Use the other helpers where they fit

- `app.Helpers.Env.GetOr("PORT", "8080")` reads configuration with a fallback.
- `helpers.S(value).Trim().Lower().String()` composes string transformations.
- `app.Helpers.Logger.Info`, `Warn`, and `Error` write to the application’s logger.
- `helpers.NewJSON().Set("status", "ok")` builds a JSON object; `hyperchi.H` is convenient for a small response literal.

Typed models, `context.Context`, embedded assets, and standard HTTP middleware remain useful Go primitives. The goal is to avoid reimplementing conveniences that HyperChi already supplies, while keeping application logic clear.

## Every helper {#reference}

The complete list, grouped by what it is for: the request and response helpers on `Context`, input binding and errors from the core package, then the `helpers` package. Each entry shows its signature and what it does; open a group to browse it, or search the docs for a name.

<!-- slot:helpers-reference -->

## Follow the working examples {#next}

Start with [Passing data](/docs/data) for `app.Get`, query defaults, typed filtering, and JSON responses. Compare [templ](/docs/templ) and [Jet](/docs/jet) for the external rendering boundary. The [helpers package](https://github.com/regiellis/hyperchi/tree/main/hyperchi/helpers) contains the full collection, string, file, logging, and HTTP-client APIs.
