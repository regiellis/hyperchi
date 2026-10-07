package hyperchi

import (
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"time"

	cws "github.com/coder/websocket"
	"github.com/regiellis/hyperchi/hyperchi/extensions/websocket"
)

// ErrSocketClosed reports that the client went away. Receive returns it (or an
// error wrapping it), and a SocketHandler that returns it ends normally.
var ErrSocketClosed = errors.New("hyperchi: socket closed")

// SocketHandler serves one WebSocket connection for its whole life: the
// connection closes when the handler returns. Return ErrSocketClosed, or the
// error Receive gave you, when the client leaves; any other error is logged
// and closes the socket with an internal-error status.
type SocketHandler func(s *Socket) error

// WebSocket registers a WebSocket route. The handshake runs through optional
// middleware like any GET route, so auth middleware can guard it.
//
// Sockets share the app's hub with Extensions().EnableWebSocket, so they join
// rooms that app.Room broadcasts to, and they follow the hub's origin policy
// (same-origin in production; set Extensions().GetWebSocketHub().
// SetOriginPatterns for other origins) and its server-side user identity.
// The page needs the htmx 4 ws extension (Extensions().EnableExtensionAssets
// serves it at /hyperchi/js/hx-ws.js).
//
//	app.WebSocket("/chat/{room}", func(s *hyperchi.Socket) error {
//		room := s.Context().Param("room")
//		s.Join(room)
//		for {
//			msg, err := s.Receive()
//			if err != nil {
//				return err
//			}
//			line, err := hyperchi.BindMessage[ChatLine](msg)
//			if fields := hyperchi.FieldErrors(err); fields != nil {
//				if err := s.Swap("#chat-form", "outerHTML", "chat-form", hyperchi.H{"errors": fields}); err != nil {
//					return err
//				}
//				continue
//			} else if err != nil {
//				return err
//			}
//			if err := app.Room(room).Swap("#feed", "beforeend", "chat-line", line); err != nil {
//				return err
//			}
//		}
//	})
func (g *HyperChi) WebSocket(pattern string, h SocketHandler, mw ...Middleware) {
	if h == nil {
		panic(fmt.Sprintf("hyperchi: nil SocketHandler for %s", pattern))
	}
	hub := g.extensions.prepareWebSocketHub(pattern)
	g.Get(pattern, func(c *Context) error {
		conn, err := hub.Accept(c.w, c.r)
		if err != nil {
			// Accept has already answered the client (400, 403, or 426): a bad
			// handshake is the client's error, not the app's.
			return nil
		}
		s := &Socket{c: c, conn: conn, hub: hub, incoming: make(chan []byte, 16)}
		go s.readLoop()
		herr := h(s)
		// Let the writer deliver what the handler queued before tearing the
		// connection down; a client that is already gone ends the wait early.
		conn.Flush(socketFlushTimeout)
		if herr != nil && !errors.Is(herr, ErrSocketClosed) {
			log.Printf("hyperchi: socket %s %s: %v", c.r.URL.Path, conn.ID, herr)
			if err := conn.Conn.Close(cws.StatusInternalError, "internal error"); err != nil && cws.CloseStatus(err) == -1 && !errors.Is(err, net.ErrClosed) {
				log.Printf("hyperchi: socket %s close: %v", conn.ID, err)
			}
		}
		hub.RemoveConnection(conn.ID)
		return nil
	}, mw...)
}

// socketFlushTimeout bounds how long a returning SocketHandler's queued
// messages get to reach the client before the connection closes.
const socketFlushTimeout = 2 * time.Second

// Socket is one WebSocket connection inside a SocketHandler.
type Socket struct {
	c        *Context
	conn     *websocket.WebSocketConnection
	hub      *websocket.WebSocketHub
	incoming chan []byte
	readErr  error
}

// Context is the handshake request: route parameters, query, session, user.
// Do not write an HTTP response through it.
func (s *Socket) Context() *Context { return s.c }

