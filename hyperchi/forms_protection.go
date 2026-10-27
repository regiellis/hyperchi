package hyperchi

import (
	"errors"
	"fmt"
	"mime"
	"net/http"
	"time"
)

// FormProtectionOptions configures a URL-encoded browser form endpoint.
// Use RateLimiter.KeyGenerator to choose a site-wide or per-user budget.
type FormProtectionOptions struct {
	MaxBodyBytes   int64         // body limit; default 4 KiB
	TrustedOrigins []string      // cross-origin senders to accept, such as "https://example.com"
	RateLimiter    *RateLimiter  // optional; its ErrorHandler is replaced by OnError
	OnError        StatusHandler // renders refusals; default plain text
	// TooLargeStatus is the status for an oversized body: 413 (the default),
	// or 400 for an app that already answers 400.
	TooLargeStatus int
}

// ProtectForm returns middleware for a form endpoint. It refuses cross-origin
// browser requests (http.CrossOriginProtection, 403), applies the optional rate
// limit (429), requires an application/x-www-form-urlencoded body (415), and
// parses at most MaxBodyBytes of it. Refusals are no-store and noindex.
// Validation and the success redirect belong to the handler. It returns an
// error for invalid options or a malformed trusted origin.
func ProtectForm(options FormProtectionOptions) (func(http.Handler) http.Handler, error) {
	if options.MaxBodyBytes == 0 {
		options.MaxBodyBytes = 4 << 10
	}
	if options.MaxBodyBytes < 0 {
		return nil, fmt.Errorf("form body limit must be positive")
	}
	if options.TooLargeStatus == 0 {
		options.TooLargeStatus = http.StatusRequestEntityTooLarge
	}
	if options.TooLargeStatus != 400 && options.TooLargeStatus != 413 {
		return nil, fmt.Errorf("form body limit status must be 400 or 413")
	}
	deny := func(w http.ResponseWriter, r *http.Request, code int, err error) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Robots-Tag", "noindex")
		if options.OnError != nil {
			options.OnError(w, r, code, err)
		} else {
			http.Error(w, http.StatusText(code), code)
		}
	}
	origin := http.NewCrossOriginProtection()
	for _, trusted := range options.TrustedOrigins {
		if err := origin.AddTrustedOrigin(trusted); err != nil {
			return nil, fmt.Errorf("trusted form origin: %w", err)
		}
	}
	origin.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { deny(w, r, 403, nil) }))
	return func(next http.Handler) http.Handler {
		parsed := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if err != nil || media != "application/x-www-form-urlencoded" {
				deny(w, r, 415, err)
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, options.MaxBodyBytes)
			if err := r.ParseForm(); err != nil {
				code := http.StatusBadRequest
				var limit *http.MaxBytesError
				if errors.As(err, &limit) {
					code = options.TooLargeStatus
				}
				deny(w, r, code, err)
				return
			}
			next.ServeHTTP(w, r)
		})
		var h http.Handler = parsed
		if options.RateLimiter != nil {
			h = options.RateLimiter.middleware(func(w http.ResponseWriter, r *http.Request, _ time.Duration) {
				deny(w, r, http.StatusTooManyRequests, nil)
			})(h)
		}
		return origin.Handler(h)
	}, nil
}

// ProtectForm is the package ProtectForm with the app's error pipeline as the
// default OnError, so a refused form renders like every other denial (JSON,
// htmx fragment, or error page through OnError/DefaultError).
func (g *HyperChi) ProtectForm(options FormProtectionOptions) (Middleware, error) {
	if options.OnError == nil {
		options.OnError = func(w http.ResponseWriter, r *http.Request, code int, err error) {
			g.respondStatus(w, r, code, err)
		}
	}
	return ProtectForm(options)
}
