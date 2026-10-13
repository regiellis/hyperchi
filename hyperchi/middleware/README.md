# HyperChi Middleware

This package provides HTMX-specific middleware for Chi router, designed to enhance HyperChi applications with server-side rendering and HTMX functionality.

It is a compatibility package for plain chi routers, imported as
`github.com/regiellis/hyperchi/hyperchi/middleware`. In a HyperChi app, use the methods on
`app.Middleware` (`HTMX`, `CORS`, `Security`, `CSRF`, `RateLimit`, `Recovery`, `Default`, ...)
or `app.AutoMiddleware()`: they read the app's configuration and send denials through the
app's error handler. This package reads `GO_ENV`, `CORS_ORIGINS`, and `CSP_POLICY` directly.
It also exposes `CSRF`/`CSRFWithConfig` and the core rate limiter (`RateLimit`,
`RateLimitWithConfig`, `NewRateLimiterE`).

## Available Middleware

### Core Middleware

#### `HTMX()`
Adds response headers that mark htmx traffic.
- Sets an informational `HX-Vary` header. Caches do not read it, so it does not keep them
  from mixing full pages and fragments: send `Vary: HX-Request` for that
- Marks htmx requests with an `X-HTMX-Request: true` response header
- Echoes the `HX-Current-URL` request header as `X-Current-URL`

```go
r.Use(middleware.HTMX())
```

#### `CORS()`
Handles CORS for HTMX requests with proper headers.
- Allows HTMX-specific headers
- Handles preflight requests
- Configures proper CORS for HTMX applications

```go
r.Use(middleware.CORS())
```

#### `Security()`
Adds security headers compatible with HTMX applications.
- Always: `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy`
- Outside development: a Content-Security-Policy (`CSP_POLICY` overrides the default), HSTS,
  and a `Permissions-Policy`

```go
r.Use(middleware.Security())
```

#### `NoCache()`
Adds no-cache headers specifically for HTMX requests.
- Only affects HTMX requests
- Ensures dynamic content isn't cached
- Preserves normal caching for static assets

```go
r.Use(middleware.NoCache())
```

### Development Middleware

#### `Development()`
Adds development-specific helpers and enhanced logging.
- Debug headers for development
- Enhanced HTMX request logging
- Framework identification headers

```go
if middleware.IsDevelopment() {
    r.Use(middleware.Development())
}
```

#### `RequestLogger()`
Enhanced logging specifically for HTMX requests.
- Logs HTMX-specific headers
- Includes target, trigger, and current URL
- Helpful for debugging HTMX interactions

```go
r.Use(middleware.RequestLogger())
```

### Utility Functions

#### `IsDevelopment()`
Checks if the application is running in development mode.

```go
if middleware.IsDevelopment() {
    // Development-only code
}
```

#### `Default()`
Returns a sensible middleware stack for HyperChi applications.

```go
for _, mw := range middleware.Default() {
    r.Use(mw)
}
```

## Usage Examples

### Basic Setup

```go
package main

import (
    "github.com/go-chi/chi/v5"
    "github.com/go-chi/chi/v5/middleware"
    hyperchiMiddleware "github.com/regiellis/hyperchi/hyperchi/middleware"
)

func main() {
    r := chi.NewRouter()
    
    // Standard Chi middleware
    r.Use(middleware.Logger)
    r.Use(middleware.Recoverer)
    r.Use(middleware.Compress(5))
    
    // HyperChi middleware
    r.Use(hyperchiMiddleware.HTMX())
    r.Use(hyperchiMiddleware.CORS())
    r.Use(hyperchiMiddleware.Security())
    
    // Development middleware
    if hyperchiMiddleware.IsDevelopment() {
        r.Use(hyperchiMiddleware.Development())
    }
    
    // Your routes...
}
```

### Using Default Stack

```go
func main() {
    r := chi.NewRouter()
    
    // Standard Chi middleware
    r.Use(middleware.Logger)
    r.Use(middleware.Recoverer)
    
    // All HyperChi middleware with sensible defaults
    for _, mw := range hyperchiMiddleware.Default() {
        r.Use(mw)
    }
    
    // Your routes...
}
```

