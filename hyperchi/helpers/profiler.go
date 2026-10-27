package helpers

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Profiler records request timings through its Middleware and named
// timings through StartTimer and TimeOperation. It keeps the last 1000
// requests in memory. A Profiler is safe for concurrent use and starts
// enabled.
type Profiler struct {
	enabled    atomic.Bool
	requests   []RequestProfile
	mutex      sync.RWMutex
	startTime  time.Time
	timers     map[string]*timerStats
	timerMutex sync.RWMutex
}

// RequestProfile is one request the Middleware recorded.
type RequestProfile struct {
	Method    string        `json:"method"`
	Path      string        `json:"path"`
	Duration  time.Duration `json:"duration"`
	Status    int           `json:"status"`
	Timestamp time.Time     `json:"timestamp"`
	MemoryMB  float64       `json:"memory_mb"` // megabytes allocated while serving the request
}

// Timer is one run of a named timer, returned by StartTimer. Each call to
// StartTimer returns a new Timer with its own start time, so runs of the same
// name that overlap (concurrent requests, recursion) are measured
// independently. Stop records the run in the name's statistics, which
// GetStats reports in CustomTimers.
type Timer struct {
	Name      string        `json:"name"`
	StartTime time.Time     `json:"start_time"`
	Duration  time.Duration `json:"duration"` // set by Stop
	mutex     sync.Mutex
	stopped   bool
	stats     *timerStats // nil for a timer from a disabled profiler
}

// timerStats aggregates the completed runs of one timer name and tracks the
// runs still in progress, newest last, for StopTimer.
type timerStats struct {
	mu      sync.Mutex
	count   int64
	total   time.Duration
	lastRun time.Time
	running []*Timer
}

// ProfileStats is the snapshot GetStats returns. Request figures cover the
// retained requests (at most 1000); RecentRequests holds the last 10.
type ProfileStats struct {
	Uptime           time.Duration          `json:"uptime"`
	TotalRequests    int                    `json:"total_requests"`
	AverageResponse  time.Duration          `json:"average_response"`
	SlowestRequest   *RequestProfile        `json:"slowest_request"`
	FastestRequest   *RequestProfile        `json:"fastest_request"`
	RequestsByMethod map[string]int         `json:"requests_by_method"`
	RequestsByPath   map[string]int         `json:"requests_by_path"`
	MemoryStats      MemoryStats            `json:"memory_stats"`
	CustomTimers     map[string]*TimerStats `json:"custom_timers"`
	RecentRequests   []RequestProfile       `json:"recent_requests"`
}

// TimerStats summarizes the completed runs of one named timer.
type TimerStats struct {
	Count       int64         `json:"count"`
	TotalTime   time.Duration `json:"total_time"`
	AverageTime time.Duration `json:"average_time"`
	LastRun     time.Time     `json:"last_run"`
}

// MemoryStats is a snapshot of runtime memory figures, in megabytes, plus
// the GC count and goroutine count.
type MemoryStats struct {
	AllocMB      float64 `json:"alloc_mb"`
	TotalAllocMB float64 `json:"total_alloc_mb"`
	SysMB        float64 `json:"sys_mb"`
	NumGC        uint32  `json:"num_gc"`
	Goroutines   int     `json:"goroutines"`
}

// NewProfiler returns an enabled Profiler with no data.
func NewProfiler() *Profiler {
	p := &Profiler{
		requests:  make([]RequestProfile, 0),
		startTime: time.Now(),
		timers:    make(map[string]*timerStats),
	}
	p.enabled.Store(true)
	return p
}

// Enable turns recording on and returns p.
func (p *Profiler) Enable() *Profiler {
	p.enabled.Store(true)
	return p
}

// Disable turns recording off and returns p. Recorded data is kept.
func (p *Profiler) Disable() *Profiler {
	p.enabled.Store(false)
	return p
}

// IsEnabled reports whether the profiler is recording.
func (p *Profiler) IsEnabled() bool {
	return p.enabled.Load()
}

