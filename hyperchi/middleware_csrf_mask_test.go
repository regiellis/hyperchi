package hyperchi

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// csrfMaskApp renders c.CSRFToken() as the page body and accepts posts.
func csrfMaskApp(t *testing.T) *HyperChi {
	t.Helper()
	app := newTemplateApp(t, true)
	app.Use(app.Middleware.CSRF())
	app.Get("/page", func(c *Context) error { return c.Text(c.CSRFToken()) })
	app.Post("/save", func(c *Context) error { return c.Text("saved") })
	return app
}

// csrfPage loads /page, optionally with an existing cookie, and returns the
// rendered token and the cookie the visitor holds afterwards.
func csrfPage(t *testing.T, app *HyperChi, cookie *http.Cookie) (string, *http.Cookie) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/page", nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	w := serve(app, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /page = %d", w.Code)
	}
	if set := w.Result().Cookies(); len(set) == 1 {
		cookie = set[0]
	}
	if cookie == nil {
		t.Fatal("no csrf_token cookie")
	}
	return w.Body.String(), cookie
}

func postCSRF(app *HyperChi, cookie *http.Cookie, header, field string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/save", strings.NewReader(url.Values{"csrf_token": {field}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if header != "" {
		req.Header.Set("X-CSRF-Token", header)
	}
	req.AddCookie(cookie)
	return serve(app, req)
}

// Each render masks the token with a fresh pad, so a compressed page never
// repeats the secret, yet every rendered value still validates.
func TestCSRFMaskedTokensDifferPerRenderAndBothValidate(t *testing.T) {
	app := csrfMaskApp(t)
	first, cookie := csrfPage(t, app, nil)
	second, _ := csrfPage(t, app, cookie)
	if first == second {
		t.Fatalf("two renders produced the same token %q", first)
	}
	for _, token := range []string{first, second} {
		if token == cookie.Value || strings.Contains(token, cookie.Value) {
			t.Fatalf("rendered token %q carries the raw cookie value", token)
		}
		if w := postCSRF(app, cookie, token, ""); w.Code != http.StatusOK {
			t.Errorf("masked token in header = %d", w.Code)
		}
		if w := postCSRF(app, cookie, "", token); w.Code != http.StatusOK {
			t.Errorf("masked token in form field = %d", w.Code)
		}
	}
}

func TestCSRFTamperedMaskedTokenFails(t *testing.T) {
	app := csrfMaskApp(t)
	token, cookie := csrfPage(t, app, nil)
	buf, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range []int{0, len(buf) / 2, len(buf) - 1} {
		tampered := append([]byte(nil), buf...)
		tampered[i] ^= 0x01
		forged := base64.RawURLEncoding.EncodeToString(tampered)
		if w := postCSRF(app, cookie, forged, ""); w.Code != http.StatusForbidden {
			t.Errorf("byte %d flipped, header: %d, want 403", i, w.Code)
		}
		if w := postCSRF(app, cookie, "", forged); w.Code != http.StatusForbidden {
			t.Errorf("byte %d flipped, form field: %d, want 403", i, w.Code)
		}
	}
	// A token masked for another visitor does not validate here.
	other, _ := csrfPage(t, app, nil)
	if w := postCSRF(app, cookie, other, ""); w.Code != http.StatusForbidden {
		t.Errorf("another visitor's token: %d, want 403", w.Code)
	}
}

// Scripts that read the cookie send its raw value as a header. The form field
// takes masked tokens only, so a raw token is never a field a page echoes.
func TestCSRFRawCookieValueInHeaderPasses(t *testing.T) {
	app := csrfMaskApp(t)
	_, cookie := csrfPage(t, app, nil)
	if w := postCSRF(app, cookie, cookie.Value, ""); w.Code != http.StatusOK {
		t.Fatalf("raw cookie value in header = %d, want 200", w.Code)
	}
	if w := postCSRF(app, cookie, "", cookie.Value); w.Code != http.StatusForbidden {
		t.Fatalf("raw cookie value in form field = %d, want 403", w.Code)
	}
}

func TestCSRFWrongLengthTokensFailWithoutPanic(t *testing.T) {
	app := csrfMaskApp(t)
	token, cookie := csrfPage(t, app, nil)
	buf, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		t.Fatal(err)
	}
	enc := base64.RawURLEncoding.EncodeToString
	for name, sent := range map[string]string{
		"one byte":          enc([]byte{1}),
		"odd length":        enc(buf[:len(buf)-1]),
		"half":              enc(buf[:len(buf)/2]),
		"doubled":           enc(append(append([]byte(nil), buf...), buf...)),
		"truncated text":    token[:len(token)-1],
		"not base64":        "!!!" + token,
		"padded base64":     base64.URLEncoding.EncodeToString(buf),
		"raw cookie, short": cookie.Value[:10],
		"huge":              strings.Repeat("A", 8192),
		"all zero bytes":    enc(append(make([]byte, len(buf)/2), make([]byte, len(buf)/2)...)),
	} {
		t.Run(name, func(t *testing.T) {
			if w := postCSRF(app, cookie, sent, ""); w.Code != http.StatusForbidden {
				t.Errorf("header %q: %d, want 403", sent, w.Code)
			}
			if w := postCSRF(app, cookie, "", sent); w.Code != http.StatusForbidden {
				t.Errorf("field %q: %d, want 403", sent, w.Code)
			}
		})
	}
}
