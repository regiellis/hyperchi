// Package websocket is HyperChi's WebSocket hub for the htmx 4 ws extension
// (hx-ws.js). The hub tracks connections and rooms, owns every write through
// one write pump per connection, pings to keep connections alive, and applies
// the origin policy and server-side identity to each handshake. Route-level
// sockets (app.WebSocket) run on the same hub through Accept.
//
// Rooms: server code joins a connection to a room with JoinRoom (Socket.Join
// in a route-level socket), and that is never checked. A client asks to join
// with a join_room message, or with "?rooms=" on the connect URL when the hub
// runs its own read loop (HTTPHandler, which EnableWebSocket mounts). The hub
// refuses every join_room message until the application installs a room
// authorizer:
//
//	hub.SetRoomAuthorizer(func(conn *websocket.WebSocketConnection, room string) bool {
//		return canSee(conn.UserID, room) // the app's own access check
//	})
//
// Once an authorizer is set it also decides the "?rooms=" rooms. Without one,
// those keep the older contract: authorize them in HTTP middleware.
package websocket

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/regiellis/hyperchi/hyperchi/extensions"
)

// WebSocketExtension registers the htmx ws extension script, its attributes,
// and the WebSocket template helpers, and owns the hub.
type WebSocketExtension struct {
	extensions.BaseExtension
	hub *WebSocketHub
}

// NewWebSocketExtension returns the extension with a new hub. The hub's
// cleanup goroutine runs for the life of the process.
func NewWebSocketExtension() *WebSocketExtension {
	ext := &WebSocketExtension{
		BaseExtension: extensions.BaseExtension{},
		hub:           NewWebSocketHub(),
	}

	ext.SetName("ws").
		SetVersion("4.0.0").
		SetDescription("htmx 4 WebSocket extension for bidirectional real-time communication").
		AddScript("/hyperchi/js/hx-ws.js").
		AddAttribute("hx-ws:connect").
		AddAttribute("hx-ws:send")

	return ext
}

// Initialize adds the WebSocket template helpers (see TemplateFuncs) to the
// registry.
func (e *WebSocketExtension) Initialize(registry *extensions.Registry) error {
	for name, fn := range TemplateFuncs() {
		registry.AddTemplateFunc(name, fn)
	}
	return nil
}

// GetHub returns the extension's hub.
func (e *WebSocketExtension) GetHub() *WebSocketHub {
	return e.hub
}

// WebSocketHub tracks WebSocket connections and the rooms they join. Its
// methods are safe for concurrent use.
//
// Locking: mu guards the maps, stats, the policy fields, and every
// connection's Rooms slice. It is not reentrant, so a method holding it must
// release it before calling anything that takes it again (JoinRoom and
// LeaveRoom broadcast after unlocking; cleanup removes after unlocking).
// Senders copy the connections they need under mu and enqueue after
// releasing it.
//
// Channel ownership: a connection's Send channel is never closed. Senders
// enqueue with trySend, which never blocks and checks the connection's
// context, and RemoveConnection only cancels that context. So removal may run
// any number of times, from any goroutine, without a send on a closed channel.
type WebSocketHub struct {
	// connections is keyed by connection ID.
	connections map[string]*WebSocketConnection

	// rooms maps a room name to its members, keyed by connection ID.
	rooms map[string]map[string]*WebSocketConnection

	mu sync.RWMutex

	// acceptOpts holds the origin policy for the handshake.
	acceptOpts *websocket.AcceptOptions

	// originConfigured records that the application set the origin policy
	// (SetOriginPatterns or SetInsecureSkipVerify), so
	// UseDevelopmentOriginPolicy leaves it alone.
	originConfigured bool

	// receiveOnly permits server broadcasts but rejects client application messages.
	receiveOnly bool

	// identifyUser derives the authenticated user ID for a handshake request,
	// server-side. It is nil by default (connections are anonymous), so
	// untrusted client input can never set a connection's identity. Apps set
	// it with SetIdentifyUser, usually reading the authenticated session, for
	// example security.GetUserIDFromContext(r.Context()).
	identifyUser func(*http.Request) string

	// identifyConfigured records that the application called SetIdentifyUser,
	// so the default EnableWebSocket installs never replaces it.
	identifyConfigured bool

	// authorizeRoom decides joins a client asks for (see SetRoomAuthorizer).
	// Nil refuses join_room messages.
	authorizeRoom func(conn *WebSocketConnection, room string) bool

	// joinNotice logs, once per hub, that join_room needs an authorizer.
	joinNotice sync.Once

	stats WebSocketStats
}

