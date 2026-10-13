package performance

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/regiellis/hyperchi/hyperchi"
)

func newTestQueue(t *testing.T, store hyperchi.KVStore, config JobQueueConfig) *JobQueue {
	t.Helper()
	registry := hyperchi.NewKVRegistry()
	if err := registry.Register("test", store); err != nil {
		t.Fatal(err)
	}
	config.Backend = "test"
	queue, err := NewJobQueue(registry, config)
	if err != nil {
		t.Fatal(err)
	}
	return queue
}

func TestQueueClaimsByPriorityAmongDueJobs(t *testing.T) {
	for name, store := range performanceTestStores(t) {
		t.Run(name, func(t *testing.T) {
			queue := newTestQueue(t, store, JobQueueConfig{})
			later := time.Now().Add(time.Hour)
			// IDs sort in the order pushed, so key order alone would pick "a".
			pushes := []struct {
				id       string
				priority int
				at       *time.Time
			}{
				{"a-future", 100, &later},
				{"b-low", 0, nil},
				{"c-high", 5, nil},
				{"d-high", 5, nil},
			}
			for _, p := range pushes {
				if _, err := queue.PushWithOptions("work", nil, JobOptions{ID: p.id, Priority: p.priority, ScheduleAt: p.at}); err != nil {
					t.Fatal(err)
				}
			}
			for _, want := range []string{"c-high", "d-high", "b-low"} {
				job, err := queue.getNextJob()
				if err != nil {
					t.Fatal(err)
				}
				if job.ID != want {
					t.Fatalf("claimed %s, want %s", job.ID, want)
				}
			}
			if job, err := queue.getNextJob(); !errors.Is(err, hyperchi.ErrKeyNotFound) {
				t.Fatalf("claimed a job not yet due: %v, %v", job, err)
			}
		})
	}
}

func TestRetryBackoffDoubles(t *testing.T) {
	store := memoryStore(t)
	queue := newTestQueue(t, store, JobQueueConfig{RetryDelay: time.Minute, MaxRetryDelay: 5 * time.Minute})
	for attempts, want := range map[int]time.Duration{1: time.Minute, 2: 2 * time.Minute, 3: 4 * time.Minute, 4: 5 * time.Minute, 10: 5 * time.Minute} {
		job := &Job{ID: "retry", Type: "work", Attempts: attempts, MaxAttempts: 20}
		before := time.Now()
		if err := queue.handleJobFailure(job, errors.New("boom")); err != nil {
			t.Fatal(err)
		}
		after := time.Now()
		if job.ScheduledAt.Before(before.Add(want)) || job.ScheduledAt.After(after.Add(want)) {
			t.Errorf("after %d attempts: retry in %v, want %v", attempts, job.ScheduledAt.Sub(before), want)
		}
	}
}

func TestQueueStatsCountEveryStatus(t *testing.T) {
	queue := newTestQueue(t, memoryStore(t), JobQueueConfig{})
	queue.RegisterHandler("ok", func(*Job) error { return nil })
	queue.RegisterHandler("bad", func(*Job) error { return errors.New("boom") })
	push := func(id, jobType string, maxAttempts int) {
		t.Helper()
		if _, err := queue.PushWithOptions(jobType, nil, JobOptions{ID: id, MaxAttempts: maxAttempts}); err != nil {
			t.Fatal(err)
		}
	}
	push("1", "ok", 0)
	push("2", "bad", 1)
	push("3", "bad", 3)
	for range 3 {
		if err := queue.processNextJob(0); err != nil {
			t.Fatal(err)
		}
	}
	push("4", "ok", 0)

	stats, err := queue.GetStats()
	if err != nil {
		t.Fatal(err)
	}
	want := JobQueueStats{TotalJobs: 4, PendingJobs: 1, RetryingJobs: 1, CompletedJobs: 1, FailedJobs: 1, Workers: 4}
	if stats.TotalJobs != want.TotalJobs || stats.PendingJobs != want.PendingJobs || stats.RetryingJobs != want.RetryingJobs ||
		stats.ProcessingJobs != 0 || stats.CompletedJobs != want.CompletedJobs || stats.FailedJobs != want.FailedJobs || stats.Workers != want.Workers {
		t.Fatalf("stats = %+v, want %+v", stats, want)
	}
	if len(stats.Handlers) != 2 || stats.Handlers[0] != "bad" || stats.Handlers[1] != "ok" {
		t.Fatalf("handlers = %v", stats.Handlers)
	}
}

