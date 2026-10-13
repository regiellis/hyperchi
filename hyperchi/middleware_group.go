package hyperchi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/regiellis/hyperchi/hyperchi/config"
	"github.com/regiellis/hyperchi/hyperchi/security"
)

// AutoMiddleware installs chi's RequestID, Middleware.ClientIP, and the
// Middleware.Default stack (every app already answers HEAD on GET routes),
// then Middleware.RateLimit(100, time.Minute) when Config.Security.RateLimit
// is set, Middleware.CSRF when
// Config.Security.CSRF is set (the default), and, outside development,
// Middleware.Compress inside Recovery. With CSRF on, every unsafe request
// needs the token templates read as .csrfToken: send it in the X-CSRF-Token
// header (hx-headers on the body covers every htmx request) or a csrf_token
// form field. Development sends responses uncompressed so they read
// plainly in browser tools and curl. It uses chi's Use, so call it before
// registering routes.
//
// Compression is safe for the CSRF token because Middleware.CSRF masks it
// afresh on every render, so a page that reflects request input never repeats
// it for the BREACH attack to recover. A page that renders another per-user
// secret unmasked next to reflected input (a session-bound value, an API key)
// is still exposed when compressed; an app with such pages assembles its own
// stack from Middleware.Default instead of calling AutoMiddleware.
func (g *HyperChi) AutoMiddleware() *HyperChi {
	g.router.Use(middleware.RequestID)
	// The client IP is the TCP peer unless Config.Security.TrustedProxies names
	// the proxies allowed to report it (see Middleware.ClientIP and
	// hyperchi.ClientIP). Never RealIP: it trusts headers any client can set.
	// Panics are recovered by Middleware.Recovery in the default stack below.
	g.router.Use(g.Middleware.ClientIP())

	g.Use(g.Middleware.Default()...)
	if g.Middleware.config.Security.RateLimit {
		g.Use(g.Middleware.RateLimit(100, time.Minute))
	}
	if g.Middleware.config.Security.CSRF {
		g.Use(g.Middleware.CSRF())
	}
	if !g.Middleware.config.IsDevelopment() {
		// After Default, so Recovery wraps it and a panic still gets a clean
		// error page. SSE, WebSocket upgrades, and HEAD pass through it.
		g.Use(g.Middleware.Compress())
	}

	return g
}

// MiddlewareGroup builds the framework's middleware from the app's
// configuration. Reach it as app.Middleware.
type MiddlewareGroup struct {
	corsOrigins string
	config      *config.Config
	errors      *ErrorPageHandler
	respond     StatusHandler // the app's error pipeline (respondStatus), when attached to an app
}

func newMiddlewareGroup(configs ...*config.Config) *MiddlewareGroup {
	cfg := config.FromEnv()
	if len(configs) > 0 {
		cfg = configs[0]
	}
	origins := cfg.Security.CORSOrigins
	if origins == "" && cfg.IsDevelopment() {
		origins = "*"
	}
	return &MiddlewareGroup{corsOrigins: origins, config: cfg, errors: NewErrorPageHandler(cfg.IsDevelopment())}
}

// HTMX sets HX-Vary on every response and echoes htmx request details in the
// X-HTMX-Request and X-Current-URL response headers.
func (m *MiddlewareGroup) HTMX() func(http.Handler) http.Handler {
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

// CORS sends CORS headers, including the htmx request and response headers,
// and answers preflight OPTIONS requests with 204. The allowed origin is
// Config.Security.CORSOrigins (CORS_ORIGINS). When that is empty, development
// allows "*" and production sends no CORS headers.
func (m *MiddlewareGroup) CORS() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := m.corsOrigins
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

// Logger logs each request's method and path, plus the htmx target, source,
// and current URL for htmx requests.
func (m *MiddlewareGroup) Logger() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("HX-Request") == "true" {
				log.Printf("HTMX: %s %s | Target: %s | Source: %s | Current: %s",
					r.Method, r.URL.Path,
					r.Header.Get("HX-Target"),
					r.Header.Get("HX-Source"),
					r.Header.Get("HX-Current-URL"))
			} else {
				log.Printf("%s %s", r.Method, r.URL.Path)
			}

			next.ServeHTTP(w, r)
		})
	}
}

