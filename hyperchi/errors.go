// Error capture and error pages. ErrorTracker records stack traces and source
// context, ErrorPageHandler renders development and production error pages, and
// its RecoveryMiddleware turns panics into them.
//
//	app.EnableErrorRecovery() // installs app.ErrorPageHandler.RecoveryMiddleware()
//
//	// Outside an app, wrap any http.Handler:
//	handler := hyperchi.NewErrorPageHandler(true)
//	mux.Handle("/", handler.RecoveryMiddleware()(next))

package hyperchi

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/go-chi/chi/v5/middleware"
)

// ErrorDetails is the captured state of one error: its stack trace, the request,
// the runtime environment, and, in development, the source around the failing
// line. Error page templates execute with it.
type ErrorDetails struct {
	ID          string            `json:"id"`
	Type        string            `json:"type"`
	Message     string            `json:"message"`
	Time        time.Time         `json:"time"`
	Request     *RequestInfo      `json:"request"`
	StackTrace  []StackFrame      `json:"stack_trace"`
	SourceCode  *SourceContext    `json:"source_code,omitempty"`
	Environment map[string]string `json:"environment"`
	Headers     map[string]string `json:"headers"`
}

// RequestInfo describes the request that caused an error. Headers that carry
// credentials (Authorization, Cookie, Set-Cookie, and any whose name looks
// secret, such as X-Api-Key), query parameters named by the same rule (in
// Query and in URL), and form values whose names look secret (password,
// token, key, ...) are stored as "[REDACTED]".
type RequestInfo struct {
	Method     string            `json:"method"`
	URL        string            `json:"url"`
	RemoteAddr string            `json:"remote_addr"`
	UserAgent  string            `json:"user_agent"`
	Headers    map[string]string `json:"headers"`
	Query      map[string]string `json:"query"`
	Form       map[string]string `json:"form,omitempty"`
}

// StackFrame is one frame of a captured stack. Code holds the frame's source
// line in development and is empty otherwise.
type StackFrame struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	Function string `json:"function"`
	Package  string `json:"package"`
	Code     string `json:"code,omitempty"`
}

// SourceContext holds the source lines around an error, for development error
// pages.
type SourceContext struct {
	File        string       `json:"file"`
	Line        int          `json:"line"`
	Column      int          `json:"column"`
	LinesBefore []SourceLine `json:"lines_before"`
	ErrorLine   SourceLine   `json:"error_line"`
	LinesAfter  []SourceLine `json:"lines_after"`
}

// SourceLine is one numbered line of source.
type SourceLine struct {
	Number  int    `json:"number"`
	Content string `json:"content"`
}

// ErrorTracker turns an error into ErrorDetails. In development it also reads
// source files to show the code around each frame. Create one with
// NewErrorTracker.
type ErrorTracker struct {
	isDev       bool
	projectRoot string
}

// NewErrorTracker returns a tracker that shows file paths relative to the
// working directory, or absolute when the working directory cannot be read
// (logged). isDev turns on source capture for development error pages.
func NewErrorTracker(isDev bool) *ErrorTracker {
	pwd, err := os.Getwd()
	if err != nil {
		log.Printf("hyperchi: error reports show absolute paths: read working directory: %v", err)
		pwd = ""
	}

	return &ErrorTracker{
		isDev:       isDev,
		projectRoot: pwd,
	}
}

// CaptureError records err with the current stack, req, and the runtime
// environment, plus the source around the top frame in development. req may be
// nil.
func (et *ErrorTracker) CaptureError(err error, req *RequestInfo) *ErrorDetails {
	now := time.Now()

	details := &ErrorDetails{
		ID:          fmt.Sprintf("err_%d", now.UnixNano()),
		Type:        fmt.Sprintf("%T", err),
		Message:     err.Error(),
		Time:        now,
		Request:     req,
		Environment: et.captureEnvironment(),
	}

	details.StackTrace = et.captureStackTrace()

	if et.isDev && len(details.StackTrace) > 0 {
		details.SourceCode = et.captureSourceContext(details.StackTrace[0])
	}

	return details
}

