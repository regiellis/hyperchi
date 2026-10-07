package main

import (
	"context"
	"sync"
	"testing"
	"time"
)

// recorder captures every progress publish a job makes.
type recorder struct {
	mu     sync.Mutex
	a      *testApp
	events []Progress
	finals int
	// onEvent runs after each publish, outside the lock.
	onEvent func(Progress)
}

func (r *recorder) publish(ctx context.Context, id int64, final bool) error {
	p, err := loadProgress(ctx, r.a.s.db, id)
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.events = append(r.events, p)
	if final {
		r.finals++
	}
	hook := r.onEvent
	r.mu.Unlock()
	if hook != nil {
		hook(p)
	}
	return nil
}

func (r *recorder) snapshot() ([]Progress, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Progress(nil), r.events...), r.finals
}

// withRecorder swaps the app's job runner for one that records progress.
func withRecorder(a *testApp) *recorder {
	r := &recorder{a: a}
	jobs := newJobs(a.s.opts.Shutdown, a.s.db, 0, 0, r.publish)
	jobs.finished = make(chan int64, 16)
	a.s.jobs = jobs
	return r
}

func TestJobRendersEveryRecipientIntoTheOutbox(t *testing.T) {
	a := newTestApp(t, nil)
	r := withRecorder(a)
	listID := a.seedList("All", 25, nil)
	letterID := a.seedLetter("Hi {{name}}", "Dear {{name}} of {{company}}", formatText, false)
	sid := a.seedSendout(letterID, listID)

	if started, err := a.s.jobs.Start(sid); err != nil || !started {
		t.Fatalf("start: %v %v", started, err)
	}
	a.waitJob(sid)

	p := a.progress(sid)
	if p.Sendout.Status != statusDone || p.Rendered != 25 || p.Total != 25 || p.Queued != 0 || p.Failed != 0 {
		t.Fatalf("progress %+v", p)
	}
	item, err := one[OutboxItem](a.s.db, `SELECT * FROM outbox WHERE sendout_id = ? AND email = 'person007@example.com'`, sid)
	if err != nil {
		t.Fatal(err)
	}
	if item.Subject != "Hi Person 7" || item.Body != "Dear Person 7 of Firm 7" || item.RenderedAt == nil {
		t.Fatalf("item %+v", item)
	}

	events, finals := r.snapshot()
	if finals != 1 || len(events) < 3 {
		t.Fatalf("%d events, %d final", len(events), finals)
	}
	if first := events[0]; first.Rendered != 0 || first.Queued != 25 || first.Sendout.Status != statusRunning {
		t.Fatalf("first event %+v", first)
	}
	last := events[len(events)-1]
	if last.Sendout.Status != statusDone || last.Rendered != 25 {
		t.Fatalf("last event %+v", last)
	}
	for i := 1; i < len(events); i++ {
		if events[i].Done() < events[i-1].Done() {
			t.Fatalf("progress went backwards at event %d", i)
		}
	}
}

