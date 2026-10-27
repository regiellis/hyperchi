package hyperchi

import (
	"sync"
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/regiellis/hyperchi/hyperchi/config" // the DST case needs America/New_York on any host
)

// fakeClock is a scheduler clock moved by hand. Each NewTimer is announced on
// created and each Stop on stopped, so a test can wait for the scheduler
// goroutine to reach a known point instead of sleeping.
type fakeClock struct {
	mu      sync.Mutex
	now     time.Time
	timers  []*fakeTimer
	created chan *fakeTimer
	stopped chan *fakeTimer
}

type fakeTimer struct {
	clock    *fakeClock
	deadline time.Time
	c        chan time.Time
	done     bool
}

func newFakeClock(now time.Time) *fakeClock {
	return &fakeClock{now: now, created: make(chan *fakeTimer, 16), stopped: make(chan *fakeTimer, 16)}
}

func (f *fakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *fakeClock) NewTimer(d time.Duration) schedulerTimer {
	f.mu.Lock()
	t := &fakeTimer{clock: f, deadline: f.now.Add(d), c: make(chan time.Time, 1)}
	if d <= 0 {
		t.c <- f.now
		t.done = true
	} else {
		f.timers = append(f.timers, t)
	}
	f.mu.Unlock()
	f.created <- t
	return t
}

// Advance moves the clock forward and fires every timer now due.
func (f *fakeClock) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
	for _, t := range f.timers {
		if !t.done && !t.deadline.After(f.now) {
			t.c <- f.now
			t.done = true
		}
	}
}

func (t *fakeTimer) C() <-chan time.Time { return t.c }

func (t *fakeTimer) Stop() bool {
	t.clock.mu.Lock()
	active := !t.done
	t.done = true
	t.clock.mu.Unlock()
	t.clock.stopped <- t
	return active
}

func waitTimer(t *testing.T, ch <-chan *fakeTimer) *fakeTimer {
	t.Helper()
	select {
	case timer := <-ch:
		return timer
	case <-time.After(5 * time.Second):
		t.Fatal("scheduler did not reach the expected timer step")
		return nil
	}
}

func waitRun(t *testing.T, ran <-chan time.Time) time.Time {
	t.Helper()
	select {
	case at := <-ran:
		return at
	case <-time.After(5 * time.Second):
		t.Fatal("scheduled task did not run")
		return time.Time{}
	}
}

func schedulerApp(t *testing.T, clock *fakeClock) *HyperChi {
	t.Helper()
	cfg := config.Default()
	cfg.Database.Disabled = true
	app, err := NewWithConfigE(cfg)
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	app.clock = clock
	return app
}

// Every, ScheduleJob, Schedule, and Daily return an error for a bad spec, so a
// typo such as Every("5x", ...) fails at setup instead of never running.
func TestSchedulerRejectsInvalidSpecs(t *testing.T) {
	app := schedulerApp(t, newFakeClock(time.Now()))
	noop := func() {}
	cases := map[string]func() (func(), error){
		"Every 5x":          func() (func(), error) { return app.Every("5x", noop) },
		"Every too short":   func() (func(), error) { return app.Every("10ms", noop) },
		"ScheduleJob bogus": func() (func(), error) { return app.ScheduleJob("cleanup", "soon", noop) },
		"Schedule empty":    func() (func(), error) { return app.Schedule("", noop) },
		"Daily 25:00":       func() (func(), error) { return app.Daily("25:00", noop) },
		"Daily 3am":         func() (func(), error) { return app.Daily("3am", noop) },
	}
	for name, schedule := range cases {
		stop, err := schedule()
		if err == nil {
			t.Errorf("%s: expected an error, got none", name)
		}
		if stop != nil {
			t.Errorf("%s: expected no stop function with an error", name)
		}
	}
}