// WebSocketStats is a snapshot of hub counters, as GetStats returns it.
type WebSocketStats struct {
	TotalConnections  int64     `json:"total_connections"`
	ActiveConnections int       `json:"active_connections"`
	TotalRooms        int       `json:"total_rooms"`
	MessagesSent      int64     `json:"messages_sent"`
	MessagesReceived  int64     `json:"messages_received"`
	LastActivity      time.Time `json:"last_activity"`
}

// NewWebSocketHub returns a hub that accepts same-origin handshakes only, and
// starts its cleanup goroutine. The goroutine never stops, so create one hub
// per process.
func NewWebSocketHub() *WebSocketHub {
	hub := &WebSocketHub{
		connections: make(map[string]*WebSocketConnection),
		rooms:       make(map[string]map[string]*WebSocketConnection),
		// With no OriginPatterns and InsecureSkipVerify false, coder/websocket
		// enforces same-origin on the handshake, which blocks Cross-Site
		// WebSocket Hijacking. SetOriginPatterns allows specific cross-origin
		// hosts; SetInsecureSkipVerify is for local development only.
		acceptOpts: &websocket.AcceptOptions{},
	}

	go hub.cleanupLoop()

	return hub
}

// SetOriginPatterns configures the origin hosts allowed to open a WebSocket.
// Patterns follow coder/websocket matching (e.g. "example.com",
// "*.example.com"). With no patterns set, only same-origin requests are
// accepted. Call before the hub begins serving.
func (h *WebSocketHub) SetOriginPatterns(patterns []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.acceptOpts.OriginPatterns = patterns
	h.originConfigured = true
}

// SetReceiveOnly makes connections receive-only for browser clients. When enabled,
// client application messages close the connection with a policy violation;
// WebSocket protocol ping/pong frames still work. Initial rooms in the handshake
// remain supported: the room authorizer decides them when one is set, and the
// application's HTTP middleware must authorize them otherwise. Server-side
// broadcasts are unaffected. Configure before serving connections.
func (h *WebSocketHub) SetReceiveOnly(enabled bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.receiveOnly = enabled
}

// SetInsecureSkipVerify disables origin verification on the handshake. This
// removes Cross-Site WebSocket Hijacking protection, so use it for local
// development only, never in production. Call before serving.
func (h *WebSocketHub) SetInsecureSkipVerify(skip bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.acceptOpts.InsecureSkipVerify = skip
	h.originConfigured = true
}

// UseDevelopmentOriginPolicy turns off origin verification so a remote
// browser can reach the socket during local development, for example through
// a Vite dev server bound to 0.0.0.0 and opened over SSH or the LAN, where the
// handshake's Origin does not match the backend's Host. It does nothing once
// the application has called SetOriginPatterns or SetInsecureSkipVerify.
// EnableWebSocket calls it outside production; production keeps same-origin.
// It reports whether it relaxed the policy.
func (h *WebSocketHub) UseDevelopmentOriginPolicy() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.originConfigured {
		return false
	}
	h.acceptOpts.InsecureSkipVerify = true
	return true
}

// SetIdentifyUser installs a server-side function that derives the
// authenticated user ID for a handshake request. The connection's UserID
// comes from this function only, never from client-supplied query parameters
// or headers, so a client cannot spoof it. A typical implementation reads the
// authenticated session:
//
//	hub.SetIdentifyUser(func(r *http.Request) string {
//		return security.GetUserIDFromContext(r.Context())
//	})
func (h *WebSocketHub) SetIdentifyUser(fn func(*http.Request) string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.identifyUser = fn
	h.identifyConfigured = true
}

// SetRoomAuthorizer installs fn to decide the rooms a client asks to join: a
// join_room message, and the "?rooms=" list of an HTTPHandler handshake. fn
// sees the connection, including its server-side UserID (see
// SetIdentifyUser), and the room name; false refuses the join. Without an
// authorizer the hub refuses every join_room message. Joins made by server
// code (JoinRoom, Socket.Join) never consult it. fn runs without the hub's
// lock held, so it may call hub methods. Configure before serving.
func (h *WebSocketHub) SetRoomAuthorizer(fn func(conn *WebSocketConnection, room string) bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.authorizeRoom = fn
}