// ID identifies the connection within the app's hub.
func (s *Socket) ID() string { return s.conn.ID }

// UserID is the server-side identity of the connection's user, or "" for an
// anonymous socket.
func (s *Socket) UserID() string { return s.conn.UserID }

// readLoop keeps a read pending for the life of the connection, which is also
// what lets the hub's keepalive pings see their pongs.
func (s *Socket) readLoop() {
	defer close(s.incoming)
	for {
		data, err := s.conn.ReadMessage()
		if err != nil {
			s.readErr = err
			return
		}
		select {
		case s.incoming <- data:
		case <-s.conn.Context.Done():
			return
		}
	}
}

// Receive waits for the next client message. When the client goes away it
// returns an error that matches ErrSocketClosed with errors.Is.
func (s *Socket) Receive() (*SocketMessage, error) {
	select {
	case data, ok := <-s.incoming:
		if !ok {
			return nil, s.closedErr()
		}
		return parseSocketMessage(data), nil
	case <-s.conn.Context.Done():
		return nil, ErrSocketClosed
	}
}

func (s *Socket) closedErr() error {
	switch cws.CloseStatus(s.readErr) {
	case cws.StatusNormalClosure, cws.StatusGoingAway, cws.StatusNoStatusRcvd:
		return ErrSocketClosed
	}
	if s.readErr == nil {
		return ErrSocketClosed
	}
	return fmt.Errorf("%w: %w", ErrSocketClosed, s.readErr)
}

// Wait discards client messages until the client leaves, for sockets that
// only push (join a room, then Wait). It returns ErrSocketClosed.
func (s *Socket) Wait() error {
	for {
		if _, err := s.Receive(); err != nil {
			return err
		}
	}
}

// Join adds the socket to a room, so app.Room(name) broadcasts reach it.
func (s *Socket) Join(room string) { s.hub.JoinRoom(s.conn.ID, room) }

// Leave removes the socket from a room.
func (s *Socket) Leave(room string) { s.hub.LeaveRoom(s.conn.ID, room) }

// Swap renders a template without a layout and sends it to this client for
// the htmx ws extension to swap into target ("#feed") with swap ("innerHTML",
// "beforeend", "outerHTML", ...).
func (s *Socket) Swap(target, swap, name string, data any) error {
	html, err := s.c.app.renderFragment(s.c.r, name, data)
	if err != nil {
		return err
	}
	return s.send(websocket.WebSocketMessage{Type: "htmx", Target: target, Swap: swap, Content: html})
}

// SendHTML sends trusted markup for the htmx ws extension to swap.
func (s *Socket) SendHTML(target, swap string, html template.HTML) error {
	return s.send(websocket.WebSocketMessage{Type: "htmx", Target: target, Swap: swap, Content: string(html)})
}

// SendJSON sends a data-only message; the page handles it in an
// htmx:ws:after:message:incoming listener.
func (s *Socket) SendJSON(event string, data any) error {
	return s.send(websocket.WebSocketMessage{Type: "json", Event: event, Data: data})
}

func (s *Socket) send(msg websocket.WebSocketMessage) error {
	select {
	case <-s.conn.Context.Done():
		return ErrSocketClosed
	default:
	}
	s.hub.SendToConnection(s.conn.ID, msg)
	return nil
}

// SocketMessage is one message from the client. The htmx 4 ws extension sends
// the triggering form's values plus the htmx request headers; other clients
// may send any JSON object or plain text.
type SocketMessage struct {
	raw     []byte
	values  map[string]any
	headers map[string]string
}

func parseSocketMessage(data []byte) *SocketMessage {
	m := &SocketMessage{raw: data}
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		return m
	}
	if h, ok := obj["headers"].(map[string]any); ok {
		m.headers = make(map[string]string, len(h))
		for k, v := range h {
			m.headers[http.CanonicalHeaderKey(k)] = fmt.Sprint(v)
		}
		delete(obj, "headers")
	}
	m.values = obj
	return m
}

