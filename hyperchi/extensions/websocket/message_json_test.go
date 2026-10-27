package websocket

import (
	"encoding/json"
	"strings"
	"testing"
)

// The htmx 4 ws extension swaps only messages that carry `content`, so an
// empty render ("clear this region") must keep the field; dropped by
// omitempty, it becomes a data-only message and the target keeps its old
// markup.
func TestSwapMessageKeepsEmptyContent(t *testing.T) {
	b, err := json.Marshal(WebSocketMessage{Type: "htmx", Target: "#flash", Swap: "innerHTML"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"content":""`) {
		t.Fatalf("swap message dropped empty content: %s", b)
	}

	b, err = json.Marshal(WebSocketMessage{Type: "message", Event: "ping"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `"content"`) {
		t.Fatalf("message without a target gained a content field: %s", b)
	}

	var back WebSocketMessage
	if err := json.Unmarshal([]byte(`{"type":"htmx","target":"#x","content":"<p>hi</p>"}`), &back); err != nil {
		t.Fatal(err)
	}
	if back.Target != "#x" || back.Content != "<p>hi</p>" {
		t.Fatalf("round trip = %+v", back)
	}
}
