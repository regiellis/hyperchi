# templ adapter

An optional module connecting templ components to HyperChi's integration manager.
The package name is `templadapter`.

```go
helpers.Must(app.Integrations().Register("views", templadapter.New()))
app.Get("/", func(c *hyperchi.Context) error {
    return c.RenderWith("views", HomePage(data))
})
```

Import `github.com/regiellis/hyperchi/modules/templ`. `HomePage` is a generated
`templ.Component`. Go 1.26 is required by the pinned templ runtime. HyperChi buffers
output, forwards the request context, sets HTML response headers, and propagates
errors. Do not pass a template filename or an arbitrary data struct instead of a
component. The adapter is stateless and needs no lifecycle hooks.

Run `go test -race ./...` in this directory. The showcase uses a local replace
and demonstrates full pages and fragment responses. Modules must be released
with their own module-path version tags before consumers can fetch those versions.
