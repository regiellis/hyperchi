---
title: Testing
section: Production
order: 160
next: integrations
description: Test handlers against the real app with httptest, in the same shapes a browser, htmx, and an API client use, with temporary files and a throwaway database.
---

## Test the real app {#overview}

A HyperChi app is an `http.Handler`, so a test needs no server and no mocks. Build the app the way `main` does, register the same routes, and call `app.ServeHTTP` with an `httptest` request and recorder. The request travels through the router, the middleware, `Bind`, the template engine, and the error pipeline, which is where handler bugs live.

Three habits keep this cheap:

- **One routes function.** Put route registration in `func Routes(app *hyperchi.HyperChi)` and call it from `main` and from the tests. A route added in `main` only is a route no test sees.
- **Temporary everything.** Write fixture templates and files under `t.TempDir()`, and give the app a database file there too, or disable it. A test must never create `hyperchi.db` in the package directory (`config.Default()` names it that) or touch a shared file.
- **Real headers.** htmx behavior depends on request headers, so set them: `HX-Request: true` for a partial, `Accept: application/json` for an API client.

## A test app and three kinds of response {#example}

The routes under test:

```go
type newUser struct {
    Name  string `form:"name" json:"name" validate:"required,max=40"`
    Email string `form:"email" json:"email" validate:"required,email"`
}

func Routes(app *hyperchi.HyperChi) {
    app.Get("/users", func(c *hyperchi.Context) error {
        return c.View("users", hyperchi.H{"users": []string{"Ada", "Grace"}})
    })
    app.Get("/api/users/{id}", func(c *hyperchi.Context) error {
        id, err := c.ParamInt("id")
        if err != nil {
            return err
        }
        if id != 1 {
            return hyperchi.NotFound("No such user.")
        }
        return c.JSON(hyperchi.H{"id": id, "name": "Ada"})
    })
    app.Post("/users", func(c *hyperchi.Context) error {
        in, err := hyperchi.Bind[newUser](c)
        if err != nil {
            return err
        }
        return c.Status(201).Fragment("user-row", hyperchi.H{"name": in.Name})
    })
}
```

The helper that builds the app, with fixtures in a temporary directory and a temporary database:

```go
func newTestApp(t *testing.T) *hyperchi.HyperChi {
    t.Helper()
    dir := t.TempDir()
    templates := map[string]string{
        "layout.tmpl": `{{define "layout-base"}}<html><body>{{.content}}</body></html>{{end}}`,
        "users.tmpl":  `{{define "users"}}<ul>{{range .users}}<li>{{.}}</li>{{end}}</ul>{{end}}`,
        "row.tmpl":    `{{define "user-row"}}<li>{{.name}}</li>{{end}}`,
    }
    for name, body := range templates {
        if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
            t.Fatal(err)
        }
    }

    cfg := config.Default()
    cfg.Database.Name = filepath.Join(dir, "test.db") // or cfg.Database.Disabled = true
    cfg.Assets.HotReload = false
    app, err := hyperchi.NewWithConfigE(cfg)
    if err != nil {
        t.Fatal(err)
    }
    t.Cleanup(func() {
        if err := app.Close(); err != nil {
            t.Error(err)
        }
    })
    if err := app.LoadTemplates(filepath.Join(dir, "*.tmpl")); err != nil {
        t.Fatal(err)
    }
    Routes(app)
    return app
}

func serve(app *hyperchi.HyperChi, req *http.Request) *httptest.ResponseRecorder {
    rec := httptest.NewRecorder()
    app.ServeHTTP(rec, req)
    return rec
}
```

### Page or fragment

`c.View` sends the whole page for a normal request and only the template for an htmx partial. Test both from one route:

```go
func TestUsersPageAndFragment(t *testing.T) {
    app := newTestApp(t)

    page := serve(app, httptest.NewRequest(http.MethodGet, "/users", nil))
    if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "<html>") {
        t.Fatalf("page: %d %q", page.Code, page.Body.String())
    }

    req := httptest.NewRequest(http.MethodGet, "/users", nil)
    req.Header.Set("HX-Request", "true")
    fragment := serve(app, req)
    if strings.Contains(fragment.Body.String(), "<html>") || !strings.Contains(fragment.Body.String(), "<li>Ada</li>") {
        t.Fatalf("fragment: %d %q", fragment.Code, fragment.Body.String())
    }
}
```

A boosted navigation (`HX-Boosted: true`) and a history restore (`HX-History-Restore-Request: true`) also get the full page, so add a case for each if your layout matters to them. To check response headers such as `HX-Trigger`, read `rec.Header()`.

### JSON and returned errors

Decode the body into a struct, and send `Accept: application/json` to see an error as JSON. A browser asking for the same URL gets an error page instead.

