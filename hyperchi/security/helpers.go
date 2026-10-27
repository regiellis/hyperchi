package security

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"time"
)

// LoginAttempt is one submitted login, as read by ExtractLoginAttempt.
type LoginAttempt struct {
	Email     string
	Password  string
	UserAgent string
	IPAddress string
	Timestamp time.Time
}

// User is the account record Authenticator checks credentials against.
type User struct {
	ID           int    `json:"id" db:"id"`
	Email        string `json:"email" db:"email"`
	PasswordHash string `json:"-" db:"password_hash"` // never serialized to JSON
	Name         string `json:"name" db:"name"`
	Role         string `json:"role" db:"role"`
	Active       bool   `json:"active" db:"active"`
	CreatedAt    string `json:"created_at" db:"created_at"`
	UpdatedAt    string `json:"updated_at" db:"updated_at"`
}

// AuthResult is the outcome of AuthenticateUser. On success User and Token are
// set. On failure Error holds a message safe to show the client.
type AuthResult struct {
	Success bool   `json:"success"`
	User    *User  `json:"user,omitempty"`
	Token   string `json:"token,omitempty"`
	Error   string `json:"error,omitempty"`
}

// UserRepository loads users for Authenticator. FindByEmail may return a nil
// user or an error for an unknown email. Both count as a failed login.
type UserRepository interface {
	FindByEmail(email string) (*User, error)
	UpdateLastLogin(userID int) error
}

// Authenticator checks login credentials and issues session tokens.
type Authenticator struct {
	userRepo UserRepository
	helper   *AuthHelper
}

// NewAuthenticator returns an Authenticator that issues session tokens from
// helper. Pass the same helper to RequireAuth so it accepts those tokens. A
// nil helper means a new one from NewAuthHelper; reach it with Helper.
func NewAuthenticator(userRepo UserRepository, helper *AuthHelper) *Authenticator {
	if helper == nil {
		helper = NewAuthHelper()
	}
	return &Authenticator{
		userRepo: userRepo,
		helper:   helper,
	}
}

// Helper returns the AuthHelper that issues this Authenticator's tokens: the
// one to pass to RequireAuth, and to configure with SetSessionStore and
// SetTokenExpiry before serving.
func (a *Authenticator) Helper() *AuthHelper {
	return a.helper
}

// AuthenticateUser checks attempt against the stored user. It fails for an
// unknown email, an inactive account, or a wrong password, and on success
// issues a session token and records the login time.
//
// Every credential failure returns the same "Invalid credentials" message, so
// responses cannot reveal which accounts exist.
//
//	sessions := security.NewAuthHelper()
//	auth := security.NewAuthenticator(userRepo, sessions)
//	app.Post("/login", func(c *hyperchi.Context) error {
//		result := auth.AuthenticateUser(security.ExtractLoginAttempt(c.Request()))
//		if !result.Success {
//			return hyperchi.Unauthorized(result.Error)
//		}
//		// set the session cookie from result.Token
//		return c.Redirect("/account")
//	})
//	app.Get("/account", account, security.RequireAuth(sessions))
func (a *Authenticator) AuthenticateUser(attempt *LoginAttempt) *AuthResult {
	user, err := a.userRepo.FindByEmail(attempt.Email)
	if err != nil {
		return &AuthResult{
			Success: false,
			Error:   "Invalid credentials",
		}
	}

	if user == nil {
		return &AuthResult{
			Success: false,
			Error:   "Invalid credentials",
		}
	}

	if !user.Active {
		return &AuthResult{
			Success: false,
			Error:   "Invalid credentials",
		}
	}

	if !CheckPasswordHash(attempt.Password, user.PasswordHash) {
		return &AuthResult{
			Success: false,
			Error:   "Invalid credentials",
		}
	}

	token, err := a.helper.CreateLoginSession(fmt.Sprintf("%d", user.ID))
	if err != nil {
		return &AuthResult{
			Success: false,
			Error:   "Failed to create session",
		}
	}

	// A failed last-login update is logged, not fatal: the credentials were valid.
	if err := a.userRepo.UpdateLastLogin(user.ID); err != nil {
		log.Printf("security: failed to update last login for user %d: %v", user.ID, err)
	}

	return &AuthResult{
		Success: true,
		User:    user,
		Token:   token,
	}
}

