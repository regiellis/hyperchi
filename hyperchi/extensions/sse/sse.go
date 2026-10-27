// Package sse is HyperChi's Server-Sent Events hub for the htmx 4 SSE
// extension (hx-sse.js). Clients connect to SSEHub.HTTPHandler, subscribe to
// channels through the "channels" query parameter, and receive the messages
// the server broadcasts to those channels.
package sse

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/regiellis/hyperchi/hyperchi/extensions"
)

// SSEExtension registers the htmx SSE extension script, its attributes, and
// the SSE template helpers, and owns the hub that serves the streams.
type SSEExtension struct {
	extensions.BaseExtension
	hub *SSEHub
}

// NewSSEExtension returns the extension with a new hub. The hub's sweep
// goroutine runs for the life of the process.
func NewSSEExtension() *SSEExtension {
	ext := &SSEExtension{
		BaseExtension: extensions.BaseExtension{},
		hub:           NewSSEHub(),
	}

	ext.SetName("sse").
		SetVersion("4.0.0").
		SetDescription("htmx 4 Server-Sent Events extension for real-time updates").
		AddScript("/hyperchi/js/hx-sse.js").
		AddAttribute("hx-sse:connect").
		AddAttribute("hx-sse:close")

	return ext
}

// Initialize adds the SSE template helpers (see TemplateFuncs) to the
// registry.
func (e *SSEExtension) Initialize(registry *extensions.Registry) error {
	for name, fn := range TemplateFuncs() {
		registry.AddTemplateFunc(name, fn)
	}
	return nil
}

// GetHub returns the extension's hub.
func (e *SSEExtension) GetHub() *SSEHub {
	return e.hub
}

// SSEHub tracks SSE connections and the channels they subscribe to. Its
// methods are safe for concurrent use.
//
// Locking: mu guards the maps, stats, and every connection's Channels slice.
// mu and a connection's writeMu are never held together. Senders copy the
// connections they need under mu and write after releasing it, and
// RemoveConnection releases mu before it waits for an in-flight write.
type SSEHub struct {
	// connections is keyed by connection ID.
	connections map[string]*SSEConnection

	// channels maps a channel name to its subscribers, keyed by connection ID.
	channels map[string]map[string]*SSEConnection

	mu sync.RWMutex

	// authorizeChannel decides which channels a client may name in
	// ?channels=; nil allows every channel.
	authorizeChannel func(r *http.Request, channel string) bool

	stats SSEStats
}

// SSEStats is a snapshot of hub counters, as GetStats returns it.
type SSEStats struct {
	TotalConnections  int64     `json:"total_connections"`
	ActiveConnections int       `json:"active_connections"`
	TotalChannels     int       `json:"total_channels"`
	MessagesSent      int64     `json:"messages_sent"`
	LastActivity      time.Time `json:"last_activity"`
}

// SetChannelAuthorizer installs fn to decide which channels a client may
// subscribe to through the "?channels=" list of an HTTPHandler stream. fn sees
// the request (its session, cookies, and route values) and one channel name;
// false refuses the whole stream with 403 before it opens. Without an
// authorizer every requested channel is allowed, which suits public
// broadcasts; install one before any channel carries per-user data.
// Server-side Subscribe calls never consult it. Configure before serving.
func (h *SSEHub) SetChannelAuthorizer(fn func(r *http.Request, channel string) bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.authorizeChannel = fn
}

// NewSSEHub returns a hub and starts its sweep goroutine, which every 30
// seconds pings each connection and removes those whose request has ended.
// The goroutine never stops, so create one hub per process.
func NewSSEHub() *SSEHub {
	hub := &SSEHub{
		connections: make(map[string]*SSEConnection),
		channels:    make(map[string]map[string]*SSEConnection),
	}

	go hub.cleanupLoop()

	return hub
}

// SSEConnection is one client stream. Writer and Flusher belong to the HTTP
// handler serving the stream: write through the hub, which serializes writes
// under writeMu and stops once Context is cancelled.
type SSEConnection struct {
	ID       string
	Writer   http.ResponseWriter
	Request  *http.Request
	Flusher  http.Flusher
	Context  context.Context
	Cancel   context.CancelFunc
	Channels []string
	Created  time.Time

	// lastPing and messagesSent are written by the ping/send paths and read
	// by the hub's cleanup goroutine, so they must be atomic.
	writeMu      sync.Mutex   // serializes writes and synchronizes handler teardown
	lastPing     atomic.Int64 // UnixNano of the last ping sent
	messagesSent atomic.Int64
}

// waitForWrites blocks until a write already in progress finishes. Called
// after Cancel, it guarantees no sender touches the response once it returns,
// because sendToConnection checks the context under writeMu.
func (c *SSEConnection) waitForWrites() {
	c.writeMu.Lock()
	c.writeMu.Unlock() //nolint:staticcheck // SA2001: acquiring the lock waits for the in-flight writer.
}