```go
func TestUserJSON(t *testing.T) {
    app := newTestApp(t)

    rec := serve(app, httptest.NewRequest(http.MethodGet, "/api/users/1", nil))
    var got struct {
        ID   int    `json:"id"`
        Name string `json:"name"`
    }
    if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
        t.Fatalf("%v: %q", err, rec.Body.String())
    }
    if rec.Code != http.StatusOK || got.Name != "Ada" {
        t.Fatalf("%d %+v", rec.Code, got)
    }

    req := httptest.NewRequest(http.MethodGet, "/api/users/2", nil)
    req.Header.Set("Accept", "application/json")
    if rec := serve(app, req); rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "No such user.") {
        t.Fatalf("%d %q", rec.Code, rec.Body.String())
    }
}
```

### Bind and the 422

A failed validation is a 422 with a `fields` object, so assert on the field and its message, not on the whole body:

```go
func TestCreateUserValidation(t *testing.T) {
    app := newTestApp(t)
    post := func(form url.Values) *httptest.ResponseRecorder {
        req := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(form.Encode()))
        req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
        req.Header.Set("Accept", "application/json")
        return serve(app, req)
    }

    rec := post(url.Values{"name": {"Ada"}, "email": {"nope"}})
    if rec.Code != http.StatusUnprocessableEntity {
        t.Fatalf("status %d: %q", rec.Code, rec.Body.String())
    }
    var body struct {
        Fields map[string][]string `json:"fields"`
    }
    if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
        t.Fatal(err)
    }
    if got := body.Fields["email"]; len(got) != 1 || got[0] != "must be a valid email address" {
        t.Fatalf("email errors: %v", got)
    }

    if rec := post(url.Values{"name": {"Ada"}, "email": {"ada@example.com"}}); rec.Code != http.StatusCreated {
        t.Fatalf("valid form: %d %q", rec.Code, rec.Body.String())
    }
}
```

### The database

With a database file in `t.TempDir()`, `app.DB` is a real SQLite connection that disappears with the test. Create the schema in the test or a helper, and assert through `app.DB`:

```go
func TestDatabaseIsTemporary(t *testing.T) {
    app := newTestApp(t)
    if _, err := app.DB.Exec(`CREATE TABLE notes (id INTEGER PRIMARY KEY, body TEXT NOT NULL)`); err != nil {
        t.Fatal(err)
    }
    if _, err := app.DB.Create("notes", map[string]any{"body": "first"}); err != nil {
        t.Fatal(err)
    }
    n, err := app.DB.Count("notes", "")
    if err != nil || n != 1 {
        t.Fatalf("count = %d, err = %v", n, err)
    }
}
```

`cfg.Database.Disabled = true` leaves `app.DB` nil, which is right for apps without persistence. Prefer a file in `t.TempDir()` over `":memory:"`, so the test reads and writes the same database a deployed app would.

### Middleware, client IPs, and CSRF

Tests that exercise the stack build the app the way `main` does: `app.AutoMiddleware()`, then `app.Use(...)` before any route. Set `req.RemoteAddr` to play the TCP peer:

```go
app.AutoMiddleware() // includes CSRF
app.Get("/ip", func(c *hyperchi.Context) error { return c.Text(c.ClientIP()) })

req := httptest.NewRequest(http.MethodGet, "/ip", nil)
req.RemoteAddr = "203.0.113.9:4000"
req.Header.Set("X-Forwarded-For", "198.51.100.1")
// rec.Body is "203.0.113.9": the header is ignored from an untrusted peer.
```

With `cfg.Security.TrustedProxies = []string{"172.18.0.0/16"}` and `RemoteAddr = "172.18.0.2:4000"`, the same request reports `198.51.100.1`. For CSRF, a POST with no token must be 403. A GET issues the `csrf_token` cookie, and a POST that sends that cookie back with its value in `X-CSRF-Token` passes. Collect cookies with `rec.Result().Cookies()`. When a flow spans several requests (a login, a wizard), start `httptest.NewServer(app)` and use an `http.Client` with a cookie jar, as the mailgrid example does.

## Run them {#next}

```bash
go vet ./...
go test -race -count=1 ./...
```

`-race` needs a C toolchain because Go's race detector does, even though your app builds with `CGO_ENABLED=0`. Run it in CI: handlers share an app across requests, and the race detector finds the shared state you did not mean to share. If your project has a `Taskfile.yml`, use its `test` target instead of the raw command.

The framework's own tests under `hyperchi/` and the reference apps in the [apps index](/docs/apps) use these same patterns. Before you ship, walk the [Deploying](/docs/deploying) checklist, and read [Security](/docs/security) to decide which of its rules deserve a test of their own.
