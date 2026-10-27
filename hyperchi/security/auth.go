// Package security holds HyperChi's authentication and request-safety helpers:
// bcrypt password hashing and strength checks, login session tokens, the
// RequireAuth middleware, upload validation, and HTML escaping and sanitizing.
//
// Hashing and checking a password:
//
//	hash, err := security.HashPassword(password)
//	if err != nil {
//		return err
//	}
//	if !security.CheckPasswordHash(password, hash) {
//		return hyperchi.Unauthorized("Invalid credentials.")
//	}
package security

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"

	"golang.org/x/crypto/bcrypt"
)

// ErrPasswordTooLong is returned by HashPassword for a password over 72 bytes.
// bcrypt ignores every byte past the 72nd, so two passwords sharing a 72-byte
// prefix would hash alike. Reject the password and ask for a shorter one.
var ErrPasswordTooLong = errors.New("password exceeds maximum length of 72 bytes")

// PasswordStrength is the rating CheckPasswordStrength gives a password.
type PasswordStrength int

// Password strength levels, weakest first, so levels compare with <.
const (
	StrengthWeak PasswordStrength = iota
	StrengthMedium
	StrengthStrong
	StrengthVeryStrong
)

// String returns the level's lowercase name, or "unknown".
func (ps PasswordStrength) String() string {
	switch ps {
	case StrengthWeak:
		return "weak"
	case StrengthMedium:
		return "medium"
	case StrengthStrong:
		return "strong"
	case StrengthVeryStrong:
		return "very strong"
	default:
		return "unknown"
	}
}

// HashPassword returns a bcrypt hash of password at cost 14 (2^14 rounds),
// above bcrypt's default of 10. It returns ErrPasswordTooLong for a password
// over 72 bytes rather than letting bcrypt truncate it.
func HashPassword(password string) (string, error) {
	if len([]byte(password)) > 72 {
		return "", ErrPasswordTooLong
	}

	bytes, err := bcrypt.GenerateFromPassword([]byte(password), 14)
	if err != nil {
		return "", fmt.Errorf("failed to hash password: %w", err)
	}
	return string(bytes), nil
}

// CheckPasswordHash reports whether password matches the bcrypt hash. It uses
// bcrypt.CompareHashAndPassword, which reads the salt from the hash and
// compares in constant time. A malformed hash reports false.
func CheckPasswordHash(password, hash string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}

// CheckPasswordStrength rates a password by score. It earns a point for each
// length threshold reached (8, 12, and 16 bytes) and for each character class
// present (lowercase, uppercase, digit, symbol). It loses 2 for a common
// password and 1 for three identical characters in a row or a three-character
// keyboard run. A score of 2 or less is weak, 4 or less medium, 6 or less
// strong, and above that very strong. Passwords under 4 bytes are always weak.
func CheckPasswordStrength(password string) PasswordStrength {
	if len(password) < 4 {
		return StrengthWeak
	}

	score := 0

	if len(password) >= 8 {
		score++
	}
	if len(password) >= 12 {
		score++
	}
	if len(password) >= 16 {
		score++
	}

	hasLower := false
	hasUpper := false
	hasNumber := false
	hasSymbol := false

	for _, char := range password {
		switch {
		case unicode.IsLower(char):
			hasLower = true
		case unicode.IsUpper(char):
			hasUpper = true
		case unicode.IsNumber(char):
			hasNumber = true
		case unicode.IsPunct(char) || unicode.IsSymbol(char):
			hasSymbol = true
		}
	}

	if hasLower {
		score++
	}
	if hasUpper {
		score++
	}
	if hasNumber {
		score++
	}
	if hasSymbol {
		score++
	}

	if isCommonPassword(password) {
		score -= 2
	}
	if hasRepetitiveChars(password) {
		score -= 1
	}

	switch {
	case score <= 2:
		return StrengthWeak
	case score <= 4:
		return StrengthMedium
	case score <= 6:
		return StrengthStrong
	default:
		return StrengthVeryStrong
	}
}

// GenerateSecureToken returns length bytes from crypto/rand, encoded as padded
// URL-safe base64. It suits session tokens, CSRF tokens, and API keys.
func GenerateSecureToken(length int) (string, error) {
	bytes := make([]byte, length)
	_, err := rand.Read(bytes)
	if err != nil {
		return "", fmt.Errorf("failed to generate secure token: %w", err)
	}
	return base64.URLEncoding.EncodeToString(bytes), nil
}

