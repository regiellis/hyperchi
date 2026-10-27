package middleware

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"strings"

	"github.com/regiellis/hyperchi/hyperchi/security"
)

// CSRFConfig configures CSRFWithConfig. Zero fields take the defaults noted
// on each.
type CSRFConfig struct {
	// TokenLength is the random nonce length in bytes (default 32, minimum
	// 16). The token also carries a 32-byte HMAC.
	TokenLength int

	// CookieName is the name of the CSRF cookie (default: "csrf_token")
	CookieName string

	// HeaderName is the name of the CSRF header (default: "X-CSRF-Token")
	HeaderName string

	// FormFieldName is the name of the CSRF form field (default: "csrf_token")
	FormFieldName string

	// Secure sets the cookie's Secure flag. DefaultCSRFConfig sets it, and a zero
	// CSRFConfig leaves it off. Browsers do not send a Secure cookie over
	// plain http://, so local development without TLS needs it false.
	Secure bool

	// SameSite sets the cookie's SameSite attribute (DefaultCSRFConfig:
	// SameSiteStrictMode).
	SameSite http.SameSite

	// SecretKey is the HMAC key that makes tokens self-validating.
	//
	// A token is base64url(nonce || HMAC-SHA256(SecretKey, nonce)), with the
	// request's user ID mixed into the HMAC when there is one. Validation
	// recomputes the HMAC and compares in constant time, so nobody without
	// SecretKey can mint a token the server accepts.
	//
	// When empty, a random key is generated once, when the middleware is built.
	// Tokens then stop validating after a restart and do not carry across
	// instances. Behind a load balancer, or to survive restarts, set a stable
	// secret from the environment or a secret manager.
	SecretKey []byte

	// Domain sets the cookie's Domain attribute (default: none).
	Domain string

	// Path sets the cookie's Path attribute (default: "/").
	Path string

	// MaxAge sets the cookie's Max-Age in seconds (default 0, a session cookie).
	MaxAge int

	// SkipMethods lists the methods that are not checked. They issue the
	// token instead (default: GET, HEAD, OPTIONS, TRACE).
	SkipMethods []string

	// ErrorHandler answers a failed check (default: a plain-text 403).
	ErrorHandler func(w http.ResponseWriter, r *http.Request, err error)

	// TokenExtractor reads the submitted token from a request (default: the
	// HeaderName header, then the FormFieldName field, then HX-CSRF-Token).
	TokenExtractor func(r *http.Request) string
}

// DefaultCSRFConfig returns the defaults: 32-byte nonce, csrf_token cookie and
// form field, X-CSRF-Token header, Secure, SameSite=Strict, Path "/", and a
// session cookie.
func DefaultCSRFConfig() *CSRFConfig {
	return &CSRFConfig{
		TokenLength:    32,
		CookieName:     "csrf_token",
		HeaderName:     "X-CSRF-Token",
		FormFieldName:  "csrf_token",
		Secure:         true,
		SameSite:       http.SameSiteStrictMode,
		Domain:         "",
		Path:           "/",
		MaxAge:         0,
		SkipMethods:    []string{"GET", "HEAD", "OPTIONS", "TRACE"},
		ErrorHandler:   defaultCSRFErrorHandler,
		TokenExtractor: nil,
	}
}

// contextKey is private so this package's context keys cannot collide with
// others.
type contextKey string

const (
	csrfTokenKey contextKey = "csrf_token"
	csrfNamesKey contextKey = "csrf_names"
)

// csrfNames are the configured field and header names, stored beside the
// token so CSRFTemplateHelper writes the names the middleware reads back.
type csrfNames struct {
	field  string
	header string
}

// withCSRFToken stores the token and config's names in r's context.
func withCSRFToken(r *http.Request, token string, config *CSRFConfig) *http.Request {
	ctx := context.WithValue(r.Context(), csrfTokenKey, token)
	ctx = context.WithValue(ctx, csrfNamesKey, csrfNames{field: config.FormFieldName, header: config.HeaderName})
	return r.WithContext(ctx)
}

// namesFromRequest returns the names the CSRF middleware stored in r's
// context, or the defaults when it did not run.
func namesFromRequest(r *http.Request) csrfNames {
	if names, ok := r.Context().Value(csrfNamesKey).(csrfNames); ok {
		return names
	}
	return csrfNames{field: "csrf_token", header: "X-CSRF-Token"}
}

