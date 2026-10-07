package websocket

import (
	"context"
	"sync"
	"testing"
	"time"
)

// newTestConn builds a WebSocketConnection without a real socket so the
// send/cancel lifecycle can be exercised in isolation.
func newTestConn(id string) *WebSocketConnection {
	ctx, cancel := context.WithCancel(context.Background())
	c := &WebSocketConnection{
		ID:       id,
		Context:  ctx,
		Cancel:   cancel,
		Rooms:    make([]string, 0),
		Send:     make(chan WebSocketMessage, 8),
		Created:  time.Now(),
		Metadata: make(map[string]any),
	}
	c.touchPing()
	return c
}

// registerConn inserts a connection into the hub without launching the
// read/write pumps (which require a real *websocket.Conn).
func (h *WebSocketHub) registerConn(conn *WebSocketConnection) {
	h.mu.Lock()
	h.connections[conn.ID] = conn
	h.mu.Unlock()
}

// TestTrySendVsRemoveNoRace sends on a connection while another goroutine
// removes it. Removal cancels the context and never closes the channel, so
// trySend stops enqueueing without a send-on-closed-channel race or panic.
// Run with -race.
func TestTrySendVsRemoveNoRace(t *testing.T) {
	const rounds = 200
	for r := 0; r < rounds; r++ {
		hub := NewWebSocketHub()
		conn := newTestConn("c1")
		hub.registerConn(conn)

		var wg sync.WaitGroup
		wg.Add(3)

		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				conn.trySend(WebSocketMessage{Type: "ping"})
			}
		}()
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				hub.BroadcastToRoom("", WebSocketMessage{Type: "pong"})
			}
		}()
		go func() {
			defer wg.Done()
			hub.RemoveConnection("c1")
		}()

		wg.Wait()
	}
}

// TestRemoveConnectionIdempotent ensures RemoveConnection can be called
// concurrently and repeatedly (readPump defer + eviction + cleanup) without
// panicking.
func TestRemoveConnectionIdempotent(t *testing.T) {
	hub := NewWebSocketHub()
	conn := newTestConn("dup")
	hub.registerConn(conn)

	var wg sync.WaitGroup
	const callers = 16
	wg.Add(callers)
	for i := 0; i < callers; i++ {
		go func() {
			defer wg.Done()
			hub.RemoveConnection("dup")
		}()
	}
	wg.Wait()

	if _, ok := hub.connections["dup"]; ok {
		t.Fatal("connection should have been removed")
	}
}

// TestTrySendAfterCancel verifies a cancelled connection rejects sends instead
// of blocking or panicking.
func TestTrySendAfterCancel(t *testing.T) {
	conn := newTestConn("x")
	conn.Cancel()
	if conn.trySend(WebSocketMessage{Type: "ping"}) {
		t.Fatal("trySend should return false after the context is cancelled")
	}
}
