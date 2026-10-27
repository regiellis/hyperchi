// Sessions stored in any KVStore, with their cookies and middleware.

package hyperchi

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net"
	"net/http"
	"time"
)

// sessionCtxKey carries the request's *KVSession; read it via
// GetSessionFromContext.
const sessionCtxKey hcCtxKey = iota + 100

// KVSessionStore keeps sessions in a KVStore and manages their cookies. Create
// one with NewKVSessionStore.
type KVSessionStore struct {
	store      KVStore
	cookieName string
	maxAge     time.Duration
	keyPrefix  string

	// secureCookies controls the Secure flag on session cookies.
	// Defaults to true so cookies are only sent over HTTPS. Set
	// KVSessionConfig.InsecureCookies=true for local HTTP development.
	secureCookies bool
	// cookieSameSite controls the SameSite policy for session cookies.
	// Defaults to http.SameSiteStrictMode for CSRF resistance.
	cookieSameSite http.SameSite
}

// KVSessionConfig configures NewKVSessionStore.
type KVSessionConfig struct {
	Backend    string        // Name of a store in the KVRegistry
	CookieName string        // Session cookie name (default: "session_id")
	MaxAge     time.Duration // Session expiration (default: 24 hours)
	KeyPrefix  string        // Key prefix for session keys (default: "session:")

	// InsecureCookies disables the Secure cookie flag. Default is false
	// (Secure=true). Only enable this for local development over plain HTTP,
	// never in production.
	InsecureCookies bool
	// CookieSameSite overrides the SameSite policy. Unset (0) or
	// http.SameSiteDefaultMode means the secure default,
	// http.SameSiteStrictMode.
	CookieSameSite http.SameSite
}

// NewKVSessionStore returns a session store on the registry's config.Backend
// store, with the defaults noted on KVSessionConfig. It fails when no such store
// is registered.
func NewKVSessionStore(registry *KVRegistry, config KVSessionConfig) (*KVSessionStore, error) {
	store, err := registry.Get(config.Backend)
	if err != nil {
		return nil, fmt.Errorf("failed to get KV store '%s': %w", config.Backend, err)
	}

	if config.CookieName == "" {
		config.CookieName = "session_id"
	}
	if config.MaxAge == 0 {
		config.MaxAge = 24 * time.Hour
	}
	if config.KeyPrefix == "" {
		config.KeyPrefix = "session:"
	}

	sameSite := config.CookieSameSite
	// An unset field is 0, not SameSiteDefaultMode (1); both mean "use the
	// secure default".
	if sameSite == 0 || sameSite == http.SameSiteDefaultMode {
		sameSite = http.SameSiteStrictMode
	}

	return &KVSessionStore{
		store:          store,
		cookieName:     config.CookieName,
		maxAge:         config.MaxAge,
		keyPrefix:      config.KeyPrefix,
		secureCookies:  !config.InsecureCookies,
		cookieSameSite: sameSite,
	}, nil
}

