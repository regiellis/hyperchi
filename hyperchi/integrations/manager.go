// Package integrations manages optional application capabilities without importing
// their third-party dependencies. Register before starting; drain requests before closing.
package integrations

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync"
	"time"
)

// Renderer writes escaped HTML. The manager buffers output so render failures
// never publish partial HTML. Implementations must support concurrent requests.
type Renderer interface {
	Render(context.Context, io.Writer, any) error
}

// Starter initializes a capability before requests are accepted. The context
// bounds startup work; it is not a lifetime context for background workers.
type Starter interface{ Start(context.Context) error }

// Closer releases resources after requests have drained, including partial startup.
type Closer interface{ Close(context.Context) error }

// HealthChecker checks readiness using the caller's deadline.
type HealthChecker interface{ Health(context.Context) error }

// Validator checks configuration without allocating long-lived resources.
type Validator interface{ Validate() error }

// Errors the Manager returns, for matching with errors.Is.
var (
	ErrNotFound   = errors.New("integration not found")
	ErrNotStarted = errors.New("integrations not started")
	ErrClosed     = errors.New("integrations closed")
)

// Info describes one integration's capabilities without exposing its
// configuration or credentials. Lifecycle means it is a Starter or a Closer.
type Info struct {
	Name                        string
	Renderer, Lifecycle, Health bool
}
type entry struct {
	name  string
	value any
}
type state uint8

const (
	configuring state = iota
	starting
	running
	closing
	closed
)

// Manager owns registered integrations. Its zero value is ready for registration.
// Lifecycle calls are serialized; hooks may inspect the registry but must not
// call Start or Close recursively. Applications drain users before calling Close.
type Manager struct {
	lifecycle sync.Mutex
	mu        sync.RWMutex
	entries   []entry
	state     state
	closeErr  error
}

// New creates an empty application-scoped manager.
func New() *Manager { return &Manager{} }

// Register validates a named capability and transfers lifecycle ownership to the
// manager on success. It rejects an empty name or one with surrounding
// whitespace, a nil value, a value with none of the Renderer, Starter, Closer,
// or HealthChecker methods, a failed Validate, a duplicate name, and any
// registration after Start or Close. Integration constructors retain their own
// typed configuration.
func (m *Manager) Register(name string, value any) error {
	if name == "" || strings.TrimSpace(name) != name {
		return fmt.Errorf("integration name must be nonempty without surrounding whitespace")
	}
	if value == nil {
		return fmt.Errorf("integration %q is nil", name)
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Func, reflect.Interface, reflect.Chan:
		if v.IsNil() {
			return fmt.Errorf("integration %q is nil", name)
		}
	}
	_, renderer := value.(Renderer)
	_, starter := value.(Starter)
	_, closer := value.(Closer)
	_, health := value.(HealthChecker)
	if !renderer && !starter && !closer && !health {
		return fmt.Errorf("integration %q has no supported capability", name)
	}
	if validator, ok := value.(Validator); ok {
		if err := validator.Validate(); err != nil {
			return fmt.Errorf("validate integration %q: %w", name, err)
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state != configuring {
		return fmt.Errorf("register %q: registration is frozen after start or close", name)
	}
	for _, e := range m.entries {
		if e.name == name {
			return fmt.Errorf("integration %q already registered", name)
		}
	}
	m.entries = append(m.entries, entry{name, value})
	return nil
}

// Lookup retrieves a registered integration for use through its native typed API.
// It does not initialize the integration. Prefer Get for a checked type assertion.
func (m *Manager) Lookup(name string) (any, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, e := range m.entries {
		if e.name == name {
			return e.value, nil
		}
	}
	return nil, fmt.Errorf("%w: %s", ErrNotFound, name)
}

// Get retrieves an integration with a checked type assertion, including interfaces.
func Get[T any](m *Manager, name string) (T, error) {
	var zero T
	value, err := m.Lookup(name)
	if err != nil {
		return zero, err
	}
	typed, ok := value.(T)
	if !ok {
		return zero, fmt.Errorf("integration %q does not implement requested type", name)
	}
	return typed, nil
}

// List returns a registration-order snapshot of names and supported capabilities.
func (m *Manager) List() []Info {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]Info, 0, len(m.entries))
	for _, e := range m.entries {
		_, r := e.value.(Renderer)
		_, s := e.value.(Starter)
		_, c := e.value.(Closer)
		_, h := e.value.(HealthChecker)
		result = append(result, Info{e.name, r, s || c, h})
	}
	return result
}

