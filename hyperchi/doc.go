/*
Package hyperchi is an htmx-first web framework for Go. It combines routing (chi),
server-side rendering, htmx request and response handling, content collections, and
real-time updates over SSE and WebSockets, aimed at blogs, documentation sites, podcasts,
and other content-heavy sites.

# Quick Start

	package main

	import (
		"log"
		"path/filepath"

		"github.com/regiellis/hyperchi/hyperchi"
	)

	func main() {
		app := hyperchi.New()
		if err := app.LoadTemplates(filepath.Join("templates", "**", "*.tmpl")); err != nil {
			log.Fatal(err)
		}
		app.Use(app.Middleware.Logger())

		app.Get("/", func(c *hyperchi.Context) error {
			return c.View("index", hyperchi.H{"title": "Welcome"})
		})

		log.Fatal(app.Serve(":8080"))
	}

# Handlers and responses

Every route is a [Handler], func(c *Context) error. Read the request through c and
return one of its responses:

	app.Get("/users/{id}", func(c *hyperchi.Context) error {
		id, err := c.ParamInt("id") // a non-integer is a 400
		if err != nil {
			return err
		}
		user, err := users.Find(c.Ctx(), id)
		if err != nil {
			return hyperchi.NotFound("No such user.")
		}
		return c.View("user", user)
	})

[Context.View] renders the full page inside its layout for a browser or boosted navigation and
the template alone for an htmx partial request. [Context.Page] always uses the layout,
[Context.Fragment] never does. [Context.JSON], [Context.Text], [Context.Redirect] (HX-Redirect
for htmx, 303 otherwise), and [Context.NoContent] cover the rest. [Context.Status],
[Context.Layout], and [Context.HX] chain in front of any of them:

	return c.Status(201).HX().Trigger("user-added").Fragment("user-row", user)

A returned error goes to [HyperChi.DefaultError] (or a function set with [HyperChi.OnError]),
which renders JSON for an API client, the "error" template as a fragment for htmx, and an
error page for a browser. [NotFound], [BadRequest], [Forbidden], [Conflict], [Unprocessable],
and [Errorf] choose the status; any other error is a 500 whose text is logged, never shown.

[Bind] decodes and validates input into a struct:

	type Signup struct {
		Email string `form:"email" validate:"required,email"`
		Age   *int   `form:"age" validate:"min=13"`
	}

	in, err := hyperchi.Bind[Signup](c) // 400 if unreadable, 422 with field messages if invalid

[HyperChi.SetGlobal] adds a value every template sees, such as a site name or the Vite dev
server URL. Middleware is plain func(http.Handler) http.Handler. Code that already speaks
net/http reaches the router through [HyperChi.Handle] and [WrapHTTP].

# Features

  - Content: a filesystem loader for Markdown, MDX, HTML, and text files.
  - Templates: layouts, components, and fragments, with over 200 template filters.
  - htmx: template helpers for hx-* attributes and response helpers for HX-* headers.
  - Real time: Server-Sent Events and WebSockets.
  - Database: SQLite (the pure-Go modernc.org/sqlite driver) with generic typed models.
  - Security: html/template escaping, CSRF middleware, upload validation, auth helpers.

The chi router, the SQLite driver, the bbolt KV store, and the SSE and WebSocket hubs ship
with the framework module. They are third-party dependencies, not standard-library code.

# Template System

[HyperChi.LoadTemplatesWithLayout] reads a directory layout inspired by Astro:

	templates/
	├── layouts/      # Page structure (layout-base, layout-minimal)
	├── components/   # Reusable UI (component-header, component-footer)
	├── fragments/    # HTMX islands (fragment-search, fragment-stats)
	└── pages/        # Content pages

Load it with:

	app.LoadTemplatesWithLayout()

Use in handlers:

	return c.View("home", data)              // layout for pages, bare for htmx
	return c.Fragment("fragment-search", data) // never a layout
	return c.Layout("minimal").Page("home", data)

# HTMX Integration

Template filters write htmx attributes:

	<div {{ hxGet "/api/data" }}
	     {{ hxTarget "#content" }}
	     {{ hxSwap "innerHTML" }}
	     {{ hxTrigger "click" }}>
	    Click me!
	</div>

Server-side htmx helpers:

	if c.IsHTMX() && c.HX().Target() == "div#results" {
		return c.Fragment("results", items)
	}
	return c.HX().Trigger("refresh").PushURL("/new-url").View("list", items)

# Content Management

Load a directory of Markdown, HTML, or text files into app.Content. Each
subdirectory is a collection; front matter becomes each item's Metadata:

	err := app.Content.LoadDir("content",
		hyperchi.WithContentCollection(content.CollectionConfig{Name: "posts", Schema: content.BlogPostSchema()}))
	if err != nil {
		return err // a *ContentLoadError lists every file left out; valid files loaded
	}
	app.ContentRoutes("/posts", "posts", hyperchi.ContentRouteConfig{SortBy: "publishedAt", SortOrder: "desc"})

	post, err := app.Content.Get("posts", "hello-world")
	recent, err := app.Content.Query("posts").Where("published", true).Limit(10).All()

In development LoadDir reloads changed files and app.Close stops it; in
production it loads once. [ContentManager.LoadFS] loads an embedded tree.
[ContentManager.Create] adds items at runtime. For richer queries (search,
pattern filters, numeric sorts) use the content package's ContentLayer
directly.

# Real-time Communication

Server-Sent Events push updates to the page:

	app.Extensions().EnableSSE("/events")
	app.Extensions().GetSSEHub().BroadcastHTML("channel", "event", "<div>Update</div>")

A WebSocket route reads client messages and answers with rendered templates; see
[HyperChi.WebSocket] for a full handler. [HyperChi.Room] broadcasts to every socket in a
room from any handler:

	app.WebSocket("/live", func(s *hyperchi.Socket) error {
		s.Join("lobby")
		return s.Wait()
	})

	err := app.Room("lobby").Swap("#feed", "beforeend", "chat-line", line)

# Database Integration

[database.NewModel] gives typed queries over a table:

	type Post struct {
		ID    int    `db:"id" json:"id"`
		Title string `db:"title" json:"title"`
		Body  string `db:"body" json:"body"`
	}

	postModel := database.NewModel[Post](app.DB, "posts")
	posts, err := postModel.Where("title LIKE ?", "Hello%")
	post, err := postModel.Get(1)

	// Inserts go through app.DB.
	id, err := app.DB.Create("posts", map[string]any{"title": "Hello", "body": "World"})

# Static Files and Compression

[HyperChi.StaticFS] and [HyperChi.StaticWithOptions] serve files and record each mount. The
asset template function versions a URL under a mount with a hash of the file's content:

	<link rel="stylesheet" href="{{asset "/static/css/app.css"}}">
	<!-- renders /static/css/app.css?v=3f2a9c1b7e4d -->

A request carrying the current version is cached for a year as immutable. Any other request
gets the mount's normal policy, and development always revalidates. In development an edited
file gets a new version on the next render. An unknown path fails the render. Go code calls
[HyperChi.AssetURL].

[MiddlewareGroup.Compress] gzips responses for clients that accept it, and leaves SSE streams,
WebSocket upgrades, small bodies, and compressed media alone. [HyperChi.AutoMiddleware] installs
it outside development. The CSRF token is masked afresh on every render, so compressing a page
that carries it and reflects request input does not expose it to BREACH. A page that renders
another per-user secret unmasked next to reflected input should not be compressed.

# Security

Templates are html/template, so every value is escaped for its context when rendered.
Mark markup you trust, and only that, as safe:

	safeHTML := security.MarkSafe("<p>Trusted content</p>")

CSRF protection (a double-submit cookie; c.CSRFToken() returns the token masked for one render):

	app.Use(app.Middleware.CSRF())

SECRET_KEY (config.Security.SecretKey, at least 32 bytes) signs cookies and flash messages.
SECRET_KEY_PREVIOUS keeps retired keys verifying during a rotation. Without a key, development
signs with a random per-process key and production fails each signing call with
[ErrNoSecretKey]. Signed values are readable base64url, not encrypted:

	if err := c.SetSignedCookie(&http.Cookie{Name: "player", Value: id}); err != nil {
		return err
	}
	id, err := c.SignedCookie("player", 30*24*time.Hour) // ErrCookieMissing, ErrCookieInvalid, ErrCookieExpired

[Context.Flash] keeps a message for the next request, in a signed cookie or the session when
there is one. Templates rendered by View, Page, or Fragment receive unread messages as .flashes:

	if err := c.Flash("success", "Post published."); err != nil {
		return err
	}
	return c.Redirect("/posts")

	{{range .flashes}}<p class="flash-{{.Kind}}">{{.Message}}</p>{{end}}

File upload validation:

	uploadConfig := &security.FileUploadConfig{
		MaxFileSize: 10 * 1024 * 1024, // 10 MiB
		AllowedTypes: []string{"image/jpeg", "image/png"},
	}
	app.Use(security.UploadMiddleware(uploadConfig))

# Validation

Request input is validated by [Bind] with validate tags on a struct, plus an optional
Validate() error method for rules that span fields or need the database
(database.IsTaken):

	type Signup struct {
		Email    string `form:"email" validate:"required,email"`
		Password string `form:"password" validate:"required,min=8"`
	}

	in, err := hyperchi.Bind[Signup](c)
	if fields := hyperchi.FieldErrors(err); fields != nil {
		return c.Status(422).Fragment("signup-form", hyperchi.H{"in": in, "errors": fields})
	}

For input whose shape is only known at runtime, register a schema from the validator
package on [HyperChi.Schemas] and check a request with [Context.ValidateSchema].

# Helper Libraries

String manipulation:

	s := helpers.S("hello world")
	upper := s.Upper().String()        // "HELLO WORLD"
	title := s.Title().String()         // "Hello World"
	slug := helpers.Slugify("hello world") // "hello-world"

Collections:

	list := helpers.NewSlice(1, 2, 3, 4, 5)
	list.ForEach(func(item int) {
		fmt.Println(item)
	})
	filtered := list.Filter(func(item int) bool {
		return item > 2
	})

Logging:

	app.Helpers.Logger.Info("Server starting on :8080")
	app.Helpers.Logger.Error("Failed to connect: %v", err)
	app.Helpers.Logger.Warn("High memory usage: %d MB", memUsage)

Error handling (Try turns a panic into an error; handle what it returns):

	if err := helpers.Try(riskyOperation); err != nil {
		return err
	}

	helpers.Catch(func() error {
		return operation()
	}, func(err error) {
		app.Helpers.Logger.Error("Operation failed: %v", err)
	})

# Development

Hot reload with Air (InitAir fails when .air.toml already exists):

	if err := app.Helpers.Air.InitAir(); err != nil {
		return err
	}
	if err := app.Helpers.Air.CreateDevScript(); err != nil {
		return err
	}

Development vs Production logging:

	if app.IsProduction() {
		helpers.SetupProductionLogging()
	} else {
		helpers.SetupDevelopmentLogging()
	}

Health checks:

	app.CreateHealthEndpoints() // /health, /health/ready, and /health/live

# Examples

See the examples/ directory for complete applications:

	examples/showcase/         # The HyperChi site: docs, live demos, integrations
	examples/streamer/         # Podcast platform
	examples/streamer-vite/    # Podcast platform with Vite

# Package Structure

	hyperchi/           # Core: routing, Context, rendering, middleware, sessions
	  assets/           # Static asset copying, minification, image optimization
	  config/           # Configuration and environment presets
	  content/          # Content collections
	  database/         # SQLite wrapper, query builder, migrations
	  extensions/       # SSE and WebSocket hubs
	  filters/          # Template filters
	  health/           # Health checks
	  helpers/          # Strings, slices, files, env, logging, HTTP client, Air
	  integrations/     # Optional capabilities registered by modules
	  logging/          # Structured logging
	  middleware/       # htmx, CSRF, and rate-limit middleware
	  patterns/         # htmx UI patterns
	  response/         # HX-* response headers
	  security/         # Auth, CSRF, uploads, crypto
	  server/           # Graceful shutdown
	  validator/        # Schemas for runtime-shaped input

# Scope

HyperChi targets content-driven sites (blogs, documentation, podcasts, portfolios, course
platforms) that render on the server, use little JavaScript, and deploy as one binary.

# Further Reading

- GitHub: https://github.com/regiellis/hyperchi
- Documentation: See docs/ directory
- Examples: See examples/ directory
- HTMX: https://htmx.org

# License

MIT. See the LICENSE file.
*/
package hyperchi
