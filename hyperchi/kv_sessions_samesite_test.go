package hyperchi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// An unset CookieSameSite is the zero value, not SameSiteDefaultMode, and
// must still produce the documented Strict default rather than a cookie with
// no SameSite attribute at all. The handler writes nothing, which must still
// save the session.
func TestSessionCookieDefaultsToSameSiteStrict(t *testing.T) {
	store := newTestSessionStore(t)
	handler := store.SessionMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session := GetSessionFromContext(r)
		if session == nil {
			t.Fatal("no session on the request")
		}
		session.Set("seen", true)
	}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	cookies := rec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("no session cookie was set")
	}
	if got := cookies[0].SameSite; got != http.SameSiteStrictMode {
		t.Fatalf("session cookie SameSite = %v, want Strict (%v)", got, http.SameSiteStrictMode)
	}
}