// TimingSafeEqual reports whether a and b are equal, taking the same time
// wherever they differ, so a caller cannot learn a secret byte by byte.
//
// subtle.ConstantTimeCompare is constant-time only for equal-length inputs: it
// returns at once when the lengths differ, which reveals the secret's length.
// That is fine for fixed-length values (session and CSRF tokens, hex or base64
// digests). Do not use it to hide the length of a variable-length secret.
func TimingSafeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// RehashPasswordIfNeeded reports whether hash should be replaced: its bcrypt
// cost is below HashPassword's 14, or the cost cannot be read. Rehash on the
// next successful login, while the plain password is at hand.
func RehashPasswordIfNeeded(hash string) bool {
	cost, err := bcrypt.Cost([]byte(hash))
	if err != nil {
		return true
	}
	return cost < 14
}

// ValidatePasswordRequirements returns an error describing why password cannot
// be set, or nil. A password must be 8 to 128 bytes, rate at least
// StrengthMedium, and not be a common password.
func ValidatePasswordRequirements(password string) error {
	if len(password) < 8 {
		return fmt.Errorf("password must be at least 8 characters long")
	}

	if len(password) > 128 {
		return fmt.Errorf("password must be no more than 128 characters long")
	}

	strength := CheckPasswordStrength(password)
	if strength < StrengthMedium {
		return fmt.Errorf("password is too weak - must contain at least 8 characters with mixed case, numbers, and symbols")
	}

	if isCommonPassword(password) {
		return fmt.Errorf("password is too common - please choose a more unique password")
	}

	return nil
}

// SessionRecord is the data persisted for an issued login session token.
type SessionRecord struct {
	UserID    string
	ExpiresAt time.Time
}

// SessionStore persists the session tokens AuthHelper issues. The default keeps
// them in process memory, so sessions end on restart and are not shared across
// instances. Production deployments should pass a durable implementation to
// AuthHelper.SetSessionStore. Implementations must be safe for concurrent use.
type SessionStore interface {
	// Save stores a token with its user ID and absolute expiry time.
	Save(token, userID string, expiresAt time.Time) error
	// Lookup returns the record for a token, with false when the token is
	// unknown. It need not drop expired records: AuthHelper checks expiry
	// itself.
	Lookup(token string) (SessionRecord, bool, error)
	// Delete removes a token, for example on logout.
	Delete(token string) error
}

// memorySessionStore is the default SessionStore. Its sessions live only as
// long as the process.
type memorySessionStore struct {
	mu       sync.RWMutex
	sessions map[string]SessionRecord
}

func newMemorySessionStore() *memorySessionStore {
	return &memorySessionStore{sessions: make(map[string]SessionRecord)}
}

func (m *memorySessionStore) Save(token, userID string, expiresAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[token] = SessionRecord{UserID: userID, ExpiresAt: expiresAt}
	return nil
}

func (m *memorySessionStore) Lookup(token string) (SessionRecord, bool, error) {
	m.mu.RLock()
	rec, ok := m.sessions[token]
	m.mu.RUnlock()
	if !ok {
		return SessionRecord{}, false, nil
	}
	if !rec.ExpiresAt.IsZero() && time.Now().After(rec.ExpiresAt) {
		// Expired: drop it and report as not found.
		m.mu.Lock()
		delete(m.sessions, token)
		m.mu.Unlock()
		return SessionRecord{}, false, nil
	}
	return rec, true, nil
}

func (m *memorySessionStore) Delete(token string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, token)
	return nil
}

// AuthHelper issues, resolves, and revokes login session tokens. Configure it
// before serving: SetTokenExpiry and SetSessionStore are not safe to call
// concurrently with the session methods.
type AuthHelper struct {
	tokenExpiry time.Duration
	store       SessionStore
}

// NewAuthHelper returns an AuthHelper with a 24-hour token expiry and an
// in-memory session store. Call SetSessionStore to use a durable one.
func NewAuthHelper() *AuthHelper {
	return &AuthHelper{
		tokenExpiry: 24 * time.Hour,
		store:       newMemorySessionStore(),
	}
}

// SetTokenExpiry sets how long tokens issued from now on stay valid.
func (ah *AuthHelper) SetTokenExpiry(duration time.Duration) {
	ah.tokenExpiry = duration
}

