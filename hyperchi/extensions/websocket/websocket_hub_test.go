package websocket

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// withinDeadline runs fn and fails the test if it does not return within d.
// A regression in the hub's lock discipline (JoinRoom/LeaveRoom/cleanup calling
// a method that re-acquires the non-reentrant RWMutex) manifests as a permanent
// hang, which this turns into a clear failure instead of a suite-wide timeout.
func withinDeadline(t *testing.T, d time.Duration, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		fn()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatal("operation did not complete in time — likely a self-deadlock on the hub lock")
	}
}

// TestJoinLeaveRoomNoDeadlock checks JoinRoom and LeaveRoom release the hub
// lock before they broadcast: broadcastToRoom takes the same RWMutex, so
// holding the write lock across the broadcast deadlocks permanently.
func TestJoinLeaveRoomNoDeadlock(t *testing.T) {
	withinDeadline(t, 5*time.Second, func() {
		hub := NewWebSocketHub()
		a, b := newTestConn("a"), newTestConn("b")
		hub.registerConn(a)
		hub.registerConn(b)

		// b is already in the room, so a's join broadcasts to b (the path that
		// re-enters the lock).
		hub.JoinRoom("b", "room1")
		hub.JoinRoom("a", "room1")
		hub.LeaveRoom("a", "room1")
	})
}

// TestCleanupNoDeadlock checks cleanup releases the hub lock before it calls
// RemoveConnection, which takes the write lock; holding it would deadlock on
// the first dead connection.
func TestCleanupNoDeadlock(t *testing.T) {
	withinDeadline(t, 5*time.Second, func() {
		hub := NewWebSocketHub()
		dead := newTestConn("dead")
		hub.registerConn(dead)
		dead.Cancel() // cancelled context → cleanup marks it for removal

		hub.cleanup()
	})

	// Re-create to assert removal actually happened (separate hub to avoid the
	// closure capture above).
	hub := NewWebSocketHub()
	dead := newTestConn("dead")
	hub.registerConn(dead)
	dead.Cancel()
	hub.cleanup()

	hub.mu.RLock()
	_, stillThere := hub.connections["dead"]
	hub.mu.RUnlock()
	if stillThere {
		t.Fatal("cleanup did not remove the dead connection")
	}
}

// TestSecureOriginByDefault checks a new hub enforces same-origin (no
// InsecureSkipVerify, no permissive OriginPatterns).
func TestSecureOriginByDefault(t *testing.T) {
	hub := NewWebSocketHub()
	if hub.acceptOpts.InsecureSkipVerify {
		t.Fatal("InsecureSkipVerify must be false by default (Cross-Site WebSocket Hijacking risk)")
	}
	if len(hub.acceptOpts.OriginPatterns) != 0 {
		t.Fatalf("OriginPatterns must be empty by default, got %v", hub.acceptOpts.OriginPatterns)
	}
}

// TestDevelopmentOriginPolicy: a hub with no explicit policy relaxes origin
// verification for development so a remote browser (e.g. Vite on 0.0.0.0 over
// SSH) can connect.
func TestDevelopmentOriginPolicy(t *testing.T) {
	hub := NewWebSocketHub()
	if !hub.UseDevelopmentOriginPolicy() {
		t.Fatal("expected dev policy to be applied on a fresh hub")
	}
	if !hub.acceptOpts.InsecureSkipVerify {
		t.Fatal("dev policy should relax origin verification")
	}
}

// TestDevelopmentOriginPolicyRespectsExplicitConfig: once the app sets an
// explicit policy, the development convenience must not override it.
func TestDevelopmentOriginPolicyRespectsExplicitConfig(t *testing.T) {
	hub := NewWebSocketHub()
	hub.SetOriginPatterns([]string{"app.example.com"})
	if hub.UseDevelopmentOriginPolicy() {
		t.Fatal("dev policy must not override an explicit origin configuration")
	}
	if hub.acceptOpts.InsecureSkipVerify {
		t.Fatal("explicit SetOriginPatterns must not be downgraded to skip-verify")
	}
	if len(hub.acceptOpts.OriginPatterns) != 1 {
		t.Fatal("explicit origin patterns should be preserved")
	}
}

