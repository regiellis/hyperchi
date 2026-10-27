package integrations

import (
	"context"
	"fmt"
	"io"
)

// RenderFunc adapts a rendering function without requiring a new struct.
// Its output must be escaped HTML; response buffering belongs to Manager.
type RenderFunc func(context.Context, io.Writer, any) error

// Render calls the underlying function with the request context and data.
func (f RenderFunc) Render(ctx context.Context, w io.Writer, data any) error { return f(ctx, w, data) }

// TypedRenderer adapts an existing typed rendering function. Invalid data types
// return an error instead of panicking. A nil function is rejected by registration.
func TypedRenderer[T any](fn func(context.Context, io.Writer, T) error) RenderFunc {
	if fn == nil {
		return nil
	}
	return func(ctx context.Context, w io.Writer, data any) error {
		value, ok := data.(T)
		if !ok {
			return fmt.Errorf("renderer expected %T, got %T", *new(T), data)
		}
		return fn(ctx, w, value)
	}
}

// Hooks adapts existing service lifecycle functions. Omitted hooks are no-ops.
// Keep the original typed client in your handler closure; register Hooks to let
// the manager own its lifecycle without turning it into an untyped service bag.
// Configure hooks once before registration and do not mutate them while serving.
type Hooks struct {
	ValidateFunc func() error
	StartFunc    func(context.Context) error
	CloseFunc    func(context.Context) error
	HealthFunc   func(context.Context) error
}

// Validate checks the supplied service configuration when a validator is present.
func (h Hooks) Validate() error {
	if h.ValidateFunc != nil {
		return h.ValidateFunc()
	}
	return nil
}

// Start initializes the service when a startup hook is present.
func (h Hooks) Start(ctx context.Context) error {
	if h.StartFunc != nil {
		return h.StartFunc(ctx)
	}
	return nil
}

// Close releases the service when a cleanup hook is present.
func (h Hooks) Close(ctx context.Context) error {
	if h.CloseFunc != nil {
		return h.CloseFunc(ctx)
	}
	return nil
}

// Health checks service readiness when a health hook is present.
func (h Hooks) Health(ctx context.Context) error {
	if h.HealthFunc != nil {
		return h.HealthFunc(ctx)
	}
	return nil
}
