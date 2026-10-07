// Package templadapter connects templ components to HyperChi's integration manager.
package templadapter

import (
	"context"
	"fmt"
	"io"
	"reflect"

	"github.com/a-h/templ"
)

// Renderer accepts templ.Component values. HyperChi owns response buffering.
type Renderer struct{}

// New returns a stateless adapter safe for concurrent rendering.
func New() *Renderer { return &Renderer{} }

// Render invokes the component with the request context; invalid values are errors.
func (*Renderer) Render(ctx context.Context, w io.Writer, data any) error {
	component, ok := data.(templ.Component)
	if !ok || component == nil {
		return fmt.Errorf("templ: expected templ.Component, got %T", data)
	}
	value := reflect.ValueOf(component)
	switch value.Kind() {
	case reflect.Pointer, reflect.Func, reflect.Interface, reflect.Map, reflect.Slice:
		if value.IsNil() {
			return fmt.Errorf("templ: nil component")
		}
	}
	return component.Render(ctx, w)
}
