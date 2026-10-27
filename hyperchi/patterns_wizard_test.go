package hyperchi

import (
	"errors"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/regiellis/hyperchi/hyperchi/patterns"
)

// wizardClient drives an app like a browser: it keeps the session cookie
// between requests.
type wizardClient struct {
	t       *testing.T
	app     *HyperChi
	cookies map[string]*http.Cookie
}

func (c *wizardClient) do(req *http.Request) *httptest.ResponseRecorder {
	c.t.Helper()
	for _, cookie := range c.cookies {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	c.app.ServeHTTP(rec, req)
	for _, cookie := range rec.Result().Cookies() {
		c.cookies[cookie.Name] = cookie
	}
	return rec
}

func (c *wizardClient) post(form url.Values) *httptest.ResponseRecorder {
	c.t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/join", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	return c.do(req)
}

func (c *wizardClient) get(target string) *httptest.ResponseRecorder {
	c.t.Helper()
	return c.do(httptest.NewRequest(http.MethodGet, target, nil))
}

// joinWizard registers a two-step wizard on app and returns the answers each
// Complete call received.
func joinWizard(t *testing.T, app *HyperChi) *[]url.Values {
	t.Helper()
	pm := app.Extensions().GetPatternManager()
	const tmpl = `STEP {{.step}} email={{.values.Get "email"}} role={{.values.Get "role"}}{{range $f, $m := .errors}} ERR {{$f}}{{end}}`
	pm.RegisterTemplate("join-1", template.Must(template.New("join-1").Parse(tmpl)))
	pm.RegisterTemplate("join-2", template.Must(template.New("join-2").Parse(tmpl)))
	var calls []url.Values
	pm.MultiStepForm("/join", patterns.MultiStepFormConfig{
		SuccessURL: "/joined",
		Steps: []patterns.FormStep{
			{Template: "join-1", Fields: []string{"email"}, Validate: func(v url.Values) map[string][]string {
				if !strings.Contains(v.Get("email"), "@") {
					return map[string][]string{"email": {"enter an email address"}}
				}
				return nil
			}},
			{Template: "join-2", Fields: []string{"role"}},
		},
		Complete: func(_ *http.Request, answers url.Values) error {
			calls = append(calls, answers)
			return nil
		},
	})
	return &calls
}

// The app's pattern host keeps wizard answers in the KV session: they survive
// between requests through the session cookie, reach Complete, and are gone
// from the session afterwards.
func TestMultiStepFormKeepsAnswersInKVSession(t *testing.T) {
	app := New()
	if err := app.UseKVStoreMemory("memory"); err != nil {
		t.Fatal(err)
	}
	if err := app.UseSessions(KVSessionConfig{Backend: "memory", InsecureCookies: true}); err != nil {
		t.Fatal(err)
	}
	app.Use(app.SessionMiddleware())
	calls := joinWizard(t, app)
	c := &wizardClient{t: t, app: app, cookies: map[string]*http.Cookie{}}

	if rec := c.post(url.Values{"step": {"1"}, "email": {"nope"}}); rec.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(rec.Body.String(), "ERR email") {
		t.Fatalf("invalid step 1 = %d %q, want 422 with the email error", rec.Code, rec.Body.String())
	}
	if rec := c.post(url.Values{"step": {"1"}, "email": {"a@example.com"}}); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), "STEP 2 email=a@example.com") {
		t.Fatalf("step 1 = %d %q, want step 2 with the stored email", rec.Code, rec.Body.String())
	}
	if rec := c.get("/join?step=1"); !strings.Contains(rec.Body.String(), "STEP 1 email=a@example.com") {
		t.Fatalf("back to step 1 = %d %q, want the stored email", rec.Code, rec.Body.String())
	}

	rec := c.post(url.Values{"step": {"2"}, "role": {"ops"}})
	if rec.Header().Get("HX-Redirect") != "/joined" {
		t.Fatalf("last step = %d HX-Redirect %q, want /joined", rec.Code, rec.Header().Get("HX-Redirect"))
	}
	want := url.Values{"email": {"a@example.com"}, "role": {"ops"}}
	if len(*calls) != 1 || (*calls)[0].Encode() != want.Encode() {
		t.Fatalf("Complete received %v, want %v", *calls, want)
	}

	// The completion response saved the session without the answers.
	sessions, err := app.Sessions().GetAllSessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(sessions))
	}
	if sessions[0].Has("hyperchi.wizard:/join") {
		t.Fatalf("answers left in the session after completion: %v", sessions[0].Data)
	}
	if rec := c.get("/join?step=2"); rec.Code != http.StatusSeeOther {
		t.Fatalf("GET step 2 after completion = %d, want 303 back to step 1", rec.Code)
	}
}

// A wizard on a route without SessionMiddleware is a 500 through the app's
// error pipeline, with a cause that says sessions are required.
func TestMultiStepFormWithoutSessionsIs500(t *testing.T) {
	app := New()
	var seen error
	app.OnError(func(c *Context, err error) {
		seen = err
		if werr := c.Status(http.StatusInternalServerError).Text("no sessions"); werr != nil {
			t.Error(werr)
		}
	})
	joinWizard(t, app)

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/join", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	var he *HTTPError
	if !errors.As(seen, &he) || he.Code != http.StatusInternalServerError || !errors.Is(seen, patterns.ErrNoWizardStore) {
		t.Fatalf("OnError saw %v, want a 500 wrapping patterns.ErrNoWizardStore", seen)
	}
}
