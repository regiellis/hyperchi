package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// renderThroughCSRF runs one GET through CSRFWithConfig and returns what
// render produced from the request the middleware passed on.
func renderThroughCSRF(t *testing.T, config *CSRFConfig, render func(r *http.Request) string) string {
	t.Helper()
	var out string
	h := CSRFWithConfig(config)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		out = render(r)
		w.WriteHeader(http.StatusNoContent)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	return out
}

// htmx 4 turns implicit inheritance off, so a plain hx-headers on <body>
// never reaches the requests of elements inside it.
func TestTokenHXHeadersIsInherited(t *testing.T) {
	helper := NewCSRFTemplateHelper()
	got := renderThroughCSRF(t, DefaultCSRFConfig(), func(r *http.Request) string {
		return string(helper.TokenHXHeaders(r))
	})
	if !strings.HasPrefix(got, `hx-headers:inherited='{"X-CSRF-Token":"`) {
		t.Fatalf("TokenHXHeaders = %q, want an hx-headers:inherited attribute", got)
	}
}

func TestTokenHelpersUseConfiguredNames(t *testing.T) {
	config := DefaultCSRFConfig()
	config.FormFieldName = "authenticity_token"
	config.HeaderName = "X-App-CSRF"
	helper := NewCSRFTemplateHelper()

	input := renderThroughCSRF(t, config, helper.TokenInput)
	if !strings.Contains(input, `name="authenticity_token"`) {
		t.Fatalf("TokenInput = %q, want the configured field name", input)
	}

	headers := renderThroughCSRF(t, config, func(r *http.Request) string {
		return string(helper.TokenHXHeaders(r))
	})
	if !strings.Contains(headers, `"X-App-CSRF":`) {
		t.Fatalf("TokenHXHeaders = %q, want the configured header name", headers)
	}
}

// The field the helper writes must be the one the middleware reads back.
func TestTokenInputRoundTripsWithCustomFieldName(t *testing.T) {
	config := DefaultCSRFConfig()
	config.Secure = false
	config.FormFieldName = "authenticity_token"
	helper := NewCSRFTemplateHelper()

	var token string
	mw := CSRFWithConfig(config)
	issue := httptest.NewRecorder()
	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token = GetCSRFToken(r)
		if !strings.Contains(helper.TokenInput(r), `name="authenticity_token"`) {
			t.Error("TokenInput ignored FormFieldName")
		}
	})).ServeHTTP(issue, httptest.NewRequest(http.MethodGet, "/", nil))

	post := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("authenticity_token="+token))
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, c := range issue.Result().Cookies() {
		post.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(rec, post)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("POST with the helper's field: status %d, want 204", rec.Code)
	}
}
