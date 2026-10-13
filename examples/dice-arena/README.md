# Dice Arena

A realtime multiplayer dice game, and HyperChi's WebSocket example. A lobby lists open tables
live. At a table, two to four players take turns at Hot Dice while anyone else watches. Each
page holds one WebSocket; every move goes up it as an htmx form message, and every result comes
back as HTML the server rendered, which the htmx 4 ws extension swaps into place.

No Node, no build step, no database. Game state lives in memory on the server.

## Run it

```bash
cd examples/dice-arena
go run .          # http://localhost:8080, and every LAN address it binds
task dev          # the same with Air hot reload (needs air on PATH)
task test         # go test -race ./...
task lint         # gofmt, go vet, golangci-lint
```

The server binds `0.0.0.0` and logs each URL it answers on, so a phone on the same network can
join. In development a taken port falls through to the next free one. Templates and static files
are embedded in the binary, so it runs from any working directory.

Open the page in two browsers (or one normal and one private window), pick a name in each, open
a table in one and join it from the other.

## Production

```bash
ENVIRONMENT=production SECRET_KEY="$(openssl rand -base64 32)" PORT=8080 go run .
```

- `ENVIRONMENT=production` (read by `config.FromEnv`) turns on the production defaults: strict
  CSP and HSTS headers, generic error pages, `Secure` cookies, an exact port bind, and the
  WebSocket hub's same-origin handshake check.
- `SECRET_KEY` (at least 32 bytes) is HyperChi's application secret; it signs the player
  cookie. Production refuses to start without it. Keep it stable across restarts and
  deployments, or every player gets a new identity. To rotate, move the old key to
  `SECRET_KEY_PREVIOUS` (comma-separated) and set a new `SECRET_KEY`: existing cookies keep
  verifying and new ones use the new key. In development it may be unset, and a random key is
  made at startup.
- `ALLOWED_ORIGINS=play.example.com,*.example.com` lets pages served from other hosts open a
  socket. The app passes the list to `hub.SetOriginPatterns`. Same-origin pages need nothing.
- Behind a reverse proxy, set `TRUSTED_PROXIES` to its CIDRs so rate limits see real clients,
  and make sure the proxy forwards WebSocket upgrades.

## Hot Dice

- Six dice. On your turn, roll all six.
- After every roll, set aside at least one scoring die. Then roll the rest again, or bank the
  points you set aside this turn.
- A roll with nothing that scores is a bust: the turn's points are lost and the dice pass on.
- Set aside all six and you have hot dice: roll all six again and keep building the turn.
- A turn has at most four rolls. After the last one, bank.
- First to the target (2,000, 5,000, or 10,000, picked when the table opens) wins.

| Dice | Points |
| --- | --- |
| Each 1 | 100 |
| Each 5 | 50 |
| Three 1s | 1,000 |
| Three of any other face | face × 100 |
| Each extra matching die | doubles it |
| Straight, 1 to 6 | 1,500 |
| Three pairs | 750 |

Dice set aside together must all score. Keyboard: `1` to `6` pick dice, `R` rolls, `B` banks;
every control is also a native button or checkbox, so Tab and Space work everywhere.

## How it fits together

| File | Job |
| --- | --- |
| `game.go` | The rules: `Score`, `HasScore`, and `Game`, with no I/O. Dice come from an injected `Roller`; production uses `CryptoRoller` (crypto/rand). |
| `room.go` | `Arena` (every table, the lobby listing) and `Room` (seats, pages, the game). One mutex per room; every change and the pushes announcing it happen under it. |
| `ws.go` | The two socket routes: the lobby feed and a table. |
| `routes.go` | Pages, forms, and embedded static files. |
| `identity.go` | The signed player cookie and the middleware that reads it. |
| `main.go` | Configuration and `newApp`, which the tests share. |

### Identity

A middleware gives every visitor a random player ID. The ID and the display name travel
together in one cookie written with `c.SetSignedCookie` and read with
`c.SignedCookie(name, 30*24*time.Hour)`: the framework signs the value with `SECRET_KEY`
(HMAC-SHA256 over the cookie name, value, and issue time) and sends it `HttpOnly`,
`SameSite=Lax`, and `Secure` in production. Signing both in one value means a name cannot be
moved to another player, and a tampered, expired, or unsigned cookie just gets a new ID. The
value is signed, not encrypted, so it holds nothing secret. The WebSocket hub learns who a
connection is from that same cookie through `SetIdentifyUser`, never from anything the client
sends.

Two layers stop cross-site form posts. A cross-site post arrives without the Lax cookies and has
no identity to act as. `Middleware.CSRF()` adds a double-submit token: the layout puts it in
`hx-headers:inherited` on `<body>`, so every htmx post carries `X-CSRF-Token`, and a post without
it gets a 403. The lobby list is one render pushed to every lobby page, so its Join buttons rely
on the header rather than a per-visitor hidden field.

### Disconnects

A player whose last page closes keeps their seat for 45 seconds and the roster shows them as
reconnecting. Reloading the page, or the ws extension reconnecting by itself, gives the seat
back. When the window runs out the seat is released; if it was their turn, the turn passes on,
and a game left with one player ends with that player as the winner. A room with no seats and
no open pages closes and leaves the lobby.

The layout sets `htmx.config.ws.pauseOnBackground` to false: by default the extension closes the
socket of a hidden tab, which here would look like a player leaving.

