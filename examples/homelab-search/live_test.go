package main

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestWatcherReindexesChanges drives the watcher by hand: one step per change.
func TestWatcherReindexesChanges(t *testing.T) {
	s := testSite(t, map[string]string{"notes/first.md": "# First\n\nalpha\n"})
	ctx := context.Background()
	lib := s.ix.dir

	// Add.
	writeFiles(t, lib, map[string]string{"notes/added.txt": "bravo charlie"})
	s.watcher.step(ctx)
	if got := hitPaths(search(t, s, SearchParams{Query: "bravo "})); !slices.Equal(got, []string{"notes/added.txt"}) {
		t.Fatalf("after add: %v", got)
	}
	if st := s.ix.Status(); st.Files != 2 || st.LastPass != "file change" || st.Changed != 1 {
		t.Errorf("status after add: %+v", st)
	}

	// Change: new contents and a new size, so the poll sees it.
	writeFiles(t, lib, map[string]string{"notes/added.txt": "delta echo foxtrot"})
	s.watcher.step(ctx)
	if got := hitPaths(search(t, s, SearchParams{Query: "bravo "})); len(got) != 0 {
		t.Errorf("old contents still found: %v", got)
	}
	if got := hitPaths(search(t, s, SearchParams{Query: "foxtrot "})); !slices.Equal(got, []string{"notes/added.txt"}) {
		t.Errorf("after change: %v", got)
	}

	// Delete, including a whole directory.
	writeFiles(t, lib, map[string]string{"old/a.md": "# A\n", "old/b.md": "# B\n"})
	s.watcher.step(ctx)
	if err := os.Remove(filepath.Join(lib, "notes", "added.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(lib, "old")); err != nil {
		t.Fatal(err)
	}
	s.watcher.step(ctx)
	if got := hitPaths(search(t, s, SearchParams{})); !slices.Equal(got, []string{"notes/first.md"}) {
		t.Errorf("after delete: %v", got)
	}
	if st := s.ix.Status(); st.Files != 1 || st.Changed != 3 {
		t.Errorf("status after delete: %+v", st)
	}

	// The framework's watcher skips hidden entries, so they never enter the index.
	writeFiles(t, lib, map[string]string{".git/HEAD": "ref: refs/heads/main"})
	s.watcher.step(ctx)
	if st := s.ix.Status(); st.Files != 1 {
		t.Errorf("hidden file indexed: %+v", st)
	}
}

// TestWatcherRunUsesInterval runs the real Watch loop with a short injected
// interval and waits for the status callback, not for time to pass.
func TestWatcherRunUsesInterval(t *testing.T) {
	s := testSite(t, map[string]string{"a.md": "# A\n"})
	changes := make(chan Status, 8)
	s.ix.onStatus = func(st Status) {
		if !st.Scanning && st.LastPass == "file change" {
			changes <- st
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() {
		if err := s.watcher.run(ctx, 5*time.Millisecond); err != nil {
			t.Error(err)
		}
	})
	defer func() {
		cancel()
		wg.Wait()
	}()

	writeFiles(t, s.ix.dir, map[string]string{"b.md": "# B\n\nwatched\n"})
	select {
	case st := <-changes:
		if st.Files != 2 {
			t.Errorf("status %+v", st)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the watcher did not report the new file")
	}
	if got := hitPaths(search(t, s, SearchParams{Query: "watched "})); !slices.Equal(got, []string{"b.md"}) {
		t.Errorf("search after watch: %v", got)
	}
}

// TestStatusStreamsOverSSE opens the real /events stream and checks that an
// index change pushes a rendered status panel to it.
func TestStatusStreamsOverSSE(t *testing.T) {
	s := testSite(t, map[string]string{"a.md": "# A\n"})
	srv := httptest.NewServer(s.app)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/events?channels="+statusChannel, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		cancel()
		if err := res.Body.Close(); err != nil {
			t.Log(err)
		}
	}()
	if ct := res.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type %q", ct)
	}

	events := make(chan sseEvent, 16)
	go readEvents(res, events)
	first, ok := <-events
	if !ok || first.name != "connected" {
		t.Fatalf("first event %+v", first)
	}
	// The hub subscribes the connection before it sends "connected", so a
	// change made now reaches this stream.
	if n := s.hub.GetStats().TotalChannels; n != 1 {
		t.Fatalf("subscribed channels after connected = %d, want 1", n)
	}

	writeFiles(t, s.ix.dir, map[string]string{"b.md": "# B\n"})
	s.watcher.step(context.Background())
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatal("stream closed before the status update")
			}
			if ev.name == "" && strings.Contains(ev.data, "<dt>Files</dt><dd>2</dd>") {
				if !strings.Contains(ev.data, "Rescan now") {
					t.Errorf("status panel is incomplete: %s", ev.data)
				}
				return
			}
		case <-ctx.Done():
			t.Fatal("no status update arrived on the stream")
		}
	}
}

type sseEvent struct{ name, data string }

// readEvents parses a text/event-stream into events until the body ends.
func readEvents(res *http.Response, out chan<- sseEvent) {
	defer close(out)
	sc := bufio.NewScanner(res.Body)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	var ev sseEvent
	var data []string
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			ev.data = strings.Join(data, "\n")
			out <- ev
			ev, data = sseEvent{}, nil
		case strings.HasPrefix(line, "event: "):
			ev.name = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			data = append(data, strings.TrimPrefix(line, "data: "))
		}
	}
}
