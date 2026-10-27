package assets

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/coder/websocket"
)

// The hot-reload page is served by the same Go server that hosts the socket, so
// a handshake from another site is a cross-site WebSocket hijack, not a client.
func TestHotReloadWebSocketRejectsCrossOrigin(t *testing.T) {
	cfg, _ := quietConfig(t.TempDir(), t.TempDir())
	cfg.HotReload = true
	w := NewWatcher(cfg, NewWithConfig(cfg))
	srv := httptest.NewServer(http.HandlerFunc(w.HandleWebSocket))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), waitTimeout)
	defer cancel()
	url := "ws" + strings.TrimPrefix(srv.URL, "http")

	header := http.Header{"Origin": {"https://evil.example"}}
	conn, resp, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: header})
	if resp != nil && resp.Body != nil {
		if cerr := resp.Body.Close(); cerr != nil {
			t.Fatal(cerr)
		}
	}
	if err == nil {
		if cerr := conn.CloseNow(); cerr != nil {
			t.Fatal(cerr)
		}
		t.Fatal("cross-origin hot-reload handshake was accepted")
	}

	header = http.Header{"Origin": {srv.URL}}
	conn, resp, err = websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: header})
	if resp != nil && resp.Body != nil {
		if cerr := resp.Body.Close(); cerr != nil {
			t.Fatal(cerr)
		}
	}
	if err != nil {
		t.Fatalf("same-origin handshake rejected: %v", err)
	}
	if err := conn.CloseNow(); err != nil {
		t.Fatal(err)
	}
}
