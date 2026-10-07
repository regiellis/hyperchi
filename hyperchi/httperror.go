package hyperchi

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// HTTPError is an error with an HTTP status. Return one from a Handler to
// answer with that status; the error handler renders Message for the client.
// Err keeps the underlying cause for logs and errors.Is/As, and is never shown
// to the client.
type HTTPError struct {
	Code    int
	Message string
	Err     error
	// Fields holds per-field messages for a validation failure (see Bind).
	Fields map[string][]string
}

// Error reports the status code, message, and wrapped cause.
func (e *HTTPError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%d %s: %v", e.Code, e.Message, e.Err)
	}
	return fmt.Sprintf("%d %s", e.Code, e.Message)
}

// Unwrap returns the wrapped cause, for errors.Is and errors.As.
func (e *HTTPError) Unwrap() error { return e.Err }

// Wrap attaches the underlying cause for logging.
func (e *HTTPError) Wrap(err error) *HTTPError {
	e.Err = err
	return e
}

// NewHTTPError returns an error that answers with code and message.
func NewHTTPError(code int, message string) *HTTPError {
	if message == "" {
		message = http.StatusText(code)
	}
	return &HTTPError{Code: code, Message: message}
}

// Errorf returns an HTTPError with a formatted message. A %w verb keeps the
// wrapped error as the cause, for logs and errors.Is, and leaves its text out
// of the Message the client sees, with the separator before it:
//
//	hyperchi.Errorf(503, "could not load user %d: %w", id, err) // Message "could not load user 42"
//
// A message left empty becomes the status text. Format a value with %v or %s
// only when the client may read it.
func Errorf(code int, format string, args ...any) *HTTPError {
	err := fmt.Errorf(format, args...)
	var cause error
	switch wrapped := err.(type) {
	case interface{ Unwrap() error }:
		cause = wrapped.Unwrap()
	case interface{ Unwrap() []error }:
		cause = errors.Join(wrapped.Unwrap()...)
	}
	message := clientMessage(format, args)
	if message == "" {
		message = http.StatusText(code)
	}
	return &HTTPError{Code: code, Message: message, Err: cause}
}

// omittedCause stands in for a %w operand when Errorf builds the client
// message. It formats as a marker that clientMessage then removes.
type omittedCause struct{}

const omittedMarker = "\x00"

func (omittedCause) Format(f fmt.State, _ rune) {
	if _, err := io.WriteString(f, omittedMarker); err != nil {
		log.Printf("hyperchi: Errorf: %v", err)
	}
}

// clientMessage formats format and args with every %w operand left out. It
// walks the verbs to find which argument each %w consumes, honoring %%, flags,
// explicit [n] indexes, and * widths and precisions, as fmt does.
func clientMessage(format string, args []any) string {
	var out strings.Builder
	hidden := map[int]bool{}
	arg := 0
	for i := 0; i < len(format); {
		if format[i] != '%' {
			out.WriteByte(format[i])
			i++
			continue
		}
		start := i
		i++
		if i < len(format) && format[i] == '%' {
			out.WriteString("%%")
			i++
			continue
		}
		index := func() {
			if i < len(format) && format[i] == '[' {
				if end := strings.IndexByte(format[i:], ']'); end > 0 {
					if n, err := strconv.Atoi(format[i+1 : i+end]); err == nil && n > 0 {
						arg = n - 1
					}
					i += end + 1
				}
			}
		}
		number := func() {
			index()
			if i < len(format) && format[i] == '*' {
				arg++
				i++
				return
			}
			for i < len(format) && format[i] >= '0' && format[i] <= '9' {
				i++
			}
		}
		for i < len(format) && strings.IndexByte("+-# 0", format[i]) >= 0 {
			i++
		}
		number()
		if i < len(format) && format[i] == '.' {
			i++
			number()
		}
		index()
		if i >= len(format) {
			out.WriteString(format[start:])
			break
		}
		verb, size := utf8.DecodeRuneInString(format[i:])
		i += size
		if verb == 'w' {
			hidden[arg] = true
			out.WriteString(format[start : i-size])
			out.WriteByte('v')
		} else {
			out.WriteString(format[start:i])
		}
		arg++
	}

	shown := append([]any(nil), args...)
	for n := range hidden {
		if n < len(shown) {
			shown[n] = omittedCause{}
		}
	}
	message := fmt.Sprintf(out.String(), shown...)
	message = strings.ReplaceAll(message, " "+omittedMarker+" ", " ")
	message = strings.ReplaceAll(message, omittedMarker, "")
	return strings.TrimRight(strings.TrimSpace(message), " :")
}