// roomAuthorizer returns the installed room authorizer, or nil.
func (h *WebSocketHub) roomAuthorizer() func(*WebSocketConnection, string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.authorizeRoom
}

// clientMayJoin reports whether a join_room message from conn may join room.
// With no authorizer it refuses, and says why once per hub.
func (h *WebSocketHub) clientMayJoin(conn *WebSocketConnection, room string) bool {
	authorize := h.roomAuthorizer()
	if authorize == nil {
		h.joinNotice.Do(func() {
			log.Printf("WebSocket: refused a client join_room: clients can join rooms only once the hub has a SetRoomAuthorizer (server-side JoinRoom and Socket.Join are unaffected)")
		})
		return false
	}
	if !authorize(conn, room) {
		log.Printf("WebSocket: refused %s joining room %q", conn.ID, room)
		return false
	}
	return true
}

// SetDefaultIdentifyUser installs fn only if no identity function is set.
// EnableWebSocket uses it to derive identity from the authenticated session
// without replacing the application's SetIdentifyUser, whichever runs first.
// It reports whether it installed fn.
func (h *WebSocketHub) SetDefaultIdentifyUser(fn func(*http.Request) string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.identifyConfigured || h.identifyUser != nil {
		return false
	}
	h.identifyUser = fn
	return true
}

// WebSocketConnection is one client socket. Write through the hub
// (SendToConnection, the Broadcast methods), never Conn or Send directly:
// the write pump owns Conn's writes, and once the pump has exited a direct
// send on a full Send blocks forever. Send holds up to 256 queued messages;
// the hub evicts a connection whose queue is full. Rooms is guarded by the
// hub's mu.
type WebSocketConnection struct {
	ID       string
	Conn     *websocket.Conn
	Request  *http.Request
	Context  context.Context
	Cancel   context.CancelFunc
	Rooms    []string
	Send     chan WebSocketMessage
	Created  time.Time
	UserID   string
	Metadata map[string]any

	// lastPing, messagesSent, and messagesReceived are written by the pumps
	// and read by the hub's cleanup goroutine, so they must be atomic.
	lastPing         atomic.Int64 // UnixNano of the last successful ping
	messagesSent     atomic.Int64
	messagesReceived atomic.Int64

	// pending counts messages queued but not yet written; writerDone closes
	// when the write pump exits. Flush uses both.
	pending    atomic.Int64
	writerDone chan struct{}
}

// Flush waits until every message queued for c has been written, the writer
// has stopped, or timeout passes, and reports whether the queue drained. Call
// it before removing a connection so its last messages are not cut off.
func (c *WebSocketConnection) Flush(timeout time.Duration) bool {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for c.pending.Load() > 0 {
		select {
		case <-c.writerDone:
			return false
		case <-deadline.C:
			return false
		case <-tick.C:
		}
	}
	return true
}

// LastPing returns the time of the last successful ping exchange.
func (c *WebSocketConnection) LastPing() time.Time {
	return time.Unix(0, c.lastPing.Load())
}

// touchPing records a successful ping at the current time.
func (c *WebSocketConnection) touchPing() {
	c.lastPing.Store(time.Now().UnixNano())
}

// trySend enqueues message without blocking and reports whether it did. A
// full queue or a cancelled context returns false. Send is never closed (see
// WebSocketHub), so this cannot send on a closed channel.
func (c *WebSocketConnection) trySend(message WebSocketMessage) bool {
	select {
	case <-c.Context.Done():
		return false
	default:
	}

	// Count the message before it can reach the writer, so pending never
	// reads zero while a write is in flight.
	c.pending.Add(1)
	select {
	case c.Send <- message:
		return true
	case <-c.Context.Done():
		c.pending.Add(-1)
		return false
	default:
		c.pending.Add(-1)
		return false
	}
}

// NewWebSocketConnection wraps an accepted socket. Its Context keeps r's
// values but not its cancellation (see below), and RemoveConnection cancels
// it.
func NewWebSocketConnection(id string, conn *websocket.Conn, r *http.Request) *WebSocketConnection {
	// The connection must outlive the HTTP request: net/http cancels
	// r.Context() the moment the handler returns, even for a hijacked socket.
	// The hub cancels this context through RemoveConnection instead.
	ctx, cancel := context.WithCancel(context.WithoutCancel(r.Context()))

	c := &WebSocketConnection{
		ID:      id,
		Conn:    conn,
		Request: r,
		Context: ctx,
		Cancel:  cancel,
		Rooms:   make([]string, 0),
		Send:    make(chan WebSocketMessage, 256),

		writerDone: make(chan struct{}),
		Created:    time.Now(),
		Metadata:   make(map[string]any),
	}
	c.touchPing()
	return c
}

