// Package logging is a structured logger over log/slog, writing JSON or text
// records that carry service, version, and environment fields. It adds request
// and trace IDs from the context, a package-level default logger, and HTTP
// request-logging middleware.
package logging

import (
	"context"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/regiellis/hyperchi/hyperchi/config"
	"github.com/regiellis/hyperchi/hyperchi/security"
)

// ctxKey is private so this package's context keys cannot collide with other
// packages' keys.
type ctxKey int

const (
	requestIDCtxKey ctxKey = iota
	traceIDCtxKey
)

// ContextWithRequestID returns a context carrying a request ID.
func ContextWithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDCtxKey, id)
}

// RequestIDFromContext returns the request ID, or "" when absent.
func RequestIDFromContext(ctx context.Context) string {
	if id, ok := ctx.Value(requestIDCtxKey).(string); ok {
		return id
	}
	return ""
}

// ContextWithTraceID returns a context carrying a trace ID.
func ContextWithTraceID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, traceIDCtxKey, id)
}

// TraceIDFromContext returns the trace ID, or "" when absent.
func TraceIDFromContext(ctx context.Context) string {
	if id, ok := ctx.Value(traceIDCtxKey).(string); ok {
		return id
	}
	return ""
}

// Level is a log severity. The values match slog's levels, with LevelFatal
// above slog.LevelError.
type Level int

// Log levels, least severe first.
const (
	LevelDebug Level = iota - 4
	LevelInfo  Level = 0
	LevelWarn  Level = 4
	LevelError Level = 8
	LevelFatal Level = 12
)

// String returns the level's upper-case name, or "UNKNOWN".
func (l Level) String() string {
	switch l {
	case LevelDebug:
		return "DEBUG"
	case LevelInfo:
		return "INFO"
	case LevelWarn:
		return "WARN"
	case LevelError:
		return "ERROR"
	case LevelFatal:
		return "FATAL"
	default:
		return "UNKNOWN"
	}
}

// Logger writes leveled records with a fixed set of fields. The With methods
// return a new Logger and never modify the receiver, so a Logger is safe to
// share between goroutines.
type Logger struct {
	handler slog.Handler
	level   Level
	fields  map[string]any
}

// Config configures NewLogger.
type Config struct {
	Level       Level
	Format      string // "json", or anything else for text
	Output      io.Writer
	AddSource   bool
	TimeFormat  string // layout for each record's time (time.Format); "" keeps slog's RFC 3339 time
	ServiceName string
	Version     string
	Environment string
}

// DefaultConfig returns Info-level JSON on stderr, with the service name
// "hyperchi", the framework version, and the environment from ENVIRONMENT or
// GO_ENV (default "development"). Stderr keeps log records out of a
// program's stdout output.
func DefaultConfig() *Config {
	return &Config{
		Level:       LevelInfo,
		Format:      "json",
		Output:      os.Stderr,
		AddSource:   false,
		ServiceName: "hyperchi",
		Version:     config.FrameworkVersion(),
		Environment: environmentName(),
	}
}

// NewLogger returns a Logger for config, or for DefaultConfig when config is
// nil.
func NewLogger(config *Config) *Logger {
	if config == nil {
		config = DefaultConfig()
	}

	var handler slog.Handler

	opts := &slog.HandlerOptions{
		Level:     slog.Level(config.Level),
		AddSource: config.AddSource,
	}
	if layout := config.TimeFormat; layout != "" {
		opts.ReplaceAttr = func(groups []string, a slog.Attr) slog.Attr {
			if len(groups) == 0 && a.Key == slog.TimeKey && a.Value.Kind() == slog.KindTime {
				return slog.String(slog.TimeKey, a.Value.Time().Format(layout))
			}
			return a
		}
	}

	if config.Format == "json" {
		handler = slog.NewJSONHandler(config.Output, opts)
	} else {
		handler = slog.NewTextHandler(config.Output, opts)
	}

	fields := map[string]any{
		"service":     config.ServiceName,
		"version":     config.Version,
		"environment": config.Environment,
	}

	return &Logger{
		handler: handler,
		level:   config.Level,
		fields:  fields,
	}
}

// WithField returns a copy of l that adds key=value to every record.
func (l *Logger) WithField(key string, value any) *Logger {
	newFields := make(map[string]any)
	for k, v := range l.fields {
		newFields[k] = v
	}
	newFields[key] = value

	return &Logger{
		handler: l.handler,
		level:   l.level,
		fields:  newFields,
	}
}

