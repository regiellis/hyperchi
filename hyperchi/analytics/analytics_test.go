package analytics_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/regiellis/hyperchi/hyperchi/analytics"
)

func TestRequestEvents(t *testing.T) {
	t.Parallel()
	var events []analytics.RequestEvent
	collector, err := analytics.New(analytics.Config{Recorder: analytics.RecorderFunc(func(_ context.Context, e analytics.RequestEvent) error { events = append(events, e); return nil })})
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	router.Use(collector.Middleware)
	router.Route("/users", func(r chi.Router) {
		r.Get("/{id}", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(201)
			w.WriteHeader(202)
			_, _ = w.Write([]byte("ok"))
		})
	})
	router.Get("/empty", func(http.ResponseWriter, *http.Request) {})
	for _, tt := range []struct {
		name, path, route string
		status, bytes     int
	}{
		{"matched", "/users/private-id?secret=password", "/users/{id}", 201, 2},
		{"unmatched", "/private-missing?token=secret", "", 404, 19},
		{"implicit", "/empty", "/empty", 200, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", tt.path, nil)
			req.Header.Set("HX-Request", "true")
			router.ServeHTTP(httptest.NewRecorder(), req)
			e := events[len(events)-1]
			if e.Route != tt.route || e.Status != tt.status || e.Bytes != tt.bytes || e.Method != "GET" || !e.HTMX || e.Aborted || e.Duration < 0 {
				t.Fatalf("event: %+v", e)
			}
		})
	}
}

func TestExclusions(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, header, value string
		include             bool
	}{
		{"dnt", "DNT", "1", true}, {"gpc", "Sec-GPC", "1", true}, {"prefetch", "Purpose", "prefetch", true},
		{"secure prefetch", "Sec-Purpose", "prefetch;prerender", true}, {"consent", "", "", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			c, err := analytics.New(analytics.Config{Recorder: analytics.RecorderFunc(func(context.Context, analytics.RequestEvent) error { called = true; return nil }), Include: func(*http.Request) bool { return tt.include }})
			if err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest("GET", "/", nil)
			r.Header.Set(tt.header, tt.value)
			w := httptest.NewRecorder()
			c.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })).ServeHTTP(w, r)
			if called || w.Code != 204 {
				t.Fatal("excluded request recorded or response changed")
			}
		})
	}
}

func TestFailuresDoNotChangeResponse(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"error", "recorder panic", "include panic", "error callback panic"} {
		t.Run(mode, func(t *testing.T) {
			reported := false
			cfg := analytics.Config{Recorder: analytics.RecorderFunc(func(context.Context, analytics.RequestEvent) error {
				if mode == "recorder panic" {
					panic("private data")
				}
				return errors.New("offline")
			}), OnError: func(error) {
				reported = true
				if mode == "error callback panic" {
					panic("broken reporter")
				}
			}}
			if mode == "include panic" {
				cfg.Include = func(*http.Request) bool { panic("bad filter") }
			}
			c, err := analytics.New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			c.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })).ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
			if !reported || w.Code != 200 || w.Body.String() != "ok" {
				t.Fatal("failure escaped or was not reported")
			}
		})
	}
}

func TestPanicAndStreaming(t *testing.T) {
	t.Parallel()
	var event analytics.RequestEvent
	c, err := analytics.New(analytics.Config{Recorder: analytics.RecorderFunc(func(_ context.Context, e analytics.RequestEvent) error { event = e; return nil })})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	c.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Error(err)
		}
		_, _ = w.Write([]byte("stream"))
	})).ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if !w.Flushed || event.Bytes != 6 || event.Status != 200 {
		t.Fatalf("stream: %+v", event)
	}
	func() {
		defer func() {
			if recover() != "handler panic" {
				t.Error("handler panic swallowed")
			}
		}()
		c.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("handler panic") })).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	}()
	if !event.Aborted || event.Status != 0 {
		t.Fatalf("panic: %+v", event)
	}
}

func TestConcurrentDelivery(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	count := 0
	c, err := analytics.New(analytics.Config{Recorder: analytics.RecorderFunc(func(context.Context, analytics.RequestEvent) error { mu.Lock(); defer mu.Unlock(); count++; return nil })})
	if err != nil {
		t.Fatal(err)
	}
	h := c.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() { h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil)) })
	}
	wg.Wait()
	if count != 20 {
		t.Fatalf("got %d", count)
	}
	if _, err := analytics.New(analytics.Config{}); err == nil {
		t.Fatal("nil recorder accepted")
	}
}

func ExampleCollector() {
	c, err := analytics.New(analytics.Config{Recorder: analytics.RecorderFunc(func(_ context.Context, event analytics.RequestEvent) error {
		fmt.Println(event.Method, event.Route, event.Status)
		return nil
	})})
	if err != nil {
		panic(err)
	}
	router := chi.NewRouter()
	router.Use(c.Middleware)
	router.Get("/articles/{slug}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/articles/example?campaign=private", nil))
	// Output: GET /articles/{slug} 204
}
