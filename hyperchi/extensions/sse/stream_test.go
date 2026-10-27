package sse

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// openStream connects to the hub over a real server and returns a line reader.
func openStream(t *testing.T, srv *httptest.Server, query string) (*bufio.Reader, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/events"+query, nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := resp.Body.Close(); err != nil {
			t.Log(err)
		}
	})
	return bufio.NewReader(resp.Body), cancel
}

// readUntil reads lines until one contains want, failing on stream end.
func readUntil(t *testing.T, r *bufio.Reader, want string) {
	t.Helper()
	for {
		line, err := r.ReadString('\n')
		if strings.Contains(line, want) {
			return
		}
		if err != nil {
			t.Fatalf("stream ended before %q: %v", want, err)
		}
	}
}

// A server WriteTimeout (the framework sets one) must not cut a stream once
// it elapses: the handler lifts the deadline for the life of the stream.
func TestStreamOutlivesServerWriteTimeout(t *testing.T) {
	hub := NewSSEHub()
	srv := httptest.NewUnstartedServer(hub.HTTPHandler())
	srv.Config.WriteTimeout = 100 * time.Millisecond
	srv.Start()
	t.Cleanup(srv.Close)

	r, cancel := openStream(t, srv, "?channels=news")
	defer cancel()
	readUntil(t, r, "connected")

	timer := time.AfterFunc(400*time.Millisecond, func() {
		hub.Broadcast("news", SSEMessage{Event: "update", Data: "late"})
	})
	defer timer.Stop()
	readUntil(t, r, "late")
}

// The "connected" event must mean subscribed: a broadcast sent right after it
// reaches the client.
func TestConnectedEventFollowsSubscription(t *testing.T) {
	hub := NewSSEHub()
	srv := httptest.NewServer(hub.HTTPHandler())
	t.Cleanup(srv.Close)

	for range 20 {
		r, cancel := openStream(t, srv, "?channels=news")
		readUntil(t, r, "connected")
		hub.Broadcast("news", SSEMessage{Event: "update", Data: "first"})
		readUntil(t, r, "first")
		cancel()
	}
}

// The hub culls connections whose last ping is older than two minutes, so its
// periodic sweep must ping live connections before it culls.
func TestSweepKeepsLiveConnections(t *testing.T) {
	hub := NewSSEHub()
	rec := httptest.NewRecorder()
	conn := NewSSEConnection("c1", rec, httptest.NewRequest(http.MethodGet, "/events", nil))
	hub.AddConnection(conn)
	conn.lastPing.Store(time.Now().Add(-3 * time.Minute).UnixNano())

	hub.sweep()

	if hub.GetStats().ActiveConnections != 1 {
		t.Fatal("a live connection was culled by the sweep")
	}
	conn.Cancel()
	hub.sweep()
	if hub.GetStats().ActiveConnections != 0 {
		t.Fatal("a closed connection survived the sweep")
	}
}
