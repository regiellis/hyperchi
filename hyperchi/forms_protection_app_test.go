package hyperchi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The package-level ProtectForm answers denials as plain text; the app method
// routes them through the error pipeline like every other denial.
func TestAppProtectFormDenialsReachOnError(t *testing.T) {
	app := New()
	var seen int
	app.OnError(func(c *Context, err error) {
		if he, ok := err.(*HTTPError); ok {
			seen = he.Code
			if werr := c.Status(he.Code).Text("handled " + he.Message); werr != nil {
				t.Error(werr)
			}
		}
	})
	protect, err := app.ProtectForm(FormProtectionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	app.Post("/signup", func(c *Context) error { return c.Text("ok") }, protect)

	req := httptest.NewRequest(http.MethodPost, "/signup", strings.NewReader("email=a@b.co"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || seen != http.StatusForbidden || !strings.HasPrefix(rec.Body.String(), "handled") {
		t.Fatalf("cross-site denial = %d %q (OnError saw %d)", rec.Code, rec.Body.String(), seen)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("denial is cacheable: %v", rec.Header())
	}
}