// captureStackTrace records up to 20 frames of the caller's stack, stopping at
// a main or test function.
func (et *ErrorTracker) captureStackTrace() []StackFrame {
	var frames []StackFrame

	// Skip this function and the error-handling frames that called it.
	skip := 3
	for i := skip; i < skip+20; i++ {
		pc, file, line, ok := runtime.Caller(i)
		if !ok {
			break
		}

		fn := runtime.FuncForPC(pc)
		if fn == nil {
			continue
		}

		relFile := et.makeRelativePath(file)

		fullName := fn.Name()
		parts := strings.Split(fullName, ".")
		var pkg, funcName string
		if len(parts) >= 2 {
			pkg = strings.Join(parts[:len(parts)-1], ".")
			funcName = parts[len(parts)-1]
		} else {
			funcName = fullName
		}

		frame := StackFrame{
			File:     relFile,
			Line:     line,
			Function: funcName,
			Package:  pkg,
		}

		if et.isDev {
			frame.Code = et.getLineFromFile(file, line)
		}

		frames = append(frames, frame)

		if strings.HasSuffix(funcName, "main") || strings.Contains(funcName, "Test") {
			break
		}
	}

	return frames
}

// captureSourceContext returns the lines around frame, or nil outside
// development or when the file cannot be read.
func (et *ErrorTracker) captureSourceContext(frame StackFrame) *SourceContext {
	if !et.isDev {
		return nil
	}

	var filePath string
	if filepath.IsAbs(frame.File) {
		filePath = frame.File
	} else {
		filePath = filepath.Join(et.projectRoot, frame.File)
	}

	file, err := os.Open(filePath)
	if err != nil {
		return nil
	}
	defer func() { _ = file.Close() }() // best-effort close; file opened read-only

	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	if frame.Line <= 0 || frame.Line > len(lines) {
		return nil
	}

	context := &SourceContext{
		File: frame.File,
		Line: frame.Line,
	}

	// Get 5 lines before and after
	start := max(0, frame.Line-6)
	end := min(len(lines), frame.Line+5)

	for i := start; i < frame.Line-1; i++ {
		context.LinesBefore = append(context.LinesBefore, SourceLine{
			Number:  i + 1,
			Content: lines[i],
		})
	}

	if frame.Line-1 < len(lines) {
		context.ErrorLine = SourceLine{
			Number:  frame.Line,
			Content: lines[frame.Line-1],
		}
	}

	for i := frame.Line; i < end; i++ {
		context.LinesAfter = append(context.LinesAfter, SourceLine{
			Number:  i + 1,
			Content: lines[i],
		})
	}

	return context
}

// captureEnvironment records Go runtime details and the environment variables
// on a fixed allowlist of non-secret keys. Denylist redaction was rejected on
// purpose: custom-named credentials (DATABASE_URL and friends) slip through any
// substring filter, and ErrorDetails is serialized to JSON, so one leak here
// reaches logs and error handlers.
func (et *ErrorTracker) captureEnvironment() map[string]string {
	env := make(map[string]string)

	env["go_version"] = runtime.Version()
	env["go_os"] = runtime.GOOS
	env["go_arch"] = runtime.GOARCH
	env["num_goroutines"] = fmt.Sprintf("%d", runtime.NumGoroutine())

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	env["memory_alloc"] = fmt.Sprintf("%d KB", m.Alloc/1024)
	env["memory_sys"] = fmt.Sprintf("%d KB", m.Sys/1024)

	for _, key := range []string{"ENVIRONMENT", "GO_ENV", "APP_ENV", "PORT", "TZ", "LANG"} {
		if value, ok := os.LookupEnv(key); ok {
			env[key] = value
		}
	}

	return env
}

// makeRelativePath makes a file path relative to the project root for display.
func (et *ErrorTracker) makeRelativePath(filePath string) string {
	if et.projectRoot == "" {
		return filePath
	}
	if rel, err := filepath.Rel(et.projectRoot, filePath); err == nil {
		return rel
	}
	return filePath
}

