---
title: Forms and validation
section: Core
order: 60
next: errors
description: Bind a request into a struct, check it with tags or a schema, and answer a failed submit with a fragment that keeps what the visitor typed. Covers CSRF, uploads, and multi-step forms.
---

## Bind, validate, answer {#overview}

`hyperchi.Bind[T](c)` decodes the request into a new `T` and checks it in one call. JSON bodies decode by `json` tags. Forms, multipart forms, and query strings decode by `form` tags, falling back to `json` and then the lower-cased field name. A pointer field such as `*int` stays nil when the field is missing or empty, so `required` and `min` can tell "not sent" from zero.

The returned error already carries an HTTP status, so a handler can return it as is:

- A body that cannot be read is a 400, and a JSON body over 1 MiB or a multipart body over 32 MiB is a 413.
- A value that does not convert (letters in a number field) or a failed rule is a 422 with per-field messages, such as `"email": ["must be a valid email address"]`.
- A `Validate() error` method on the struct runs after the tags pass, for rules that span fields or need the database. Return a plain error for a 422 with one message, or an `*HTTPError` to choose the fields.

The rules are `required`, `min=N`, `max=N` (length for strings and slices, value for numbers), `email`, `url`, and `oneof=a b c`. An unknown rule in a tag is a 500 on the first request, so a typo fails loudly instead of validating nothing.

htmx 4 swaps every status except 204 and 304, so a 422 response lands in the page like any other. The error pipeline decides what the 422 looks like: JSON for an API client, a small alert for htmx, an error page for a browser (see [Errors](/docs/errors)). For a form you usually want better than that: the form again, with the visitor's input and a message beside each bad field.

## A form that explains itself {#example}

`hyperchi.FieldErrors(err)` returns the per-field messages of a validation failure, or nil for any other error. Use it to re-render the form fragment with status 422:

```go
type Signup struct {
    Name  string `form:"name" validate:"required,max=80"`
    Email string `form:"email" validate:"required,email"`
    Plan  string `form:"plan" validate:"oneof=free team"`
    Age   *int   `form:"age" validate:"min=13"`
}

app.Post("/signup", func(c *hyperchi.Context) error {
    in, err := hyperchi.Bind[Signup](c)
    if fields := hyperchi.FieldErrors(err); fields != nil {
        return c.Status(422).Fragment("signup-form", hyperchi.H{
            "in":     in,
            "errors": fields,
        })
    }
    if err != nil {
        return err // a 400 or 413 for an unreadable body
    }
    return c.Fragment("signup-done", in)
})
```

`Bind` returns the partly filled struct along with the error, so `in` holds whatever decoded cleanly. The GET route that first shows the form passes `"errors": map[string][]string{}` so the template can index it the same way. Messages read as the end of a sentence ("is required", "must be at least 13"), so the template supplies the subject:

```html
{{define "signup-form"}}
<form hx-post="/signup" hx-target="this" hx-swap="outerHTML">
  <label>Name
    <input name="name" value="{{.in.Name}}"{{if index .errors "name"}} aria-invalid="true"{{end}}>
  </label>
  {{with index .errors "name"}}<p class="field-error">Name {{index . 0}}.</p>{{end}}

  <label>Email
    <input name="email" type="email" value="{{.in.Email}}">
  </label>
  {{with index .errors "email"}}<p class="field-error">Email {{index . 0}}.</p>{{end}}

  <button>Join</button>
</form>
{{end}}
```

`html/template` escapes `.in.Name`, so echoing input back is safe. Never echo a password field.

A cross-field rule goes in `Validate`:

```go
type Booking struct {
    Start time.Time `form:"start" validate:"required"`
    End   time.Time `form:"end" validate:"required"`
}

func (b *Booking) Validate() error {
    if !b.End.After(b.Start) {
        return hyperchi.Unprocessable("Please correct the highlighted fields.",
            map[string][]string{"end": {"must be after the start"}})
    }
    return nil
}
```

`time.Time` fields accept `2006-01-02`, `2006-01-02T15:04`, and RFC 3339.

### Schemas for input with no struct

