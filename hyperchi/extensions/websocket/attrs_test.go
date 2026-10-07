package websocket

import "testing"

// htmx 4's hx-ws.js has no room option, so a helper rendering one would join
// nothing. Rooms are joined with "?rooms=" on the connect URL or a join_room
// message; no template helper may suggest otherwise.
func TestTemplateFuncsOfferNoRoomHelper(t *testing.T) {
	funcs := TemplateFuncs()
	if _, ok := funcs["wsRoom"]; ok {
		t.Fatal("wsRoom is registered, but htmx 4's ws extension reads no room setting")
	}
	for _, name := range []string{"wsConnect", "wsSend", "wsClose"} {
		if _, ok := funcs[name]; !ok {
			t.Errorf("%s is missing from TemplateFuncs", name)
		}
	}
}