// WithFields returns a copy of l that adds fields to every record, replacing
// fields with the same key.
func (l *Logger) WithFields(fields map[string]any) *Logger {
	newFields := make(map[string]any)
	for k, v := range l.fields {
		newFields[k] = v
	}
	for k, v := range fields {
		newFields[k] = v
	}

	return &Logger{
		handler: l.handler,
		level:   l.level,
		fields:  newFields,
	}
}

// WithError returns a copy of l with error and error_type fields, or l itself
// when err is nil.
func (l *Logger) WithError(err error) *Logger {
	if err == nil {
		return l
	}
	return l.WithField("error", err.Error()).WithField("error_type", fmt.Sprintf("%T", err))
}

// WithContext returns a copy of l with the request_id, user_id, and trace_id
// that ctx carries, skipping any that are absent.
func (l *Logger) WithContext(ctx context.Context) *Logger {
	fields := make(map[string]any)

	if reqID := RequestIDFromContext(ctx); reqID != "" {
		fields["request_id"] = reqID
	}
	if userID := security.GetUserIDFromContext(ctx); userID != "" {
		fields["user_id"] = userID
	}
	if traceID := TraceIDFromContext(ctx); traceID != "" {
		fields["trace_id"] = traceID
	}

	return l.WithFields(fields)
}

// log writes one record if level is at or above the logger's level. Fields
// that are nil or "" are left out. args are key-value pairs: a pair whose key
// is not a string, and an odd trailing argument, are dropped. A Debug-level
// logger adds a caller field naming the code that called the exported method.
// Through ContextualLogger or a package-level helper, that is the wrapper
// itself. LevelFatal exits
// the process with status 1 after writing, without running deferred calls.
func (l *Logger) log(ctx context.Context, level Level, msg string, args ...any) {
	if level < l.level {
		return
	}

	attrs := make([]slog.Attr, 0, len(l.fields)+len(args)/2)

	for k, v := range l.fields {
		if v != nil && v != "" {
			attrs = append(attrs, slog.Any(k, v))
		}
	}

	if l.level <= LevelDebug {
		// Skip log and the exported method to reach their caller.
		if pc, file, line, ok := runtime.Caller(2); ok {
			fn := runtime.FuncForPC(pc)
			if fn != nil {
				name := fn.Name()
				if idx := strings.LastIndex(name, "."); idx != -1 {
					name = name[idx+1:]
				}
				attrs = append(attrs, slog.String("caller", fmt.Sprintf("%s:%d:%s", getFileName(file), line, name)))
			}
		}
	}

	for i := 0; i < len(args)-1; i += 2 {
		if key, ok := args[i].(string); ok {
			attrs = append(attrs, slog.Any(key, args[i+1]))
		}
	}

	record := slog.NewRecord(time.Now(), slog.Level(level), msg, 0)
	record.AddAttrs(attrs...)

	if ctx == nil {
		ctx = context.Background()
	}

	// A failed write cannot be logged through this logger, so it goes to the
	// standard library logger like the rest of the framework's errors.
	if err := l.handler.Handle(ctx, record); err != nil {
		log.Printf("hyperchi: logging: write record: %v", err)
	}

	if level >= LevelFatal {
		os.Exit(1)
	}
}

// Debug logs msg at LevelDebug with key-value args.
func (l *Logger) Debug(msg string, args ...any) {
	l.log(context.Background(), LevelDebug, msg, args...)
}

// DebugContext logs msg at LevelDebug, passing ctx to the slog handler. It
// does not add ctx's IDs. WithContext does that.
func (l *Logger) DebugContext(ctx context.Context, msg string, args ...any) {
	l.log(ctx, LevelDebug, msg, args...)
}

// Info logs msg at LevelInfo with key-value args.
func (l *Logger) Info(msg string, args ...any) {
	l.log(context.Background(), LevelInfo, msg, args...)
}

// InfoContext logs msg at LevelInfo, passing ctx to the slog handler.
func (l *Logger) InfoContext(ctx context.Context, msg string, args ...any) {
	l.log(ctx, LevelInfo, msg, args...)
}

// Warn logs msg at LevelWarn with key-value args.
func (l *Logger) Warn(msg string, args ...any) {
	l.log(context.Background(), LevelWarn, msg, args...)
}

// WarnContext logs msg at LevelWarn, passing ctx to the slog handler.
func (l *Logger) WarnContext(ctx context.Context, msg string, args ...any) {
	l.log(ctx, LevelWarn, msg, args...)
}

