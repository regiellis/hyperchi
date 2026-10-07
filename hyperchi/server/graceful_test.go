package server

import (
	"net"
	"net/http"
	"testing"
	"time"
)

// A programmatic Shutdown() unblocks a running Serve or ListenAndServe, which
// would otherwise wait for an OS signal that never comes.
func TestManualShutdownUnblocksServe(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	gs := NewGracefulServer("", http.NewServeMux())
	var before, after int
	gs.OnBeforeShutdown(func() { before++ })
	gs.OnAfterShutdown(func() { after++ })

	errCh := make(chan error, 1)
	go func() { errCh <- gs.Serve(ln) }()

	// Let the server come up before shutting it down.
	time.Sleep(50 * time.Millisecond)

	if err := gs.Shutdown(); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Serve returned error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Serve did not return after Shutdown() — blocked forever (F10 regression)")
	}

	if before != 1 || after != 1 {
		t.Fatalf("shutdown hooks ran before=%d after=%d, want 1/1", before, after)
	}
}

// Shutdown is idempotent: a manual Shutdown() racing or repeated alongside
// signal-driven shutdown runs the hooks only once.
func TestShutdownIsIdempotent(t *testing.T) {
	gs := NewGracefulServer(":0", http.NewServeMux())
	var n int
	gs.OnBeforeShutdown(func() { n++ })

	if err := gs.Shutdown(); err != nil {
		t.Fatalf("first Shutdown: %v", err)
	}
	if err := gs.Shutdown(); err != nil {
		t.Fatalf("second Shutdown: %v", err)
	}

	if n != 1 {
		t.Fatalf("before-shutdown hook ran %d times, want 1", n)
	}
}