// LastPing returns the time of the last ping sent on this connection.
func (c *SSEConnection) LastPing() time.Time {
	return time.Unix(0, c.lastPing.Load())
}

// touchPing records a ping at the current time.
func (c *SSEConnection) touchPing() {
	c.lastPing.Store(time.Now().UnixNano())
}

// newConnectionID returns a crypto/rand connection ID.
func newConnectionID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "sse_" + hex.EncodeToString(b[:]), nil
}

// NewSSEConnection wraps an SSE response. Its Context is a child of r's and
// is cancelled by RemoveConnection. A writer that is not an http.Flusher is
// logged, and messages to it are written without flushing.
func NewSSEConnection(id string, w http.ResponseWriter, r *http.Request) *SSEConnection {
	ctx, cancel := context.WithCancel(r.Context())

	flusher, ok := w.(http.Flusher)
	if !ok {
		log.Printf("SSE: ResponseWriter does not support flushing")
	}

	c := &SSEConnection{
		ID:       id,
		Writer:   w,
		Request:  r,
		Flusher:  flusher,
		Context:  ctx,
		Cancel:   cancel,
		Channels: make([]string, 0),
		Created:  time.Now(),
	}
	c.touchPing()
	return c
}

// AddConnection registers conn under its ID, replacing any connection with
// the same ID.
func (h *SSEHub) AddConnection(conn *SSEConnection) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.connections[conn.ID] = conn
	h.stats.TotalConnections++
	h.stats.ActiveConnections++
	h.stats.LastActivity = time.Now()

	log.Printf("SSE: New connection added: %s", conn.ID)
}

// RemoveConnection unsubscribes the connection from every channel, cancels
// its context, and waits for any in-flight write, so the HTTP handler may
// return safely afterwards. An unknown ID does nothing.
func (h *SSEHub) RemoveConnection(connectionID string) {
	h.mu.Lock()

	conn, exists := h.connections[connectionID]
	if !exists {
		h.mu.Unlock()
		return
	}

	for _, channelName := range conn.Channels {
		if channel, exists := h.channels[channelName]; exists {
			delete(channel, connectionID)
			if len(channel) == 0 {
				delete(h.channels, channelName)
			}
		}
	}

	conn.Cancel()
	delete(h.connections, connectionID)
	h.stats.ActiveConnections--
	h.mu.Unlock()

	// mu is released first: a writer that fails calls RemoveConnection, which
	// takes mu, so waiting under it could deadlock.
	conn.waitForWrites()

	log.Printf("SSE: Connection removed: %s", connectionID)
}

// Subscribe adds the connection to channelName, creating the channel. An
// unknown connection ID does nothing.
func (h *SSEHub) Subscribe(connectionID, channelName string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	conn, exists := h.connections[connectionID]
	if !exists {
		return
	}

	if _, exists := h.channels[channelName]; !exists {
		h.channels[channelName] = make(map[string]*SSEConnection)
	}

	h.channels[channelName][connectionID] = conn
	conn.Channels = append(conn.Channels, channelName)

	log.Printf("SSE: Connection %s subscribed to channel: %s", connectionID, channelName)
}

// Unsubscribe removes the connection from channelName and deletes the
// channel once it is empty.
func (h *SSEHub) Unsubscribe(connectionID, channelName string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if channel, exists := h.channels[channelName]; exists {
		delete(channel, connectionID)
		if len(channel) == 0 {
			delete(h.channels, channelName)
		}
	}

	if conn, exists := h.connections[connectionID]; exists {
		for i, ch := range conn.Channels {
			if ch == channelName {
				conn.Channels = append(conn.Channels[:i], conn.Channels[i+1:]...)
				break
			}
		}
	}

	log.Printf("SSE: Connection %s unsubscribed from channel: %s", connectionID, channelName)
}

// SSEMessage is one SSE event. An empty Event sends an unnamed message, which
// hx-sse.js swaps into the connecting element; a named one is dispatched as
// a DOM event instead. A string Data is sent as is, anything else as JSON.
type SSEMessage struct {
	Event string `json:"event,omitempty"`
	Data  any    `json:"data"`
	ID    string `json:"id,omitempty"`
	Retry int    `json:"retry,omitempty"`
}

// Broadcast sends message to every connection subscribed to channelName. It
// writes to each connection in turn, so a slow client delays the rest; a
// failed write removes that connection.
func (h *SSEHub) Broadcast(channelName string, message SSEMessage) {
	h.mu.RLock()
	channel, exists := h.channels[channelName]
	if !exists {
		h.mu.RUnlock()
		return
	}

	// Copy the subscribers so no lock is held while writing.
	connections := make([]*SSEConnection, 0, len(channel))
	for _, conn := range channel {
		connections = append(connections, conn)
	}
	h.mu.RUnlock()

	for _, conn := range connections {
		h.sendToConnection(conn, message)
	}

	h.mu.Lock()
	h.stats.MessagesSent += int64(len(connections))
	h.stats.LastActivity = time.Now()
	h.mu.Unlock()

	log.Printf("SSE: Broadcast to channel %s: %d connections", channelName, len(connections))
}

