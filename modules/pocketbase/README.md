# HyperChi PocketBase Module

HTTP client for PocketBase records and password authentication. This module uses
HTTP directly; it does not embed a PocketBase server or depend on its Go SDK.

Supported operations:

- Record create, read, update, delete, and filtered listing.
- `GetFullList` fetches every page and returns an error if any page fails.
- User and superuser password authentication.
- Custom record fields decoded into `Record.Data`, and encoded back beside the
  system fields when a `Record` is marshaled.
- Health checks and configurable HTTP timeouts.
- Retries for reads (see below).

Real-time subscriptions, file uploads, and MFA flows are not implemented.

## Compatibility

`AdminAuth` uses `/api/collections/_superusers/auth-with-password`, the superuser
endpoint introduced in PocketBase v0.23. The method name and `AdminEmail` /
`AdminPassword` configuration fields remain available for existing callers.
PocketBase versions before v0.23 are not supported by this authentication path.

The endpoint and pagination contracts were checked against the official
[authentication](https://pocketbase.io/docs/authentication/) and
[records API](https://pocketbase.io/docs/api-records/) documentation on 2026-09-16
(documentation version v0.40.4). Tests use local HTTP test servers; this is not a
claim of live-server validation against every PocketBase release.

## Setup

```go
app := hyperchi.New()
pb := pocketbase.New(pocketbase.Config{
    BaseURL: "http://localhost:8090",
    Timeout: 30 * time.Second,
})
if err := pb.Init(app); err != nil {
    log.Fatal(err)
}
client := pb.Client()
```

`Init` checks the connection; it does not store the client on the app. Keep `pb`
or `pb.Client()` where your handlers can reach it. HyperChi has no other
PocketBase client.

For a server-side superuser client, set `AdminEmail`, `AdminPassword`, and
`AutoAuth: true` using credentials from your application configuration. Keep
superuser clients separate from user-scoped clients: a stored superuser token
has priority over a user token. User authentication changes client state, so
create a separate client for each user session rather than sharing it across users.

## Records

```go
posts := client.Collection("posts")
all, err := posts.GetFullList()
record, err := posts.GetOne("record-id")
created, err := posts.Create(map[string]any{"title": "Hello"})
updated, err := posts.Update("record-id", map[string]any{"title": "Updated"})
err = posts.Delete("record-id")
filtered, err := posts.Filter("published = true")
```

Check each operation's error before using its result. `Filter` returns the server's
first result page. `GetFullList` fetches all pages. Custom fields are accessed with
`record.Data["title"]`. Metadata such as `record.ID` has dedicated fields.

Collection names and record IDs are escaped as single URL path segments, so an ID
such as `../users` or `a/b?x=1` cannot reach another path or add a query. An
empty name or ID, `.`, and `..` are rejected before any request is sent.

## Retries

`RetryAttempts` is how many more times a GET (`GetOne`, `GetFullList`, `Filter`,
`HealthCheck`) is sent after a network error or a 429, 502, 503, or 504 answer.
The default, 0, sends each request once. The wait starts at `RetryDelay` (default
500 milliseconds) and doubles before each further attempt. Creates, updates,
deletes, and authentication are never retried, because a lost response would make
a retry repeat the change.

## Authentication

```go
err := client.AdminAuth(superuserEmail, superuserPassword)
auth, err := userClient.UserAuth("users", email, password)
```

Successful authentication stores the returned token for subsequent requests on
that client. `SetToken` sets a user token; it does not override an existing
superuser token. `AutoAuth` performs superuser authentication during `Init` before
the health check.

## Requirements and verification

Requires Go 1.26.8 or newer (this module's `go` directive), a running PocketBase instance,
and network access to its HTTP API. No C toolchain is needed: HyperChi's SQLite driver is
pure Go.

Run from `modules/pocketbase`:

```bash
go test -race ./...
go vet ./...
golangci-lint run ./...
```
