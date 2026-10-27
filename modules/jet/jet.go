// Package jetadapter connects a configured Jet template set to HyperChi integrations.
package jetadapter

import (
	"context"
	"fmt"
	"io"

	"github.com/CloudyKit/jet/v6"
)

// View selects a preloaded template and request-local data. Never share mutable
// Vars or Data between requests without application-level synchronization.
type View struct {
	Name string
	Data any
	Vars jet.VarMap
}

// Renderer owns a fixed set of preloaded views. Configure the Jet set before New;
// do not mutate its loader or globals while requests are being served.
type Renderer struct{ views map[string]*jet.Template }

// New parses and validates all allowed templates at startup. It preserves Jet's
// HTML escaping; configuring a loader remains the application's responsibility.
func New(set *jet.Set, names ...string) (*Renderer, error) {
	if set == nil {
		return nil, fmt.Errorf("jet: nil template set")
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("jet: at least one template name is required")
	}
	r := &Renderer{views: make(map[string]*jet.Template, len(names))}
	for _, name := range names {
		if name == "" {
			return nil, fmt.Errorf("jet: empty template name")
		}
		if _, ok := r.views[name]; ok {
			return nil, fmt.Errorf("jet: duplicate template %q", name)
		}
		view, err := set.GetTemplate(name)
		if err != nil {
			return nil, fmt.Errorf("jet: load %q: %w", name, err)
		}
		r.views[name] = view
	}
	return r, nil
}

// Validate rejects an uninitialized adapter during registration.
func (r *Renderer) Validate() error {
	if r == nil || len(r.views) == 0 {
		return fmt.Errorf("jet: no templates configured")
	}
	return nil
}

// Render executes a preloaded View. Jet has no cancellable Execute method, so
// request cancellation is checked before execution and at every output write.
func (r *Renderer) Render(ctx context.Context, w io.Writer, data any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	view, ok := data.(View)
	if !ok {
		return fmt.Errorf("jet: expected jetadapter.View, got %T", data)
	}
	template, ok := r.views[view.Name]
	if !ok {
		return fmt.Errorf("jet: unknown template %q", view.Name)
	}
	return template.Execute(contextWriter{ctx, w}, view.Vars, view.Data)
}

// contextWriter stops a render once its request context is done.
type contextWriter struct {
	ctx context.Context
	w   io.Writer
}

// Write fails with the context's error once ctx is done, else writes to w.
func (w contextWriter) Write(p []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	return w.w.Write(p)
}
