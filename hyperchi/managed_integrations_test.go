package hyperchi

import (
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"testing"

	"github.com/regiellis/hyperchi/hyperchi/config"
	"github.com/regiellis/hyperchi/hyperchi/integrations"
)

type testViewRenderer struct{ fail bool }

func (r testViewRenderer) Render(_ context.Context, w io.Writer, data any) error {
	_, err := io.WriteString(w, "<p>"+data.(string)+"</p>")
	if err != nil {
		return err
	}
	if r.fail {
		return errors.New("render failure")
	}
	return nil
}
func TestContextRenderWith(t *testing.T) {
	cfg := config.Production()
	cfg.Database.Name = ":memory:"
	app := NewWithConfig(cfg)
	t.Cleanup(func() { _ = app.CloseDB() })
	if err := app.Integrations().Register("good", testViewRenderer{}); err != nil {
		t.Fatal(err)
	}
	if err := app.Integrations().Register("bad", testViewRenderer{true}); err != nil {
		t.Fatal(err)
	}
	if err := app.Integrations().Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := app.Integrations().Close(context.Background()); err != nil {
			t.Errorf("close integrations: %v", err)
		}
	})
	app.Group("/group", func(group *HyperChi) {
		if group.Integrations() != app.Integrations() {
			t.Error("group owns a different manager")
		}
	})
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	c := app.NewContext(w, req)
	if err := c.RenderWith("bad", "partial"); err == nil {
		t.Fatal("missing render failure")
	}
	if w.Body.Len() != 0 || w.Header().Get("Content-Type") != "" {
		t.Fatal("partial response committed")
	}
	if err := c.RenderWith("good", "ready"); err != nil {
		t.Fatal(err)
	}
	if w.Body.String() != "<p>ready</p>" || w.Header().Get("Content-Type") != "text/html; charset=utf-8" {
		t.Fatal(w)
	}
}

func TestServeFailureClosesIntegrations(t *testing.T) {
	cfg := config.Production()
	cfg.Database.Name = ":memory:"
	app := NewWithConfig(cfg)
	t.Cleanup(func() { _ = app.CloseDB() })
	started, closed := false, false
	if err := app.Integrations().Register("service", integrations.Hooks{
		StartFunc: func(context.Context) error { started = true; return nil },
		CloseFunc: func(context.Context) error { closed = true; return nil },
	}); err != nil {
		t.Fatal(err)
	}
	if err := app.Serve("invalid-address"); err == nil {
		t.Fatal("invalid address accepted")
	}
	if !started || !closed {
		t.Fatalf("started=%v closed=%v", started, closed)
	}
}
