package performance

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/regiellis/hyperchi/hyperchi"
)

// waitForGoroutines yields until at most want goroutines run, failing at a
// deadline. Close returns once the job workers and Badger's goroutines have
// signaled they are done, which can be a moment before they exit, so a single
// count can be early. Yielding lets them finish without a fixed sleep.
func waitForGoroutines(t *testing.T, want int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for got := runtime.NumGoroutine(); got > want; got = runtime.NumGoroutine() {
		if time.Now().After(deadline) {
			buf := make([]byte, 1<<20)
			t.Fatalf("goroutines = %d, want at most %d\n%s", got, want, buf[:runtime.Stack(buf, true)])
		}
		runtime.Gosched()
	}
}

func TestModuleCloseStopsJobWorkers(t *testing.T) {
	app := hyperchi.New()
	baseline := runtime.NumGoroutine()

	m := New(Config{Dir: t.TempDir(), Workers: 2, EnableJobQueue: true, JobWorkers: 4})
	if err := m.Install(app); err != nil {
		t.Fatal(err)
	}
	if err := app.Integrations().Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if runtime.NumGoroutine() <= baseline {
		t.Fatal("expected the store and workers to be running")
	}

	if err := app.Close(); err != nil {
		t.Fatal(err)
	}
	waitForGoroutines(t, baseline)
}

func TestModuleQueueRunsJobsAfterStart(t *testing.T) {
	app := hyperchi.New()
	m := New(Config{Dir: t.TempDir(), Workers: 2, EnableJobQueue: true, JobWorkers: 1})
	if err := m.Install(app); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := app.Close(); err != nil {
			t.Error(err)
		}
	}()
	done := make(chan string, 1)
	m.JobQueue().RegisterHandler("echo", func(job *Job) error {
		done <- job.Payload["msg"].(string)
		return nil
	})
	if _, err := m.JobQueue().Push("echo", map[string]any{"msg": "hi"}); err != nil {
		t.Fatal(err)
	}
	if err := app.Integrations().Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-done:
		if got != "hi" {
			t.Fatalf("payload = %q", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("job did not run")
	}
}

func TestInstallAppliesRateLimitRules(t *testing.T) {
	app := hyperchi.New()
	m := New(Config{
		Dir:             t.TempDir(),
		Workers:         2,
		EnableRateLimit: true,
		RateLimitRules:  []RateLimitRule{{Path: "/limited", Rate: "2/hour"}},
	})
	if err := m.Install(app); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := app.Close(); err != nil {
			t.Error(err)
		}
	}()
	app.Get("/limited", func(c *hyperchi.Context) error { return c.Text("ok") })
	app.Get("/free", func(c *hyperchi.Context) error { return c.Text("ok") })

	for i, want := range []int{http.StatusOK, http.StatusOK, http.StatusTooManyRequests} {
		w := httptest.NewRecorder()
		app.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/limited", nil))
		if w.Code != want {
			t.Fatalf("request %d: status %d, want %d", i+1, w.Code, want)
		}
	}
	for range 3 {
		w := httptest.NewRecorder()
		app.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/free", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("unmatched route limited: %d", w.Code)
		}
	}
}

func TestInstallRejectsInvalidRateLimitRule(t *testing.T) {
	app := hyperchi.New()
	defer func() {
		if err := app.Close(); err != nil {
			t.Error(err)
		}
	}()
	m := New(Config{Dir: t.TempDir(), EnableRateLimit: true, RateLimitRules: []RateLimitRule{{Path: "/", Rate: "lots"}}})
	if err := m.Install(app); err == nil {
		t.Fatal("Install accepted an unparseable rate")
	}
}

func TestInstallTreatsZeroWorkersAsDefault(t *testing.T) {
	app := hyperchi.New()
	defer func() {
		if err := app.Close(); err != nil {
			t.Error(err)
		}
	}()
	m := New(Config{Dir: t.TempDir()})
	if err := m.Install(app); err != nil {
		t.Fatal(err)
	}
	store := m.GetStore("badger").(*BadgerAdvancedStore)
	if got := store.db.Opts().NumCompactors; got != 4 {
		t.Fatalf("NumCompactors = %d, want the default 4", got)
	}
}

func TestInstallRejectsWorkersBadgerRefuses(t *testing.T) {
	for _, workers := range []int{1, -1} {
		app := hyperchi.New()
		err := New(Config{Dir: t.TempDir(), Workers: workers}).Install(app)
		if err == nil || !strings.Contains(err.Error(), "Workers") {
			t.Errorf("Workers %d: Install error = %v, want one naming Config.Workers", workers, err)
		}
		if err := app.Close(); err != nil {
			t.Error(err)
		}
	}
}

func TestInstallRoutesBadgerLogsThroughSlog(t *testing.T) {
	for _, tc := range []struct {
		name      string
		level     slog.Leveler
		wantInfo  bool
		wantLines bool
	}{
		{name: "default drops INFO", level: nil},
		{name: "LevelInfo keeps INFO", level: slog.LevelInfo, wantInfo: true, wantLines: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
			app := hyperchi.New()
			m := New(Config{Dir: t.TempDir(), BadgerLogger: logger, BadgerLogLevel: tc.level})
			if err := m.Install(app); err != nil {
				t.Fatal(err)
			}
			if err := app.Close(); err != nil {
				t.Fatal(err)
			}
			out := buf.String()
			if got := strings.Contains(out, "level=INFO"); got != tc.wantInfo {
				t.Fatalf("INFO lines present = %v, want %v:\n%s", got, tc.wantInfo, out)
			}
			if got := strings.Contains(out, "component=badger"); got != tc.wantLines {
				t.Fatalf("badger lines present = %v, want %v:\n%s", got, tc.wantLines, out)
			}
		})
	}

	var buf bytes.Buffer
	newSlogBadgerLogger(slog.New(slog.NewTextHandler(&buf, nil)), nil).Warningf("disk %s\n", "low")
	if out := buf.String(); !strings.Contains(out, "level=WARN") || !strings.Contains(out, `msg="disk low"`) {
		t.Fatalf("warning not passed through: %q", out)
	}
}
