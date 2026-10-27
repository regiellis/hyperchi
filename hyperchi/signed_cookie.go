// The application secret key and the cookies it signs.

package hyperchi

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/regiellis/hyperchi/hyperchi/config"
)

// ErrNoSecretKey is returned by every signing and verifying call when no
// secret key is configured outside development. Set SECRET_KEY
// (config.Security.SecretKey) to at least config.MinSecretKeyLength bytes.
var ErrNoSecretKey = errors.New("hyperchi: no secret key configured: set SECRET_KEY (config.Security.SecretKey) to sign cookies and flash messages")

// Errors from Context.SignedCookie and Context.SetSignedCookie. They are
// wrapped with the cookie name; test for them with errors.Is.
var (
	// ErrCookieMissing means the request does not carry the cookie.
	ErrCookieMissing = errors.New("hyperchi: cookie not set")
	// ErrCookieInvalid means the value was not signed by any configured key
	// for this cookie name: tampered, truncated, moved from another cookie,
	// or signed by a key that has since been removed.
	ErrCookieInvalid = errors.New("hyperchi: cookie signature is invalid")
	// ErrCookieExpired means the signature is good but the value is older
	// than the maxAge passed to SignedCookie.
	ErrCookieExpired = errors.New("hyperchi: cookie has expired")
	// ErrCookieTooLarge means the signed cookie would exceed the 4096 bytes
	// browsers are required to store, so a browser could drop it silently.
	ErrCookieTooLarge = errors.New("hyperchi: cookie exceeds 4096 bytes")
)

const (
	// maxCookieBytes is the Set-Cookie size every browser must accept
	// (RFC 6265 section 6.1: name, value, and attributes).
	maxCookieBytes = 4096

	signedCookieVersion byte = 1
	signedHeaderLen          = 1 + 8 // version, issued-at seconds
	signedMACLen             = sha256.Size
)

// keyring holds the application secret keys. keys[0] signs and every key
// verifies. A development app without a configured key signs with the
// per-process development key. A production app without one has no keys and
// fails with ErrNoSecretKey.
type keyring struct {
	keys    [][]byte
	dev     bool
	missing sync.Once // logs the first signing attempt without a key
}

// newKeyring builds the app's keyring from a validated configuration. It
// never logs a key.
func newKeyring(cfg *config.Config) *keyring {
	sec := cfg.Security
	if sec.SecretKey == "" {
		if cfg.IsDevelopment() {
			return &keyring{dev: true}
		}
		return &keyring{}
	}
	keys := make([][]byte, 0, 1+len(sec.SecretKeyPrevious))
	keys = append(keys, []byte(sec.SecretKey))
	for _, key := range sec.SecretKeyPrevious {
		keys = append(keys, []byte(key))
	}
	return &keyring{keys: keys}
}

// devKey is the per-process key a development app signs with when no key is
// configured. It is made on first use, so an app that signs nothing never
// logs about it.
var devKey struct {
	once sync.Once
	key  []byte
	err  error
}

// active returns the keys to sign and verify with, the signing key first.
func (k *keyring) active() ([][]byte, error) {
	if k == nil {
		return nil, ErrNoSecretKey
	}
	if !k.dev {
		if len(k.keys) == 0 {
			// Logged on first use, not at startup: an app that never signs
			// needs no key and should not warn about one.
			k.missing.Do(func() {
				log.Printf("hyperchi: SECRET_KEY is unset: signed cookies and flash messages fail with ErrNoSecretKey until it is set")
			})
			return nil, ErrNoSecretKey
		}
		return k.keys, nil
	}
	devKey.once.Do(func() {
		key := make([]byte, config.MinSecretKeyLength)
		if _, err := rand.Read(key); err != nil {
			devKey.err = fmt.Errorf("hyperchi: generate development secret key: %w", err)
			return
		}
		devKey.key = key
		log.Printf("hyperchi: SECRET_KEY is unset: signing with a random development key, so signed cookies and flash messages will not survive a restart")
	})
	if devKey.err != nil {
		return nil, devKey.err
	}
	return [][]byte{devKey.key}, nil
}

// signedMAC is HMAC-SHA256 over a domain label, the cookie name, the header
// (version and issued-at), and the value. Cookie names cannot contain a NUL
// byte, so the separator keeps name and header apart.
func signedMAC(key []byte, name string, header, value []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("hyperchi-signed-cookie\x00"))
	mac.Write([]byte(name))
	mac.Write([]byte{0})
	mac.Write(header)
	mac.Write(value)
	return mac.Sum(nil)
}

