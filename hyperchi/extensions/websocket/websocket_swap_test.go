package websocket

import (
	"encoding/json"
	"strings"
	"testing"
)

// The shipped htmx 4 ws extension (hx-ws.js) swaps the `content` field of an
// incoming JSON message into `target` with `swap`, and ignores `data`. A message
// carrying its HTML only in `data` is delivered but never appears in the page.
// BroadcastSwap and BroadcastHTML must therefore put the HTML in Content.

func addConnToRoom(h *WebSocketHub, room string, conn *WebSocketConnection) {
	h.mu.Lock()
	if h.rooms[room] == nil {
		h.rooms[room] = make(map[string]*WebSocketConnection)
	}
	h.rooms[room][conn.ID] = conn
	h.mu.Unlock()
}

func TestBroadcastSwapPutsHTMLInContentField(t *testing.T) {
	h := NewWebSocketHub()
	conn := newTestConn("c1")
	addConnToRoom(h, "activity", conn)

	const html = `<div class="feed-item">hi</div>`
	h.BroadcastSwap("activity", "#activity-log", "beforeend", html)

	select {
	case msg := <-conn.Send:
		if msg.Content != html {
			t.Fatalf("Content = %q, want the swap HTML", msg.Content)
		}
		if msg.Target != "#activity-log" || msg.Swap != "beforeend" {
			t.Fatalf("target/swap = %q/%q, want #activity-log/beforeend", msg.Target, msg.Swap)
		}
		// The wire JSON must expose the HTML under "content" (what the extension reads).
		b, err := json.Marshal(msg)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), `"content":`) {
			t.Fatalf("marshaled message has no content field: %s", b)
		}
	default:
		t.Fatal("BroadcastSwap enqueued nothing to the room connection")
	}
}

func TestBroadcastHTMLPutsHTMLInContentField(t *testing.T) {
	h := NewWebSocketHub()
	conn := newTestConn("c2")
	addConnToRoom(h, "room", conn)

	h.BroadcastHTML("room", "", "#slot", "<p>x</p>")

	select {
	case msg := <-conn.Send:
		if msg.Content != "<p>x</p>" {
			t.Fatalf("Content = %q, want the HTML (htmx 4 swaps content, not data)", msg.Content)
		}
	default:
		t.Fatal("BroadcastHTML enqueued nothing")
	}
}
