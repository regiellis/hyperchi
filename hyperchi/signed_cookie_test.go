package hyperchi

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/regiellis/hyperchi/hyperchi/config"
)

var (
	testKeyA = strings.Repeat("a", config.MinSecretKeyLength)
	testKeyB = strings.Repeat("b", config.MinSecretKeyLength)
)

// keyApp builds an app without a database. env is "development" or
// "production"; key and previous configure the secret keys.
func keyApp(t *testing.T, env, key string, previous ...string) *HyperChi {
	t.Helper()
	cfg := config.Default()
	cfg.Server.Environment = env
	cfg.Database.Disabled = true
	cfg.Security.SecretKey = key
	cfg.Security.SecretKeyPrevious = previous
	app, err := NewWithConfigE(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return app
}

// captureLog sends the standard logger to a buffer until the test ends.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev) })
	return &buf
}

// resetDevKey forgets the per-process development key, so a test sees the
// first-use path.
func resetDevKey(t *testing.T) {
	t.Helper()
	devKey.once = sync.Once{}
	devKey.key = nil
	devKey.err = nil
}

// setSigned runs SetSignedCookie in a request and returns the cookie sent.
func setSigned(t *testing.T, app *HyperChi, ck *http.Cookie) *http.Cookie {
	t.Helper()
	rec := httptest.NewRecorder()
	c := app.NewContext(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if err := c.SetSignedCookie(ck); err != nil {
		t.Fatalf("SetSignedCookie: %v", err)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("sent %d cookies, want 1", len(cookies))
	}
	return cookies[0]
}

// readSigned runs SignedCookie for name in a request carrying sent.
func readSigned(app *HyperChi, sent *http.Cookie, name string, maxAge time.Duration) (string, error) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if sent != nil {
		req.AddCookie(sent)
	}
	return app.NewContext(httptest.NewRecorder(), req).SignedCookie(name, maxAge)
}

func TestSignedCookieRoundTrip(t *testing.T) {
	app := keyApp(t, "development", testKeyA)
	original := &http.Cookie{Name: "player", Value: "id; with spaces=and \"quotes\""}
	sent := setSigned(t, app, original)

	if original.Value != "id; with spaces=and \"quotes\"" || original.HttpOnly {
		t.Fatal("SetSignedCookie modified the caller's cookie")
	}
	if !sent.HttpOnly || sent.SameSite != http.SameSiteLaxMode || sent.Path != "/" || sent.Secure {
		t.Fatalf("development defaults: HttpOnly=%v SameSite=%v Path=%q Secure=%v", sent.HttpOnly, sent.SameSite, sent.Path, sent.Secure)
	}
	if _, err := base64.RawURLEncoding.DecodeString(sent.Value); err != nil {
		t.Fatalf("value is not base64url: %v", err)
	}
	got, err := readSigned(app, sent, "player", time.Hour)
	if err != nil || got != original.Value {
		t.Fatalf("SignedCookie = %q, %v", got, err)
	}

	prod := keyApp(t, "production", testKeyA)
	if !setSigned(t, prod, &http.Cookie{Name: "player", Value: "x"}).Secure {
		t.Fatal("production signed cookie is not Secure")
	}
}

func TestSignedCookieMissing(t *testing.T) {
	app := keyApp(t, "development", testKeyA)
	if _, err := readSigned(app, nil, "player", 0); !errors.Is(err, ErrCookieMissing) {
		t.Fatalf("err = %v, want ErrCookieMissing", err)
	}
}

