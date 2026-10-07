package integrations

import (
	"context"
	"errors"
	"io"
	"reflect"
	"sync"
	"testing"
)

type testIntegration struct {
	name                          string
	events                        *[]string
	startErr, closeErr, healthErr error
}

func (p *testIntegration) Start(context.Context) error {
	*p.events = append(*p.events, "start "+p.name)
	return p.startErr
}
func (p *testIntegration) Close(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	*p.events = append(*p.events, "close "+p.name)
	return p.closeErr
}
func (p *testIntegration) Health(context.Context) error { return p.healthErr }

type renderFunc func(context.Context, io.Writer, any) error

func (f renderFunc) Render(c context.Context, w io.Writer, v any) error { return f(c, w, v) }

func TestLifecycle(t *testing.T) {
	events := []string{}
	m := New()
	for _, name := range []string{"db", "views"} {
		if err := m.Register(name, &testIntegration{name: name, events: &events}); err != nil {
			t.Fatal(err)
		}
	}
	if !errors.Is(m.Health(context.Background())["db"], ErrNotStarted) {
		t.Fatal("health before startup")
	}
	for range 2 {
		if err := m.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.Register("late", renderFunc(func(context.Context, io.Writer, any) error { return nil })); err == nil {
		t.Fatal("late registration accepted")
	}
	for _, err := range m.Health(context.Background()) {
		if err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if err := m.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"start db", "start views", "close views", "close db"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("%v", events)
	}
	if !errors.Is(m.Start(context.Background()), ErrClosed) {
		t.Fatal("restarted closed manager")
	}
}

func TestStartupRollback(t *testing.T) {
	events := []string{}
	m := New()
	failure := errors.New("unavailable")
	closeFailure := errors.New("close failed")
	for _, p := range []*testIntegration{{name: "first", events: &events, closeErr: closeFailure}, {name: "second", events: &events, startErr: failure}, {name: "third", events: &events}} {
		if err := m.Register(p.name, p); err != nil {
			t.Fatal(err)
		}
	}
	err := m.Start(context.Background())
	if !errors.Is(err, failure) || !errors.Is(err, closeFailure) {
		t.Fatal(err)
	}
	want := []string{"start first", "start second", "close third", "close second", "close first"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("%v", events)
	}
	if !errors.Is(m.Close(context.Background()), closeFailure) {
		t.Fatal("lost close error")
	}
}

func TestCancelledStartupStillCleansUp(t *testing.T) {
	m := New()
	events := []string{}
	if err := m.Register("one", &testIntegration{name: "one", events: &events}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if !errors.Is(m.Start(ctx), context.Canceled) {
		t.Fatal("missing cancellation")
	}
	if !reflect.DeepEqual(events, []string{"close one"}) {
		t.Fatal(events)
	}
}

func TestRegistrationAndLookup(t *testing.T) {
	m := New()
	var nilPointer *testIntegration
	for _, value := range []any{nil, nilPointer, 42} {
		if err := m.Register("bad", value); err == nil {
			t.Fatalf("accepted %T", value)
		}
	}
	renderer := renderFunc(func(context.Context, io.Writer, any) error { return nil })
	if err := m.Register("", renderer); err == nil {
		t.Fatal("empty name accepted")
	}
	if err := m.Register("view", renderer); err != nil {
		t.Fatal(err)
	}
	if err := m.Register("view", renderer); err == nil {
		t.Fatal("duplicate accepted")
	}
	if _, err := Get[Renderer](m, "view"); err != nil {
		t.Fatal(err)
	}
	if _, err := Get[Starter](m, "view"); err == nil {
		t.Fatal("wrong type accepted")
	}
	if _, err := m.Lookup("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	infos := m.List()
	infos[0].Name = "changed"
	if m.List()[0].Name != "view" {
		t.Fatal("not a snapshot")
	}
}

func TestRenderBufferContextAndConcurrency(t *testing.T) {
	m := New()
	failure := errors.New("render failed")
	type requestKey struct{}
	key := requestKey{}
	ctx := context.WithValue(context.Background(), key, "request")
	if err := m.Register("view", renderFunc(func(c context.Context, w io.Writer, data any) error {
		if c.Value(key) != "request" {
			return errors.New("context lost")
		}
		if _, err := io.WriteString(w, "<p>content</p>"); err != nil {
			return err
		}
		if data == "fail" {
			return failure
		}
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Render(ctx, "view", nil); !errors.Is(err, ErrNotStarted) {
		t.Fatal(err)
	}
	if err := m.Start(ctx); err != nil {
		t.Fatal(err)
	}
	body, err := m.Render(ctx, "view", "fail")
	if body != nil || !errors.Is(err, failure) {
		t.Fatalf("partial body: %q, %v", body, err)
	}
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			body, err := m.Render(ctx, "view", nil)
			if err != nil || string(body) != "<p>content</p>" {
				t.Errorf("%q %v", body, err)
			}
			_ = m.List()
		})
	}
	wg.Wait()
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := m.Render(cancelled, "view", nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := m.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Render(ctx, "view", nil); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
}

func TestHooksAndTypedRenderer(t *testing.T) {
	failure := errors.New("bad config")
	m := New()
	if err := m.Register("bad", Hooks{ValidateFunc: func() error { return failure }}); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if len(m.List()) != 0 {
		t.Fatal("invalid registration published")
	}
	if err := m.Register("service", Hooks{HealthFunc: func(context.Context) error { return failure }}); err != nil {
		t.Fatal(err)
	}
	typed := TypedRenderer(func(_ context.Context, w io.Writer, text string) error { _, err := io.WriteString(w, text); return err })
	if err := m.Register("typed", typed); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(m.Health(context.Background())["service"], failure) {
		t.Fatal("lost health failure")
	}
	if _, err := m.Render(context.Background(), "typed", 42); err == nil {
		t.Fatal("wrong renderer data accepted")
	}
	body, err := m.Render(context.Background(), "typed", "ready")
	if err != nil || string(body) != "ready" {
		t.Fatal(string(body), err)
	}
	if err := m.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}
