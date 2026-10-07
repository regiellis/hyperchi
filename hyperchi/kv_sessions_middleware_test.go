package hyperchi

import (
	"bufio"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestSessionStore(t *testing.T) *KVSessionStore {
	t.Helper()
	registry := NewKVRegistry()
	if err := registry.Register("memory", NewMemoryStore()); err != nil {
		t.Fatalf("register memory store: %v", err)
	}
	store, err := NewKVSessionStore(registry, KVSessionConfig{Backend: "memory"})
	if err != nil {
		t.Fatalf("create session store: %v", err)
	}
	return store
}

// SessionMiddleware wraps the ResponseWriter; the wrapper must keep exposing
// http.Flusher (SSE) and http.Hijacker (WebSocket upgrades), or every
// realtime feature silently breaks behind the session middleware.
func TestSessionResponseWriterPassesThroughFlusher(t *testing.T) {
	store := newTestSessionStore(t)

	var sawFlusher bool
	handler := store.SessionMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if f, ok := w.(http.Flusher); ok {
			sawFlusher = true
			f.Flush()
		}
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if !sawFlusher {
		t.Fatal("ResponseWriter behind SessionMiddleware does not expose http.Flusher")
	}
	if !rec.Flushed {
		t.Fatal("Flush() did not reach the underlying ResponseWriter")
	}
}

type hijackableRecorder struct {
	*httptest.ResponseRecorder
	hijacked bool
}

func (h *hijackableRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h.hijacked = true
	server, client := net.Pipe()
	if err := client.Close(); err != nil {
		return nil, nil, err
	}
	return server, bufio.NewReadWriter(bufio.NewReader(server), bufio.NewWriter(server)), nil
}

func TestSessionResponseWriterPassesThroughHijacker(t *testing.T) {
	store := newTestSessionStore(t)

	var hijackErr error
	handler := store.SessionMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Error("ResponseWriter behind SessionMiddleware does not expose http.Hijacker")
			return
		}
		var conn net.Conn
		conn, _, hijackErr = hj.Hijack()
		if conn != nil {
			if err := conn.Close(); err != nil {
				t.Errorf("close hijacked conn: %v", err)
			}
		}
	}))

	rec := &hijackableRecorder{ResponseRecorder: httptest.NewRecorder()}
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if hijackErr != nil {
		t.Fatalf("Hijack() failed: %v", hijackErr)
	}
	if !rec.hijacked {
		t.Fatal("Hijack() did not reach the underlying ResponseWriter")
	}
}

func TestSessionResponseWriterUnwrap(t *testing.T) {
	rec := httptest.NewRecorder()
	sw := &sessionResponseWriter{ResponseWriter: rec}
	if sw.Unwrap() != rec {
		t.Fatal("Unwrap() must return the underlying ResponseWriter")
	}
}

func TestSessionResponseWriterHijackWithoutSupport(t *testing.T) {
	store := newTestSessionStore(t)
	sw := &sessionResponseWriter{ResponseWriter: httptest.NewRecorder(), store: store, session: &KVSession{}}
	if _, _, err := sw.Hijack(); err == nil {
		t.Fatal("Hijack() must fail when the underlying writer does not support it")
	}
}

// A missing session store is a setup mistake, so SessionMiddleware panics at
// registration. Passing requests through would leave c.Session() nil and make
// session-backed login and logout silently do nothing.
func TestSessionMiddlewareWithoutStorePanics(t *testing.T) {
	app := newTemplateApp(t, false)
	defer func() {
		rec := recover()
		msg, ok := rec.(string)
		if !ok || !strings.Contains(msg, "UseSessions") {
			t.Fatalf("SessionMiddleware without a store: recovered %v, want a panic naming UseSessions", rec)
		}
	}()
	app.SessionMiddleware()
}

func TestSessionMiddlewareWithStoreServesSession(t *testing.T) {
	app := newTemplateApp(t, false)
	if err := app.UseKVStoreMemory("memory"); err != nil {
		t.Fatal(err)
	}
	if err := app.UseSessions(KVSessionConfig{Backend: "memory"}); err != nil {
		t.Fatal(err)
	}
	var sawSession bool
	h := app.SessionMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawSession = app.GetSession(r) != nil
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	if !sawSession {
		t.Fatal("no session in the request context behind SessionMiddleware")
	}
}