// CreateUser validates a new account and returns its fields, keyed by column
// name, ready to insert: email, password_hash, name, role, active (true),
// created_at, and updated_at. It stores nothing. An empty role becomes "user".
// It returns an error for an invalid email or a password that fails
// ValidatePasswordRequirements.
func CreateUser(email, password, name, role string) (map[string]any, error) {
	if email == "" || !isValidEmail(email) {
		return nil, fmt.Errorf("invalid email address")
	}

	if err := ValidatePasswordRequirements(password); err != nil {
		return nil, fmt.Errorf("password validation failed: %w", err)
	}

	if role == "" {
		role = "user"
	}

	passwordHash, err := HashPassword(password)
	if err != nil {
		return nil, fmt.Errorf("failed to hash password: %w", err)
	}

	userData := map[string]any{
		"email":         email,
		"password_hash": passwordHash,
		"name":          name,
		"role":          role,
		"active":        true,
		"created_at":    time.Now(),
		"updated_at":    time.Now(),
	}

	return userData, nil
}

// ChangePassword returns a new hash for newPassword after checking that
// currentPassword matches user.PasswordHash, that newPassword passes
// ValidatePasswordRequirements, and that it differs from the current one. It
// does not modify user. The caller stores the returned hash.
func ChangePassword(user *User, currentPassword, newPassword string) (string, error) {
	if !CheckPasswordHash(currentPassword, user.PasswordHash) {
		return "", fmt.Errorf("current password is incorrect")
	}

	if err := ValidatePasswordRequirements(newPassword); err != nil {
		return "", fmt.Errorf("new password validation failed: %w", err)
	}

	if CheckPasswordHash(newPassword, user.PasswordHash) {
		return "", fmt.Errorf("new password must be different from current password")
	}

	newHash, err := HashPassword(newPassword)
	if err != nil {
		return "", fmt.Errorf("failed to hash new password: %w", err)
	}

	return newHash, nil
}

// TokenValidator resolves an authentication token to a user ID, returning a
// non-nil error when the token is unknown, malformed, or expired. *AuthHelper
// implements it. Give the helper a durable store with SetSessionStore.
type TokenValidator interface {
	ValidateSessionToken(token string) (userID string, err error)
}

// RequireAuth returns middleware that admits only requests carrying a token
// the validator resolves to a user ID. The token comes from a Bearer
// Authorization header, the "session" cookie, or the X-Auth-Token header, in
// that order. A missing, malformed, unknown, or expired token gets a plain-text
// 401. On success the user ID is stored in the request context, where
// GetUserIDFromContext and c.UserID read it.
//
//	auth := security.NewAuthHelper()
//	app.Group("/dashboard", func(g *hyperchi.HyperChi) {
//		g.Use(security.RequireAuth(auth))
//		g.Get("/", dashboard)
//	})
func RequireAuth(validator TokenValidator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := extractTokenFromRequest(r)
			if token == "" {
				http.Error(w, "Authentication required", http.StatusUnauthorized)
				return
			}

			// A cheap format check rejects junk before the validator, which
			// may hit a store.
			if !isValidTokenFormat(token) {
				http.Error(w, "Invalid token format", http.StatusUnauthorized)
				return
			}

			userID, err := validator.ValidateSessionToken(token)
			if err != nil || userID == "" {
				http.Error(w, "Invalid token", http.StatusUnauthorized)
				return
			}

			r = r.WithContext(setUserContext(r.Context(), userID))

			next.ServeHTTP(w, r)
		})
	}
}

// isValidEmail is a loose shape check: 4 to 254 bytes, containing "@" and ".",
// and not starting or ending with ".". It does not parse the address.
func isValidEmail(email string) bool {
	return len(email) > 3 &&
		len(email) < 255 &&
		strings.Contains(email, "@") &&
		strings.Contains(email, ".") &&
		!strings.HasPrefix(email, ".") &&
		!strings.HasSuffix(email, ".")
}