// Middleware returns middleware that records each request's method, path,
// status, duration, and bytes allocated, for app.Use. It calls
// runtime.ReadMemStats twice per request, which briefly stops the world. A
// disabled profiler passes requests through.
func (p *Profiler) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !p.enabled.Load() {
				next.ServeHTTP(w, r)
				return
			}

			start := time.Now()

			var memBefore runtime.MemStats
			runtime.ReadMemStats(&memBefore)

			wrapped := &responseWriter{ResponseWriter: w, statusCode: 200}

			next.ServeHTTP(wrapped, r)

			duration := time.Since(start)
			var memAfter runtime.MemStats
			runtime.ReadMemStats(&memAfter)
			// TotalAlloc only grows, so the delta cannot underflow when a
			// collection runs during the request (Alloc can shrink).
			memoryMB := float64(memAfter.TotalAlloc-memBefore.TotalAlloc) / 1024 / 1024

			profile := RequestProfile{
				Method:    r.Method,
				Path:      r.URL.Path,
				Duration:  duration,
				Status:    wrapped.statusCode,
				Timestamp: start,
				MemoryMB:  memoryMB,
			}

			p.recordRequest(profile)
		})
	}
}

// responseWriter records the status code a handler writes. It reports 200
// when the handler never calls WriteHeader.
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// Flush forwards to the wrapped writer so streaming handlers (SSE) still see
// an http.Flusher behind the profiler.
func (rw *responseWriter) Flush() {
	if f, ok := rw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap exposes the wrapped writer to http.ResponseController and to
// libraries that look for http.Hijacker (WebSocket upgrades).
func (rw *responseWriter) Unwrap() http.ResponseWriter {
	return rw.ResponseWriter
}

// recordRequest appends profile, keeping only the newest 1000 so memory
// stays bounded.
func (p *Profiler) recordRequest(profile RequestProfile) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	p.requests = append(p.requests, profile)

	if len(p.requests) > 1000 {
		p.requests = p.requests[len(p.requests)-1000:]
	}
}

// StartTimer starts a new run of the named timer and returns it. Call Stop on
// the returned Timer to record the run; that is the reliable form when runs of
// one name overlap. A disabled profiler returns a Timer that records nothing.
func (p *Profiler) StartTimer(name string) *Timer {
	if !p.enabled.Load() {
		return &Timer{Name: name, StartTime: time.Now()}
	}

	p.timerMutex.Lock()
	stats, exists := p.timers[name]
	if !exists {
		stats = &timerStats{}
		p.timers[name] = stats
	}
	p.timerMutex.Unlock()

	timer := &Timer{Name: name, StartTime: time.Now(), stats: stats}
	stats.mu.Lock()
	stats.running = append(stats.running, timer)
	stats.mu.Unlock()
	return timer
}

// StopTimer stops the most recently started run of the named timer that is
// still running and returns its duration, or 0 when none is running. With
// overlapping runs from several goroutines it cannot know which run the
// caller means; keep the Timer from StartTimer and call its Stop instead.
func (p *Profiler) StopTimer(name string) time.Duration {
	if !p.enabled.Load() {
		return 0
	}

	p.timerMutex.RLock()
	stats, exists := p.timers[name]
	p.timerMutex.RUnlock()
	if !exists {
		return 0
	}

	stats.mu.Lock()
	var timer *Timer
	if n := len(stats.running); n > 0 {
		timer = stats.running[n-1]
	}
	stats.mu.Unlock()
	if timer == nil {
		return 0
	}
	return timer.Stop()
}

// TimeOperation calls fn, records the run under name, and returns its
// duration. The run is recorded even if fn panics. A disabled profiler
// calls fn and returns 0.
func (p *Profiler) TimeOperation(name string, fn func()) (duration time.Duration) {
	if !p.enabled.Load() {
		fn()
		return 0
	}

	timer := p.StartTimer(name)
	defer func() { duration = timer.Stop() }()
	fn()
	return 0
}

// TimeOperationWithResult is TimeOperation for a function that returns a
// value; it returns that value and the duration.
func (p *Profiler) TimeOperationWithResult(name string, fn func() any) (result any, duration time.Duration) {
	if !p.enabled.Load() {
		return fn(), 0
	}

	timer := p.StartTimer(name)
	defer func() { duration = timer.Stop() }()
	return fn(), 0
}