// getLineFromFile returns line lineNum of filePath, trimmed, or "" when the file
// cannot be read.
func (et *ErrorTracker) getLineFromFile(filePath string, lineNum int) string {
	file, err := os.Open(filePath)
	if err != nil {
		return ""
	}
	defer func() { _ = file.Close() }() // best-effort close; file opened read-only

	scanner := bufio.NewScanner(file)
	currentLine := 1

	for scanner.Scan() {
		if currentLine == lineNum {
			return strings.TrimSpace(scanner.Text())
		}
		currentLine++
	}

	return ""
}

// ErrorPageHandler writes error responses: a page with the stack trace and
// source in development, a generic page in production. Create one with
// NewErrorPageHandler.
type ErrorPageHandler struct {
	tracker        *ErrorTracker
	isDev          bool
	template       string
	logErrors      bool
	customTemplate bool
	// ResponseHandler, when set, writes every response HandleStatus produces;
	// an app points it at its error pipeline. Set it before serving.
	ResponseHandler StatusHandler
}

// NewErrorPageHandler returns a handler that logs 5xx errors. isDev shows the
// stack trace and source on 500 pages.
func NewErrorPageHandler(isDev bool) *ErrorPageHandler {
	return &ErrorPageHandler{
		tracker:   NewErrorTracker(isDev),
		isDev:     isDev,
		template:  defaultErrorTemplate(),
		logErrors: true,
	}
}

// StatusHandler writes an application-specific error response for a status
// code and the error behind it (which may be nil).
type StatusHandler func(http.ResponseWriter, *http.Request, int, error)

// HandleError answers err with a 500. See HandleStatus.
func (eh *ErrorPageHandler) HandleError(err error, w http.ResponseWriter, r *http.Request) {
	eh.HandleStatus(err, w, r, http.StatusInternalServerError)
}

// HandleStatus answers err with status code; a code outside 400 to 599 becomes
// 500. If the response has already started it only logs err. Responses are
// marked no-store and noindex. When ResponseHandler is set (an app routes it
// into DefaultError or OnError) it writes the body; otherwise the built-in page
// does.
func (eh *ErrorPageHandler) HandleStatus(err error, w http.ResponseWriter, r *http.Request, code int) {
	eh.handleStatus(err, w, r, code, true)
}

// RenderDefault renders the built-in error page (full detail in development,
// a generic page in production) without consulting ResponseHandler.
func (eh *ErrorPageHandler) RenderDefault(err error, w http.ResponseWriter, r *http.Request, code int) {
	eh.handleStatus(err, w, r, code, false)
}

func (eh *ErrorPageHandler) handleStatus(err error, w http.ResponseWriter, r *http.Request, code int, delegate bool) {
	if code < 400 || code > 599 {
		code = http.StatusInternalServerError
	}
	if responseStarted(w) {
		if err != nil {
			log.Printf("hyperchi: error after response started: %v", err)
		}
		return
	}
	w.Header().Del("Content-Length")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex")
	if r.Method == http.MethodHead {
		w = headResponseWriter{w}
	}
	if delegate && eh.ResponseHandler != nil {
		eh.ResponseHandler(w, r, code, err)
		return
	}
	if err != nil && code >= 500 && eh.logErrors {
		log.Printf("hyperchi: %s %s: %v", r.Method, r.URL.Path, err)
	}
	if eh.customTemplate || (eh.isDev && code == http.StatusInternalServerError) {
		message := http.StatusText(code)
		details := &ErrorDetails{Message: message}
		if eh.isDev {
			if err == nil {
				err = errors.New(message)
			}
			details = eh.tracker.CaptureError(err, eh.captureRequestInfo(r))
		}
		tmpl, renderErr := template.New("error").Parse(eh.template)
		var buf bytes.Buffer
		if renderErr == nil {
			renderErr = tmpl.Execute(&buf, details)
		}
		if renderErr == nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(code)
			if _, writeErr := w.Write(buf.Bytes()); writeErr != nil {
				log.Printf("hyperchi: write error page: %v", writeErr)
			}
			return
		}
		log.Printf("hyperchi: render error page: %v", renderErr)
	}
	http.Error(w, http.StatusText(code), code)
}

type headResponseWriter struct{ http.ResponseWriter }