When the shape is only known at runtime, build a `validator.Schema`, register it on `app.Schemas`, and call `c.ValidateSchema(name)`. It reads a JSON or form body and returns the cleaned values, or the same 422 that `Bind` produces:

```go
app.Schemas.Register("contact", validator.NewSchema().
    Field("email", validator.String().Email().Required()).
    Field("topic", validator.String().OneOf("sales", "support").Required()).
    Field("message", validator.String().MinLength(10).MaxLength(2000)))

app.Post("/contact", func(c *hyperchi.Context) error {
    data, err := c.ValidateSchema("contact")
    if err != nil {
        return err
    }
    return c.JSON(hyperchi.H{"received": data["topic"]})
})
```

An unregistered name is a programming error and answers 500. `FieldErrors` works on this error too.

## CSRF {#csrf}

`app.AutoMiddleware()` turns CSRF protection on, because `Config.Security.CSRF` is true by default. An app that assembles its own stack adds it with `app.Use(app.Middleware.CSRF())`; installing it twice is harmless. A GET request gets a `csrf_token` cookie (SameSite Strict, Secure outside development, readable by scripts), and every other method must send the token back or get a 403 through the error pipeline.

Every template rendered for a request gets the token as `.csrfToken`. It is masked with a fresh one-time pad on every render, so no two responses contain the same bytes. That keeps a compressed page from leaking the token through the BREACH attack, and it is why production turns gzip on safely. In Go, `c.CSRFToken()` returns the same kind of masked token.

```go
app := hyperchi.New()
app.AutoMiddleware() // CSRF included

app.Get("/signup", func(c *hyperchi.Context) error {
    return c.View("signup", hyperchi.H{"errors": map[string][]string{}})
})
```

Give htmx the token once, on the body in your layout, and every request on the page carries it. htmx 4 does not inherit attributes unless you ask, hence `:inherited`. Projects made with `hyperchi new` already have this line:

```html
<body{{with .csrfToken}} hx-headers:inherited='{"X-CSRF-Token": "{{.}}"}'{{end}}>
```

A plain form that does not go through htmx sends the same token in a hidden field named `csrf_token`: `<input type="hidden" name="csrf_token" value="{{.csrfToken}}">`. The field accepts masked tokens only. The header also accepts the raw cookie value, for scripts that read the cookie themselves.

A page pre-rendered once at startup (`app.PreRender`, `app.PreRenderedPage`) has no request, so it cannot carry a token. Protect the forms on such pages with `app.ProtectForm`, which refuses cross-site posts by their `Origin` and `Sec-Fetch-Site` headers, and set `cfg.Security.CSRF = false`. Do not turn the token check off for any other reason.

## File uploads {#uploads}

A `*multipart.FileHeader` field (or a slice of them) in a `Bind` struct receives the uploaded file. Bind caps the whole multipart body at 32 MiB. `security.SaveUpload` stores the file under a directory you choose:

```go
type Avatar struct {
    Caption string                `form:"caption" validate:"max=120"`
    File    *multipart.FileHeader `form:"avatar"`
}

app.Post("/avatar", func(c *hyperchi.Context) error {
    in, err := hyperchi.Bind[Avatar](c)
    if err != nil {
        return err
    }
    if in.File == nil {
        return hyperchi.Unprocessable("Please correct the highlighted fields.",
            map[string][]string{"avatar": {"is required"}})
    }
    saved, err := security.SaveUpload("data/avatars", in.File, security.SaveOptions{MaxSize: 2 << 20})
    var limit *security.UploadLimitError
    if errors.As(err, &limit) {
        return hyperchi.NewHTTPError(http.StatusRequestEntityTooLarge, "That file is too large.").Wrap(err)
    }
    if err != nil {
        return hyperchi.BadRequest("That file type is not accepted.").Wrap(err)
    }
    return c.Fragment("avatar-saved", hyperchi.H{"name": saved.Name, "original": saved.OriginalName})
})
```

`SaveUpload` never lets the client choose the path. It names the file with 128 random bits plus an extension taken from the type it sniffs from the first 512 bytes, writes through an `os.Root` so a symlink cannot escape the directory, and removes a partial file on failure. By default it accepts JPEG, PNG, GIF, WebP, plain text, and PDF; pass `SaveOptions.Extensions` (start from `security.DefaultSaveExtensions()`) to change that. HTML, XML, SVG, and JavaScript are refused even if you list them. `SavedFile.OriginalName` is for display only.

