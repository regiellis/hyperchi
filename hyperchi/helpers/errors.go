// Error handling helpers: Must and MustValue turn errors into panics, Try and
// Catch turn panics back into errors, and Chain runs steps until the first
// error.

package helpers

import (
	"fmt"
	"log"
	"runtime"
	"sync"
)

// ErrorHandler controls what Must does with a non-nil error: log it with the
// caller's file and line, then panic.
//
//	handler := NewErrorHandler().Silent().WithLogger(customLogger)
//	handler.Must(err) // panics without logging
//
// An ErrorHandler is safe for concurrent use; Silent and WithLogger may run
// while other goroutines call Must.
type ErrorHandler struct {
	mu     sync.RWMutex
	silent bool
	logger func(string, ...any)
}

// NewErrorHandler returns an ErrorHandler that logs with log.Printf before
// panicking.
func NewErrorHandler() *ErrorHandler {
	return &ErrorHandler{
		logger: log.Printf,
	}
}

// Silent makes Must panic without logging, for code that reports the panic
// at a higher level. It returns eh for chaining.
func (eh *ErrorHandler) Silent() *ErrorHandler {
	eh.setSilent(true)
	return eh
}

// setSilent sets whether Must logs before panicking.
func (eh *ErrorHandler) setSilent(silent bool) {
	eh.mu.Lock()
	defer eh.mu.Unlock()
	eh.silent = silent
}

// WithLogger sets the printf-style function Must logs through. A nil logger
// disables logging. It returns eh for chaining.
//
//	handler := NewErrorHandler().WithLogger(func(format string, args ...any) {
//		fmt.Fprintf(os.Stderr, "[ERROR] "+format+"\n", args...)
//	})
func (eh *ErrorHandler) WithLogger(logger func(string, ...any)) *ErrorHandler {
	eh.mu.Lock()
	defer eh.mu.Unlock()
	eh.logger = logger
	return eh
}

// Must panics with err if err is not nil. Unless the handler is silent, it
// first logs "FATAL: file:line - err" with the caller's location.
//
//	file, err := os.Open("nonexistent.txt")
//	handler.Must(err) // logs, then panics
func (eh *ErrorHandler) Must(err error) {
	eh.mustAt(2, err)
}

// mustAt implements Must. skip is the runtime.Caller depth of the user's call
// site, so the logged file and line point at the code that called Must.
func (eh *ErrorHandler) mustAt(skip int, err error) {
	if err == nil {
		return
	}
	eh.mu.RLock()
	silent, logger := eh.silent, eh.logger
	eh.mu.RUnlock()
	if !silent && logger != nil {
		_, file, line, _ := runtime.Caller(skip)
		logger("FATAL: %s:%d - %v", file, line, err)
	}
	panic(err)
}

// Must panics with err if err is not nil, logging the caller's file and line
// first through the package default handler (see SetDefaultLogger and
// SetDefaultSilent).
//
//	file, err := os.Open("config.json")
//	Must(err)
func Must(err error) {
	defaultErrorHandler.mustAt(2, err)
}

// MustValue returns value if err is nil and otherwise panics like Must. It
// takes a (value, error) call directly:
//
//	n := MustValue(strconv.Atoi("42"))
//	data := MustValue(os.ReadFile("config.json"))
func MustValue[T any](value T, err error) T {
	defaultErrorHandler.mustAt(2, err)
	return value
}

// Try calls fn and returns its error. A panic inside fn is recovered and
// returned as an error prefixed "panic recovered:"; a panicked error is
// wrapped, so errors.Is and errors.As still match it.
//
//	err := Try(func() error {
//		Must(riskyOperation())
//		return nil
//	})
func Try(fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = recoveredError(r)
		}
	}()
	return fn()
}

// recoveredError converts a recovered panic value to an error. A panicked
// error is wrapped, so errors.Is and errors.As still match it.
func recoveredError(r any) error {
	if err, ok := r.(error); ok {
		return fmt.Errorf("panic recovered: %w", err)
	}
	return fmt.Errorf("panic recovered: %v", r)
}

// TryValue calls fn and returns its result, recovering a panic as Try does.
// After a panic the result is the zero value.
//
//	text, err := TryValue(func() (string, error) {
//		data := MustValue(os.ReadFile("config.json"))
//		return string(data), nil
//	})
func TryValue[T any](fn func() (T, error)) (result T, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = recoveredError(r)
		}
	}()
	return fn()
}

// Catch calls fn through Try and passes any error, including a recovered
// panic, to handler.
//
//	Catch(func() error {
//		return processFile("important.txt")
//	}, func(err error) {
//		log.Printf("process file: %v", err)
//	})
func Catch(fn func() error, handler func(error)) {
	if err := Try(fn); err != nil {
		handler(err)
	}
}

// CatchValue calls fn through TryValue and returns its value. On an error or
// a panic it returns handler's value instead.
//
//	cfg := CatchValue(func() (Config, error) {
//		return loadConfig("app.json")
//	}, func(err error) Config {
//		log.Printf("using default config: %v", err)
//		return defaultConfig()
//	})
func CatchValue[T any](fn func() (T, error), handler func(error) T) T {
	value, err := TryValue(fn)
	if err != nil {
		return handler(err)
	}
	return value
}