// Error logs msg at LevelError with key-value args.
func (l *Logger) Error(msg string, args ...any) {
	l.log(context.Background(), LevelError, msg, args...)
}

// ErrorContext logs msg at LevelError, passing ctx to the slog handler.
func (l *Logger) ErrorContext(ctx context.Context, msg string, args ...any) {
	l.log(ctx, LevelError, msg, args...)
}

// Fatal logs msg at LevelFatal, then exits with status 1. Deferred calls do
// not run.
func (l *Logger) Fatal(msg string, args ...any) {
	l.log(context.Background(), LevelFatal, msg, args...)
}

// FatalContext logs msg at LevelFatal, passing ctx to the slog handler, then
// exits with status 1.
func (l *Logger) FatalContext(ctx context.Context, msg string, args ...any) {
	l.log(ctx, LevelFatal, msg, args...)
}

// getFileName returns the part of a slash-separated path after the last slash.
func getFileName(path string) string {
	if idx := strings.LastIndex(path, "/"); idx != -1 {
		return path[idx+1:]
	}
	return path
}

// defaultLogger is the logger behind the package-level helpers. It is an
// atomic pointer because SetDefaultLogger may run while requests log.
var defaultLogger atomic.Pointer[Logger]

// SetDefaultLogger sets the default logger. It is safe for concurrent use;
// nil restores a lazily created logger with DefaultConfig.
func SetDefaultLogger(logger *Logger) {
	defaultLogger.Store(logger)
}

// GetDefaultLogger returns the default logger, creating one with
// DefaultConfig on first use. It is safe for concurrent use.
func GetDefaultLogger() *Logger {
	for {
		if l := defaultLogger.Load(); l != nil {
			return l
		}
		created := NewLogger(DefaultConfig())
		if defaultLogger.CompareAndSwap(nil, created) {
			return created
		}
	}
}

// Debug logs msg at LevelDebug through the default logger.
func Debug(msg string, args ...any) {
	GetDefaultLogger().Debug(msg, args...)
}

// Info logs msg at LevelInfo through the default logger.
func Info(msg string, args ...any) {
	GetDefaultLogger().Info(msg, args...)
}

// Warn logs msg at LevelWarn through the default logger.
func Warn(msg string, args ...any) {
	GetDefaultLogger().Warn(msg, args...)
}

// Error logs msg at LevelError through the default logger.
func Error(msg string, args ...any) {
	GetDefaultLogger().Error(msg, args...)
}

// Fatal logs msg at LevelFatal through the default logger, then exits with
// status 1.
func Fatal(msg string, args ...any) {
	GetDefaultLogger().Fatal(msg, args...)
}

// WithField returns a copy of the default logger with key=value.
func WithField(key string, value any) *Logger {
	return GetDefaultLogger().WithField(key, value)
}

// WithFields returns a copy of the default logger with fields.
func WithFields(fields map[string]any) *Logger {
	return GetDefaultLogger().WithFields(fields)
}

// WithError returns a copy of the default logger with err's fields.
func WithError(err error) *Logger {
	return GetDefaultLogger().WithError(err)
}

// RequestLogger returns middleware that logs "Request started" and "Request
// completed" at Info for every request, with method, path, status, duration,
// and bytes written. A nil logger means the default logger.
//
// It reuses the client's X-Request-ID header as is, or sets one on the request
// from the time and process ID, and stores it in the context (see
// RequestIDFromContext). The client IP comes from chi's ClientIPFrom*
// middleware or the TCP peer, never from forwarding headers.
//
// The wrapped ResponseWriter does not implement http.Flusher or
// http.Hijacker, so keep this middleware off SSE and WebSocket routes.
func RequestLogger(logger *Logger) func(http.Handler) http.Handler {
	if logger == nil {
		logger = GetDefaultLogger()
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			requestID := r.Header.Get("X-Request-ID")
			if requestID == "" {
				requestID = generateRequestID()
				r.Header.Set("X-Request-ID", requestID)
			}

			ctx := ContextWithRequestID(r.Context(), requestID)
			r = r.WithContext(ctx)

			wrapped := &responseWriter{ResponseWriter: w, statusCode: 200}

			logger.WithFields(map[string]any{
				"request_id": requestID,
				"method":     r.Method,
				"path":       r.URL.Path,
				"query":      r.URL.RawQuery,
				"user_agent": r.UserAgent(),
				"remote_ip":  getClientIP(r),
				"referer":    r.Referer(),
			}).Info("Request started")

			next.ServeHTTP(wrapped, r)

			duration := time.Since(start)
			logger.WithFields(map[string]any{
				"request_id":    requestID,
				"method":        r.Method,
				"path":          r.URL.Path,
				"status_code":   wrapped.statusCode,
				"duration_ms":   duration.Milliseconds(),
				"duration":      duration.String(),
				"bytes_written": wrapped.bytesWritten,
			}).Info("Request completed")
		})
	}
}