To check type, count, and size limits before your handler runs, put `security.UploadMiddleware` on the route and let `SaveAll` store the files that passed. It answers 413 for a size failure and 400 for the rest. The default config requires a signed-in user, so set your own limits:

```go
limits := &security.FileUploadConfig{
    MaxFileSize:  5 << 20,
    MaxFiles:     3,
    AllowedTypes: []string{"image/png", "image/jpeg"},
}
app.Post("/gallery", func(c *hyperchi.Context) error {
    result := security.GetValidationResult(c.Request())
    saved, err := result.SaveAll("data/gallery", security.SaveOptions{})
    if err != nil {
        return err
    }
    return c.JSON(saved)
}, security.UploadMiddleware(limits))
```

`SaveAll` stores every file or none. The whole request body is capped at `MaxFiles * MaxFileSize` plus 1 MiB for boundaries. Serve stored files with a Content-Type from their extension and `X-Content-Type-Options: nosniff`.

## Multi-step forms {#wizard}

`patterns.MultiStepForm` serves a wizard at one path and keeps the answers in the visitor's session, so it needs sessions first:

```go
if err := app.UseKVStoreMemoryAdvanced("sessions"); err != nil {
    return err
}
if err := app.UseSessions(hyperchi.KVSessionConfig{Backend: "sessions", MaxAge: 30 * time.Minute}); err != nil {
    return err
}
app.Use(app.SessionMiddleware())

account := validator.NewSchema().
    Field("email", validator.String().Email().Required())

pm := app.Extensions().GetPatternManager()
pm.MultiStepForm("/join", patterns.MultiStepFormConfig{
    SuccessURL: "/joined",
    Steps: []patterns.FormStep{
        {Name: "account", Title: "Your account", Template: "join-account",
            Fields: []string{"email"}, Validate: patterns.SchemaStep(account)},
        {Name: "plan", Title: "Pick a plan", Template: "join-plan",
            Fields: []string{"plan"},
            Validate: func(v url.Values) map[string][]string {
                if v.Get("plan") != "free" && v.Get("plan") != "team" {
                    return map[string][]string{"plan": {"must be free or team"}}
                }
                return nil
            }},
    },
    Complete: func(r *http.Request, answers url.Values) error {
        return createAccount(r, answers.Get("email"), answers.Get("plan"))
    },
})
```

- `Fields` lists what a step keeps. Other submitted fields are dropped before `Validate` runs, so a crafted POST cannot add answers no validator saw.
- `Validate` returns messages by field name, or nil to accept. `patterns.SchemaStep` adapts a `validator.Schema`.
- `Complete` is required, and `MultiStepForm` panics without it. It receives every answer once the last step passes. If it returns an error the visitor gets a 500 and the answers stay, so they can submit the last step again.
- A rejected step renders again with status 422 and nothing stored. An accepted step is stored and the next step is the response. The last step redirects to `SuccessURL` (`HX-Redirect` for htmx, a 303 otherwise).
- A step cannot be skipped: asking for a later one redirects to the first unanswered step.

Each step template receives `.step`, `.total_steps`, `.progress`, `.step_info` (the `FormStep`), `.values` (`url.Values`), and `.errors`:

```html
{{define "join-account"}}
<form hx-post="/join" hx-target="this" hx-swap="outerHTML">
  <input type="hidden" name="step" value="{{.step}}">
  <label>Email <input name="email" type="email" value="{{.values.Get "email"}}"></label>
  {{range index .errors "email"}}<p class="field-error">{{.}}</p>{{end}}
  <button>Continue</button>
</form>
{{end}}
```

Without session middleware on the wizard's route, each request is a 500 whose logged cause is `patterns.ErrNoWizardStore`. The waitlist on the [home page](/#waitlist) is a running three-step wizard.

## Keep going {#next}

A form is the front of a write path, so the [data layer](/docs/data) is next. For what happens when a handler returns the error `Bind` gave it, read [Errors](/docs/errors).