// WebSocketMessage is the JSON shape the hub reads and writes. Type routes a
// client message in the hub's read loop (join_room, leave_room, ping, or a
// message for Room); the htmx ws extension swaps Content into Target with
// Swap and leaves every other message to event listeners.
type WebSocketMessage struct {
	Type  string `json:"type"`
	Event string `json:"event,omitempty"`
	Data  any    `json:"data,omitempty"`
	// Content carries the HTML the htmx 4 ws extension swaps. The extension
	// reads `content` (with `target` and `swap`) and ignores `data`, so a
	// swap must set Content: Data alone never reaches the page. See
	// BroadcastSwap.
	Content string `json:"content,omitempty"`
	Room    string `json:"room,omitempty"`
	Target  string `json:"target,omitempty"`
	Swap    string `json:"swap,omitempty"`
	ID      string `json:"id,omitempty"`
	// UserID on a message the hub relays between clients is the sender's
	// server-side identity; the hub overwrites whatever the client sent.
	UserID string `json:"user_id,omitempty"`
}

// MarshalJSON keeps `content` on a message that targets an element even when it
// is empty, because the htmx ws extension swaps only messages that carry it: an
// empty render is how a handler clears a region. Other messages omit it.
func (m WebSocketMessage) MarshalJSON() ([]byte, error) {
	type plain WebSocketMessage
	if m.Target == "" {
		return json.Marshal(plain(m))
	}
	return json.Marshal(struct {
		plain
		Content string `json:"content"`
	}{plain: plain(m), Content: m.Content})
}

// AddConnection registers conn and starts its read and write pumps. The
// read pump handles join_room, leave_room, ping, and room messages, and
// removes the connection when the client goes away.
func (h *WebSocketHub) AddConnection(conn *WebSocketConnection) {
	h.addConnection(conn, true)
}

// addConnection registers conn and starts its writer. With hubReads the hub
// also runs the read loop that routes join_room, leave_room, ping, and room
// messages; without it the caller owns reading (see Accept).
func (h *WebSocketHub) addConnection(conn *WebSocketConnection, hubReads bool) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.connections[conn.ID] = conn
	h.stats.TotalConnections++
	h.stats.ActiveConnections++
	h.stats.LastActivity = time.Now()

	log.Printf("WebSocket: New connection added: %s", conn.ID)

	go h.writePump(conn)
	if hubReads {
		go h.readPump(conn)
	}
}

// Accept upgrades the request and registers the connection with the hub, so it
// can join rooms, receive broadcasts, and keep the hub's keepalive pings,
// origin policy, and server-side identity. Unlike HTTPHandler, the caller owns
// reading: call ReadMessage in a loop (keepalive pongs are only processed
// while a read is pending) and call RemoveConnection when done. Writes go
// through the hub (SendToConnection, room broadcasts), never Conn directly.
func (h *WebSocketHub) Accept(w http.ResponseWriter, r *http.Request) (*WebSocketConnection, error) {
	h.mu.RLock()
	opts := *h.acceptOpts
	identify := h.identifyUser
	h.mu.RUnlock()

	conn, err := websocket.Accept(w, r, &opts)
	if err != nil {
		return nil, err
	}
	id, err := newConnectionID()
	if err != nil {
		logCloseErr(conn.Close(websocket.StatusInternalError, "internal error"))
		return nil, err
	}
	conn.SetReadLimit(32768)
	wsConn := NewWebSocketConnection(id, conn, r)
	if identify != nil {
		wsConn.UserID = identify(r)
	}
	h.addConnection(wsConn, false)
	return wsConn, nil
}

// ReadMessage returns the next data message from a connection accepted with
// Accept. It returns an error once the client closes or the connection is
// removed from the hub.
func (c *WebSocketConnection) ReadMessage() ([]byte, error) {
	_, data, err := c.Conn.Read(c.Context)
	if err != nil {
		return nil, err
	}
	c.messagesReceived.Add(1)
	return data, nil
}