// Security sets nosniff, X-Frame-Options DENY, and a Referrer-Policy on every
// response. Outside development it also sends a Content-Security-Policy
// (Config.Security.CSPPolicy, from CSP_POLICY, or a built-in default), a
// Permissions-Policy, and Strict-Transport-Security when Config.Security.HSTS
// is set. The configuration is read once, when the middleware is built.
func (m *MiddlewareGroup) Security() func(http.Handler) http.Handler {
	cspPolicy := m.config.Security.CSPPolicy
	if cspPolicy == "" {
		// 'unsafe-eval' is required by:
		//   - HyperChi directives (hc-data, hc-on, etc.) which compile expressions via new Function()
		//   - htmx 4 features that rely on eval: hx-on, event filters, js:/javascript: prefixes
		// If your app avoids those features and prefers stricter CSP, set CSP_POLICY
		// to override and remove 'unsafe-eval'.
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

			if !m.config.IsDevelopment() {
				w.Header().Set("Content-Security-Policy", cspPolicy)
				if m.config.Security.HSTS != "" {
					w.Header().Set("Strict-Transport-Security", m.config.Security.HSTS)
				}
				w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
			}

			next.ServeHTTP(w, r)
		})
	}
}

// deny answers a middleware denial through the app's error pipeline
// (respondStatus: OnError or DefaultError), or the built-in error page when
// the group is not attached to an app. Both send Cache-Control: no-store.
func (m *MiddlewareGroup) deny(w http.ResponseWriter, r *http.Request, code int, err error) {
	if m.respond != nil {
		m.respond(w, r, code, err)
		return
	}
	m.errors.HandleStatus(err, w, r, code)
}

// CSRF is double-submit cookie protection. A safe request (GET, HEAD, OPTIONS,
// TRACE) gets a csrf_token cookie when it has no well-formed one, and the token
// goes into the request context for GetCSRFToken and c.CSRFToken.
//
// Pages never carry the cookie's value itself. Each call to GetCSRFToken (and
// c.CSRFToken) returns the token masked with a fresh one-time pad, so no two
// renders contain the same bytes and a compressed page cannot leak it to the
// BREACH attack. The cookie keeps the raw token.
//
// Any other request must send a token back: a masked token from a page in the
// X-CSRF-Token header or the csrf_token form field, or the cookie's raw value
// in the X-CSRF-Token header (for scripts that read the cookie; a request
// header is not echoed into a response body). The form field accepts masked
// tokens only. Tokens are compared in constant time. A missing or mismatched
// token is a 403 rendered by the app's error pipeline (OnError or
// DefaultError), so htmx requests get the error fragment, API clients JSON,
// and browsers the error page. Installing it twice is harmless: an inner layer
// sees the outer one's token and passes the request through.
func (m *MiddlewareGroup) CSRF() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// An outer CSRF layer (AutoMiddleware's, say) already checked this
			// request and stored its token; a second one would only issue a
			// competing cookie.
			if token, ok := r.Context().Value(csrfTokenCtxKey).(string); ok && token != "" {
				next.ServeHTTP(w, r)
				return
			}
			safeMethods := []string{"GET", "HEAD", "OPTIONS", "TRACE"}
			isSafe := false
			for _, method := range safeMethods {
				if r.Method == method {
					isSafe = true
					break
				}
			}

			if isSafe {
				// Reuse the visitor's token: a page's own asset and htmx GETs
				// pass through here too, and a new token on each would replace
				// the cookie under the token already rendered into the page.
				token := ""
				if cookie, err := r.Cookie("csrf_token"); err == nil && wellFormedCSRFToken(cookie.Value) {
					token = cookie.Value
				}
				if token == "" {
					fresh, err := security.GenerateSecureToken(32)
					if err != nil {
						m.deny(w, r, http.StatusInternalServerError, fmt.Errorf("generate CSRF token: %w", err))
						return
					}
					token = fresh
					http.SetCookie(w, &http.Cookie{
						Name:     "csrf_token",
						Value:    token,
						Path:     "/",
						Secure:   !m.config.IsDevelopment(), // plain HTTP works in development
						HttpOnly: false,                     // page scripts read it to send X-CSRF-Token
						SameSite: http.SameSiteStrictMode,
					})
				}

				ctx := context.WithValue(r.Context(), csrfTokenCtxKey, token)
				r = r.WithContext(ctx)

				next.ServeHTTP(w, r)
				return
			}

			cookieToken := ""
			if cookie, err := r.Cookie("csrf_token"); err == nil {
				cookieToken = cookie.Value
			}

			if cookieToken == "" {
				m.deny(w, r, http.StatusForbidden, Forbidden("CSRF token missing"))
				return
			}

			// A multipart form needs ParseMultipartForm: ParseForm alone fills
			// r.Form from the query string only, and FormValue then never reads
			// the body.
			requestToken := r.Header.Get("X-CSRF-Token")
			fromHeader := requestToken != ""
			if !fromHeader {
				field, err := csrfFormField(w, r)
				if err != nil {
					var maxErr *http.MaxBytesError
					if errors.As(err, &maxErr) {
						m.deny(w, r, http.StatusRequestEntityTooLarge, NewHTTPError(http.StatusRequestEntityTooLarge, "The request body is too large."))
						return
					}
					m.deny(w, r, http.StatusBadRequest, BadRequest("The form could not be read.").Wrap(err))
					return
				}
				requestToken = field
			}

			if requestToken == "" {
				m.deny(w, r, http.StatusForbidden, Forbidden("CSRF token not found in request"))
				return
			}

			if !csrfTokenMatches(cookieToken, requestToken, fromHeader) {
				m.deny(w, r, http.StatusForbidden, Forbidden("CSRF token mismatch"))
				return
			}

			ctx := context.WithValue(r.Context(), csrfTokenCtxKey, cookieToken)
			r = r.WithContext(ctx)
			next.ServeHTTP(w, r)
		})
	}
}

