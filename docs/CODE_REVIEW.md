# HyperChi Codebase Review

## October 2026: release pass

- Graceful shutdown could stall for five seconds on a connection that had not sent a request
  (a browser preconnect, an HTTP client's spare dial): `http.Server.Shutdown` counts a
  `StateNew` connection as active until it is five seconds old. `GracefulServer` now tracks
  those through `ConnState` and closes them from a `RegisterOnShutdown` hook
  (`TestShutdownClosesConnectionsWithoutRequest`). It showed up as an intermittent
  `TestGeneratedProjectsBuild` failure, whose shutdown limit is also five seconds. Deliberate
  limit kept: replacing the server's `ConnState` through `GetServer` brings the wait back.

## October 2026: Built with page

- The template functions `or` and `and` returned a bool, so `{{or .x "default"}}` printed
  `true`. They replace text/template's builtins in every app's function map, and the builtins
  return an argument. Both now do (`TestAndOrReturnArgumentsLikeTheBuiltins`). Deliberate
  limit kept: as plain functions they evaluate every argument, where the builtins stop early.

## October 2026: dependency pass findings

Checking the dependency bumps on 2026-10-13 turned these up; each is fixed.

- `TestCSRFMaskedTokens` failed about one run in five. Its tamper step changed the last
  base64 character, which carries two padding bits, so some changes decoded to the same
  token and the request was accepted. The test now changes the first character. The
  middleware was correct: a second encoding of a token the client already holds is no
  forgery.
- `task lint` reported 9 issues in `analytics`, `assets`, `deployment`, and `publishing`:
  unchecked `Close`, `os.Remove`, and `os.RemoveAll` results, and one negated compound
  condition. `deployment.Build` and `writeImageAtomic` now return a failed close or a failed
  stage cleanup. `Build` closes the project root before the rename, so an error still means
  no output was written. `golangci-lint` at 0 issues is the check.

## October 2026: docs pass findings

Writing and running the v0.2.0 guides turned these up; each is fixed with a test.

- `hc-if` that started false marked its descendants processed with no scope, so they never
  bound when it showed (`frontend/tests/directives-if.test.mjs`).
- Template data let `?id=` replace the route's `id` (`TestExtractContextRouteParamBeatsQuery`).
- A struct passed to `c.Fragment` or `Room.Swap` reached templates only as `.Result`, so an
  item template rendered empty fields (`TestStructDataFieldsReachTemplate`).
- SSE `?channels=` had no authorization hook (`TestChannelAuthorizer`).
- `Security.CSRF`, `RateLimit`, and `CORS` were never read; `AutoMiddleware` now honors them,
  with CSRF on and rate limiting off by default (`middleware_flags_test.go`).
- `NewProduction` ignored the environment, `Serve("")` ignored `Config.Server`, and
  `NewBBoltStore` failed on a missing directory (`config_gaps_test.go`).
- Deliberate limit kept: route sockets (`app.WebSocket`) ignore `?rooms=` and
  `SetReceiveOnly`, because the handler owns reads and joins.

## October 2026: pre-merge pass

A second review before merging the v0.2.0 line found these; each is fixed with a test.

- Security: client `join_room` joined any room; it is refused unless the app sets
  `SetRoomAuthorizer`. Attribute-name filters escaped names instead of validating them, and
  the element filters spliced trailing attribute strings from any source (now
  `template.HTMLAttr`). CSRF tokens rendered unmasked, so gzip would leak them (BREACH);
  both CSRF middlewares now mask per render, and production `AutoMiddleware` compresses.
- Sessions: cookies went out with no SameSite (an unset field is 0, not
  `SameSiteDefaultMode`), and a handler that wrote nothing lost its session changes.
- `ContentManager` cache keys collided across collections; `ContentQuery.SortBy` compared
  numbers and dates as text; static mounts inside `app.Group` answered 404.
- Stubs the first closure deleted were standard features, rebuilt for real: content-hashed
  asset URLs, gzip, content directory loading, `SECRET_KEY` with signed cookies and flash
  messages, session-backed multi-step forms, and `security.SaveUpload`.

## October 2026: review closure (v0.2.0 line)

Every item in §6 to §10 below is resolved on `feat/context-first-api`, each with a
regression test; the headings carry the marks. The larger changes:

- SQLite is pure Go (`modernc.org/sqlite` v1.60.1). The framework, the scaffold, and the
  generated Dockerfile build with `CGO_ENABLED=0`, and CI builds that way.
- The CLI is its own module (`cmd/hyperchi`), so web apps no longer pull cobra or lipgloss.
  The `go` directive is the real minimum, 1.26.0, with no `toolchain` line. (bbolt's own
  go.mod still names cobra, so it appears in the module graph but is never built.)
- `hyperchi.go` is split into subsystem files (A1); every `*HyperChi` helper pass-through
  was removed earlier in the 0.2.0 line.
- Runtime failures return errors (A5, A8): scheduling, extension and KV setup, file and
  string helpers. Library code no longer prints to stdout.
- Security, found during the closure: the SSE/WebSocket template helpers now escape their
  values (they were also never registered with templates); the hot-reload socket checks the
  origin; the generated Dockerfile no longer bakes `.env.production` into the image; asset
  downloads are pinned by SHA-256; the unused `devtools` package, whose error page echoed
  Cookie and Authorization headers, is removed; CSRF denials render through the error
  pipeline.
- `config.FromEnv` now reads `DB_PATH`, which every generated deployment sets; it read only
  `DB_NAME`, so deployed databases landed outside their volume.

Still open from this review: nothing. One convention sweep remains: a few `_ =` and
`x, _ :=` sites predate the no-ignored-errors rule (`kv_bbolt.go` dropping an `Update`
error, `patterns` treating an unparsable `step` as 0, `errors.go` ignoring `os.Getwd`,
`helpers/json_http.go` body closes, a websocket policy close). Writes to a client that may
have disconnected are commented and deliberate. Left as deliberate limits, not defects: the
directive runtime evaluates expressions with `new Function` (CSP needs `'unsafe-eval'`;
`HyperChi.config.sanitizeHTML` is an opt-in hook for `hc-html`), the embedded copy in
`cmd/hyperchi/assets/` is synced by hand with `task sync-cli-assets`, and `filters`,
`pocketbase`, and `cmd/hyperchi` remain below 45% coverage.

## October 2026 production pass

The Playlogic integration now drives regressions for configuration isolation,
initialization failures, layout errors, spoofed forwarding headers, static root
confinement, conditional documents, form denials, and graceful shutdown. The core
and compatibility rate-limit APIs share the same bounded token-bucket implementation.
Both CLI templates are generated, compiled, served over HTTP, and stopped in tests.
See [PRODUCTION.md](PRODUCTION.md) for migration details and [RELEASING.md](RELEASING.md)
for the publication gates. This pass does not close the unrelated structural,
validation, or asset-pipeline findings below. It shipped as v0.1.0 and was merged
into the context-first line, where its error hooks route through `DefaultError`/`OnError`.

## October 2026: context-first API

A9 and the route-spelling item in A10 are resolved by `specs/CONTEXT_FIRST_API.md`. Every
route is a `Handler` (`func(c *Context) error`) that picks its response explicitly, so the
JSON body-sniffing `HX`/`Page` wrappers and `responseCapture` are gone, along with the extra
route spellings and the app-level `(w, r)` helpers. Regression coverage lives in
`hyperchi/context_api_test.go` and the runnable examples in `hyperchi/example_test.go`.
The same pass replaced `middleware.RealIP` with `ClientIPFromRemoteAddr` and made every
client-IP reader (both rate limiters, the security and logging helpers) resist forged
forwarding headers; see `hyperchi/clientip_test.go`. `golangci-lint` reports no issues.

## October 2026: coverage and CI gates

CI now runs `golangci-lint` (v2.12.2) and gates total statement coverage with
`scripts/coverage-floor.sh` (floor 52%, measured 54.0% on 2026-10-05; `task cover` runs the
same gate locally). Tests written for `content`, `helpers`, and `database` found and fixed 42
defects; each fix has a regression test. The ones with the widest reach:

- `database`: a query that failed while reading its first row returned `ErrNoRows`, so a
  server error became a 404. `Join`/`LeftJoin` skipped the identifier check `From` applies.
  Migration `Default` values were not quote-escaped (SQL injection through a crafted
  default). `CommonMigrations` failed on the second boot (`CREATE INDEX` without
  `IF NOT EXISTS`). `Offset` without `Limit` produced SQL SQLite rejects.
- `helpers`: loading a `.env` file that used `${VAR}` deadlocked (read lock taken under the
  write lock). `WithAuth` sent undecodable Basic credentials. `Try`/`Catch` lost the wrapped
  error. `Remove` mutated the caller's slice. The profiler middleware hid `Flush`/`Hijack`, so
  SSE and WebSocket broke under `StartProfiler()`. Concurrent logger writes raced. The
  generated `.air.toml` was invalid TOML.
- `content`: front matter coerced quoted values, a query with no sort paged in map order
  (duplicates and gaps), mixed int/float fields sorted as text, nil slice/map fields panicked
  in validation, and `LoadContent` replaced a declared collection, dropping its schema.

The items once listed here as still open are resolved; see the review closure above.

## September 2026 follow-up fixes

The 2026-09-16 review identified fourteen additional defects. The fixes below
preserve the public APIs except for additive KV capabilities and stricter upload
authentication. The June review and its prior status updates follow this section.

| Area | Corrected behavior |
| --- | --- |
| Session logout | Destruction marks the middleware-owned session terminal, preventing response-time persistence from restoring it. |
| Upload authentication | `RequireAuth` checks a verified user in context, populated by authentication middleware, rather than bearer-token syntax. |
| Transaction SQL | Transactional CRUD validates identifiers using the same allowlist as direct database operations. |
| Job queue | Built-in KV stores atomically consume pending references so competing workers cannot claim the same job. |
| Rate limits | Fixed/sliding window counters update their value and expiry atomically on supported stores. |
| SSE | Per-connection locking serializes writes and flushes; handler teardown waits for an active writer. |
| Token expiry | `AuthHelper` rejects expired records even when a custom session store retains them. |
| PocketBase | Record decoding preserves flattened custom fields in `Record.Data`. |
| Unique arrays | Maps and nested arrays are compared safely instead of being used as unhashable map keys. |
| Content filters | Slice filters reach containment handling without an interface-comparison panic. |
| Frontend build | Vite runs before the library build, so clearing `dist` cannot delete the directives bundles. |
| Render status | Superseded 2026-10-05: `Context` responses honor `c.Status` and send no body for `204`, `205`, `304`, or `HEAD`. |
| BBolt expiry | Replacing or deleting a value also clears its old expiry metadata. |
| Cache bytes | Byte entries carry a type marker so `GetBytes` restores the original bytes without decoding ordinary strings. |

Custom KV implementations should implement `AtomicTakeStore` and `AtomicTTLStore`
to coordinate multiple queue or rate-limiter instances. The compatibility fallback
serializes operations within one instance only. Queue claims are destructive and
do not provide crash recovery or exactly-once processing across process failures.
Token buckets require one limiter instance per backend and key prefix.

Cache entries written before the byte-type marker remain readable with their old
interpretation. Invalidate and repopulate existing byte-valued entries to restore
their type; old base64 strings cannot safely be distinguished from ordinary strings.

Verification includes regression tests for the thirteen Go defects, the root
race-enabled suite, separate optional-module tests, and a frontend production build
with checks that both directives bundles and Vite's JS/CSS outputs exist.
Root and PocketBase lint pass. The PocketBase follow-up reports response-body
close errors, authenticates superusers through the v0.23+ endpoint, and fetches
every page in `GetFullList`. Its README documents the supported client API.

## June 2026 review

**Date:** 2026-06-10
**Scope:** Full framework review — security, concurrency, architecture, validators/templates/frontend, testing/tooling
**State reviewed:** working tree on branch `fix/review-findings` (includes uncommitted changes)
**Methods:** manual code review by 5 specialized review passes, `go build`, `go vet`, `go test`, `go test -race`, coverage analysis

> **Status update (2026-06-17):** B1, B2 verified resolved (build/vet/`go test ./...` green).
> The WebSocket/SSE cluster is now fixed and covered by regression tests
> (`hyperchi/extensions/websocket/websocket_hub_test.go`): **C1** (JoinRoom/LeaveRoom
> deadlock), **C2** (cleanup deadlock, websocket + sse), **C3** (connection context outliving
> the request), **S1** (same-origin by default + `SetOriginPatterns`/`SetInsecureSkipVerify`;
> auto-relaxed in dev via `EnableWebSocket`), **S2** (server-side identity via
> `SetIdentifyUser`, defaulted to the session in `EnableWebSocket`; client `user_id`/`X-User-ID`
> no longer trusted), and **CC3** (crypto/rand connection IDs).
>
> The database layer is now fixed and covered (`hyperchi/database/database_test.go`): **C4** —
> SQLite PRAGMAs are applied via a registered driver `ConnectHook` (per-connection, mattn-correct)
> with a startup assertion (`verifyPragmas`) that WAL + foreign_keys actually took effect; **S3**
> — `Create`/`Update`/`Delete`/`Count`/`Exists` validate table and column identifiers against the
> allowlist, and `Backup.Create` escapes the VACUUM INTO path. (note C4 was fixed on mattn rather
> than switching to modernc.org/sqlite — see A7).
>
> **C5** is now fixed and covered (`hyperchi/content_race_test.go`): `ContentManager` has an
> RWMutex guarding `collections`/`cache`/`Items`; `Get` uses double-checked locking, mutators emit
> events outside the lock, and `ContentQuery.All` snapshots under the read lock. All §1–§2 blockers
> and criticals plus §3 S1–S3 are now resolved.
>
> **§4 F-series (partial):** F1 (`Try`/`TryValue` now convert panics to errors; `Catch` fires),
> F2 (`response` Status is buffered and flushed lazily so HX-* headers set after it are emitted;
> `NoContent` flushes explicitly), F3 (`AutoSave` parses the form before reading it), F4
> (`MultiStepForm` uses one method-branching handler so the display step is reachable), F5
> (`LiveFeed` defaults `PollInterval`, no more `NewTicker(0)` panic), F6 (`content.Query` clamps
> offset and guards the page-size division), and F9 (`validator` required fields reject blank/
> whitespace strings). Covered by new tests in helpers/response/patterns/content/validator.
>
> **F7:** the hand-rolled JS/CSS minifier (which corrupted string literals, ASI, url()/calc()) is
> replaced by `github.com/tdewolff/minify/v2`; `minifyCSS`/`minifyJS` now return errors so invalid
> input fails the build instead of shipping broken assets. Covered by `assets/compiler_test.go`.
>
> **F8 / A4:** the lying/dead compat layers are deleted — `validator/compat.go` (the always-
> `Valid:true` `ValidateForm` shim + the parent-package import), `middleware/compat.go`, and the
> entire `routing/` package (built on a `Fragment` that discarded its args; nothing imported it).
> **F10:** `GracefulServer` shutdown is now idempotent (`sync.Once`), returns the real shutdown
> error, and a programmatic `Shutdown()` unblocks `ListenAndServe`/`Serve` via a `done` channel
> instead of hanging until a signal (covered by `server/graceful_test.go`). **F11:** `Serve` wires
> shutdown hooks that stop the asset watcher and close the DB and KV stores; `EnableGracefulShutdown`
> doc is corrected. **F12:** `app.WebSocket()` returns 501 instead of faking a 101 that hung
> clients; the pocketbase stub's side-effecting ops return `ErrStubNotImplemented` instead of fake
> success; content `WatchForChanges` logs that hot-reload is not active instead of silently doing
> nothing. The full **F-series (F1–F12) is resolved**; remaining open work is §5 (CC concurrency)
> and §6–§10 (architecture, validators, assets, CLI, tooling).
>
> **Status update (2026-07-11, pre-publish cleanup):** the §10 hygiene items are addressed. The
> stale `Makefile` is removed outright (`Taskfile.yml` is the single automation entry point, so
> B2 is moot), `tests/benchmark_helpers.go` is deleted, the CHANGELOG heading is now
> `[Unreleased]`, and the internal context-doc drift is corrected (those docs are no longer
> tracked). Docs describing the abandoned component system (`specs/BUILD.md`,
> `examples/comparison.md`, `examples/TEMPLATES_AND_COMPONENTS.md`) are deleted;
> `examples/README.md` is rewritten for the three examples that exist; `docs/HTMX_ROADMAP.md`
> now records the completed htmx 4 migration. Still open: §5 concurrency, §6–§9, and the §10
> coverage and CI items. Note the test suite still creates `tests/hyperchi.db` because
> `hyperchi.New()` opens the default DB path in the cwd; the file is git-ignored but the tests
> should pass a `t.TempDir()` path.
>
> **Status update (2026-07-11, release-evaluation fixes):** a verification pass re-checked every
> open item against current code and the confirmed findings are now fixed. **§3:** S4 (URL
> filters scheme-allowlist + tests), S5 (helpers documented as non-security best-effort), S6
> (env capture inverted to an allowlist), and the lower-severity bullets (typed context keys
> everywhere, generic auth error, `io.ReadFull` upload sniff, bundler timeout + filename match,
> `AnimeAttr` attribute escaping, validation errors escaped and `Value` no longer serialized).
> **§5:** CC1–CC7 and CC9–CC13 resolved (CC7 — the session writer now passes through
> Flusher/Hijacker with tests — was the blocker); CC8 stays open as documented init-time-only
> template registration. **§6:** A2 (`Model()` deleted), A5 (stdout → logger), A10 (version from
> build info; `RequestID` middleware actually generates IDs; DB gains `...Context` variants and
> an honest debug logger); the false zero-dependencies doc claim is gone. **§9:** all four CLI
> defects fixed. `modules/pocketbase` (previously unbuildable go.mod) and `modules/performance`
> build, vet, and test clean. README examples all compile against the real API. Still open:
> §6 A1/A3/A7 (structural: god object, validation consolidation, module split + toolchain
> directive), A9 (body-sniffing architecture), the §7 V-series correctness details, the §8
> assets-pipeline items, and the §10 coverage and CI gates.

---

## Executive Summary

The codebase is in a notably better state than its history suggests — the uncommitted work on `fix/review-findings` already hardened CSRF, session IDs, rate limiting, and query-builder identifier validation. `go build` and `go vet` are clean, and the new race tests pass under `-race`.

However, the review found:

- **1 failing test** introduced by the current uncommitted refactor (blocks committing this branch)
- **3 deadlocks** in the WebSocket/SSE hubs that would freeze any real deployment using rooms or long-lived connections
- **A headline feature that silently doesn't work**: all SQLite pragmas (WAL, foreign keys, busy timeout) are no-ops due to wrong DSN syntax for the `mattn/go-sqlite3` driver
- **2 high-severity security gaps** in WebSockets (origin checking disabled, identity from client input)
- **15 packages with zero test coverage**, including `database/` and `server/` which are modified on this branch
- A pattern of **silent-failure bugs**: panic-swallowing `Try()`, an AutoSave that saves nothing, validation shims that always pass, dropped HTMX trigger headers

**Fix-before-merge:** items in §1 (Blockers) and §2 (Critical).

---

## 1. Blockers — broken by the current uncommitted changes

### B1. Failing test: `Page()` skips `responseCapture.finalize()`
- `hyperchi/hyperchi.go:1394-1423` (test: `tests/template_render_paths_test.go:423`)
- `TestRender_PageGet_AcceptOnlyJSON_StillRendersHTML` fails on the working tree but passes on HEAD. The refactor moved speculative JSON parsing into `rc.finalize()`, which is only called on the `HX` path (line 1269). `Page()` calls `capture.shouldPassThrough()` without calling `finalize()` first, so `parsedAsJSON` is always false and raw JSON bodies pass through instead of rendering into the template.
- **Fix:** call `capture.finalize()` in `Page()` immediately after `handler(capture, r)`, mirroring line 1269.

### B2. `make test` hides exactly this failure
- `Makefile:58-60` runs `go test -v ./hyperchi/...` only — the `tests/` integration package (the one failing) and `modules/` are excluded, so `make test` reports green while `go test ./...` fails.
- **Fix:** change the target to `go test ./...`.

---

## 2. Critical — deadlocks and broken core paths

### C1. Self-deadlock: `JoinRoom`/`LeaveRoom` call `broadcastToRoom` while holding the write lock
- `hyperchi/extensions/websocket/websocket.go:287, :319` (lock taken at 267/299, re-acquired at 339)
- `JoinRoom` holds `h.mu.Lock()` then calls `broadcastToRoom`, which takes `h.mu.RLock()`. `sync.RWMutex` is not reentrant — the goroutine blocks forever holding the write lock, freezing the entire hub permanently. Reachable from the first `join_room` message (readPump :451) or any connection with `?rooms=` (HTTPHandler :616). The new race tests never call `JoinRoom`, which is why this wasn't caught.
- **Fix:** snapshot the room's connection list before broadcasting, or add a `broadcastToRoomLocked` variant; never acquire `h.mu` while holding it.

### C2. Self-deadlock: `cleanup` calls `RemoveConnection` while holding `h.mu` (websocket AND sse)
- `hyperchi/extensions/websocket/websocket.go:551-573`, `hyperchi/extensions/sse/sse.go:404-426`
- `cleanup()` holds `h.mu.Lock()` and loops `h.RemoveConnection(id)`, which itself takes `h.mu.Lock()`. The first time the 30-second cleanup tick finds a dead connection, the cleanup goroutine deadlocks itself holding the hub write lock — all hub operations freeze forever.
- **Fix:** collect IDs to remove under the lock, release, then remove — or factor out a `removeConnectionLocked` helper.

### C3. WebSocket connections die immediately: context derived from `r.Context()` after handler returns
- `hyperchi/extensions/websocket/websocket.go:187, :586-620`
- `net/http` cancels the request context when `ServeHTTP` returns, even for hijacked connections. `HTTPHandler` returns right after `AddConnection`, so `conn.Context` is cancelled moments after upgrade — `readPump` fails, `writePump` exits, and every connection tears down almost instantly.
- **Fix:** block in the handler until the connection ends (run `readPump` inline — the idiomatic `coder/websocket` pattern), or build the connection context from `context.WithoutCancel(r.Context())` with explicit hub-driven cancellation.

### C4. SQLite pragmas silently never applied — wrong DSN syntax for the mattn driver ✅ FIXED (now on modernc.org/sqlite, whose DSN pragmas are verified at open)
- `hyperchi/database/database.go:57-66`
- The DSN uses `_pragma=journal_mode(WAL)` style, which is **modernc.org/sqlite** syntax. The project imports `mattn/go-sqlite3`, which expects `_journal_mode=WAL`, `_foreign_keys=on`, `_busy_timeout=30000`, etc., and silently ignores unknown params. Verified empirically: opening with this DSN yields `journal_mode=delete`, `foreign_keys=0`. All seven "PocketBase optimizations" — the package's headline feature — are no-ops. Consequences: `database is locked` errors under concurrent writes (no WAL, no busy timeout, `MaxOpenConns: 10`), and **foreign-key constraints are off** despite the comments — a data-integrity issue.
- **Fix:** rewrite the DSN in mattn syntax (`?_journal_mode=WAL&_synchronous=NORMAL&_foreign_keys=on&_busy_timeout=30000&_cache_size=-64000`), or switch to the cgo-free `modernc.org/sqlite` driver (which would also fix cross-compilation, see A7). Add a startup assertion (`PRAGMA journal_mode` must return `wal`) so this can't regress silently.

### C5. `ContentManager` maps mutated without any lock — fatal concurrent-map crash on the read path
- `hyperchi/hyperchi.go:263-272` (struct has no mutex), `:3555` (`Get` writes `cm.cache` on miss), `:3622-3626`, `:3744`
- `ContentManager.Get` is callable from concurrent HTTP handlers yet writes to `cm.cache` — two concurrent requests for uncached items is a `concurrent map writes` **fatal runtime crash** (not a recoverable panic). `Create`/`Update`/`Delete` mutating `col.Items` while `Get` ranges over it is also a race.
- **Fix:** add a `sync.RWMutex` guarding `collections`, `cache`, and `col.Items`.

---

## 3. Security — High

### S1. WebSocket origin verification disabled by default (Cross-Site WebSocket Hijacking)
- `hyperchi/extensions/websocket/websocket.go:133-137, :589`
- `NewWebSocketHub()` hardcodes `InsecureSkipVerify: true` and `acceptOpts` is unexported with no setter — there is no supported way to enable origin checking. Any malicious website a logged-in user visits can open a WebSocket in the user's auth context (cookies are sent on the handshake) and read/write room traffic.
- **Fix:** default to same-origin validation and add `SetOriginPatterns([]string)` / a constructor option. Never ship `InsecureSkipVerify: true` as the only behavior. (Same pattern in `assets/watcher.go:60-62` for the dev hot-reload socket — lower stakes, but use `OriginPatterns: []string{"localhost:*", "127.0.0.1:*"}` there too.)

### S2. WebSocket identity taken from unauthenticated client input
- `hyperchi/extensions/websocket/websocket.go:601-606`
- `HTTPHandler` sets `wsConn.UserID` from `?user_id=` or the `X-User-ID` header — both fully attacker-controlled. `UserID` is attached to `room_join`/`room_leave` broadcasts (:291, :322) and used as the connection's identity, so any client can impersonate any user. Combined with S1, this allows cross-site impersonation.
- **Fix:** derive identity server-side from the authenticated session/context (e.g. `security.GetUserIDFromContext(r.Context())` after auth middleware). Never trust a client-supplied user identifier.

### S3. Core DB write helpers interpolate table/column names without validation
- `hyperchi/database/database.go:243-321` (`Create`, `Update`, `Delete`, `Count`, `Exists`)
- The query builder and `Model` were hardened with `mustValidateIdentifier`, but the lower-level `DB` methods were not: `Create`/`Update` interpolate `table` and the **keys of the `data map[string]any`** directly into SQL; `Count`/`Exists` interpolate `table` and a raw `where` string. If an app passes a user-influenced map (e.g. keys from a decoded form/JSON body — a pattern this framework encourages), column names become a SQL-injection vector. `HyperChi.Paginate` (hyperchi.go:2980) also exposes `table` directly.
- **Fix:** apply the existing `validateIdentifier` allowlist to `table` and every column key in these methods; document `where` as developer-controlled with `?` placeholders only. Related: `Backup.Create` (`database/utils.go:525-530`) interpolates the backup path into `VACUUM INTO '%s'` — escape embedded quotes at minimum.

### S4. URL-building HTML filters allow `javascript:` URI XSS
- `hyperchi/filters/html.go:76-138` (`link`, `img`, `script`, `style`)
- These helpers `html.EscapeString` the URL but never validate the scheme, then return `template.HTML` — so the auto-escaper never gets a second chance. `{{ link "click" .UserURL }}` with `javascript:alert(1)` yields working XSS that `html/template` would otherwise have neutralized.
- **Fix:** parse with `net/url` and allowlist schemes (http/https/mailto/tel), or return plain values and let `html/template` sanitize in context.

### S5. `SanitizeInput`/`ValidateUserInput` are denylists that give false assurance
- `hyperchi/security/xss.go:193-229`
- Substring stripping of `"javascript:"`, `"onerror="`, etc. after escaping is both redundant and trivially bypassed (`java\tscript:`, `oneonerror=rror=`). Shipping these as named "sanitize" helpers invites developers to rely on them instead of contextual output encoding.
- **Fix:** remove or clearly scope as non-security best-effort; recommend/integrate `bluemonday` for actual untrusted-HTML sanitization (already mentioned in the file's comments).

### S6. Error pages capture the entire process environment with substring-based redaction
- `hyperchi/errors.go:277-323`
- `captureEnvironment` copies every env var into `ErrorDetails`, redacting only keys containing `PASSWORD`/`SECRET`/`KEY`/`TOKEN`. `DATABASE_URL=postgres://user:pass@...` and custom-named credentials pass through. One misconfigured `isDev` flag in production exposes everything.
- **Fix:** invert to an allowlist (Go version, GOOS, runtime stats) or drop env capture entirely.

### Security — lower severity ✅ FIXED (bundler integrity 2026-10-05; the rest in earlier passes)
- **Context keys are bare strings** (`"user_id"`, `"session"`): `security/helpers.go:427-450`, `kv_sessions.go:486,529`, `middleware/csrf.go:396`. The CSRF middleware coupling to the literal `"user_id"` string across packages is fragile. Use unexported key types + exported accessors. (staticcheck SA1029)
- **User enumeration**: `security/helpers.go:120-126` returns "Account is disabled" vs "Invalid credentials" — a minor enumeration oracle. Return the generic message.
- **Upload sniff short-read**: `security/upload.go:272-280` uses a single `file.Read(buffer)`; use `io.ReadFull` so `http.DetectContentType` always sees the full 512-byte prefix.
- **Bundler downloads CDN assets with no timeout and no integrity check**: `assets/bundler.go:128-160` — `http.Get` with the default client can hang startup forever; no SHA-256/SRI verification (supply-chain exposure). Also a filename mismatch: bundler writes `tailwindcss.min.css` but the compiler rewrites imports to `tailwind.min.css` — the import 404s.
- **`AnimeAttr` builds unescaped JS inside an HTML attribute**: `hyperchi/animation.go:380-391` returns `template.HTMLAttr` with JSON interpolated into `x-init="...JSON.parse('%s')..."` — any `'` or `\` in preset values breaks out into script injection. Also targets Alpine.js (`x-init`) while the framework documents hc-directives without Alpine.
- **Validation errors rendered as `template.HTML` without escaping**: `validator/form.go:113-152`, `validator/templates.go:75-112, 267-372` — latent XSS via custom messages; also the JSON error path echoes raw submitted `Value` back, including failed passwords. Escape interpolations; drop `Value` from serialized errors.

---

## 4. High — silent failures and broken features

### F1. `helpers.Try`/`TryValue` swallow panics and report success
- `helpers/errors.go:185-192, 208-215`; re-exported as `HyperChi.Try`/`.Catch` (hyperchi.go:4642-4649)
- The `recover()` result is discarded and there's no named return, so a panic inside `fn` makes `Try` return `nil` (success). `Catch`'s error handler is never invoked for panics. The docs explicitly promise "converting panics to errors."
- **Fix:** named return: `func Try(fn func() error) (err error) { defer func() { if r := recover(); r != nil { err = fmt.Errorf("panic: %v", r) } }(); return fn() }`

### F2. `response` helpers set HTMX headers after `WriteHeader` — headers silently dropped
- `hyperchi/response/htmx.go:286-358` (`Status`, `Success`, `Error`, `NoContent`)
- `Success` writes the status, then calls `.Trigger(...)` — headers set after `WriteHeader` never reach the client, so the `HX-Trigger` toast events these helpers exist to send are discarded on every call.
- **Fix:** record the status and flush lazily on first `Write`, or reorder helpers to set headers before status.

### F3. `patterns.AutoSave` always saves an empty map
- `hyperchi/patterns/patterns.go:478-485`
- Iterates `r.Form` without ever calling `r.ParseForm()` — `r.Form` is nil, so `SaveFunc` persists nothing while the client sees "Draft saved". Silent data loss in a feature whose purpose is not losing data.
- **Fix:** call `r.ParseForm()` first and handle its error.

### F4. `patterns.MultiStepForm` registers two handlers on the same pattern
- `hyperchi/patterns/patterns.go:246, :271`
- Both GET-display and POST-process handlers are registered on the same chi pattern; the POST handler 405s non-POSTs, so the form-display step is unreachable. The documented flow cannot work.
- **Fix:** one handler branching on method, or method-specific routes.

### F5. `patterns.LiveFeed` panics the process when `PollInterval` is unset
- `hyperchi/patterns/patterns.go:131` (goroutine spawn at :126)
- `time.NewTicker(0)` panics inside a bare goroutine → crashes the server. The doc example omits `PollInterval`, so following the docs crashes the app. `DefaultPollInterval` exists (:63) but is never applied.
- **Fix:** default `config.PollInterval` when `<= 0`.

### F6. `Collection.Query` divide-by-zero / negative-slice panics
- `hyperchi/content/content.go:997-1024`
- Empty filtered collection with `Limit <= 0` → `pageSize = 0` → `total / pageSize` panics. Negative `Offset` → negative slice index panic. Both reachable from template-driven queries.
- **Fix:** guard `pageSize == 0`; clamp `Offset >= 0`.

### F7. Hand-rolled JS/CSS minifier corrupts assets in the production path
- `hyperchi/assets/compiler.go:146-250`
- `minifyJS` joins trimmed lines with **no separator** (`let a = 1let b = 2`; ASI semantics change), `removeJSComments` truncates lines at `//` inside string literals (`"https://x"` → `"`), `minifyCSS` mangles strings/`url()`/`calc()`. With `EnableMinify` (the production default), the pipeline ships broken assets.
- **Fix:** use a real minifier (`tdewolff/minify`) or make `EnableMinify` a documented no-op until one is integrated.

### F8. Deprecated compat shims silently lie
- `validator/compat.go:106-109` — `ValidateForm` returns `Valid: true` unconditionally: migrated code believes forms are validated when nothing is checked.
- `routing/compat.go:42-45` — `Fragment(pattern, template, handler)` discards `pattern` and `handler`; no route is registered, no error returned.
- **Fix:** delegate to real implementations, or delete the compat layers (recommended — see A4).

### F9. `Required()` accepts empty strings — the most common form case fails open
- `validator/core.go:88-106` (form data at `form.go:40-46`)
- HTML forms always submit empty inputs as `""`, but the required check only triggers on absent/nil. `String().Required()` accepts a blank submission (e.g. `password_confirm` in `RegisterSchema`). `Boolean().Required()` is satisfied by `""`.
- **Fix:** treat `""` as missing for required string fields (or add explicit `NonEmpty`).

### F10. Manual `GracefulServer.Shutdown()` leaves `ListenAndServe` blocked forever
- `hyperchi/server/graceful.go:76-91, 103-117, 190-192`
- The serve goroutine only reports errors `!= http.ErrServerClosed`. On programmatic `Shutdown()`, nothing is sent on either channel and the `select` blocks on `sigChan` until an OS signal — the caller never returns. Affects `HyperChi.Serve` (hyperchi.go:2564).
- **Fix:** always send the serve result on `serverErr`, or add a `done` channel closed by `shutdown()`. Also: `shutdown()` swallows `Shutdown` errors (returns nil), and concurrent manual+signal shutdown runs hooks twice (no `sync.Once`).

### F11. Graceful shutdown closes nothing
- `hyperchi/hyperchi.go:2554-2565`
- `Serve` never wires `OnBeforeShutdown`/`OnAfterShutdown`, so on SIGTERM the bbolt files, SQLite pool, asset watcher, and scheduler goroutines are abandoned. `EnableGracefulShutdown` (:5013-5017) is a documented no-op.
- **Fix:** `gs.OnBeforeShutdown(func(){ g.kvRegistry.Close(); g.CloseDB(); ... })` inside `Serve`.

### F12. Content hot-reload doesn't exist; mock WebSocket methods hang clients ✅ FIXED 2026-10-05 (the content watcher reloads and stops)
- `content/content.go:1463` — `cl.watcher` is never initialized, so `WatchForChanges: true` (the default) is a silent no-op. If wired up, `reloadContent` (:1492-1502) would race on `cl.collections`.
- `hyperchi.go:2085-2116` — `g.WebSocket`/`g.WS` write a fake `Sec-WebSocket-Accept: mock-accept-key` + 101 without hijacking — clients hang. Return 501 or delegate to the real extension hub.
- `pocketbase/pocketbase.go` — the whole package is a (well-labeled) stub, but `UsePocketBase` (hyperchi.go:5237) wires it in with a real-looking API; `Delete` returns `nil` success without deleting. Make stubs return `ErrStubNotImplemented`.

---

## 5. Concurrency — Medium

| # | Issue | Location | Fix |
|---|-------|----------|-----|
| CC1 | `Watcher.Stop` non-blocking send can drop the stop signal → poller leak + duplicate pollers on restart | `assets/watcher.go:96-135` | `close(w.stop)` instead of send; recreate channel on `Start` |
| CC2 | bbolt store: unsynchronized `closed` bool (race); `SetTTL` expiry never enforced — expired values served forever, file grows unbounded; `:__expiry__` keys pollute `Keys()` and make `CleanupExpiredSessions` delete live sessions' expiry markers | `kv_bbolt.go:23, 58-61, 164-171, 298-358`; `kv_sessions.go:393-409` | `atomic.Bool` for closed; lazy-expiry check in `Get`/`Has`; filter `:__expiry__` from `Keys` |
| CC3 | WS connection IDs from `time.Now().UnixNano()` can collide under load → silent map overwrite, orphaned connection | `websocket.go:596` | `crypto/rand` hex or atomic counter |
| CC4 | Data races on `WebSocketConnection` counters/`LastPing` between pumps and `cleanup`; `LastPing` only updates on outgoing pings so busy connections can be reaped | `websocket.go:441, 510, 520, 565` | `atomic.Int64` fields |
| CC5 | Unstoppable `for range ticker.C` goroutines: WS/SSE cleanupLoop, `Schedule` (every task = forever-goroutine, no cancel handle), patterns, content | `websocket.go:541-548`, `sse.go:393-400`, `hyperchi.go:2168-2199`, `patterns.go:126,134,192,200`, `content.go:389` | Mirror `MemoryStore.stopCh` pattern; `Schedule` should return a stop handle |
| CC6 | `KVCache` tag index: non-atomic read-modify-write loses tag mappings → `InvalidateByTag` misses entries (stale cache survives invalidation); `GetOrSetTTL` has no singleflight (stampede) | `kv_cache.go:249, 271-335` | Mutex around tag-index mutation or per-key tag entries |
| CC7 | `sessionResponseWriter` only embeds `http.ResponseWriter` — `Flusher`/`Hijacker` assertions fail, so SSE and WS upgrades break behind `SessionMiddleware`; `SaveSession` errors silently dropped | `kv_sessions.go:501-525` | Implement `Flush`/`Hijack` passthrough or use `http.NewResponseController` |
| CC8 | `g.templates` pointer + `funcMap` swapped without synchronization; late `RegisterFilter`/`LoadTemplates` races with in-flight renders | `hyperchi.go:1161-1203, 1428-1436` | `atomic.Pointer[template.Template]` or enforce init-time-only |
| CC9 | `ValidatorEngine.schemas` and `FormBuilder.forms` are unsynchronized maps mutated by public methods | `hyperchi.go:275-277, 305-307, 3928-3944, 4277` | `sync.RWMutex` or document init-only |
| CC10 | `EventBus.Emit` runs handlers via bare `go handler(data)` — a panicking subscriber kills the process (the scheduler emits events, so this is in-framework reachable) | `hyperchi.go:4019-4030` | `defer recover()` wrapper per handler |
| CC11 | No `context.Context` anywhere in the DB layer (`Query`/`Exec`/`Tx` use non-ctx APIs) — request cancellation never reaches SQLite; slow queries hold pool connections after clients disconnect | `database/database.go`, `generic.go`, `transaction.go` | Add `...Context` variants, thread `r.Context()` |
| CC12 | `triggerHotReload` holds the client-map write lock across up-to-5s network writes | `assets/watcher.go:288-306` | Snapshot under lock, write outside |
| CC13 | `Group()` builds a partial `HyperChi` (4 fields) — most methods on the sub-app nil-panic | `hyperchi.go:2511-2521` | Copy the parent struct, override router |

---

## 6. Architecture & API design

### A1. `hyperchi.go` is a 5,716-line god object — 217 methods on `*HyperChi` ✅ FIXED 2026-10-05 (pass-throughs removed, subsystems on group fields, file split)
The App owns routing, templates, validation DSL, event bus, two cache systems, form builder, content management, a scheduler, WebSockets, KV/sessions, PocketBase, health endpoints, deployment-file generation, env access, a profiler — plus pure pass-throughs (`g.Str()`, `g.Map()`, `g.HTTPGet()`, `g.Ptr()`...) and demo code shipped as public API (`KVStoreExample()`, :5672, which itself ignores errors).
**Fix (phased):** (1) mechanically split into focused files (`routes.go`, `render.go`, `scheduler.go`, ...) — zero API change; (2) delete `KVStoreExample` and pass-through sugar; (3) expose subsystems via group fields rather than flat methods.

### A2. `HyperChi.Model()` is a stub returning a placeholder map ✅ FIXED (removed)
- `hyperchi.go:2930-2941` — returns `map[string]any{"table":..., "db":...}` with a "this is a placeholder" comment. Go methods can't have type parameters anyway. Delete it; point docs at `database.NewModel[T]`.

### A3. Three overlapping validation systems ✅ FIXED 2026-10-05
- **Resolution:** request input validates through `hyperchi.Bind` tags (plus an optional `Validate()` method); runtime-shaped input through `validator` schemas registered on `app.Schemas`. The in-package `Schema`/`ValidatorEngine` duplicate, the unused `FormBuilder`, the core-coupled `validator` render helpers, and the chained `database.Validator` are removed; `database.IsTaken` keeps the uniqueness check. The `content` package keeps its own front-matter schema, which is content typing, not request validation. The two slug generators remain.
- Slugs: `database.SlugGenerator.Generate` delegates to `helpers.Slugify`, so there is one slug rule.

### A4. Dead/deprecated compat layers; subpackages import their parent ✅ FIXED (compat files and `routing/` deleted)
- `middleware/compat.go`, `validator/compat.go`, `routing/compat.go` are runtime-printing wrappers (two of them broken — F8). `routing/htmx.go:11` and `routing/helpers.go:34` import the parent `hyperchi` package — a dependency inversion that guarantees the parent can never use them. `validator/compat.go:21-23` prints "this package is deprecated" from `init()` on import while the same package holds the only implementation of its documented API.
- **Fix:** delete all three compat files; delete or rewrite `routing/`. Use the canonical `// Deprecated:` doc-comment prefix (current `DEPRECATED:` is invisible to staticcheck/gopls).

### A5. Library code prints to stdout instead of returning errors ✅ FIXED 2026-10-05
- `integration.go:77-148` (extension registration *failures* printed and swallowed), `kv_cache.go:264`, `content.go:1495`, all compat shims. A framework writing to stdout corrupts host programs that use stdout for output, and callers can't detect that SSE/WS setup failed.
- **Fix:** return errors from registration; route info through the existing logger.

### A6. Asset scripts resolved via `runtime.Caller` against the framework's source checkout ✅ FIXED (scripts embedded)
- `integration.go:28-57` — probes paths relative to the framework source (including `examples/mini-crm/static/js`!). Consumers installing via the module cache silently get placeholder JS (`// hyperchi: missing asset X`) with a 200 status — undiagnosable.
- **Fix:** `//go:embed` the scripts (the CLI already does this correctly at `cmd/hyperchi-cli.go:89`).

### A8. Template loading silently dropped nested templates ✅ FIXED 2026-08-09
- `LoadTemplates` called `template.ParseGlob`, and Go's `filepath.Glob` has no `**` operator: it degrades `**` to a single `*`. The scaffold itself ships `filepath.Join("templates", "**", "*.tmpl")`, so it worked only because the generated tree happens to be exactly one level deep. Any project nesting templates deeper lost them with no error at load time, surfacing much later as a confusing "no such template" at render.
- A pattern matching nothing also produced an empty template set and a nil error.
- **Fix:** `resolveTemplatePattern` expands `**` by walking the tree. Results are sorted so parsing is deterministic, and an empty match is now an error naming the pattern. Covered by `templates_loading_test.go`.

### A9. No way to render a named template to a string ✅ FIXED 2026-08-09
- Every renderer was bound to an `http.ResponseWriter`. A component or block system needs to render template X with data Y and get the markup back, so callers had to hold the `*template.Template` themselves and bypass the app.
- **Fix:** `HyperChi.RenderToString(name string, data any) (template.HTML, error)`. Data is still escaped by `html/template` on the way in; only the returned wrapper is trusted. An escaping case is covered by test.

### A10. Template names had no extension fallback ✅ FIXED 2026-08-09
- `ParseGlob` and `ParseFiles` register a template under its base filename including the extension (`index.tmpl`), while callers write `hx.Template("index")`. The scaffold only worked because every scaffold file carries an explicit `{{define "index"}}`, which was an undocumented convention.
- **Fix:** name resolution tries the bare name first, then `.tmpl`, `.html`, `.htm`, `.gohtml`. An explicit `{{define}}` still wins because the bare name is matched first. A failed lookup now lists the loaded template names, which is the information needed to fix it.

### A11. `database` leaked `sql.ErrNoRows` ✅ FIXED 2026-08-09
- `DB.Get`, `DB.GetMap`, the `Tx` equivalents, and `generic.go` all returned `sql.ErrNoRows` unwrapped, forcing every caller to import `database/sql` purely to name the sentinel.
- **Fix:** `database.ErrNoRows` re-exports it. The value is identical, so `errors.Is` works against either and existing code keeps compiling.

### A7. Module hygiene ✅ FIXED 2026-10-05 (go 1.26.0 with no toolchain; modernc.org/sqlite; imaging replaced by x/image; CLI in its own module)
- `go.mod` sets `go 1.25.0` + `toolchain go1.26.0`, forcing every consumer onto the newest toolchain. Lower the `go` directive to the actual minimum; drop `toolchain`.
- `mattn/go-sqlite3` requires cgo (cross-compilation pain for every consumer); `modernc.org/sqlite` is pure-Go **and** would make the current DSN syntax correct (fixes C4 simultaneously).
- `disintegration/imaging` is unmaintained (last release 2019); used only in `assets/optimizer.go`.
- `lipgloss` + `cobra` are CLI-only but live in the framework module — every web app downloads the TUI stack. Split the CLI into its own module.
- `hyperchi.go:12` doc claims "Zero external dependencies beyond Go standard library" — false (chi, bbolt, websocket, sqlite3, x/crypto).

### A8. Panic-vs-error strategy is inconsistent ✅ FIXED 2026-10-05
- Good: `NewWithConfig` panics with `NewWithConfigE` escape hatch; builders panic on bad identifiers (documented).
- Bad: `helpers.ReadFile` returns `""` on error while documented as "safe"; `ToInt`/`ToFloat`/`Matches` discard errors; `Schedule` logs an invalid interval and returns void — a typo'd `app.Every("5x", ...)` compiles, logs once, and the job silently never runs.
- **Fix:** one rule per layer — builders may panic on programmer error; anything driven by runtime input returns `error`. `Schedule`/`Every` should return `error`.

### A9. `HX`/`responseCapture` body-sniffing architecture ✅ FIXED 2026-10-05
- Handlers expressed template data by JSON-encoding it to the ResponseWriter; middleware buffered, sniffed the first byte, and re-parsed.
- **Resolution:** the Context API is the only route shape. Handlers call `c.View`/`c.Page`/`c.Fragment` with data directly; `HX`, `Page`, and `responseCapture` are deleted. The Context writer passes `Flusher` and `Hijacker` through.

### A10. Misc design ✅ FIXED 2026-10-05
- **Hardcoded version strings**: `Health()`/`Status()` report literal `"2.0.0"` (hyperchi.go:2021, 2031) while `GetBuildInfo` (:5020) uses `debug.ReadBuildInfo`. Single source of truth.
- **`Daily()` means "every 24h from registration"**, not midnight (hyperchi.go:2147-2255). Document or implement.
- ✅ FIXED 2026-10-05 **Route registration had too many spellings**: one layer now, `Get`/`Post`/... with a `Handler`, plus `app.Handle` for raw net/http. HX response headers are set through `c.HX()` (backed by the `response` package); the package-level `SetHX*` helpers remain for middleware.
- **`config/config.go`**: `yaml` tags with no loader; `DSN()` advertises postgres/mysql with no drivers; `Validate` never checks `Security.SecretKey` in production.
- **`debug query logger measures nothing`**: `database/database.go:358-366` — the defer fires inside `logQuery` itself, before the query runs; every "took Xms" line is ~0ns. Return a `func()` to defer at the call site.

---

## 7. Validator & helpers — correctness details

| # | Issue | Location |
|---|-------|----------|
| V1 ✅ | `MultipleOf` divide-by-zero panic for values < 1e-6; `int(num*1e6)` overflows above ~9.2e12; `NaN` passes all min/max checks (`ParseFloat("NaN")` succeeds, comparisons all false) | `validator/types.go:376, 390-404` |
| V2 ✅ | String length rules count bytes, not runes — `MaxLength(10)` rejects valid unicode input (content half ✅ FIXED 2026-10-05) | `validator/types.go:192,196`; `content.go:1580-1585` |
| V3 ✅ | `CommonTemplateSnippets` use `sub`/`default` functions that are never registered — advertised snippets fail to execute | `validator/templates.go:151, 177, 207` vs `:19-31` |
| V4 ✅ | `ParseTime` tries US `01/02/2006` before EU `02/01/2006` — "05/03/2026" silently parses as March 5; EU format unreachable for day ≤ 12. Data-corruption-grade ambiguity | `validator/core.go:255-262` |
| V5 ✅ | Validation middleware skips only `GET` — `HEAD`/`OPTIONS` can 422 | `validator/form.go:161`, `middleware.go:82` |
| V6 ✅ | Byte-vs-rune bugs in "Python-style" string helpers (`Slice`/`Left`/`Right`/`Truncate` split UTF-8 mid-rune; `Reverse` uses runes — inconsistent) | `helpers/strings.go:188-230, 411-426` |
| V7 ✅ | `strings.Title` (deprecated, unicode-broken) | `helpers/strings.go:89` |
| V8 ✅ | Hot-path regex compilation on every call | `helpers/strings.go:283-489`, `database/utils.go:352-454` |
| V9 ✅ | `scanIntoStruct` re-reflects the struct type per row | `database/generic.go:96-151` |
| V10 ✅ | `ErrStoreNotFound` declared but `KVRegistry.Get` returns ad-hoc errors — `errors.Is` never matches | `kv.go:92, 184-188` |
| V11 ✅ | `min`/`max` shadow Go 1.21+ builtins | `errors.go:355-368` |
| V12 ✅ | Multiple competing `// Package hyperchi` doc comments (7 files in root pkg, 4 in database/, 2 in helpers/) — godoc concatenates nondeterministically | keep one per package in `doc.go` |
| V13 ✅ | Global mutable singletons in helpers (`defaultErrorHandler`, logging, env) — unsynchronized | `helpers/errors.go:558`, `helpers/logging.go:211`, `helpers/env.go:496` |

---

## 8. Assets pipeline & frontend

- ✅ FIXED 2026-10-05: **Watcher `skipDirs` substring matching defeats its own extension list**: `assets/watcher.go:413-433` skips any path *containing* `"tmp"`/`"temp"` — so `.tmpl` files and anything under `templates/` can never be watched; the `.html/.tmpl/.tpl` entries in `watchableExts` are dead code. Match path segments, not substrings.
- ✅ FIXED 2026-10-05: **Optimizer**: JPEG/PNG use `getOutputPath` (no `images/` subdir) while SVG/copy use `getOptimizedPath` (adds `images/`) — output lands in two layouts. `optimizeWebP` converts WebP→JPEG (loses alpha) and likely just fails (no webp decoder imported); `ConvertToWebP` is a copy-file placeholder despite `EnableWebP` docs promising conversion. (`assets/optimizer.go:241-366`)
- ✅ FIXED 2026-10-05: **Docs oversell**: `assets/assets.go` promises tree shaking, code splitting, purging, source maps — none implemented; `SourceMaps` is read nowhere; the package doc ends with "Basic Example:" followed by nothing; the `EnableProduction` doc example doesn't compile.
- ✅ FIXED 2026-10-05: **Frontend directives** (`frontend/assets/scripts/hyperchi-directives.ts`): well-built (reactivity-backed scopes, expression caching, honest XSS warnings), but: `new Function` + `with` requires CSP `unsafe-eval` (document this); `hc-html` assigns `innerHTML` from arbitrary expressions — consider an opt-in sanitizer hook; the canonical runtime is a manually-synced copy in `cmd/assets/` (`task sync-cli-assets`) — drift-prone; `dist/hyperchi-directives.js` (build:lib output) is absent from `frontend/dist/`.
- **Resolved 2026-09-16:** frontend, scaffold, and streamer-vite pin `htmx.org@4.0.0` exactly. Standalone runtimes and embedded SSE/WS extensions use the same stable release; inherited boost attributes and extension markers were updated. See `HTMX_ROADMAP.md`.

---

## 9. CLI ✅ FIXED 2026-10-05

- `serveApp` runs `exec.Command("./" + binary)` — breaks absolute `--binary` paths (`hyperchi-cli.go:609`).
- `startBothServers` kills only the `npm`/`go run` parents — compiled children and Vite's node child survive, orphaning ports 8080/3000. Use process groups (`Setpgid` + negative-PID kill) or SIGINT+wait (`hyperchi-cli.go:442-480`). Doc comment says Vite is on 5173; code uses 3000.
- Project name is substituted unvalidated into `go.mod` module path and `package.json` name — spaces/uppercase produce invalid output (`hyperchi-cli.go:314`).
- `profiler reset` prints a green SUCCESS without doing anything; `profiler`/`deploy health` subcommands only print instructions (`hyperchi-cli.go:823-870`).
- `writeFile` (:1461) is dead code with a false doc comment.

---

## 10. Testing, CI & project hygiene

**Observed state:** build ✅, vet ✅, race tests ✅, **1 failing integration test** (B1).

### Coverage

Measured 2026-10-05 with `go test -coverprofile` (framework total 64.5%; CI floor 62%).

| Package | Coverage | | Package | Coverage |
|---|---|---|---|---|
| validator | 99.8% | | websocket | 53.7% |
| database | 97.0% | | hyperchi (core) | 51.1% |
| helpers | 96.5% | | middleware | 50.5% |
| content | 95.5% | | sse | 46.0% |
| integrations | 94.5% | | patterns | 44.1% |
| assets | 91.5% | | server | 43.6% |
| config | 75.4% | | response | 40.2% |
| security | 66.8% | | filters | 28.2% |
| logging | 62.7% | | pocketbase | 27.0% |

The CLI module (`cmd/hyperchi`) is 40.9%. No tests: `extensions`, `extensions/response_targets`,
`health`. The black-box `tests/` suite does not count toward core coverage without
`-coverpkg=./hyperchi/...`.

### Findings
- **CI is thin**: ✅ FIXED 2026-10-05. CI race-tests, runs `golangci-lint`, and gates total coverage (see the October 2026 coverage section).
- ✅ FIXED: **CHANGELOG**: single entry `## [1.0.0] - 2025-01-XX` (literal placeholder date, never filled). No git tags exist despite `scripts/prepare-release.sh` and a release guide — the release process has never been exercised.
- ✅ FIXED: **Tests write `tests/hyperchi.db` into the source tree** instead of `t.TempDir()` — stateful across runs. Also a committed `hyperchi/validator/hyperchi.db` artifact should be removed.
- ✅ FIXED: **`tests/benchmark_helpers.go`**: non-`_test.go` file importing `testing`, defining helpers nothing references. Delete or rename.
- **`examples/zeroindex/`**: RESOLVED. The vendored external Astro reference (22M, mostly stock demo images) was removed rather than committed.
- ✅ FIXED: **Makefile mojibake**: corrupted emoji bytes in echo strings (lines 59, 99-103).
- **Context-doc drift**: cited `go test -run TestStringValidator` which doesn't exist (fixed;
  the internal context docs are no longer tracked).
- **Noisy test logs**: ✅ FIXED 2026-10-05. The hot-reload line prints at server start, and the WebSocket hub no longer logs expected teardown errors.
- Minimal `t.Parallel()` (1 occurrence) — low priority while the suite is <5s.

### Positives
- `.gitignore` is thorough; no binaries/node_modules/dist tracked.
- `tests/` integration suite is genuinely good black-box testing: httptest-based (no hardcoded ports), table-driven, real error-path coverage, zero skips.

---

## 11. What's done well

- **Password hashing**: bcrypt cost 14 with explicit 72-byte rejection preventing silent truncation collisions (`security/auth.go:106-121`).
- **CSRF**: double-submit + HMAC-SHA256 self-validating tokens, constant-time comparison, optional session binding, fails closed on entropy failure, SameSite=Strict/Secure defaults (`middleware/csrf.go`).
- **Sessions**: crypto/rand IDs that fail closed, HttpOnly/Secure/SameSite=Strict defaults, `Regenerate()` for fixation defense (`kv_sessions.go`).
- **Rate limiter**: forwarding headers honored only from trusted-proxy CIDRs — spoof-resistant (`middleware/ratelimit.go:294-375`).
- **Query-builder identifier allowlisting** with explicit SECURITY doc comments — better than many comparable builders (`database/utils.go:64-102`).
- **Upload MIME validation** correctly refuses sniffed active content as compatible with benign declared types (`security/upload.go:335-384`).
- `kv_memory.go` post-fix: clean stop-channel lifecycle, no lock-upgrade, copy-on-read/write.
- WebSocket `trySend`/never-close-`Send` ownership discipline is correct and documented; new race tests genuinely cover the previously fixed races.
- `database.Tx` panic-safe rollback-and-repanic; `NewWithConfigE` error-returning sibling to the panicking constructor.
- The CLI scaffolder: `//go:embed all:_scaffold` with the `.tpl` trick, the framework-path resolution chain, clear post-create UX.
- `hyperchi-directives.ts`: thoughtful reactive design with honest inline security documentation.
- Doc-comment coverage on exported identifiers is unusually thorough.

---

## 12. Recommended priority order

1. **Now (blocks this branch):** B1 (failing test), B2 (`make test` scope).
2. **Before any deployment uses WS/SSE:** C1–C3 (deadlocks + instant disconnect), S1–S2 (origin + identity).
3. **Before relying on the DB layer:** C4 (SQLite pragmas — consider switching to `modernc.org/sqlite`, fixing C4 + cgo in one move), C5 (ContentManager race), S3 (identifier validation in `DB.Create`/`Update`).
4. **Silent-failure bugs (cheap fixes, high trust impact):** F1 (`Try`), F2 (HX headers), F3 (AutoSave), F5/F6 (panics), F9 (`Required("")`), F10/F11 (shutdown).
5. **Structural (schedule deliberately):** A1 (split hyperchi.go), A3/A4 (consolidate validation, delete compat + routing), A7 (module split, deps), test coverage for `database/` + `server/`, CI `-race` + lint.
6. **Cleanups:** everything in §7–§9 as the relevant files are touched.
