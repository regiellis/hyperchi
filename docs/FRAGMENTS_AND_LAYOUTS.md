# Fragments and Layouts in HyperChi

HyperChi provides a comprehensive template system with support for layouts, components, and HTMX fragments, inspired by Astro's approach to islands and components.

## 🏗️ Directory Structure

```
templates/
├── layouts/          # Page layouts (wrapping templates)
│   ├── base.tmpl    # Default layout
│   ├── minimal.tmpl # Alternative layout
│   └── admin.tmpl   # Admin-specific layout
├── components/       # Reusable UI components
│   ├── header.tmpl  # Navigation header
│   ├── footer.tmpl  # Page footer
│   └── sidebar.tmpl # Sidebar component
├── fragments/        # HTMX islands/partial updates
│   ├── time.tmpl    # Live time fragment
│   ├── search.tmpl  # Search results fragment
│   └── cart.tmpl    # Shopping cart fragment
└── pages/           # Page templates
    ├── home.tmpl    # Home page
    ├── about.tmpl   # About page
    └── contact.tmpl # Contact page
```

## 🎨 Template Types

### 1. **Layouts** - Page Structure
Layouts wrap page content and provide the overall page structure.

**Location**: `templates/layouts/`  
**Naming**: `layout-{name}` (e.g., `layout-base`, `layout-minimal`)  
**Purpose**: HTML document structure, navigation, common elements

**Example** (`templates/layouts/base.tmpl`):
```html
<!DOCTYPE html>
<html>
<head>
    <title>{{.title}} - My Site</title>
</head>
<body>
    {{template "component-header" .}}
    <main>
        {{.content}}
    </main>
    {{template "component-footer" .}}
</body>
</html>
```

### 2. **Components** - Reusable UI Elements
Components are reusable UI pieces that can be included in layouts and pages.

**Location**: `templates/components/`  
**Naming**: `component-{name}` (e.g., `component-header`, `component-nav`)  
**Purpose**: Navigation, footers, sidebars, common UI elements

**Example** (`templates/components/header.tmpl`):
```html
<header>
    <nav>
        <a href="/">Home</a>
        <a href="/about">About</a>
        <a href="/contact">Contact</a>
    </nav>
</header>
```

### 3. **Fragments** - HTMX Islands
Fragments are partial templates for HTMX updates, similar to Astro islands.

**Location**: `templates/fragments/`  
**Naming**: `fragment-{name}` (e.g., `fragment-time`, `fragment-search`)  
**Purpose**: Live updates, interactive components, partial page updates

**Example** (`templates/fragments/time.tmpl`):
```html
<div id="time-display">
    <p>Current time: {{.current_time}}</p>
    <button hx-get="/fragments/time" hx-target="#time-display">
        Refresh
    </button>
</div>
```

## 🚀 Usage

A handler picks the layout behavior through the response method it returns:

| Method | Layout |
| --- | --- |
| `c.View(name, data)` | the layout for a browser or boosted navigation; none for an htmx partial request |
| `c.Page(name, data)` | the layout, always |
| `c.Fragment(name, data)` | none, ever |

The layout is `layout-base` unless `c.Layout(name)` picks `layout-<name>`. When the chosen
layout template does not exist, the page renders bare.

### Layout Selection

#### Default Layout
```go
// layout-base for a browser, the bare "home" template for an htmx partial request
app.Get("/", func(c *hyperchi.Context) error {
    return c.View("home", hyperchi.H{"title": "Home"})
})
```

#### Specific Layout
```go
// layout-minimal instead of layout-base
app.Get("/products/{slug}", func(c *hyperchi.Context) error {
    product, err := catalog.Find(c.Ctx(), c.Param("slug"))
    if err != nil {
        return hyperchi.NotFound("No such product.")
    }
    return c.Layout("minimal").View("product-page", hyperchi.H{"product": product})
})
```

`c.Layout("none").Page(...)` renders a full-page template with no layout.

#### No Layout (Fragments)
```go
// Never wrapped, whatever sent the request
app.Get("/fragments/time", func(c *hyperchi.Context) error {
    return c.Fragment("fragment-time", hyperchi.H{
        "current_time": time.Now().Format("3:04:05 PM"),
    })
})
```

### Template Loading

#### Automatic Discovery
```go
// Loads layouts/, components/, fragments/, then pages, and data/ files
if err := app.LoadTemplatesWithLayout(); err != nil {
    log.Fatal(err)
}
```

#### Glob Loading
```go
// "**" matches every directory level; a pattern that matches nothing is an error
if err := app.LoadTemplates(filepath.Join("templates", "**", "*.tmpl")); err != nil {
    log.Fatal(err)
}
```

`LoadTemplates` names a template after its base filename with the extension
(`home.tmpl`), unless the file declares `{{define "name"}}`. Prefer an explicit `define` in
every file, so the name does not depend on where the file sits.

## 🎯 Template Resolution

### Automatic Naming (`LoadTemplatesWithLayout`)
- `templates/layouts/base.tmpl` → `layout-base`
- `templates/components/header.tmpl` → `component-header`
- `templates/fragments/search.tmpl` → `fragment-search`
- `templates/home.tmpl` or `templates/pages/home.tmpl` → `home`

Layouts, components, and fragments load from `.html`, `.tmpl`, and `.htm` files. Pages also
load `.md` and `.mdx`.

### Name Lookup
A render looks up the exact name first, then the name with `.tmpl`, `.html`, `.htm`, and
`.gohtml` appended, so `c.View("index", ...)` finds `index.tmpl`. An explicit `define` wins.
A name that resolves to nothing is an error listing the loaded template names.

### Directory Search Order
1. `templates/`
2. `views/`
3. Top-level `layouts/`, `components/`, `fragments/`, and `pages/`

