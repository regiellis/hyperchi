// Package extensions is the registry for HyperChi's htmx extensions: each
// declares its scripts, styles, attributes, and dependencies, and can add
// template functions when it registers. The SSE and WebSocket hubs live in the
// sse and websocket subpackages.
package extensions

import (
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"sync"
)

// Extension is an htmx extension the Registry can hold: its client assets,
// the attributes it adds, and an Initialize hook run at registration. Embed
// BaseExtension to get defaults for every method:
//
//	type MyExtension struct{ extensions.BaseExtension }
//
//	func (e *MyExtension) Scripts() []string { return []string{"/js/my-extension.js"} }
type Extension interface {
	// Name returns the extension's unique name.
	Name() string

	// Version returns the extension version.
	Version() string

	// Scripts returns the URLs of the JavaScript files the extension needs.
	Scripts() []string

	// Styles returns the URLs of the CSS files the extension needs.
	Styles() []string

	// Dependencies returns the names of extensions that must be registered
	// first.
	Dependencies() []string

	// Initialize runs once during Registry.Register, without the registry
	// lock held, so it may call back into the registry. To add template
	// functions, implement TemplateFuncProvider instead.
	Initialize(registry *Registry) error

	// Attributes returns the htmx attributes the extension adds.
	Attributes() []string

	// Description returns a human-readable description.
	Description() string
}

// Registry holds extensions by name, orders their assets after their
// dependencies', and collects the template functions they add. It is safe
// for concurrent use.
type Registry struct {
	extensions map[string]Extension
	mu         sync.RWMutex

	templateFuncs template.FuncMap
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{
		extensions:    make(map[string]Extension),
		templateFuncs: make(template.FuncMap),
	}
}

// Register initializes ext and adds it. After Initialize it calls
// RegisterTemplateFuncs when ext implements TemplateFuncProvider. It returns
// an error, and adds nothing, if the name is taken, a dependency is not
// registered yet, or Initialize or RegisterTemplateFuncs fails.
func (r *Registry) Register(ext Extension) error {
	name := ext.Name()

	r.mu.RLock()
	if _, exists := r.extensions[name]; exists {
		r.mu.RUnlock()
		return fmt.Errorf("extension %s is already registered", name)
	}
	for _, dep := range ext.Dependencies() {
		if _, exists := r.extensions[dep]; !exists {
			r.mu.RUnlock()
			return fmt.Errorf("extension %s depends on %s which is not registered", name, dep)
		}
	}
	r.mu.RUnlock()

	// Initialize runs without the lock: extensions call back into the
	// registry (AddTemplateFunc), which would deadlock under it.
	if err := ext.Initialize(r); err != nil {
		return fmt.Errorf("failed to initialize extension %s: %w", name, err)
	}
	// Dispatch on ext, the value the caller registered, so the method of a
	// type embedding BaseExtension is found.
	if provider, ok := ext.(TemplateFuncProvider); ok {
		if err := provider.RegisterTemplateFuncs(r); err != nil {
			return fmt.Errorf("failed to register template functions of extension %s: %w", name, err)
		}
	}

	// Check the name again: another Register may have won while Initialize
	// ran unlocked.
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.extensions[name]; exists {
		return fmt.Errorf("extension %s is already registered", name)
	}
	r.extensions[name] = ext
	return nil
}

// Get returns the extension registered under name.
func (r *Registry) Get(name string) (Extension, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	ext, exists := r.extensions[name]
	return ext, exists
}

// List returns the registered extensions in no particular order.
func (r *Registry) List() []Extension {
	r.mu.RLock()
	defer r.mu.RUnlock()

	extensions := make([]Extension, 0, len(r.extensions))
	for _, ext := range r.extensions {
		extensions = append(extensions, ext)
	}
	return extensions
}

// GetScripts returns the scripts of the named extensions, each extension's
// dependencies first, without repeating an extension. Unknown names are
// skipped.
func (r *Registry) GetScripts(extensionNames []string) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var scripts []string
	processed := make(map[string]bool)

	var addScripts func(string)
	addScripts = func(name string) {
		if processed[name] {
			return
		}
		processed[name] = true

		ext, exists := r.extensions[name]
		if !exists {
			return
		}

		for _, dep := range ext.Dependencies() {
			addScripts(dep)
		}

		scripts = append(scripts, ext.Scripts()...)
	}

	for _, name := range extensionNames {
		addScripts(name)
	}

	return scripts
}

// GetStyles returns the styles of the named extensions in the order
// GetScripts uses.
func (r *Registry) GetStyles(extensionNames []string) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var styles []string
	processed := make(map[string]bool)

	var addStyles func(string)
	addStyles = func(name string) {
		if processed[name] {
			return
		}
		processed[name] = true

		ext, exists := r.extensions[name]
		if !exists {
			return
		}

		for _, dep := range ext.Dependencies() {
			addStyles(dep)
		}

		styles = append(styles, ext.Styles()...)
	}

	for _, name := range extensionNames {
		addStyles(name)
	}

	return styles
}

// AddTemplateFunc adds a template function, replacing one with the same name.
func (r *Registry) AddTemplateFunc(name string, fn any) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.templateFuncs[name] = fn
}

// GetTemplateFuncs returns a copy of the template functions extensions added.
func (r *Registry) GetTemplateFuncs() template.FuncMap {
	r.mu.RLock()
	defer r.mu.RUnlock()

	funcs := make(template.FuncMap)
	for name, fn := range r.templateFuncs {
		funcs[name] = fn
	}
	return funcs
}

// ExtensionInfo is an extension's metadata, as GetInfo returns it.
type ExtensionInfo struct {
	Name         string   `json:"name"`
	Version      string   `json:"version"`
	Description  string   `json:"description"`
	Dependencies []string `json:"dependencies"`
	Attributes   []string `json:"attributes"`
	Scripts      []string `json:"scripts"`
	Styles       []string `json:"styles"`
}

// GetInfo returns the metadata of every registered extension, in no
// particular order.
func (r *Registry) GetInfo() []ExtensionInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var info []ExtensionInfo
	for _, ext := range r.extensions {
		info = append(info, ExtensionInfo{
			Name:         ext.Name(),
			Version:      ext.Version(),
			Description:  ext.Description(),
			Dependencies: ext.Dependencies(),
			Attributes:   ext.Attributes(),
			Scripts:      ext.Scripts(),
			Styles:       ext.Styles(),
		})
	}
	return info
}

// Middleware returns middleware that lists the registered extension names in
// an X-HTMX-Extensions response header on htmx requests, for debugging. It
// does not load or inject any asset.
func (r *Registry) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if req.Header.Get("HX-Request") == "true" {
				extensions := r.List()
				if len(extensions) > 0 {
					var names []string
					for _, ext := range extensions {
						names = append(names, ext.Name())
					}
					w.Header().Set("X-HTMX-Extensions", strings.Join(names, ","))
				}
			}

			next.ServeHTTP(w, req)
		})
	}
}

// globalRegistry backs the package-level Register, Get, and List.
var globalRegistry = NewRegistry()

// Register adds ext to the global registry; see Registry.Register.
func Register(ext Extension) error {
	return globalRegistry.Register(ext)
}

// Get returns an extension from the global registry.
func Get(name string) (Extension, bool) {
	return globalRegistry.Get(name)
}

// List returns all extensions from the global registry.
func List() []Extension {
	return globalRegistry.List()
}

// GetGlobalRegistry returns the global extension registry.
func GetGlobalRegistry() *Registry {
	return globalRegistry
}