// responseWriter records the status code and body size it passes through.
type responseWriter struct {
	http.ResponseWriter
	statusCode   int
	bytesWritten int64
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	n, err := rw.ResponseWriter.Write(b)
	rw.bytesWritten += int64(n)
	return n, err
}

// getClientIP returns the client IP a chi ClientIPFrom* middleware stored for
// the request (see MiddlewareGroup.ClientIP), or else the TCP peer. Forwarding
// headers are never read here: any client can set them.
func getClientIP(r *http.Request) string {
	if ip := middleware.GetClientIP(r.Context()); ip != "" {
		return ip
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// generateRequestID returns "<unix nanoseconds>-<pid>". It is unique within a
// host, not across hosts, and is not random.
func generateRequestID() string {
	return fmt.Sprintf("%d-%d", time.Now().UnixNano(), os.Getpid())
}

// SetupDevelopmentLogging makes the default logger Debug-level text with
// source locations.
func SetupDevelopmentLogging() {
	config := DefaultConfig()
	config.Level = LevelDebug
	config.Format = "text"
	config.AddSource = true

	logger := NewLogger(config)
	SetDefaultLogger(logger)
}

// SetupProductionLogging makes the default logger Info-level JSON.
func SetupProductionLogging() {
	config := DefaultConfig()
	config.Level = LevelInfo
	config.Format = "json"
	config.AddSource = false

	logger := NewLogger(config)
	SetDefaultLogger(logger)
}

// SetupTestLogging makes the default logger discard all output.
func SetupTestLogging() {
	config := DefaultConfig()
	config.Level = LevelError
	config.Output = io.Discard

	logger := NewLogger(config)
	SetDefaultLogger(logger)
}

// ContextualLogger is a Logger bound to one context, usually a request's.
type ContextualLogger struct {
	logger *Logger
	ctx    context.Context
}

// NewContextualLogger returns logger (or the default logger when nil) with
// ctx's IDs added (see WithContext), bound to ctx.
func NewContextualLogger(ctx context.Context, logger *Logger) *ContextualLogger {
	if logger == nil {
		logger = GetDefaultLogger()
	}

	return &ContextualLogger{
		logger: logger.WithContext(ctx),
		ctx:    ctx,
	}
}

// Debug logs msg at LevelDebug.
func (cl *ContextualLogger) Debug(msg string, args ...any) {
	cl.logger.DebugContext(cl.ctx, msg, args...)
}

// Info logs msg at LevelInfo.
func (cl *ContextualLogger) Info(msg string, args ...any) {
	cl.logger.InfoContext(cl.ctx, msg, args...)
}

// Warn logs msg at LevelWarn.
func (cl *ContextualLogger) Warn(msg string, args ...any) {
	cl.logger.WarnContext(cl.ctx, msg, args...)
}

// Error logs msg at LevelError.
func (cl *ContextualLogger) Error(msg string, args ...any) {
	cl.logger.ErrorContext(cl.ctx, msg, args...)
}

// Fatal logs msg at LevelFatal, then exits with status 1.
func (cl *ContextualLogger) Fatal(msg string, args ...any) {
	cl.logger.FatalContext(cl.ctx, msg, args...)
}

// WithField returns a copy of cl with key=value.
func (cl *ContextualLogger) WithField(key string, value any) *ContextualLogger {
	return &ContextualLogger{
		logger: cl.logger.WithField(key, value),
		ctx:    cl.ctx,
	}
}

// WithError returns a copy of cl with err's fields.
func (cl *ContextualLogger) WithError(err error) *ContextualLogger {
	return &ContextualLogger{
		logger: cl.logger.WithError(err),
		ctx:    cl.ctx,
	}
}

// environmentName reports the deployment environment the framework uses to
// decide development mode: ENVIRONMENT, else GO_ENV, else "development".
func environmentName() string {
	for _, key := range []string{"ENVIRONMENT", "GO_ENV"} {
		if v := os.Getenv(key); v != "" {
			return v
		}
	}
	return "development"
}
