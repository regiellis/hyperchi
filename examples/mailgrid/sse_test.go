package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// sseEvent is one message from a text/event-stream body.
type sseEvent struct{ name, data string }

// readEvents parses an event stream and sends each message on the channel.
// It ends when the stream ends or its request's context does.
func readEvents(body io.Reader, out chan<- sseEvent) {
	defer close(out)
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	var ev sseEvent
	var data []string
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if ev.name != "" || len(data) > 0 {
				ev.data = strings.Join(data, "\n")
				out <- ev
			}
			ev, data = sseEvent{}, nil
		case strings.HasPrefix(line, "event: "):
			ev.name = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			data = append(data, strings.TrimPrefix(line, "data: "))
		case line == "data:":
			data = append(data, "")
		}
	}
}

// openStream connects to a send-out's events with the test's session.
func (a *testApp) openStream(ctx context.Context, sid int64) (*http.Response, <-chan sseEvent) {
	a.t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/sendouts/%d/events", a.srv.URL, sid), nil)
	if err != nil {
		a.t.Fatal(err)
	}
	req.Header.Set("Accept", "text/event-stream")
	res, err := a.client.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	a.t.Cleanup(func() {
		if err := res.Body.Close(); err != nil {
			a.t.Log(err)
		}
	})
	events := make(chan sseEvent, 64)
	go readEvents(res.Body, events)
	return res, events
}

// next waits for the next event that matches, failing at the deadline.
func next(t *testing.T, ctx context.Context, events <-chan sseEvent, match func(sseEvent) bool) sseEvent {
	t.Helper()
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatal("the stream ended")
			}
			if match(ev) {
				return ev
			}
		case <-ctx.Done():
			t.Fatal("no matching event before the deadline")
		}
	}
}

func TestEventStreamCarriesJobProgress(t *testing.T) {
	// A long pace holds the job after its first letter, so the stream opens
	// while it runs; cancelling it produces the final events.
	a := newTestApp(t, func(o *Options) { o.Pace = time.Hour })
	a.signIn()
	listID := a.seedList("Live", 4, nil)
	letterID := a.seedLetter("Hi {{name}}", "Body", formatText, false)

	res, body := a.post("/sendouts", url.Values{
		"letter_id": {fmt.Sprint(letterID)}, "list_id": {fmt.Sprint(listID)}, "confirm": {"outbox"},
	})
	if res.StatusCode != http.StatusOK || !strings.HasPrefix(res.Header.Get("HX-Redirect"), "/sendouts/") {
		t.Fatalf("start: %d %v %s", res.StatusCode, res.Header, body)
	}
	var sid int64
	if _, err := fmt.Sscanf(res.Header.Get("HX-Redirect"), "/sendouts/%d", &sid); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	stream, events := a.openStream(ctx, sid)
	if ct := stream.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type %q", ct)
	}
	first := next(t, ctx, events, func(ev sseEvent) bool { return ev.name == "" })
	if !strings.Contains(first.data, "Rendering") || !strings.Contains(first.data, "Cancel send-out") {
		t.Fatalf("first message is not the running panel: %s", first.data)
	}
	// The job publishes after each letter, then waits out its pace. The
	// first letter shows up either in the snapshot or in a broadcast.
	if !strings.Contains(first.data, "1 of 4") {
		next(t, ctx, events, func(ev sseEvent) bool { return ev.name == "" && strings.Contains(ev.data, "1 of 4") })
	}

	if res, body := a.post(fmt.Sprintf("/sendouts/%d/cancel", sid), nil); res.StatusCode != http.StatusNoContent {
		t.Fatalf("cancel: %d %s", res.StatusCode, body)
	}
	final := next(t, ctx, events, func(ev sseEvent) bool {
		return ev.name == "" && strings.Contains(ev.data, "Cancelled")
	})
	if !strings.Contains(final.data, "1 of 4") || !strings.Contains(final.data, "3 still queued") {
		t.Fatalf("final panel: %s", final.data)
	}
	next(t, ctx, events, func(ev sseEvent) bool { return ev.name == "done" })
	if n := a.count("audit_log", "action IN ('started a send-out', 'cancelled a send-out')"); n != 2 {
		t.Fatalf("%d audit rows", n)
	}
}

func TestEventStreamForAFinishedSendoutSendsStateAndCloses(t *testing.T) {
	a := newTestApp(t, nil)
	a.signIn()
	listID := a.seedList("Done", 2, nil)
	sid := a.seedSendout(a.seedLetter("Hi", "Body", formatText, false), listID)
	if _, err := a.s.jobs.Start(sid); err != nil {
		t.Fatal(err)
	}
	a.waitJob(sid)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, events := a.openStream(ctx, sid)
	panel := next(t, ctx, events, func(ev sseEvent) bool { return ev.name == "" })
	if !strings.Contains(panel.data, "Finished") || !strings.Contains(panel.data, "2 of 2") {
		t.Fatalf("panel: %s", panel.data)
	}
	next(t, ctx, events, func(ev sseEvent) bool { return ev.name == "done" })
	for {
		select {
		case _, ok := <-events:
			if !ok {
				return // the server ended the stream
			}
		case <-ctx.Done():
			t.Fatal("the stream stayed open for a finished send-out")
		}
	}
}

func TestEventStreamNeedsASignedInUser(t *testing.T) {
	a := newTestApp(t, nil)
	a.signIn()
	listID := a.seedList("Private", 1, nil)
	sid := a.seedSendout(a.seedLetter("Hi", "Body", formatText, false), listID)
	a.client.Jar = nil
	res, _ := a.get(fmt.Sprintf("/sendouts/%d/events", sid), false)
	if res.StatusCode != http.StatusSeeOther || !strings.HasPrefix(res.Header.Get("Location"), "/login") {
		t.Fatalf("status %d location %q", res.StatusCode, res.Header.Get("Location"))
	}
}
