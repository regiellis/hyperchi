package hyperchi

import (
	"encoding/json"
	"errors"
	"html/template"
	"mime"
	"net/http"
	"net/url"
	"strings"

	"github.com/regiellis/hyperchi/hyperchi/response"
)

// errResponseWritten reports a second response from one handler. The first
// response already reached the client, so the second is dropped.
var errResponseWritten = errors.New("hyperchi: response already written")

// Status sets the status code for the response this handler sends next.
//
//	return c.Status(http.StatusCreated).Fragment("row", item)
func (c *Context) Status(code int) *Context {
	c.status = code
	return c
}

// SetHeader sets a response header.
func (c *Context) SetHeader(key, value string) *Context {
	c.w.Header().Set(key, value)
	return c
}

// SetCookie adds a Set-Cookie header to the response.
func (c *Context) SetCookie(cookie *http.Cookie) *Context {
	http.SetCookie(c.w, cookie)
	return c
}

// Layout picks the layout template ("layout-<name>") for Page and View. The
// default is "layout-base" when the app defines it.
func (c *Context) Layout(name string) *Context {
	c.layout = name
	return c
}

// View renders a template the way the request needs it: the full page inside
// its layout for a normal or boosted navigation, and the template alone for an
// htmx partial request. It is the right default for most page routes.
func (c *Context) View(name string, data any) error {
	if c.wantsFragment() {
		return c.Fragment(name, data)
	}
	return c.Page(name, data)
}

// Page renders a template inside its layout, whatever sent the request.
// Layout("none") renders it bare.
func (c *Context) Page(name string, data any) error {
	return c.renderTemplate(name, c.layout, data)
}

// Fragment renders a template without a layout, for htmx swaps.
func (c *Context) Fragment(name string, data any) error {
	return c.renderTemplate(name, "none", data)
}

// JSON writes data as a JSON response. Encoding finishes before any header
// is sent, so an encoding failure never leaves a half-written body.
func (c *Context) JSON(data any) error {
	body, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return c.write("application/json", append(body, '\n'))
}

// Text writes a plain-text response.
func (c *Context) Text(s string) error {
	return c.write("text/plain; charset=utf-8", []byte(s))
}

// HTML writes trusted HTML. Only pass markup the application built itself;
// render user data through a template so it is escaped.
func (c *Context) HTML(markup template.HTML) error {
	return c.write("text/html; charset=utf-8", []byte(markup))
}

// Blob writes body with the given content type, for responses Text and HTML
// do not cover (Markdown, CSV, images). An empty contentType sends
// application/octet-stream.
func (c *Context) Blob(contentType string, body []byte) error {
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	return c.write(contentType, body)
}

// Redirect sends the client to url. An htmx request gets an HX-Redirect
// header, which htmx follows with a full navigation; any other request gets a
// 303 See Other.
func (c *Context) Redirect(url string) error {
	if c.IsHTMX() {
		response.HXRedirect(c.w, url)
		return c.write("", nil)
	}
	if c.w.written {
		return errResponseWritten
	}
	http.Redirect(c.w, c.r, url, http.StatusSeeOther)
	return nil
}

// NoContent sends 204 No Content.
func (c *Context) NoContent() error {
	c.status = http.StatusNoContent
	return c.write("", nil)
}

// Empty sends the status (200 unless set with Status) and headers with no
// body, for responses whose headers are the answer.
func (c *Context) Empty() error { return c.write("", nil) }

// Attachment sends body as a download named filename. Content-Disposition
// encodes a non-ASCII name per RFC 6266, and X-Content-Type-Options: nosniff
// stops the browser from reading the body as anything but contentType. An
// empty contentType sends application/octet-stream. Serve user uploads this
// way, with a type the application derived itself.
func (c *Context) Attachment(filename, contentType string, body []byte) error {
	disposition := "attachment"
	if filename != "" {
		if d := mime.FormatMediaType("attachment", map[string]string{"filename": filename}); d != "" {
			disposition = d
		}
	}
	c.w.Header().Set("Content-Disposition", disposition)
	c.w.Header().Set("X-Content-Type-Options", "nosniff")
	return c.Blob(contentType, body)
}

