// Package middleware is plain net/http middleware for chi routers: CSRF
// protection, htmx response headers, CORS, security headers, and aliases for
// the core rate limiter.
//
// In a HyperChi app, use the methods on app.Middleware instead. They read the
// app's configuration and send denials through its error pipeline, while this
// package reads GO_ENV, CORS_ORIGINS, and CSP_POLICY directly and answers
// errors itself.
package middleware

import (
	"log"
	"net/http"
	"os"
)

// HTMX sets response headers that mark htmx traffic: HX-Vary on every
// response, X-HTMX-Request: true for htmx requests, and X-Current-URL echoing
// the HX-Current-URL request header. HX-Vary is not a header caches read. To
// keep caches from mixing full pages and fragments, send Vary: HX-Request.
func HTMX() func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("HX-Vary", "HX-Request, HX-Target, HX-Trigger")

			if r.Header.Get("HX-Request") == "true" {
				w.Header().Set("X-HTMX-Request", "true")
			}

			if r.Header.Get("HX-Current-URL") != "" {
				w.Header().Set("X-Current-URL", r.Header.Get("HX-Current-URL"))
			}

			next.ServeHTTP(w, r)
		})
	}
}

// CORS sets CORS headers that allow the htmx request headers and expose the
// htmx response headers, and answers OPTIONS preflights with 204.
//
// The allowed origin is CORS_ORIGINS, read once when CORS is called and sent
// verbatim as Access-Control-Allow-Origin, so it must be a single origin or
// "*". When CORS_ORIGINS is unset, development (see IsDevelopment) allows "*"
// and production sends no CORS headers at all.
func CORS() func(next http.Handler) http.Handler {
	origin := os.Getenv("CORS_ORIGINS")
	if origin == "" && IsDevelopment() {
		origin = "*"
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if origin == "" {
				next.ServeHTTP(w, r)
				return
			}

			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With, HX-Request, HX-Target, HX-Current-URL, HX-Trigger, HX-CSRF-Token")
			w.Header().Set("Access-Control-Expose-Headers", "HX-Trigger, HX-Push-Url, HX-Redirect, HX-Refresh")
			if origin != "*" {
				w.Header().Set("Vary", "Origin")
			}

			if r.Method == "OPTIONS" {
				w.Header().Set("Access-Control-Max-Age", "86400")
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// Development sets X-Dev-Mode, X-Framework, and X-Router response headers and
// logs each htmx request's method, path, target, and source. It reveals the
// stack, so install it only in development:
//
//	if middleware.IsDevelopment() {
//		r.Use(middleware.Development())
//	}
func Development() func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Dev-Mode", "true")
			w.Header().Set("X-Framework", "HyperChi")
			w.Header().Set("X-Router", "Chi")

			if r.Header.Get("HX-Request") == "true" {
				log.Printf("HTMX Request: %s %s (Target: %s, Source: %s)",
					r.Method, r.URL.Path,
					r.Header.Get("HX-Target"),
					r.Header.Get("HX-Source"))
			}

			next.ServeHTTP(w, r)
		})
	}
}

// Security sets X-Content-Type-Options: nosniff, X-Frame-Options: DENY, and
// Referrer-Policy: strict-origin-when-cross-origin on every response. Outside
// development (see IsDevelopment) it also sets a Content-Security-Policy,
// Permissions-Policy denying camera, microphone, and geolocation, and
// Strict-Transport-Security for two years with includeSubDomains and preload.
// Preload commits the domain and every subdomain to HTTPS, so serve it only in
// production behind TLS.
//
// The CSP is CSP_POLICY, read once when Security is called, or the default
// below.
func Security() func(next http.Handler) http.Handler {
	cspPolicy := os.Getenv("CSP_POLICY")
	if cspPolicy == "" {
		// 'unsafe-eval' is needed by HyperChi directives (hc-data, hc-on),
		// which compile expressions with new Function(), and by htmx 4's
		// hx-on, event filters, and js: prefixes. An app that uses none of
		// them can set CSP_POLICY without it.
		cspPolicy = "default-src 'self'; " +
			"script-src 'self' 'unsafe-eval'; " +
			"style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; " +
			"font-src 'self' https://fonts.gstatic.com; " +
			"img-src 'self' data: https:; " +
			"connect-src 'self';"
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("X-Frame-Options", "DENY")
			w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")

			if !IsDevelopment() {
				w.Header().Set("Content-Security-Policy", cspPolicy)
				w.Header().Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains; preload")
				w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
			}

			next.ServeHTTP(w, r)
		})
	}
}

// NoCache marks htmx responses uncacheable (Cache-Control no-store, Pragma,
// Expires), so a browser never serves a stale fragment. Other responses are
// left alone.
func NoCache() func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("HX-Request") == "true" {
				w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
				w.Header().Set("Pragma", "no-cache")
				w.Header().Set("Expires", "0")
			}

			next.ServeHTTP(w, r)
		})
	}
}

// RequestLogger logs each htmx request's method, path, target, source, and
// current URL with the standard log package. Other requests are not logged.
func RequestLogger() func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("HX-Request") == "true" {
				log.Printf("HTMX: %s %s | Target: %s | Source: %s | Current: %s",
					r.Method, r.URL.Path,
					r.Header.Get("HX-Target"),
					r.Header.Get("HX-Source"),
					r.Header.Get("HX-Current-URL"))
			}

			next.ServeHTTP(w, r)
		})
	}
}

// IsDevelopment reports whether GO_ENV is unset, "development", or "dev". It
// reads GO_ENV only: unlike the framework's config it ignores ENVIRONMENT, so
// ENVIRONMENT=production alone leaves this package in development mode.
func IsDevelopment() bool {
	env := os.Getenv("GO_ENV")
	return env == "" || env == "development" || env == "dev"
}

// Default returns HTMX, CORS, Security, NoCache, and RequestLogger, plus
// Development when IsDevelopment is true.
//
//	r.Use(middleware.Default()...)
func Default() []func(next http.Handler) http.Handler {
	middlewares := []func(next http.Handler) http.Handler{
		HTMX(),
		CORS(),
		Security(),
		NoCache(),
		RequestLogger(),
	}

	if IsDevelopment() {
		middlewares = append(middlewares, Development())
	}

	return middlewares
}
