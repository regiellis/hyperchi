package helpers

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// LogLevel is a Logger severity. A Logger writes messages at or above its
// level.
type LogLevel int

// Log levels from least to most severe. FATAL exits the process after the
// message is written.
const (
	DEBUG LogLevel = iota
	INFO
	WARN
	ERROR
	FATAL
)

// String returns the level name, such as "WARN", or "UNKNOWN".
func (l LogLevel) String() string {
	switch l {
	case DEBUG:
		return "DEBUG"
	case INFO:
		return "INFO"
	case WARN:
		return "WARN"
	case ERROR:
		return "ERROR"
	case FATAL:
		return "FATAL"
	default:
		return "UNKNOWN"
	}
}

// Color returns the ANSI escape that colors the level name, or the reset
// code for an unknown level.
func (l LogLevel) Color() string {
	switch l {
	case DEBUG:
		return "\033[36m" // Cyan
	case INFO:
		return "\033[32m" // Green
	case WARN:
		return "\033[33m" // Yellow
	case ERROR:
		return "\033[31m" // Red
	case FATAL:
		return "\033[35m" // Magenta
	default:
		return "\033[0m" // Reset
	}
}

// Logger writes leveled, printf-style log lines of the form
// "2006-01-02 15:04:05 [LEVEL] [prefix] file:line message". A Logger from
// NewLogger is safe for concurrent use: the setters may run while other
// goroutines log.
type Logger struct {
	level      LogLevel
	output     io.Writer
	colored    bool
	showCaller bool
	prefix     string
	// mu guards the fields above and serializes writes. Loggers derived with
	// WithField share it, since they usually share the output writer.
	mu *sync.Mutex
}

// lock acquires l.mu and returns its release. A Logger built as a struct
// literal has no mutex and is not synchronized.
func (l *Logger) lock() func() {
	if l.mu == nil {
		return func() {}
	}
	l.mu.Lock()
	return l.mu.Unlock
}

// NewLogger returns a Logger that writes INFO and above to os.Stdout with
// colored level names and no caller information.
func NewLogger() *Logger {
	return &Logger{
		level:      INFO,
		output:     os.Stdout,
		colored:    true,
		showCaller: false,
		mu:         &sync.Mutex{},
	}
}

// SetLevel sets the lowest level the logger writes.
func (l *Logger) SetLevel(level LogLevel) *Logger {
	defer l.lock()()
	l.level = level
	return l
}

// SetOutput sets the writer log lines go to.
func (l *Logger) SetOutput(w io.Writer) *Logger {
	defer l.lock()()
	l.output = w
	return l
}

// SetColored turns ANSI colors on the level name on or off.
func (l *Logger) SetColored(colored bool) *Logger {
	defer l.lock()()
	l.colored = colored
	return l
}

// SetShowCaller turns the caller's file:line on or off.
func (l *Logger) SetShowCaller(show bool) *Logger {
	defer l.lock()()
	l.showCaller = show
	return l
}

// SetPrefix sets text written in brackets after the level on every line.
func (l *Logger) SetPrefix(prefix string) *Logger {
	defer l.lock()()
	l.prefix = prefix
	return l
}

// log is the internal logging method. skip is the runtime.Caller depth of
// the user's call site (2 when called directly from an exported function), so
// caller information points at application code rather than this package.
func (l *Logger) log(skip int, level LogLevel, format string, args ...any) {
	// Read the settings under the lock, then format outside it, so an
	// argument whose String method logs through this logger cannot deadlock.
	cfg := l.snapshot()
	if level < cfg.level {
		return
	}

	var parts []string

	timestamp := time.Now().Format("2006-01-02 15:04:05")
	parts = append(parts, timestamp)

	levelStr := level.String()
	if cfg.colored {
		levelStr = fmt.Sprintf("%s%s\033[0m", level.Color(), levelStr)
	}
	parts = append(parts, fmt.Sprintf("[%s]", levelStr))

	if cfg.prefix != "" {
		parts = append(parts, fmt.Sprintf("[%s]", cfg.prefix))
	}

	if cfg.showCaller {
		_, file, line, ok := runtime.Caller(skip)
		if ok {
			if idx := strings.LastIndex(file, "/"); idx != -1 {
				file = file[idx+1:]
			}
			parts = append(parts, fmt.Sprintf("%s:%d", file, line))
		}
	}

	message := fmt.Sprintf(format, args...)
	parts = append(parts, message)

	// A failed write has no other log to go to, so it is reported on stderr.
	logLine := strings.Join(parts, " ") + "\n"
	unlock := l.lock()
	_, err := cfg.output.Write([]byte(logLine))
	unlock()
	if err != nil {
		fmt.Fprintf(os.Stderr, "hyperchi: failed to write log line: %v\n", err)
	}

	if level == FATAL {
		os.Exit(1)
	}
}