// Back redirects to the page the request came from, read from the Referer
// header, or to fallback when there is none or it names another host. Only
// the referrer's path and query are used, so Back never sends a client off
// site. It answers through Redirect, so htmx gets HX-Redirect.
func (c *Context) Back(fallback string) error {
	ref, err := url.Parse(c.r.Referer())
	if err != nil || (ref.Scheme != "http" && ref.Scheme != "https") || !strings.EqualFold(ref.Host, c.r.Host) {
		return c.Redirect(fallback)
	}
	target := ref.RequestURI()
	// A path starting "//" or "/\" reads as another host to a browser.
	if strings.HasPrefix(target, "//") || strings.HasPrefix(target, `/\`) {
		return c.Redirect(fallback)
	}
	return c.Redirect(target)
}

// ETag sets the response's entity tag. A tag without quotes is quoted, so
// ETag("v42") sends "v42"; pass W/"v42" for a weak tag. Check Fresh next.
//
//	c.ETag(post.Version)
//	if c.Fresh() {
//		return c.NotModified()
//	}
//	return c.View("post", post)
func (c *Context) ETag(tag string) *Context {
	if !strings.HasPrefix(tag, `"`) && !strings.HasPrefix(tag, `W/"`) {
		tag = `"` + tag + `"`
	}
	c.w.Header().Set("ETag", tag)
	return c
}

// Fresh reports whether the client's cached copy is current: a GET or HEAD
// whose If-None-Match names the response's ETag, compared weakly as RFC 9110
// requires, or is "*". Without an ETag it is false.
func (c *Context) Fresh() bool {
	if c.r.Method != http.MethodGet && c.r.Method != http.MethodHead {
		return false
	}
	etag := c.w.Header().Get("ETag")
	if etag == "" {
		return false
	}
	for candidate := range strings.SplitSeq(c.r.Header.Get("If-None-Match"), ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || strings.TrimPrefix(candidate, "W/") == strings.TrimPrefix(etag, "W/") {
			return true
		}
	}
	return false
}

// NotModified sends 304 Not Modified with no body, keeping the ETag and other
// headers already set.
func (c *Context) NotModified() error {
	c.status = http.StatusNotModified
	return c.write("", nil)
}

// RenderWith renders through a named integration (see Integrations), such as
// a templ or Jet adapter, buffering before any header is sent.
func (c *Context) RenderWith(name string, data any) error {
	body, err := c.app.Integrations().Render(c.r.Context(), name, data)
	if err != nil {
		return err
	}
	return c.write("text/html; charset=utf-8", body)
}

// wantsFragment reports whether View should skip the layout: an htmx partial
// request, not a boosted navigation or a history restore, which both need the
// whole page.
func (c *Context) wantsFragment() bool {
	if !c.IsHTMX() || IsHXBoosted(c.r) || IsHXHistoryRestoreRequest(c.r) {
		return false
	}
	return GetHXRequestType(c.r) != "full"
}

func (c *Context) renderTemplate(name, layout string, data any) error {
	tr := c.app.Template(name)
	tr.Layout = layout
	html, err := tr.Render(data, c.templateContext(), c.r)
	if err != nil {
		return err
	}
	return c.write("text/html; charset=utf-8", []byte(html))
}

// write sends the status, content type, and body once. A HEAD request or a
// bodiless status gets headers only.
func (c *Context) write(contentType string, body []byte) error {
	if c.w.written {
		return errResponseWritten
	}
	if contentType != "" {
		c.w.Header().Set("Content-Type", contentType)
	}
	code := c.status
	if code == 0 {
		code = http.StatusOK
	}
	c.w.WriteHeader(code)
	if len(body) == 0 || c.r.Method == http.MethodHead || bodylessStatus(code) {
		return nil
	}
	_, err := c.w.Write(body)
	return err
}

func bodylessStatus(code int) bool {
	return code == http.StatusNoContent || code == http.StatusResetContent || code == http.StatusNotModified || code < 200
}

// SetGlobal makes a value available to every template rendered through a
// Context, such as a site name or the Vite dev-server URL. Set globals during
// startup, before Serve. Handler data wins over a global with the same key,
// and a global wins over a query parameter with the same key.
func (g *HyperChi) SetGlobal(key string, value any) {
	if g.globals == nil {
		g.globals = make(map[string]any)
	}
	g.globals[key] = value
}

// hasTemplate reports whether a loaded template resolves to name.
func (g *HyperChi) hasTemplate(name string) bool {
	return g.templates != nil && resolveNameIn(g.templates, name) != ""
}

// templateContext is the base data for a render: route and query parameters
// and middleware values from the request, then app globals on top.
func (g *HyperChi) templateContext(r *http.Request) map[string]any {
	ctx := extractContext(r)
	for k, v := range g.globals {
		ctx[k] = v
	}
	if token := GetCSRFToken(r); token != "" {
		ctx["csrfToken"] = token
	}
	return ctx
}

// templateContext is the app's base data for this request, plus the
// request's flash messages as "flashes" when it has any. Reading them here
// clears them (see Flashes).
func (c *Context) templateContext() map[string]any {
	ctx := c.app.templateContext(c.r)
	if flashes := c.Flashes(); len(flashes) > 0 {
		ctx["flashes"] = flashes
	}
	return ctx
}

// HX reads htmx request headers and sets htmx response headers. Response
// setters chain and end in a response method:
//
//	return c.HX().Trigger("saved").Retarget("#list").Fragment("row", item)
type HX struct {
	c        *Context
	triggers []hxTrigger
}

type hxTrigger struct {
	name   string
	detail any
}

// HX returns the htmx helper for this request.
func (c *Context) HX() *HX {
	if c.hx == nil {
		c.hx = &HX{c: c}
	}
	return c.hx
}

// Target identifies the element htmx will swap into, as "tagName#id" such as
// "div#results" (HX-Target).
func (h *HX) Target() string { return GetHXTarget(h.c.r) }

// Source is the element that issued the request, as "tag#id" (HX-Source).
func (h *HX) Source() string { return GetHXSource(h.c.r) }

// CurrentURL is the page the browser was on (HX-Current-URL).
func (h *HX) CurrentURL() string { return GetHXCurrentURL(h.c.r) }

// Boosted reports a boosted link or form navigation (HX-Boosted).
func (h *HX) Boosted() bool { return IsHXBoosted(h.c.r) }

// Trigger fires a client-side event after the swap. Call it more than once to
// fire several events; pass a detail to send data with the event.
func (h *HX) Trigger(event string, detail ...any) *HX {
	var d any
	if len(detail) > 0 {
		d = detail[0]
	}
	h.triggers = append(h.triggers, hxTrigger{event, d})
	response.HXTrigger(h.c.w, h.triggerValue())
	return h
}

func (h *HX) triggerValue() any {
	withDetail := false
	names := make([]string, 0, len(h.triggers))
	for _, t := range h.triggers {
		names = append(names, t.name)
		if t.detail != nil {
			withDetail = true
		}
	}
	if !withDetail {
		return strings.Join(names, ",")
	}
	events := make(map[string]any, len(h.triggers))
	for _, t := range h.triggers {
		events[t.name] = t.detail
	}
	return events
}

// Retarget swaps the response into a different element (HX-Retarget).
func (h *HX) Retarget(selector string) *HX {
	response.HXRetarget(h.c.w, selector)
	return h
}

// Reswap changes the swap strategy, such as "outerHTML" (HX-Reswap).
func (h *HX) Reswap(swap string) *HX {
	response.HXReswap(h.c.w, swap)
	return h
}

// Reselect picks part of the response to swap (HX-Reselect).
func (h *HX) Reselect(selector string) *HX {
	response.HXReselect(h.c.w, selector)
	return h
}

// PushURL pushes url onto browser history (HX-Push-Url).
func (h *HX) PushURL(url string) *HX {
	response.HXPushURL(h.c.w, url)
	return h
}

// ReplaceURL replaces the current history entry (HX-Replace-Url).
func (h *HX) ReplaceURL(url string) *HX {
	response.HXReplaceURL(h.c.w, url)
	return h
}

// Refresh makes the browser reload the page (HX-Refresh).
func (h *HX) Refresh() *HX {
	response.HXRefresh(h.c.w)
	return h
}

// Location navigates without a full reload (HX-Location).
func (h *HX) Location(url string) *HX {
	response.HXLocation(h.c.w, url)
	return h
}

// Status sets the response status, as Context.Status does.
func (h *HX) Status(code int) *HX {
	h.c.status = code
	return h
}

// View finishes with Context.View.
func (h *HX) View(name string, data any) error { return h.c.View(name, data) }

// Fragment finishes with Context.Fragment.
func (h *HX) Fragment(name string, data any) error { return h.c.Fragment(name, data) }

// Page finishes with Context.Page.
func (h *HX) Page(name string, data any) error { return h.c.Page(name, data) }

// HTML finishes with Context.HTML.
func (h *HX) HTML(markup template.HTML) error { return h.c.HTML(markup) }

// JSON finishes with Context.JSON.
func (h *HX) JSON(data any) error { return h.c.JSON(data) }

// NoContent finishes with Context.NoContent, which still sends the headers.
func (h *HX) NoContent() error { return h.c.NoContent() }

// Empty finishes with an empty 200 response, for a request that only needs
// the htmx headers (a trigger, a refresh, a location change).
func (h *HX) Empty() error { return h.c.Empty() }
