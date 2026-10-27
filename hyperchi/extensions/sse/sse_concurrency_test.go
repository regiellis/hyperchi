package sse

import (
	"context"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestConcurrentConnectionWrites(t *testing.T) {
	hub := NewSSEHub()
	recorder := httptest.NewRecorder()
	conn := NewSSEConnection("test", recorder, httptest.NewRequest("GET", "/", nil))
	hub.AddConnection(conn)
	defer hub.RemoveConnection(conn.ID)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				hub.SendToConnection(conn.ID, SSEMessage{Data: "hello"})
			}
		}()
	}
	wg.Wait()
	if got := conn.messagesSent.Load(); got != 400 {
		t.Fatalf("sent %d, want 400", got)
	}
}

type blockedResponse struct {
	*httptest.ResponseRecorder
	ready   chan struct{}
	entered chan struct{}
	release chan struct{}
	writes  int
}

func (w *blockedResponse) Write(data []byte) (int, error) {
	w.writes++
	if w.writes == 2 {
		close(w.entered)
		<-w.release
	}
	return w.ResponseRecorder.Write(data)
}
func (w *blockedResponse) Flush() {
	w.ResponseRecorder.Flush()
	if w.writes == 1 {
		close(w.ready)
	}
}

func TestHandlerWaitsForWriterOnCancellation(t *testing.T) {
	hub := NewSSEHub()
	w := &blockedResponse{ResponseRecorder: httptest.NewRecorder(), ready: make(chan struct{}), entered: make(chan struct{}), release: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request := httptest.NewRequest("GET", "/", nil).WithContext(ctx)
	done := make(chan struct{})
	go func() { hub.HTTPHandler().ServeHTTP(w, request); close(done) }()
	<-w.ready
	hub.mu.RLock()
	var conn *SSEConnection
	for _, c := range hub.connections {
		conn = c
	}
	hub.mu.RUnlock()
	sent := make(chan struct{})
	go func() { hub.SendToConnection(conn.ID, SSEMessage{Data: "blocked"}); close(sent) }()
	<-w.entered
	cancel()
	select {
	case <-done:
		t.Error("handler returned during write")
	case <-time.After(20 * time.Millisecond):
	}
	close(w.release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handler did not finish")
	}
	<-sent
	before := w.Body.Len()
	hub.sendToConnection(conn, SSEMessage{Data: "late"})
	if w.Body.Len() != before {
		t.Fatal("write after handler return")
	}
}