// BadRequest answers 400.
func BadRequest(message string) *HTTPError { return NewHTTPError(http.StatusBadRequest, message) }

// Unauthorized answers 401.
func Unauthorized(message string) *HTTPError {
	return NewHTTPError(http.StatusUnauthorized, message)
}

// Forbidden answers 403.
func Forbidden(message string) *HTTPError { return NewHTTPError(http.StatusForbidden, message) }

// NotFound answers 404.
func NotFound(message string) *HTTPError { return NewHTTPError(http.StatusNotFound, message) }

// Conflict answers 409.
func Conflict(message string) *HTTPError { return NewHTTPError(http.StatusConflict, message) }

// Unprocessable answers 422 with per-field messages, the shape Bind returns
// when validation fails.
func Unprocessable(message string, fields map[string][]string) *HTTPError {
	e := NewHTTPError(http.StatusUnprocessableEntity, message)
	e.Fields = fields
	return e
}

// FieldErrors returns the per-field messages of a validation error from Bind
// or BindMessage, or nil for any other error. Use it to re-render a form with
// its errors:
//
//	in, err := hyperchi.Bind[Signup](c)
//	if fields := hyperchi.FieldErrors(err); fields != nil {
//		return c.Status(422).Fragment("signup-form", hyperchi.H{"in": in, "errors": fields})
//	}
func FieldErrors(err error) map[string][]string {
	var he *HTTPError
	if errors.As(err, &he) {
		return he.Fields
	}
	return nil
}

// ErrorFunc renders an error a Handler returned. Replace the default with
// OnError to render errors your own way.
type ErrorFunc func(c *Context, err error)

// OnError replaces the default error handler. Call DefaultError from your
// function to keep the default for cases you do not handle.
func (g *HyperChi) OnError(fn ErrorFunc) { g.onError = fn }

// respondStatus answers a status-level failure from net/http code (router
// misses, static files, middleware) through the app's error pipeline, so one
// error template or OnError brands every error response. It is the default
// StatusHandler for the components that take one. An *HTTPError with the
// same code is rendered as is, keeping its message; any other err is the
// logged cause behind the status text.
func (g *HyperChi) respondStatus(w http.ResponseWriter, r *http.Request, code int, err error) {
	var he *HTTPError
	if !errors.As(err, &he) || he.Code != code {
		he = &HTTPError{Code: code, Message: http.StatusText(code), Err: err}
	}
	g.handleError(g.newContext(w, r), he)
}

func (g *HyperChi) handleError(c *Context, err error) {
	if c.w.written {
		log.Printf("hyperchi: %s %s: error after the response started: %v", c.r.Method, c.r.URL.Path, err)
		return
	}
	// Error responses are never cached or indexed, whoever renders them.
	c.w.Header().Del("Content-Length")
	c.w.Header().Set("Cache-Control", "no-store")
	c.w.Header().Set("X-Robots-Tag", "noindex")
	if g.onError != nil {
		g.onError(c, err)
		return
	}
	g.DefaultError(c, err)
}