// CSRF returns double-submit cookie CSRF middleware with DefaultCSRFConfig.
//
// A safe request (GET, HEAD, OPTIONS, TRACE) keeps its valid csrf_token
// cookie or gets a new one, and the token goes into the request context for
// GetCSRFToken. Every other request must send that token back, as
// GetCSRFToken rendered it (masked) or as the raw cookie value, in the
// X-CSRF-Token header or the csrf_token form field, and the cookie must carry
// a valid HMAC. Otherwise it gets a 403.
//
//	<form method="post">
//		<input type="hidden" name="csrf_token" value="{{.CSRFToken}}">
//	</form>
//
// The cookie is not HttpOnly, so script can read it and send it as the
// X-CSRF-Token header.
func CSRF() func(http.Handler) http.Handler {
	return CSRFWithConfig(DefaultCSRFConfig())
}

// CSRFWithConfig returns CSRF middleware for config, filling its zero fields
// with defaults in place. A nil config means DefaultCSRFConfig. It panics if
// SecretKey is empty and crypto/rand cannot supply one.
//
//	config := middleware.DefaultCSRFConfig()
//	config.SecretKey = []byte(os.Getenv("CSRF_SECRET"))
//	r.Use(middleware.CSRFWithConfig(config))
func CSRFWithConfig(config *CSRFConfig) func(http.Handler) http.Handler {
	if config == nil {
		config = DefaultCSRFConfig()
	}

	if config.TokenLength == 0 {
		config.TokenLength = 32
	}
	if config.CookieName == "" {
		config.CookieName = "csrf_token"
	}
	if config.HeaderName == "" {
		config.HeaderName = "X-CSRF-Token"
	}
	if config.FormFieldName == "" {
		config.FormFieldName = "csrf_token"
	}
	if config.Path == "" {
		config.Path = "/"
	}
	if len(config.SkipMethods) == 0 {
		config.SkipMethods = []string{"GET", "HEAD", "OPTIONS", "TRACE"}
	}
	if config.ErrorHandler == nil {
		config.ErrorHandler = defaultCSRFErrorHandler
	}
	if config.TokenExtractor == nil {
		config.TokenExtractor = defaultTokenExtractor(config)
	}
	if len(config.SecretKey) == 0 {
		// Safe for one instance. SecretKey describes the restart and
		// multi-instance cost.
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			// Fail closed: never run with an empty HMAC key.
			panic(fmt.Sprintf("middleware: failed to generate CSRF secret key: %v", err))
		}
		config.SecretKey = key
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Bind the token to the user when the request has one. Otherwise
			// the HMAC covers the nonce alone.
			sessionID := sessionIDFromContext(r)

			if isSafeMethod(r.Method, config.SkipMethods) {
				// Keep a valid token rather than rotating it on every safe
				// request. Rotation breaks concurrent flows, such as htmx
				// polling replacing the cookie while a form holds the old
				// value, and adds no security.
				token := getCSRFCookieValue(r, config.CookieName)
				if token == "" || !validateCSRFToken(token, config.SecretKey, sessionID) {
					newToken, err := generateCSRFToken(config.TokenLength, config.SecretKey, sessionID)
					if err != nil {
						config.ErrorHandler(w, r, fmt.Errorf("failed to generate CSRF token: %w", err))
						return
					}
					token = newToken
					setCSRFCookie(w, token, config)
				}

				next.ServeHTTP(w, withCSRFToken(r, token, config))
				return
			}

			cookieToken := getCSRFCookieValue(r, config.CookieName)
			if cookieToken == "" {
				config.ErrorHandler(w, r, fmt.Errorf("CSRF cookie not found"))
				return
			}

			requestToken := config.TokenExtractor(r)
			if requestToken == "" {
				config.ErrorHandler(w, r, fmt.Errorf("CSRF token not found in request"))
				return
			}

			// The submitted token must match the cookie, masked (as pages
			// render it) or raw (as script reads it from the cookie), and the
			// cookie must carry a valid HMAC. The HMAC check stops an attacker
			// who can plant a cookie, for example from a sibling subdomain,
			// from passing with a matching pair of arbitrary values.
			if !csrfTokenMatches(cookieToken, requestToken) {
				config.ErrorHandler(w, r, fmt.Errorf("CSRF token mismatch"))
				return
			}
			if !validateCSRFToken(cookieToken, config.SecretKey, sessionID) {
				config.ErrorHandler(w, r, fmt.Errorf("CSRF token failed integrity check"))
				return
			}

			next.ServeHTTP(w, withCSRFToken(r, cookieToken, config))
		})
	}
}

