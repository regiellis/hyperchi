// Package filters is the set of template functions every HyperChi app
// registers, over 200 of them: strings (title, truncate, slug), dates (date,
// timeAgo), numbers (currency, percent, bytes), HTML (nl2br, classNames,
// attr), htmx attributes (hxGet, hxTarget, hxSwap), JSON (toJSON,
// prettyJSON), and collections (sort, unique, chunk).
//
// In a template a piped value becomes the filter's last argument:
//
//	{{ "hello world" | title }}                  // Hello World
//	{{ .price | currency }}                      // $19.99
//	{{ .createdAt | timeAgo }}                   // 2 hours ago
//	{{ join .tags ", " }}                        // go, htmx, web
//	<button {{ hxGet "/api/data" }} {{ hxTarget "#result" }}>Load</button>
//
// Outside a HyperChi app, pass FuncMap to a template:
//
//	tmpl := template.New("").Funcs(filters.FuncMap())
package filters

import (
	"html/template"
	"strings"
	"sync"
)

// defaultFilters is the registry FuncMap copies; mu guards it.
var (
	defaultFilters = make(template.FuncMap)
	mu             sync.RWMutex
)

func init() {
	registerStringFilters()
	registerHTMLFilters()
	registerJSONFilters()
	registerDateFilters()
	registerNumberFilters()
	registerCollectionFilters()
	registerHTMXFilters()
	registerUtilityFilters()
}

// FuncMap returns a copy of every registered filter, keyed by name.
func FuncMap() template.FuncMap {
	mu.RLock()
	defer mu.RUnlock()

	result := make(template.FuncMap, len(defaultFilters))
	for k, v := range defaultFilters {
		result[k] = v
	}
	return result
}

// Register adds fn under name, replacing any filter with that name. fn must
// be a valid template function. A HyperChi app copies the filter set when it
// is created, so call Register before creating the app, or use
// HyperChi.RegisterFilter afterwards. Register is safe for concurrent use.
func Register(name string, fn any) {
	mu.Lock()
	defer mu.Unlock()
	defaultFilters[name] = fn
}

// RegisterMap calls Register for each entry of funcMap.
func RegisterMap(funcMap template.FuncMap) {
	mu.Lock()
	defer mu.Unlock()
	for name, fn := range funcMap {
		defaultFilters[name] = fn
	}
}

// joinPath joins parts into one absolute path, trimming slashes from each
// part and dropping empty ones.
func joinPath(parts ...string) string {
	cleaned := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.Trim(part, "/")
		if part != "" {
			cleaned = append(cleaned, part)
		}
	}
	return "/" + strings.Join(cleaned, "/")
}