## 🔄 HTMX Integration

`c.View` returns the fragment for an htmx partial request and the full page for a boosted
navigation or a history restore, which both need the whole document. `c.Status(code)` sets the
status for any render. Statuses `204`, `205`, and `304` send headers only, with no body; `204`
keeps htmx from swapping the current content. `c.NoContent()` is the short form, and
`c.HX().Trigger("refreshed").NoContent()` fires an event without a swap.

### Fragment Endpoints
```go
// An endpoint that only answers htmx swaps
app.Get("/fragments/time", func(c *hyperchi.Context) error {
    return c.Fragment("fragment-time", hyperchi.H{
        "current_time": time.Now().Format("3:04:05 PM"),
    })
})
```

### Client-Side Usage
```html
<!-- Auto-loading fragment -->
<div hx-get="/fragments/time" hx-trigger="load">
    Loading...
</div>

<!-- Interactive fragment -->
<div id="search-results">
    <input hx-get="/fragments/search" 
           hx-trigger="keyup changed delay:300ms"
           hx-target="#search-results">
</div>
```

## 🎨 Advanced Features

### Layout Inheritance
```gotemplate
{{/* templates/layouts/admin.tmpl: an admin layout that reuses the base layout */}}
{{define "layout-admin"}}{{template "layout-base" .}}{{end}}
```

### Conditional Components
```html
<!-- In layout template -->
{{if .user}}
    {{template "component-user-menu" .}}
{{else}}
    {{template "component-login-form" .}}
{{end}}
```

### Nested Fragments
```html
<!-- Main fragment -->
<div id="dashboard">
    {{template "fragment-stats" .}}
    {{template "fragment-notifications" .}}
</div>
```

### Data Context
```go
// A global every template sees as {{ .site_name }}; set once at startup
app.SetGlobal("site_name", "My Website")

// Handler data, merged on top of globals and request values
return c.View("profile", hyperchi.H{
    "title": "Page Title",
    "user":  currentUser,
})
```

Keys merge in this order, later winning: route parameters, query parameters, and middleware
values; then `app.SetGlobal` values; then the handler's data. Files under `data/` loaded by
`LoadTemplatesWithLayout` are available as `.Data`. A layout receives the rendered page as
`.content`.

## 🔧 Configuration

### Template Functions
```go
// Add custom template functions
app.RegisterFilter("currency", func(amount float64) string {
    return fmt.Sprintf("$%.2f", amount)
})
```

### Reloading templates
Templates are parsed once, when `LoadTemplates` or `LoadTemplatesWithLayout` runs, in every
environment. To see template edits during development, restart the server on change: a
scaffolded project's `task dev:air` runs Air, which watches `.go`, `.tmpl`, and `.html` files
and rebuilds.

## 📱 Responsive Fragments

### Mobile-First Fragments
```html
<!-- Fragment with responsive behavior -->
<div class="search-fragment" 
     hx-get="/fragments/search"
     hx-trigger="load, resize from:window">
    {{if .mobile}}
        {{template "fragment-mobile-search" .}}
    {{else}}
        {{template "fragment-desktop-search" .}}
    {{end}}
</div>
```

### Adaptive Loading
```html
<!-- Load different content based on connection -->
<div hx-get="/fragments/content" 
     hx-vals='{"fast": {{.fastConnection}}}'>
</div>
```

## 🎯 Best Practices

### 1. **Organization**
- **Layouts**: Page structure only
- **Components**: Reusable UI elements  
- **Fragments**: Interactive/live-updating content
- **Pages**: Content-specific templates

### 2. **Naming Conventions**
```
layouts/base.tmpl      → layout-base
components/nav.tmpl    → component-nav
fragments/cart.tmpl    → fragment-cart
pages/product.tmpl     → product
```

### 3. **Performance**
- Use fragments for partial updates
- Cache static components
- Minimize layout complexity
- Lazy-load non-critical fragments

### 4. **HTMX Patterns**
```html
<!-- Polling fragment -->
<div hx-get="/fragments/stats" hx-trigger="every 30s">

<!-- Form submission fragment -->
<form hx-post="/fragments/comment" hx-target="#comments">

<!-- Infinite scroll fragment -->
<div hx-get="/fragments/posts" hx-trigger="revealed">
```

## 🎉 Complete Example

### Handler
```go
app.Get("/dashboard", func(c *hyperchi.Context) error {
    stats, err := getDashboardStats(c.Ctx())
    if err != nil {
        return err
    }
    // layout-admin for a browser, the bare template for an htmx request
    return c.Layout("admin").View("dashboard", hyperchi.H{
        "title": "Dashboard",
        "user":  c.UserID(),
        "stats": stats,
    })
})

app.Get("/fragments/stats", func(c *hyperchi.Context) error {
    stats, err := getDashboardStats(c.Ctx())
    if err != nil {
        return err
    }
    return c.Fragment("fragment-stats", hyperchi.H{"stats": stats})
})
```

### Templates
```html
<!-- templates/layouts/admin.tmpl -->
<!DOCTYPE html>
<html>
<head><title>{{.title}} - Admin</title></head>
<body>
    {{template "component-admin-nav" .}}
    <main>{{.content}}</main>
</body>
</html>

<!-- templates/dashboard.tmpl -->
<h1>{{.title}}</h1>
<div hx-get="/fragments/stats" hx-trigger="load every 60s">
    Loading stats...
</div>

<!-- templates/fragments/stats.tmpl -->
<div class="stats">
    <div>Users: {{.stats.users}}</div>
    <div>Revenue: {{.stats.revenue}}</div>
</div>
```

This system provides the flexibility of Astro's islands with the simplicity of Go templates, giving you the best of both worlds for building modern web applications with HyperChi.
