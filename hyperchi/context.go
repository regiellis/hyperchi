package hyperchi

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/regiellis/hyperchi/hyperchi/security"
)

// Context carries one request through a Handler: read the request with its
// accessors, then answer with one of its response methods. A Context is not
// safe for use after the handler returns.
type Context struct {
	w      *responseWriter
	r      *http.Request
	app    *HyperChi
	status int    // status for the next response write; 0 means 200
	layout string // layout for Page and View; "" means the default layout
	hx     *HX
	flash  *flashState // loaded on first use by Flash or Flashes
}

func (g *HyperChi) newContext(w http.ResponseWriter, r *http.Request) *Context {
	rw, ok := w.(*responseWriter)
	if !ok {
		rw = &responseWriter{ResponseWriter: w}
	}
	return &Context{w: rw, r: r, app: g}
}

// NewContext builds a Context outside a route, for tests and for adapters
// that receive a plain net/http request.
func (g *HyperChi) NewContext(w http.ResponseWriter, r *http.Request) *Context {
	return g.newContext(w, r)
}

// App returns the application serving this request.
func (c *Context) App() *HyperChi { return c.app }

// Request returns the underlying *http.Request.
func (c *Context) Request() *http.Request { return c.r }

// Response returns the underlying http.ResponseWriter, for code that has to
// write the response itself (streaming, a third-party encoder).
func (c *Context) Response() http.ResponseWriter { return c.w }

// Ctx returns the request's context.Context, for cancellation and deadlines.
func (c *Context) Ctx() context.Context { return c.r.Context() }

// Method returns the request method.
func (c *Context) Method() string { return c.r.Method }

// Path returns the request URL path.
func (c *Context) Path() string { return c.r.URL.Path }

// Param returns a route parameter, such as {id} in "/users/{id}".
func (c *Context) Param(key string) string { return chi.URLParam(c.r, key) }

// ParamInt returns a route parameter as an int. A value that is not an
// integer comes back as a 400 error, so a handler can return it directly.
func (c *Context) ParamInt(key string) (int, error) {
	n, err := strconv.Atoi(c.Param(key))
	if err != nil {
		return 0, BadRequest(fmt.Sprintf("%s must be an integer", key))
	}
	return n, nil
}

// Query returns the first value of a query string parameter.
func (c *Context) Query(key string) string { return c.r.URL.Query().Get(key) }

// QueryDefault returns the first query value, or fallback when it is absent
// or empty.
func (c *Context) QueryDefault(key, fallback string) string {
	return GetQueryDefault(c.r, key, fallback)
}

// QueryInt returns a query parameter as an int, or fallback when it is
// absent or not an integer.
func (c *Context) QueryInt(key string, fallback int) int {
	n, err := strconv.Atoi(c.Query(key))
	if err != nil {
		return fallback
	}
	return n
}

// Queries returns every value of a query parameter.
func (c *Context) Queries(key string) []string { return GetQueries(c.r, key) }

// FormValue returns a submitted form field, parsing the body on first use.
// Use Bind to decode and validate a whole form into a struct.
func (c *Context) FormValue(key string) string { return c.r.FormValue(key) }

// Header returns a request header.
func (c *Context) Header(key string) string { return c.r.Header.Get(key) }

// Cookie returns a request cookie's value, or "" when it is not set.
func (c *Context) Cookie(name string) string {
	ck, err := c.r.Cookie(name)
	if err != nil {
		return ""
	}
	return ck.Value
}

// Value returns a value attached to the request with WithContext, typically
// by middleware. The same values are visible to templates by key.
func (c *Context) Value(key string) any {
	values, ok := c.r.Context().Value(hxValuesCtxKey).(map[string]any)
	if !ok {
		return nil
	}
	return values[key]
}

// IsHTMX reports whether htmx sent the request.
func (c *Context) IsHTMX() bool { return isHTMXRequest(c.r) }

// Session returns the request's session, or nil when the route does not run
// behind SessionMiddleware (see UseSessions).
func (c *Context) Session() *KVSession { return c.app.GetSession(c.r) }

// UserID returns the authenticated user's ID set by the auth middleware, or
// "" for an anonymous request.
func (c *Context) UserID() string { return security.GetUserIDFromContext(c.r.Context()) }

// CSRFToken returns the request's CSRF token for forms and htmx headers that
// post back, or "" outside Middleware.CSRF. The token is masked afresh on each
// call (see MiddlewareGroup.CSRF), so it differs from the csrf_token cookie
// and from every other render, and each value validates.
func (c *Context) CSRFToken() string { return GetCSRFToken(c.r) }
