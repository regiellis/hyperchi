//go:build integration

package websocket

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

func TestReceiveOnlyRejectsClientBroadcastAndRoomMutation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		message WebSocketMessage
	}{
		{"HTML broadcast", WebSocketMessage{Room: "activity", Content: "<b>untrusted</b>", Target: "body", Swap: "innerHTML"}},
		{"room mutation", WebSocketMessage{Type: "join_room", Data: "unexpected"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hub := NewWebSocketHub()
			hub.SetReceiveOnly(true)
			server := httptest.NewServer(hub.HTTPHandler())
			defer server.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			url := "ws" + strings.TrimPrefix(server.URL, "http")
			victim, _, err := websocket.Dial(ctx, url+"?rooms=activity", nil)
			if err != nil {
				t.Fatal(err)
			}
			defer victim.CloseNow()
			for hub.GetStats().TotalRooms != 1 {
				select {
				case <-ctx.Done():
					t.Fatal("victim did not join activity room")
				case <-time.After(time.Millisecond):
				}
			}
			attacker, _, err := websocket.Dial(ctx, url, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer attacker.CloseNow()
			closed := make(chan error, 1)
			go func() { _, _, err := attacker.Read(ctx); closed <- err }()
			// A receive-only connection must still service protocol heartbeat frames.
			if err := attacker.Ping(ctx); err != nil {
				t.Fatalf("protocol ping: %v", err)
			}
			if err := wsjson.Write(ctx, attacker, tc.message); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-closed:
				if websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
					t.Fatalf("close: %v", err)
				}
			case <-ctx.Done():
				t.Fatal("client message was not rejected")
			}
			if got := hub.GetStats().TotalRooms; got != 1 {
				t.Fatalf("client mutated rooms: got %d", got)
			}
			// FIFO delivery makes this marker a barrier: injected content would precede it.
			hub.BroadcastSwap("activity", "#activity-log", "beforeend", "<b>trusted</b>")
			var received WebSocketMessage
			if err := wsjson.Read(ctx, victim, &received); err != nil {
				t.Fatal(err)
			}
			if received.Content != "<b>trusted</b>" || received.Target != "#activity-log" {
				t.Fatalf("unexpected broadcast: %+v", received)
			}
		})
	}
}