// RemoveConnection takes the connection out of every room and the hub and
// cancels its context, which stops both pumps. It is safe to call more than
// once and from any goroutine. An unknown ID does nothing.
func (h *WebSocketHub) RemoveConnection(connectionID string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	conn, exists := h.connections[connectionID]
	if !exists {
		return
	}

	for _, roomName := range conn.Rooms {
		if room, exists := h.rooms[roomName]; exists {
			delete(room, connectionID)
			if len(room) == 0 {
				delete(h.rooms, roomName)
			}
		}
	}

	// Cancel, never close conn.Send: removal runs from several places (the
	// read pump's defer, full-queue eviction, the cleanup loop), and closing
	// the channel would let a concurrent trySend panic.
	conn.Cancel()
	delete(h.connections, connectionID)
	h.stats.ActiveConnections--

	log.Printf("WebSocket: Connection removed: %s", connectionID)
}

// JoinRoom adds the connection to roomName, creating the room, and sends the
// other members a room_join message. An unknown connection ID does nothing.
// It is the server-side join and never consults the room authorizer; check
// access before calling it with a room name that came from a client.
func (h *WebSocketHub) JoinRoom(connectionID, roomName string) {
	h.mu.Lock()

	conn, exists := h.connections[connectionID]
	if !exists {
		h.mu.Unlock()
		return
	}

	if _, exists := h.rooms[roomName]; !exists {
		h.rooms[roomName] = make(map[string]*WebSocketConnection)
	}

	h.rooms[roomName][connectionID] = conn
	conn.Rooms = append(conn.Rooms, roomName)
	userID := conn.UserID

	// Release the lock before broadcasting: broadcastToRoom takes the same
	// non-reentrant RWMutex, so calling it under the write lock would deadlock
	// and freeze the whole hub.
	h.mu.Unlock()

	log.Printf("WebSocket: Connection %s joined room: %s", connectionID, roomName)

	h.broadcastToRoom(roomName, WebSocketMessage{
		Type:  "room_join",
		Event: "user_joined",
		Data: map[string]any{
			"user_id":       userID,
			"connection_id": connectionID,
		},
	}, connectionID)
}

// LeaveRoom removes the connection from roomName, deleting the room once it
// is empty, and sends the other members a room_leave message.
func (h *WebSocketHub) LeaveRoom(connectionID, roomName string) {
	h.mu.Lock()

	if room, exists := h.rooms[roomName]; exists {
		delete(room, connectionID)
		if len(room) == 0 {
			delete(h.rooms, roomName)
		}
	}

	conn, exists := h.connections[connectionID]
	if exists {
		for i, r := range conn.Rooms {
			if r == roomName {
				conn.Rooms = append(conn.Rooms[:i], conn.Rooms[i+1:]...)
				break
			}
		}
	}
	var userID string
	if exists {
		userID = conn.UserID
	}

	// Release the lock before broadcasting, as in JoinRoom: broadcastToRoom
	// takes it again.
	h.mu.Unlock()

	if exists {
		h.broadcastToRoom(roomName, WebSocketMessage{
			Type:  "room_leave",
			Event: "user_left",
			Data: map[string]any{
				"user_id":       userID,
				"connection_id": connectionID,
			},
		}, connectionID)
	}

	log.Printf("WebSocket: Connection %s left room: %s", connectionID, roomName)
}

// BroadcastToRoom queues message for every member of roomName. A member
// whose queue is full is evicted rather than waited for.
func (h *WebSocketHub) BroadcastToRoom(roomName string, message WebSocketMessage) {
	h.broadcastToRoom(roomName, message, "")
}

// broadcastToRoom queues message for every member of roomName except
// excludeConnectionID. It must be called without h.mu held.
func (h *WebSocketHub) broadcastToRoom(roomName string, message WebSocketMessage, excludeConnectionID string) {
	h.mu.RLock()
	room, exists := h.rooms[roomName]
	if !exists {
		h.mu.RUnlock()
		return
	}

	// Copy the members so no lock is held while sending.
	connections := make([]*WebSocketConnection, 0, len(room))
	for id, conn := range room {
		if id != excludeConnectionID {
			connections = append(connections, conn)
		}
	}
	h.mu.RUnlock()

	// Send to all connections without holding the lock. trySend never blocks
	// and never sends on a closed channel.
	sent := 0
	for _, conn := range connections {
		if conn.trySend(message) {
			sent++
		} else {
			// Channel is full or connection is closing; evict it.
			h.RemoveConnection(conn.ID)
		}
	}

	h.mu.Lock()
	h.stats.MessagesSent += int64(sent)
	h.stats.LastActivity = time.Now()
	h.mu.Unlock()

	log.Printf("WebSocket: Broadcast to room %s: %d connections", roomName, len(connections))
}

