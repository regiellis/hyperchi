package helpers

import (
	"bytes"
	"errors"
	"io"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// useDefaultLogger swaps in a fresh package-level logger writing to the
// returned buffer and restores the previous one when the test ends.
func useDefaultLogger(t *testing.T) *bytes.Buffer {
	t.Helper()
	prev := defaultLogger
	t.Cleanup(func() { defaultLogger = prev })
	var buf bytes.Buffer
	defaultLogger = NewLogger()
	ConfigureLogger(LoggerConfig{Level: DEBUG, Output: &buf})
	return &buf
}

var logLine = regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2} \[(DEBUG|INFO|WARN|ERROR)\] `)

func TestLogLevelNamesAndColors(t *testing.T) {
	for level, name := range map[LogLevel]string{DEBUG: "DEBUG", INFO: "INFO", WARN: "WARN", ERROR: "ERROR", FATAL: "FATAL", LogLevel(42): "UNKNOWN"} {
		if level.String() != name {
			t.Errorf("%d.String() = %q", level, level.String())
		}
	}
	colors := map[string]bool{}
	for _, level := range []LogLevel{DEBUG, INFO, WARN, ERROR, FATAL} {
		c := level.Color()
		if !strings.HasPrefix(c, "\033[") || colors[c] {
			t.Errorf("%s color %q missing or reused", level, c)
		}
		colors[c] = true
	}
	if LogLevel(-1).Color() != "\033[0m" {
		t.Error("unknown level should reset color")
	}
}

func TestLoggerLevelsAndFormat(t *testing.T) {
	var buf bytes.Buffer
	l := NewLogger().SetOutput(&buf).SetColored(false).SetLevel(WARN)
	l.Debug("hidden %d", 1)
	l.Info("hidden")
	l.Warn("careful %s", "now")
	l.Error("bad: %v", errors.New("boom"))

	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines: %q", len(lines), buf.String())
	}
	if !logLine.MatchString(lines[0]) || !strings.HasSuffix(lines[0], "[WARN] careful now") {
		t.Errorf("warn line = %q", lines[0])
	}
	if !strings.HasSuffix(lines[1], "[ERROR] bad: boom") {
		t.Errorf("error line = %q", lines[1])
	}

	buf.Reset()
	l.SetColored(true).SetLevel(DEBUG).SetPrefix("api").Debug("x")
	if !strings.Contains(buf.String(), "[\033[36mDEBUG\033[0m] [api] x") {
		t.Errorf("colored line = %q", buf.String())
	}
}

func TestLoggerShowCallerPointsAtTheCallSite(t *testing.T) {
	var buf bytes.Buffer
	l := NewLogger().SetOutput(&buf).SetColored(false).SetShowCaller(true)
	l.Info("method")
	if !strings.Contains(buf.String(), " logging_test.go:") {
		t.Errorf("method caller = %q", buf.String())
	}

	pkg := useDefaultLogger(t)
	SetLogShowCaller(true)
	Debug("d")
	Info("i")
	Warn("w")
	Error("e")
	Print("p", 1)
	Printf("pf %d", 2)
	LogError(errors.New("le"), "ctx")
	LogErrorf(errors.New("lef"), "op %d", 3)
	Log("l")
	Logf("lf")
	lines := strings.Split(strings.TrimSuffix(pkg.String(), "\n"), "\n")
	if len(lines) != 10 {
		t.Fatalf("got %d lines: %q", len(lines), pkg.String())
	}
	for _, line := range lines {
		if !strings.Contains(line, " logging_test.go:") {
			t.Errorf("package-level caller should be this test file: %q", line)
		}
	}
}

func TestPackageLevelLogging(t *testing.T) {
	buf := useDefaultLogger(t)
	SetLogColored(false)
	SetLogLevel(INFO)
	Debug("hidden")
	Info("count=%d", 3)
	Print("a", "b")
	Printf("%s!", "hey")
	LogError(nil, "ignored")
	LogError(errors.New("e1"))
	LogError(errors.New("e2"), "load", "config")
	LogErrorf(nil, "ignored")
	LogErrorf(errors.New("e3"), "user %d", 7)
	Log("debug only")

	out := buf.String()
	for _, want := range []string{"[INFO] count=3", "[INFO] ab", "[INFO] hey!", "[ERROR] Error: e1", "[ERROR] Error (load, config): e2", "[ERROR] Error (user 7): e3"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %q", want, out)
		}
	}
	if strings.Contains(out, "hidden") || strings.Contains(out, "ignored") || strings.Contains(out, "debug only") {
		t.Errorf("unexpected output: %q", out)
	}

	SetLogOutput(io.Discard)
	Info("discarded")
	if strings.Contains(buf.String(), "discarded") {
		t.Error("SetLogOutput")
	}
}

func TestConfigureLoggerPresets(t *testing.T) {
	prev := defaultLogger
	t.Cleanup(func() { defaultLogger = prev })

	defaultLogger = NewLogger()
	SetupDevelopmentLogging()
	if defaultLogger.level != DEBUG || !defaultLogger.colored || !defaultLogger.showCaller || defaultLogger.output != os.Stdout {
		t.Error("development preset")
	}
	SetupProductionLogging()
	if defaultLogger.level != INFO || defaultLogger.colored || defaultLogger.showCaller {
		t.Error("production preset")
	}
	SetupTestLogging()
	if defaultLogger.level != ERROR || defaultLogger.output != io.Discard {
		t.Error("test preset")
	}
	ConfigureLogger(LoggerConfig{Level: WARN, Prefix: "svc"})
	if defaultLogger.output != io.Discard || defaultLogger.prefix != "svc" {
		t.Error("a nil Output keeps the current writer; a prefix is applied")
	}
}

func TestLoggerFields(t *testing.T) {
	var buf bytes.Buffer
	base := NewLogger().SetOutput(&buf).SetColored(false)
	child := base.WithField("req", "42").WithFields(map[string]string{"user": "ana", "app": "hc", "mod": "x"})
	child.Info("hi")
	if !strings.HasSuffix(buf.String(), "[INFO] [req=42 app=hc mod=x user=ana] hi\n") {
		t.Errorf("fields line = %q", buf.String())
	}
	buf.Reset()
	base.Info("plain")
	if strings.Contains(buf.String(), "req=") {
		t.Error("WithField must not modify the parent logger")
	}
	buf.Reset()
	base.WithFields(map[string]string{"k": "v"}).Info("x")
	if !strings.Contains(buf.String(), "[k=v] x") {
		t.Errorf("WithFields without a prefix = %q", buf.String())
	}
}

func TestLoggerConcurrentWrites(t *testing.T) {
	var buf bytes.Buffer // not safe for concurrent use on its own
	base := NewLogger().SetOutput(&buf).SetColored(false)
	derived := base.WithField("worker", "w")

	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l := base
			if i%2 == 0 {
				l = derived
			}
			for range 50 {
				l.Info("message")
			}
		}()
	}
	wg.Wait()

	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	if len(lines) != 400 {
		t.Fatalf("got %d lines, want 400", len(lines))
	}
	for _, line := range lines {
		if !logLine.MatchString(line) || !strings.HasSuffix(line, "message") {
			t.Fatalf("interleaved line %q", line)
		}
	}
}