// Ignore discards err. It marks an error as deliberately unchecked, such as
// the result of a best-effort cleanup.
//
//	Ignore(tempFile.Close())
//	Ignore(os.Remove(tempFile.Name()))
func Ignore(err error) {
	_ = err
}

// IgnoreValue returns value and discards err. The value is whatever the
// callee returned, usually the zero value on failure.
//
//	hostname := IgnoreValue(os.Hostname())
func IgnoreValue[T any](value T, err error) T {
	Ignore(err)
	return value
}

// Default returns value if err is nil, otherwise defaultValue.
//
//	port := Default(portValue, portErr, 8080)
func Default[T any](value T, err error, defaultValue T) T {
	if err != nil {
		return defaultValue
	}
	return value
}

// OrPanic returns value if err is nil and otherwise panics with err. Unlike
// MustValue it never logs.
//
//	db := OrPanic(sql.Open("sqlite3", dsn))
func OrPanic[T any](value T, err error) T {
	if err != nil {
		panic(err)
	}
	return value
}

// OrLog returns value if err is nil. Otherwise it logs err with the caller's
// file and line through log.Printf and returns the zero value of T.
//
//	count := OrLog(strconv.Atoi(userInput))
func OrLog[T any](value T, err error) T {
	if err != nil {
		_, file, line, _ := runtime.Caller(1)
		log.Printf("ERROR: %s:%d - %v", file, line, err)
		var zero T
		return zero
	}
	return value
}

// OrLogDefault returns value if err is nil. Otherwise it logs err with the
// caller's file and line through log.Printf and returns defaultValue.
//
//	port := OrLogDefault(portValue, portErr, 8080)
func OrLogDefault[T any](value T, err error, defaultValue T) T {
	if err != nil {
		_, file, line, _ := runtime.Caller(1)
		log.Printf("ERROR: %s:%d - %v", file, line, err)
		return defaultValue
	}
	return value
}

// Chain runs a sequence of steps and stops at the first error, which it
// keeps. A Chain is not safe for concurrent use.
//
//	chain := NewChain().
//		Do(func() error { return createDirectory(dir) }).
//		Do(func() error { return writeConfigFile(dir) }).
//		Do(func() error { return startService() })
//	if err := chain.Error(); err != nil {
//		log.Fatalf("setup: %v", err)
//	}
type Chain struct {
	err error
}

// NewChain returns an empty Chain with no error.
func NewChain() *Chain {
	return &Chain{}
}

// Do calls fn unless an earlier step failed. An error from fn becomes the
// chain's error and skips every later step. It returns c for chaining.
func (c *Chain) Do(fn func() error) *Chain {
	if c.err == nil {
		c.err = fn()
	}
	return c
}

// DoValue is the same as Do.
func (c *Chain) DoValue(fn func() error) *Chain {
	return c.Do(fn)
}

// Error returns the first error a step returned, or nil if every step
// succeeded.
func (c *Chain) Error() error {
	return c.err
}

// Success reports whether every step so far succeeded.
func (c *Chain) Success() bool {
	return c.err == nil
}

// Result is the same as Error.
func (c *Chain) Result() error {
	return c.err
}

// ErrorInfo pairs an error with the file, line, and function where
// GetErrorInfo was called.
type ErrorInfo struct {
	Err      error  // The original error
	File     string // File path where GetErrorInfo was called
	Line     int    // Line number where GetErrorInfo was called
	Function string // Function name where GetErrorInfo was called
}

// GetErrorInfo records err with the caller's file, line, and function name.
// It returns nil when err is nil. The function name is "unknown" when the
// runtime cannot resolve it.
//
//	if err != nil {
//		log.Print(GetErrorInfo(err))
//		// /path/to/main.go:15 in main.loadConfig: open config.json: no such file or directory
//	}
func GetErrorInfo(err error) *ErrorInfo {
	if err == nil {
		return nil
	}

	pc, file, line, _ := runtime.Caller(1)
	fn := runtime.FuncForPC(pc)
	fnName := "unknown"
	if fn != nil {
		fnName = fn.Name()
	}

	return &ErrorInfo{
		Err:      err,
		File:     file,
		Line:     line,
		Function: fnName,
	}
}

// String formats the error as "file:line in function: error".
func (ei *ErrorInfo) String() string {
	return fmt.Sprintf("%s:%d in %s: %v", ei.File, ei.Line, ei.Function, ei.Err)
}

// defaultErrorHandler backs the package-level Must and MustValue.
// SetDefaultLogger and SetDefaultSilent configure it under its lock, so they
// may run while other goroutines call Must.
var defaultErrorHandler = NewErrorHandler()

// SetDefaultLogger sets the printf-style function the package-level Must and
// MustValue log through. It is safe to call while other goroutines call Must.
//
//	SetDefaultLogger(func(format string, args ...any) {
//		fmt.Fprintf(os.Stderr, "[FATAL] "+format+"\n", args...)
//	})
func SetDefaultLogger(logger func(string, ...any)) {
	defaultErrorHandler.WithLogger(logger)
}

// SetDefaultSilent sets whether the package-level Must and MustValue skip
// logging before they panic. The default is false: they log.
func SetDefaultSilent(silent bool) {
	defaultErrorHandler.setSilent(silent)
}
