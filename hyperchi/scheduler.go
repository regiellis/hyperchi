package hyperchi

import (
	"fmt"
	"log"
	"sync"
	"time"
)

// schedulerClock is the time source for scheduled tasks. The app uses the
// real clock; tests substitute one they advance by hand, so schedules are
// checked without sleeping.
type schedulerClock interface {
	Now() time.Time
	NewTimer(d time.Duration) schedulerTimer
}

// schedulerTimer is the part of *time.Timer the scheduler uses.
type schedulerTimer interface {
	C() <-chan time.Time
	Stop() bool
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

func (realClock) NewTimer(d time.Duration) schedulerTimer { return realTimer{time.NewTimer(d)} }

type realTimer struct{ t *time.Timer }

func (r realTimer) C() <-chan time.Time { return r.t.C }
func (r realTimer) Stop() bool          { return r.t.Stop() }

func (g *HyperChi) schedulerClock() schedulerClock {
	if g.clock != nil {
		return g.clock
	}
	return realClock{}
}

// Schedule runs task every interval, measured from the moment it is
// registered, and returns a stop function that ends the schedule (idempotent,
// safe to call more than once). interval is a Go duration of at least one
// second ("30s", "5m", "1h") or one of the keywords "minutely", "hourly",
// "daily", and "weekly". The keywords are intervals too: "daily" means every
// 24 hours from registration, not at a time of day; use Daily for that.
// An unparseable interval returns an error and schedules nothing.
func (g *HyperChi) Schedule(interval string, task func()) (func(), error) {
	duration, err := parseInterval(interval)
	if err != nil {
		return nil, fmt.Errorf("hyperchi: invalid schedule interval %q: %w", interval, err)
	}
	g.Events.Emit("scheduler:task:scheduled", map[string]any{
		"interval": interval,
		"duration": duration.String(),
	})
	stop := g.startSchedule(interval, func(prev time.Time) time.Time { return prev.Add(duration) }, task)
	log.Printf("hyperchi: scheduled task every %s (%v)", interval, duration)
	return stop, nil
}

// Daily runs task once a day at the wall-clock time at, written "15:04" in
// 24-hour form, in the local time zone. The first run is the next occurrence of
// that time, today or tomorrow. Daylight-saving changes keep the wall-clock
// time, so a day can be 23 or 25 hours long. It returns a stop function, or an
// error when at is not a valid time of day.
func (g *HyperChi) Daily(at string, task func()) (func(), error) {
	clock, err := time.Parse("15:04", at)
	if err != nil {
		return nil, fmt.Errorf("hyperchi: invalid daily time %q (use 24-hour \"15:04\"): %w", at, err)
	}
	hour, minute := clock.Hour(), clock.Minute()
	label := "daily at " + at
	g.Events.Emit("scheduler:task:scheduled", map[string]any{"interval": label})
	stop := g.startSchedule(label, func(prev time.Time) time.Time {
		return nextDailyRun(prev, hour, minute)
	}, task)
	log.Printf("hyperchi: scheduled task %s", label)
	return stop, nil
}

// nextDailyRun returns the first time strictly after t whose wall clock in
// t's location reads hour:minute.
func nextDailyRun(t time.Time, hour, minute int) time.Time {
	y, m, d := t.Date()
	next := time.Date(y, m, d, hour, minute, 0, 0, t.Location())
	if !next.After(t) {
		next = time.Date(y, m, d+1, hour, minute, 0, 0, t.Location())
	}
	return next
}

// startSchedule runs task at each time produced by next, starting from now,
// until the returned stop function is called.
func (g *HyperChi) startSchedule(label string, next func(time.Time) time.Time, task func()) func() {
	done := make(chan struct{})
	var once sync.Once
	go g.runSchedule(label, next, task, done)
	return func() { once.Do(func() { close(done) }) }
}

// runSchedule waits for each run time, runs task, and computes the next run
// from the previous one. Runs missed while a slow task was executing are
// skipped rather than queued.
func (g *HyperChi) runSchedule(label string, next func(time.Time) time.Time, task func(), done <-chan struct{}) {
	clock := g.schedulerClock()
	at := next(clock.Now())
	for {
		timer := clock.NewTimer(at.Sub(clock.Now()))
		select {
		case <-done:
			timer.Stop()
			return
		case <-timer.C():
		}
		g.runScheduledTask(label, clock, task)
		now := clock.Now()
		at = next(at)
		for !at.After(now) {
			at = next(at)
		}
	}
}

// runScheduledTask runs one scheduled execution, recovering a panic so one bad
// run does not end the schedule.
func (g *HyperChi) runScheduledTask(label string, clock schedulerClock, task func()) {
	g.Events.Emit("scheduler:task:executing", map[string]any{
		"interval": label,
		"time":     clock.Now().Format(time.RFC3339),
	})
	func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("hyperchi: scheduled task panicked: %v", r)
				g.Events.Emit("scheduler:task:error", map[string]any{
					"interval": label,
					"error":    fmt.Sprintf("%v", r),
				})
			}
		}()
		task()
	}()
	g.Events.Emit("scheduler:task:completed", map[string]any{
		"interval": label,
		"time":     clock.Now().Format(time.RFC3339),
	})
}

// parseInterval parses a Go duration of at least one second, or one of the
// keywords "minutely", "hourly", "daily", "weekly" (also written "every
// minute", "every hour", "every day", "every week").
func parseInterval(interval string) (time.Duration, error) {
	switch interval {
	case "minutely", "every minute":
		return time.Minute, nil
	case "hourly", "every hour":
		return time.Hour, nil
	case "daily", "every day":
		return 24 * time.Hour, nil
	case "weekly", "every week":
		return 7 * 24 * time.Hour, nil
	}

	duration, err := time.ParseDuration(interval)
	if err != nil {
		return 0, fmt.Errorf("invalid interval format: %s (use formats like '5m', '1h', '30s' or keywords like 'hourly', 'daily')", interval)
	}
	if duration < time.Second {
		return 0, fmt.Errorf("interval too short: minimum is 1 second")
	}
	return duration, nil
}

// ScheduleJob is Schedule with a name that is logged on every run.
func (g *HyperChi) ScheduleJob(name, interval string, task func()) (func(), error) {
	stop, err := g.Schedule(interval, func() {
		log.Printf("hyperchi: running scheduled job %q", name)
		task()
	})
	if err != nil {
		return nil, fmt.Errorf("hyperchi: schedule job %q: %w", name, err)
	}
	return stop, nil
}

// Hourly runs task every hour from registration. It is Schedule("hourly", task).
func (g *HyperChi) Hourly(task func()) (func(), error) {
	return g.Schedule("hourly", task)
}

// Every runs task at the given interval. It is Schedule(interval, task).
func (g *HyperChi) Every(interval string, task func()) (func(), error) {
	return g.Schedule(interval, task)
}
