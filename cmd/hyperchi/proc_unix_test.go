//go:build unix

package main

import (
	"bufio"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"
)

// A dev server's port is held by a grandchild (Vite's node under npm, the
// compiled binary under `go run`). stop must reach it even when it ignores
// SIGINT and the leader exits cleanly. The shell's background job below
// ignores SIGINT, as non-interactive shells arrange for background jobs.
func TestStopKillsTheWholeProcessGroup(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := r.Close(); err != nil {
			t.Errorf("close pipe: %v", err)
		}
	})
	cmd := exec.Command("sh", "-c", "sleep 300 & echo started; wait")
	cmd.Stdout = w
	g, err := startGroup(cmd)
	if err != nil {
		t.Fatal(err)
	}
	// Both the shell and the sleep hold the write end; EOF on the read end
	// means every member of the group is gone.
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := r.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil || line != "started\n" {
		t.Fatalf("group did not start: %q, %v", line, err)
	}

	g.stop(2 * time.Second)

	select {
	case <-g.done:
	default:
		t.Fatal("stop returned before the leader was reaped")
	}
	if _, err := io.Copy(io.Discard, r); err != nil {
		t.Fatalf("a group member survived stop: %v", err)
	}
}