## WebSocket protocol

Pages talk to `/ws/r/{code}` (a table) and `/ws/lobby` (the open-tables list, server to client
only). Client messages are what the htmx 4 ws extension sends for a form with `hx-ws:send`: the
form's fields as a JSON object plus a `headers` object.

| Client sends | Fields | Server answers |
| --- | --- | --- |
| `sit` | | Seats the sender if the table is open. Everyone gets the new board and tables. |
| `leave` | | Frees the sender's seat. Mid-game, the turn moves on. |
| `start` | | Starts a game with two or more seated players. |
| `rematch` | | Starts a new game after one ends; the first roller rotates. |
| `roll` | `keep`: die indexes 0 to 5, required after the first roll | Sets those dice aside and rolls the rest. |
| `bank` | `keep` | Sets those dice aside and banks the turn. |
| `preview` | `keep` | What the selection would score, to the sender only. Nothing changes. |

Every message is decoded with `hyperchi.BindMessage[Action]`: `action` must be one of the names
above and `keep` must be numbers from 0 to 5. A message that fails, a move out of turn, or a move
the rules refuse is answered with an error swapped into `#flash` on the sender's page only.

| Server pushes | Target | To | Template |
| --- | --- | --- | --- |
| Roster, scores, log | `#board` | everyone at the table, one render (`app.Room`) | `board` |
| Dice and controls | `#table` | each page, rendered for that viewer (`Socket.Swap`) | `table` |
| What just happened | `#announce` (an `aria-live` region) | each page, with "Your turn." for the player up next | `announce` |
| Selection score | `#preview` | the sender | `preview` |
| Errors | `#flash` | the sender | `flash` |
| Open tables | `#open-rooms` | every lobby page (`app.Room("lobby")`) | `room-list` |

Every push is the hub's htmx message, `{"type": "htmx", "target": ..., "swap": "innerHTML",
"content": ...}`, which the ws extension swaps without any page script.

## Framework calls used, and why

- `app.WebSocket(pattern, func(s *hyperchi.Socket) error)` for both sockets. The handler owns
  the connection's whole life: `s.Receive` in a loop, return when the page leaves.
- `hyperchi.BindMessage[Action]` validates socket messages with the same `validate` tags and
  `Validate()` method as HTTP forms; `hyperchi.FieldErrors` tells a bad message from a broken
  connection.
- `s.Join` plus `app.Room(name).Swap` render the shared board once per change and fan it out.
  `s.Swap` sends the per-viewer table, because a player's own dice are checkboxes and everyone
  else's are not.
- `Extensions().GetWebSocketHub().SetIdentifyUser` ties sockets to the signed cookie, and
  `SetOriginPatterns` widens the production origin check when asked.
- `Extensions().EnableExtensionAssets()` serves the ws extension at `/hyperchi/js/hx-ws.js`.
  The app does not call `EnableWebSocket` or `EnableAllExtensions`: that generic `/ws` endpoint
  lets a client join any room and relay messages into it, and every message here has to go
  through the game.
- `c.View` for pages, `c.Fragment` and `c.Status(422)` to re-render the lobby forms with
  `hyperchi.FieldErrors`, `c.Redirect` (an `HX-Redirect` for htmx, a 303 otherwise),
  `c.HX().Trigger`, `hyperchi.NotFound` for a missing table, and `app.SetGlobal` for values
  every template reads.
- `app.LoadTemplatesFS` and `app.StaticFS` serve the embedded `templates/` and `static/`
  directories: the binary needs no files beside it. Static files get a content-hash `ETag`,
  `nosniff`, an hour of caching in production, and `no-cache` in development.
- `app.Middleware.CSRF` on every post, `app.Middleware.RateLimit` on the form posts; `app.Serve`
  for the port fallback, address logging, and graceful shutdown.
- `hc-data`, `hc-on`, `hc-bind`, `hc-show`, and `hc-text` for local UI state (`static/js/arena.js`):
  the reconnecting banner, the invite button, keyboard shortcuts, and enabling the set-aside
  buttons once a die is picked. The server still checks every move.

## Accessibility and motion

Dice are real checkboxes with their value as text. Turn changes, rolls, and results are
announced through a polite live region, errors through `role="alert"`. Focus rings are visible
on the light page and on the felt. Rolled dice tumble in once with a short stagger; under
`prefers-reduced-motion` they appear in place, and the live-turn pulse stops. The layout holds
at 360px wide.

## Tests

```bash
go test -race -count=1 -cover ./...
```

- `game_test.go`: scoring and turn rules, table-driven, with scripted dice.
- `room_test.go`: four players and a stream of watchers hammering one room at once under
  `-race`; seat limits, grace expiry, and room cleanup with fake timers.
- `http_test.go`: the lobby forms (422 re-renders), signed cookies, the CSRF token (403
  without it; a token from any render works, since each render masks it differently), pages, static files and their cache headers,
  a run from another working directory, and that `ENVIRONMENT=production` reaches the app (CSP,
  `Secure` cookies, cross-origin sockets refused).
- `ws_test.go`: real WebSocket clients against `httptest`. Two players take turns, an
  out-of-turn roll reaches only its sender, broadcasts reach both, a dropped player rejoins
  their seat, an expired seat ends the game, a cleared error, malformed messages, spectators, and
  the live lobby.
  Tests read pushes until the one they expect, with a deadline; nothing sleeps.
