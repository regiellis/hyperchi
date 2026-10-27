# Working in __APPNAME__

This is a [HyperChi](https://github.com/regiellis/hyperchi) app: Go on the server, htmx 4 in
the browser, HTML rendered from Go templates. Write it with the framework's API. Plain
`net/http` code here skips the framework's htmx negotiation, error pages, and validation.

## Use this, not that

| Do | Instead of |
| --- | --- |
| `app.Get("/x", func(c *hyperchi.Context) error { ... })` | `func(w http.ResponseWriter, r *http.Request)` |
| `return c.View("page", data)` | `json.NewEncoder(w)`, `hyperchi.WriteJSON` to render |
| `return c.Fragment("row", item)` | building HTML strings by hand |
| `return c.JSON(data)` | `w.Header().Set("Content-Type", ...)` plus an encoder |
| `return c.Blob("text/csv", body)` for any other content type | `w.Header().Set(...)` plus `w.Write` |
| `c.Param("id")`, `c.ParamInt("id")`, `c.ParamInt64("id")`, `c.ParamUUID("id")` (a bad value is a 400 to return) | `chi.URLParam(r, "id")` plus `strconv` or a UUID regexp |
| `c.Query("q")`, `c.QueryDefault("page", "1")`, `c.QueryInt("page", 1)`, `c.QueryInt64`, `c.QueryFloat`, `c.QueryBool("all", false)` | `r.URL.Query().Get(...)` plus `strconv` |
| `in, err := hyperchi.Bind[Form](c)` | `r.ParseForm()` plus manual checks |
| `c.FormValues("tag")`, `c.FormInt("qty", 1)`, `c.FormBool("subscribe")` for one or two loose fields | `r.ParseForm()` and `r.Form["tag"]` |
| `return hyperchi.NotFound("No such item.")` | `http.Error(w, ..., 404)` |
| `return c.Redirect("/done")` | `http.Redirect`, which misses htmx's `HX-Redirect` |
| `return c.Back("/items")` after a form post | redirecting to `r.Referer()`, which can send the user off site |
| `return c.Attachment("report.csv", "text/csv", body)` | setting `Content-Disposition` by hand |
| `c.ETag(v)`, then `if c.Fresh() { return c.NotModified() }` | comparing `If-None-Match` yourself |
| `c.WantsJSON()` | parsing the `Accept` header |
| `c.HX().Trigger("saved").Retarget("#list")` | `w.Header().Set("HX-Trigger", ...)` |
| `app.SetGlobal("key", value)` once at startup | repeating the same keys in every handler |
| `app.WebSocket("/ws", func(s *hyperchi.Socket) error { ... })` | upgrading a connection by hand |
| `app.Room("lobby").Swap("#feed", "beforeend", "row", data)` | tracking connections to broadcast to |
| `app.Serve(addr)` | `http.ListenAndServe` |
| `<link href="{{asset "/static/css/app.css"}}">` | hand-built `?v=` query strings or hashes |
| `c.Flash("success", "Saved.")` then `return c.Redirect("/items")`; show `.flashes` | query-string notices, or rendering the page in the POST response |
| `c.SetSignedCookie(ck)` and `c.SignedCookie(name, maxAge)` | hand-rolled HMAC over cookie values |
| `app.Content.LoadDir("content", hyperchi.WithMarkdown(fn))` (or `LoadFS`) plus `app.ContentRoutes` | walking a Markdown folder and parsing files yourself |
| `pm.MultiStepForm(path, ...)` with per-step `Validate`, `Fields`, and `Complete` | wizard state in hidden fields or hand-managed session keys |
| `result.SaveAll(dir, security.SaveOptions{})` or `security.SaveUpload` | `os.Create` with the client's filename |
| `hc-on:click`, `hc-data` directives | Alpine.js or inline `onclick` |

## Routes and responses

```go
app.Get("/items", func(c *hyperchi.Context) error {
	items, err := store.List(c.Ctx())
	if err != nil {
		return err // a 500; the error text is logged, never shown
	}
	return c.View("items", hyperchi.H{"items": items})
})

type NewItem struct {
	Name string `form:"name" validate:"required,max=80"`
}

app.Post("/items", func(c *hyperchi.Context) error {
	in, err := hyperchi.Bind[NewItem](c)
	if err != nil {
		return err // 422 with per-field messages, or 400 for an unreadable body
	}
	item, err := store.Add(c.Ctx(), in.Name)
	if err != nil {
		return err
	}
	return c.Status(201).HX().Trigger("item-added").Fragment("item-row", item)
})
```

- `c.View` sends the full page inside `layout-base` for a normal or boosted navigation, and
  only the template for an htmx partial request. Use it for pages. Use `c.Fragment` for
  endpoints that only answer htmx swaps, and `c.Page` to always include the layout.
- A returned error is rendered for whoever asked: JSON for an API client, the `error`
  template (or a small alert) for htmx, an error page for a browser. Pick the status with
  `hyperchi.NotFound`, `BadRequest`, `Forbidden`, `Conflict`, `Unprocessable`, or
  `hyperchi.Errorf(code, ...)`.
- API routes whose clients never ask for HTML take `hyperchi.JSONErrors` as route or group
  middleware, so their errors are always JSON.
- htmx 4 swaps every status except 204 and 304, so a 422 form fragment lands in place.
- Validation tags: `required`, `min=N`, `max=N`, `email`, `url`, `oneof=a b c`. Add a
  `Validate() error` method for rules that span fields or need the database
  (`database.IsTaken`). Input whose shape is only known at runtime: register a
  `validator.Schema` on `app.Schemas` and call `c.ValidateSchema(name)`.
- Middleware is plain `func(http.Handler) http.Handler`: `app.Use(...)` for every route, or
  pass it after the handler for one route.
- Live updates: `app.WebSocket` handlers read htmx ws messages with `s.Receive()`, validate
  them with `hyperchi.BindMessage[T](msg)`, and answer with `s.Swap(target, swap, template,
  data)`. `s.Join(room)` plus `app.Room(room).Swap(...)` pushes to everyone in a room. The page
  needs the htmx ws extension (`app.Extensions().EnableExtensionAssets()` serves it at
  `/hyperchi/js/hx-ws.js`).
- General helpers live in packages, not on the app: `helpers.S(s)` for string chains,
  `helpers.FromSlice(xs)` for collections, `helpers.NewJSON()`, `helpers.Must(err)`,
  `helpers.Ptr(v)`. App-owned services sit on `app.Helpers`: `.Logger`, `.Env`, `.HTTP`,
  `.Profiler`. Look there before adding a utility of your own.
- Raw `net/http` is for code that already speaks it (a third-party handler, a proxy):
  `app.Handle(method, pattern, handler)` or `hyperchi.WrapHTTP(handler)`.

## Forms, cookies, and files

- CSRF is on: `app.AutoMiddleware()` refuses a POST, PUT, PATCH, or DELETE without the token
  (403). The layout sends it on every htmx request (`hx-headers` on `<body>`); a plain
  `<form method="post">` adds `<input type="hidden" name="csrf_token" value="{{.csrfToken}}">`.
  Never turn it off to get a form working.
- `c.Flash(kind, message)` keeps a notice for the next request; templates read `.flashes`
  (`{{range .flashes}}{{.Kind}}: {{.Message}}{{end}}`). It travels in a signed cookie, or the
  session behind `app.SessionMiddleware()`.
- Signed cookies and flashes need `SECRET_KEY` (32 bytes or more) in production; without it
  they fail with `hyperchi.ErrNoSecretKey`. Development signs with a random key.
- `pm := app.Extensions().GetPatternManager()`. `pm.MultiStepForm` keeps answers in the
  session, so call `app.UseSessions(...)` and `app.Use(app.SessionMiddleware())` first. Each
  step lists its `Fields` and a `Validate` (`patterns.SchemaStep(schema)` adapts a
  `validator.Schema`); step templates read `.values` and `.errors`; `Complete` gets every
  answer.
- Store uploads with `security.SaveUpload` or `UploadValidationResult.SaveAll`: they name the
  file themselves from its sniffed type. Show `SavedFile.OriginalName`; never use it as a path.

## Templates

- Every file under `templates/` loads at startup. Name each one with `{{define "name"}}`;
  that name is what `c.View("name", ...)` uses.
- `templates/layouts/base.tmpl` defines `layout-base`. `c.Layout("other").Page(...)` uses a
  `layout-other` instead.
- Template data is the handler's data, plus `app.SetGlobal` values (`development`,
  `vite_dev_server`), plus route and query parameters. Handler data wins over globals, and
  globals win over the query string.
- Link static files with `{{asset "/static/..."}}`. It adds a content version that the static
  handler caches for a year, and a missing file fails the render.

## Running it

- Configuration comes from the environment (`config.FromEnv` in `main.go`). Every deployment
  sets `ENVIRONMENT=production`; an unset value means development (relaxed WebSocket origins,
  open CORS, detailed error pages). Behind a reverse proxy, list its CIDRs in
  `Config.Security.TrustedProxies` so `c.ClientIP()` reports the real visitor.
- `task dev` runs the Go server on :8080 and Vite on :3000. Open the Go server; Vite only
  serves assets. `task dev:air` adds Go hot reload.
- `task build` builds assets and the binary; `task serve` runs it with `PRODUCTION=true`.
- Check work with `go vet ./...` and `go test ./...`.
