# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.3.1] - 2026-10-07

### Fixed
- Every `Get` route answers `HEAD` (same status and headers, no body), with or without `AutoMiddleware`; it answered 405 before unless the app installed chi's `GetHead` itself. An explicit `Head` route still wins.

## [0.3.0] - 2026-10-07

Fixes found while writing and running the v0.2.0 guides. CSRF is now on by default in `AutoMiddleware`.

### Changed
- **BREAKING CHANGE**: `AutoMiddleware` installs CSRF protection when `Config.Security.CSRF` is set, which is the default. Unsafe requests (POST, PUT, PATCH, DELETE) need the token: templates read it as `.csrfToken`, and the scaffold layout sends it on every htmx request through `hx-headers`. Pages pre-rendered at startup cannot carry a token; protect their forms with `ProtectForm` and set `Security.CSRF = false`. Installing `Middleware.CSRF` a second time is now harmless.
- **BREAKING CHANGE**: `Config.Security.RateLimit` defaults to false. When set, `AutoMiddleware` adds `Middleware.RateLimit(100, time.Minute)`; the flag was never read before. `Security.CORS` now decides whether `Middleware.Default` includes the CORS middleware.
- **BREAKING CHANGE**: a route parameter beats a query parameter of the same name in template data, so `/users/7?id=99` renders user 7, matching `c.Param`.
- `NewProduction` builds from the new `config.ProductionFromEnv`, so `SECRET_KEY`, `DB_PATH`, and the other environment settings apply; it read none of them before.
- `Serve("")` serves on `Config.Server`'s host and port, and `Run` uses the configured host.

### Added
- A struct passed as template data (to `c.Fragment`, `c.View`, `Room.Swap`, ...) exposes its exported fields as top-level keys beside `.Result`, so one item template serves a `range` and a single render.
- `SSEHub.SetChannelAuthorizer` decides which `?channels=` a client may stream; a refusal is a 403 before the stream opens. Without one, every channel stays open.

### Fixed
- `hc-if` that starts false left the directives inside it unbound when it turned true.
- `NewBBoltStore` creates the directory its file lives in.

## [0.2.0] - 2026-10-07

The context-first API. Design record: `specs/CONTEXT_FIRST_API.md`.