// RateLimit allows each client IP requestsPerWindow requests per
// windowDuration, finding the client through Config.Security.TrustedProxies. A
// denial is a 429 rendered by the app's error pipeline.
func (m *MiddlewareGroup) RateLimit(requestsPerWindow int, windowDuration time.Duration) func(http.Handler) http.Handler {
	cfg := DefaultRateLimitConfig()
	cfg.RequestsPerWindow = requestsPerWindow
	cfg.WindowDuration = windowDuration
	cfg.TrustedProxies = m.config.Security.TrustedProxies
	cfg.ErrorHandler = func(w http.ResponseWriter, r *http.Request, _ time.Duration) {
		m.deny(w, r, http.StatusTooManyRequests, nil)
	}
	return RateLimitWithConfig(cfg)
}

// ClientIP records the client address that hyperchi.ClientIP returns: the TCP
// peer, or, when the peer is in Config.Security.TrustedProxies, the address
// X-Forwarded-For reports. r.RemoteAddr is left alone. It panics on a malformed
// proxy CIDR.
func (m *MiddlewareGroup) ClientIP() func(http.Handler) http.Handler {
	if len(m.config.Security.TrustedProxies) == 0 {
		return middleware.ClientIPFromRemoteAddr
	}
	prefixes := make([]netip.Prefix, 0, len(m.config.Security.TrustedProxies))
	cidrs := make([]string, 0, len(m.config.Security.TrustedProxies))
	for _, raw := range m.config.Security.TrustedProxies {
		cidr := strings.TrimSpace(raw)
		prefixes = append(prefixes, netip.MustParsePrefix(cidr))
		cidrs = append(cidrs, cidr)
	}
	forwarded := middleware.ClientIPFromXFF(cidrs...)
	return func(next http.Handler) http.Handler {
		fromPeer := middleware.ClientIPFromRemoteAddr(next)
		fromForwarded := middleware.ClientIPFromRemoteAddr(forwarded(next))
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			host, _, err := net.SplitHostPort(r.RemoteAddr)
			if err != nil {
				host = r.RemoteAddr
			}
			peer, err := netip.ParseAddr(host)
			if err == nil {
				for _, prefix := range prefixes {
					if prefix.Contains(peer.WithZone("").Unmap()) {
						fromForwarded.ServeHTTP(w, r)
						return
					}
				}
			}
			fromPeer.ServeHTTP(w, r)
		})
	}
}

