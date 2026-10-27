# __APPNAME__

Scaffolded with [HyperChi](https://github.com/regiellis/hyperchi), a Go web framework with
htmx 4 and a small declarative directive engine.

## Quick start

```bash
task dev          # backend + Vite together; visit http://localhost:8080
```

The page that comes up is a self-documenting docs site. Read through it to learn what
HyperChi does and how to use it. `AGENTS.md` is the short version for coding agents (and for
you): which framework call to use for each job.

## Common tasks

| Command | What it does |
|---|---|
| `task dev` | Backend + Vite together (no backend hot reload) |
| `task dev:air` | Backend (Air hot reload) + Vite together |
| `task build` | Build frontend + Go binary into `bin/app` |
| `task serve` | Run the production binary with `PRODUCTION=true` |
| `task clean` | Remove `bin/`, `dist/`, `node_modules/`, `tmp/` |
| `task --list` | List every task |

## Layout

```
.
├── main.go                 routes, template loading, globals, app.Serve
├── AGENTS.md               framework conventions for coding agents
├── go.mod
├── package.json            Vite, Tailwind v4, htmx 4
├── vite.config.js
├── postcss.config.js
├── Taskfile.yml            task runner targets
├── .air.toml               backend hot-reload config
├── src/
│   ├── main.js             bundled by Vite, exposes window.htmx
│   └── style.css           Tailwind v4, editorial typography, view transitions
├── static/
│   └── js/
│       ├── hyperchi-directives.js          (vendored runtime)
│       └── hyperchi-view-transitions.js    (vendored runtime)
└── templates/
    ├── layouts/
    │   └── base.tmpl       full HTML layout, htmx config, dark mode
    ├── pages/
    │   ├── index.tmpl                  landing
    │   ├── docs-directives.tmpl        directive reference + live demos
    │   ├── docs-htmx.tmpl              htmx 4 patterns
    │   ├── docs-view-transitions.tmpl  view transitions guide
    │   └── docs-deploy.tmpl            production, build, Docker
    └── components/
        ├── sidebar.tmpl    shared docs sidebar partial
        └── time.tmpl       example htmx fragment
```

## Where to start editing

1. **Replace the docs site.** Delete `templates/pages/docs-*.tmpl`, rewrite
   `templates/pages/index.tmpl` for your real landing page, and drop the `/docs/{slug}`
   route from `main.go`.
2. **Add a page.** Register a route in `main.go` that returns `c.View("name", data)`, and
   define `{{define "name"}}` in a file under `templates/`.
3. **Add styles.** Tailwind v4 picks up classes from `templates/**/*.tmpl` and
   `src/**/*.{js,ts}` automatically. No config needed.

## Adding a fragment route (htmx pattern)

```go
app.Get("/api/things", func(c *hyperchi.Context) error {
	return c.Fragment("things", hyperchi.H{"items": []string{"a", "b", "c"}})
})
```

```html
{{define "things"}}
<ul>{{range .items}}<li>{{.}}</li>{{end}}</ul>
{{end}}
```

```html
<div hx-get="/api/things" hx-trigger="load" hx-swap="innerHTML"></div>
```

## Handling a form

```go
type Signup struct {
	Email string `form:"email" validate:"required,email"`
}

app.Post("/signup", func(c *hyperchi.Context) error {
	in, err := hyperchi.Bind[Signup](c)
	if err != nil {
		return err // 422 with field messages; htmx swaps the error fragment in place
	}
	return c.HX().Trigger("signed-up").Fragment("thanks", in)
})
```

## Production

`task build` produces `bin/app` plus `dist/assets/{main.js,style.css}`. Ship all four: the
binary, `dist/`, `static/`, `templates/`. Set `PRODUCTION=true` so templates load from
`/dist/` instead of the Vite dev server. See `templates/pages/docs-deploy.tmpl` for a
Dockerfile.