// GetStats returns a snapshot of the recorded requests, the named timers
// with at least one completed run, and current memory figures.
func (p *Profiler) GetStats() ProfileStats {
	p.mutex.RLock()
	requests := make([]RequestProfile, len(p.requests))
	copy(requests, p.requests)
	startTime := p.startTime
	p.mutex.RUnlock()

	p.timerMutex.RLock()
	customTimers := make(map[string]*TimerStats)
	for name, timer := range p.timers {
		timer.mu.Lock()
		if timer.count > 0 {
			customTimers[name] = &TimerStats{
				Count:       timer.count,
				TotalTime:   timer.total,
				AverageTime: timer.total / time.Duration(timer.count),
				LastRun:     timer.lastRun,
			}
		}
		timer.mu.Unlock()
	}
	p.timerMutex.RUnlock()

	stats := ProfileStats{
		Uptime:           time.Since(startTime),
		TotalRequests:    len(requests),
		RequestsByMethod: make(map[string]int),
		RequestsByPath:   make(map[string]int),
		MemoryStats:      p.getMemoryStats(),
		CustomTimers:     customTimers,
		RecentRequests:   requests,
	}

	if len(requests) > 0 {
		var totalDuration time.Duration
		var slowest, fastest *RequestProfile

		for i, req := range requests {
			totalDuration += req.Duration
			stats.RequestsByMethod[req.Method]++
			stats.RequestsByPath[req.Path]++

			if slowest == nil || req.Duration > slowest.Duration {
				slowest = &requests[i]
			}
			if fastest == nil || req.Duration < fastest.Duration {
				fastest = &requests[i]
			}
		}

		stats.AverageResponse = totalDuration / time.Duration(len(requests))
		stats.SlowestRequest = slowest
		stats.FastestRequest = fastest

		if len(stats.RecentRequests) > 10 {
			stats.RecentRequests = stats.RecentRequests[len(stats.RecentRequests)-10:]
		}
	}

	return stats
}

// getMemoryStats reads the current runtime memory statistics.
func (p *Profiler) getMemoryStats() MemoryStats {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	return MemoryStats{
		AllocMB:      float64(m.Alloc) / 1024 / 1024,
		TotalAllocMB: float64(m.TotalAlloc) / 1024 / 1024,
		SysMB:        float64(m.Sys) / 1024 / 1024,
		NumGC:        m.NumGC,
		Goroutines:   runtime.NumGoroutine(),
	}
}

// GetTopSlowRequests returns up to n recorded requests, slowest first.
func (p *Profiler) GetTopSlowRequests(n int) []RequestProfile {
	p.mutex.RLock()
	requests := make([]RequestProfile, len(p.requests))
	copy(requests, p.requests)
	p.mutex.RUnlock()

	sort.Slice(requests, func(i, j int) bool {
		return requests[i].Duration > requests[j].Duration
	})

	if n > len(requests) {
		n = len(requests)
	}
	if n < 0 {
		n = 0
	}

	return requests[:n]
}

// GetRequestsByPath returns timing statistics for each recorded path.
func (p *Profiler) GetRequestsByPath() map[string]PathStats {
	p.mutex.RLock()
	requests := make([]RequestProfile, len(p.requests))
	copy(requests, p.requests)
	p.mutex.RUnlock()

	pathStats := make(map[string]PathStats)

	for _, req := range requests {
		stats, exists := pathStats[req.Path]
		if !exists {
			stats = PathStats{
				Path:        req.Path,
				Count:       0,
				TotalTime:   0,
				AverageTime: 0,
				SlowestTime: 0,
				FastestTime: 0,
			}
		}

		stats.Count++
		stats.TotalTime += req.Duration

		if stats.SlowestTime == 0 || req.Duration > stats.SlowestTime {
			stats.SlowestTime = req.Duration
		}
		if stats.FastestTime == 0 || req.Duration < stats.FastestTime {
			stats.FastestTime = req.Duration
		}

		stats.AverageTime = stats.TotalTime / time.Duration(stats.Count)
		pathStats[req.Path] = stats
	}

	return pathStats
}

