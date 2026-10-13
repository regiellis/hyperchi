# HyperChi Microframework Architecture

> **Status: historical design record.** The single-package layout below did not hold: content,
> validation schemas, database, security, filters, and the extensions are sub-packages under
> `hyperchi/`, and the framework depends on more than chi. `app.NewContentLayer`,
> `hyperchi.ContentConfig`, `hyperchi.CollectionConfig`, and `hyperchi.QueryOptions` were never
> part of the API (content lives in `hyperchi/content`; see its README), and
> `examples/microframework/` does not exist. The filter pipelines below pass arguments in the
> wrong order; `hyperchi/filters/README.md` has the calling conventions. The current API is
> described in [CONTEXT_FIRST_API.md](CONTEXT_FIRST_API.md) and the root README.

## 🎯 True Microframework Approach

HyperChi has been transformed from a collection of packages into a true microframework with a single import. Everything you need is included in one cohesive package.

## 📦 Single Package Structure

```
github.com/regiellis/hyperchi/hyperchi/
├── hyperchi.go              # Core framework & router
├── middleware.go         # Built-in middleware stack  
├── filters.go            # Template filter system
├── filters_string.go     # String manipulation filters
├── filters_stubs.go      # Other filter categories
├── validator.go          # Type-safe validation
├── content.go            # Content management system
└── README.md             # Framework documentation
```

## 🔄 Before vs After

### Before (Multiple Packages)
```go
import (
    "github.com/go-chi/chi/v5"
    "github.com/regiellis/hyperchi/hyperchi"
    "github.com/regiellis/hyperchi/middleware"
    "github.com/regiellis/hyperchi/filters"
    "github.com/regiellis/hyperchi/validator"
    "github.com/regiellis/hyperchi/content"
    "github.com/regiellis/hyperchi/routing"
)

func main() {
    r := chi.NewRouter()
    hx := hyperchi.New()
    
    r.Use(middleware.HTMX())
    r.Use(middleware.Logger())
    
    cl := content.NewContentLayer(&content.ContentConfig{
        BaseDir: "content",
    })
    
    schema := validator.NewSchema().Fields(map[string]validator.Field{
        "title": validator.String().Required(),
    })
    
    r.Get("/", handler) // a net/http handler that renders and writes by hand
    
    http.ListenAndServe(":8080", r)
}
```

### After (Single Package)
```go
import "github.com/regiellis/hyperchi/hyperchi"

func main() {
    app := hyperchi.New()
    
    app.Use(app.Middleware.HTMX())
    app.Use(app.Middleware.Logger())
    
    content := app.NewContentLayer(&hyperchi.ContentConfig{
        BaseDir: "content",
    })
    
    app.Get("/", func(c *hyperchi.Context) error {
        return c.View("index", hyperchi.H{"title": "Home"})
    })
    
    app.Serve(":8080")
}
```

## ✨ Key Improvements

### 1. **Single Import**
- Only need to import `github.com/regiellis/hyperchi/hyperchi`
- No need to learn multiple package APIs
- Simplified dependency management

### 2. **Unified API**
- Everything accessed through the main `app` instance
- Consistent method signatures
- No cross-package dependencies

### 3. **Built-in Everything**
- **Routing**: `app.Get()`, `app.Post()`, `app.Group()`
- **Middleware**: `app.Middleware.HTMX()`, `app.Middleware.Logger()`
- **Templates**: `app.LoadTemplates()` with 100+ built-in filters
- **Validation**: `hyperchi.Bind[T](c)` with `validate` tags; `app.Schemas` + `c.ValidateSchema` for runtime-shaped input
- **Content**: `app.NewContentLayer()`, collections, queries
- **Server**: `app.Serve()` - no need for `http.ListenAndServe`

### 4. **HTMX-First Design**
- `c.View` negotiates per request: the full page for a browser, the fragment for an htmx
  partial request. Returned errors render as JSON, an htmx fragment, or an error page
- Built-in HTMX headers and response management
- Template filters for HTMX attributes
- Development helpers for HTMX debugging

