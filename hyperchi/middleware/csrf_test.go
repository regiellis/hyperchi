package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestCSRF(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK")) // test recorder write never fails
	})

	csrfHandler := CSRF()(handler)

	t.Run("GET request should set CSRF token", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		rec := httptest.NewRecorder()

		csrfHandler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d", rec.Code)
		}

		cookies := rec.Result().Cookies()
		var csrfCookie *http.Cookie
		for _, cookie := range cookies {
			if cookie.Name == "csrf_token" {
				csrfCookie = cookie
				break
			}
		}

		if csrfCookie == nil {
			t.Fatal("CSRF cookie not set")
		}

		if csrfCookie.Value == "" {
			t.Fatal("CSRF cookie value is empty")
		}

		if csrfCookie.HttpOnly {
			t.Error("CSRF cookie should not be HttpOnly (needs to be readable by JavaScript)")
		}
	})

	t.Run("POST request without token should fail", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/", nil)
		rec := httptest.NewRecorder()

		csrfHandler.ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Errorf("Expected status 403, got %d", rec.Code)
		}
	})

	t.Run("POST request with valid token should succeed", func(t *testing.T) {
		getReq := httptest.NewRequest("GET", "/", nil)
		getRec := httptest.NewRecorder()
		csrfHandler.ServeHTTP(getRec, getReq)

		var tokenValue string
		cookies := getRec.Result().Cookies()
		for _, cookie := range cookies {
			if cookie.Name == "csrf_token" {
				tokenValue = cookie.Value
				break
			}
		}

		if tokenValue == "" {
			t.Fatal("Could not extract CSRF token from GET request")
		}

		form := url.Values{}
		form.Add("csrf_token", tokenValue)

		postReq := httptest.NewRequest("POST", "/", strings.NewReader(form.Encode()))
		postReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		postReq.AddCookie(&http.Cookie{
			Name:  "csrf_token",
			Value: tokenValue,
		})

		postRec := httptest.NewRecorder()
		csrfHandler.ServeHTTP(postRec, postReq)

		if postRec.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d", postRec.Code)
		}
	})

	t.Run("POST request with token in header should succeed", func(t *testing.T) {
		getReq := httptest.NewRequest("GET", "/", nil)
		getRec := httptest.NewRecorder()
		csrfHandler.ServeHTTP(getRec, getReq)

		var tokenValue string
		cookies := getRec.Result().Cookies()
		for _, cookie := range cookies {
			if cookie.Name == "csrf_token" {
				tokenValue = cookie.Value
				break
			}
		}

		if tokenValue == "" {
			t.Fatal("Could not extract CSRF token from GET request")
		}

		postReq := httptest.NewRequest("POST", "/", nil)
		postReq.Header.Set("X-CSRF-Token", tokenValue)

		postReq.AddCookie(&http.Cookie{
			Name:  "csrf_token",
			Value: tokenValue,
		})

		postRec := httptest.NewRecorder()
		csrfHandler.ServeHTTP(postRec, postReq)

		if postRec.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d", postRec.Code)
		}
	})

	t.Run("POST request with invalid token should fail", func(t *testing.T) {
		postReq := httptest.NewRequest("POST", "/", nil)
		postReq.Header.Set("X-CSRF-Token", "invalid-token")

		postReq.AddCookie(&http.Cookie{
			Name:  "csrf_token",
			Value: "different-token",
		})

		postRec := httptest.NewRecorder()
		csrfHandler.ServeHTTP(postRec, postReq)

		if postRec.Code != http.StatusForbidden {
			t.Errorf("Expected status 403, got %d", postRec.Code)
		}
	})
}

func TestCSRFWithConfig(t *testing.T) {
	config := &CSRFConfig{
		TokenLength:   16,
		CookieName:    "custom_csrf",
		HeaderName:    "X-Custom-CSRF",
		FormFieldName: "custom_token",
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK")) // test recorder write never fails
	})

	csrfHandler := CSRFWithConfig(config)(handler)

	t.Run("Should use custom configuration", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		rec := httptest.NewRecorder()

		csrfHandler.ServeHTTP(rec, req)

		cookies := rec.Result().Cookies()
		var csrfCookie *http.Cookie
		for _, cookie := range cookies {
			if cookie.Name == "custom_csrf" {
				csrfCookie = cookie
				break
			}
		}

		if csrfCookie == nil {
			t.Fatal("Custom CSRF cookie not set")
		}
	})
}

