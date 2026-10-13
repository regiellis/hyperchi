package sse

import (
	"fmt"
	"html/template"
	"regexp"
)

// eventName is the character set allowed in an event name, which becomes part
// of an attribute name (hx-on:<event>) and so cannot be escaped.
var eventName = regexp.MustCompile(`^[A-Za-z0-9_.:-]+$`)

// TemplateFuncs returns the SSE template helpers. Each renders an attribute for
// tag position, for example <div {{sseConnect "/events"}}>, with its value
// HTML-escaped. HyperChi apps get them automatically.
func TemplateFuncs() template.FuncMap {
	return template.FuncMap{
		"sseConnect": ConnectAttr,
		"sseSwap":    SwapAttr,
		"sseClose":   CloseAttr,
	}
}

// ConnectAttr renders hx-sse:connect for url.
func ConnectAttr(url string) template.HTMLAttr {
	return template.HTMLAttr(`hx-sse:connect="` + template.HTMLEscapeString(url) + `"`)
}

// SwapAttr renders an hx-on handler that swaps the data of the named event
// into the element. It returns an error for an event name outside
// [A-Za-z0-9_.:-], since the name is part of the attribute and cannot be
// escaped.
func SwapAttr(event string) (template.HTMLAttr, error) {
	if !eventName.MatchString(event) {
		return "", fmt.Errorf("sseSwap: invalid event name %q", event)
	}
	return template.HTMLAttr(`hx-on:` + event + `="htmx.swap({text: event.detail.data, sourceElement: this, target: this})"`), nil
}

// CloseAttr renders hx-sse:close for event, which closes the stream when
// that event arrives. It rejects the same event names as SwapAttr.
func CloseAttr(event string) (template.HTMLAttr, error) {
	if !eventName.MatchString(event) {
		return "", fmt.Errorf("sseClose: invalid event name %q", event)
	}
	return template.HTMLAttr(`hx-sse:close="` + event + `"`), nil
}
