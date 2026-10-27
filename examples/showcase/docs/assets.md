---
title: Static files and assets
section: Built-ins
order: 90
next: content
description: Serve CSS, JavaScript, and images with content-hashed URLs and long caching, compress responses, embed everything in one binary, and run Vite beside Go in development.
---

## Serve files, then version them {#overview}

A static mount publishes the regular files under a directory at a URL prefix. Directories and dotfiles are never published, and a symlink may resolve only inside the root. HyperChi answers GET and HEAD, and gives every file an ETag so conditional requests return 304.

Three methods mount files, and each takes a pattern of the form `/prefix/*`:

- `app.Static(pattern, dir)` serves a directory on disk. It panics if the directory cannot be opened.
- `app.StaticWithOptions(pattern, dir, options)` does the same and returns the error.
- `app.StaticFS(pattern, fsys, options)` serves any `fs.FS`, such as an embedded directory.

`StaticOptions` has four fields. `Development` sends `Cache-Control: no-cache` for every file. `CacheControl` sets the policy for an unversioned request (default `public, max-age=3600`). `CachePolicy` decides per request and overrides the other two policies. `OnError` renders 404, 405, and 500, and defaults to the app's error pipeline, so a missing file gets the same error page as a missing route.

### Content-hashed URLs

A year of caching is only safe when the URL changes with the file. The `asset` template function does that. It hashes the file and appends a short version:

```html
<link rel="stylesheet" href="{{asset "/static/css/app.css"}}">
<!-- /static/css/app.css?v=6d6068180a5c -->
```

A request whose `?v=` matches the file's current hash is answered with `public, max-age=31536000, immutable`. Any other request gets the mount's `CacheControl`. Edit the file and the next render writes a new `?v=`, so browsers fetch it once and then keep it.

In Go, `app.AssetURL` does the same lookup:

```go
u, err := app.AssetURL("/static/app.css")
if err != nil {
    return err
}
// u is "/static/app.css?v=6d6068180a5c"
```

The path must start with the prefix of a mount on the app and name a file that mount serves. A missing file, a directory, a dotfile, an unclean path such as one with `..`, or a path carrying a query or fragment is an error. In a template that error stops the render, so a broken reference fails in development and in tests instead of shipping as a 404. A mount made inside `app.Group` is known by its full path, such as `/admin/static/`.

Each file is hashed once, and the result is cached. In development, or with `Development: true`, the file is checked on every call and rehashed when its size or modification time changes.

## Responsive images {#responsive-images}

`ResponsiveImage` is an opt-in build/startup operation available on both
`AssetPipeline` and `Optimizer`. It decodes JPEG, PNG, or WebP once and generates
JPEG or PNG variants. No new dependencies or C toolchain are required.

```go
pipeline := assets.NewWithConfig(&assets.Config{
    SourceDir: "assets", OutputDir: "static", JPEGQuality: 85,
})
hero, err := pipeline.ResponsiveImage("assets/images/hero.png", assets.ImageOptions{
    Widths: []int{480, 960, 1440},
    Format: "jpeg",
    URLPrefix: "/static", // mount static/ at this URL in the app
})
if err != nil {
    log.Fatal(err)
}
// Pass hero to the template as .hero.
```

```gotemplate
{{with .hero}}
  {{$fallback := index .Variants 0}}
  <img src="{{$fallback.URL}}" srcset="{{.SrcSet}}"
       sizes="(max-width: 960px) 100vw, 960px"
       width="{{$fallback.Width}}" height="{{$fallback.Height}}" alt="Mountain sunrise">
{{end}}
```

Widths are sorted and deduplicated after clamping to the original width and
`Config.MaxWidth`. Images retain their aspect ratio and are never enlarged.
Empty `Format` preserves JPEG/PNG; WebP input requires an explicit output format.
PNG keeps transparency; JPEG composites onto white. `JPEGQuality` must be 1–100
(or zero for 85). Widths must contain 1–32 positive values. Inputs are limited to
32 MiB and 40 million pixels before full decoding.

Files land in `OutputDir/responsive/<sha256>.<format>`; the digest covers encoded
bytes, so changed content gets a new URL. Writes use temporary files and rename.
On failure, already completed variants remain. Build into a clean output directory
to discard obsolete variants. Serve the output with `app.Static` or another static
mount; `URLPrefix` must be a clean local absolute path. These files are generated
explicitly: the watcher does not remember responsive recipes or rebuild them.
WebP/AVIF encoding and automatic template/CLI integration are follow-up work.

