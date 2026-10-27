// Package response sets htmx response headers (HX-Trigger, HX-Location,
// HX-Retarget, and the rest) on an http.ResponseWriter. Inside a HyperChi
// route use c.HX() and c.Redirect instead. This package serves middleware and
// code that stays on net/http.
//
//	response.HXTrigger(w, map[string]any{"showToast": "Saved."})
//	response.HXReplaceURL(w, "/items/7")
//
// Headers must be set before the status or body is written.
package response

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// HXLocation sets HX-Location, which has htmx load url with an ajax request,
// swap it in, and push it to history, without a full page load.
func HXLocation(w http.ResponseWriter, url string) {
	w.Header().Set("HX-Location", url)
}

// HXLocationWithContext sets HX-Location to ctx as JSON, so the navigation
// can name its target, swap style, values, and headers. If ctx does not
// marshal (a Values entry JSON cannot encode), it falls back to ctx.Path
// alone.
//
//	response.HXLocationWithContext(w, response.LocationContext{
//		Path:   "/users/123",
//		Target: "#main-content",
//	})
func HXLocationWithContext(w http.ResponseWriter, ctx LocationContext) {
	data, err := json.Marshal(ctx)
	if err != nil {
		HXLocation(w, ctx.Path)
		return
	}
	w.Header().Set("HX-Location", string(data))
}

