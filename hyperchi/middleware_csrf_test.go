package hyperchi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func csrfApp(t *testing.T) *HyperChi {
	t.Helper()
	app := newTemplateApp(t, true)
	app.Use(app.Middleware.CSRF())
	app.Post("/save", func(c *Context) error { return c.Text("saved") })
	return app
}

func assertNoStoreDenial(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403; body %q", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control %q, want no-store", got)
	}
	if got := w.Header().Get("X-Robots-Tag"); got != "noindex" {
		t.Fatalf("X-Robots-Tag %q, want noindex", got)
	}
}

// CSRF denials render through the error pipeline like every other denial,
// not as plain-text http.Error responses.
func TestCSRFDenialRendersHTMXFragment(t *testing.T) {
	app := csrfApp(t)
	req := htmxReq(http.MethodPost, "/save")
	w := serve(app, req)
	assertNoStoreDenial(t, w)
	body := w.Body.String()
	if !strings.Contains(body, `<section class="err">403: CSRF token missing</section>`) {
		t.Fatalf("htmx denial did not render the error fragment: %q", body)
	}
	if strings.Contains(body, "<html>") {
		t.Fatalf("htmx denial rendered a full page: %q", body)
	}
}

func TestCSRFMismatchRendersJSON(t *testing.T) {
	app := csrfApp(t)
	req := httptest.NewRequest(http.MethodPost, "/save", strings.NewReader(`{}`))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", "forged")
	req.AddCookie(&http.Cookie{Name: "csrf_token", Value: "real"})
	w := serve(app, req)
	assertNoStoreDenial(t, w)
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("denial is not JSON: %v; body %q", err, w.Body.String())
	}
	if got["error"] != "CSRF token mismatch" {
		t.Fatalf("JSON denial %v, want error \"CSRF token mismatch\"", got)
	}
}

func TestCSRFDenialReachesOnError(t *testing.T) {
	app := csrfApp(t)
	var seen *HTTPError
	app.OnError(func(c *Context, err error) {
		if !errors.As(err, &seen) {
			t.Errorf("OnError got %T, want *HTTPError", err)
			return
		}
		if err := c.Status(seen.Code).Text("custom"); err != nil {
			t.Error(err)
		}
	})
	form := url.Values{"title": {"x"}}
	req := httptest.NewRequest(http.MethodPost, "/save", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "csrf_token", Value: "real"})
	w := serve(app, req)
	assertNoStoreDenial(t, w)
	if w.Body.String() != "custom" || seen == nil || seen.Code != http.StatusForbidden || seen.Message != "CSRF token not found in request" {
		t.Fatalf("OnError not used for the denial: body %q, err %+v", w.Body.String(), seen)
	}
}

func TestCSRFValidTokenPasses(t *testing.T) {
	app := csrfApp(t)
	req := httptest.NewRequest(http.MethodPost, "/save", nil)
	req.Header.Set("X-CSRF-Token", "same")
	req.AddCookie(&http.Cookie{Name: "csrf_token", Value: "same"})
	if w := serve(app, req); w.Code != http.StatusOK || w.Body.String() != "saved" {
		t.Fatalf("valid token: %d %q", w.Code, w.Body.String())
	}
}
