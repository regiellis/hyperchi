package hyperchi

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"net/http"
)

// Handler is the one shape every HyperChi route takes. It reads the request
// through c, picks a response (c.View, c.Fragment, c.JSON, c.Redirect, ...) and
// returns it. A returned error goes to the app's error handler, which renders
// it for the client: an error fragment for htmx, an error page for a browser,
// JSON for an API client. Return hyperchi.NotFound(...) and friends to choose
// the status.
//
//	app.Get("/users/{id}", func(c *hyperchi.Context) error {
//		user, err := users.Find(c.Param("id"))
//		if err != nil {
//			return hyperchi.NotFound("no such user")
//		}
//		return c.View("user", user)
//	})
type Handler func(c *Context) error

// Middleware is standard net/http middleware. Any chi or net/http middleware
// works with Use, Group, and the per-route middleware arguments.
type Middleware = func(http.Handler) http.Handler

// Get registers a GET route. Optional middleware applies to this route only.
func (g *HyperChi) Get(pattern string, h Handler, mw ...Middleware) {
	g.route(http.MethodGet, pattern, h, mw)
}

// Post registers a POST route.
func (g *HyperChi) Post(pattern string, h Handler, mw ...Middleware) {
	g.route(http.MethodPost, pattern, h, mw)
}

// Put registers a PUT route.
func (g *HyperChi) Put(pattern string, h Handler, mw ...Middleware) {
	g.route(http.MethodPut, pattern, h, mw)
}

// Patch registers a PATCH route.
func (g *HyperChi) Patch(pattern string, h Handler, mw ...Middleware) {
	g.route(http.MethodPatch, pattern, h, mw)
}

// Delete registers a DELETE route.
func (g *HyperChi) Delete(pattern string, h Handler, mw ...Middleware) {
	g.route(http.MethodDelete, pattern, h, mw)
}

// Head registers a HEAD route.
func (g *HyperChi) Head(pattern string, h Handler, mw ...Middleware) {
	g.route(http.MethodHead, pattern, h, mw)
}

// Options registers an OPTIONS route.
func (g *HyperChi) Options(pattern string, h Handler, mw ...Middleware) {
	g.route(http.MethodOptions, pattern, h, mw)
}

// Handle registers a plain net/http handler for one method. It is the escape
// hatch for code that already speaks net/http (a third-party handler, a file
// server, a proxy). Application routes should use Get, Post, and the rest.
func (g *HyperChi) Handle(method, pattern string, h http.Handler) {
	if h == nil {
		panic(fmt.Sprintf("hyperchi: nil http.Handler for %s %s", method, pattern))
	}
	g.router.Method(method, pattern, h)
}

// ToHTTP adapts a Handler to net/http, for mounting it in another router or a
// test server. Errors go through the app's error handler, as they do for
// routes registered with Get and friends.
func (g *HyperChi) ToHTTP(h Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c := g.newContext(w, r)
		if err := h(c); err != nil {
			g.handleError(c, err)
		}
	}
}

// WrapHTTP adapts a net/http handler to a Handler, so existing net/http code
// can sit inside a HyperChi route. Prefer writing the route as a Handler.
func WrapHTTP(h http.Handler) Handler {
	return func(c *Context) error {
		h.ServeHTTP(c.w, c.r)
		return nil
	}
}

func (g *HyperChi) route(method, pattern string, h Handler, mw []Middleware) {
	if h == nil {
		panic(fmt.Sprintf("hyperchi: nil Handler for %s %s", method, pattern))
	}
	g.ensureHotReloadRoute()
	r := g.router
	if len(mw) > 0 {
		r = g.router.With(mw...)
	}
	r.Method(method, pattern, g.ToHTTP(h))
}

// responseWriter records whether the response has started so the error handler
// knows when it can still write, and keeps Flusher and Hijacker reachable for
// streaming and WebSocket upgrades.
type responseWriter struct {
	http.ResponseWriter
	status  int
	written bool
}

func (w *responseWriter) WriteHeader(code int) {
	if w.written {
		return
	}
	if code >= 200 {
		w.status = code
		w.written = true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *responseWriter) Write(b []byte) (int, error) {
	if !w.written {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

// Status reports the committed status, or 0 before anything was written.
func (w *responseWriter) Status() int {
	if !w.written {
		return 0
	}
	return w.status
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (w *responseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *responseWriter) Flush() {
	if !w.written {
		w.WriteHeader(http.StatusOK)
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *responseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("hyperchi: underlying ResponseWriter does not support hijacking")
	}
	w.written = true
	return h.Hijack()
}
