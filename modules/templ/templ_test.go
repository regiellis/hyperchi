package templadapter

import (
	"bytes"
	"context"
	"errors"
	"github.com/a-h/templ"
	"io"
	"testing"
)

func TestRender(t *testing.T) {
	renderer := New()
	var out bytes.Buffer
	failure := errors.New("failed")
	c := templ.ComponentFunc(func(ctx context.Context, w io.Writer) error { _, _ = io.WriteString(w, "<p>ok</p>"); return failure })
	if err := renderer.Render(context.Background(), &out, c); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if out.String() != "<p>ok</p>" {
		t.Fatal(out.String())
	}
	if err := renderer.Render(context.Background(), &out, "wrong"); err == nil {
		t.Fatal("wrong type accepted")
	}
	var nilComponent templ.ComponentFunc
	if err := renderer.Render(context.Background(), &out, nilComponent); err == nil {
		t.Fatal("typed nil accepted")
	}
}