// TestSetDefaultIdentifyUser: the framework default applies when the app has
// set nothing, but an explicit SetIdentifyUser always wins regardless of order.
func TestSetDefaultIdentifyUser(t *testing.T) {
	// Default applies on an unconfigured hub.
	hub := NewWebSocketHub()
	if !hub.SetDefaultIdentifyUser(func(*http.Request) string { return "from-default" }) {
		t.Fatal("default should install on an unconfigured hub")
	}
	if hub.identifyUser == nil || hub.identifyUser(nil) != "from-default" {
		t.Fatal("default identity function was not installed")
	}

	// Explicit set before the default → default is a no-op.
	hub2 := NewWebSocketHub()
	hub2.SetIdentifyUser(func(*http.Request) string { return "explicit" })
	if hub2.SetDefaultIdentifyUser(func(*http.Request) string { return "from-default" }) {
		t.Fatal("default must not override an explicit identity function")
	}
	if hub2.identifyUser(nil) != "explicit" {
		t.Fatal("explicit identity function should be preserved")
	}
}

// waitForConnections polls until the hub holds n connections or the deadline
// passes (AddConnection happens asynchronously w.r.t. a successful Dial).
func waitForConnections(t *testing.T, hub *WebSocketHub, n int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		hub.mu.RLock()
		got := len(hub.connections)
		hub.mu.RUnlock()
		if got == n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("hub never reached %d connection(s)", n)
}

func singleConnUserID(t *testing.T, hub *WebSocketHub) string {
	t.Helper()
	hub.mu.RLock()
	defer hub.mu.RUnlock()
	for _, c := range hub.connections {
		return c.UserID
	}
	t.Fatal("no connection present")
	return ""
}

// TestIdentityIgnoresClientInput checks the connection's UserID never comes
// from client-supplied query params or headers. With no IdentifyUser hook the
// connection is anonymous even when the client sends ?user_id=.
func TestIdentityIgnoresClientInput(t *testing.T) {
	hub := NewWebSocketHub()
	srv := httptest.NewServer(hub.HTTPHandler())
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "?user_id=attacker"
	c, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"X-User-ID": []string{"attacker"}},
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = c.CloseNow() }() // best-effort close; test cleanup

	waitForConnections(t, hub, 1)
	if got := singleConnUserID(t, hub); got != "" {
		t.Fatalf("UserID must be empty for an unauthenticated client, got %q (impersonation)", got)
	}
}

// TestIdentityFromServerHook checks identity comes from the server-side
// IdentifyUser hook, not the conflicting client input.
func TestIdentityFromServerHook(t *testing.T) {
	hub := NewWebSocketHub()
	hub.SetIdentifyUser(func(r *http.Request) string { return "server-derived" })
	srv := httptest.NewServer(hub.HTTPHandler())
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "?user_id=attacker"
	c, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = c.CloseNow() }() // best-effort close; test cleanup

	waitForConnections(t, hub, 1)
	if got := singleConnUserID(t, hub); got != "server-derived" {
		t.Fatalf("UserID must come from the server hook, got %q", got)
	}
}

// TestConnectionOutlivesRequest checks the connection context is not
// cancelled when the HTTP handler returns. It dials, confirms the connection
// is registered and its context still live a short while later, then closes
// from the client side.
func TestConnectionOutlivesRequest(t *testing.T) {
	hub := NewWebSocketHub()
	srv := httptest.NewServer(hub.HTTPHandler())
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	c, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = c.CloseNow() }() // best-effort close; test cleanup

	waitForConnections(t, hub, 1)

	// Give the request handler time to have returned were it going to. The
	// connection must still be alive (context not Done).
	time.Sleep(100 * time.Millisecond)

	hub.mu.RLock()
	var connCtx context.Context
	for _, conn := range hub.connections {
		connCtx = conn.Context
	}
	hub.mu.RUnlock()

	if connCtx == nil {
		t.Fatal("connection disappeared from hub")
	}
	select {
	case <-connCtx.Done():
		t.Fatal("connection context was cancelled while the socket is still open (C3 regression)")
	default:
	}
}