func TestJobRerunIsIdempotent(t *testing.T) {
	a := newTestApp(t, nil)
	withRecorder(a)
	listID := a.seedList("Twice", 10, nil)
	letterID := a.seedLetter("Hi {{name}}", "Body", formatText, false)
	sid := a.seedSendout(letterID, listID)
	run := func() {
		t.Helper()
		if _, err := a.s.jobs.Start(sid); err != nil {
			t.Fatal(err)
		}
		a.waitJob(sid)
	}
	run()
	before, err := one[OutboxItem](a.s.db, `SELECT * FROM outbox WHERE sendout_id = ? ORDER BY id LIMIT 1`, sid)
	if err != nil {
		t.Fatal(err)
	}

	run()
	if n := a.count("outbox", "sendout_id = ?", sid); n != 10 {
		t.Fatalf("rerun made %d outbox rows, want 10", n)
	}
	after, err := one[OutboxItem](a.s.db, `SELECT * FROM outbox WHERE id = ?`, before.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !after.RenderedAt.Equal(*before.RenderedAt) {
		t.Fatal("rerun rendered an existing letter again")
	}

	if _, err := a.s.db.Create("recipients", map[string]any{"list_id": listID, "email": "late@example.com", "name": "Late"}); err != nil {
		t.Fatal(err)
	}
	run()
	p := a.progress(sid)
	if p.Total != 11 || p.Rendered != 11 || a.count("outbox", "email = 'late@example.com'") != 1 {
		t.Fatalf("after adding a recipient: %+v", p)
	}
}

func TestJobCancelsMidRunAndResumes(t *testing.T) {
	a := newTestApp(t, nil)
	r := withRecorder(a)
	listID := a.seedList("Cancel", 20, nil)
	letterID := a.seedLetter("Hi {{name}}", "Body", formatText, false)
	sid := a.seedSendout(letterID, listID)
	// Cancel from inside the job's own progress callback once five letters
	// are rendered: deterministic, with no timing involved.
	r.onEvent = func(p Progress) {
		if p.Rendered == 5 && p.Running() {
			a.s.jobs.Cancel(sid)
		}
	}
	if _, err := a.s.jobs.Start(sid); err != nil {
		t.Fatal(err)
	}
	a.waitJob(sid)
	p := a.progress(sid)
	if p.Sendout.Status != statusCancelled || p.Rendered != 5 || p.Queued != 15 {
		t.Fatalf("after cancel: %+v", p)
	}
	if a.s.jobs.Running(sid) || a.s.jobs.Cancel(sid) {
		t.Fatal("a cancelled job is still registered")
	}

	r.mu.Lock()
	r.onEvent = nil
	r.mu.Unlock()
	if _, err := a.s.jobs.Start(sid); err != nil {
		t.Fatal(err)
	}
	a.waitJob(sid)
	p = a.progress(sid)
	if p.Sendout.Status != statusDone || p.Rendered != 20 || a.count("outbox", "sendout_id = ?", sid) != 20 {
		t.Fatalf("after resume: %+v", p)
	}
}

func TestStrictLetterFailsRecipientsMissingAField(t *testing.T) {
	a := newTestApp(t, nil)
	r := withRecorder(a)
	listID := a.seedList("Gaps", 6, func(i int) map[string]string {
		if i%2 == 0 {
			return map[string]string{}
		}
		return map[string]string{"company": "Firm"}
	})
	letterID := a.seedLetter("Hi {{name}}", "From {{company}}", formatText, true)
	sid := a.seedSendout(letterID, listID)
	if _, err := a.s.jobs.Start(sid); err != nil {
		t.Fatal(err)
	}
	a.waitJob(sid)
	p := a.progress(sid)
	if p.Rendered != 3 || p.Failed != 3 || p.Sendout.Status != statusDone {
		t.Fatalf("progress %+v", p)
	}
	if len(p.Failures) != 3 || p.Failures[0].Error != "No value for {{company}}." {
		t.Fatalf("failures %+v", p.Failures)
	}
	events, _ := r.snapshot()
	if last := events[len(events)-1]; last.Failed != 3 {
		t.Fatalf("the final event does not carry the failures: %+v", last)
	}
}

func TestJobsStopOnShutdownAndAreMarkedOnStart(t *testing.T) {
	a := newTestApp(t, nil)
	r := withRecorder(a)
	a.s.jobs.pace = time.Hour // the job waits after each letter
	listID := a.seedList("Slow", 3, nil)
	sid := a.seedSendout(a.seedLetter("Hi", "Body", formatText, false), listID)
	// The server begins shutting down once the first letter is rendered.
	r.onEvent = func(p Progress) {
		if p.Rendered == 1 && p.Running() {
			a.stop()
		}
	}
	if _, err := a.s.jobs.Start(sid); err != nil {
		t.Fatal(err)
	}
	a.waitJob(sid)
	if p := a.progress(sid); p.Sendout.Status != statusInterrupted || p.Rendered != 1 || p.Queued != 2 {
		t.Fatalf("progress %+v", p)
	}
	if _, err := a.s.jobs.Start(sid); err == nil {
		t.Fatal("a job started during shutdown")
	}

	// A process that died mid-job leaves "running" behind; the next start
	// marks it interrupted.
	if err := a.s.db.Update("sendouts", sid, map[string]any{"status": statusRunning}); err != nil {
		t.Fatal(err)
	}
	if err := a.s.jobs.markInterrupted(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p := a.progress(sid); p.Sendout.Status != statusInterrupted {
		t.Fatalf("status %s", p.Sendout.Status)
	}
}
