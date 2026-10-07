# Mailgrid

An internal tool for a small team that writes letters to a list of people.
Import recipients from CSV, write a letter with merge fields, preview it
for a real recipient, and run a send-out that renders one letter per
recipient into an outbox. Then browse the outbox or export it as CSV or as
`.eml` files.

**Mailgrid never sends email.** A send-out writes each rendered letter to the
`outbox` table in SQLite and stops there. The UI says so on every page, and
starting a send-out asks the person to confirm it. Deliver the exported `.eml`
files with whatever system your team trusts for that.

Every page is rendered in Go with
[gomponents](https://www.gomponents.com) through `modules/gomponents`. There
are no templates and no Node build.

## Run it

```sh
go run .          # or: task dev
air               # or: task dev:air, rebuilds on .go, .js, .css, and .svg edits
```

In development it binds `0.0.0.0:8080` (the next free port when 8080 is
taken), logs each address it answers on, and adds a demo list of 60 people
and a demo letter on first run. With no account yet, any page goes to
`/setup`, which creates the first one. Never `go build .` in this directory;
the repository keeps examples to `go run` and Air.

| Variable | Default | Meaning |
| --- | --- | --- |
| `ENVIRONMENT` | `development` | `production` turns on secure cookies, the CSP, and exact port binding, and skips the demo data |
| `HOST`, `PORT` | `0.0.0.0`, `8080` | Listen address |
| `DB_PATH` | `data/mailgrid.db` | SQLite file. Sessions go in `sessions.db` beside it |
| `MAILGRID_ADMIN_EMAIL`, `MAILGRID_ADMIN_PASSWORD`, `MAILGRID_ADMIN_NAME` | unset | Creates this account at startup when there is none yet (password at least 12 characters) |
| `MAILGRID_PACE` | `75ms` in development, `0` in production | Pause between recipients, so a person can watch a send-out and cancel it |
| `MAILGRID_MAX_UPLOAD` | `2097152` | Largest CSV upload, in bytes |
| `MAILGRID_FROM` | `Mailgrid <outbox@mailgrid.invalid>` | From header in exported `.eml` files |
| `DB_LOG_QUERIES` | `false` | `true` logs every SQL statement with its arguments (a send-out writes several rows per recipient) |

In production, cookies are `Secure`, so serve Mailgrid over HTTPS (behind a
proxy, list its CIDRs in `TRUSTED_PROXIES`).

## What it does

- **Lists.** Upload a CSV with a header row: `email` is required, `name` is
  optional, and every other column becomes a merge field (`First Name`
  becomes `first_name`). Each row is checked: invalid addresses, wrong column
  counts, and oversized values are listed by line number while the rest
  imports. Emails are lowercased, duplicates in the file are skipped, and
  people already on the list are left as they are. The recipient table pages
  and searches as you type, and each recipient can be edited or removed.
- **Letters.** A title for the team, a subject, and a plain-text or HTML body.
  The one merge syntax is `{{field}}`. A custom renderer replaces tags with
  values and can do nothing else, unlike `text/template`. In HTML letters
  every merged value is HTML-escaped. Text between braces that is not a valid
  tag is rejected on save. The preview renders the unsaved form for a chosen
  recipient as you type (HTML in a sandboxed iframe), lists the fields the
  letter uses, and warns when the recipient or the list has no value for one.
  A strict letter holds those recipients as failed instead of rendering a
  blank.
- **Send-outs.** Pick a letter and a list, read what will happen, tick the box
  that says nothing is emailed, and start. A background job queues every
  recipient into the outbox, then renders them one by one. Progress (counts,
  the current recipient, recent failures) streams to the page over SSE. Cancel
  stops the job after the current recipient. Reloading the page reads the
  state from the database. There is one send-out per letter and list, and the
  outbox holds one row per letter, list, and recipient, so running it again
  renders only people not rendered yet.
- **Outbox.** Browse rendered letters by status, open one, download it as
  `.eml`, or export the whole send-out as CSV or a zip of `.eml` files.
- **Team.** Sign-in with bcrypt-hashed passwords and server-side sessions;
  any teammate can add another. The audit log records who signed in,
  imported, edited, started, cancelled, and exported what.

## Data model

| Table | Holds |
| --- | --- |
| `users` | email, name, bcrypt hash |
| `lists` | name |
| `recipients` | list, email (unique per list), name, merge fields as JSON |
| `letters` | title, subject, body, `text` or `html`, strict |
| `sendouts` | letter and list (unique pair), status, current recipient, error, times |
| `outbox` | send-out, recipient, a copy of their email, name, and fields, then `queued`, `rendered`, or `failed` with the rendered subject and body (unique per letter, list, recipient) |
| `audit_log` | who, action, detail, when |
| `settings` | whether the demo data was added |

Migrations run on every boot and are idempotent. Simple tables use the
`database.Table` builder. Tables with composite unique keys or cascades are
plain SQL.

## Framework and module calls it uses

- Routes are `func(c *hyperchi.Context) error`. Pages render through
  `c.RenderWith("gomponents", node)` after `app.Integrations().Register` of
  `gomponentsadapter.New()`. htmx swaps get fragments, and `c.Status(422)`
  keeps the form a failed save re-renders. `app.OnError` renders errors as
  gomponents pages or alerts and leaves JSON clients to `DefaultError`.
- Input: `hyperchi.Bind[T]` with `validate` tags, a `Validate()` method for
  merge tags, and a `*multipart.FileHeader` field for the CSV.
  `MAILGRID_MAX_UPLOAD` is a product limit under the framework's 32 MiB
  multipart cap; an htmx upload's body is capped to it with
  `http.MaxBytesReader` before parsing. `hyperchi.FieldErrors` feeds the form
  messages, and `database.IsTaken` checks account emails.
- Responses: `c.Redirect` (an `HX-Redirect` for htmx), `c.HX().Trigger`,
  `c.Blob` for CSV, zip, and `.eml`, `c.NoContent`, and `hyperchi.NotFound`
  and friends.
- Data: `app.Migrate`, `database.Table`, `database.GetStruct` and
  `ListStruct`, with their `Context` forms where the send-out job reads rows
  (cancelling the job stops its query), `database.Select` with
  `database.NewPaginator`, `db.Tx`, `db.Create`, `Update`, `Delete`, `Count`,
  and `Exists`.
- Sessions and security: `app.UseKVStoreBBolt` and `app.UseSessions` with
  `app.SessionMiddleware()`, `session.Regenerate()` at sign-in,
  `security.HashPassword` and `CheckPasswordHash`, `app.Middleware.CSRF()`,
  `Security()`, and `Recovery()`, and `security.WithUserID` so `c.UserID()`
  is set. Every form renders the token as a `csrf_token` field, so posts work
  without JavaScript, the multipart CSV upload included; `static/js/app.js`
  also sends it as `X-CSRF-Token` on every htmx request, which covers buttons
  that send no form.
- Live progress: `app.Extensions().GetSSEHub()` with `BroadcastSwap`,
  `Broadcast` for the named `done` event, and `SendToConnection`, and
  `EnableExtensionAssets()` to serve `/hyperchi/js/hx-sse.js`. The send-out's
  job lifecycle is registered as `integrations.Hooks`, so `app.ServeContext`
  marks interrupted runs at start and waits for jobs before the database
  closes.
- Browser: htmx 4.0.0 (`static/js/htmx.min.js`, SHA-256
  `e484d9171a9db30a39c8f16e3d709d4137f3211c659f8e6125816635033d593f`) and
  the HyperChi directives runtime, both vendored, embedded, and served with
  `app.StaticFS`, so the app runs from any working directory. `hc-data`,
  `hc-model`, and `hc-bind:disabled` keep the start button disabled until the
  outbox box is ticked.

### Design notes

- The SSE endpoint is a route handler built from the hub's connection API
  rather than `EnableSSE`, so it can check the session, take the channel from
  the route instead of a `?channels=` query any client can set, and subscribe
  before it sends the first snapshot. Because it does not go through the
  hub's `HTTPHandler`, it clears the server's read and write deadlines for
  the stream itself. The hub's sweep pings every open stream every 30
  seconds, which keeps quiet streams alive.
- htmx and `/hyperchi/js/hx-sse.js` load as blocking scripts in the head. With
  `defer`, htmx processes the page as soon as it runs, before the extension
  registers, so `hx-sse:connect` on the first page never connects.
- Pages use `database.Select(...).OrderBy(...)` with `database.NewPaginator`
  rather than `database.Paginate`, which orders by `rowid` and returns every
  column as a map: recipients page by email, the audit log newest first, and
  the outbox scans typed rows without the letter bodies.

### One upstream workaround

When `hx-sse:close="done"` closes a stream, the htmx 4.0.0 SSE extension
(`hx-sse.js`, which the framework serves unchanged) leaves an unhandled
`AbortError` from its `cleanup()`. This is an htmx bug, not a HyperChi one.
`app.js` silences that one rejection and nothing else.

## Tests

```sh
task check   # gofmt, go vet, golangci-lint, then go test -race -count=1 -cover ./...
task grade   # the agent-idiom grader: net/http fallbacks, expected zero
```

The tests cover CSV parsing (valid, invalid, duplicate, and oversize input,
and the upload route with and without JavaScript), merge rendering (missing fields, HTML escaping, header
safety), the send-out job (every recipient rendered, idempotent reruns,
cancellation mid-run, shutdown, published progress), the SSE endpoint read
with a deadline, sign-in and CSRF, the export content types, and pages and
assets served from an unrelated working directory. Each test
gets its own database in `t.TempDir()`, and none sleeps. The race run takes
about half a minute, mostly bcrypt at the framework's cost of 14.