// GetCSRFToken returns the token this package's CSRF middleware stored in r's
// context, masked for a page, or "" when it did not run. Every call returns a
// different string the middleware accepts, so a compressed page never repeats
// the cookie's bytes (BREACH). It does not see the token from
// app.Middleware.CSRF. In a HyperChi handler, c.CSRFToken() reads that one.
func GetCSRFToken(r *http.Request) string {
	if token, ok := r.Context().Value(csrfTokenKey).(string); ok && token != "" {
		return maskCSRFToken(token)
	}
	return ""
}

// maskCSRFToken returns base64url(pad || token XOR pad) with a fresh random
// pad as long as the token.
func maskCSRFToken(token string) string {
	raw := []byte(token)
	buf := make([]byte, 2*len(raw))
	pad, masked := buf[:len(raw)], buf[len(raw):]
	if _, err := rand.Read(pad); err != nil {
		// crypto/rand.Read does not fail on supported platforms; a page
		// without a usable token must not be rendered.
		panic(fmt.Sprintf("middleware: mask CSRF token: %v", err))
	}
	for i := range raw {
		masked[i] = raw[i] ^ pad[i]
	}
	return base64.RawURLEncoding.EncodeToString(buf)
}

// csrfTokenMatches reports whether sent is the cookie token or a mask of it.
// Both comparisons run in constant time for equal lengths.
func csrfTokenMatches(cookie, sent string) bool {
	match := subtle.ConstantTimeCompare([]byte(cookie), []byte(sent))
	buf, err := base64.RawURLEncoding.DecodeString(sent)
	if err == nil && len(buf) > 0 && len(buf)%2 == 0 {
		n := len(buf) / 2
		token := make([]byte, n)
		for i := range n {
			token[i] = buf[i] ^ buf[n+i]
		}
		match |= subtle.ConstantTimeCompare(token, []byte(cookie))
	}
	return match == 1
}

// generateCSRFToken returns base64url(nonce || HMAC-SHA256(secretKey,
// nonce [|| 0x00 || sessionID])), with a random nonce of length bytes (at
// least 16). The embedded HMAC lets the server verify its own tokens without
// storing them, so a forged token fails even when it matches the cookie.
func generateCSRFToken(length int, secretKey []byte, sessionID string) (string, error) {
	if length < 16 {
		length = 16
	}

	nonce := make([]byte, length)
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("failed to generate CSRF nonce: %w", err)
	}

	mac := csrfHMAC(secretKey, nonce, sessionID)

	token := make([]byte, 0, len(nonce)+len(mac))
	token = append(token, nonce...)
	token = append(token, mac...)

	return base64.URLEncoding.EncodeToString(token), nil
}

// validateCSRFToken recomputes the HMAC over the token's nonce and compares it
// in constant time with the embedded one. It returns false for a malformed
// token, one issued under another key or user, or one generateCSRFToken did
// not produce.
func validateCSRFToken(token string, secretKey []byte, sessionID string) bool {
	raw, err := base64.URLEncoding.DecodeString(token)
	if err != nil {
		return false
	}

	// A token is at least a 16-byte nonce plus the 32-byte HMAC.
	const macLen = sha256.Size
	if len(raw) <= macLen {
		return false
	}

	nonce := raw[:len(raw)-macLen]
	if len(nonce) < 16 {
		return false
	}
	gotMAC := raw[len(raw)-macLen:]

	expectedMAC := csrfHMAC(secretKey, nonce, sessionID)
	return subtle.ConstantTimeCompare(gotMAC, expectedMAC) == 1
}

// csrfHMAC returns HMAC-SHA256 over the nonce, followed by a zero byte and the
// session ID when there is one.
func csrfHMAC(secretKey, nonce []byte, sessionID string) []byte {
	h := hmac.New(sha256.New, secretKey)
	h.Write(nonce)
	if sessionID != "" {
		// The separator keeps nonce and session bytes from running together.
		h.Write([]byte{0})
		h.Write([]byte(sessionID))
	}
	return h.Sum(nil)
}

// sessionIDFromContext returns the authenticated user ID (see
// security.GetUserIDFromContext), or "". Tokens are bound to it, so a token
// issued before login stops validating once the request carries a user ID.
// The next safe request issues a new one.
func sessionIDFromContext(r *http.Request) string {
	return security.GetUserIDFromContext(r.Context())
}