// SendToConnection sends message to one connection. An unknown ID does
// nothing.
func (h *SSEHub) SendToConnection(connectionID string, message SSEMessage) {
	h.mu.RLock()
	conn, exists := h.connections[connectionID]
	h.mu.RUnlock()

	if !exists {
		return
	}

	h.sendToConnection(conn, message)

	h.mu.Lock()
	h.stats.MessagesSent++
	h.stats.LastActivity = time.Now()
	h.mu.Unlock()
}

// sendToConnection formats message as an SSE event and writes it. It must be
// called without h.mu held: a failed write calls RemoveConnection.
func (h *SSEHub) sendToConnection(conn *SSEConnection, message SSEMessage) {
	select {
	case <-conn.Context.Done():
		h.RemoveConnection(conn.ID)
		return
	default:
	}

	var msgStr strings.Builder

	if message.ID != "" {
		fmt.Fprintf(&msgStr, "id: %s\n", message.ID)
	}

	if message.Event != "" {
		fmt.Fprintf(&msgStr, "event: %s\n", message.Event)
	}

	if message.Retry > 0 {
		fmt.Fprintf(&msgStr, "retry: %d\n", message.Retry)
	}

	// A string is sent as is; anything else as JSON, or its %v form if it
	// cannot be marshaled.
	var dataStr string
	switch v := message.Data.(type) {
	case string:
		dataStr = v
	default:
		if data, err := json.Marshal(v); err == nil {
			dataStr = string(data)
		} else {
			dataStr = fmt.Sprintf("%v", v)
		}
	}

	// Each line of data needs its own "data:" field.
	for _, line := range strings.Split(dataStr, "\n") {
		fmt.Fprintf(&msgStr, "data: %s\n", line)
	}

	msgStr.WriteString("\n")

	// Check cancellation after taking the write lock so a sender that was
	// waiting during teardown cannot touch the response after the handler exits.
	conn.writeMu.Lock()
	if conn.Context.Err() != nil {
		conn.writeMu.Unlock()
		return
	}
	_, err := conn.Writer.Write([]byte(msgStr.String()))
	if err == nil && conn.Flusher != nil {
		conn.Flusher.Flush()
	}
	conn.writeMu.Unlock()
	if err != nil {
		log.Printf("SSE: Error sending to connection %s: %v", conn.ID, err)
		h.RemoveConnection(conn.ID)
		return
	}

	conn.messagesSent.Add(1)
}

// Ping sends a "ping" event carrying the Unix time to every connection and
// records the ping time.
func (h *SSEHub) Ping() {
	h.mu.RLock()
	connections := make([]*SSEConnection, 0, len(h.connections))
	for _, conn := range h.connections {
		connections = append(connections, conn)
	}
	h.mu.RUnlock()

	pingMessage := SSEMessage{
		Event: "ping",
		Data:  time.Now().Unix(),
	}

	for _, conn := range connections {
		h.sendToConnection(conn, pingMessage)
		conn.touchPing()
	}
}

// GetStats returns a snapshot of the hub's counters.
func (h *SSEHub) GetStats() SSEStats {
	h.mu.RLock()
	defer h.mu.RUnlock()

	stats := h.stats
	stats.ActiveConnections = len(h.connections)
	stats.TotalChannels = len(h.channels)

	return stats
}

// cleanupLoop runs sweep every 30 seconds, forever.
func (h *SSEHub) cleanupLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		h.sweep()
	}
}

// sweep is one pass of the hub's periodic loop: ping every connection, which
// refreshes its liveness and keeps proxies from idling the stream out, then
// remove the ones whose request has ended.
func (h *SSEHub) sweep() {
	h.Ping()
	h.cleanup()
}

// cleanup removes connections whose context is done or that have not been
// pinged for two minutes.
func (h *SSEHub) cleanup() {
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
	// lock, so calling it here would deadlock the sweep goroutine and freeze
	// the hub.
	h.mu.Unlock()

	for _, id := range toRemove {
		h.RemoveConnection(id)
	}

	if len(toRemove) > 0 {
		log.Printf("SSE: Cleaned up %d dead connections", len(toRemove))
	}
}