## Embed the files in the binary {#example}

For a single-binary deploy, embed the static directory and the templates. `fs.Sub` removes the directory name so URLs stay `/static/app.css`.

```go
//go:embed static
var staticFiles embed.FS

//go:embed templates
var templateFiles embed.FS

func assets(app *hyperchi.HyperChi) error {
    sub, err := fs.Sub(staticFiles, "static")
    if err != nil {
        return err
    }
    if err := app.StaticFS("/static/*", sub, hyperchi.StaticOptions{}); err != nil {
        return err
    }
    return app.LoadTemplatesFS(templateFiles, "templates/**/*.tmpl")
}
```

`LoadTemplatesFS` follows the rules of `LoadTemplates`: `**` matches at any depth, a pattern that matches nothing is an error, and a file without a `{{define "name"}}` is named by its base file name. An embedded file has no modification time, so the server derives its ETag from the content and treats the filesystem as immutable.

Mount before you render. A template that calls `asset` fails for a path no mount serves, so the mount must exist when the page renders.

To serve a directory on disk instead, for files an operator can replace without a rebuild:

```go
func disk(app *hyperchi.HyperChi) error {
    return app.StaticWithOptions("/static/*", "static", hyperchi.StaticOptions{
        Development:  app.IsDevelopment(),
        CacheControl: "public, max-age=86400",
    })
}
```

## Compression and BREACH {#compression}

`app.Middleware.Compress()` gzips responses for clients that accept it. It passes through SSE streams, WebSocket upgrades, HEAD requests, bodies under 1 KiB, and media that is already compressed (images other than SVG, video, audio, WOFF fonts, archives). A compressed response loses its `Content-Length`, and a strong ETag becomes weak.

`app.AutoMiddleware()` installs it outside development. The reason is BREACH: compressing a page that reflects request input and also carries a fixed secret lets an attacker recover the secret from response sizes. The CSRF token would be that secret, but `Middleware.CSRF` masks it afresh on every response, so a compressed page never repeats it. The default stack is therefore safe to compress.

The exception is any other per-user secret rendered unmasked next to reflected input, such as an API key printed on a page. Keep those pages off compressed routes. An app with such pages builds its own stack from `Middleware.Default` and adds compression where it is safe. Install `Compress` after `Default`, so Recovery wraps it and a panic still gets a clean error page. To change the level or the minimum size:

```go
func compress(app *hyperchi.HyperChi) {
    app.Use(app.Middleware.Default()...)
    app.Use(hyperchi.CompressWithOptions(hyperchi.CompressOptions{Level: 6, MinSize: 512}))
}
```

Only gzip is offered, because brotli and zstd would need a third-party encoder.

## Vite beside Go {#vite}

The scaffold keeps Vite for assets only. There is no proxy: the browser talks to Go on `:8080`, and Go writes script tags that point at Vite on `:3000`. Two globals carry the switch to every template:

```go
func vite(app *hyperchi.HyperChi, cfg *config.Config) {
    app.SetGlobal("development", cfg.IsDevelopment())
    app.SetGlobal("vite_dev_server", "http://localhost:3000")
}
```

The base layout then loads modules from Vite in development, and the built files in production. The scaffold links the built files with plain paths; the version below wraps them in `asset`, which matters because its Vite config writes unhashed names (`assets/[name].js`):

```html
{{if .development}}
  <script type="module" src="{{.vite_dev_server}}/@vite/client"></script>
  <script type="module" src="{{.vite_dev_server}}/src/main.js"></script>
{{else}}
  <script type="module" src="{{asset "/dist/assets/main.js"}}"></script>
{{end}}
```

Open the Go server, not Vite. Vite's config sets `host: '0.0.0.0'`, `port: 3000`, and `cors: true`, so a browser on another machine can load modules from it. Set `vite_dev_server` to an address that browser can reach, because it is written into the page. Globals are values for every template: set them during startup, handler data wins over a global with the same key, and a global wins over a query parameter.

In production, `vite build` writes to `dist/`, and a second mount, `app.StaticWithOptions("/dist/*", "dist", ...)`, serves it with the same hashed URLs. Without `ENVIRONMENT=production`, `development` stays true and the page keeps asking Vite for modules that are not running.

## Keep going {#next}

Assets are one part of a production setup: the [configuration page](/docs/configuration) covers the environment switch, and the [deploying page](/docs/deploying) covers shipping the binary. The [templates page](/docs/templates) explains how template names and the function map work.
