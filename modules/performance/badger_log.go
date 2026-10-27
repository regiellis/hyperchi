package performance

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/dgraph-io/badger/v4"
)

// slogBadgerLogger is a badger.Logger that writes to an slog.Logger, dropping
// lines below level. Badger logs routine INFO lines on open and close, so the
// default level is slog.LevelWarn.
type slogBadgerLogger struct {
	logger *slog.Logger
	level  slog.Leveler
}

var _ badger.Logger = slogBadgerLogger{}

// newSlogBadgerLogger returns the adapter for logger (nil uses slog.Default)
// at level (nil means slog.LevelWarn).
func newSlogBadgerLogger(logger *slog.Logger, level slog.Leveler) slogBadgerLogger {
	if logger == nil {
		logger = slog.Default()
	}
	if level == nil {
		level = slog.LevelWarn
	}
	return slogBadgerLogger{logger: logger, level: level}
}

func (l slogBadgerLogger) Errorf(format string, args ...any) {
	l.log(slog.LevelError, format, args)
}

func (l slogBadgerLogger) Warningf(format string, args ...any) {
	l.log(slog.LevelWarn, format, args)
}

func (l slogBadgerLogger) Infof(format string, args ...any) {
	l.log(slog.LevelInfo, format, args)
}

func (l slogBadgerLogger) Debugf(format string, args ...any) {
	l.log(slog.LevelDebug, format, args)
}

// log formats the line only when it will be written. Badger ends most lines
// with a newline, which slog does not need.
func (l slogBadgerLogger) log(level slog.Level, format string, args []any) {
	ctx := context.Background()
	if level < l.level.Level() || !l.logger.Enabled(ctx, level) {
		return
	}
	l.logger.Log(ctx, level, strings.TrimSpace(fmt.Sprintf(format, args...)), "component", "badger")
}