// Debug logs a formatted message at DEBUG level.
func (l *Logger) Debug(format string, args ...any) {
	l.log(2, DEBUG, format, args...)
}

// Info logs a formatted message at INFO level.
func (l *Logger) Info(format string, args ...any) {
	l.log(2, INFO, format, args...)
}

// Warn logs a formatted message at WARN level.
func (l *Logger) Warn(format string, args ...any) {
	l.log(2, WARN, format, args...)
}

// Error logs a formatted message at ERROR level.
func (l *Logger) Error(format string, args ...any) {
	l.log(2, ERROR, format, args...)
}

// Fatal logs a formatted message at FATAL level, then exits the process
// with status 1. Deferred functions do not run.
func (l *Logger) Fatal(format string, args ...any) {
	l.log(2, FATAL, format, args...)
}

// WithField returns a copy of the logger whose prefix gains "key=value".
// The copy shares the original's lock and output.
func (l *Logger) WithField(key, value string) *Logger {
	newLogger := l.snapshot()
	if newLogger.prefix == "" {
		newLogger.prefix = fmt.Sprintf("%s=%s", key, value)
	} else {
		newLogger.prefix = fmt.Sprintf("%s %s=%s", newLogger.prefix, key, value)
	}
	return &newLogger
}

// snapshot copies l's settings under its lock. The copy shares l's mutex.
func (l *Logger) snapshot() Logger {
	defer l.lock()()
	return Logger{
		level:      l.level,
		output:     l.output,
		colored:    l.colored,
		showCaller: l.showCaller,
		prefix:     l.prefix,
		mu:         l.mu,
	}
}

// WithFields returns a copy of the logger whose prefix gains "key=value"
// for each field, sorted by key. The copy shares the original's lock and
// output.
func (l *Logger) WithFields(fields map[string]string) *Logger {
	newLogger := l.snapshot()
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fieldStrs := make([]string, 0, len(keys))
	for _, k := range keys {
		fieldStrs = append(fieldStrs, fmt.Sprintf("%s=%s", k, fields[k]))
	}
	fieldsStr := strings.Join(fieldStrs, " ")
	if newLogger.prefix == "" {
		newLogger.prefix = fieldsStr
	} else {
		newLogger.prefix = fmt.Sprintf("%s %s", newLogger.prefix, fieldsStr)
	}
	return &newLogger
}

// defaultLogger backs the package-level logging functions. Its setters are
// synchronized, so it may be reconfigured while other goroutines log.
var defaultLogger = NewLogger()

// Debug logs a formatted message at DEBUG level through the default logger.
func Debug(format string, args ...any) {
	defaultLogger.log(2, DEBUG, format, args...)
}

// Info logs a formatted message at INFO level through the default logger.
func Info(format string, args ...any) {
	defaultLogger.log(2, INFO, format, args...)
}

// Warn logs a formatted message at WARN level through the default logger.
func Warn(format string, args ...any) {
	defaultLogger.log(2, WARN, format, args...)
}

// Error logs a formatted message at ERROR level through the default logger.
func Error(format string, args ...any) {
	defaultLogger.log(2, ERROR, format, args...)
}