// SendToConnection queues message for one connection, evicting it if its
// queue is full. An unknown ID does nothing.
func (h *WebSocketHub) SendToConnection(connectionID string, message WebSocketMessage) {
	h.mu.RLock()
	conn, exists := h.connections[connectionID]
	h.mu.RUnlock()

	if !exists {
		return
	}

	if conn.trySend(message) {
		h.mu.Lock()
		h.stats.MessagesSent++
		h.stats.LastActivity = time.Now()
		h.mu.Unlock()
	} else {
		// Channel is full or connection is closing; evict it.
		h.RemoveConnection(connectionID)
	}
}

// Broadcast queues message for every connection, evicting any whose queue
// is full.
func (h *WebSocketHub) Broadcast(message WebSocketMessage) {
	h.mu.RLock()
	connections := make([]*WebSocketConnection, 0, len(h.connections))
	for _, conn := range h.connections {
		connections = append(connections, conn)
	}
	h.mu.RUnlock()

	sent := 0
	for _, conn := range connections {
		if conn.trySend(message) {
			sent++
		} else {
			h.RemoveConnection(conn.ID)
		}
	}

	h.mu.Lock()
	h.stats.MessagesSent += int64(sent)
	h.stats.LastActivity = time.Now()
	h.mu.Unlock()
}

// readPump reads client messages until the socket fails, then removes the
// connection. A join_room message joins only when the room authorizer allows
// it (see SetRoomAuthorizer). A message naming a Room is relayed to that room's other members
// only when the sender has joined the room, and its UserID is replaced with the
// sender's server-side identity (empty without SetIdentifyUser), so a client
// can neither post into a room it is not in nor speak as another user. A post
// to a room the sender has not joined is dropped and logged.
func (h *WebSocketHub) readPump(conn *WebSocketConnection) {
	defer func() {
		h.RemoveConnection(conn.ID)
		// Best-effort close: the connection is already being torn down, so a
		// close error has nothing left to act on.
		logCloseErr(conn.Conn.CloseNow())
	}()

	// Limit message size to guard against runaway clients.
	conn.Conn.SetReadLimit(32768)

	for {
		var message WebSocketMessage
		err := wsjson.Read(conn.Context, conn.Conn, &message)
		if err != nil {
			status := websocket.CloseStatus(err)
			if status != websocket.StatusGoingAway && status != websocket.StatusAbnormalClosure && status != websocket.StatusNormalClosure {
				log.Printf("WebSocket error: %v", err)
			}
			break
		}

		conn.messagesReceived.Add(1)
		h.mu.Lock()
		receiveOnly := h.receiveOnly
		h.stats.MessagesReceived++
		h.stats.LastActivity = time.Now()
		h.mu.Unlock()
		if receiveOnly {
			logCloseErr(conn.Conn.Close(websocket.StatusPolicyViolation, "client messages are disabled"))
			return
		}

		switch message.Type {
		case "join_room":
			if roomName, ok := message.Data.(string); ok && h.clientMayJoin(conn, roomName) {
				h.JoinRoom(conn.ID, roomName)
			}
		case "leave_room":
			if roomName, ok := message.Data.(string); ok {
				h.LeaveRoom(conn.ID, roomName)
			}
		case "ping":
			h.SendToConnection(conn.ID, WebSocketMessage{
				Type: "pong",
				Data: time.Now().Unix(),
			})
		default:
			switch {
			case message.Room == "":
				// Messages for no room are only logged.
				log.Printf("WebSocket: Received message from %s: %+v", conn.ID, message)
			case !h.inRoom(conn.ID, message.Room):
				log.Printf("WebSocket: dropped a post from %s to room %q, which it has not joined", conn.ID, message.Room)
			default:
				// Identity comes from the handshake, never from the message.
				message.UserID = conn.UserID
				h.broadcastToRoom(message.Room, message, conn.ID)
			}
		}
	}
}

// inRoom reports whether the connection is a member of roomName.
func (h *WebSocketHub) inRoom(connectionID, roomName string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	_, ok := h.rooms[roomName][connectionID]
	return ok
}