func TestEveryRunsOnIntervalAndStops(t *testing.T) {
	start := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	clock := newFakeClock(start)
	app := schedulerApp(t, clock)

	ran := make(chan time.Time, 4)
	stop, err := app.Every("5m", func() { ran <- clock.Now() })
	if err != nil {
		t.Fatalf("Every: %v", err)
	}

	first := waitTimer(t, clock.created)
	if want := start.Add(5 * time.Minute); !first.deadline.Equal(want) {
		t.Fatalf("first run due %v, want %v", first.deadline, want)
	}
	clock.Advance(5 * time.Minute)
	if got := waitRun(t, ran); !got.Equal(start.Add(5 * time.Minute)) {
		t.Fatalf("first run at %v", got)
	}

	second := waitTimer(t, clock.created)
	if want := start.Add(10 * time.Minute); !second.deadline.Equal(want) {
		t.Fatalf("second run due %v, want %v", second.deadline, want)
	}

	stop()
	stop() // idempotent
	if got := waitTimer(t, clock.stopped); got != second {
		t.Fatal("stop did not cancel the pending timer")
	}
}

// Daily runs at a wall-clock time of day, not every 24 hours from
// registration.
func TestDailyRunsAtTimeOfDay(t *testing.T) {
	start := time.Date(2026, 10, 5, 1, 0, 0, 0, time.UTC)
	clock := newFakeClock(start)
	app := schedulerApp(t, clock)

	ran := make(chan time.Time, 4)
	stop, err := app.Daily("03:00", func() { ran <- clock.Now() })
	if err != nil {
		t.Fatalf("Daily: %v", err)
	}
	defer stop()

	first := waitTimer(t, clock.created)
	wantFirst := time.Date(2026, 10, 5, 3, 0, 0, 0, time.UTC)
	if !first.deadline.Equal(wantFirst) {
		t.Fatalf("first run due %v, want %v (24h from registration would be %v)",
			first.deadline, wantFirst, start.Add(24*time.Hour))
	}
	clock.Advance(2 * time.Hour)
	if got := waitRun(t, ran); !got.Equal(wantFirst) {
		t.Fatalf("first run at %v, want %v", got, wantFirst)
	}

	second := waitTimer(t, clock.created)
	if want := wantFirst.AddDate(0, 0, 1); !second.deadline.Equal(want) {
		t.Fatalf("second run due %v, want %v", second.deadline, want)
	}
}

func TestDailyAfterTodaysTimeWaitsForTomorrow(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 10, 5, 3, 0, 0, 0, time.UTC))
	app := schedulerApp(t, clock)
	stop, err := app.Daily("03:00", func() {})
	if err != nil {
		t.Fatalf("Daily: %v", err)
	}
	defer stop()
	got := waitTimer(t, clock.created)
	if want := time.Date(2026, 10, 6, 3, 0, 0, 0, time.UTC); !got.deadline.Equal(want) {
		t.Fatalf("run due %v, want %v", got.deadline, want)
	}
}

func TestNextDailyRunKeepsWallClockAcrossDST(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	// US daylight saving ends on 2026-11-01 at 02:00, so that day is 25 hours.
	prev := time.Date(2026, 10, 31, 3, 0, 0, 0, ny)
	next := nextDailyRun(prev, 3, 0)
	if want := time.Date(2026, 11, 1, 3, 0, 0, 0, ny); !next.Equal(want) {
		t.Fatalf("next run %v, want %v", next, want)
	}
	if gap := next.Sub(prev); gap != 25*time.Hour {
		t.Fatalf("gap across DST end is %v, want 25h", gap)
	}
}

func TestScheduledTaskPanicDoesNotEndSchedule(t *testing.T) {
	start := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	clock := newFakeClock(start)
	app := schedulerApp(t, clock)

	errs := make(chan any, 1)
	app.Events.On("scheduler:task:error", func(data any) { errs <- data })
	calls := 0
	ran := make(chan time.Time, 4)
	stop, err := app.Every("1m", func() {
		calls++
		if calls == 1 {
			panic("boom")
		}
		ran <- clock.Now()
	})
	if err != nil {
		t.Fatalf("Every: %v", err)
	}
	defer stop()

	waitTimer(t, clock.created)
	clock.Advance(time.Minute)
	select {
	case <-errs:
	case <-time.After(5 * time.Second):
		t.Fatal("panic was not reported as scheduler:task:error")
	}
	waitTimer(t, clock.created)
	clock.Advance(time.Minute)
	waitRun(t, ran)
}