// DefaultError renders err for the client that asked:
//   - an API client (Accept: application/json) gets {"error": ..., "fields": ...};
//   - an htmx request gets the "error" template as a fragment, or a small
//     alert when the app has none;
//   - a browser gets the "error" template as a page, or a built-in page. In
//     development a 5xx with an underlying cause shows that cause in full.
//
// An error that is not an *HTTPError is a 500, logged in full, and its text
// never reaches the client.
func (g *HyperChi) DefaultError(c *Context, err error) {
	var he *HTTPError
	typed := errors.As(err, &he)
	if !typed {
		he = &HTTPError{Code: http.StatusInternalServerError, Message: http.StatusText(http.StatusInternalServerError), Err: err}
	}
	if he.Code >= 500 {
		log.Printf("hyperchi: %s %s: %v", c.r.Method, c.r.URL.Path, err)
	}
	c.status = he.Code
	c.layout = ""
	data := H{"status": he.Code, "message": he.Message, "fields": he.Fields}

	switch {
	case wantsJSON(c.r):
		body := H{"error": he.Message}
		if len(he.Fields) > 0 {
			body["fields"] = he.Fields
		}
		g.writeFallback(c, c.JSON(body))
	case c.IsHTMX():
		if g.hasTemplate("error") {
			g.writeFallback(c, c.Fragment("error", data))
			return
		}
		g.writeFallback(c, c.HTML(errorAlert(he)))
	case he.Code >= 500 && (!typed || he.Err != nil) && g.ErrorPageHandler != nil && g.ErrorPageHandler.isDev:
		// Development: show the cause in full (the page is never served in production).
		g.ErrorPageHandler.RenderDefault(he.Err, c.w, c.r, he.Code)
	case g.hasTemplate("error"):
		g.writeFallback(c, c.Page("error", data))
	case !typed && g.ErrorPageHandler != nil:
		g.ErrorPageHandler.RenderDefault(err, c.w, c.r, http.StatusInternalServerError)
	default:
		g.writeFallback(c, c.HTML(errorPage(he)))
	}
}

// writeFallback covers a failure while rendering the error itself: log it
// and send plain text if nothing has gone out yet.
func (g *HyperChi) writeFallback(c *Context, err error) {
	if err == nil {
		return
	}
	log.Printf("hyperchi: rendering error response for %s %s: %v", c.r.Method, c.r.URL.Path, err)
	if !c.w.written {
		http.Error(c.w, http.StatusText(c.status), c.status)
	}
}

// JSONErrors makes the routes it wraps answer every error as JSON, whatever
// the request's Accept header says, for API routes whose clients never ask
// for HTML. Apply it to a route, a group, or the app:
//
//	app.Get("/api/users/{id}", showUser, hyperchi.JSONErrors)
//	app.Group("/api", func(api *hyperchi.HyperChi) { api.Use(hyperchi.JSONErrors) })
//
// RESTful routes use it automatically.
func JSONErrors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), jsonErrorsCtxKey, true)))
	})
}

func wantsJSON(r *http.Request) bool {
	if forced, ok := r.Context().Value(jsonErrorsCtxKey).(bool); ok && forced {
		return true
	}
	accept := r.Header.Get("Accept")
	return strings.Contains(accept, "application/json") && !strings.Contains(accept, "text/html")
}

func errorAlert(he *HTTPError) template.HTML {
	var b strings.Builder
	b.WriteString(`<div class="hc-error" role="alert"><p>`)
	b.WriteString(template.HTMLEscapeString(he.Message))
	b.WriteString(`</p>`)
	if len(he.Fields) > 0 {
		names := make([]string, 0, len(he.Fields))
		for name := range he.Fields {
			names = append(names, name)
		}
		sort.Strings(names)
		b.WriteString(`<ul>`)
		for _, name := range names {
			for _, msg := range he.Fields[name] {
				b.WriteString(`<li>`)
				b.WriteString(template.HTMLEscapeString(msg))
				b.WriteString(`</li>`)
			}
		}
		b.WriteString(`</ul>`)
	}
	b.WriteString(`</div>`)
	return template.HTML(b.String())
}

func errorPage(he *HTTPError) template.HTML {
	title := template.HTMLEscapeString(fmt.Sprintf("%d %s", he.Code, http.StatusText(he.Code)))
	return template.HTML(`<!doctype html><html lang="en"><head><meta charset="utf-8">` +
		`<meta name="viewport" content="width=device-width, initial-scale=1"><title>` + title +
		`</title></head><body><main><h1>` + title + `</h1>` + string(errorAlert(he)) + `</main></body></html>`)
}

// RespondStatus answers a refusal from net/http middleware (a rate limiter, an
// auth check, an optional module) through the app's error pipeline, so it
// renders like every framework denial: OnError or DefaultError, with no-store
// headers. An *HTTPError with the same code keeps its message; any other err is
// logged as the cause behind the status text and never shown to the client.
func (g *HyperChi) RespondStatus(w http.ResponseWriter, r *http.Request, code int, err error) {
	g.respondStatus(w, r, code, err)
}
