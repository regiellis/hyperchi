package logging

import (
	"bytes"
	"testing"
	"time"
)

func TestTimeFormatShapesRecordTime(t *testing.T) {
	for _, format := range []string{time.DateOnly, time.Kitchen, "2006-01-02T15:04:05Z07:00"} {
		t.Run(format, func(t *testing.T) {
			var buf bytes.Buffer
			cfg := DefaultConfig()
			cfg.Output = &buf
			cfg.TimeFormat = format
			NewLogger(cfg).Info("hello")
			got, ok := decodeLines(t, &buf)[0]["time"].(string)
			if !ok {
				t.Fatal("record has no string time")
			}
			if _, err := time.Parse(format, got); err != nil {
				t.Fatalf("time %q does not match TimeFormat %q: %v", got, format, err)
			}
		})
	}
}

// An empty TimeFormat keeps slog's own time, an RFC 3339 timestamp with
// fractional seconds.
func TestEmptyTimeFormatKeepsSlogTime(t *testing.T) {
	var buf bytes.Buffer
	cfg := DefaultConfig()
	cfg.Output = &buf
	cfg.TimeFormat = ""
	NewLogger(cfg).Info("hello")
	got, ok := decodeLines(t, &buf)[0]["time"].(string)
	if !ok {
		t.Fatal("record has no string time")
	}
	if _, err := time.Parse(time.RFC3339Nano, got); err != nil {
		t.Fatalf("time %q is not RFC 3339: %v", got, err)
	}
}