// KVSession is one session's data and timestamps. Each request loads its own
// copy; a KVSession is not safe for concurrent use.
type KVSession struct {
	ID        string         `json:"id"`
	Data      map[string]any `json:"data"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	ExpiresAt time.Time      `json:"expires_at"`

	store     KVStore
	keyPrefix string
	dirty     bool
	destroyed bool
	owner     *KVSessionStore
}

// NewKVSession creates a new session. It returns an error if a
// cryptographically secure session ID cannot be generated, so callers can
// fail the request instead of issuing a predictable identifier.
func NewKVSession(store KVStore, keyPrefix string, maxAge time.Duration) (*KVSession, error) {
	sessionID, err := generateSessionID()
	if err != nil {
		return nil, err
	}
	now := time.Now()

	return &KVSession{
		ID:        sessionID,
		Data:      make(map[string]any),
		CreatedAt: now,
		UpdatedAt: now,
		ExpiresAt: now.Add(maxAge),
		store:     store,
		keyPrefix: keyPrefix,
		dirty:     true, // New session is dirty
	}, nil
}

// Get returns the value stored under key, or nil.
func (s *KVSession) Get(key string) any {
	return s.Data[key]
}

// GetString returns key as a string, or "" when it is missing or not a string.
func (s *KVSession) GetString(key string) string {
	if value, ok := s.Data[key].(string); ok {
		return value
	}
	return ""
}

// GetInt returns key as an int, or 0 when it is missing or not a number. It
// reads any Go integer or float type and json.Number, so a value Set earlier
// in the same request reads the same as one loaded from the store (a JSON
// number decodes as float64). Fractions truncate toward zero, and a value
// that does not fit in an int reads as 0.
func (s *KVSession) GetInt(key string) int {
	switch v := s.Data[key].(type) {
	case int:
		return v
	case int8:
		return int(v)
	case int16:
		return int(v)
	case int32:
		return int(v)
	case int64:
		if v < math.MinInt || v > math.MaxInt {
			return 0
		}
		return int(v)
	case uint:
		if v > math.MaxInt {
			return 0
		}
		return int(v)
	case uint8:
		return int(v)
	case uint16:
		return int(v)
	case uint32:
		if uint64(v) > math.MaxInt {
			return 0
		}
		return int(v)
	case uint64:
		if v > math.MaxInt {
			return 0
		}
		return int(v)
	case float32:
		return floatToInt(float64(v))
	case float64:
		return floatToInt(v)
	case json.Number:
		if n, err := v.Int64(); err == nil && n >= math.MinInt && n <= math.MaxInt {
			return int(n)
		}
		if f, err := v.Float64(); err == nil {
			return floatToInt(f)
		}
	}
	return 0
}

// floatToInt truncates f toward zero, or returns 0 when it is not finite or
// does not fit in an int.
func floatToInt(f float64) int {
	if math.IsNaN(f) || f < math.MinInt || f >= math.MaxInt {
		return 0
	}
	return int(f)
}

// GetBool returns key as a bool, or false when it is missing or not a bool.
func (s *KVSession) GetBool(key string) bool {
	if value, ok := s.Data[key].(bool); ok {
		return value
	}
	return false
}

// Set stores value under key. The value must survive a JSON round trip.
func (s *KVSession) Set(key string, value any) {
	s.Data[key] = value
	s.UpdatedAt = time.Now()
	s.dirty = true
}

// Delete removes key from the session.
func (s *KVSession) Delete(key string) {
	delete(s.Data, key)
	s.UpdatedAt = time.Now()
	s.dirty = true
}

// Has reports whether key is set.
func (s *KVSession) Has(key string) bool {
	_, exists := s.Data[key]
	return exists
}

// Clear removes every value from the session.
func (s *KVSession) Clear() {
	s.Data = make(map[string]any)
	s.UpdatedAt = time.Now()
	s.dirty = true
}

// IsExpired reports whether ExpiresAt has passed.
func (s *KVSession) IsExpired() bool {
	return time.Now().After(s.ExpiresAt)
}

// Touch extends the session expiration using a sliding window. To avoid a
// backing-store write on every read-only request (bbolt performs an fsync
// write transaction per save), the expiry is only extended when more than
// half of maxAge has elapsed since the last update. It returns true when the
// expiry was actually extended (and the session marked dirty), false when the
// touch was a no-op.
func (s *KVSession) Touch(maxAge time.Duration) bool {
	now := time.Now()

	// More than half the lifetime remains: skip the write.
	if remaining := time.Until(s.ExpiresAt); remaining > maxAge/2 {
		return false
	}

	s.ExpiresAt = now.Add(maxAge)
	s.UpdatedAt = now
	s.dirty = true
	return true
}

// Save writes the session to the store when it has unsaved changes and has not
// been destroyed. A store that supports TTLs expires the record at ExpiresAt.
func (s *KVSession) Save() error {
	if s.destroyed || !s.dirty {
		return nil
	}

	sessionKey := s.keyPrefix + s.ID
	sessionData, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("failed to marshal session: %w", err)
	}

	if advancedStore, ok := s.store.(AdvancedKVStore); ok {
		ttl := time.Until(s.ExpiresAt)
		if ttl > 0 {
			err = advancedStore.SetTTL(sessionKey, sessionData, ttl)
		} else {
			err = s.store.Set(sessionKey, sessionData)
		}
	} else {
		err = s.store.Set(sessionKey, sessionData)
	}

	if err != nil {
		return fmt.Errorf("failed to save session: %w", err)
	}

	s.dirty = false
	return nil
}

// Destroy removes the session from the KV store and prevents it from being saved again.
func (s *KVSession) Destroy() error {
	sessionKey := s.keyPrefix + s.ID
	if err := s.store.Delete(sessionKey); err != nil {
		return err
	}
	s.destroyed = true
	s.dirty = false
	s.Data = make(map[string]any)
	return nil
}

// Regenerate issues a new session ID while preserving the session data,
// persists the session under the new key, and destroys the old key. Call this
// immediately after any privilege change (most importantly after a successful
// login) to prevent session fixation attacks, where an attacker fixes a known
// session ID on a victim before authentication.
//
// On success the session's ID is the freshly generated value. If the new
// session cannot be persisted, the original key is left untouched and an error
// is returned.
func (s *KVSession) Regenerate() error {
	if s.destroyed {
		return fmt.Errorf("cannot regenerate a destroyed session")
	}
	newID, err := generateSessionID()
	if err != nil {
		return err
	}

	oldID := s.ID

	// Switch to the new ID and persist under the new key.
	s.ID = newID
	s.UpdatedAt = time.Now()
	s.dirty = true
	if err := s.Save(); err != nil {
		// Roll back the ID change so the in-memory session still maps to the
		// (still-present) backing-store entry.
		s.ID = oldID
		return fmt.Errorf("failed to persist regenerated session: %w", err)
	}

	// Best-effort removal of the old key. The new session is already durable,
	// so a failure here is logged rather than failing the operation; the stale
	// entry will be reaped by expiry/cleanup.
	oldKey := s.keyPrefix + oldID
	if err := s.store.Delete(oldKey); err != nil {
		log.Printf("hyperchi/sessions: failed to delete old session key %q after regenerate: %v", oldID, err)
	}

	return nil
}

// GetSession loads the session named by r's cookie. A missing cookie, an
// unknown or unreadable record, or an expired session yields a new, unsaved
// session; the expired record is deleted. A loaded session's expiry slides (see
// Touch). It fails only when the store cannot be read.
func (store *KVSessionStore) GetSession(r *http.Request) (*KVSession, error) {
	cookie, err := r.Cookie(store.cookieName)
	if err != nil || cookie.Value == "" {
		return NewKVSession(store.store, store.keyPrefix, store.maxAge)
	}

	sessionID := cookie.Value
	sessionKey := store.keyPrefix + sessionID

	sessionData, err := store.store.Get(sessionKey)
	if err != nil {
		if err == ErrKeyNotFound {
			return NewKVSession(store.store, store.keyPrefix, store.maxAge)
		}
		return nil, fmt.Errorf("failed to load session: %w", err)
	}

	var session KVSession
	if err := json.Unmarshal(sessionData, &session); err != nil {
		return NewKVSession(store.store, store.keyPrefix, store.maxAge)
	}

	session.store = store.store
	session.keyPrefix = store.keyPrefix

	if session.IsExpired() {
		if err := session.Destroy(); err != nil {
			log.Printf("hyperchi/sessions: failed to destroy expired session %q: %v", session.ID, err)
		}
		return NewKVSession(store.store, store.keyPrefix, store.maxAge)
	}

	session.Touch(store.maxAge)

	return &session, nil
}

// SaveSession saves a session and sets its cookie, or clears a destroyed
// session's cookie. Call it before the response header is written.
func (store *KVSessionStore) SaveSession(w http.ResponseWriter, session *KVSession) error {
	if err := session.Save(); err != nil {
		return err
	}

	cookie := &http.Cookie{
		Name:     store.cookieName,
		Value:    session.ID,
		Path:     "/",
		MaxAge:   int(store.maxAge.Seconds()),
		HttpOnly: true,
		Secure:   store.secureCookies,
		SameSite: store.cookieSameSite,
	}
	if session.destroyed {
		cookie.Value = ""
		cookie.MaxAge = -1
	}

	http.SetCookie(w, cookie)
	return nil
}

// DestroySession deletes the request's session and clears its cookie. Inside
// SessionMiddleware it destroys the middleware's own session, so the response
// writer does not save it again.
func (store *KVSessionStore) DestroySession(w http.ResponseWriter, r *http.Request) error {
	session := GetSessionFromContext(r)
	if session == nil || session.owner != store {
		var err error
		session, err = store.GetSession(r)
		if err != nil {
			return err
		}
	}

	if err := session.Destroy(); err != nil {
		return err
	}

	// The attributes must match the cookie that was set, or the browser keeps it.
	cookie := &http.Cookie{
		Name:     store.cookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   store.secureCookies,
		SameSite: store.cookieSameSite,
	}

	http.SetCookie(w, cookie)
	return nil
}

// CleanupExpiredSessions deletes expired and unreadable session records. A
// record that fails to delete is logged, not returned.
func (store *KVSessionStore) CleanupExpiredSessions() error {
	keys, err := store.store.Keys(store.keyPrefix)
	if err != nil {
		return fmt.Errorf("failed to get session keys: %w", err)
	}

	var expiredKeys []string
	now := time.Now()

	for _, key := range keys {
		sessionData, err := store.store.Get(key)
		if err != nil {
			continue // deleted since Keys ran, or unreadable: leave it
		}

		var session KVSession
		if err := json.Unmarshal(sessionData, &session); err != nil {
			expiredKeys = append(expiredKeys, key)
			continue
		}

		if now.After(session.ExpiresAt) {
			expiredKeys = append(expiredKeys, key)
		}
	}

	for _, key := range expiredKeys {
		if err := store.store.Delete(key); err != nil {
			log.Printf("hyperchi/sessions: failed to delete expired session key %q: %v", key, err)
		}
	}

	return nil
}

// GetAllSessions returns every unexpired, readable session. It reads every
// session record, so it suits admin pages, not request paths.
func (store *KVSessionStore) GetAllSessions() ([]*KVSession, error) {
	keys, err := store.store.Keys(store.keyPrefix)
	if err != nil {
		return nil, fmt.Errorf("failed to get session keys: %w", err)
	}

	var sessions []*KVSession
	now := time.Now()

	for _, key := range keys {
		sessionData, err := store.store.Get(key)
		if err != nil {
			continue
		}

		var session KVSession
		if err := json.Unmarshal(sessionData, &session); err != nil {
			continue
		}

		if now.After(session.ExpiresAt) {
			continue
		}

		session.store = store.store
		session.keyPrefix = store.keyPrefix
		sessions = append(sessions, &session)
	}

	return sessions, nil
}

// CountActiveSessions returns len(GetAllSessions()).
func (store *KVSessionStore) CountActiveSessions() (int, error) {
	sessions, err := store.GetAllSessions()
	if err != nil {
		return 0, err
	}
	return len(sessions), nil
}

// generateSessionID returns 32 random bytes from crypto/rand, hex-encoded. It
// never falls back to a predictable value: a crypto/rand failure is returned so
// the caller can fail the request.
func generateSessionID() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("failed to generate secure session ID: %w", err)
	}
	return hex.EncodeToString(bytes), nil
}

// SessionMiddleware loads or creates the request's session, puts it in the
// request context (GetSessionFromContext), and saves it with its cookie before
// the response's first header, byte, flush, or hijack. A store read failure is
// answered with a 500.
func (store *KVSessionStore) SessionMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			session, err := store.GetSession(r)
			if err != nil {
				http.Error(w, "Session error", http.StatusInternalServerError)
				return
			}

			session.owner = store
			ctx := r.Context()
			ctx = context.WithValue(ctx, sessionCtxKey, session)
			r = r.WithContext(ctx)

			sw := &sessionResponseWriter{
				ResponseWriter: w,
				store:          store,
				session:        session,
			}

			next.ServeHTTP(sw, r)
			// A handler that writes nothing gets net/http's implicit 200,
			// which bypasses the wrapper; headers are still unsent here, so
			// the session cookie can go out.
			sw.saveOnce()
		})
	}
}

// sessionResponseWriter wraps http.ResponseWriter to auto-save sessions.
// It passes through http.Flusher and http.Hijacker (and exposes Unwrap for
// http.ResponseController) so streaming responses (SSE) and connection
// upgrades (WebSocket) keep working behind SessionMiddleware.
type sessionResponseWriter struct {
	http.ResponseWriter
	store   *KVSessionStore
	session *KVSession
	saved   bool
}

// saveOnce persists the session cookie before the first header, byte, flush,
// or hijack. Save failures cannot change the response at this point, so they
// are logged rather than silently dropped.
func (sw *sessionResponseWriter) saveOnce() {
	if sw.saved {
		return
	}
	sw.saved = true
	if err := sw.store.SaveSession(sw.ResponseWriter, sw.session); err != nil {
		log.Printf("hyperchi: failed to save session %s: %v", sw.session.ID, err)
	}
}

// WriteHeader saves the session, then writes the header.
func (sw *sessionResponseWriter) WriteHeader(code int) {
	sw.saveOnce()
	sw.ResponseWriter.WriteHeader(code)
}

// Write saves the session, then writes data.
func (sw *sessionResponseWriter) Write(data []byte) (int, error) {
	sw.saveOnce()
	return sw.ResponseWriter.Write(data)
}

// Flush implements http.Flusher when the underlying writer supports it.
func (sw *sessionResponseWriter) Flush() {
	sw.saveOnce()
	if f, ok := sw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack implements http.Hijacker when the underlying writer supports it.
func (sw *sessionResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hj, ok := sw.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("hyperchi: underlying ResponseWriter does not support hijacking")
	}
	sw.saveOnce()
	return hj.Hijack()
}

// Unwrap exposes the underlying writer to http.ResponseController.
func (sw *sessionResponseWriter) Unwrap() http.ResponseWriter {
	return sw.ResponseWriter
}

// GetSessionFromContext returns the session SessionMiddleware stored on r, or
// nil outside the middleware.
func GetSessionFromContext(r *http.Request) *KVSession {
	if session, ok := r.Context().Value(sessionCtxKey).(*KVSession); ok {
		return session
	}
	return nil
}
