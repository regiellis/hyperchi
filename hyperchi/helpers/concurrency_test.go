package helpers

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// Logger setters run safely while other goroutines log. The race
// detector fails this test when the setters write unsynchronized fields.
func TestLoggerSettersAreSynchronized(t *testing.T) {
	var buf bytes.Buffer
	l := NewLogger().SetOutput(&buf).SetColored(false)
	child := l.WithField("req", "1")

	var wg sync.WaitGroup
	for i := range 4 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for range 50 {
				l.Info("parent %d", i)
				child.Warn("child %d", i)
				child.WithFields(map[string]string{"k": "v"}).Error("derived")
			}
		}()
		go func() {
			defer wg.Done()
			for j := range 50 {
				l.SetLevel(LogLevel(j % 3)).SetPrefix(fmt.Sprint(j)).SetShowCaller(j%2 == 0).SetColored(false)
				child.SetPrefix("child")
			}
		}()
	}
	wg.Wait()
	if !strings.Contains(buf.String(), "child") {
		t.Error("no log lines were written")
	}
}

// The package-level setters synchronize with the package-level log functions.
func TestDefaultLoggerReconfiguresSafely(t *testing.T) {
	var buf safeBuffer
	prev := defaultLogger
	t.Cleanup(func() { defaultLogger = prev })
	defaultLogger = NewLogger()
	SetupTestLogging()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for range 50 {
			Info("hello")
			LogError(errors.New("boom"), "ctx")
		}
	}()
	go func() {
		defer wg.Done()
		for i := range 50 {
			ConfigureLogger(LoggerConfig{Level: INFO, Output: &buf, Prefix: "p"})
			SetLogLevel(LogLevel(i % 2))
			SetLogColored(false)
			SetLogShowCaller(i%2 == 0)
			SetLogOutput(io.Discard)
		}
	}()
	wg.Wait()
}

// safeBuffer is a bytes.Buffer safe for concurrent writes.
type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

// A log argument whose String method logs through the same logger does not
// deadlock: formatting happens outside the lock.
func TestLoggerReentrantStringer(t *testing.T) {
	var buf bytes.Buffer
	l := NewLogger().SetOutput(&buf).SetColored(false)
	done := make(chan struct{})
	go func() {
		l.Info("outer %v", reentrant{l})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("logging a value that logs deadlocked")
	}
	if !strings.Contains(buf.String(), "inner") || !strings.Contains(buf.String(), "outer value") {
		t.Errorf("output:\n%s", buf.String())
	}
}

type reentrant struct{ l *Logger }

func (r reentrant) String() string {
	r.l.Info("inner")
	return "value"
}

// The default error handler's setters are synchronized with Must.
func TestDefaultErrorHandlerSettersAreSynchronized(t *testing.T) {
	prev := defaultErrorHandler
	t.Cleanup(func() { defaultErrorHandler = prev })
	defaultErrorHandler = NewErrorHandler().WithLogger(func(string, ...any) {})
	var logged sync.Map
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := range 50 {
			func() {
				defer func() {
					if recover() == nil {
						t.Error("Must did not panic")
					}
				}()
				Must(fmt.Errorf("e%d", i))
			}()
		}
	}()
	go func() {
		defer wg.Done()
		for i := range 50 {
			SetDefaultSilent(i%2 == 0)
			SetDefaultLogger(func(format string, args ...any) { logged.Store(fmt.Sprintf(format, args...), true) })
		}
	}()
	wg.Wait()

	h := NewErrorHandler().WithLogger(nil)
	func() {
		defer func() {
			if recover() == nil {
				t.Error("Must with a nil logger still panics")
			}
		}()
		h.Must(errors.New("x"))
	}()
}