func TestSignedCookieRejectsTampering(t *testing.T) {
	app := keyApp(t, "development", testKeyA)
	sent := setSigned(t, app, &http.Cookie{Name: "role", Value: "member"})
	raw, err := base64.RawURLEncoding.DecodeString(sent.Value)
	if err != nil {
		t.Fatal(err)
	}

	edited := append([]byte(nil), raw...)
	copy(edited[signedHeaderLen:], "admin!")
	backdated := append([]byte(nil), raw...)
	backdated[signedHeaderLen-1]++

	cases := map[string]*http.Cookie{
		"value edited":   {Name: "role", Value: base64.RawURLEncoding.EncodeToString(edited)},
		"issued edited":  {Name: "role", Value: base64.RawURLEncoding.EncodeToString(backdated)},
		"truncated":      {Name: "role", Value: sent.Value[:len(sent.Value)-4]},
		"too short":      {Name: "role", Value: base64.RawURLEncoding.EncodeToString(raw[:signedHeaderLen+signedMACLen-1])},
		"not base64":     {Name: "role", Value: "not*base64"},
		"empty":          {Name: "role", Value: ""},
		"unsigned value": {Name: "role", Value: "admin"},
	}
	for name, ck := range cases {
		if _, err := readSigned(app, ck, "role", 0); !errors.Is(err, ErrCookieInvalid) {
			t.Errorf("%s: err = %v, want ErrCookieInvalid", name, err)
		}
	}

	// A value signed for one cookie name does not verify under another.
	moved := &http.Cookie{Name: "admin_role", Value: sent.Value}
	if _, err := readSigned(app, moved, "admin_role", 0); !errors.Is(err, ErrCookieInvalid) {
		t.Errorf("name swap: err = %v, want ErrCookieInvalid", err)
	}

	// A different key rejects it.
	other := keyApp(t, "development", testKeyB)
	if _, err := readSigned(other, sent, "role", 0); !errors.Is(err, ErrCookieInvalid) {
		t.Errorf("wrong key: err = %v, want ErrCookieInvalid", err)
	}
}

func TestSignedCookieKeyRotation(t *testing.T) {
	old := keyApp(t, "production", testKeyA)
	rotated := keyApp(t, "production", testKeyB, testKeyA)

	signedOld := setSigned(t, old, &http.Cookie{Name: "player", Value: "p1"})
	if got, err := readSigned(rotated, signedOld, "player", 0); err != nil || got != "p1" {
		t.Fatalf("previous key does not verify: %q, %v", got, err)
	}

	signedNew := setSigned(t, rotated, &http.Cookie{Name: "player", Value: "p2"})
	if _, err := readSigned(old, signedNew, "player", 0); !errors.Is(err, ErrCookieInvalid) {
		t.Fatalf("rotated app did not sign with the new key: %v", err)
	}
	onlyNew := keyApp(t, "production", testKeyB)
	if got, err := readSigned(onlyNew, signedNew, "player", 0); err != nil || got != "p2" {
		t.Fatalf("new key: %q, %v", got, err)
	}
}

func TestSignedCookieExpiry(t *testing.T) {
	app := keyApp(t, "development", testKeyA)
	clock := newFakeClock(time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC))
	app.clock = clock
	sent := setSigned(t, app, &http.Cookie{Name: "player", Value: "p1"})

	clock.Advance(59 * time.Minute)
	if _, err := readSigned(app, sent, "player", time.Hour); err != nil {
		t.Fatalf("fresh value rejected: %v", err)
	}
	clock.Advance(2 * time.Minute)
	if _, err := readSigned(app, sent, "player", time.Hour); !errors.Is(err, ErrCookieExpired) {
		t.Fatalf("old value: err = %v, want ErrCookieExpired", err)
	}
	if got, err := readSigned(app, sent, "player", 0); err != nil || got != "p1" {
		t.Fatalf("maxAge 0 should accept any age: %q, %v", got, err)
	}
}

