// Package analytics provides opt-in, provider-neutral completed-request hooks.
// It has no storage, background workers, network transport, or visitor identity.
package analytics

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

// RequestEvent describes a completed handler invocation. Route is a registered
// chi pattern (possibly a mount prefix), or empty if unavailable. Raw paths,
// query strings, headers, IPs,
// cookies, and request/response bodies are never copied into the event.
// Status is zero if an aborted or hijacked handler never committed a response.
// Bytes counts
// writes observed here, not necessarily bytes transmitted after compression.
type RequestEvent struct {
	Hijacked bool          `json:"hijacked"`
	Method   string        `json:"method"`
	Route    string        `json:"route"`
	Status   int           `json:"status"`
	Bytes    int           `json:"bytes"`
	Duration time.Duration `json:"duration"`
	HTMX     bool          `json:"htmx"`
	Aborted  bool          `json:"aborted"`
}

// Recorder receives immutable event values synchronously. Implementations must
// be safe for concurrent calls and return promptly. Context is the request
// context (possibly canceled); do not retain it or extract visitor identifiers.
// Adapters own their timeouts, bounded queues, retries, and shutdown lifecycle.
type Recorder interface {
	Record(context.Context, RequestEvent) error
}

// RecorderFunc adapts a function into a Recorder.
type RecorderFunc func(context.Context, RequestEvent) error

func (f RecorderFunc) Record(ctx context.Context, event RequestEvent) error { return f(ctx, event) }

// Config enables request collection explicitly. Include can enforce application
// consent or route exclusions; nil includes all eligible requests. DNT: 1,
// Sec-GPC: 1, and speculative prefetch requests are always skipped. OnError
// receives recorder failures; nil discards them. Callbacks must be concurrency-safe.
type Config struct {
	Recorder Recorder
	Include  func(*http.Request) bool
	OnError  func(error)
}

// Collector is immutable. Construct it with New before serving requests.
type Collector struct{ config Config }

// New validates the recorder and constructs a collector without starting work.
func New(config Config) (*Collector, error) {
	if config.Recorder == nil {
		return nil, errors.New("analytics: recorder is required")
	}
	return &Collector{config: config}, nil
}

func (c *Collector) report(err error) {
	if err == nil || c.config.OnError == nil {
		return
	}
	defer func() { _ = recover() }()
	c.config.OnError(err)
}

func (c *Collector) eligible(r *http.Request) (allowed bool) {
	if r.Header.Get("DNT") == "1" || r.Header.Get("Sec-GPC") == "1" {
		return false
	}
	for _, header := range []string{"Purpose", "Sec-Purpose"} {
		if strings.Contains(strings.ToLower(r.Header.Get(header)), "prefetch") {
			return false
		}
	}
	if c.config.Include == nil {
		return true
	}
	defer func() {
		if recover() != nil {
			allowed = false
			c.report(errors.New("analytics: include callback panicked"))
		}
	}()
	return c.config.Include(r)
}

func (c *Collector) record(ctx context.Context, event RequestEvent) {
	defer func() {
		if recover() != nil {
			c.report(errors.New("analytics: recorder panicked"))
		}
	}()
	c.report(c.config.Recorder.Record(ctx, event))
}

// Middleware records after the handler returns, including handler errors already
// rendered by HyperChi. Install inside chi with Use before routes, so the route
// context is visible. A surrounding panic recovery runs later: such an event is
// marked Aborted and reports only the status committed before the panic. Panics
// still propagate to that recovery. Streaming events arrive when handlers end;
// WebSocket frames written after hijacking are not measured. Recorder failures
// and callback panics never replace the application response.
func (c *Collector) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !c.eligible(r) {
			next.ServeHTTP(w, r)
			return
		}
		started := time.Now()
		writer := &responseWriter{ResponseWriter: w}
		completed := false
		defer func() {
			status := writer.status
			if status == 0 && completed && !writer.hijacked {
				status = http.StatusOK
			}
			route := ""
			if routeContext := chi.RouteContext(r.Context()); routeContext != nil {
				route = routeContext.RoutePattern()
			}
			method := r.Method
			switch method {
			case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "CONNECT", "TRACE":
			default:
				method = "OTHER"
			}
			c.record(r.Context(), RequestEvent{Method: method, Route: route, Status: status, Bytes: writer.bytes,
				Duration: time.Since(started), HTMX: r.Header.Get("HX-Request") == "true",
				Aborted: !completed, Hijacked: writer.hijacked})
		}()
		next.ServeHTTP(writer, r)
		completed = true
	})
}
