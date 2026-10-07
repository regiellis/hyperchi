package websocket

import (
	"bytes"
	"context"
	"log"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// membersOf returns the sorted user IDs of roomName's members.
func membersOf(hub *WebSocketHub, roomName string) []string {
	hub.mu.RLock()
	defer hub.mu.RUnlock()
	var users []string
	for _, conn := range hub.rooms[roomName] {
		users = append(users, conn.UserID)
	}
	slices.Sort(users)
	return users
}

// lockedBuffer is a log destination that is safe for the hub's goroutines.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// captureLog sends the standard logger to a buffer for the rest of the test.
func captureLog(t *testing.T) *lockedBuffer {
	t.Helper()
	buf := &lockedBuffer{}
	prevOut, prevFlags := log.Writer(), log.Flags()
	log.SetOutput(buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(prevOut)
		log.SetFlags(prevFlags)
	})
	return buf
}

// newIdentityServer serves hub over HTTP with the identity the test sets per
// dial (see dialAs).
func newIdentityServer(t *testing.T, hub *WebSocketHub) *httptest.Server {
	t.Helper()
	hub.SetIdentifyUser(func(r *http.Request) string { return "user-" + r.Header.Get("X-Test-Identity") })
	srv := httptest.NewServer(hub.HTTPHandler())
	t.Cleanup(srv.Close)
	return srv
}

// Without a room authorizer a client's join_room message joins nothing, and
// the hub says once that client joins need an authorizer.
func TestClientJoinRefusedWithoutAuthorizer(t *testing.T) {
	logs := captureLog(t)
	hub := NewWebSocketHub()
	srv := newIdentityServer(t, hub)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	c := dialAs(ctx, t, srv, "mallory")
	roundTrip(ctx, t, c, WebSocketMessage{Type: "join_room", Data: "admin"})
	roundTrip(ctx, t, c, WebSocketMessage{Type: "join_room", Data: "lobby"})

	for _, room := range []string{"admin", "lobby"} {
		if got := membersOf(hub, room); len(got) != 0 {
			t.Errorf("room %s members = %v, want none without an authorizer", room, got)
		}
	}
	if n := strings.Count(logs.String(), "SetRoomAuthorizer"); n != 1 {
		t.Errorf("the missing-authorizer notice was logged %d times, want once per hub:\n%s", n, logs.String())
	}
}

// With an authorizer, each client join is allowed or refused by it.
func TestClientJoinFollowsAuthorizer(t *testing.T) {
	hub := NewWebSocketHub()
	hub.SetRoomAuthorizer(func(conn *WebSocketConnection, room string) bool {
		return room == "lobby" || (room == "admin" && conn.UserID == "user-alice")
	})
	srv := newIdentityServer(t, hub)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	alice := dialAs(ctx, t, srv, "alice")
	bob := dialAs(ctx, t, srv, "bob")
	roundTrip(ctx, t, alice, WebSocketMessage{Type: "join_room", Data: "admin"})
	roundTrip(ctx, t, bob, WebSocketMessage{Type: "join_room", Data: "admin"})
	roundTrip(ctx, t, bob, WebSocketMessage{Type: "join_room", Data: "lobby"})
	roundTrip(ctx, t, bob, WebSocketMessage{Type: "join_room", Data: "secret"})

	cases := map[string][]string{
		"admin":  {"user-alice"},
		"lobby":  {"user-bob"},
		"secret": nil,
	}
	for room, want := range cases {
		if got := membersOf(hub, room); !slices.Equal(got, want) {
			t.Errorf("room %s members = %v, want %v", room, got, want)
		}
	}
}

// Server-side joins are the application's own decision: JoinRoom (and so
// Socket.Join) never consults the authorizer, set or not.
func TestServerJoinRoomIgnoresAuthorizer(t *testing.T) {
	for _, tc := range []struct {
		name       string
		authorizer func(*WebSocketConnection, string) bool
	}{
		{"no authorizer", nil},
		{"deny-all authorizer", func(*WebSocketConnection, string) bool { return false }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hub := NewWebSocketHub()
			if tc.authorizer != nil {
				hub.SetRoomAuthorizer(tc.authorizer)
			}
			conn := newTestConn("a")
			conn.UserID = "user-a"
			hub.registerConn(conn)
			hub.JoinRoom("a", "lobby")
			if got := membersOf(hub, "lobby"); !slices.Equal(got, []string{"user-a"}) {
				t.Fatalf("lobby members = %v, want [user-a]", got)
			}
		})
	}
}

// Rooms named in the handshake's ?rooms= are client input too: an authorizer,
// once set, decides them as it decides join_room. Without one they keep the
// HTTP-middleware contract and are joined.
func TestHandshakeRoomsUseAuthorizerWhenSet(t *testing.T) {
	hub := NewWebSocketHub()
	hub.SetRoomAuthorizer(func(_ *WebSocketConnection, room string) bool { return room == "lobby" })
	srv := newIdentityServer(t, hub)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "?rooms=lobby,admin"
	c, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{
		HTTPHeader: http.Header{"X-Test-Identity": []string{"eve"}},
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() {
		if err := c.CloseNow(); err != nil && !expectedCloseErr(err) {
			t.Logf("close: %v", err)
		}
	})
	// The hub joins handshake rooms before its read loop starts, so once a pong
	// comes back the joins are settled.
	roundTrip(ctx, t, c, WebSocketMessage{Type: "ping"})
	if got := membersOf(hub, "lobby"); !slices.Equal(got, []string{"user-eve"}) {
		t.Fatalf("lobby members = %v, want [user-eve]", got)
	}
	if got := membersOf(hub, "admin"); len(got) != 0 {
		t.Fatalf("admin members = %v, want none: the authorizer refused it", got)
	}
}
