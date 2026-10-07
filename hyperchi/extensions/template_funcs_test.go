package extensions

import (
	"errors"
	"testing"
)

// greeter embeds BaseExtension and adds template functions through
// TemplateFuncProvider, without writing an Initialize of its own.
type greeter struct {
	BaseExtension
}

func (g *greeter) RegisterTemplateFuncs(registry *Registry) error {
	registry.AddTemplateFunc("greet", func(name string) string { return "hello " + name })
	return nil
}

// The registry calls RegisterTemplateFuncs on the registered value, so an
// embedding type's method is found; a method on an embedded base type could
// never see it.
func TestRegisterCallsEmbeddingTypesTemplateFuncs(t *testing.T) {
	r := NewRegistry()
	g := &greeter{}
	g.SetName("greeter")
	if err := r.Register(g); err != nil {
		t.Fatalf("Register: %v", err)
	}
	fn, ok := r.GetTemplateFuncs()["greet"].(func(string) string)
	if !ok {
		t.Fatal("greet was not registered: RegisterTemplateFuncs never ran")
	}
	if got := fn("ada"); got != "hello ada" {
		t.Fatalf("greet(ada) = %q", got)
	}
}

type failingFuncs struct {
	BaseExtension
}

var errFuncs = errors.New("funcs failed")

func (f *failingFuncs) RegisterTemplateFuncs(*Registry) error { return errFuncs }

// A RegisterTemplateFuncs error fails Register, which adds nothing.
func TestRegisterTemplateFuncsErrorFailsRegister(t *testing.T) {
	r := NewRegistry()
	f := &failingFuncs{}
	f.SetName("failing")
	if err := r.Register(f); !errors.Is(err, errFuncs) {
		t.Fatalf("Register error = %v, want %v", err, errFuncs)
	}
	if _, ok := r.Get("failing"); ok {
		t.Fatal("an extension whose template funcs failed was registered")
	}
}