func TestGetCSRFToken(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := GetCSRFToken(r)
		if token == "" {
			t.Error("CSRF token not found in request context")
		}
		w.WriteHeader(http.StatusOK)
	})

	csrfHandler := CSRF()(handler)

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()

	csrfHandler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rec.Code)
	}
}

func TestCSRFTemplateHelper(t *testing.T) {
	helper := NewCSRFTemplateHelper()

	req := httptest.NewRequest("GET", "/", nil)
	req = req.WithContext(context.WithValue(req.Context(), csrfTokenKey, "test-token"))

	// The helpers render a masked token: a fresh string each call that
	// unmasks to the stored one.
	maskedValue := func(t *testing.T, markup, prefix, suffix string) {
		t.Helper()
		if !strings.HasPrefix(markup, prefix) || !strings.HasSuffix(markup, suffix) {
			t.Fatalf("markup = %q, want %s...%s", markup, prefix, suffix)
		}
		value := strings.TrimSuffix(strings.TrimPrefix(markup, prefix), suffix)
		if value == "test-token" {
			t.Fatal("helper rendered the raw token")
		}
		if !csrfTokenMatches("test-token", value) {
			t.Fatalf("rendered value %q does not unmask to the token", value)
		}
	}

	t.Run("TokenInput should generate input field", func(t *testing.T) {
		maskedValue(t, helper.TokenInput(req), `<input type="hidden" name="csrf_token" value="`, `">`)
	})

	t.Run("TokenMeta should generate meta tag", func(t *testing.T) {
		maskedValue(t, helper.TokenMeta(req), `<meta name="csrf-token" content="`, `">`)
	})

	t.Run("Should handle missing token gracefully", func(t *testing.T) {
		emptyReq := httptest.NewRequest("GET", "/", nil)

		input := helper.TokenInput(emptyReq)
		if !strings.Contains(input, "not available") {
			t.Error("Should indicate token not available")
		}

		meta := helper.TokenMeta(emptyReq)
		if !strings.Contains(meta, "not available") {
			t.Error("Should indicate token not available")
		}
	})
}

func TestSafeMethods(t *testing.T) {
	skipMethods := []string{"GET", "HEAD", "OPTIONS", "TRACE"}

	testCases := []struct {
		method string
		safe   bool
	}{
		{"GET", true},
		{"HEAD", true},
		{"OPTIONS", true},
		{"TRACE", true},
		{"POST", false},
		{"PUT", false},
		{"DELETE", false},
		{"PATCH", false},
	}

	for _, tc := range testCases {
		t.Run(tc.method, func(t *testing.T) {
			result := isSafeMethod(tc.method, skipMethods)
			if result != tc.safe {
				t.Errorf("isSafeMethod(%q) = %v, expected %v", tc.method, result, tc.safe)
			}
		})
	}
}

// Pages render a different masked token each time; every mask and the raw
// cookie value pass, and a tampered or foreign token fails.
func TestCSRFMaskedTokens(t *testing.T) {
	config := DefaultCSRFConfig()
	config.Secure = false
	mw := CSRFWithConfig(config)

	var first, second string
	issue := httptest.NewRecorder()
	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		first, second = GetCSRFToken(r), GetCSRFToken(r)
	})).ServeHTTP(issue, httptest.NewRequest(http.MethodGet, "/", nil))
	cookies := issue.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("got %d cookies, want 1", len(cookies))
	}
	raw := cookies[0].Value
	if first == second || first == raw || second == raw {
		t.Fatalf("tokens must differ per call and from the cookie: %q %q %q", first, second, raw)
	}

	post := func(token string) int {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("csrf_token="+token))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(cookies[0])
		rec := httptest.NewRecorder()
		mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		})).ServeHTTP(rec, req)
		return rec.Code
	}
	for name, token := range map[string]string{"first mask": first, "second mask": second, "raw cookie": raw} {
		if code := post(token); code != http.StatusNoContent {
			t.Errorf("%s: status %d, want 204", name, code)
		}
	}
	tampered := []byte(first)
	tampered[len(tampered)-1] ^= 1
	if tampered[len(tampered)-1] == '_' || tampered[len(tampered)-1] == '-' {
		tampered[len(tampered)-1] = 'A'
	}
	for name, token := range map[string]string{"tampered": string(tampered), "garbage": "!!!", "short": "AA"} {
		if code := post(token); code != http.StatusForbidden {
			t.Errorf("%s: status %d, want 403", name, code)
		}
	}
}