// GetCSRFToken returns the request's CSRF token, masked for a page, or ""
// outside Middleware.CSRF. Every call returns a different string that
// Middleware.CSRF accepts (see MiddlewareGroup.CSRF), so call it once per
// render, not once per request.
func GetCSRFToken(r *http.Request) string {
	if token, ok := r.Context().Value(csrfTokenCtxKey).(string); ok && token != "" {
		return maskCSRFToken(token)
	}
	return ""
}

// maskCSRFToken returns base64url(pad || token XOR pad) with a fresh random
// pad as long as the token, so the same token never renders the same way
// twice.
func maskCSRFToken(token string) string {
	raw := []byte(token)
	buf := make([]byte, 2*len(raw))
	pad, masked := buf[:len(raw)], buf[len(raw):]
	if _, err := rand.Read(pad); err != nil {
		// crypto/rand.Read does not return errors on supported platforms; a
		// page without a usable token must not be rendered.
		panic(fmt.Sprintf("hyperchi: mask CSRF token: %v", err))
	}
	for i := range raw {
		masked[i] = raw[i] ^ pad[i]
	}
	return base64.RawURLEncoding.EncodeToString(buf)
}

// unmaskCSRFToken reverses maskCSRFToken. ok is false when sent is not
// base64url of an even, non-zero number of bytes.
func unmaskCSRFToken(sent string) (token []byte, ok bool) {
	buf, err := base64.RawURLEncoding.DecodeString(sent)
	if err != nil || len(buf) == 0 || len(buf)%2 != 0 {
		return nil, false
	}
	n := len(buf) / 2
	token = make([]byte, n)
	for i := range n {
		token[i] = buf[i] ^ buf[n+i]
	}
	return token, true
}

// csrfTokenMatches reports whether sent proves knowledge of the cookie token:
// it unmasks to the cookie value, or, when allowRaw, it is the cookie value.
// Both comparisons run in constant time for equal lengths.
func csrfTokenMatches(cookie, sent string, allowRaw bool) bool {
	match := 0
	if allowRaw {
		match |= subtle.ConstantTimeCompare([]byte(cookie), []byte(sent))
	}
	if token, ok := unmaskCSRFToken(sent); ok {
		match |= subtle.ConstantTimeCompare(token, []byte(cookie))
	}
	return match == 1
}

// Development sets X-Dev-Mode and X-Framework response headers and logs htmx
// requests with their target and source.
func (m *MiddlewareGroup) Development() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Dev-Mode", "true")
			w.Header().Set("X-Framework", "HyperChi")

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

// NoCache marks responses to htmx requests uncacheable. Other requests are
// left alone.
func (m *MiddlewareGroup) NoCache() func(http.Handler) http.Handler {
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

// Compress gzips responses for clients that accept it, with the defaults of
// CompressWithOptions (default level, 1 KiB minimum). SSE streams, WebSocket
// upgrades, HEAD requests, small bodies, and compressed media pass through.
// AutoMiddleware installs it outside development. Elsewhere, install it inside
// Recovery so a panic still gets a clean error page:
//
//	app.Use(app.Middleware.Default()...) // includes Recovery
//	app.Use(app.Middleware.Compress())   // runs inside it
//
// A second Compress inside the first is harmless: the outer one leaves a
// response that already has a Content-Encoding alone. See CompressWithOptions
// for what is safe to compress.
func (m *MiddlewareGroup) Compress() func(http.Handler) http.Handler {
	return CompressWithOptions(CompressOptions{})
}

// Recovery recovers panics and renders them through the app's error pipeline
// (see ErrorPageHandler.RecoveryMiddleware).
func (m *MiddlewareGroup) Recovery() func(http.Handler) http.Handler {
	return m.errors.RecoveryMiddleware()
}

// RequestID gives each request an ID: the inbound X-Request-ID header, or a
// random one. The ID is echoed in the X-Request-ID response header and stored
// in the request context.
func (m *MiddlewareGroup) RequestID() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get("X-Request-ID")
			if id == "" {
				var b [8]byte
				if _, err := rand.Read(b[:]); err == nil {
					id = hex.EncodeToString(b[:])
				}
			}
			if id != "" {
				w.Header().Set("X-Request-ID", id)
				r = r.WithContext(context.WithValue(r.Context(), requestIDCtxKey, id))
			}
			next.ServeHTTP(w, r)
		})
	}
}

