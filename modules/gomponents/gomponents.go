// Package gomponentsadapter renders gomponents nodes through HyperChi's
// integration manager, so a handler can answer with c.RenderWith(name, node).
// It also provides typed htmx and hc-* directive attributes whose names are
// checked and whose values are HTML-escaped, and HTML for placing a node
// inside an html/template layout.
package gomponentsadapter

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"io"
	"reflect"
	"strings"

	g "maragu.dev/gomponents"
)

// Renderer accepts gomponents.Node values. It is stateless and safe for
// concurrent use; HyperChi buffers its output, so a failed render never sends
// a partial page.
type Renderer struct{}

// New returns a Renderer. Register it once at startup:
//
//	helpers.Must(app.Integrations().Register("gomponents", gomponentsadapter.New()))
func New() *Renderer { return &Renderer{} }

// Render writes the node to w. Anything other than a non-nil gomponents.Node
// is an error, and a request context that is already done stops the render.
func (*Renderer) Render(ctx context.Context, w io.Writer, data any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	node, ok := data.(g.Node)
	if !ok {
		return fmt.Errorf("gomponents: expected gomponents.Node, got %T", data)
	}
	if isNil(node) {
		return errors.New("gomponents: nil node")
	}
	return node.Render(w)
}

// HTML renders a node to template.HTML, for placing gomponents output inside
// an html/template layout. The node escapes its own text and attribute values,
// so only build it from trusted structure; never wrap request input in g.Raw.
func HTML(node g.Node) (template.HTML, error) {
	if node == nil || isNil(node) {
		return "", errors.New("gomponents: nil node")
	}
	var b strings.Builder
	if err := node.Render(&b); err != nil {
		return "", err
	}
	return template.HTML(b.String()), nil // #nosec G203 -- gomponents escaped every value while rendering
}

// isNil reports a typed nil inside the interface, such as a nil NodeFunc,
// which would otherwise panic when rendered. An empty g.Group is a slice and
// renders nothing, which is not an error.
func isNil(node g.Node) bool {
	if node == nil {
		return true
	}
	v := reflect.ValueOf(node)
	switch v.Kind() {
	case reflect.Pointer, reflect.Func, reflect.Interface, reflect.Map:
		return v.IsNil()
	}
	return false
}
