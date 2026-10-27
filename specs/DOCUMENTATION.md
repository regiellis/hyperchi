# HyperChi Documentation Guide

This document explains how to generate, view, and maintain documentation for the HyperChi project using Go's built-in `godoc` tool.

## Overview

HyperChi uses **godoc** to generate comprehensive documentation from GoDoc comments in the source code. This provides:

- **API documentation** for all exported functions, types, and variables
- **Package overviews** with usage examples
- **Cross-linked navigation** between packages and symbols
- **Example code** with runnable demonstrations
- **Static HTML generation** for offline viewing

## Quick Start

### Start Documentation Server
```bash
# Using Task
task docs

# Using script directly
./scripts/docs.sh serve

# Custom port
./scripts/docs.sh serve -p 8080
```

### Generate Static Documentation
```bash
# Using Task
task docs_generate

# Using script directly
./scripts/docs.sh generate
```

### Check Documentation Coverage
```bash
# Using Task
task docs_check

# Using script directly
./scripts/docs.sh check
```

## Documentation Structure

### Main Package
- **URL**: `http://localhost:6060/pkg/github.com/regiellis/hyperchi/`
- **Content**: Overview of the entire HyperChi project
- **Includes**: Quick start guide, architecture overview, key features

### HyperChi Core Package
- **URL**: `http://localhost:6060/pkg/github.com/regiellis/hyperchi/hyperchi/`
- **Content**: Complete API documentation for the core framework
- **Includes**: All functions, types, methods with examples

### Examples
- `Example*` functions in `hyperchi/example_test.go` appear on the core package's page.
- The apps under `examples/` are separate Go modules and do not appear in godoc.

## Documentation Features

### API Documentation
Every exported symbol includes:
- **Function signatures** with parameter and return types
- **Detailed descriptions** of functionality
- **Usage examples** with code snippets
- **Related functions** and cross-references

### Package Documentation
Each package provides:
- **Package overview** explaining the purpose and scope
- **Installation instructions** and setup guides
- **Quick start examples** for immediate usage
- **Architecture explanations** for understanding design decisions

### Example Code
Documentation includes:
- **Runnable examples** that demonstrate usage
- **Code snippets** embedded in function documentation
- **Complete applications** showing real-world usage
- **Performance benchmarks** with timing information

## Writing Good Documentation

### Package-Level Documentation
```go
// Package hyperchi is an HTMX-first framework for content-driven web applications.
//
// It combines chi routing, server-side rendering, htmx request and response handling, and
// real-time hubs behind one handler shape, func(c *hyperchi.Context) error.
//
// # Quick Start
//
//   package main
//   
//   import (
//       "log"
//
//       "github.com/regiellis/hyperchi/hyperchi"
//   )
//
//   func main() {
//       app := hyperchi.New()
//       app.Get("/", func(c *hyperchi.Context) error {
//           return c.View("index", hyperchi.H{"title": "Home"})
//       })
//       log.Fatal(app.Serve(":8080"))
//   }
//
// # Key Features
//
// - Server-side rendering with htmx negotiation (c.View)
// - Layouts, components, and fragments
// - Typed errors rendered per client (JSON, htmx fragment, error page)
package hyperchi
```

### Function Documentation
```go
// View renders a template the way the request needs it: the full page inside
// its layout for a normal or boosted navigation, and the template alone for an
// htmx partial request.
//
// Example:
//
//   app.Get("/users/{id}", func(c *hyperchi.Context) error {
//       user, err := users.Find(c.Ctx(), c.Param("id"))
//       if err != nil {
//           return hyperchi.NotFound("No such user.")
//       }
//       return c.View("user-profile", hyperchi.H{"user": user})
//   })
//
// View:
//   - Detects htmx partial requests from the HX-Request and HX-Boosted headers
//   - Buffers the render, so a template error never sends a half page
//   - Returns render errors for the app's error handler
func (c *Context) View(name string, data any) error
```

### Type Documentation
```go
// HTMLRenderer defines the interface for rendering HTML responses.
//
// TemplateRenderer implements it; route handlers render through Context.View,
// Page, and Fragment instead of calling a renderer directly.
type HTMLRenderer interface {
    // Render returns the HTML for result, the handler's response data.
    //
    // Parameters:
    //   - result: the data the handler passed to the response method
    //   - context: values extracted from the request (route and query parameters)
    //   - request: the HTTP request, for request-specific rendering
    //
    // Returns the rendered HTML string or an error if rendering fails.
    Render(result any, context map[string]any, request *http.Request) (string, error)
}
```

## Documentation Best Practices

### Structure
1. **Start with a summary** - One sentence describing the function/type
2. **Provide context** - Explain when and why to use it
3. **Include examples** - Show realistic usage scenarios
4. **Document parameters** - Explain each parameter's purpose
5. **Explain returns** - Document return values and error conditions
6. **Cross-reference** - Link to related functions and types

### Examples
- **Use realistic scenarios** - Show actual use cases, not contrived examples
- **Include error handling** - Demonstrate proper error handling patterns
- **Show complete code** - Provide working examples when possible
- **Explain the output** - Show what the example produces

### Formatting
- **Use proper heading levels** - Start with # for major sections
- **Code blocks** - Wrap code in triple backticks with language
- **Lists** - Use bullet points for features and benefits
- **Links** - Reference other packages and external resources

## Static Documentation Generation

### Output Structure
```
docs/generated/
├── index.html              # Main package documentation
├── hyperchi/
│   └── index.html          # HyperChi package documentation
├── examples/
│   └── index.html          # Examples package documentation
└── lib/godoc/              # CSS and JavaScript assets
    ├── style.css
    ├── jquery.js
    └── godocs.js
```

### Hosting Static Documentation
The generated static documentation can be:
- **Hosted on GitHub Pages** for public projects
- **Served by any web server** for private documentation
- **Included in releases** for offline documentation
- **Integrated into CI/CD** for automatic updates

### Automation
```bash
# Add to CI/CD pipeline
./scripts/docs.sh generate
# Upload docs/generated/ to hosting service
```

## Integration with Development Workflow

### During Development
1. **Write documentation** as you write code
2. **Test examples** to ensure they work correctly
3. **Review documentation** before committing changes
4. **Check coverage** to find missing documentation

### Before Releases
1. **Generate static docs** for the release
2. **Review all documentation** for accuracy
3. **Update package versions** in examples
4. **Test documentation links** and examples

### Continuous Integration
```yaml
# Example GitHub Actions workflow
- name: Generate Documentation
  run: |
    ./scripts/docs.sh install
    ./scripts/docs.sh generate
    ./scripts/docs.sh check
```

## Troubleshooting

### Common Issues

**godoc not found**:
```bash
./scripts/docs.sh install
```

**Port already in use**:
```bash
./scripts/docs.sh serve -p 8080
```

**Missing documentation**:
```bash
./scripts/docs.sh check
```

**Broken examples**:
```bash
go test -run Example
```

### Performance
- **Large codebases** may take time to generate static docs
- **Use caching** in CI/CD to speed up documentation builds
- **Generate incrementally** for faster development cycles

## Maintenance

### Regular Tasks
- **Review documentation coverage** monthly
- **Update examples** when APIs change
- **Fix broken links** and outdated information
- **Improve clarity** based on user feedback

### Quality Metrics
- **Coverage percentage** - Aim for >90% documented exports
- **Example tests** - Ensure all examples compile and run
- **Link validation** - Check internal and external links
- **User feedback** - Monitor questions about unclear documentation

This documentation system ensures that HyperChi maintains high-quality, comprehensive documentation that helps developers understand and effectively use the framework.