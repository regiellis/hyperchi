package helpers

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func serve(t *testing.T, h http.Handler, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func TestProfilerMiddlewareRecordsRequests(t *testing.T) {
	p := NewProfiler()
	h := p.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/missing":
			http.NotFound(w, r)
		case "/created":
			w.WriteHeader(http.StatusCreated)
		default:
			if _, err := io.WriteString(w, "ok"); err != nil {
				t.Error(err)
			}
		}
	}))

	if rec := serve(t, h, "GET", "/a"); rec.Code != 200 || rec.Body.String() != "ok" {
		t.Fatalf("passthrough: %d %q", rec.Code, rec.Body.String())
	}
	serve(t, h, "GET", "/a")
	serve(t, h, "POST", "/created")
	serve(t, h, "GET", "/missing")

	stats := p.GetStats()
	if stats.TotalRequests != 4 || len(stats.RecentRequests) != 4 {
		t.Fatalf("TotalRequests = %d", stats.TotalRequests)
	}
	if stats.RequestsByMethod["GET"] != 3 || stats.RequestsByMethod["POST"] != 1 || stats.RequestsByPath["/a"] != 2 {
		t.Errorf("grouping: %v %v", stats.RequestsByMethod, stats.RequestsByPath)
	}
	statuses := map[string]int{}
	for _, r := range stats.RecentRequests {
		statuses[r.Path] = r.Status
		if r.Timestamp.IsZero() || r.Duration < 0 || r.MemoryMB < 0 {
			t.Errorf("bad profile %+v", r)
		}
	}
	if statuses["/a"] != 200 || statuses["/created"] != 201 || statuses["/missing"] != 404 {
		t.Errorf("statuses = %v", statuses)
	}
	if stats.SlowestRequest == nil || stats.FastestRequest == nil || stats.SlowestRequest.Duration < stats.FastestRequest.Duration {
		t.Error("slowest/fastest")
	}
	if stats.Uptime <= 0 || stats.MemoryStats.Goroutines < 1 || stats.MemoryStats.SysMB <= 0 {
		t.Errorf("uptime/memory: %v %+v", stats.Uptime, stats.MemoryStats)
	}

	byPath := p.GetRequestsByPath()
	if a := byPath["/a"]; a.Count != 2 || a.Path != "/a" || a.TotalTime < a.SlowestTime || a.SlowestTime < a.FastestTime {
		t.Errorf("GetRequestsByPath[/a] = %+v", a)
	}

	p.Disable()
	if p.IsEnabled() {
		t.Fatal("Disable")
	}
	serve(t, h, "GET", "/a")
	if p.GetStats().TotalRequests != 4 {
		t.Error("a disabled profiler recorded a request")
	}
	if !p.Enable().IsEnabled() {
		t.Error("Enable")
	}

	p.Reset()
	if s := p.GetStats(); s.TotalRequests != 0 || len(s.CustomTimers) != 0 || s.SlowestRequest != nil {
		t.Errorf("Reset: %+v", s)
	}
}

func TestProfilerStatsWindows(t *testing.T) {
	p := NewProfiler()
	for i := range 1005 {
		p.recordRequest(RequestProfile{Method: "GET", Path: "/p", Duration: time.Duration(i) * time.Millisecond})
	}
	stats := p.GetStats()
	if stats.TotalRequests != 1000 {
		t.Errorf("history should keep the last 1000, got %d", stats.TotalRequests)
	}
	if len(stats.RecentRequests) != 10 || stats.RecentRequests[9].Duration != 1004*time.Millisecond {
		t.Errorf("RecentRequests should be the last 10")
	}
	if stats.SlowestRequest.Duration != 1004*time.Millisecond || stats.FastestRequest.Duration != 5*time.Millisecond {
		t.Errorf("slowest %v fastest %v", stats.SlowestRequest.Duration, stats.FastestRequest.Duration)
	}

	top := p.GetTopSlowRequests(3)
	if len(top) != 3 || top[0].Duration != 1004*time.Millisecond || top[2].Duration != 1002*time.Millisecond {
		t.Errorf("GetTopSlowRequests = %v", top)
	}
	if len(p.GetTopSlowRequests(5000)) != 1000 {
		t.Error("GetTopSlowRequests clamps to the history size")
	}
	if got := p.GetTopSlowRequests(-1); len(got) != 0 {
		t.Errorf("GetTopSlowRequests(-1) = %d items, want none", len(got))
	}
}

// gcGarbage keeps a large allocation reachable until the test drops it.
var gcGarbage []byte

func TestProfilerMemoryDeltaSurvivesGC(t *testing.T) {
	p := NewProfiler()
	h := p.Middleware()(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		// Free garbage allocated before the request: Alloc drops below
		// the value sampled at the start of the request.
		gcGarbage = nil
		runtime.GC()
	}))
	gcGarbage = make([]byte, 64<<20)
	serve(t, h, "GET", "/gc")

	mem := p.GetStats().RecentRequests[0].MemoryMB
	if mem < 0 || mem > 1024 {
		t.Fatalf("MemoryMB = %v after a GC during the request, want a small non-negative figure", mem)
	}
}

