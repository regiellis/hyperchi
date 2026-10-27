package hyperchi

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
)

const devToolbarPath = "/__hyperchi/devtools"

//go:embed devtoolbar/toolbar.js
var devToolbarJS string

//go:embed devtoolbar/toolbar.css
var devToolbarCSS string

// DevToolbarOptions controls the opt-in local developer toolbar.
type DevToolbarOptions struct {
	// MaxRequests bounds retained completed requests. Zero uses 100; max is 1000.
	MaxRequests int
}

type devToolbar struct {
	mu       sync.Mutex
	requests []devRequest
	next     int
	count    int
	sequence uint64
}

type devRequest struct {
	Aborted    bool      `json:"aborted"`
	ID         uint64    `json:"id"`
	Method     string    `json:"method"`
	Route      string    `json:"route"`
	Status     int       `json:"status"`
	Bytes      int       `json:"bytes"`
	DurationMS float64   `json:"durationMS"`
	Started    time.Time `json:"started"`
	HTMX       bool      `json:"htmx"`
}

type devRoute struct {
	Method  string `json:"method"`
	Pattern string `json:"pattern"`
}
type devLocalKey struct{}
type devRequestKey struct{}

// EnableDevToolbar enables a local developer toolbar. Call on the root app
// before registering any routes; put {{devToolbar}} in the full-page layout.
// In production this is a no-op. Repeated development calls return an error.
// The reserved /__hyperchi/devtools/* endpoints accept only direct loopback
// clients using a localhost or loopback Host and same-origin browser requests.
// Forwarded headers are ignored. This is not a remotely accessible dashboard.
func (g *HyperChi) EnableDevToolbar(opts DevToolbarOptions) error {
	if !g.isDevelopment() {
		return nil
	}
	if g.routePrefix != "" || g.devToolbar != nil || len(g.router.Routes()) != 0 {
		return fmt.Errorf("enable developer toolbar once on the root app before registering routes")
	}
	capacity := opts.MaxRequests
	if capacity == 0 {
		capacity = 100
	}
	if capacity < 1 || capacity > 1000 {
		return fmt.Errorf("developer toolbar MaxRequests must be between 1 and 1000")
	}
	toolbar := &devToolbar{requests: make([]devRequest, capacity)}
	g.devToolbar = toolbar
	// Run the access check before router middleware such as RealIP can rewrite
	// RemoteAddr. Keep its result private rather than trusting forwarded headers.
	gate := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			local := g.isDevelopment() && devLocalRequest(r)
			if (r.URL.Path == devToolbarPath || strings.HasPrefix(r.URL.Path, devToolbarPath+"/")) && !local {
				http.NotFound(w, r)
				return
			}
			r = r.WithContext(context.WithValue(r.Context(), devLocalKey{}, local))
			if !local || strings.HasPrefix(r.URL.Path, "/__hyperchi/") {
				next.ServeHTTP(w, r)
				return
			}
			started := time.Now()
			method := r.Method
			if len(method) > 32 {
				method = "(other)"
			}
			record := &devRequest{Method: method, Route: "(unmatched)", Started: started.UTC(), HTMX: r.Header.Get("HX-Request") == "true"}
			r = r.WithContext(context.WithValue(r.Context(), devRequestKey{}, record))
			writer := chimiddleware.NewWrapResponseWriter(w, r.ProtoMajor)
			completed := false
			defer func() {
				record.Aborted = !completed
				record.Status = writer.Status()
				if record.Status == 0 && completed {
					record.Status = http.StatusOK
				}
				record.Bytes = writer.BytesWritten()
				record.DurationMS = float64(time.Since(started)) / float64(time.Millisecond)
				toolbar.add(*record)
			}()
			next.ServeHTTP(writer, r)
			completed = true
		})
	}
	g.deferredMiddleware = append([]func(http.Handler) http.Handler{gate}, g.deferredMiddleware...)
	// Route patterns become available inside chi. Transfer only the pattern
	// to the outer recorder, which also observes middleware rejections and
	// recovery responses without creating or replacing chi's routing context.
	g.router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if record, ok := r.Context().Value(devRequestKey{}).(*devRequest); ok {
				defer func() {
					if pattern := chi.RouteContext(r.Context()).RoutePattern(); pattern != "" {
						record.Route = pattern
					}
				}()
			}
			next.ServeHTTP(w, r)
		})
	})
	g.router.Get(devToolbarPath+"/toolbar.js", g.devAsset("text/javascript; charset=utf-8", devToolbarJS))
	g.router.Get(devToolbarPath+"/toolbar.css", g.devAsset("text/css; charset=utf-8", devToolbarCSS))
	g.router.Get(devToolbarPath+"/data", func(w http.ResponseWriter, r *http.Request) {
		if !g.devAllowed(w, r) {
			return
		}
		routes := []devRoute{}
		err := chi.Walk(g.router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
			if !strings.HasPrefix(route, devToolbarPath) {
				routes = append(routes, devRoute{method, route})
			}
			return nil
		})
		if err != nil {
			http.Error(w, "cannot list routes", 500)
			return
		}
		sort.Slice(routes, func(i, j int) bool {
			if routes[i].Pattern == routes[j].Pattern {
				return routes[i].Method < routes[j].Method
			}
			return routes[i].Pattern < routes[j].Pattern
		})
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if r.Method == http.MethodHead {
			return
		}
		_ = json.NewEncoder(w).Encode(struct {
			Routes   []devRoute   `json:"routes"`
			Requests []devRequest `json:"requests"`
		}{routes, toolbar.snapshot()})
	})
	return nil
}

// DevToolbar returns an external script tag when the toolbar is enabled in
// development, otherwise empty HTML. Templates use {{devToolbar}}. Include it
// once in the full-page layout; partial responses are never rewritten.
func (g *HyperChi) DevToolbar() template.HTML {
	if g.devToolbar == nil || !g.isDevelopment() {
		return ""
	}
	return template.HTML(`<script defer src="` + devToolbarPath + `/toolbar.js"></script>`)
}

func (g *HyperChi) devAllowed(w http.ResponseWriter, r *http.Request) bool {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	local, _ := r.Context().Value(devLocalKey{}).(bool)
	if !local || !g.isDevelopment() {
		http.NotFound(w, r)
		return false
	}
	return true
}
func (g *HyperChi) devAsset(media, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !g.devAllowed(w, r) {
			return
		}
		w.Header().Set("Content-Type", media)
		if r.Method == http.MethodHead {
			return
		}
		_, _ = w.Write([]byte(body))
	}
}
func devLocalRequest(r *http.Request) bool {
	remote, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || !net.ParseIP(remote).IsLoopback() {
		return false
	}
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if !strings.EqualFold(host, "localhost") && !net.ParseIP(host).IsLoopback() {
		return false
	}
	site := r.Header.Get("Sec-Fetch-Site")
	if site != "" && site != "same-origin" && site != "none" {
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		if origin != scheme+"://"+r.Host {
			return false
		}
	}
	return true
}
func (t *devToolbar) add(request devRequest) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sequence++
	request.ID = t.sequence
	t.requests[t.next] = request
	t.next = (t.next + 1) % len(t.requests)
	if t.count < len(t.requests) {
		t.count++
	}
}
func (t *devToolbar) snapshot() []devRequest {
	t.mu.Lock()
	defer t.mu.Unlock()
	result := make([]devRequest, t.count)
	for i := range result {
		index := (t.next - 1 - i + len(t.requests)) % len(t.requests)
		result[i] = t.requests[index]
	}
	return result
}