### 5. **Zero External Dependencies**
- Only depends on Go standard library + Chi router
- Chi is used internally - users never import it directly
- Self-contained with all necessary functionality

## 🏗️ Framework Components

### Core Framework
```go
app := hyperchi.New()

// Built-in methods
app.LoadTemplates("*.html")
app.Use(middleware...)
app.Get("/path", func(c *hyperchi.Context) error { return c.View("template", data) })
app.Serve(":8080")
```

### Middleware Stack
```go
app.Use(app.Middleware.HTMX())        // HTMX integration
app.Use(app.Middleware.Logger())      // Request logging
app.Use(app.Middleware.CORS())        // HTMX-compatible CORS
app.Use(app.Middleware.Security())    // Security headers
app.Use(app.Middleware.Recovery())    // Panic recovery
app.Use(app.Middleware.Development()) // Dev helpers
```

### Template System
```go
// 100+ built-in filters
{{ .name | title | truncate 50 }}
{{ .price | currency }}
{{ .date | timeAgo }}
{{ .content | nl2br }}

// HTMX helpers
<button {{ hxGet "/api/data" }} {{ hxTarget "#content" }}>Load</button>
```

### Validation System
```go
type Post struct {
    Title string   `form:"title" validate:"required,max=200"`
    Email string   `form:"email" validate:"required,email"`
    Age   int      `form:"age" validate:"min=18,max=120"`
    Tags  []string `form:"tag" validate:"min=1"`
}

in, err := hyperchi.Bind[Post](c) // 400 unreadable, 422 with per-field messages
```

### Content Management
```go
content := app.NewContentLayer(&hyperchi.ContentConfig{
    BaseDir: "content",
})

content.DefineCollection("blog", hyperchi.CollectionConfig{
    Path: "content/blog",
    Schema: blogSchema,
})

posts := content.Query("blog", hyperchi.QueryOptions{
    Published: &[]bool{true}[0],
    Limit: 10,
})
```

## 📊 Comparison

| Feature | Before | After |
|---------|---------|--------|
| **Imports** | 7 packages | 1 package |
| **Learning Curve** | Multiple APIs | Single API |
| **Dependencies** | Cross-package | Self-contained |
| **Chi Usage** | Direct import | Hidden implementation |
| **Setup Code** | 15+ lines | 5 lines |
| **Type Safety** | Scattered | Unified |

## 🎯 Benefits

### For Developers
- **Faster Development**: Single import, single API
- **Less Complexity**: No need to understand multiple packages
- **Better IntelliSense**: Everything under one namespace
- **Easier Testing**: Unified mocking and testing patterns

### For Projects
- **Smaller Binaries**: No duplicate dependencies
- **Faster Builds**: Single package compilation
- **Better Maintainability**: Consistent patterns throughout
- **Easier Upgrades**: Single version to manage

### For Teams
- **Lower Learning Curve**: One API to learn
- **Consistent Code Style**: Framework enforces patterns
- **Better Documentation**: Single source of truth
- **Faster Onboarding**: Less concepts to understand

## 🚀 Migration Guide

### From Old HyperChi
1. Replace multiple imports with single import
2. Change `hyperchi.New()` to `hyperchi.New()`
3. Replace `r.Get()` with `app.Get()`
4. Replace middleware imports with `app.Middleware.*`
5. Replace validator/content imports with `app.*` methods

### From Other Frameworks
- **From Gin**: Similar API but with HTMX-first design
- **From Echo**: Replace with `app.Get/Post/etc` methods
- **From Chi**: Replace with HyperChi routing methods
- **From Template Libraries**: Use built-in filters
- **From Validation Libraries**: Use built-in validation

## 📈 Examples

Check out the `/examples/microframework/` directory for a complete example showing:
- Single import usage
- Built-in middleware
- Template system with filters
- Form validation
- Content management
- HTMX integration
- API endpoints
- Static file serving

---

**HyperChi** - The complete microframework for Go! 🚀  
*Everything you need in a single package.*