func TestProfilerKeepsStreamingInterfaces(t *testing.T) {
	p := NewProfiler()

	flushed := false
	h := p.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f, ok := w.(http.Flusher)
		if !ok {
			t.Error("profiled writer hides http.Flusher (SSE breaks)")
			return
		}
		if _, err := io.WriteString(w, "data: x\n\n"); err != nil {
			t.Error(err)
		}
		f.Flush()
		flushed = true
	}))
	rec := serve(t, h, "GET", "/events")
	if !flushed || !rec.Flushed {
		t.Error("Flush did not reach the underlying writer")
	}

	hijacked := false
	h = p.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _, err := http.NewResponseController(w).Hijack()
		if err != nil {
			t.Errorf("hijack through the profiler: %v", err)
			return
		}
		hijacked = true
		if _, err := io.WriteString(conn, "HTTP/1.1 101 Switching Protocols\r\n\r\n"); err != nil {
			t.Error(err)
		}
		if err := conn.Close(); err != nil {
			t.Error(err)
		}
	}))
	srv := httptest.NewServer(h)
	defer srv.Close()
	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := conn.Close(); err != nil && !strings.Contains(err.Error(), "use of closed") {
			t.Error(err)
		}
	}()
	if _, err := io.WriteString(conn, "GET /ws HTTP/1.1\r\nHost: x\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	status, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil || !strings.HasPrefix(status, "HTTP/1.1 101") || !hijacked {
		t.Errorf("hijacked response %q, %v", status, err)
	}

	// Unwrap exposes the original writer to http.ResponseController.
	rec = httptest.NewRecorder()
	rw := &responseWriter{ResponseWriter: rec, statusCode: 200}
	if rw.Unwrap() != rec {
		t.Error("Unwrap")
	}
}

func TestProfilerTimers(t *testing.T) {
	p := NewProfiler()
	timer := p.StartTimer("db")
	if timer.Name != "db" {
		t.Fatal("StartTimer name")
	}
	if d := p.StopTimer("db"); d < 0 {
		t.Error("StopTimer")
	}
	if p.StopTimer("never-started") != 0 {
		t.Error("StopTimer of an unknown timer")
	}

	ran := false
	if d := p.TimeOperation("db", func() { ran = true }); !ran || d < 0 {
		t.Error("TimeOperation")
	}
	result, d := p.TimeOperationWithResult("calc", func() any { return 42 })
	if result != 42 || d < 0 {
		t.Error("TimeOperationWithResult")
	}
	if p.StartTimer("db").Stop() < 0 {
		t.Error("Timer.Stop")
	}

	stats := p.GetStats().CustomTimers
	if stats["db"] == nil || stats["db"].Count != 3 || stats["calc"].Count != 1 {
		t.Fatalf("CustomTimers = %+v", stats)
	}
	if stats["db"].AverageTime != stats["db"].TotalTime/3 || stats["db"].LastRun.IsZero() {
		t.Errorf("db stats = %+v", stats["db"])
	}

	p.Disable()
	ran = false
	if d := p.TimeOperation("off", func() { ran = true }); !ran || d != 0 {
		t.Error("disabled TimeOperation still runs fn and reports 0")
	}
	if r, d := p.TimeOperationWithResult("off", func() any { return "v" }); r != "v" || d != 0 {
		t.Error("disabled TimeOperationWithResult")
	}
	if p.StopTimer("db") != 0 {
		t.Error("disabled StopTimer")
	}
	if d := p.StartTimer("off").Stop(); d != 0 {
		t.Errorf("a disabled profiler's timer reported %v", d)
	}
	if _, ok := p.GetStats().CustomTimers["off"]; ok {
		t.Error("a disabled profiler registered a timer")
	}
}

func TestProfilerConcurrentUse(t *testing.T) {
	p := NewProfiler()
	h := p.Middleware()(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	var wg sync.WaitGroup
	for i := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 25 {
				serve(t, h, "GET", "/c")
				p.TimeOperation("op", func() {})
				p.GetStats()
				if i == 0 {
					p.Disable()
					p.Enable()
					p.Reset()
				}
			}
		}()
	}
	wg.Wait()
	if p.GetStats().TotalRequests > 100 {
		t.Error("recorded more requests than were served")
	}
}

func TestProfilerPrintStats(t *testing.T) {
	p := NewProfiler()
	p.recordRequest(RequestProfile{Method: "GET", Path: "/x", Duration: time.Millisecond})
	p.TimeOperation("job", func() {})

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout := os.Stdout
	os.Stdout = w
	p.PrintStats()
	os.Stdout = stdout
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Total Requests: 1", "Slowest Request: GET /x", "Fastest Request: GET /x", "Memory Stats:", "job: 1 calls"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("PrintStats missing %q:\n%s", want, out)
		}
	}
}