// Text is the message as sent.
func (m *SocketMessage) Text() string { return string(m.raw) }

// Values are the message's fields, or nil when the message was not a JSON
// object.
func (m *SocketMessage) Values() map[string]any { return m.values }

// Value returns one field as a string (the first one for a repeated field).
func (m *SocketMessage) Value(key string) string {
	switch v := m.values[key].(type) {
	case nil:
		return ""
	case string:
		return v
	case []any:
		if len(v) == 0 {
			return ""
		}
		return fmt.Sprint(v[0])
	default:
		return fmt.Sprint(v)
	}
}

// Header returns an htmx request header the extension sent, such as
// "HX-Source" (the element that sent the message) or "HX-Current-URL".
func (m *SocketMessage) Header(key string) string { return m.headers[http.CanonicalHeaderKey(key)] }

// Bind decodes the message's fields into dst, a pointer to a struct, with the
// same form tags and validate rules as Context.Bind. A failed check is a 422
// *HTTPError whose Fields hold per-field messages.
func (m *SocketMessage) Bind(dst any) error {
	rv := reflect.ValueOf(dst)
	if rv.Kind() != reflect.Pointer || rv.IsNil() || rv.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("hyperchi: Bind needs a non-nil pointer to a struct, got %T", dst)
	}
	if m.values == nil {
		return BadRequest("The message is not a JSON object.")
	}
	form := url.Values{}
	for k, v := range m.values {
		switch vv := v.(type) {
		case []any:
			for _, item := range vv {
				form.Add(k, fmt.Sprint(item))
			}
		case nil:
		default:
			form.Add(k, fmt.Sprint(vv))
		}
	}
	fields := map[string][]string{}
	if err := decodeForm(rv.Elem(), form, nil, fields); err != nil {
		return err
	}
	return finishBind(dst, rv, fields)
}

// BindMessage decodes and validates a socket message into a new T, the
// socket counterpart of Bind.
func BindMessage[T any](m *SocketMessage) (T, error) {
	var v T
	err := m.Bind(&v)
	return v, err
}

// Room addresses every socket that joined name, from any handler or
// goroutine:
//
//	app.Room("lobby").Swap("#feed", "beforeend", "chat-line", line)
func (g *HyperChi) Room(name string) *Room {
	return &Room{app: g, name: name}
}

// Room broadcasts to the sockets in one room.
type Room struct {
	app  *HyperChi
	name string
}

func (r *Room) hub() *websocket.WebSocketHub { return r.app.extensions.GetWebSocketHub() }

// Swap renders a template once, without a layout, and sends it to every
// socket in the room for htmx to swap into target with swap.
func (r *Room) Swap(target, swap, name string, data any) error {
	html, err := r.app.renderFragment(nil, name, data)
	if err != nil {
		return err
	}
	r.hub().BroadcastSwap(r.name, target, swap, html)
	return nil
}

// SendHTML sends trusted markup to every socket in the room.
func (r *Room) SendHTML(target, swap string, html template.HTML) {
	r.hub().BroadcastSwap(r.name, target, swap, string(html))
}

// SendJSON sends a data-only message to every socket in the room.
func (r *Room) SendJSON(event string, data any) { r.hub().BroadcastJSON(r.name, event, data) }

// Reload asks every page in the room to reload.
func (r *Room) Reload() { r.hub().BroadcastReload(r.name) }

// Redirect sends every page in the room to url.
func (r *Room) Redirect(url string) { r.hub().BroadcastRedirect(r.name, url) }

// renderFragment renders a template without a layout to a string. A nil
// request renders with app globals only, for broadcasts outside a request.
func (g *HyperChi) renderFragment(r *http.Request, name string, data any) (string, error) {
	tr := g.Template(name)
	tr.Layout = "none"
	base := map[string]any{}
	if r != nil {
		base = g.templateContext(r)
	} else {
		for k, v := range g.globals {
			base[k] = v
		}
	}
	return tr.Render(data, base, r)
}