// ExtractLoginAttempt reads the "email" and "password" form fields, the user
// agent, and the client IP (see getClientIP) from r, stamped with the current
// time.
func ExtractLoginAttempt(r *http.Request) *LoginAttempt {
	return &LoginAttempt{
		Email:     r.FormValue("email"),
		Password:  r.FormValue("password"),
		UserAgent: r.UserAgent(),
		IPAddress: getClientIP(r),
		Timestamp: time.Now(),
	}
}

// getClientIP returns the client IP for audit logging.
//
// Forwarding headers (X-Forwarded-For, X-Real-IP) are client-controlled, so
// they count only when the TCP peer is a loopback address, the usual reverse
// proxy on localhost. For any other peer the socket address is used, so a
// client cannot spoof its IP. Host and port are split IPv6-safely.
func getClientIP(r *http.Request) string {
	peerHost := r.RemoteAddr
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		peerHost = host
	}

	if peerIP := net.ParseIP(peerHost); peerIP != nil && peerIP.IsLoopback() {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			// Walk from the right: the proxy appended the peer it saw, and
			// anything left of that came from the client and can be forged.
			entries := strings.Split(xff, ",")
			for i := len(entries) - 1; i >= 0; i-- {
				v := strings.TrimSpace(entries[i])
				if v == "" {
					break
				}
				if ip := net.ParseIP(v); ip == nil || !ip.IsLoopback() {
					return v
				}
			}
		}
		if xri := strings.TrimSpace(r.Header.Get("X-Real-IP")); xri != "" {
			return xri
		}
	}

	return peerHost
}

// extractTokenFromRequest returns the first token found in a Bearer
// Authorization header, the "session" cookie, or the X-Auth-Token header, or
// "" when there is none.
func extractTokenFromRequest(r *http.Request) string {
	if auth := r.Header.Get("Authorization"); auth != "" {
		if strings.HasPrefix(auth, "Bearer ") {
			return strings.TrimPrefix(auth, "Bearer ")
		}
	}

	if cookie, err := r.Cookie("session"); err == nil {
		return cookie.Value
	}

	if token := r.Header.Get("X-Auth-Token"); token != "" {
		return token
	}

	return ""
}

// isValidTokenFormat reports whether token is 10 to 200 bytes of padded
// URL-safe base64, the encoding GenerateSecureToken produces. Passing it says
// nothing about whether the token was issued.
func isValidTokenFormat(token string) bool {
	if len(token) < 10 || len(token) > 200 {
		return false
	}

	_, err := base64.URLEncoding.DecodeString(token)
	return err == nil
}

// userIDCtxKey uses the package's private ctxKey type (declared in upload.go)
// so it cannot collide with other packages' context keys. The offset keeps it
// distinct from the upload keys.
const userIDCtxKey ctxKey = iota + 100

// WithUserID returns a context carrying the authenticated user ID. Call it
// only from middleware that has verified the user: GetUserIDFromContext,
// c.UserID, upload validation with RequireAuth, and the WebSocket identity
// default all trust this value.
func WithUserID(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, userIDCtxKey, userID)
}

// setUserContext stores the verified user ID in ctx.
func setUserContext(ctx context.Context, userID string) context.Context {
	return WithUserID(ctx, userID)
}

// GetUserIDFromContext returns the user ID that RequireAuth or WithUserID
// stored in ctx, or "" when the request is not authenticated. In a HyperChi
// handler, c.UserID() reads the same value.
//
//	app.Get("/dashboard", func(c *hyperchi.Context) error {
//		userID := security.GetUserIDFromContext(c.Ctx())
//		if userID == "" {
//			return hyperchi.Unauthorized("Please sign in.")
//		}
//		return c.View("dashboard", hyperchi.H{"user_id": userID})
//	}, security.RequireAuth(authHelper))
func GetUserIDFromContext(ctx context.Context) string {
	if userID, ok := ctx.Value(userIDCtxKey).(string); ok {
		return userID
	}
	return ""
}