// setCSRFCookie sets the token cookie with the configured attributes.
func setCSRFCookie(w http.ResponseWriter, token string, config *CSRFConfig) {
	cookie := &http.Cookie{
		Name:     config.CookieName,
		Value:    token,
		Path:     config.Path,
		Domain:   config.Domain,
		MaxAge:   config.MaxAge,
		Secure:   config.Secure,
		HttpOnly: false, // script reads it to send the X-CSRF-Token header
		SameSite: config.SameSite,
	}

	http.SetCookie(w, cookie)
}

// getCSRFCookieValue returns the named cookie's value, or "".
func getCSRFCookieValue(r *http.Request, cookieName string) string {
	cookie, err := r.Cookie(cookieName)
	if err != nil {
		return ""
	}
	return cookie.Value
}

// defaultTokenExtractor reads the token from the HeaderName header, then the
// FormFieldName form field, then the HX-CSRF-Token header.
func defaultTokenExtractor(config *CSRFConfig) func(*http.Request) string {
	return func(r *http.Request) string {
		if token := r.Header.Get(config.HeaderName); token != "" {
			return token
		}

		if err := r.ParseForm(); err == nil {
			if token := r.FormValue(config.FormFieldName); token != "" {
				return token
			}
		}

		if token := r.Header.Get("HX-CSRF-Token"); token != "" {
			return token
		}

		return ""
	}
}

// isSafeMethod reports whether method is in skipMethods, ignoring case.
func isSafeMethod(method string, skipMethods []string) bool {
	for _, skipMethod := range skipMethods {
		if strings.EqualFold(method, skipMethod) {
			return true
		}
	}
	return false
}

// defaultCSRFErrorHandler answers a plain-text 403. The message is generic so
// it does not tell an attacker which check failed.
func defaultCSRFErrorHandler(w http.ResponseWriter, r *http.Request, err error) {
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusForbidden)
	_, _ = w.Write([]byte("Forbidden: CSRF token validation failed")) // client may have disconnected
}

// CSRFTemplateHelper builds markup that carries the token from GetCSRFToken,
// under the field and header names of the CSRFConfig that issued it. It has
// no state of its own.
type CSRFTemplateHelper struct{}

// NewCSRFTemplateHelper returns a CSRFTemplateHelper.
func NewCSRFTemplateHelper() *CSRFTemplateHelper {
	return &CSRFTemplateHelper{}
}

// TokenInput returns a hidden input holding the token, named after the
// config's FormFieldName, or an HTML comment when there is no token. The
// result is a plain string, so html/template escapes it into visible text.
// Convert it to template.HTML before passing it in. That is safe because the
// token is base64url and the name is escaped here.
func (h *CSRFTemplateHelper) TokenInput(r *http.Request) string {
	token := GetCSRFToken(r)
	if token == "" {
		return `<!-- CSRF token not available -->`
	}

	name := template.HTMLEscapeString(namesFromRequest(r).field)
	return fmt.Sprintf(`<input type="hidden" name="%s" value="%s">`, name, token)
}

// TokenMeta returns <meta name="csrf-token"> holding the token, for script to
// read, or an HTML comment when there is none. Like TokenInput it returns a
// plain string, so convert it to template.HTML for html/template.
func (h *CSRFTemplateHelper) TokenMeta(r *http.Request) string {
	token := GetCSRFToken(r)
	if token == "" {
		return `<!-- CSRF token not available -->`
	}

	return fmt.Sprintf(`<meta name="csrf-token" content="%s">`, token)
}

// TokenHXHeaders returns hx-headers:inherited='{"X-CSRF-Token":"<token>"}',
// keyed by the config's HeaderName, or "" when there is no token. It is a
// template.HTMLAttr so html/template writes it as an attribute instead of
// escaping the quotes.
//
// htmx 4 inherits an attribute only when it is marked :inherited, so this one
// is: on <body> it sends the header with every htmx request issued inside.
//
//	<body {{.csrf_hx_headers}}>
func (h *CSRFTemplateHelper) TokenHXHeaders(r *http.Request) template.HTMLAttr {
	token := GetCSRFToken(r)
	if token == "" {
		return ""
	}
	body, err := json.Marshal(map[string]string{namesFromRequest(r).header: token})
	if err != nil {
		return ""
	}
	// json.Marshal escapes <, >, and & but not a single quote, which would
	// end the attribute. The token is base64url, so only a header name could
	// carry one.
	value := strings.ReplaceAll(string(body), "'", "&#39;")
	return template.HTMLAttr(fmt.Sprintf(`hx-headers:inherited='%s'`, value))
}