// HTTPHandler returns the handler that opens a stream. It subscribes the
// connection to each comma-separated channel in the "channels" query
// parameter, sends a "connected" event carrying the connection ID, and holds
// the stream open until the client disconnects.
//
// The Access-Control-Allow-Origin header is CORS_ORIGINS when set, "*" when
// ENVIRONMENT (or GO_ENV) is unset, "development", or "dev", and absent
// otherwise.
//
//	extension := sse.NewSSEExtension()
//	http.HandleFunc("/events", extension.GetHub().HTTPHandler())
//
// In a HyperChi app, app.Extensions().EnableSSE("/events") mounts this handler.
func (h *SSEHub) HTTPHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		var channels []string
		for _, channel := range strings.Split(r.URL.Query().Get("channels"), ",") {
			if channel = strings.TrimSpace(channel); channel != "" {
				channels = append(channels, channel)
			}
		}
		h.mu.RLock()
		authorize := h.authorizeChannel
		h.mu.RUnlock()
		if authorize != nil {
			for _, channel := range channels {
				if !authorize(r, channel) {
					http.Error(w, "forbidden", http.StatusForbidden)
					return
				}
			}
		}

		sseOrigin := os.Getenv("CORS_ORIGINS")
		if sseOrigin == "" {
			env := os.Getenv("ENVIRONMENT")
			if env == "" {
				env = os.Getenv("GO_ENV")
			}
			if env == "" || env == "development" || env == "dev" {
				sseOrigin = "*"
			}
		}
		if sseOrigin != "" {
			w.Header().Set("Access-Control-Allow-Origin", sseOrigin)
			w.Header().Set("Access-Control-Allow-Headers", "Cache-Control")
		}

		// crypto/rand, not a timestamp: two connections in the same
		// nanosecond would share an ID and overwrite each other in the hub.
		connectionID, err := newConnectionID()
		if err != nil {
			http.Error(w, "failed to create connection", http.StatusInternalServerError)
			return
		}

		// A stream lives far longer than a normal response, so lift the
		// server's write deadline for this request; otherwise WriteTimeout
		// cuts every stream once it elapses.
		if err := http.NewResponseController(w).SetWriteDeadline(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
			log.Printf("SSE: clearing write deadline: %v", err)
		}

		conn := NewSSEConnection(connectionID, w, r)
		h.AddConnection(conn)

		// Subscribe before announcing the connection, so "connected" means a
		// broadcast sent after it reaches this client.
		for _, channel := range channels {
			h.Subscribe(connectionID, channel)
		}
		h.sendToConnection(conn, SSEMessage{
			Event: "connected",
			Data:  map[string]any{"id": connectionID},
		})

		// Hold the stream open until the client disconnects or the hub
		// removes the connection.
		<-conn.Context.Done()
		h.RemoveConnection(connectionID)
		// Another remover may already have deleted the hub entry. Always join
		// the writer here as well, before net/http reclaims the response.
		conn.waitForWrites()
	}
}

// BroadcastSwap broadcasts HTML as an unnamed message. The htmx 4 SSE
// extension (hx-sse.js) swaps only unnamed messages into the element's
// hx-swap target; a named event is dispatched as a DOM event and never
// swapped. Use this to push HTML that appears in the page:
//
//	<div hx-sse:connect="/sse?channels=metrics" hx-swap="innerHTML">...</div>
//	hub.BroadcastSwap("metrics", "<span>42</span>")
func (h *SSEHub) BroadcastSwap(channelName, html string) {
	h.Broadcast(channelName, SSEMessage{
		Data: html,
	})
}

// BroadcastHTML broadcasts HTML under the named event eventName.
//
// htmx 4 dispatches a named event as a DOM event and does not swap it (it
// removed sse-swap). The client must handle it, with hx-trigger or with the
// sseSwap template helper, which swaps the event's data into the element. To
// make HTML appear without a handler, use BroadcastSwap.
func (h *SSEHub) BroadcastHTML(channelName, eventName, html string) {
	h.Broadcast(channelName, SSEMessage{
		Event: eventName,
		Data:  html,
	})
}

// BroadcastJSON broadcasts data, marshaled as JSON, under the named event
// eventName.
func (h *SSEHub) BroadcastJSON(channelName, eventName string, data any) {
	h.Broadcast(channelName, SSEMessage{
		Event: eventName,
		Data:  data,
	})
}

// BroadcastReload broadcasts a named "reload" event. The client must listen
// for it; nothing reloads by default.
func (h *SSEHub) BroadcastReload(channelName string) {
	h.Broadcast(channelName, SSEMessage{
		Event: "reload",
		Data:  "reload",
	})
}

// BroadcastRedirect broadcasts a named "redirect" event whose data is
// {"url": url}. The client must listen for it; nothing navigates by default.
func (h *SSEHub) BroadcastRedirect(channelName, url string) {
	h.Broadcast(channelName, SSEMessage{
		Event: "redirect",
		Data:  map[string]any{"url": url},
	})
}