// writePump is the only goroutine that writes to conn.Conn. It writes queued
// messages, pings every 54 seconds, and returns when a write or ping fails or
// the connection's context is cancelled, removing the connection on the way
// out. It never closes conn.Send (see WebSocketHub).
func (h *WebSocketHub) writePump(conn *WebSocketConnection) {
	if conn.writerDone != nil {
		defer close(conn.writerDone)
	}
	ticker := time.NewTicker(54 * time.Second)
	defer func() {
		ticker.Stop()
		// Removing the connection cancels its context, so senders stop
		// enqueueing. Send is never closed, since closing it would race with a
		// concurrent trySend; it is garbage-collected once unreferenced.
		h.RemoveConnection(conn.ID)
		// Best-effort graceful close, then a force-close. The connection is
		// already being torn down, so close errors have nothing left to act on.
		logCloseErr(conn.Conn.Close(websocket.StatusNormalClosure, ""))
		logCloseErr(conn.Conn.CloseNow())
	}()

	for {
		select {
		case message := <-conn.Send:
			ctx, cancel := context.WithTimeout(conn.Context, 10*time.Second)
			err := wsjson.Write(ctx, conn.Conn, message)
			cancel()
			conn.pending.Add(-1)
			if err != nil {
				if !expectedDisconnectErr(err) {
					log.Printf("WebSocket write error: %v", err)
				}
				return
			}

			conn.messagesSent.Add(1)

		case <-ticker.C:
			// Ping blocks until pong is received or the context expires.
			ctx, cancel := context.WithTimeout(conn.Context, 10*time.Second)
			err := conn.Conn.Ping(ctx)
			cancel()
			if err != nil {
				return
			}
			conn.touchPing()

		case <-conn.Context.Done():
			return
		}
	}
}

// GetStats returns a snapshot of the hub's counters.
func (h *WebSocketHub) GetStats() WebSocketStats {
	h.mu.RLock()
	defer h.mu.RUnlock()

	stats := h.stats
	stats.ActiveConnections = len(h.connections)
	stats.TotalRooms = len(h.rooms)

	return stats
}

// cleanupLoop runs cleanup every 30 seconds, forever.
func (h *WebSocketHub) cleanupLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		h.cleanup()
	}
}

// cleanup removes connections whose context is done or whose last
// successful ping is more than two minutes old.
func (h *WebSocketHub) cleanup() {
	h.mu.Lock()

	now := time.Now()
	var toRemove []string

	for id, conn := range h.connections {
		select {
		case <-conn.Context.Done():
			toRemove = append(toRemove, id)
		default:
			if now.Sub(conn.LastPing()) > 2*time.Minute {
				toRemove = append(toRemove, id)
			}
		}
	}

	// Release the lock before removing: RemoveConnection takes the same write
	// lock, so calling it here would deadlock the cleanup goroutine and freeze
	// the hub.
	h.mu.Unlock()

	for _, id := range toRemove {
		h.RemoveConnection(id)
	}

	if len(toRemove) > 0 {
		log.Printf("WebSocket: Cleaned up %d dead connections", len(toRemove))
	}
}

// HTTPHandler returns the handler that upgrades a request and registers the
// connection with the hub's read and write pumps. A comma-separated "rooms"
// query parameter joins those rooms at the handshake, before the hub reads
// any client message. The room authorizer decides each one when it is set;
// without one, authorize them in HTTP middleware, since the hub does not.
//
//	extension := websocket.NewWebSocketExtension()
//	http.HandleFunc("/ws", extension.GetHub().HTTPHandler())
//
// In a HyperChi app, app.Extensions().EnableWebSocket("/ws") mounts this
// handler; app.WebSocket registers a route with its own SocketHandler on the
// same hub.
func (h *WebSocketHub) HTTPHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, h.acceptOpts)
		if err != nil {
			log.Printf("WebSocket upgrade error: %v", err)
			return
		}

		// crypto/rand, not a timestamp: two connections in the same
		// nanosecond would share an ID and overwrite each other in the hub.
		connectionID, err := newConnectionID()
		if err != nil {
			log.Printf("WebSocket: failed to generate connection ID: %v", err)
			// Best-effort close: the connection cannot be used further.
			logCloseErr(conn.Close(websocket.StatusInternalError, "internal error"))
			return
		}

		wsConn := NewWebSocketConnection(connectionID, conn, r)

		// Derive identity server-side only. A client-supplied user_id or
		// X-User-ID would allow trivial impersonation. Without SetIdentifyUser
		// the connection is anonymous.
		h.mu.RLock()
		identify := h.identifyUser
		h.mu.RUnlock()
		if identify != nil {
			wsConn.UserID = identify(r)
		}

		// Join the handshake rooms before the read loop starts, so the first
		// client message already sees them.
		h.addConnection(wsConn, false)
		authorize := h.roomAuthorizer()
		if rooms := r.URL.Query().Get("rooms"); rooms != "" {
			for _, room := range strings.Split(rooms, ",") {
				room = strings.TrimSpace(room)
				if room == "" {
					continue
				}
				if authorize != nil && !authorize(wsConn, room) {
					log.Printf("WebSocket: refused %s joining room %q at the handshake", connectionID, room)
					continue
				}
				h.JoinRoom(connectionID, room)
			}
		}
		go h.readPump(wsConn)

		// Block until the connection closes: net/http tears down a hijacked
		// socket once the handler returns. The connection's context is
		// independent of the request (see NewWebSocketConnection) and is
		// cancelled through RemoveConnection when a pump sees the socket end.
		<-wsConn.Context.Done()
	}
}

