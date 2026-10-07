---
title: Realtime
section: Built-ins
order: 110
next: directives
description: Send new HTML when something changes. Server-sent events and WebSocket rooms let your Go application keep browsers up to date.
---

## Two ways to push HTML {#overview}

HyperChi sends HTML, not data, over both transports, and the htmx 4 extensions swap it into the page.

- **WebSocket routes** (`app.WebSocket`) are two-way. Your handler reads messages from one visitor, and `app.Room(name)` broadcasts to every socket that joined a room. Use them for chat, games, and anything a visitor also sends to.
- **SSE channels** (`app.Extensions().EnableSSE`) are one-way. The server broadcasts to a named channel and every subscribed page swaps the HTML in. Use them for progress bars, counters, and dashboards.

The framework embeds the two htmx 4.0.0 extension scripts. Serve them with one call, then load them after htmx:

```go
app := hyperchi.New()
app.Extensions().EnableExtensionAssets() // serves /hyperchi/js/hx-sse.js and hx-ws.js
```

```html
<script src="https://cdn.jsdelivr.net/npm/htmx.org@4.0.0/dist/htmx.min.js"></script>
<script src="/hyperchi/js/hx-sse.js"></script>
<script src="/hyperchi/js/hx-ws.js"></script>
```

Load them as plain scripts in the head. With `defer`, htmx processes the page before an extension registers, and the first `hx-sse:connect` never connects. Both extensions reconnect on their own with a growing delay, and pause while the tab is in the background.

## A chat room with a WebSocket route {#example}

A socket handler serves one connection for its whole life. `s.Receive()` returns the next message, `hyperchi.BindMessage` validates it with the same `form` and `validate` tags as a form, and `app.Room(room).Swap` renders a template once and sends it to everyone in the room.

```go
type ChatLine struct {
    Text string `form:"text" validate:"required,max=280"`
}

app.WebSocket("/ws/chat/{room}", func(s *hyperchi.Socket) error {
    room := s.Context().Param("room")
    // The room authorizer only checks joins a client asks for. Authorize
    // access to room in route middleware, or here, before joining.
    s.Join(room)
    for {
        msg, err := s.Receive()
        if err != nil {
            return err // ErrSocketClosed when the visitor leaves
        }
        line, err := hyperchi.BindMessage[ChatLine](msg)
        if fields := hyperchi.FieldErrors(err); fields != nil {
            if err := s.Swap("#chat-form", "outerHTML", "chat-form", hyperchi.H{"errors": fields}); err != nil {
                return err
            }
            continue
        } else if err != nil {
            return err
        }
        data := hyperchi.H{"user": s.UserID(), "text": line.Text}
        if err := app.Room(room).Swap("#feed", "beforeend", "chat-line", data); err != nil {
            return err
        }
    }
}, requireLogin)
```

The page connects with `hx-ws:connect`, and any element with `hx-ws:send` posts its form values to the server:

```html
<div hx-ws:connect="/ws/chat/general">
  <ul id="feed" aria-live="polite"></ul>
  <form id="chat-form" hx-ws:send>
    <input name="text" required maxlength="280">
    <button>Send</button>
  </form>
</div>
```

Templates can render those attributes with escaped values: `<div {{wsConnect "/ws/chat/general"}}>`, `{{wsSend ""}}`, and `{{wsClose "done"}}`.

## Sockets and rooms {#sockets}

A socket handler returns when the visitor leaves. Returning `hyperchi.ErrSocketClosed` or the error `Receive` gave ends it normally; any other error is logged and closes the socket with an internal-error status.

| Call | Does |
| --- | --- |
| `s.Receive()`, `s.Wait()` | Next message; or discard messages until the client leaves, for push-only sockets. |
| `s.Swap(target, swap, template, data)` | Render a template and swap it into this visitor's page only. |
| `s.SendHTML`, `s.SendJSON` | Send trusted markup, or a data-only message for an `htmx:ws:after:message:incoming` listener. |
| `s.Join(room)`, `s.Leave(room)` | Room membership. |
| `s.ID()`, `s.UserID()` | Connection ID and the server-side identity. |
| `app.Room(name).Swap(...)`, `.SendHTML`, `.SendJSON`, `.Reload`, `.Redirect` | Broadcast to the whole room from any handler or goroutine. |

A push-only feed is a handler that joins and waits. Anything else in your program can then broadcast:

```go
app.WebSocket("/ws/notices", func(s *hyperchi.Socket) error {
    s.Join("notices")
    return s.Wait()
})

func announce(app *hyperchi.HyperChi) error {
    return app.Room("notices").Swap("#notices", "afterbegin", "notice", hyperchi.H{"text": "Deploy at 5"})
}
```