func (w headResponseWriter) Write(p []byte) (int, error) { return len(p), nil }
func (w headResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func responseStarted(w http.ResponseWriter) bool {
	for {
		if state, ok := w.(interface{ Status() int }); ok && state.Status() != 0 {
			return true
		}
		inner, ok := w.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			return false
		}
		w = inner.Unwrap()
	}
}

// captureRequestInfo copies r's method, URL, headers, query, and form for an
// error report, redacting credential headers, query parameters named like
// them, and form fields whose names look secret.
func (eh *ErrorPageHandler) captureRequestInfo(r *http.Request) *RequestInfo {
	headers := make(map[string]string)
	for key, values := range r.Header {
		if isSensitiveHeader(key) {
			headers[key] = "[REDACTED]"
			continue
		}
		headers[key] = strings.Join(values, ", ")
	}

	query := make(map[string]string)
	for key, values := range r.URL.Query() {
		if isSensitiveName(key) {
			query[key] = "[REDACTED]"
			continue
		}
		query[key] = strings.Join(values, ", ")
	}

	form := make(map[string]string)
	if r.Method == "POST" || r.Method == "PUT" || r.Method == "PATCH" {
		if err := r.ParseForm(); err != nil {
			log.Printf("hyperchi: failed to parse form for error report: %v", err)
		}
		for key, values := range r.PostForm {
			if eh.isSensitiveField(key) {
				form[key] = "[REDACTED]"
			} else {
				form[key] = strings.Join(values, ", ")
			}
		}
	}

	return &RequestInfo{
		Method:     r.Method,
		URL:        redactedURL(r.URL),
		RemoteAddr: r.RemoteAddr,
		UserAgent:  r.UserAgent(),
		Headers:    headers,
		Query:      query,
		Form:       form,
	}
}

// isSensitiveField reports whether a form field name looks like it holds a
// secret.
func (eh *ErrorPageHandler) isSensitiveField(field string) bool {
	return looksSecret(field)
}

// looksSecret reports whether a field or header name suggests a secret value.
func looksSecret(name string) bool {
	lower := strings.ToLower(name)
	for _, sensitive := range []string{
		"password", "passwd", "secret", "token", "key", "auth", "credential",
	} {
		if strings.Contains(lower, sensitive) {
			return true
		}
	}
	return false
}

// isSensitiveHeader reports whether a header carries credentials: the
// cookie headers, or a sensitive name (Authorization, Proxy-Authorization,
// X-Api-Key, X-Auth-Token, X-CSRF-Token, ...).
func isSensitiveHeader(name string) bool {
	switch http.CanonicalHeaderKey(name) {
	case "Cookie", "Set-Cookie":
		return true
	}
	return isSensitiveName(name)
}

// isSensitiveName reports whether a header or query parameter name suggests a
// credential: one that looks secret, or mentions session, signature, or csrf.
func isSensitiveName(name string) bool {
	lower := strings.ToLower(name)
	if strings.Contains(lower, "session") || strings.Contains(lower, "signature") || strings.Contains(lower, "csrf") {
		return true
	}
	return looksSecret(lower)
}

// redactedURL returns u as a string with the values of sensitive query
// parameters replaced by [REDACTED] and any password in the user info masked.
// Parameter names and order are kept, so the report still shows the request's
// shape.
func redactedURL(u *url.URL) string {
	clean := *u
	if clean.RawQuery != "" {
		pairs := strings.Split(clean.RawQuery, "&")
		for i, pair := range pairs {
			rawName, _, hasValue := strings.Cut(pair, "=")
			name, err := url.QueryUnescape(rawName)
			if err != nil {
				// Judge a name that does not unescape by its raw form.
				name = rawName
			}
			if hasValue && isSensitiveName(name) {
				pairs[i] = rawName + "=[REDACTED]"
			}
		}
		clean.RawQuery = strings.Join(pairs, "&")
	}
	return clean.Redacted()
}