### Custom Configuration

```go
func main() {
    r := chi.NewRouter()
    
    // Only specific middleware
    r.Use(hyperchiMiddleware.HTMX())
    r.Use(hyperchiMiddleware.RequestLogger())
    
    if os.Getenv("ENABLE_CORS") == "true" {
        r.Use(hyperchiMiddleware.CORS())
    }
    
    // Your routes...
}
```

## Middleware Stack Recommendations

### Development
```go
r.Use(middleware.Logger)
r.Use(middleware.Recoverer)
r.Use(middleware.RequestID)
r.Use(middleware.ClientIPFromRemoteAddr) // the TCP peer; read it with hyperchi.ClientIP
r.Use(middleware.Heartbeat("/ping"))
r.Use(middleware.Compress(5))
r.Use(middleware.RedirectSlashes)
r.Use(middleware.CleanPath)
r.Use(middleware.Timeout(60 * time.Second))

// HyperChi middleware
for _, mw := range hyperchiMiddleware.Default() {
    r.Use(mw)
}
```

### Production
```go
r.Use(middleware.Logger)
r.Use(middleware.Recoverer)
r.Use(middleware.RequestID)
// Behind a reverse proxy, name its addresses so only it can set the client IP.
// Never use middleware.RealIP: it trusts forwarding headers any client can send.
r.Use(middleware.ClientIPFromXFF("10.0.0.0/8"))
r.Use(middleware.Heartbeat("/health"))
r.Use(middleware.Compress(5))
r.Use(middleware.RedirectSlashes)
r.Use(middleware.CleanPath)
r.Use(middleware.Timeout(30 * time.Second))
r.Use(middleware.Throttle(1000))

// HyperChi middleware (without development middleware)
r.Use(hyperchiMiddleware.HTMX())
r.Use(hyperchiMiddleware.CORS())
r.Use(hyperchiMiddleware.Security())
r.Use(hyperchiMiddleware.NoCache())
r.Use(hyperchiMiddleware.RequestLogger())
```

## Environment Variables

The middleware respects the following environment variables:

- `GO_ENV`: anything other than unset, `development`, or `dev` disables the development
  middleware, wildcard CORS, and relaxed headers. This package does not read `ENVIRONMENT`.
- `CORS_ORIGINS`: allowed origins outside development; with it unset, no CORS headers are sent
- `CSP_POLICY`: replaces the default Content-Security-Policy
- `ENABLE_CORS` in the custom configuration example above is the application's own variable

## HTMX Headers Handled

The middleware properly handles these HTMX headers:

**Request Headers:**
- `HX-Request`: Identifies HTMX requests
- `HX-Target`: Target element for the response
- `HX-Source`: Element that triggered the request (htmx 4)
- `HX-Current-URL`: Current URL of the browser

**Response Headers:**
- `HX-Trigger`: Trigger events after response
- `HX-Push-Url`: Push URL to browser history
- `HX-Redirect`: Redirect the browser
- `HX-Refresh`: Refresh the page

`HX-Vary`, which `HTMX()` sets, is not an htmx header and caches ignore it. Use
`Vary: HX-Request` on responses that differ between full-page and htmx requests.

## Security Considerations

The security middleware includes:
- Content type sniffing protection
- Frame options to prevent clickjacking
- A referrer policy
- Outside development, a Content Security Policy, HSTS, and a Permissions-Policy

CSRF protection is separate: `CSRF()` or `CSRFWithConfig(...)`. `CSRFTemplateHelper` writes
the token under the config's field and header names. `TokenHXHeaders(r)` returns an
`hx-headers:inherited` attribute: htmx 4 inherits only attributes marked `:inherited`, so on
`<body>` it sends the token with every htmx request issued inside.

The default CSP allows:
- `'self'` for most resources
- `'unsafe-eval'` for scripts, which HyperChi `hc-*` directives and htmx event handlers need
- `'unsafe-inline'` for styles (needed for dynamic styling)
- `https://fonts.googleapis.com` and `https://fonts.gstatic.com` for Google Fonts
- `data:` and `https:` images

Scripts load from `'self'` only, so serve htmx from your own origin (or set `CSP_POLICY`).