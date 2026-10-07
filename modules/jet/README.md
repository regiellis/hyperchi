# Jet adapter

An optional module connecting a configured Jet v6 set to HyperChi's integration
manager. The package name is `jetadapter`.

```go
views, err := jetadapter.New(set, "home.jet", "cards.jet")
helpers.Must(err)
helpers.Must(app.Integrations().Register("views", views))
app.Get("/", func(c *hyperchi.Context) error {
    return c.RenderWith("views", jetadapter.View{Name: "home.jet", Data: data})
})
```

Import `github.com/regiellis/hyperchi/modules/jet` and configure the `*jet.Set`
with your file, embedded, or in-memory loader first. `New` preloads all named
views and rejects missing or malformed templates. `View.Vars` optionally supplies
a request-local `jet.VarMap`. Unknown view names and incorrect data types are
errors. Configure loaders and global functions before serving; do not mutate them
while requests execute. Preserve Jet's default HTML escaping.

HyperChi buffers the output and sets response headers. Jet has no cancellable
execution API: cancellation is checked before execution and on output writes;
a long-running custom Jet function must implement its own cancellation.

Run `go test -race ./...` in this directory. The showcase uses local replaces.
Modules must be released with their own module-path version tags before consumers
can fetch those versions.