// Start initializes integrations in registration order and freezes registration.
// A failure closes all registered resources in reverse order, including the
// failing integration, with a fresh ten-second cleanup deadline. Failed managers
// cannot restart; construct a new one to retry. Repeated successful starts are no-ops.
func (m *Manager) Start(ctx context.Context) error {
	m.lifecycle.Lock()
	defer m.lifecycle.Unlock()
	m.mu.Lock()
	if m.state == running {
		m.mu.Unlock()
		return nil
	}
	if m.state != configuring {
		m.mu.Unlock()
		return ErrClosed
	}
	m.state = starting
	entries := append([]entry(nil), m.entries...)
	m.mu.Unlock()
	var startErr error
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			startErr = err
			break
		}
		if s, ok := e.value.(Starter); ok {
			if err := s.Start(ctx); err != nil {
				startErr = fmt.Errorf("start integration %q: %w", e.name, err)
				break
			}
		}
	}
	if startErr == nil {
		startErr = ctx.Err()
	}
	if startErr != nil {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		closeErr := closeEntries(cleanup, entries)
		m.mu.Lock()
		m.state = closed
		m.closeErr = closeErr
		m.mu.Unlock()
		return errors.Join(startErr, closeErr)
	}
	m.mu.Lock()
	m.state = running
	m.mu.Unlock()
	return nil
}

// Close releases all registered resources in reverse order, even if Start was
// never called. All closers are attempted and their errors joined. Close is
// idempotent; subsequent calls return the original outcome. Call after draining
// requests. Hooks must honor the supplied context; they are not forcibly killed.
func (m *Manager) Close(ctx context.Context) error {
	m.lifecycle.Lock()
	defer m.lifecycle.Unlock()
	m.mu.Lock()
	if m.state == closed {
		err := m.closeErr
		m.mu.Unlock()
		return err
	}
	m.state = closing
	entries := append([]entry(nil), m.entries...)
	m.mu.Unlock()
	err := closeEntries(ctx, entries)
	m.mu.Lock()
	m.state = closed
	m.closeErr = err
	m.mu.Unlock()
	return err
}
func closeEntries(ctx context.Context, entries []entry) error {
	var errs []error
	for i := len(entries) - 1; i >= 0; i-- {
		if c, ok := entries[i].value.(Closer); ok {
			if err := c.Close(ctx); err != nil {
				errs = append(errs, fmt.Errorf("close integration %q: %w", entries[i].name, err))
			}
		}
	}
	return errors.Join(errs...)
}

func (m *Manager) ready() error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.state == running {
		return nil
	}
	if m.state == closing || m.state == closed {
		return ErrClosed
	}
	return ErrNotStarted
}

// Render produces a complete HTML response body or an error. No partial body is
// returned on failure. The request context reaches the adapter unchanged.
func (m *Manager) Render(ctx context.Context, name string, data any) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := m.ready(); err != nil {
		return nil, err
	}
	renderer, err := Get[Renderer](m, name)
	if err != nil {
		return nil, err
	}
	var body bytes.Buffer
	if err := renderer.Render(ctx, &body, data); err != nil {
		return nil, fmt.Errorf("render integration %q: %w", name, err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return body.Bytes(), nil
}

// Health checks every integration in registration order. Entries without a
// HealthChecker are ready when the manager is running. The caller chooses which
// results to expose publicly; error messages may contain infrastructure details.
func (m *Manager) Health(ctx context.Context) map[string]error {
	m.mu.RLock()
	entries := append([]entry(nil), m.entries...)
	m.mu.RUnlock()
	result := make(map[string]error, len(entries))
	readyErr := m.ready()
	for _, e := range entries {
		if readyErr != nil {
			result[e.name] = readyErr
			continue
		}
		if err := ctx.Err(); err != nil {
			result[e.name] = err
			continue
		}
		if h, ok := e.value.(HealthChecker); ok {
			result[e.name] = h.Health(ctx)
		} else {
			result[e.name] = nil
		}
	}
	return result
}