func TestSignedCookieTooLarge(t *testing.T) {
	app := keyApp(t, "development", testKeyA)
	c := app.NewContext(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	err := c.SetSignedCookie(&http.Cookie{Name: "big", Value: strings.Repeat("x", 3500)})
	if !errors.Is(err, ErrCookieTooLarge) {
		t.Fatalf("err = %v, want ErrCookieTooLarge", err)
	}
}

func TestSignedCookieWithoutKeyInProduction(t *testing.T) {
	logs := captureLog(t)
	app := keyApp(t, "production", "")
	if n := strings.Count(logs.String(), "SECRET_KEY is unset"); n != 0 {
		t.Fatalf("startup logged the missing key %d times, want 0 (an app that never signs needs no key): %q", n, logs.String())
	}

	c := app.NewContext(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	for range 2 {
		if err := c.SetSignedCookie(&http.Cookie{Name: "player", Value: "p1"}); !errors.Is(err, ErrNoSecretKey) {
			t.Fatalf("SetSignedCookie err = %v, want ErrNoSecretKey", err)
		}
	}
	if n := strings.Count(logs.String(), "SECRET_KEY is unset"); n != 1 {
		t.Fatalf("signing without a key logged %d times, want once: %q", n, logs.String())
	}
	signed := setSigned(t, keyApp(t, "production", testKeyA), &http.Cookie{Name: "player", Value: "p1"})
	if _, err := readSigned(app, signed, "player", 0); !errors.Is(err, ErrNoSecretKey) {
		t.Fatalf("SignedCookie err = %v, want ErrNoSecretKey", err)
	}
	if err := c.Flash("info", "hi"); !errors.Is(err, ErrNoSecretKey) {
		t.Fatalf("Flash err = %v, want ErrNoSecretKey", err)
	}
}

func TestSignedCookieWithoutKeyInDevelopment(t *testing.T) {
	resetDevKey(t)
	logs := captureLog(t)
	app := keyApp(t, "development", "")
	other := keyApp(t, "development", "")
	if logs.Len() != 0 {
		t.Fatalf("an app that signs nothing logged: %q", logs.String())
	}

	sent := setSigned(t, app, &http.Cookie{Name: "player", Value: "p1"})
	setSigned(t, app, &http.Cookie{Name: "player", Value: "p2"})
	// The key is per process, so another app in the process verifies it.
	if got, err := readSigned(other, sent, "player", 0); err != nil || got != "p1" {
		t.Fatalf("development key round trip: %q, %v", got, err)
	}
	if n := strings.Count(logs.String(), "will not survive a restart"); n != 1 {
		t.Fatalf("development key logged %d times, want 1: %q", n, logs.String())
	}
	for _, enc := range []string{string(devKey.key), base64.StdEncoding.EncodeToString(devKey.key), base64.RawURLEncoding.EncodeToString(devKey.key)} {
		if strings.Contains(logs.String(), enc) {
			t.Fatal("the development key appears in the log")
		}
	}
}

func TestShortSecretKeyRejected(t *testing.T) {
	cfg := config.Default()
	cfg.Database.Disabled = true
	cfg.Security.SecretKey = "too-short"
	_, err := NewWithConfigE(cfg)
	if err == nil || !strings.Contains(err.Error(), "at least 32") {
		t.Fatalf("err = %v, want the 32-byte minimum", err)
	}
	if strings.Contains(err.Error(), "too-short") {
		t.Fatal("the error message contains the key")
	}
}

// The configured keys never appear in a log line, a JSON dump of the config,
// or a formatted config.
func TestSecretKeyNeverLoggedOrSerialized(t *testing.T) {
	const current = "current-key-0123456789-0123456789-abc"
	const previous = "retired-key-0123456789-0123456789-xyz"
	logs := captureLog(t)
	app := keyApp(t, "production", current, previous)

	sent := setSigned(t, app, &http.Cookie{Name: "player", Value: "p1"})
	if _, err := readSigned(app, &http.Cookie{Name: "player", Value: sent.Value + "x"}, "player", 0); err == nil {
		t.Fatal("tampered value accepted")
	}
	body, err := json.Marshal(app.Config)
	if err != nil {
		t.Fatal(err)
	}
	log.Printf("config: %v %+v", app.Config, app.Config)
	for name, out := range map[string]string{"log": logs.String(), "json": string(body)} {
		if strings.Contains(out, current) || strings.Contains(out, previous) {
			t.Errorf("%s contains a secret key: %s", name, out)
		}
	}
}