// sign returns base64url(version | issued-at | value | mac) for the cookie
// name, signed with the current key.
func (k *keyring) sign(name string, value []byte, now time.Time) (string, error) {
	keys, err := k.active()
	if err != nil {
		return "", err
	}
	buf := make([]byte, 0, signedHeaderLen+len(value)+signedMACLen)
	buf = append(buf, signedCookieVersion)
	buf = binary.BigEndian.AppendUint64(buf, uint64(now.Unix()))
	buf = append(buf, value...)
	buf = append(buf, signedMAC(keys[0], name, buf[:signedHeaderLen], value)...)
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// verify checks encoded against every key and returns the value. The
// signature is checked before the age, so an expired forgery is reported as
// invalid. maxAge zero or less skips the age check.
func (k *keyring) verify(name, encoded string, now time.Time, maxAge time.Duration) ([]byte, error) {
	keys, err := k.active()
	if err != nil {
		return nil, err
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(raw) < signedHeaderLen+signedMACLen || raw[0] != signedCookieVersion {
		return nil, ErrCookieInvalid
	}
	header := raw[:signedHeaderLen]
	value := raw[signedHeaderLen : len(raw)-signedMACLen]
	got := raw[len(raw)-signedMACLen:]
	// Every key is tried, so the time taken does not reveal which matched.
	ok := 0
	for _, key := range keys {
		if hmac.Equal(got, signedMAC(key, name, header, value)) {
			ok = 1
		}
	}
	if ok == 0 {
		return nil, ErrCookieInvalid
	}
	if maxAge > 0 {
		issued := time.Unix(int64(binary.BigEndian.Uint64(header[1:])), 0)
		if now.Sub(issued) > maxAge {
			return nil, ErrCookieExpired
		}
	}
	return value, nil
}

// now is the app's clock: the real time, or the one a test substitutes.
func (g *HyperChi) now() time.Time { return g.schedulerClock().Now() }

// secureCookieDefaults fills in the framework's cookie defaults: Path "/",
// SameSite Lax when unset, HttpOnly always, and Secure in production.
func (g *HyperChi) secureCookieDefaults(ck *http.Cookie) {
	if ck.Path == "" {
		ck.Path = "/"
	}
	// The zero SameSite is 0, not SameSiteDefaultMode (1). Both mean unset.
	if ck.SameSite == 0 || ck.SameSite == http.SameSiteDefaultMode {
		ck.SameSite = http.SameSiteLaxMode
	}
	ck.HttpOnly = true
	if g.Config.IsProduction() {
		ck.Secure = true
	}
}

// SetSignedCookie signs cookie.Value with the app's secret key and adds the
// cookie to the response. The signature covers the cookie name, the value, and
// the time of signing, so the value cannot be edited, moved to another cookie
// name, or (with SignedCookie's maxAge) replayed after it is too old.
//
// The value is signed, not encrypted: it travels as readable base64url, so
// never put a secret in it.
//
// The caller's cookie is not modified. The sent copy gets the framework
// defaults: Path "/" and SameSite Lax when unset, HttpOnly always (the value is
// for the server), and Secure in production. It fails with ErrNoSecretKey in
// production without SECRET_KEY, ErrCookieTooLarge past 4096 bytes, an error
// for an invalid cookie name, or when the response has already been written.
func (c *Context) SetSignedCookie(cookie *http.Cookie) error {
	if cookie == nil {
		return errors.New("hyperchi: SetSignedCookie: nil cookie")
	}
	if c.w.written {
		return errResponseWritten
	}
	signed, err := c.app.keys.sign(cookie.Name, []byte(cookie.Value), c.app.now())
	if err != nil {
		return err
	}
	ck := *cookie
	ck.Value = signed
	c.app.secureCookieDefaults(&ck)
	if err := ck.Valid(); err != nil {
		return fmt.Errorf("hyperchi: SetSignedCookie: %w", err)
	}
	if len(ck.String()) > maxCookieBytes {
		return fmt.Errorf("%w: %q", ErrCookieTooLarge, ck.Name)
	}
	http.SetCookie(c.w, &ck)
	return nil
}

// SignedCookie returns the value of a cookie set with SetSignedCookie after
// checking its signature, in constant time, against the current and every
// previous secret key. A maxAge above zero also rejects a value signed longer
// ago than that; zero or less accepts any age.
//
// The error wraps ErrCookieMissing, ErrCookieInvalid, ErrCookieExpired, or
// ErrNoSecretKey:
//
//	id, err := c.SignedCookie("player", 30*24*time.Hour)
//	if err != nil {
//		id = newPlayerID() // missing, tampered, or too old: start over
//	}
func (c *Context) SignedCookie(name string, maxAge time.Duration) (string, error) {
	ck, err := c.r.Cookie(name)
	if err != nil {
		return "", fmt.Errorf("%w: %q", ErrCookieMissing, name)
	}
	value, err := c.app.keys.verify(name, ck.Value, c.app.now(), maxAge)
	if err != nil {
		if errors.Is(err, ErrNoSecretKey) {
			return "", err
		}
		return "", fmt.Errorf("%w: %q", err, name)
	}
	return string(value), nil
}