// Default returns Recovery, Logger, HTMX, CORS (when Config.Security.CORS is
// set), and Security, plus
// Development in development mode.
func (m *MiddlewareGroup) Default() []func(http.Handler) http.Handler {
	middlewares := []func(http.Handler) http.Handler{
		m.Recovery(),
		m.Logger(),
		m.HTMX(),
	}
	if m.config.Security.CORS {
		middlewares = append(middlewares, m.CORS())
	}
	middlewares = append(middlewares, m.Security())

	if m.config.IsDevelopment() {
		middlewares = append(middlewares, m.Development())
	}

	return middlewares
}

// CORS installs Middleware.CORS on every route, so browsers on the configured
// origins can call the app. Call it before registering routes:
//
//	app := hyperchi.New()
//	app.CORS()
//	app.Get("/api/users", listUsers)
func (g *HyperChi) CORS() *HyperChi {
	g.Use(g.Middleware.CORS())
	log.Printf("hyperchi: CORS enabled for all routes (origins: %s)", g.Middleware.corsOrigins)
	return g
}

// BasicAuth requires HTTP Basic credentials on every route. Credentials are
// compared in constant time, and a refusal is a 401 with WWW-Authenticate,
// rendered by the app's error pipeline. It panics on an empty username or
// password. It uses chi's Use, so call it before registering routes:
//
//	app := hyperchi.New()
//	app.BasicAuth("admin", os.Getenv("ADMIN_PASSWORD"))
//	app.Get("/admin", admin)
func (g *HyperChi) BasicAuth(username, password string) *HyperChi {
	if username == "" || password == "" {
		panic("hyperchi: BasicAuth needs a non-empty username and password")
	}
	wantUser := sha256.Sum256([]byte(username))
	wantPass := sha256.Sum256([]byte(password))
	g.router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, pass, ok := r.BasicAuth()
			gotUser := sha256.Sum256([]byte(user))
			gotPass := sha256.Sum256([]byte(pass))
			// Compare fixed-size digests in constant time, both of them every
			// time, so neither the length nor which part was wrong leaks.
			userOK := subtle.ConstantTimeCompare(gotUser[:], wantUser[:])
			passOK := subtle.ConstantTimeCompare(gotPass[:], wantPass[:])
			if !ok || userOK&passOK != 1 {
				w.Header().Set("WWW-Authenticate", `Basic realm="Restricted", charset="UTF-8"`)
				g.respondStatus(w, r, http.StatusUnauthorized, Unauthorized("Authentication required"))
				return
			}
			next.ServeHTTP(w, r)
		})
	})
	log.Printf("hyperchi: basic auth enabled for all routes")
	return g
}

// wellFormedCSRFToken reports whether v has the shape of a token this
// middleware issues (URL-safe base64 of 32 random bytes), so a stale or
// injected cookie value is replaced rather than echoed into pages.
func wellFormedCSRFToken(v string) bool {
	if len(v) != 44 {
		return false
	}
	for _, r := range v {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_', r == '=':
		default:
			return false
		}
	}
	return true
}

// csrfFormField reads the csrf_token form field from a URL-encoded or
// multipart body, capping a multipart body like Bind does.
func csrfFormField(w http.ResponseWriter, r *http.Request) (string, error) {
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err == nil && mt == "multipart/form-data" {
		r.Body = http.MaxBytesReader(w, r.Body, maxMultipartBody)
		if err := r.ParseMultipartForm(maxMultipartBody); err != nil {
			return "", err
		}
		return r.FormValue("csrf_token"), nil
	}
	if err := r.ParseForm(); err != nil {
		return "", err
	}
	return r.FormValue("csrf_token"), nil
}
