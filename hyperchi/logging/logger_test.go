package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/regiellis/hyperchi/hyperchi/config"
)

func decodeLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var records []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("decode %q: %v", line, err)
		}
		records = append(records, rec)
	}
	return records
}

func jsonLogger(buf *bytes.Buffer, level Level) *Logger {
	cfg := DefaultConfig()
	cfg.Output = buf
	cfg.Level = level
	return NewLogger(cfg)
}

// The default config reports the framework's module version, not a fixed
// string.
func TestDefaultConfigVersionComesFromBuildInfo(t *testing.T) {
	if got, want := DefaultConfig().Version, config.FrameworkVersion(); got != want {
		t.Fatalf("Version = %q, want %q", got, want)
	}
	var buf bytes.Buffer
	jsonLogger(&buf, LevelInfo).Info("hello")
	records := decodeLines(t, &buf)
	if len(records) != 1 || records[0]["version"] != config.FrameworkVersion() {
		t.Fatalf("record version = %v", records)
	}
}

// The default logger writes to stderr: log records on stdout would corrupt
// programs that use stdout for their own output.
func TestDefaultConfigWritesToStderr(t *testing.T) {
	if DefaultConfig().Output != os.Stderr {
		t.Fatal("default output is not stderr")
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

// A failed record write is reported through the standard logger, which the
// rest of the framework reports through, not printed straight to os.Stderr.
func TestWriteFailureGoesThroughStandardLogger(t *testing.T) {
	var captured bytes.Buffer
	prev, prevFlags := log.Writer(), log.Flags()
	log.SetOutput(&captured)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(prev)
		log.SetFlags(prevFlags)
	})

	cfg := DefaultConfig()
	cfg.Output = failingWriter{}
	NewLogger(cfg).Error("lost")

	if got := captured.String(); !strings.HasPrefix(got, "hyperchi: logging: write record:") || !strings.Contains(got, "disk full") {
		t.Fatalf("standard logger got %q", got)
	}
}

func TestLevelFilteringAndFields(t *testing.T) {
	var buf bytes.Buffer
	logger := jsonLogger(&buf, LevelWarn).WithField("component", "billing")
	logger.Debug("dropped")
	logger.Info("dropped")
	logger.WithError(errors.New("boom")).Warn("kept", "attempt", 2)
	logger.Error("also kept")

	records := decodeLines(t, &buf)
	if len(records) != 2 {
		t.Fatalf("got %d records, want 2: %v", len(records), records)
	}
	first := records[0]
	if first["msg"] != "kept" || first["level"] != "WARN" || first["component"] != "billing" ||
		first["error"] != "boom" || first["attempt"] != float64(2) || first["service"] != "hyperchi" {
		t.Fatalf("unexpected record: %v", first)
	}
	if records[1]["level"] != "ERROR" {
		t.Fatalf("second record level = %v", records[1]["level"])
	}
}

func TestWithFieldDoesNotMutateParent(t *testing.T) {
	var buf bytes.Buffer
	parent := jsonLogger(&buf, LevelInfo)
	parent.WithFields(map[string]any{"child": true})
	parent.Info("plain")
	if rec := decodeLines(t, &buf)[0]; rec["child"] != nil {
		t.Fatal("child field leaked into the parent logger")
	}
}

func TestContextualLoggerAddsRequestAndTraceIDs(t *testing.T) {
	var buf bytes.Buffer
	ctx := ContextWithTraceID(ContextWithRequestID(context.Background(), "req-1"), "trace-9")
	NewContextualLogger(ctx, jsonLogger(&buf, LevelInfo)).WithField("k", "v").Info("hi")
	rec := decodeLines(t, &buf)[0]
	if rec["request_id"] != "req-1" || rec["trace_id"] != "trace-9" || rec["k"] != "v" {
		t.Fatalf("unexpected record: %v", rec)
	}
	if RequestIDFromContext(context.Background()) != "" || TraceIDFromContext(context.Background()) != "" {
		t.Fatal("empty context reported IDs")
	}
}

func TestRequestLoggerRecordsStatusAndBytes(t *testing.T) {
	var buf bytes.Buffer
	handler := RequestLogger(jsonLogger(&buf, LevelInfo))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if RequestIDFromContext(r.Context()) != "abc" {
			t.Error("request ID not in context")
		}
		w.WriteHeader(http.StatusTeapot)
		if _, err := w.Write([]byte("short")); err != nil {
			t.Error(err)
		}
	}))
	req := httptest.NewRequest(http.MethodGet, "/brew?x=1", nil)
	req.Header.Set("X-Request-ID", "abc")
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	handler.ServeHTTP(httptest.NewRecorder(), req)

	records := decodeLines(t, &buf)
	if len(records) != 2 {
		t.Fatalf("got %d records, want 2", len(records))
	}
	start, done := records[0], records[1]
	if start["remote_ip"] != "192.0.2.1" {
		t.Fatalf("remote_ip = %v; forwarding headers must be ignored", start["remote_ip"])
	}
	if done["status_code"] != float64(http.StatusTeapot) || done["bytes_written"] != float64(5) || done["request_id"] != "abc" {
		t.Fatalf("completion record: %v", done)
	}
}

func TestLevelString(t *testing.T) {
	for level, want := range map[Level]string{
		LevelDebug: "DEBUG", LevelInfo: "INFO", LevelWarn: "WARN",
		LevelError: "ERROR", LevelFatal: "FATAL", Level(99): "UNKNOWN",
	} {
		if got := level.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", level, got, want)
		}
	}
}

// The default logger is read by every package-level helper and may be
// replaced at any time, so concurrent Set/Get must be race-free (run -race).
func TestDefaultLoggerConcurrentSetGet(t *testing.T) {
	t.Cleanup(func() { SetDefaultLogger(nil) })
	SetDefaultLogger(nil)
	var buf bytes.Buffer
	replacement := jsonLogger(&buf, LevelError)
	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func(i int) {
			defer func() { done <- struct{}{} }()
			if i%2 == 0 {
				SetDefaultLogger(replacement)
				return
			}
			if GetDefaultLogger() == nil {
				t.Error("GetDefaultLogger returned nil")
			}
		}(i)
	}
	for i := 0; i < 8; i++ {
		<-done
	}
	if GetDefaultLogger() != replacement {
		t.Fatal("SetDefaultLogger value was lost")
	}
	SetDefaultLogger(nil)
	first := GetDefaultLogger()
	if first == nil || GetDefaultLogger() != first {
		t.Fatal("lazy default logger is not created once")
	}
}