// PathStats summarizes the recorded requests for one path.
type PathStats struct {
	Path        string        `json:"path"`
	Count       int           `json:"count"`
	TotalTime   time.Duration `json:"total_time"`
	AverageTime time.Duration `json:"average_time"`
	SlowestTime time.Duration `json:"slowest_time"`
	FastestTime time.Duration `json:"fastest_time"`
}

// Reset discards every recorded request and timer and restarts the uptime
// clock. Timers started before Reset still stop without error but their
// runs are not counted.
func (p *Profiler) Reset() {
	p.mutex.Lock()
	p.requests = make([]RequestProfile, 0)
	p.startTime = time.Now()
	p.mutex.Unlock()

	p.timerMutex.Lock()
	p.timers = make(map[string]*timerStats)
	p.timerMutex.Unlock()
}

// PrintStats writes a readable summary of GetStats to stdout.
func (p *Profiler) PrintStats() {
	stats := p.GetStats()

	fmt.Printf("\n=== HyperChi Profiler Stats ===\n")
	fmt.Printf("Uptime: %v\n", stats.Uptime)
	fmt.Printf("Total Requests: %d\n", stats.TotalRequests)

	if stats.TotalRequests > 0 {
		fmt.Printf("Average Response Time: %v\n", stats.AverageResponse)
		if stats.SlowestRequest != nil {
			fmt.Printf("Slowest Request: %s %s (%v)\n",
				stats.SlowestRequest.Method,
				stats.SlowestRequest.Path,
				stats.SlowestRequest.Duration)
		}
		if stats.FastestRequest != nil {
			fmt.Printf("Fastest Request: %s %s (%v)\n",
				stats.FastestRequest.Method,
				stats.FastestRequest.Path,
				stats.FastestRequest.Duration)
		}
	}

	fmt.Printf("\nMemory Stats:\n")
	fmt.Printf("  Current Alloc: %.2f MB\n", stats.MemoryStats.AllocMB)
	fmt.Printf("  Total Alloc: %.2f MB\n", stats.MemoryStats.TotalAllocMB)
	fmt.Printf("  System: %.2f MB\n", stats.MemoryStats.SysMB)
	fmt.Printf("  Goroutines: %d\n", stats.MemoryStats.Goroutines)
	fmt.Printf("  GC Runs: %d\n", stats.MemoryStats.NumGC)

	if len(stats.CustomTimers) > 0 {
		fmt.Printf("\nCustom Timers:\n")
		for name, timer := range stats.CustomTimers {
			fmt.Printf("  %s: %d calls, avg %v, total %v\n",
				name, timer.Count, timer.AverageTime, timer.TotalTime)
		}
	}

	fmt.Printf("\n")
}

// StatsHandler returns an http.Handler that serves GetStats as JSON.
// Durations are encoded in nanoseconds. The stats expose request paths and
// timings, so mount it on a development-only or authenticated route, for
// example app.Handle(http.MethodGet, "/debug/profile", profiler.StatsHandler()).
func (p *Profiler) StatsHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := json.Marshal(p.GetStats())
		if err != nil {
			http.Error(w, "profiler stats could not be encoded", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if _, err := w.Write(body); err != nil {
			log.Printf("hyperchi: failed to write profiler stats: %v", err)
		}
	})
}

// Stop ends this run, records it in the timer's statistics, and returns its
// duration. Calling Stop again returns the same duration without recording
// the run twice. A Timer from a disabled profiler records nothing and returns
// 0.
func (t *Timer) Stop() time.Duration {
	t.mutex.Lock()
	if t.stopped || t.stats == nil {
		duration := t.Duration
		t.mutex.Unlock()
		return duration
	}
	t.stopped = true
	t.Duration = time.Since(t.StartTime)
	duration := t.Duration
	t.mutex.Unlock()

	t.stats.record(t, duration)
	return duration
}

// record adds a completed run and removes it from the running list.
func (s *timerStats) record(t *Timer, duration time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.count++
	s.total += duration
	s.lastRun = t.StartTime
	for i, running := range s.running {
		if running == t {
			s.running = append(s.running[:i], s.running[i+1:]...)
			break
		}
	}
}
