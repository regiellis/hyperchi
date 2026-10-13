package hyperchi

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket/wsjson"
)

// A handler that sends a final message and returns races the connection
// teardown against the write pump. The client must still receive the
// message.
func TestSocketDeliversFinalMessageBeforeClosing(t *testing.T) {
	app, srv := socketApp(t)
	app.WebSocket("/bye", func(s *Socket) error {
		for range 20 {
			if err := s.SendHTML("#log", "beforeend", "<p>line</p>"); err != nil {
				return err
			}
		}
		return s.SendHTML("#status", "innerHTML", "<p>closed</p>")
	})

	for run := range 20 {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		conn := dialSocket(t, ctx, srv, "/bye")
		got := 0
		final := false
		for !final {
			var msg map[string]any
			if err := wsjson.Read(ctx, conn, &msg); err != nil {
				cancel()
				t.Fatalf("run %d: connection ended after %d messages without the final one: %v", run, got, err)
			}
			got++
			final = msg["target"] == "#status" && strings.Contains(msg["content"].(string), "closed")
		}
		if got != 21 {
			t.Fatalf("run %d: got %d messages, want 21", run, got)
		}
		cancel()
	}
}
