package websocket

import (
	"bytes"
	"context"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// dialAs opens a socket on srv whose server-side identity is who. The test
// identity function reads it from a header the test controls; it stands in for
// a session lookup.
func dialAs(ctx context.Context, t *testing.T, srv *httptest.Server, who string) *websocket.Conn {
	t.Helper()
	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	c, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{
		HTTPHeader: http.Header{"X-Test-Identity": []string{who}},
	})
	if err != nil {
		t.Fatalf("dial as %s: %v", who, err)
	}
	t.Cleanup(func() {
		if err := c.CloseNow(); err != nil && !expectedCloseErr(err) {
			t.Logf("close %s: %v", who, err)
		}
	})
	return c
}

// roundTrip sends msg, then a ping, and waits for the pong. The hub's read loop
// handles one connection's messages in order, so once the pong arrives msg has
// been fully processed.
func roundTrip(ctx context.Context, t *testing.T, c *websocket.Conn, msg WebSocketMessage) {
	t.Helper()
	if err := wsjson.Write(ctx, c, msg); err != nil {
		t.Fatalf("write %+v: %v", msg, err)
	}
	if err := wsjson.Write(ctx, c, WebSocketMessage{Type: "ping"}); err != nil {
		t.Fatalf("write ping: %v", err)
	}
	for {
		var got WebSocketMessage
		if err := wsjson.Read(ctx, c, &got); err != nil {
			t.Fatalf("read pong: %v", err)
		}
		if got.Type == "pong" {
			return
		}
	}
}

// A client may post only to a room it joined, and a relayed message carries the
// sender's server-side identity, never the user_id the client wrote.
func TestRoomPostRequiresMembershipAndServerIdentity(t *testing.T) {
	hub := NewWebSocketHub()
	hub.SetIdentifyUser(func(r *http.Request) string { return "user-" + r.Header.Get("X-Test-Identity") })
	// Every client join is allowed: this test is about posting, not joining.
	hub.SetRoomAuthorizer(func(*WebSocketConnection, string) bool { return true })
	srv := httptest.NewServer(hub.HTTPHandler())
	defer srv.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	reader := dialAs(ctx, t, srv, "reader")
	roundTrip(ctx, t, reader, WebSocketMessage{Type: "join_room", Data: "lobby"})

	member := dialAs(ctx, t, srv, "member")
	roundTrip(ctx, t, member, WebSocketMessage{Type: "join_room", Data: "lobby"})

	// The non-member's post is processed before the member's, so if it were
	// relayed the reader would see it first.
	outsider := dialAs(ctx, t, srv, "outsider")
	roundTrip(ctx, t, outsider, WebSocketMessage{
		Type: "chat", Room: "lobby", Target: "#feed", Content: "<b>injected</b>", UserID: "member",
	})
	roundTrip(ctx, t, member, WebSocketMessage{
		Type: "chat", Room: "lobby", Data: "hello", UserID: "admin",
	})

	var joined WebSocketMessage
	if err := wsjson.Read(ctx, reader, &joined); err != nil {
		t.Fatalf("read join notice: %v", err)
	}
	if joined.Type != "room_join" {
		t.Fatalf("first message = %+v, want the member's room_join", joined)
	}

	var got WebSocketMessage
	if err := wsjson.Read(ctx, reader, &got); err != nil {
		t.Fatalf("read relayed message: %v", err)
	}
	if got.Content != "" || got.Data != "hello" {
		t.Fatalf("reader got %+v, want only the member's post (a non-member's post was relayed)", got)
	}
	if got.UserID != "user-member" {
		t.Fatalf("relayed user_id = %q, want the server identity %q", got.UserID, "user-member")
	}
}

// signalWriter collects log output and closes found the first time a line
// contains want.
type signalWriter struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	want  string
	found chan struct{}
	once  sync.Once
}

func (w *signalWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.buf.Write(p)
	if strings.Contains(w.buf.String(), w.want) {
		w.once.Do(func() { close(w.found) })
	}
	return n, err
}

// When a receive-only hub closes a connection for sending a message and the
// close fails, the failure is logged instead of discarded. A client that never
// reads cannot answer the close handshake, so the hub's close times out.
func TestReceiveOnlyPolicyCloseLogsFailure(t *testing.T) {
	logs := &signalWriter{want: "WebSocket: close error", found: make(chan struct{})}
	prevOut, prevFlags := log.Writer(), log.Flags()
	log.SetOutput(logs)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(prevOut)
		log.SetFlags(prevFlags)
	})

	hub := NewWebSocketHub()
	hub.SetReceiveOnly(true)
	srv := httptest.NewServer(hub.HTTPHandler())
	defer srv.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()

	c := dialAs(ctx, t, srv, "silent")
	if err := wsjson.Write(ctx, c, WebSocketMessage{Type: "chat", Data: "hi"}); err != nil {
		t.Fatalf("write: %v", err)
	}

	select {
	case <-logs.found:
	case <-ctx.Done():
		t.Fatal("the failed policy-violation close was not logged")
	}
}
