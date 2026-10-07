package extensions

import "fmt"

// BaseExtension implements Extension with empty lists, version "1.0.0", and
// a no-op Initialize. Embed it and override only the methods you need. Its
// fields are unexported, so set them with the Set and Add methods:
//
//	type MyExtension struct {
//		extensions.BaseExtension
//	}
//
//	func NewMyExtension() *MyExtension {
//		e := &MyExtension{}
//		e.SetName("my-extension").SetDescription("My custom htmx extension")
//		return e
//	}
type BaseExtension struct {
	name         string
	version      string
	description  string
	scripts      []string
	styles       []string
	dependencies []string
	attributes   []string
}

// Name returns the extension name.
func (b *BaseExtension) Name() string {
	return b.name
}

// Version returns the extension version, "1.0.0" when unset.
func (b *BaseExtension) Version() string {
	if b.version == "" {
		return "1.0.0"
	}
	return b.version
}

// Description returns the extension description, or "HTMX extension: <name>"
// when unset.
func (b *BaseExtension) Description() string {
	if b.description == "" {
		return fmt.Sprintf("HTMX extension: %s", b.name)
	}
	return b.description
}

// Scripts returns JavaScript files for this extension.
func (b *BaseExtension) Scripts() []string {
	return b.scripts
}

// Styles returns CSS files for this extension.
func (b *BaseExtension) Styles() []string {
	return b.styles
}

// Dependencies returns extensions this extension depends on.
func (b *BaseExtension) Dependencies() []string {
	return b.dependencies
}

// Attributes returns HTMX attributes this extension provides.
func (b *BaseExtension) Attributes() []string {
	return b.attributes
}

// Initialize does nothing and returns nil.
func (b *BaseExtension) Initialize(registry *Registry) error {
	return nil
}

// SetName sets the extension name.
func (b *BaseExtension) SetName(name string) *BaseExtension {
	b.name = name
	return b
}

// SetVersion sets the extension version.
func (b *BaseExtension) SetVersion(version string) *BaseExtension {
	b.version = version
	return b
}

// SetDescription sets the extension description.
func (b *BaseExtension) SetDescription(description string) *BaseExtension {
	b.description = description
	return b
}

// AddScript adds a JavaScript file to the extension.
func (b *BaseExtension) AddScript(script string) *BaseExtension {
	b.scripts = append(b.scripts, script)
	return b
}

// AddStyle adds a CSS file to the extension.
func (b *BaseExtension) AddStyle(style string) *BaseExtension {
	b.styles = append(b.styles, style)
	return b
}

// AddDependency adds a dependency to the extension.
func (b *BaseExtension) AddDependency(dep string) *BaseExtension {
	b.dependencies = append(b.dependencies, dep)
	return b
}

// AddAttribute adds an HTMX attribute to the extension.
func (b *BaseExtension) AddAttribute(attr string) *BaseExtension {
	b.attributes = append(b.attributes, attr)
	return b
}

// TemplateFuncProvider is implemented by extensions that add template
// functions. Registry.Register calls RegisterTemplateFuncs after Initialize,
// on the value it was given, so an extension that embeds BaseExtension only
// defines this method (and does not call it from Initialize):
//
//	type MyExtension struct {
//		extensions.BaseExtension
//	}
//
//	func (e *MyExtension) RegisterTemplateFuncs(registry *extensions.Registry) error {
//		registry.AddTemplateFunc("myHelper", func(value string) string {
//			return "my-" + value
//		})
//		return nil
//	}
//
// Registry.GetTemplateFuncs returns the functions for a template's FuncMap:
//
//	<div class="{{ myHelper "class-name" }}">Content</div>
type TemplateFuncProvider interface {
	RegisterTemplateFuncs(registry *Registry) error
}