// SetSessionStore replaces the session store. A nil store is ignored so the
// helper always has one.
func (ah *AuthHelper) SetSessionStore(store SessionStore) {
	if store != nil {
		ah.store = store
	}
}

// CreateLoginSession issues a random 32-byte token for userID, saves it in the
// session store with an expiry of now plus the token expiry, and returns it.
// ValidateSessionToken resolves the token back to userID.
func (ah *AuthHelper) CreateLoginSession(userID string) (string, error) {
	if ah.store == nil {
		ah.store = newMemorySessionStore()
	}

	token, err := GenerateSecureToken(32)
	if err != nil {
		return "", fmt.Errorf("failed to create session token: %w", err)
	}

	expiresAt := time.Now().Add(ah.tokenExpiry)
	if err := ah.store.Save(token, userID, expiresAt); err != nil {
		return "", fmt.Errorf("failed to persist session token: %w", err)
	}

	return token, nil
}

// ValidateSessionToken returns the user ID for a token issued by
// CreateLoginSession. It returns an error when the token is unknown or
// expired, or when the store lookup fails. RequireAuth uses it.
func (ah *AuthHelper) ValidateSessionToken(token string) (string, error) {
	if ah.store == nil {
		return "", errors.New("no session store configured")
	}

	rec, ok, err := ah.store.Lookup(token)
	if err != nil {
		return "", fmt.Errorf("failed to look up session token: %w", err)
	}
	if !ok || (!rec.ExpiresAt.IsZero() && !time.Now().Before(rec.ExpiresAt)) {
		return "", errors.New("invalid or expired session token")
	}
	return rec.UserID, nil
}

// DestroyLoginSession revokes a previously issued session token.
func (ah *AuthHelper) DestroyLoginSession(token string) error {
	if ah.store == nil {
		return nil
	}
	return ah.store.Delete(token)
}

// isCommonPassword reports whether password, ignoring case, is on a short list
// of the most common passwords, is mostly one repeated character, or starts
// with a common keyboard or number run.
func isCommonPassword(password string) bool {
	// A short list: it stops only the most common choices.
	commonPasswords := []string{
		"password", "123456", "123456789", "qwerty", "abc123", "monkey",
		"1234567", "letmein", "trustno1", "dragon", "baseball", "111111",
		"iloveyou", "master", "sunshine", "ashley", "bailey", "passw0rd",
		"shadow", "123123", "654321", "superman", "qazwsx", "michael",
		"football", "admin", "password123", "welcome", "login", "princess",
	}

	lowerPassword := strings.ToLower(password)
	for _, common := range commonPasswords {
		if lowerPassword == common {
			return true
		}
	}

	// Go's regexp has no backreferences, so repeated characters are counted.
	if isRepeatingPattern(lowerPassword) {
		return true
	}

	for _, pattern := range leadingWeakPatterns {
		if pattern.MatchString(lowerPassword) {
			return true
		}
	}

	return false
}

// leadingWeakPatterns match passwords that start with a common keyboard or
// number run. They are anchored with ^ so the sequence counts only as a prefix.
var leadingWeakPatterns = []*regexp.Regexp{
	regexp.MustCompile(`^123`),
	regexp.MustCompile(`^abc`),
	regexp.MustCompile(`^qwe`),
	regexp.MustCompile(`^qwerty`),
}

// isRepeatingPattern reports whether one character makes up more than 60% of
// a password of 3 or more bytes.
func isRepeatingPattern(password string) bool {
	if len(password) < 3 {
		return false
	}

	charCount := make(map[rune]int)
	for _, char := range password {
		charCount[char]++
	}

	for _, count := range charCount {
		if float64(count)/float64(len(password)) > 0.6 {
			return true
		}
	}

	return false
}

// hasRepetitiveChars reports whether a password of 4 or more bytes has three
// identical bytes in a row, or any three-character run from the keyboard row
// string below.
func hasRepetitiveChars(password string) bool {
	if len(password) < 4 {
		return false
	}

	for i := 0; i < len(password)-2; i++ {
		if password[i] == password[i+1] && password[i+1] == password[i+2] {
			return true
		}
	}

	keyboard := "qwertyuiopasdfghjklzxcvbnm1234567890"
	for i := 0; i < len(password)-2; i++ {
		substr := strings.ToLower(password[i : i+3])
		if strings.Contains(keyboard, substr) {
			return true
		}
	}

	return false
}
