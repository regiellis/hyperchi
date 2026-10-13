# assets

Package `assets` copies, minifies, and optimizes static files, downloads pinned
builds of Tailwind CSS and HTMX, and reloads the browser when a source file
changes. It is a small Go pipeline, not a JavaScript bundler: for imports,
TypeScript, tree shaking, or source maps, use Vite.

```go
cfg := assets.DefaultConfig()
cfg.EnableTailwind, cfg.EnableHTMX = false, false // skip the CDN downloads
pipeline := assets.NewWithConfig(cfg)
if err := pipeline.Initialize(); err != nil {
	log.Fatal(err)
}
defer func() {
	if err := pipeline.Close(); err != nil {
		log.Print(err)
	}
}()
if err := pipeline.ProcessCSS("assets/css/app.css"); err != nil {
	log.Fatal(err)
}
```

## Components

| Part        | What it does                                                                                     |
| ----------- | ------------------------------------------------------------------------------------------------ |
| `Compiler`  | Copies CSS to `OutputDir/css` and JS to `OutputDir/js`. With minification on, output is minified by `tdewolff/minify` and named `name.min.css` / `name.min.js`. With `EnableTailwind`, `@tailwind base;` becomes an `@import` of the bundled Tailwind CSS. It compiles neither Sass nor TypeScript: `.scss`, `.sass`, and `.ts` inputs return an error naming the file, so compile them with their own tools first. |
| `Bundler`   | Downloads Tailwind CSS 4.1.11 (CSS and browser JS) and HTMX 4.0.0 from jsDelivr into `StaticDir` when the files are missing, and writes `icons/heroicons/manifest.json` with a dozen Heroicons v2 outline icons. Each download must match a pinned SHA-256 (checked against jsDelivr's published package hashes) or nothing is written. Commit the downloaded files. |
| `Optimizer` | Writes each image to `OutputDir` at its path relative to `SourceDir` (`assets/images/a.png` becomes `static/images/a.png`, and a file outside `SourceDir` keeps only its base name). See below. |
| `Watcher`   | Polls the source directories once a second, rebuilds a changed file, and sends a reload message to browsers connected to `HandleWebSocket`. |

## Images

| Input                 | Output                                                                         |
| --------------------- | ------------------------------------------------------------------------------ |
| `.jpg`, `.jpeg`       | Re-encoded at `JPEGQuality` (85 when zero), downscaled to `MaxWidth` if wider   |
| `.png`                | Re-encoded with the best compression level, downscaled the same way, alpha kept |
| `.webp`               | Checked to decode, then copied unchanged                                       |
| `.svg`                | Comments and whitespace removed when minification is on, otherwise copied      |
| anything else         | Copied unchanged                                                               |

Resizing uses Catmull-Rom from `golang.org/x/image/draw`. Nothing is converted
to WebP: neither the standard library nor `golang.org/x/image` can encode it,
and the legacy `OptimizeImage` method copies WebP files without resizing them.

### Responsive variants and conversion

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

## Configuration

| Field                                             | Meaning                                                          |
| ------------------------------------------------- | ---------------------------------------------------------------- |
| `StaticDir`                                       | Where bundles go and where `Initialize` creates `css`, `js`, `images`, `icons` |
| `SourceDir`, `OutputDir`                          | Source assets, and where compiled and optimized files go         |
| `EnableTailwind`, `EnableHTMX`, `EnableHeroicons` | Bundle these in `Initialize`                                     |
| `EnableMinify`                                     | Minify CSS, JS, and SVG output                                   |
| `EnableWatch`                                     | Create a `Watcher` and start it in `Initialize`                  |
| `HotReload`                                       | Send reload messages after rebuilds, serve `GetHotReloadScript` |
| `MaxWidth`, `JPEGQuality`                         | Image downscaling and JPEG quality                               |
| `Logger`                                          | Where the watcher logs (`slog.Default()` when nil)               |
| `Versioning`                                      | Accepted for compatibility, no effect                            |

`DefaultConfig` is the development setup (bundles, watching, hot reload).
`ProductionConfig` and `EnableProduction` turn minification on and watching and
hot reload off. `EnableProduction` does not stop a running watcher, but `Close`
does.

## Hot reload

Mount `Watcher.HandleWebSocket` at `/__hyperchi/hot-reload` and put
`GetHotReloadScript()` in the page. A client receives `{"type":"connected"}`
once it is registered, then `{"type":"reload","assetType":"css"}` (or `js`,
`image`, `page`) after each rebuild. The script swaps stylesheets in place for
CSS and reloads the page otherwise.

The watcher polls `SourceDir` plus `assets`, `styles`, and `images` in the
working directory. Change the list with `AddWatchDirectory` and
`RemoveWatchDirectory`. Below a watched directory it skips hidden files and
directories and anything under a directory named `node_modules`, `vendor`,
`tmp`, or `temp`. The match is on whole path segments, so `templates/` and
`index.tmpl` are watched. A change to `.html`, `.tmpl`, or `.tpl` reloads the
page without building anything.

`Stop` (or `AssetPipeline.Close`) ends polling, waits for the polling goroutine
to exit, and closes the hot-reload connections.