// RecoveryMiddleware recovers a panic in the next handler and answers it
// through HandleError. http.ErrAbortHandler is re-panicked, and a panic after
// the response started aborts the connection instead of appending an error page.
func (eh *ErrorPageHandler) RecoveryMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			wrapped := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			defer func() {
				if rec := recover(); rec != nil {
					if err, ok := rec.(error); ok && errors.Is(err, http.ErrAbortHandler) {
						panic(rec)
					}
					// Headers and bytes already on the wire cannot be replaced by a 500.
					// Abort the transport instead of appending an error document to a stream.
					if wrapped.Status() != 0 {
						log.Printf("hyperchi: panic after response started: %v", rec)
						panic(http.ErrAbortHandler)
					}
					err, ok := rec.(error)
					if !ok {
						err = fmt.Errorf("panic: %v", rec)
					}
					eh.HandleError(err, wrapped, r)
				}
			}()
			next.ServeHTTP(wrapped, r)
		})
	}
}

func defaultErrorTemplate() string {
	return errorPageTemplate
}

// errorPageTemplate is the built-in error page, executed with *ErrorDetails.
const errorPageTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
	<meta charset="UTF-8">
	<meta name="viewport" content="width=device-width, initial-scale=1.0">
	<title>{{.Type}} - HyperChi Error</title>
	<style>
		* { margin: 0; padding: 0; box-sizing: border-box; }
		body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; background: linear-gradient(135deg, #667eea 0%, #764ba2 100%); min-height: 100vh; padding: 20px; }
		.error-container { max-width: 1200px; margin: 0 auto; background: #fff; border-radius: 12px; box-shadow: 0 20px 40px rgba(0,0,0,0.1); overflow: hidden; }
		.error-header { background: linear-gradient(135deg, #ff6b6b, #ee5a24); color: white; padding: 30px; text-align: center; }
		.error-header h1 { font-size: 2.5rem; margin-bottom: 10px; font-weight: 700; }
		.error-header .error-id { opacity: 0.9; font-family: 'Monaco', 'Consolas', monospace; font-size: 0.9rem; }
		.error-message { background: #fff3cd; border-left: 4px solid #ffc107; padding: 20px; margin: 20px; border-radius: 8px; }
		.error-message h2 { color: #856404; margin-bottom: 10px; font-size: 1.2rem; }
		.error-message p { color: #856404; font-family: 'Monaco', 'Consolas', monospace; line-height: 1.6; word-break: break-all; }
		.stack-trace { font-family: 'Monaco', 'Consolas', monospace; font-size: 0.9rem; padding: 20px; }
		.stack-frame { border: 1px solid #e9ecef; border-radius: 8px; margin-bottom: 15px; overflow: hidden; }
		.stack-frame-header { background: #f8f9fa; padding: 12px 15px; border-bottom: 1px solid #e9ecef; }
		.stack-frame-function { font-weight: 600; color: #dc3545; }
		.stack-frame-location { color: #6c757d; font-size: 0.85rem; }
		.timestamp { color: #6c757d; font-size: 0.9rem; }
	</style>
</head>
<body>
	<div class="error-container">
		<div class="error-header">
			<h1>{{.Type}}</h1>
			<div class="error-id">Error ID: {{.ID}}</div>
			<div class="timestamp">{{.Time.Format "2006-01-02 15:04:05 MST"}}</div>
		</div>
		<div class="error-message">
			<h2>Error Message</h2>
			<p>{{.Message}}</p>
		</div>
		<div class="stack-trace">
			{{range .StackTrace}}
			<div class="stack-frame">
				<div class="stack-frame-header">
					<div class="stack-frame-function">{{.Package}}.{{.Function}}</div>
					<div class="stack-frame-location">{{.File}}:{{.Line}}</div>
					{{if .Code}}<div style="margin-top: 8px; background: #282c34; color: #abb2bf; padding: 8px; border-radius: 4px;"><code>{{.Code}}</code></div>{{end}}
				</div>
			</div>
			{{end}}
		</div>
	</div>
</body>
</html>`

// EnableErrorRecovery installs ErrorPageHandler.RecoveryMiddleware on the app.
func (g *HyperChi) EnableErrorRecovery() {
	g.Use(g.ErrorPageHandler.RecoveryMiddleware())
}

// SetErrorTemplate replaces the built-in error page template. The template
// executes with *ErrorDetails and, once set, renders every status the built-in
// page handles, not only development 500s.
func (g *HyperChi) SetErrorTemplate(template string) {
	g.ErrorPageHandler.template = template
	g.ErrorPageHandler.customTemplate = true
}
