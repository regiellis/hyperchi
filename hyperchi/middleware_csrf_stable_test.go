package hyperchi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A page's own asset requests are safe requests too. Minting a new token on
// each would replace the cookie, the token rendered into the page's forms would
// stop matching, and every submit would be refused.
func TestCSRFTokenStaysStableAcrossSafeRequests(t *testing.T) {
	app := newTemplateApp(t, true)
	app.Use(app.Middleware.CSRF())
	app.Get("/page", func(c *Context) error { return c.Text(c.CSRFToken()) })
	app.Get("/style.css", func(c *Context) error { return c.Text("body{}") })
	app.Post("/save", func(c *Context) error { return c.Text("saved") })

	first := httptest.NewRecorder()
	app.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/page", nil))
	cookies := first.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != "csrf_token" {
		t.Fatalf("first visit cookies = %v", cookies)
	}
	token := first.Body.String()
	if !csrfTokenMatches(cookies[0].Value, token, false) {
		t.Fatalf("page token %q does not unmask to cookie %q", token, cookies[0].Value)
	}

	asset := httptest.NewRequest(http.MethodGet, "/style.css", nil)
	asset.AddCookie(cookies[0])
	again := httptest.NewRecorder()
	app.ServeHTTP(again, asset)
	if got := again.Result().Cookies(); len(got) != 0 {
		t.Fatalf("asset request replaced the token cookie: %v", got)
	}

	post := httptest.NewRequest(http.MethodPost, "/save", strings.NewReader("csrf_token="+token))
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	post.AddCookie(cookies[0])
	saved := httptest.NewRecorder()
	app.ServeHTTP(saved, post)
	if saved.Code != http.StatusOK {
		t.Fatalf("submit after an asset fetch = %d, want 200", saved.Code)
	}

	// A malformed cookie is replaced, not trusted.
	bad := httptest.NewRequest(http.MethodGet, "/page", nil)
	bad.AddCookie(&http.Cookie{Name: "csrf_token", Value: "<script>"})
	fresh := httptest.NewRecorder()
	app.ServeHTTP(fresh, bad)
	if got := fresh.Result().Cookies(); len(got) != 1 || got[0].Value == "<script>" || !csrfTokenMatches(got[0].Value, fresh.Body.String(), false) {
		t.Fatalf("malformed cookie not replaced: cookies %v, body %q", got, fresh.Body.String())
	}
}