// Overlapping runs of one timer name are measured independently: a second
// StartTimer does not reset the first run's start time.
func TestProfilerConcurrentTimersAreIndependent(t *testing.T) {
	p := NewProfiler()
	first := p.StartTimer("query")
	time.Sleep(30 * time.Millisecond)
	second := p.StartTimer("query")
	quick := second.Stop()
	slow := first.Stop()

	if slow < 30*time.Millisecond {
		t.Errorf("first run took %v; it was reset by the second StartTimer", slow)
	}
	if quick >= slow {
		t.Errorf("second run %v is not shorter than the first %v", quick, slow)
	}
	stats := p.GetStats().CustomTimers["query"]
	if stats == nil || stats.Count != 2 || stats.TotalTime != quick+slow {
		t.Fatalf("stats = %+v, want 2 runs totaling %v", stats, quick+slow)
	}

	// Stop is idempotent: a second call returns the same duration and does
	// not count the run twice.
	if again := first.Stop(); again != slow {
		t.Errorf("second Stop = %v, want %v", again, slow)
	}
	if got := p.GetStats().CustomTimers["query"].Count; got != 2 {
		t.Errorf("Count after a repeated Stop = %d", got)
	}
}

// StopTimer stops the newest run still running for the name, and only once.
func TestProfilerStopTimerIsLastInFirstOut(t *testing.T) {
	p := NewProfiler()
	outer := p.StartTimer("render")
	time.Sleep(20 * time.Millisecond)
	inner := p.StartTimer("render")
	if d := p.StopTimer("render"); d >= 20*time.Millisecond {
		t.Errorf("StopTimer stopped the outer run (%v)", d)
	}
	if inner.Stop() == 0 {
		t.Error("the inner run was stopped by StopTimer and keeps its duration")
	}
	if d := p.StopTimer("render"); d < 20*time.Millisecond {
		t.Errorf("second StopTimer = %v, want the outer run", d)
	}
	if outer.Duration < 20*time.Millisecond {
		t.Error("the outer Timer records its duration")
	}
	if p.StopTimer("render") != 0 {
		t.Error("StopTimer with nothing running")
	}
	if got := p.GetStats().CustomTimers["render"].Count; got != 2 {
		t.Errorf("Count = %d, want 2", got)
	}
}

// Concurrent TimeOperation calls each record their own run.
func TestProfilerConcurrentTimeOperation(t *testing.T) {
	p := NewProfiler()
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if d := p.TimeOperation("op", func() { time.Sleep(5 * time.Millisecond) }); d < 5*time.Millisecond {
				t.Errorf("TimeOperation = %v", d)
			}
		}()
	}
	wg.Wait()
	if got := p.GetStats().CustomTimers["op"]; got.Count != 8 || got.AverageTime < 5*time.Millisecond {
		t.Errorf("stats = %+v", got)
	}
}

// A panicking operation is still recorded and the panic still propagates.
func TestProfilerTimeOperationRecordsPanics(t *testing.T) {
	p := NewProfiler()
	func() {
		defer func() {
			if recover() == nil {
				t.Error("the panic was swallowed")
			}
		}()
		p.TimeOperation("boom", func() { panic("x") })
	}()
	if p.GetStats().CustomTimers["boom"].Count != 1 {
		t.Error("the panicking run was not recorded")
	}
	if p.StopTimer("boom") != 0 {
		t.Error("the panicking run was left running")
	}
}

// StatsHandler serves GetStats as JSON.
func TestProfilerStatsHandler(t *testing.T) {
	p := NewProfiler()
	p.recordRequest(RequestProfile{Method: "GET", Path: "/x", Duration: time.Millisecond})
	p.TimeOperation("job", func() {})

	rec := httptest.NewRecorder()
	p.StatsHandler().ServeHTTP(rec, httptest.NewRequest("GET", "/debug/profile", nil))
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("status %d, content type %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	var stats ProfileStats
	if err := json.Unmarshal(rec.Body.Bytes(), &stats); err != nil {
		t.Fatal(err)
	}
	if stats.TotalRequests != 1 || stats.CustomTimers["job"] == nil || stats.CustomTimers["job"].Count != 1 {
		t.Errorf("decoded stats = %+v", stats)
	}

	// Write failures go to the log, not a panic.
	p.StatsHandler().ServeHTTP(failingWriter{httptest.NewRecorder()}, httptest.NewRequest("GET", "/", nil))
}

// failingWriter is a ResponseWriter whose body writes fail.
type failingWriter struct{ *httptest.ResponseRecorder }

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("client gone") }
