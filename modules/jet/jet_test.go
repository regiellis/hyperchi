package jetadapter

import (
	"bytes"
	"context"
	"errors"
	"github.com/CloudyKit/jet/v6"
	"testing"
)

func TestRender(t *testing.T) {
	loader := jet.NewInMemLoader()
	loader.Set("page.jet", "<p>{{.}}</p>")
	r, err := New(jet.NewSet(loader), "page.jet")
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := r.Render(context.Background(), &out, View{Name: "page.jet", Data: "<script>"}); err != nil {
		t.Fatal(err)
	}
	if out.String() != "<p>&lt;script&gt;</p>" {
		t.Fatal(out.String())
	}
	for _, data := range []any{"wrong", View{Name: "missing"}} {
		if err := r.Render(context.Background(), &out, data); err == nil {
			t.Fatal("invalid view accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := r.Render(ctx, &out, View{Name: "page.jet"}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := New(jet.NewSet(loader), "missing"); err == nil {
		t.Fatal("missing template accepted")
	}
	if _, err := New(nil, "page.jet"); err == nil {
		t.Fatal("nil set accepted")
	}
}