// Fatal logs a formatted message at FATAL level through the default logger,
// then exits the process with status 1.
func Fatal(format string, args ...any) {
	defaultLogger.log(2, FATAL, format, args...)
}

// SetLogLevel sets the lowest level the package-level functions write.
func SetLogLevel(level LogLevel) {
	defaultLogger.SetLevel(level)
}

// SetLogOutput sets the writer the package-level functions write to.
func SetLogOutput(w io.Writer) {
	defaultLogger.SetOutput(w)
}

// SetLogColored turns colored level names on or off for the package-level
// functions.
func SetLogColored(colored bool) {
	defaultLogger.SetColored(colored)
}

// SetLogShowCaller turns caller file:line on or off for the package-level
// functions.
func SetLogShowCaller(show bool) {
	defaultLogger.SetShowCaller(show)
}

// LoggerConfig holds the settings ConfigureLogger applies. A nil Output and
// an empty Prefix keep the current values; a zero Level is DEBUG.
type LoggerConfig struct {
	Level      LogLevel
	Output     io.Writer
	Colored    bool
	ShowCaller bool
	Prefix     string
}

// ConfigureLogger applies config to the logger behind the package-level
// functions. The settings apply together: a concurrent log line sees all of
// them or none.
func ConfigureLogger(config LoggerConfig) {
	defer defaultLogger.lock()()
	if config.Output != nil {
		defaultLogger.output = config.Output
	}
	defaultLogger.level = config.Level
	defaultLogger.colored = config.Colored
	defaultLogger.showCaller = config.ShowCaller
	if config.Prefix != "" {
		defaultLogger.prefix = config.Prefix
	}
}

// SetupDevelopmentLogging makes the package-level functions write DEBUG and
// above to stdout, colored, with caller information.
func SetupDevelopmentLogging() {
	ConfigureLogger(LoggerConfig{
		Level:      DEBUG,
		Output:     os.Stdout,
		Colored:    true,
		ShowCaller: true,
	})
}

// SetupProductionLogging makes the package-level functions write INFO and
// above to stdout, uncolored, without caller information.
func SetupProductionLogging() {
	ConfigureLogger(LoggerConfig{
		Level:      INFO,
		Output:     os.Stdout,
		Colored:    false,
		ShowCaller: false,
	})
}

// SetupTestLogging discards all package-level log output.
func SetupTestLogging() {
	ConfigureLogger(LoggerConfig{
		Level:      ERROR,
		Output:     io.Discard,
		Colored:    false,
		ShowCaller: false,
	})
}

// Print logs its arguments, joined as fmt.Sprint does, at INFO level.
func Print(args ...any) {
	defaultLogger.log(2, INFO, "%s", fmt.Sprint(args...))
}

// Printf logs a formatted message at INFO level. It is the same as Info.
func Printf(format string, args ...any) {
	defaultLogger.log(2, INFO, format, args...)
}

// LogError logs err at ERROR level as "Error (context...): err". A nil err
// logs nothing.
func LogError(err error, context ...string) {
	if err == nil {
		return
	}

	contextStr := ""
	if len(context) > 0 {
		contextStr = " (" + strings.Join(context, ", ") + ")"
	}

	defaultLogger.log(2, ERROR, "Error%s: %v", contextStr, err)
}

// LogErrorf logs err at ERROR level with a formatted context, as
// "Error (context): err". A nil err logs nothing.
func LogErrorf(err error, format string, args ...any) {
	if err == nil {
		return
	}

	context := fmt.Sprintf(format, args...)
	defaultLogger.log(2, ERROR, "Error (%s): %v", context, err)
}

// Log logs its arguments, joined as fmt.Sprint does, at DEBUG level.
//
// Deprecated: Use Debug.
func Log(args ...any) {
	defaultLogger.log(2, DEBUG, "%s", fmt.Sprint(args...))
}

// Logf logs a formatted message at DEBUG level.
//
// Deprecated: Use Debug.
func Logf(format string, args ...any) {
	defaultLogger.log(2, DEBUG, format, args...)
}
