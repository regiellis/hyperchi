package hyperchi

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/regiellis/hyperchi/hyperchi/patterns"
)

// A pattern that refuses a request (a bad wizard step) renders through the
// app's error pipeline, like every other denial.
func TestPatternErrorsReachOnError(t *testing.T) {
	app := New()
	var seen int
	app.OnError(func(c *Context, err error) {
		var he *HTTPError
		if errors.As(err, &he) {
			seen = he.Code
		}
		if werr := c.Status(http.StatusBadRequest).Text("handled"); werr != nil {
			t.Error(werr)
		}
	})
	pm := app.Extensions().GetPatternManager()
	if pm == nil {
		t.Fatal("no pattern manager")
	}
	pm.MultiStepForm("/wiz", patterns.MultiStepFormConfig{
		Steps:      []patterns.FormStep{{Template: "s1"}, {Template: "s2"}},
		SuccessURL: "/done",
		Complete:   func(*http.Request, url.Values) error { return nil },
	})
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/wiz?step=abc", nil))
	if rec.Code != http.StatusBadRequest || rec.Body.String() != "handled" || seen != http.StatusBadRequest {
		t.Fatalf("bad step = %d %q (OnError saw %d)", rec.Code, rec.Body.String(), seen)
	}
}