### Changed
- **BREAKING CHANGE**: One route shape, `func(c *hyperchi.Context) error` (`Handler`). `Get`, `Post`, `Put`, `Patch`, `Delete`, `Head`, and `Options` take `(pattern, Handler, ...Middleware)`. Handlers pick the response with `c.View` (full page, or the fragment for an htmx partial request), `c.Page`, `c.Fragment`, `c.JSON`, `c.Text`, `c.HTML`, `c.Redirect`, and `c.NoContent`, chained after `c.Status`, `c.Layout`, or `c.HX()`. Renders buffer before any header is sent. This replaces rendering by writing JSON to the `ResponseWriter`, which the old `HX`/`Page` wrappers buffered and parsed back.
- **BREAKING CHANGE**: Removed the old route spellings and `(w, r)` helpers: template-parameter routes, `*WithValidation`, `PageGet`, `FragmentGet`, `GetC` and its siblings, `HX`, `Page`, `HXWithValidation`, `GetValidatedData`, `CRUD`, `Scaffold`, `Form`, `Modal`, `Turbo`, `Quick`, `JSONRoute`, `API`, `HandleRoute`, `HandleFunc`, `FuncRenderer`, `app.Func`, `app.Fragment`, `app.TemplateWithLayout`, `IntegrationRenderer`, `app.JSON`/`HTML`/`Error`/`Redirect`, `app.NotFound`/`BadRequest`/`InternalError`/`Unauthorized`/`Forbidden`, `ErrorHandlers`, `JSONWithStatus`, `DebugError`, and the `app.WS`/`WebSocketBroadcast` stubs. The old `app.WebSocket(pattern, func(any))` only answered 501; `app.WebSocket` is now a working route (see Added). `Context.Writer`/`Request`/`App` are methods now. `ResourceController`, `RESTController`, and `ContentRouteConfig` handlers take `*Context`.
- `ContentRoutes` renders with `c.View`, answers a missing item with 404, and its CRUD routes return real error statuses instead of a 200 with an error body.
- `Serve` logs localhost and each running LAN or VPN address it is reachable on, skipping container bridges.
- The scaffold builds on the Context API, sets template globals once, and serves with `app.Serve`.
- **BREAKING CHANGE**: `*HyperChi` keeps routing, rendering, lifecycle, and services; about 65 helper pass-throughs moved to where they live. Generic helpers are package functions (`app.Str` to `helpers.S`, `app.Slice` to `helpers.NewSlice`, `app.NewJSON`/`ToJSON`/`FromJSON`, `Must`, `Try`, `Catch`, `Ptr`, `IsNil`, `ReadFile`/`WriteFile`/`FileExists` to `helpers.*`). App-owned services are reached through `app.Helpers` (`app.LogInfo` to `app.Helpers.Logger.Info`, `app.EnvOr` to `app.Helpers.Env.GetOr`, `app.HTTPGet` to `app.Helpers.HTTP.Get`, timers and profiling to `app.Helpers.Profiler`, Air to `app.Helpers.Air`, deployment to `app.Helpers.Deploy`). Database wrappers moved to the `database` package with `app.DB` (`database.Paginate(app.DB, ...)`, `database.Select`, `database.NewSeed`, `database.NewBackup`, `database.SlugGenerator`). Removed stubs that did nothing: `Safe`, `EnableDBDebug`, `EnableGracefulShutdown`, and `KVStoreExample`, plus the duplicate mode switches `EnableProduction`, `EnableDebugMode`, `SetupDevelopment`, and `IsTest`.
- **BREAKING CHANGE**: one validation path per job. Request input: `hyperchi.Bind` with `validate` tags and an optional `Validate()` method. Runtime-shaped input: a `validator.Schema` registered on `app.Schemas` and checked with `c.ValidateSchema`. Removed: the in-package `Schema`, `ValidatorEngine`, field builders (`hyperchi.String()`, ...), and `app.NewSchema`; `app.Validator` (now `app.Schemas`); the unused `app.Forms`/`FormBuilder`; the `validator` package's core-coupled render helpers (`HyperChiValidator`, `ValidationErrorRenderer`, the prebuilt login/register/contact/profile middlewares), which also made `validator` import the core; and `database.Validator`, whose uniqueness check is now `database.IsTaken`. Content collections given a schema now validate items in `Create` (422) instead of storing the schema unused.
- `app.IsDevelopment` and `IsProduction` follow the configuration (`ENVIRONMENT`/`GO_ENV`) that decides development mode; they read `APP_ENV` before, which could disagree with the framework. The deployment helper's generated compose, `.env`, and systemd files set `ENVIRONMENT=production` instead of `APP_ENV`, which left generated deployments in development mode.
- `/health` reports real uptime and the configured environment, sends its JSON content type, and no longer returns database error text on a public endpoint.
- `database`: `Join`/`LeftJoin` validate the table name like `From` and panic on an unsafe one, so a table alias (`"posts p"`) is no longer accepted there. `Offset` without `Limit` emits `LIMIT -1`. `Paginate` uses the normalized page size.
- `helpers`: `Slice.Remove`/`RemoveValue` return a new slice instead of overwriting the receiver's backing array. `Truncate` counts characters, not bytes. `.env` files keep whitespace inside values, expand `$VAR` next to punctuation (`$PORT/x`), leave single-quoted values literal, and `Env.Save` quotes values that need it.
- `content`: a collection declared with `DefineCollection` keeps its schema when `LoadContent` reads its directory; files that fail the schema are left out and reported in the returned error. Unsorted queries return items ordered by ID.
- **BREAKING CHANGE**: the SQLite driver is the pure-Go `modernc.org/sqlite`; the framework no longer needs CGO, and apps build and cross-compile with `CGO_ENABLED=0`. DSN options use modernc syntax (`_pragma=name(value)`), and the `sqlite3_hyperchi` driver name is gone.
- **BREAKING CHANGE**: the CLI is its own module. Install it with `go install github.com/regiellis/hyperchi/cmd/hyperchi@latest`; web apps no longer download its dependencies. The instruction-only `hyperchi profiler` and `hyperchi deploy health` commands are removed, and `hyperchi dev` stops each server's whole process group.
- **BREAKING CHANGE**: errors are returned where a typo or failure used to pass silently. `helpers.ReadFile`/`ReadFileLines`, `Str.ToInt`/`ToFloat`/`Matches`/`FindAll`/`RegexReplace`, and `database.SlugGenerator.UniqueSlug` return errors; `app.Every`, `ScheduleJob`, and `Hourly` return `(stop func(), err error)`; `NewExtensionIntegration` and `AutoSetupKVStores` return their setup errors. The `*Quiet` file helpers, which discarded errors, are removed.
- **BREAKING CHANGE**: `app.Daily(at, task)` runs at a time of day (`"15:04"`, local time) instead of every 24 hours from registration.
- **BREAKING CHANGE**: `helpers.Str` lengths, offsets, and padding count characters instead of bytes; `Slugify` lowercases and transliterates Latin accents (`Café` to `cafe`), and `database.SlugGenerator` uses it; case converters keep acronym boundaries (`userID` to `user_id`).
- **BREAKING CHANGE**: `validator.ParseTime` accepts only unambiguous ISO layouts; slash dates need an explicit `Format`. `NumberValidator.MultipleOf` panics on a zero, NaN, or infinite step.
- **BREAKING CHANGE**: the database query and migration builders validate identifiers: `Select` accepts plain columns, `table.column`, `*`, simple aggregates, and `AS` aliases; `Table`, the column methods, and `ForeignKey` panic on unsafe names.
- **BREAKING CHANGE**: configuration drops what nothing used: `yaml` tags, the postgres/mysql DSN fields, and `Assets.Versioning` (asset versioning is now the `asset` template function). The database path is read from `DB_PATH` (as `.env.example`, the scaffold, and the deployment helper write it); `DB_NAME` still works when `DB_PATH` is unset.
- **BREAKING CHANGE**: the asset pipeline drops options it never implemented (`EnableCompress`, `EnableWebP`, `SourceMaps`, `CacheHeaders`, `PNGQuality`, `EnableResize`, `Fingerprint` (see the `asset` template function and `Middleware.Compress` under Added), the `Minify` alias) and `Optimizer.ConvertToWebP`; WebP files are validated and kept instead of converted to JPEG. The content `FileWatcher` is `NewFileWatcher(dir)` with `Scan` and a cancellable `Watch(ctx, interval, onChange)`.
- **BREAKING CHANGE**: `Profiler.EnableProfileEndpoint` is replaced by `Profiler.StatsHandler()`; timers with the same name run independently. The unused `devtools` package is removed. Framework logging writes to stderr.
- **BREAKING CHANGE**: `app.SessionMiddleware()` panics at registration when no session store is configured; it used to pass requests through with a nil session, so login and logout silently did nothing. The asset compiler rejects `.scss`, `.sass`, and `.ts` inputs with an error instead of copying them out unchanged. Unused fields and types are removed: `assets.HeroiconsRepo`, and in `content` the `CacheEnabled`, `CacheTTL`, `DefaultType`, and `Components` settings, `ComponentFunc`, `ComponentRef`, and `ParsedContent`.
- The `go` directive is the real build minimum, `go 1.26.0`, with no `toolchain` line (0.1.0 required 1.26.8). Build and deploy on the latest 1.26 patch release for standard-library security fixes.
- **BREAKING CHANGE**: APIs that did nothing or were placeholders are removed: `ContentManager.Load`/`Watch` and `Config.Content` (`CONTENT_DIR`) (load files with `app.Content.LoadDir` or the `content` package's `ContentLayer`), `UsePocketBase`, `PocketBase()`, `PocketBaseHealth()`, the `PB` field, and the in-tree `hyperchi/pocketbase` mock (use `modules/pocketbase`), `extensions.TemplateExtension` and the unescaped `Create*HTMLAttrFunc` helpers, the `wsRoom` helper (htmx 4 has no room option), `security.XSSProtection`/`SetAutoEscape`, `FileUploadConfig.UploadDir`, and the unused `patterns` fields (`SessionKey`, `FormStep.Validation`, `PollingConfig.Interval`, and six `PatternConfig` fields).
- **BREAKING CHANGE**: `CacheManager.Set` takes a `time.Duration`; the cache enforces its TTL and evicts least recently used entries past `MaxEntries` (which replaces `MaxSize`). `ToHTMLAttr` returns a whole escaped `data-anime` attribute. `security.NewAuthenticator(repo, helper)` shares the `AuthHelper` that `RequireAuth` validates with. `FileUploadConfig.MaxTotalSize` (default `MaxFiles * MaxFileSize`) caps a whole upload. The `responseTargets`, `dataAttrs`, `ariaAttrs`, and `uuid` filters return an error for invalid input. `contains` is one function: a substring test for strings and a membership test for collections. Optional validator strings accept empty input, and several pattern rules on one string all apply. An invalid `?step=` in a multi-step form is a 400.
- **BREAKING CHANGE**: the WebSocket hub relays a client's room message only from a member of that room and stamps it with the server-side identity, never the client's `user_id`.
- **BREAKING CHANGE**: `modules/performance`: `Install` registers the module as the `performance` integration, so `app.Serve` starts its job workers and `app.Close` stops them (`Module.Close` takes a context); configured rate-limit rules are applied instead of dropped. `RateLimitConfig.Rules` and `RateLimitStats.LastRequest` are removed, `JobQueueStats.Handlers` is a list, and Badger `IncrementBy`/`Batch` reject a value that is not a counter. `modules/pocketbase`: `RetryAttempts` defaults to no retries and applies to GETs only.
- **BREAKING CHANGE**: the WebSocket hub refuses a client's `join_room` message unless the app sets `SetRoomAuthorizer`, which then also checks `?rooms=` handshake rooms; server-side `JoinRoom` and `Socket.Join` are unaffected. Filters that build attribute names from arguments (`attr`, `hxOn`, `hxStatus`, `jsonAttr`, `htmxAttrs`, `inputAttrs`, `htmxButton`, `htmxForm`, `htmxLink`) return an error for an invalid name, and the element filters (`link`, `img`, `script`, `style`, `formField`, `select`, `checkbox`, `radio`, `textarea`) take their trailing attributes as `template.HTMLAttr`, so only literals the template author writes (or values Go code marks trusted) reach the markup.
- **BREAKING CHANGE**: `patterns.MultiStepForm` is a real wizard: it keeps each step's answers in the user's session (it needs `app.UseSessions` and `app.SessionMiddleware()`), validates each step with its own `Validate` (a failed step re-renders at 422 with per-field messages and the submitted values), refuses to skip steps, and hands every answer to a required `Complete` before redirecting. `ValidateFunc` is replaced by per-step `Validate` and `FormStep.Fields`; templates read `.values` and `.errors`.
- **BREAKING CHANGE**: CSRF tokens are masked per render (BREACH): `c.CSRFToken()`, `hyperchi.GetCSRFToken`, and the compatibility `middleware.GetCSRFToken` return the token XORed with a fresh random pad, so every call differs and none equals the cookie. Both validate by unmasking. `Middleware.CSRF` accepts the raw cookie value only in the `X-CSRF-Token` header; the `csrf_token` form field takes masked tokens. Production `AutoMiddleware` now gzips responses (development stays uncompressed).
- **BREAKING CHANGE**: `ContentQuery.SortBy` compares numbers numerically and dates as instants (RFC 3339, `2006-01-02 15:04:05`, `2006-01-02`), then text; mixed kinds order numbers, times, text, and items missing the field sort last in both directions. A static mount whose group or pattern holds a URL parameter is a setup error.

### Added
- Typed errors: `HTTPError`, `NotFound`, `BadRequest`, `Unauthorized`, `Forbidden`, `Conflict`, `Unprocessable`, `Errorf`, `NewHTTPError`. `DefaultError` renders JSON for API clients, the `error` template (or an escaped alert) for htmx, and an error page for browsers; a plain error is a 500 whose text never reaches the client. `app.OnError` replaces it.
- `hyperchi.Bind[T](c)` decodes JSON, form, multipart, and query input into a struct and validates `required`, `min`, `max`, `email`, `url`, and `oneof` tags plus an optional `Validate() error` method: 400 for unreadable input, 422 with per-field messages for failed checks. `Bind[map[string]any]` returns raw values. `c.ValidateSchema(name)` runs a schema registered on `app.Schemas`.
- `app.SetGlobal` for template values every render sees; handler data outranks globals, and globals outrank query parameters.
- Request accessors on `Context`: `Param`, `ParamInt`, `Query`, `QueryDefault`, `QueryInt`, `Queries`, `FormValue`, `Header`, `Cookie`, `Value`, `IsHTMX`, `Session`, `UserID`, `CSRFToken`, `Ctx`.
- Escape hatches for net/http code: `app.Handle(method, pattern, http.Handler)`, `hyperchi.WrapHTTP`, `app.ToHTTP`.
- `hyperchi new` writes `AGENTS.md` into generated projects: which framework call to use for each job.
- Route-level WebSockets: `app.WebSocket(pattern, func(s *hyperchi.Socket) error)` reads htmx 4 ws messages with `s.Receive`, validates them with `hyperchi.BindMessage[T]`, and answers with `s.Swap` (render a template for htmx to swap), `s.SendHTML`, or `s.SendJSON`. `s.Join`/`Leave` manage rooms; `app.Room(name).Swap(...)` broadcasts from any handler. Sockets share the hub with `Extensions().EnableWebSocket`, including its origin policy and server-side identity.
- `hyperchi.FieldErrors(err)` returns the per-field messages of a `Bind` or `BindMessage` failure, for re-rendering a form.
- `hyperchi.JSONErrors` middleware makes a route, group, or app answer every error as JSON whatever the `Accept` header says. `RESTful` routes and the `ContentRoutes` CRUD routes use it automatically.
- `c.Empty()` sends a status and headers with no body; `c.HX()` chains can end in `HTML` or `JSON`.
- `hyperchi.ClientIP(r)` and `c.ClientIP()` return the address a chi `ClientIPFrom*` middleware (such as `Middleware.ClientIP`) declared, or the TCP peer.
- Router misses, static misses, middleware denials, and recovered panics render through the same pipeline as a returned error (`DefaultError` or `OnError`), which replaces 0.1.0's `SetErrorHandler`. `PreRender` returns a `Handler`.
- `HyperChi.config.sanitizeHTML`: an opt-in hook that `hc-html` runs before assigning markup. The directives evaluate expressions with `new Function`, so a Content-Security-Policy must allow `'unsafe-eval'` where they are used.
- `c.Blob(contentType, body)` sends a body with any content type (Markdown, CSV, feeds, downloads); `c.Text` and `c.HTML` fix their own.
- `modules/markdown`: an optional goldmark (v1.8.6) module with GitHub Flavored Markdown, heading IDs, and flat front matter, returning safe HTML, raw Markdown, headings, and plain text; it also registers as an integration for `c.RenderWith`.
- The documentation site (the showcase) is written in Markdown and serves `/llms.txt`, `/llms-full.txt`, and each page as `/docs/<topic>.md`; every code block has a copy button; each page has Copy page, View as Markdown, Open in Claude, and Open in ChatGPT actions; and `/docs/search` searches every page through htmx.
- `app.LoadTemplatesFS(fsys, pattern)` loads templates from an `fs.FS` (for example a `go:embed` directory) with the same `**` and naming rules as `LoadTemplates`, so an app no longer has to run from its own directory.
- `WebSocketConnection.Flush(timeout)`; a returning `SocketHandler`'s queued messages are now flushed before the connection closes.
- `app.StaticFS(pattern, fsys, options)` and `NewStaticFSHandler` serve files from an `fs.FS` (an embedded directory via `fs.Sub`) with the rules of `StaticWithOptions`: no directories or dotfiles, cache headers, `nosniff`, and a content-hash `ETag` for files without a modification time. Together with `LoadTemplatesFS`, an app can ship as one binary.
- `app.ProtectForm(options)`: `ProtectForm` with the app's error pipeline as the default `OnError`, so a refused form renders like every other denial.
- `database.IsTakenContext(ctx, ...)`, bound to the request's context.
- `Database.LogQueries` (`DB_LOG_QUERIES=true`) turns on statement logging; it no longer follows `Development.Debug`.
- `database.GetStructContext` and `ListStructContext`, bound to the request's context.
- `modules/gomponents`: an optional module wrapping `maragu.dev/gomponents` v1.3.0. It registers as an integration for `c.RenderWith`, renders a node to `template.HTML` for mixing with templates, and provides typed, escaped helpers for htmx (`hx-*`, `hx-sse:*`) and `hc-*` attributes.
- Four example apps, each its own module with tests, linked from the docs site's Example apps page: `examples/dice-arena` (a multiplayer dice game over two-way WebSockets), `examples/studio` (a game studio's Markdown-driven site), `examples/mailgrid` (an internal letters tool on gomponents with SSE job progress and an outbox), and `examples/homelab-search` (SQLite FTS5 search over a document folder).
- `app.RespondStatus(w, r, code, err)` answers a refusal from net/http middleware (a module's rate limiter, an app's own checks) through the error pipeline.
- Asset versioning: the `asset` template function and `app.AssetURL` turn a static path into a content-hashed URL (`/static/app.css?v=...`); requests with the current version get `Cache-Control: public, max-age=31536000, immutable`, development re-hashes edited files, and an unknown path fails the render.
- `app.Middleware.Compress()` and `CompressWithOptions`: standard-library gzip that leaves SSE streams, WebSocket upgrades, HEAD, small bodies, and already-compressed media alone.
- Content loading: `app.Content.LoadDir(dir)` and `LoadFS(fsys, root)` load Markdown, HTML, and text collections into `app.Content`, served by `Get`, `Query`, and `ContentRoutes`; a `*ContentLoadError` lists every file left out while valid files load. In development `LoadDir` reloads changed files atomically, and `app.Close` stops the watcher. `WithMarkdown` plugs in a full renderer such as `modules/markdown`.
- Application secret: `Security.SecretKey` (`SECRET_KEY`, at least 32 bytes) with `SecretKeyPrevious` (`SECRET_KEY_PREVIOUS`) for rotation, never serialized or logged. Development without a key uses a per-process key; production without one starts but every signing call returns `ErrNoSecretKey`.
- `c.SetSignedCookie` and `c.SignedCookie(name, maxAge)`: HMAC-signed cookies bound to their name, checked in constant time against every key, with typed errors for missing, invalid, expired, and too-large values.
- Flash messages: `c.Flash(kind, message)` for the next request, `c.Flashes()` to read them once, and `.flashes` in every template; stored in the KV session when there is one, otherwise in a signed, size-bounded cookie.
- Uploads: `security.SaveUpload` and `UploadValidationResult.SaveAll` store files through `os.Root` under random names, with the extension taken from the sniffed content (never the client's filename), refusing to overwrite, at mode 0640, enforcing the size limit while writing, and removing partial files. `patterns.SchemaStep` adapts a validator schema to a wizard step.

### Fixed
- `hyperchi.New()` no longer creates `hyperchi.db` in the working directory at startup. The default database opens lazily (`database.DBConfig.Lazy`), so the file appears on the first query; apps and tests that never use the database leave nothing behind. The page-size and auto-vacuum settings moved into the connection hook so a lazily created database still gets them.
- `database`: Get, GetMap, GetStruct, and their Tx versions returned `ErrNoRows` when the query failed on its first row, turning server errors into 404s. Migration `Default` strings are quote-escaped (a crafted default could inject SQL). `CommonMigrations` and `Index()` use `CREATE INDEX IF NOT EXISTS`, so migrations re-run on boot. A failed commit no longer logs a spurious rollback error.
- `helpers`: loading a `.env` file that used `${VAR}` deadlocked. `WithAuth` sends real base64 Basic credentials. `Try`/`Catch` keep the original error for `errors.Is`. `Must`, `Info`, and the other package-level loggers report the caller's line. Logger writes are synchronized and `WithFields` output is sorted. The profiler middleware passes `Flush`/`Hijack` through, so SSE and WebSocket work under `StartProfiler()`, and no longer reports negative memory or races on enable/reset. Negative counts in `Repeat`, `Times`, `Left`, `Right`, and `Truncate` no longer panic; `Unique` accepts unhashable elements. The generated `.air.toml` is valid TOML, and `SwitchToProduction` reads `.env.production` from the project root.
- `content`: front matter keeps quoted values as strings and ends only at a `---` line; lists, indented items, and unclosed code fences render correctly; content IDs no longer include the base path when it has a trailing slash; numeric sorts compare numbers; float fields accept whole numbers and min/max checks apply across int and float; length rules count characters; nil slice or map fields no longer panic in validation; string lists from front matter satisfy `GetSlice`.
- The WebSocket hub no longer logs the expected errors of tearing down a closed connection (`use of closed network connection`, `EOF`) as close errors; unexpected close failures are still logged.
- The hot-reload endpoint is announced with the listen addresses when the server starts, instead of when the first route is registered (which printed it for every app a test built).
- `config.FromEnv` ignored `DB_PATH`, the variable every generated deployment sets, so deployed databases were written outside their volume.
- The production preset configured asset minification that the pipeline never applied.
- Content: front matter dates parse into time-typed fields, the collection type is the majority of its files, and the file watcher reloads, stops, and reports walk errors.
- Assets: the watcher no longer skips every path containing `tmp` (all `.tmpl` files), image output lands in one layout, SVGs are minified by `tdewolff/minify` instead of a hand-written stripper that rewrote text content, and the hot-reload socket rejects cross-origin handshakes.
- Validator: NaN and infinite numbers fail range checks, length rules count characters, the bundled template snippets execute, and the form middleware skips `HEAD` and `OPTIONS`.
- Helpers: logger and error-handler setters are safe for concurrent use; `.env` inline comments are stripped, `.env` files are written with mode 0600, JSON number getters accept every integer type, and the generated `health.go`, Dockerfile (pinned `alpine:3.24`, CGO off), and Air install path are correct.
- `KVRegistry.Get` errors match `ErrStoreNotFound`; versions reported by `/health`, logging, and the asset manifest come from build info instead of a hardcoded `2.0.0`.
- CSRF denials from `Middleware.CSRF` render through the error pipeline (`OnError`/`DefaultError`: JSON, htmx fragment, or page) with `no-store`, instead of a plain-text `http.Error`. A same-status `*HTTPError` keeps its message.
- The `min`, `max`, and `avg` template filters skip non-numeric values instead of counting them as zero.
- `logging`'s package default logger is safe for concurrent use.
- The SSE and WebSocket template helpers (`sseConnect`, `sseSwap`, `sseClose`, `wsConnect`, `wsSend`, `wsClose`) were registered only on the extension registry, so any template using them failed to load. Every app now has them; they render attributes for tag position with their values HTML-escaped, reject unsafe event names, and `wsClose`'s selector is valid CSS.
- The asset bundler verifies each Tailwind and htmx download against a pinned SHA-256 and writes nothing on a mismatch; it now fetches the files published in the npm packages instead of CDN-generated minified copies, which can change.
- The deployment helper's Dockerfile no longer copies `.env.production` into the image, copies only the asset directories the project has (`templates`, `static`, `public`, `dist`), and is paired with a `.dockerignore` that keeps `.env` files, `.git`, and databases out of the build context. The generated compose and `.env.production` set `PORT`, which the framework reads, instead of `APP_PORT`, and `dev.sh` points at `hyperchi air init`.
- The `eq` and `ne` template functions, which replace the builtins, compared with `reflect.DeepEqual`, so a named string type never equaled its literal (`{{if eq .Status "open"}}`) and `eq` took only two arguments. They now compare basic kinds by value like the builtins, accept several candidates, and also compare integers with floats.
- A WebSocket swap whose template rendered nothing was sent without its `content` field, which the htmx ws extension treats as data, so the target was never cleared. Messages with a target always carry `content`.
- A `SocketHandler` that sent a message and returned could lose it: the connection closed before the writer ran. The hub's writer no longer logs a page closing mid-write as an error.
- `Middleware.CSRF()` minted a new token on every safe request, so a page's own asset and htmx GETs replaced the cookie under the token rendered into its forms and every submit was refused. A well-formed token is now reused; a missing or malformed one is replaced.
- SSE streams were cut whenever the server's `WriteTimeout` elapsed (10 seconds by default); the handler now lifts the write deadline for the life of the stream. The hub's periodic sweep pings every connection before culling, so a live client is no longer dropped after two minutes, and channels are subscribed before the `connected` event is sent.
- `app.BasicAuth` compares credentials in constant time, no longer logs the username, refuses through the error pipeline (with `WWW-Authenticate`), and panics at registration on an empty username or password.
- Development mode no longer logs every SQL statement with its arguments by default.
- The content `FileWatcher` skips hidden entries (`.git`, `.DS_Store`) and `node_modules`.
- `database.Paginate` orders by `rowid`; without an `ORDER BY`, SQLite returned rows in index order, so pages could repeat or skip rows.
- `Bind` caps a multipart body at 32 MiB and answers a larger one with 413; parts past the in-memory limit used to spill to disk without bound.
- `Middleware.CSRF()` reads the `csrf_token` field from multipart forms (`ParseForm` alone never reads a multipart body), so no-JS upload forms pass. Database row readers report a `rows.Close` failure instead of discarding it.
- Security: `hxConfig`, `hxVals`, `hxHeaders`, and `HTMXAttrs.Config` wrote JSON into single-quoted attributes without escaping `'`; they now render escaped, double-quoted JSON. `dataAttrs`, `ariaAttrs`, and `responseTargets` reject unsafe attribute names. `AnimeData` and `ToHTMLAttr` escape their values.
- Security: `Errorf` put the wrapped (`%w`) cause into the client-visible message; the message now omits it and the cause stays available to logs through `Unwrap`. Development error reports redact credential headers (`Authorization`, `Cookie`, and any header naming a token, key, secret, or session).
- Security: `SanitizeInput` matched only lowercase patterns and could be rebuilt from nested input; it now ignores case and repeats until clean. Its documentation says plainly that it is not an XSS defense. The `uuid` filter uses `crypto/rand`.
- `TokenHXHeaders` emits `hx-headers:inherited`, which htmx 4 needs for child requests, and both CSRF helpers use the configured field and header names. Upload errors are classified by type, so only size limits answer 413 and a malformed body is 400.
- `HTTPServiceCheck` no longer races under concurrent checks. `KVSession.GetInt` reads values set in the same request, and the bbolt and memory stores encode counters the same way (old bbolt counters still read). `Production()` really turns the debug endpoints off. `ContentQuery` sorting by metadata was wrong and is now stable. `Slice.Union` no longer writes into the receiver's backing array, and `jsonGet` treats a negative index as missing instead of panicking. Pattern errors render through the app's error pipeline. `logging.Config.TimeFormat` takes effect.
- `modules/performance`: the job queue's workers leaked (nothing stopped them); the Badger `Backup` wrote nothing (it now writes a real file, and `Restore` loads one) and its closed flag raced; `PerUser` rate limiting shared one bucket across all users; the token bucket never refilled under steady traffic and refused everything with `Burst` 0; job `Priority` was ignored, the backoff labeled exponential was linear, and the queue and Badger stats were always zero; store errors were ignored throughout.
- `modules/performance`: an installed rate limiter refuses through the app's error pipeline (`app.RespondStatus`), job IDs come from `crypto/rand` instead of colliding under concurrent pushes, `CleanupExpired` keeps counters until their own window ends, and the Badger store implements the framework's KV interfaces (sessions and cache can run on it) with counters in the framework's JSON encoding. The module has a README.
- `modules/performance` (later fixes): `Workers` 0 means the default instead of turning Badger compaction off, a duplicate job ID returns `ErrJobExists` instead of overwriting the job, the Badger store's `GetTTL` follows the framework contract, Badger logs go through `slog` at warn and above, and Badger is upgraded to v4.9.6. The module's `AdvancedKVStore` is renamed `TTLStore`.
- `modules/pocketbase`: `Record` encoded without its fields, and collection names and record IDs were placed in URL paths unescaped (a crafted ID could reach another collection); both are fixed, and `.`/`..` segments are rejected.
- Security: filters escaped attribute names with `html.EscapeString`, which does not stop a name like `x onclick=alert(1)`, and `jsonAttr` wrote its name raw; names are now validated. The element filters spliced a trailing attribute string from any source into markup. Development error reports redact sensitive query parameter values as well as headers.
- bbolt `CountPrefix` counted TTL markers and expired keys, and `DeletePrefix("user:1:")` deleted the TTL marker of the key `user:1`, making it permanent. `ContentManager` cache keys could collide across collections (`a` + `b:c` against `a:b` + `c`), and replacing a collection kept its cached items.
- KV session cookies were sent with no SameSite attribute (an unset field is 0, not `SameSiteDefaultMode`); they now default to Strict as documented. A handler that wrote nothing (net/http's implicit 200) lost its session changes; the session is now saved after the handler returns. Sanitizing an upload's filename no longer panics on a very long extension.
- Front matter lines that were not `key: value` were dropped and a repeated key overwrote the first; both now fail the file with the line named. A file that failed to parse or render inside a schema-checked collection was reported as a missing required field instead of its own error.
- A required validator field ignored its custom `Message` when the value was missing or blank. A production app without `SECRET_KEY` logged a warning at startup even if it never signs; it now logs on the first signing attempt.
- A static mount inside `app.Group` answered 404 for every file, because the handler stripped only its own pattern; it now serves under the full group prefix, and `AssetURL` resolves it there.

### Migrating from 0.1.0
| Before | After |
| --- | --- |
| `app.Get("/x", "tpl", func(w, r) { hyperchi.WriteJSON(w, data) })` | `app.Get("/x", func(c *hyperchi.Context) error { return c.View("tpl", data) })` |
| `app.PageGet(...)` / `app.FragmentGet(...)` | `c.Page(...)` / `c.Fragment(...)` in the handler |
| `app.GetC(pattern, h)` | `app.Get(pattern, h)` |
| `app.PostWithValidation(p, tpl, schema, h)` | `data, err := c.ValidateSchema(schema)` in the handler, or `hyperchi.Bind[T](c)` |
| `http.Error(w, msg, 404)` / `app.Error(w, r, 404, msg)` | `return hyperchi.NotFound(msg)` |
| `hyperchi.SetHXTrigger(w, "x")` | `c.HX().Trigger("x")` |
| `app.HandleFunc(p, h)` | `app.Handle(http.MethodGet, p, h)`, or a `Handler` |
| `app.SetErrorHandler(func(w, r, code, err) { ... })` | `app.OnError(func(c *hyperchi.Context, err error) { ... })`; the status is on `*hyperchi.HTTPError` |
| `h, _ := app.PreRender(...); app.Router().Get(p, h)` | `app.Get(p, h)`: `PreRender` returns a `Handler` |
| `go install github.com/regiellis/hyperchi/cmd@...` | `go install github.com/regiellis/hyperchi/cmd/hyperchi@latest` |
| `CGO_ENABLED=1` and `gcc`/`sqlite-dev` in the Dockerfile | `CGO_ENABLED=0`; no C toolchain |
| `s := helpers.ReadFile(p)` | `s, err := helpers.ReadFile(p)`, or `helpers.ReadFileOr(p, fallback)` |
| `app.Every("5m", task)` | `stop, err := app.Every("5m", task)` |
| `app.Daily(task)` (every 24h from start) | `stop, err := app.Daily("03:00", task)` |
| `cfg.Security.SecretKey = ...` | set `SECRET_KEY` (32+ bytes); it now signs cookies and flash messages |
| tests or script that compare `c.CSRFToken()` with the cookie, or post the cookie as `csrf_token` | post the rendered token, or send the cookie value in the `X-CSRF-Token` header |

Templates now receive handler data as Go values instead of through a JSON round trip, so a
struct's fields are read by their Go names (`.ID`, `.Description`), not by their `json` tags
(`.id`, `.description`). The compiler cannot see templates: render each page once after
migrating, or pass `hyperchi.H` maps with the keys the templates expect.

## [0.1.0] - 2026-10-05

### Production and site APIs

- Application configuration controls development behavior, security headers, error
  visibility, cookie security, and WebSocket development origin policy. Environment
  loading accepts `ENVIRONMENT`, then `GO_ENV`, then the legacy `PRODUCTION=true`.
- `NewWithConfigE` returns database initialization failures. Set
  `Database.Disabled` for applications that do not need persistence. SQLite startup
  and busy timeouts honor `Database.Timeout` on every pooled connection.
- Layout execution failures return errors in every environment. Explicit missing
  layouts fail. `SetErrorHandler` handles rendering errors, routing errors, and
  recovery; committed responses are not replaced, and abort panics propagate.
- Client-IP middleware preserves the transport peer and requires configured trusted
  proxies. Core and compatibility rate limiters share bounded token buckets,
  configurable keys, custom denial responses, and `Retry-After`.
- Confined static serving rejects directories, dotfiles, unsafe paths, and methods
  other than GET/HEAD. `StaticWithOptions` returns startup errors and accepts cache
  policies and custom errors. `Static` and `FileServer` panic on invalid setup.
- `PreRender` and `PreRenderedPage` validate complete documents at startup and serve
  immutable HTML with conditional requests, HEAD, and non-cacheable error pages.
- `ProtectForm` composes browser-origin protection, bounded URL-encoded parsing,
  and an optional rate limiter. Application handlers retain domain validation.
- Servers have configurable header deadlines, header-size limits, shutdown budgets,
  and context-controlled lifecycles. Production binds the exact requested port.
  Development port fallback is opt-in.
- `hyperchi new --template site` creates a Go-only site with a strict CSP and no
  initialized database. Both generated templates use the framework server lifecycle.
- Go minimum is 1.26.8. Dependency baseline matches the tested consuming app:
  chi 5.3.2, x/crypto 0.57.0, x/image 0.46.0, and x/sys 0.48.0.
- CI runs race tests, builds and smoke-tests generated projects, and scans current
  Go vulnerability advisories. `task release:check` runs the publication gates.

See [production migration](docs/PRODUCTION.md) for behavior changes and examples.

### Overview
Initial public release of HyperChi, an HTMX framework for Go. This release marks the transition from Gin to Chi router and establishes HyperChi as a content-driven framework optimized for building blogs, documentation sites, podcasts, and other content-heavy applications.

### Added
- **Content Management System**: Built-in filesystem-based content loader with markdown/MDX/HTML support
- **Template System**: Comprehensive template system with layouts, components, and fragments (Astro-inspired islands)
- **100+ Template Filters**: Extensive filter library for strings, dates, collections, HTML, and JSON
- **HTMX Integration**: Complete HTMX attribute support with template helpers
- **Server-Sent Events (SSE)**: Full SSE implementation for real-time updates
- **WebSocket Support**: Built-in WebSocket hub with room management
- **Advanced Patterns**: Live feeds, progress bars, multi-step forms, infinite scroll, live search, auto-save
- **Database Layer**: SQLite-first database integration with type-safe generics
- **Security Features**: XSS protection, CSRF middleware, secure file upload validation, session-based authentication
- **Validation System**: Type-safe form validation with HTMX integration
- **Helper Libraries**: Extensive helpers for strings, collections, logging, error handling, JSON
- **Development Tools**: Air integration for hot reload, profiling, deployment helpers
- **Health Checks**: Built-in health check endpoints

### Added (template and database API)
- `HyperChi.RenderToString(name, data) (template.HTML, error)` renders a loaded template and returns its HTML. This is what a block or component system needs to compose a page out of parts without holding the `*template.Template` itself.
- `database.ErrNoRows` re-exports `sql.ErrNoRows`, so callers can check for a missing row without importing `database/sql`. The value is identical, so `errors.Is` works against either.

### Changed
- **BREAKING CHANGE**: Switched from Gin router to Chi router as the core HTTP router
- Updated all documentation, examples, and comments to reflect Chi usage
- Modified handler signatures to use standard `http.HandlerFunc` instead of `gin.HandlerFunc`
- Updated API examples to use `hyperchi.GetParam()` instead of `gin.Context.Param()`
- Updated response writing to use `hyperchi.WriteJSON()` instead of `gin.Context.JSON()`
- Environment detection now uses `ENVIRONMENT` or `GO_ENV` instead of `GIN_MODE`
- Improved logging with format string safety

### Fixed
- `patterns.MultiStepForm` answers a step that fails validation with 422 instead of 200, matching `Bind` and `ValidateSchema`. The re-rendered step and the `showValidationError` trigger are unchanged; htmx 4 swaps 422 responses and fires `HX-Trigger` on them. A failed response write is now logged instead of discarded.
- The process environment now beats `.env` files: `PORT=9000 go run .` is honored even when `.env` sets `PORT`. Lookup order is values set in code with `Env.Set`, then the process environment, then `.env`; `EnvConfig.OverrideSystemEnv` lets `.env` win, as documented. Previously `.env` always won, contradicting `OverrideSystemEnv`'s default of false.
- `LoadTemplates` now treats `**` as a recursive match. Go's `filepath.Glob` has no `**` operator and quietly degraded it to a single `*`, so templates nested more than one directory deep were dropped with no error. The scaffold ships exactly that pattern.
- `LoadTemplates` returns an error when a pattern matches no files, instead of leaving an empty template set that fails later at render.
- Template names resolve with an extension fallback. `hx.Template("index")` now finds `index.tmpl` whether or not the file carries an explicit `{{define}}`. A failed lookup lists the loaded names.
- Compilation warnings in logging helpers (format string safety)
- Test failures related to environment detection
- XSS test expectations for safe HTML tags in content-driven apps

### Security
- Client IPs can no longer be forged with forwarding headers anywhere in the framework. The production hardening already removed `middleware.RealIP` and made the rate limiters walk `X-Forwarded-For` from the right; this also fixes the remaining readers: `security`'s audit IP walks the chain from the right instead of taking the client-controlled leftmost entry, request logs use the address `Middleware.ClientIP` declared (or the TCP peer) instead of trusting headers, and the performance module's limiter no longer keys buckets on `X-Forwarded-For`.
- XSS protection with automatic HTML escaping
- CSRF token support via middleware
- Secure file upload validation
- Session-based authentication helpers
- Input validation and sanitization

### Documentation
- Comprehensive README with examples
- HTMX integration guide
- Fragments and layouts documentation
- Error handling patterns
- Security best practices

### Migration Guide from Pre-1.0
To migrate from Gin-based HyperChi to Chi-based HyperChi:

#### Router Setup
**Before:**
```go
r := gin.Default()
```

**After:**
```go
r := chi.NewRouter()
```

#### Handler Functions
**Before:**
```go
func handler(c *gin.Context) {
    user := getUserByID(c.Param("id"))
    c.JSON(200, gin.H{"user": user})
}
```

**After:**
```go
func handler(w http.ResponseWriter, r *http.Request) {
    user := getUserByID(hyperchi.GetParam(r, "id"))
    hyperchi.WriteJSON(w, hyperchi.H{"user": user})
}
```

#### Route Registration
**Before:**
```go
r.GET("/users/:id", hx.HX(renderer), handler)
```

**After:**
```go
r.Get("/users/{id}", hx.HX(renderer, handler))
```

### Rationale
- Chi router provides better performance and smaller memory footprint
- Chi has a simpler, more focused API surface
- Chi follows standard library patterns more closely
- Chi has excellent middleware ecosystem compatibility
- Chi is more lightweight and has fewer dependencies