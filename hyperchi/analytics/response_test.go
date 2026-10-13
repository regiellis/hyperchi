package analytics

import (
	"bufio"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

// ResponseRecorder treats the first 1xx as final. This writer models net/http's
// informational response behavior so the accounting test can exercise both.
type informationalWriter struct {
	*httptest.ResponseRecorder
	hints []int
}

func (w *informationalWriter) WriteHeader(status int) {
	if status >= 100 && status < 200 && status != 101 {
		w.hints = append(w.hints, status)
		return
	}
	w.ResponseRecorder.WriteHeader(status)
}

func TestResponseAccounting(t *testing.T) {
	t.Parallel()
	underlying := &informationalWriter{ResponseRecorder: httptest.NewRecorder()}
	w := &responseWriter{ResponseWriter: underlying}
	w.WriteHeader(103)
	w.WriteHeader(201)
	w.WriteHeader(500)
	n, err := w.Write([]byte("ok"))
	if err != nil || n != 2 || w.status != 201 || w.bytes != 2 || underlying.Code != 201 || len(underlying.hints) != 1 {
		t.Fatalf("accounting: %+v %v", w, err)
	}
	flushed := &responseWriter{ResponseWriter: httptest.NewRecorder()}
	if err := http.NewResponseController(flushed).Flush(); err != nil {
		t.Fatal(err)
	}
	flushed.WriteHeader(500)
	if flushed.status != 200 {
		t.Fatal("flush did not commit 200")
	}
	if err := w.Push("/asset", nil); err != http.ErrNotSupported {
		t.Fatalf("push: %v", err)
	}
}

type hijackableWriter struct {
	*httptest.ResponseRecorder
	conn net.Conn
}

func (w *hijackableWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return w.conn, bufio.NewReadWriter(bufio.NewReader(w.conn), bufio.NewWriter(w.conn)), nil
}
func TestHijackForwarding(t *testing.T) {
	t.Parallel()
	left, right := net.Pipe()
	t.Cleanup(func() {
		if err := left.Close(); err != nil {
			t.Errorf("close left: %v", err)
		}
		if err := right.Close(); err != nil {
			t.Errorf("close right: %v", err)
		}
	})
	w := &responseWriter{ResponseWriter: &hijackableWriter{ResponseRecorder: httptest.NewRecorder(), conn: left}}
	conn, _, err := http.NewResponseController(w).Hijack()
	if err != nil || conn != left || !w.hijacked || w.status != 0 {
		t.Fatalf("hijack: %+v %v", w, err)
	}
	unsupported := &responseWriter{ResponseWriter: httptest.NewRecorder()}
	if _, _, err := unsupported.Hijack(); err == nil || unsupported.hijacked {
		t.Fatal("failed hijack marked successful")
	}
}
