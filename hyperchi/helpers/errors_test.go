package helpers

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// captureDefaultHandler routes the package-level error handler's log calls
// into the returned slice for the duration of the test.
func captureDefaultHandler(t *testing.T) *[]string {
	t.Helper()
	prevLogger, prevSilent := defaultErrorHandler.logger, defaultErrorHandler.silent
	t.Cleanup(func() {
		defaultErrorHandler.logger = prevLogger
		defaultErrorHandler.silent = prevSilent
	})
	var lines []string
	SetDefaultSilent(false)
	SetDefaultLogger(func(format string, args ...any) {
		lines = append(lines, fmt.Sprintf(format, args...))
	})
	return &lines
}

// captureStdLog redirects the standard logger used by OrLog/OrLogDefault.
func captureStdLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prevOut, prevFlags := log.Writer(), log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(prevOut)
		log.SetFlags(prevFlags)
	})
	return &buf
}

func TestMustPanicsWithTheError(t *testing.T) {
	lines := captureDefaultHandler(t)
	sentinel := errors.New("boom")

	err := Try(func() error { Must(sentinel); return nil })
	if !errors.Is(err, sentinel) {
		t.Fatalf("Try(Must(sentinel)) = %v, want it to wrap sentinel", err)
	}
	if len(*lines) != 1 || !strings.Contains((*lines)[0], "errors_test.go:") {
		t.Fatalf("Must should log the caller's location, got %q", *lines)
	}

	*lines = nil
	v, err := TryValue(func() (int, error) { return MustValue(0, sentinel), nil })
	if !errors.Is(err, sentinel) || v != 0 {
		t.Fatalf("TryValue(MustValue) = %d, %v", v, err)
	}
	if len(*lines) != 1 || !strings.Contains((*lines)[0], "errors_test.go:") {
		t.Fatalf("MustValue should log the caller's location, got %q", *lines)
	}

	if MustValue(7, nil) != 7 {
		t.Error("MustValue success")
	}
	Must(nil)
	if len(*lines) != 1 {
		t.Error("Must(nil) logged")
	}
}

func TestErrorHandler(t *testing.T) {
	var lines []string
	h := NewErrorHandler().WithLogger(func(format string, args ...any) {
		lines = append(lines, fmt.Sprintf(format, args...))
	})
	sentinel := errors.New("handler boom")

	if err := Try(func() error { h.Must(sentinel); return nil }); !errors.Is(err, sentinel) {
		t.Fatalf("handler Must: %v", err)
	}
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "FATAL: ") || !strings.Contains(lines[0], "errors_test.go:") || !strings.Contains(lines[0], "handler boom") {
		t.Fatalf("handler log = %q", lines)
	}

	lines = nil
	if err := Try(func() error { h.Silent().Must(sentinel); return nil }); !errors.Is(err, sentinel) {
		t.Fatalf("silent Must: %v", err)
	}
	if len(lines) != 0 {
		t.Errorf("silent handler logged %q", lines)
	}

	SetDefaultSilent(true)
	defer SetDefaultSilent(false)
	if err := Try(func() error { Must(sentinel); return nil }); !errors.Is(err, sentinel) {
		t.Fatal("silent default Must")
	}
}

func TestTryPanicValues(t *testing.T) {
	err := Try(func() error { panic(fs.ErrNotExist) })
	if !errors.Is(err, fs.ErrNotExist) || !strings.HasPrefix(err.Error(), "panic recovered: ") {
		t.Errorf("panicked error: %v", err)
	}
	var pathErr *fs.PathError
	err = Try(func() error {
		_, statErr := os.Stat(filepath.Join(t.TempDir(), "missing"))
		panic(statErr)
	})
	if !errors.As(err, &pathErr) {
		t.Errorf("errors.As through a recovered panic: %v", err)
	}
	if err := Try(func() error { panic(42) }); err == nil || err.Error() != "panic recovered: 42" {
		t.Errorf("non-error panic: %v", err)
	}
	if err := Try(func() error {
		var items []int
		return fmt.Errorf("unreachable %d", items[len(items)])
	}); err == nil || !strings.Contains(err.Error(), "index out of range") {
		t.Errorf("runtime panic: %v", err)
	}
}

func TestCatchValueAndDefaults(t *testing.T) {
	handled := ""
	got := CatchValue(func() (string, error) { panic("kaboom") }, func(err error) string {
		handled = err.Error()
		return "fallback"
	})
	if got != "fallback" || !strings.Contains(handled, "kaboom") {
		t.Errorf("CatchValue on panic = %q (%q)", got, handled)
	}
	if got := CatchValue(func() (string, error) { return "ok", nil }, func(error) string { return "x" }); got != "ok" {
		t.Errorf("CatchValue success = %q", got)
	}
	calls := 0
	Catch(func() error { return nil }, func(error) { calls++ })
	if calls != 0 {
		t.Error("Catch called handler on success")
	}

	failure := errors.New("f")
	if Default(1, nil, 2) != 1 || Default(1, failure, 2) != 2 {
		t.Error("Default")
	}
	if IgnoreValue(3, failure) != 3 {
		t.Error("IgnoreValue")
	}
	Ignore(failure)

	if OrPanic("v", nil) != "v" {
		t.Error("OrPanic success")
	}
	if err := Try(func() error { OrPanic(0, failure); return nil }); !errors.Is(err, failure) {
		t.Errorf("OrPanic: %v", err)
	}
}

func TestOrLog(t *testing.T) {
	buf := captureStdLog(t)
	failure := errors.New("disk gone")

	if OrLog(5, nil) != 5 || OrLogDefault(5, nil, 9) != 5 || buf.Len() != 0 {
		t.Error("success paths must not log")
	}
	if OrLog(5, failure) != 0 {
		t.Error("OrLog should return the zero value")
	}
	if OrLogDefault(5, failure, 9) != 9 {
		t.Error("OrLogDefault should return the default")
	}
	out := buf.String()
	if strings.Count(out, "ERROR: ") != 2 || strings.Count(out, "errors_test.go:") != 2 || !strings.Contains(out, "disk gone") {
		t.Errorf("log output = %q", out)
	}
}

func TestChain(t *testing.T) {
	failure := errors.New("step 2")
	var ran []int
	c := NewChain().
		Do(func() error { ran = append(ran, 1); return nil }).
		DoValue(func() error { ran = append(ran, 2); return failure }).
		Do(func() error { ran = append(ran, 3); return nil })
	if len(ran) != 2 || c.Success() || !errors.Is(c.Error(), failure) || !errors.Is(c.Result(), failure) {
		t.Errorf("chain ran %v, err %v", ran, c.Error())
	}
	if ok := NewChain().Do(func() error { return nil }); !ok.Success() || ok.Error() != nil || ok.Result() != nil {
		t.Error("successful chain")
	}
}

func TestGetErrorInfo(t *testing.T) {
	if GetErrorInfo(nil) != nil {
		t.Error("GetErrorInfo(nil)")
	}
	info := GetErrorInfo(errors.New("bad"))
	if info == nil || filepath.Base(info.File) != "errors_test.go" || info.Line == 0 || !strings.HasSuffix(info.Function, "TestGetErrorInfo") {
		t.Fatalf("info = %+v", info)
	}
	s := info.String()
	if !strings.Contains(s, "errors_test.go:") || !strings.Contains(s, " in ") || !strings.HasSuffix(s, ": bad") {
		t.Errorf("String = %q", s)
	}
}