func TestQueueStopTwiceAndBeforeStart(t *testing.T) {
	queue := newTestQueue(t, memoryStore(t), JobQueueConfig{Workers: 2})
	queue.Stop() // before Start
	queue.Start()
	queue.Stop()

	running := newTestQueue(t, memoryStore(t), JobQueueConfig{Workers: 2})
	running.Start()
	running.Start()
	running.Stop()
	running.Stop()
}

func TestQueueRequeuesWhenClaimedJobCannotBeSaved(t *testing.T) {
	store := memoryStore(t)
	queue := newTestQueue(t, store, JobQueueConfig{})
	queue.RegisterHandler("work", func(*Job) error { return nil })
	if _, err := queue.PushWithOptions("work", nil, JobOptions{ID: "x"}); err != nil {
		t.Fatal(err)
	}
	// Point the queue at a store that cannot save job records, after the
	// job is queued. Queue entries can still be written.
	broken := failingStore{KVStore: store, failSet: func(key string) bool { return strings.HasPrefix(key, "jobs:job:") }}
	queue.store = broken
	if err := queue.processNextJob(0); !errors.Is(err, errInjected) {
		t.Fatalf("processNextJob error = %v, want the store's", err)
	}
	queue.store = store
	if err := queue.processNextJob(0); err != nil {
		t.Fatalf("job lost after a failed save: %v", err)
	}
}

func TestRetryBackoffDefaultDoublesFromOneMinute(t *testing.T) {
	queue := newTestQueue(t, memoryStore(t), JobQueueConfig{})
	job := &Job{ID: "retry", Type: "work", Attempts: 3, MaxAttempts: 5}
	before := time.Now()
	if err := queue.handleJobFailure(job, errors.New("boom")); err != nil {
		t.Fatal(err)
	}
	if wait := job.ScheduledAt.Sub(before); wait < 4*time.Minute || wait > 4*time.Minute+time.Second {
		t.Fatalf("third retry waits %v, want 4m (1m doubled twice)", wait)
	}
}

func TestGeneratedJobIDsAreUniqueUnderConcurrentPushes(t *testing.T) {
	queue := newTestQueue(t, memoryStore(t), JobQueueConfig{})
	// Every push reads the same instant, as on a clock coarser than the
	// push rate.
	frozen := time.Unix(1_700_000_000, 0)
	queue.now = func() time.Time { return frozen }
	const goroutines, perGoroutine = 16, 200
	ids := make(chan string, goroutines*perGoroutine)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range goroutines {
		wg.Go(func() {
			<-start
			for range perGoroutine {
				job, err := queue.Push("work", nil)
				if err != nil {
					t.Error(err)
					return
				}
				ids <- job.ID
			}
		})
	}
	close(start)
	wg.Wait()
	close(ids)
	seen := make(map[string]bool)
	for id := range ids {
		if seen[id] {
			t.Fatalf("job ID %s generated twice", id)
		}
		seen[id] = true
	}
}

func TestPushRejectsDuplicateJobID(t *testing.T) {
	for name, store := range performanceTestStores(t) {
		t.Run(name, func(t *testing.T) {
			queue := newTestQueue(t, store, JobQueueConfig{})
			if _, err := queue.PushWithOptions("work", map[string]any{"n": 1.0}, JobOptions{ID: "same"}); err != nil {
				t.Fatal(err)
			}
			if _, err := queue.PushWithOptions("work", map[string]any{"n": 2.0}, JobOptions{ID: "same"}); !errors.Is(err, ErrJobExists) {
				t.Fatalf("second push error = %v, want ErrJobExists", err)
			}
			job, err := queue.GetJob("same")
			if err != nil {
				t.Fatal(err)
			}
			if job.Payload["n"] != 1.0 {
				t.Fatalf("stored payload = %v, want the first push's", job.Payload)
			}
		})
	}
}