The `swap` argument is any htmx swap style: `innerHTML`, `beforeend`, `outerHTML`, and so on. A route socket joins rooms from its own handler; the `?rooms=` query string belongs to the generic `/ws` endpoint that `EnableWebSocket` mounts.

## Identity, rooms, and origins {#identity}

Three settings on the shared hub decide who may connect and what they may do. Set them before the server starts.

```go
hub := app.Extensions().GetWebSocketHub()
hub.SetOriginPatterns([]string{"app.example.com"})
hub.SetIdentifyUser(func(r *http.Request) string {
    return security.GetUserIDFromContext(r.Context())
})
hub.SetRoomAuthorizer(func(conn *websocket.WebSocketConnection, room string) bool {
    return userMayJoin(conn.UserID, room)
})
```

- **Origin.** Production accepts same-origin handshakes only, which stops other sites from opening a socket with your visitor's cookies. `SetOriginPatterns` allows more hosts, such as `*.example.com`. In development the policy is relaxed so a phone or a Vite dev server on another port can connect; an explicit policy always wins in every environment.
- **Identity.** A connection's `UserID` comes only from `SetIdentifyUser`, which reads the handshake request, usually the session or a signed cookie. It never comes from a query string, a header, or a message, so a client cannot claim to be someone else. Without a function it defaults to the authenticated user, and anonymous sockets have an empty ID.
- **Rooms.** A client `join_room` message is refused until you set `SetRoomAuthorizer`, which then also checks `?rooms=` on the generic endpoint. Joins made by your own code (`s.Join`, `JoinRoom`) skip it, so guard those with route middleware. A client can post only to rooms it has joined.

`hub.SetReceiveOnly(true)` closes the generic endpoint on any client message, for pages that only listen. Route sockets read messages in your handler, so you decide there.

## Server-sent events {#sse}

Mount the SSE endpoint once, subscribe pages by channel name, and broadcast HTML. `BroadcastSwap` sends an unnamed message, which the extension swaps into the connecting element using its `hx-swap`.

```go
app.Extensions().EnableSSE("/sse")

func publishCount(app *hyperchi.HyperChi, n int) error {
    html, err := app.RenderToString("visitor-count", hyperchi.H{"n": n})
    if err != nil {
        return err
    }
    app.Extensions().GetSSEHub().BroadcastSwap("stats", string(html))
    return nil
}
```

```html
<div hx-sse:connect="/sse?channels=stats" hx-swap="innerHTML">
  Waiting for the first count…
</div>
```

Separate channels with commas to subscribe to several. The template helpers `{{sseConnect "/sse?channels=stats"}}`, `{{sseSwap "row"}}`, and `{{sseClose "done"}}` render the same attributes with escaping.

Named events behave differently. htmx 4 dispatches `BroadcastHTML(channel, "row", html)` as a DOM event and does not swap it, so the page needs a handler: `{{sseSwap "row"}}` swaps that event's data into the element. To end a stream, broadcast an event and name it in `hx-sse:close`:

```go
hub := app.Extensions().GetSSEHub()
hub.Broadcast("jobs", sse.SSEMessage{Event: "done", Data: "done"})
```

Without an authorizer, a client may name any channel in the URL, which suits public broadcasts. Before a channel carries per-user data, install one: it sees the request and each requested channel, and a refusal answers 403 before the stream opens.

```go
hub := app.Extensions().GetSSEHub()
hub.SetChannelAuthorizer(func(r *http.Request, channel string) bool {
    if channel == "news" {
        return true // public
    }
    id := security.GetUserIDFromContext(r.Context())
    return id != "" && channel == "user-"+id // signed in, and only your own
})
```

## Live regions never get view transitions {#transitions}

A view transition cross-fades the whole document. On a region that updates every second, that repaints the page on each message and resets stateful components. Keep `transitions` out of the `htmx-config` meta tag, and never put `transition:true` in the `hx-swap` of an element that receives SSE or WebSocket messages. Use it only on a deliberate, low-frequency swap, such as the result of a form post.

## Next steps {#next}

[Watch the live server metrics](/#proof), then [open the WebSocket room](/#realtime) in two tabs. The [Dice Arena example](/docs/apps) is a full two-way WebSocket app, and Mailgrid streams job progress over SSE. For public hosting, limit connections and request bodies, protect form submissions from cross-origin requests, and escape all visitor text before broadcasting; the [security page](/docs/security) covers these boundaries. [Directives](/docs/directives) handle the local state around a live region.