// LocationContext is the JSON form of HX-Location. Path is required, and empty
// fields are omitted.
type LocationContext struct {
	Path    string            `json:"path"`
	Source  string            `json:"source,omitempty"`
	Event   string            `json:"event,omitempty"`
	Handler string            `json:"handler,omitempty"`
	Target  string            `json:"target,omitempty"`
	Swap    string            `json:"swap,omitempty"`
	Values  map[string]any    `json:"values,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

// HXPushURL sets HX-Push-Url, which pushes url onto the browser history. The
// value "false" stops a push the request would otherwise make.
func HXPushURL(w http.ResponseWriter, url string) {
	w.Header().Set("HX-Push-Url", url)
}

// HXRedirect sets HX-Redirect, which makes the browser do a full page load of
// url. HXLocation navigates without one.
func HXRedirect(w http.ResponseWriter, url string) {
	w.Header().Set("HX-Redirect", url)
}

// HXRefresh sets HX-Refresh, which makes the browser reload the page.
func HXRefresh(w http.ResponseWriter) {
	w.Header().Set("HX-Refresh", "true")
}

// HXReplaceURL sets HX-Replace-Url, which replaces the current URL in the
// location bar without adding a history entry.
func HXReplaceURL(w http.ResponseWriter, url string) {
	w.Header().Set("HX-Replace-Url", url)
}

// HXReswap sets HX-Reswap, which overrides the triggering element's hx-swap
// (for example "outerHTML" or "afterbegin").
func HXReswap(w http.ResponseWriter, swap string) {
	w.Header().Set("HX-Reswap", swap)
}

// HXRetarget sets HX-Retarget, which overrides the triggering element's
// hx-target with a CSS selector or extended selector ("closest .container").
func HXRetarget(w http.ResponseWriter, target string) {
	w.Header().Set("HX-Retarget", target)
}

// HXReselect sets HX-Reselect, which overrides the triggering element's
// hx-select: selector picks the part of the response to swap in.
func HXReselect(w http.ResponseWriter, selector string) {
	w.Header().Set("HX-Reselect", selector)
}

// HXTrigger sets HX-Trigger, which fires client-side events once the response
// arrives. events is an event name, a []string of names, or a map[string]any
// from event name to its detail data, sent as JSON:
//
//	response.HXTrigger(w, "updateChart")
//	response.HXTrigger(w, map[string]any{
//		"showToast": map[string]any{"level": "success", "message": "Saved."},
//	})
//
// Any other type, map[string]string included, is formatted with %v, which
// htmx cannot read.
func HXTrigger(w http.ResponseWriter, events any) {
	setTriggerHeader(w, "HX-Trigger", events)
}

// setTriggerHeader writes events to header in the forms HXTrigger documents.
func setTriggerHeader(w http.ResponseWriter, header string, events any) {
	switch v := events.(type) {
	case string:
		w.Header().Set(header, v)
	case []string:
		w.Header().Set(header, strings.Join(v, ","))
	case map[string]any:
		data, err := json.Marshal(v)
		if err != nil {
			w.Header().Set(header, fmt.Sprintf("%v", v))
			return
		}
		w.Header().Set(header, string(data))
	default:
		w.Header().Set(header, fmt.Sprintf("%v", v))
	}
}

// Response chains htmx header setters on one ResponseWriter. The status set
// with Status is held until the first body write, so headers set later in the
// chain still go out:
//
//	response.New(w).
//		Status(http.StatusCreated).
//		Trigger("itemAdded").
//		PushURL("/items/7").
//		WriteString(html)
type Response struct {
	w           http.ResponseWriter
	status      int
	wroteHeader bool
}

// New returns a Response for w with no status set (200 if none is set).
func New(w http.ResponseWriter) *Response {
	return &Response{w: w}
}

// flushStatus writes the held status, or 200, exactly once. It runs at the
// first body write, so HX-* headers set after Status(), as in
// Status(200).Trigger(...), are still sent with the header block.
func (r *Response) flushStatus() {
	if r.wroteHeader {
		return
	}
	if r.status == 0 {
		r.status = http.StatusOK
	}
	r.w.WriteHeader(r.status)
	r.wroteHeader = true
}

// Location sets the HX-Location header.
func (r *Response) Location(url string) *Response {
	HXLocation(r.w, url)
	return r
}

// LocationWithContext sets the HX-Location header with additional context.
func (r *Response) LocationWithContext(ctx LocationContext) *Response {
	HXLocationWithContext(r.w, ctx)
	return r
}

// PushURL sets the HX-Push-Url header.
func (r *Response) PushURL(url string) *Response {
	HXPushURL(r.w, url)
	return r
}

// Redirect sets the HX-Redirect header.
func (r *Response) Redirect(url string) *Response {
	HXRedirect(r.w, url)
	return r
}

// Refresh sets the HX-Refresh header.
func (r *Response) Refresh() *Response {
	HXRefresh(r.w)
	return r
}

// ReplaceURL sets the HX-Replace-Url header.
func (r *Response) ReplaceURL(url string) *Response {
	HXReplaceURL(r.w, url)
	return r
}

// Reswap sets the HX-Reswap header.
func (r *Response) Reswap(swap string) *Response {
	HXReswap(r.w, swap)
	return r
}

// Retarget sets the HX-Retarget header.
func (r *Response) Retarget(target string) *Response {
	HXRetarget(r.w, target)
	return r
}

// Reselect sets the HX-Reselect header.
func (r *Response) Reselect(selector string) *Response {
	HXReselect(r.w, selector)
	return r
}

// Trigger sets the HX-Trigger header.
func (r *Response) Trigger(events any) *Response {
	HXTrigger(r.w, events)
	return r
}

// Status records the status code. It is written at the first body write, so
// headers set later in the chain are not lost.
func (r *Response) Status(code int) *Response {
	r.status = code
	return r
}

// Header sets any response header.
func (r *Response) Header(key, value string) *Response {
	r.w.Header().Set(key, value)
	return r
}

// Write writes the held status, then data. Headers set afterwards are ignored.
func (r *Response) Write(data []byte) (int, error) {
	r.flushStatus()
	return r.w.Write(data)
}

// WriteString writes the held status, then data.
func (r *Response) WriteString(data string) (int, error) {
	r.flushStatus()
	return r.w.Write([]byte(data))
}

// WriteJSON writes data as JSON with a trailing newline and an
// application/json Content-Type. On a marshal error it writes nothing and
// returns the error.
func (r *Response) WriteJSON(data any) error {
	body, err := json.Marshal(data)
	if err != nil {
		return err
	}
	r.Header("Content-Type", "application/json")
	r.flushStatus()
	_, err = r.w.Write(append(body, '\n'))
	return err
}

// Success writes a 200 with message as the body. It fires events[0] through
// HX-Trigger, or else, when message is non-empty, a showToast event with level
// "success". Later events are ignored. htmx swaps the body in as HTML and no
// Content-Type is set, so escape any untrusted text in message.
func Success(w http.ResponseWriter, message string, events ...any) {
	resp := New(w).Status(http.StatusOK)

	if len(events) > 0 {
		resp.Trigger(events[0])
	} else if message != "" {
		resp.Trigger(map[string]any{
			"showToast": map[string]any{
				"level":   "success",
				"message": message,
			},
		})
	}

	_, _ = resp.WriteString(message) // client may have disconnected
}

// Error writes code with message as the body. It fires events[0] through
// HX-Trigger, or else, when message is non-empty, a showToast event with level
// "error". As with Success, escape any untrusted text in message.
func Error(w http.ResponseWriter, code int, message string, events ...any) {
	resp := New(w).Status(code)

	if len(events) > 0 {
		resp.Trigger(events[0])
	} else if message != "" {
		resp.Trigger(map[string]any{
			"showToast": map[string]any{
				"level":   "error",
				"message": message,
			},
		})
	}

	_, _ = resp.WriteString(message) // client may have disconnected
}

// NoContent writes a 204, which htmx does not swap, firing events[0] through
// HX-Trigger when given.
func NoContent(w http.ResponseWriter, events ...any) {
	resp := New(w).Status(http.StatusNoContent)

	if len(events) > 0 {
		resp.Trigger(events[0])
	}

	// No body follows, so write the status here. Otherwise net/http would
	// send an implicit 200.
	resp.flushStatus()
}

// RedirectWithToast sets HX-Redirect to url and an HX-Trigger showToast event
// with level and message. It writes no status or body.
func RedirectWithToast(w http.ResponseWriter, url, level, message string) {
	New(w).
		Redirect(url).
		Trigger(map[string]any{
			"showToast": map[string]any{
				"level":   level,
				"message": message,
			},
		})
}
