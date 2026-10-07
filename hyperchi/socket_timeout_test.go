package hyperchi

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket/wsjson"
)

// The framework's server sets ReadTimeout and WriteTimeout. A hijacked socket
// keeps whatever deadlines the server set, so a long-lived socket must not die
// when they elapse.
func TestSocketOutlivesServerTimeouts(t *testing.T) {
	app := newTemplateApp(t, false)
	srv := httptest.NewUnstartedServer(app)
	srv.Config.ReadTimeout = 100 * time.Millisecond
	srv.Config.WriteTimeout = 100 * time.Millisecond
	srv.Start()
	t.Cleanup(srv.Close)

	app.WebSocket("/slow", func(s *Socket) error {
		msg, err := s.Receive()
		if err != nil {
			return err
		}
		if msg.Value("v") != "late" {
			return s.SendHTML("#out", "innerHTML", "<p>wrong</p>")
		}
		return s.SendHTML("#out", "innerHTML", "<p>late</p>")
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn := dialSocket(t, ctx, srv, "/slow")
	select {
	case <-time.After(400 * time.Millisecond):
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err := wsjson.Write(ctx, conn, map[string]any{"v": "late"}); err != nil {
		t.Fatalf("write after the server timeouts elapsed: %v", err)
	}
	var reply map[string]any
	if err := wsjson.Read(ctx, conn, &reply); err != nil {
		t.Fatalf("read after the server timeouts elapsed: %v", err)
	}
	if reply["content"] != "<p>late</p>" {
		t.Fatalf("reply = %v", reply)
	}
}