// newConnectionID returns a random 128-bit connection ID.
func newConnectionID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "ws_" + hex.EncodeToString(b[:]), nil
}

// BroadcastHTML broadcasts HTML that the htmx 4 ws extension swaps into
// target with innerHTML. The HTML goes in Content, the field the extension
// reads; Data mirrors it for other consumers. Use BroadcastSwap to choose the
// swap style, for example beforeend for an append-only feed.
func (h *WebSocketHub) BroadcastHTML(roomName, eventName, target, html string) {
	h.BroadcastToRoom(roomName, WebSocketMessage{
		Type:    "htmx",
		Event:   eventName,
		Target:  target,
		Content: html,
		Data:    html,
		Swap:    "innerHTML",
	})
}

// BroadcastSwap broadcasts HTML to a room for the htmx 4 ws extension to swap
// into target with the given swap style ("innerHTML", "beforeend",
// "outerHTML", ...). The HTML travels in Content: the extension swaps
// `content` and ignores `data`.
func (h *WebSocketHub) BroadcastSwap(roomName, target, swap, html string) {
	h.BroadcastToRoom(roomName, WebSocketMessage{
		Type:    "htmx",
		Target:  target,
		Swap:    swap,
		Content: html,
		Data:    html,
	})
}

// BroadcastJSON broadcasts data in a "json" message. It has no content, so
// the htmx ws extension swaps nothing; handle it in an
// htmx:ws:after:message:incoming listener.
func (h *WebSocketHub) BroadcastJSON(roomName, eventName string, data any) {
	h.BroadcastToRoom(roomName, WebSocketMessage{
		Type:  "json",
		Event: eventName,
		Data:  data,
	})
}

// BroadcastReload broadcasts a "reload" message. Nothing reloads unless the
// page listens for it.
func (h *WebSocketHub) BroadcastReload(roomName string) {
	h.BroadcastToRoom(roomName, WebSocketMessage{
		Type: "reload",
		Data: "reload",
	})
}

// BroadcastRedirect broadcasts a "redirect" message whose data is
// {"url": url}. Nothing navigates unless the page listens for it.
func (h *WebSocketHub) BroadcastRedirect(roomName, url string) {
	h.BroadcastToRoom(roomName, WebSocketMessage{
		Type: "redirect",
		Data: map[string]any{"url": url},
	})
}

// logCloseErr logs a close failure unless it is the expected result of tearing
// down a connection that the peer or the other pump already closed.
func logCloseErr(err error) {
	if !expectedCloseErr(err) {
		log.Printf("WebSocket: close error: %v", err)
	}
}

// expectedCloseErr reports whether err is nil or one of the errors a second
// close, or a close after the peer vanished, always produces.
func expectedCloseErr(err error) bool {
	return err == nil || errors.Is(err, net.ErrClosed) || errors.Is(err, io.EOF)
}

// expectedDisconnectErr reports whether a read or write failed only because the
// connection went away: closed locally, cancelled by the hub, or closed by the
// peer with a close frame.
func expectedDisconnectErr(err error) bool {
	return expectedCloseErr(err) || errors.Is(err, context.Canceled) || websocket.CloseStatus(err) != -1
}
