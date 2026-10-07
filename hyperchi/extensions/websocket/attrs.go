package websocket

import (
	"fmt"
	"html/template"
	"regexp"
)

// eventName is the character set allowed in an event name, which becomes part
// of an attribute name (hx-on:<event>) and so cannot be escaped.
var eventName = regexp.MustCompile(`^[A-Za-z0-9_.:-]+$`)

// TemplateFuncs returns the WebSocket template helpers. Each renders an
// attribute for tag position, for example <div {{wsConnect "/ws"}}>, with its
// value HTML-escaped. HyperChi apps get them automatically. There is no room
// helper: the htmx 4 ws extension has no room setting, so a socket joins rooms
// through "?rooms=" on the connect URL or a join_room message. The hub refuses
// join_room until the app sets WebSocketHub.SetRoomAuthorizer.
func TemplateFuncs() template.FuncMap {
	return template.FuncMap{
		"wsConnect": ConnectAttr,
		"wsSend":    SendAttr,
		"wsClose":   CloseAttr,
	}
}

// ConnectAttr renders hx-ws:connect for url.
func ConnectAttr(url string) template.HTMLAttr {
	return template.HTMLAttr(`hx-ws:connect="` + template.HTMLEscapeString(url) + `"`)
}

// SendAttr renders hx-ws:send, naming event when it is not empty.
func SendAttr(event string) (template.HTMLAttr, error) {
	if event == "" {
		return `hx-ws:send`, nil
	}
	if !eventName.MatchString(event) {
		return "", fmt.Errorf("wsSend: invalid event name %q", event)
	}
	return template.HTMLAttr(`hx-ws:send="` + event + `"`), nil
}

// CloseAttr renders an hx-on handler that closes the enclosing socket when
// event fires on the element.
func CloseAttr(event string) (template.HTMLAttr, error) {
	if !eventName.MatchString(event) {
		return "", fmt.Errorf("wsClose: invalid event name %q", event)
	}
	// The selector is [hx-ws\:connect]: one backslash in CSS, written as two in
	// the JavaScript string literal inside the attribute.
	return template.HTMLAttr(`hx-on:` + event + `="this.closest('[hx-ws\\:connect]')?.dispatchEvent(new Event('htmx:ws:close'))"`), nil
}
