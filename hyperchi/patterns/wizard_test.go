package patterns

import (
	"bytes"
	"encoding/json"
	"errors"
	"html/template"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/regiellis/hyperchi/hyperchi/validator"
)

// memStore is an in-memory WizardStore standing in for a session.
type memStore struct {
	data    map[string][]byte
	loadErr error
}

func newMemStore() *memStore { return &memStore{data: map[string][]byte{}} }

func (m *memStore) Load(key string) ([]byte, error) {
	if m.loadErr != nil {
		return nil, m.loadErr
	}
	return m.data[key], nil
}

func (m *memStore) Save(key string, data []byte) error {
	m.data[key] = bytes.Clone(data)
	return nil
}

func (m *memStore) Delete(key string) error {
	delete(m.data, key)
	return nil
}

// storeRouter is a host with an error pipeline and a wizard store. A nil
// store reports ErrNoWizardStore, like a route without session middleware.
type storeRouter struct {
	pipelineRouter
	store *memStore
}

func (s *storeRouter) WizardStore(*http.Request) (WizardStore, error) {
	if s.store == nil {
		return nil, ErrNoWizardStore
	}
	return s.store, nil
}

// stepTmpl prints the step, the values the wizard fields use, and any errors.
const stepTmpl = `STEP {{.step}}/{{.total_steps}} email={{.values.Get "email"}} role={{.values.Get "role"}} plan={{.values.Get "plan"}}` +
	`{{range $field, $msgs := .errors}} ERR {{$field}}={{index $msgs 0}}{{end}}`

// signup registers a three-step wizard: email (validated), role (validated),
// and plan. It returns the answers each Complete call received.
func signup(t *testing.T, r HTTPRouter, complete func(url.Values) error) *[]url.Values {
	t.Helper()
	pm := NewPatternManager(r, nil)
	for _, name := range []string{"s1", "s2", "s3"} {
		pm.RegisterTemplate(name, template.Must(template.New(name).Parse(stepTmpl)))
	}
	var calls []url.Values
	pm.MultiStepForm("/signup", MultiStepFormConfig{
		SuccessURL: "/welcome",
		Steps: []FormStep{
			{Name: "email", Template: "s1", Fields: []string{"email"}, Validate: func(v url.Values) map[string][]string {
				if !strings.Contains(v.Get("email"), "@") {
					return map[string][]string{"email": {"enter an email address"}}
				}
				return nil
			}},
			{Name: "role", Template: "s2", Validate: func(v url.Values) map[string][]string {
				if v.Get("role") == "" {
					return map[string][]string{"role": {"pick a role"}}
				}
				return nil
			}},
			{Name: "plan", Template: "s3"},
		},
		Complete: func(_ *http.Request, answers url.Values) error {
			calls = append(calls, answers)
			if complete != nil {
				return complete(answers)
			}
			return nil
		},
	})
	return &calls
}

// captureLog sends the standard logger to a buffer for the rest of the test.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev) })
	return &buf
}

func get(t *testing.T, h http.HandlerFunc, target string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	h(w, httptest.NewRequest(http.MethodGet, target, nil))
	return w
}

func htmxPost(t *testing.T, h http.HandlerFunc, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/signup", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	w := httptest.NewRecorder()
	h(w, req)
	return w
}

func wantBody(t *testing.T, w *httptest.ResponseRecorder, code int, parts ...string) {
	t.Helper()
	if w.Code != code {
		t.Fatalf("status = %d, want %d (body %q)", w.Code, code, w.Body.String())
	}
	for _, p := range parts {
		if !strings.Contains(w.Body.String(), p) {
			t.Fatalf("body = %q, want it to contain %q", w.Body.String(), p)
		}
	}
}

// A rejected step renders again at 422 with its per-field messages and the
// values just submitted, triggers showValidationError, and stores nothing.
func TestWizardRejectedStepRerendersWithErrorsAndValues(t *testing.T) {
	r := &storeRouter{store: newMemStore()}
	signup(t, r, nil)

	w := htmxPost(t, r.handler, url.Values{"step": {"1"}, "email": {"not-an-email"}})
	wantBody(t, w, http.StatusUnprocessableEntity, "STEP 1/3", "email=not-an-email", "ERR email=enter an email address")
	trigger := w.Header().Get("HX-Trigger")
	if !strings.Contains(trigger, "showValidationError") || !strings.Contains(trigger, "enter an email address") {
		t.Fatalf("HX-Trigger = %q, want showValidationError with the messages", trigger)
	}
	if len(r.store.data) != 0 {
		t.Fatalf("a rejected step stored %v", r.store.data)
	}
	if len(r.codes) != 0 {
		t.Fatalf("a rejected step went to the error pipeline: %v", r.codes)
	}
}

// Accepted answers are kept across steps, shown again when the user goes back,
// replaced when a step is submitted again, and handed to Complete in full.
// The stored answers are cleared after completion.
func TestWizardKeepsAnswersAcrossStepsAndBack(t *testing.T) {
	r := &storeRouter{store: newMemStore()}
	calls := signup(t, r, nil)

	w := htmxPost(t, r.handler, url.Values{"step": {"1"}, "email": {"a@example.com"}})
	wantBody(t, w, http.StatusOK, "STEP 2/3", "email=a@example.com")
	if got := w.Header().Get("HX-Push-Url"); got != "/signup?step=2" {
		t.Fatalf("HX-Push-Url = %q, want /signup?step=2", got)
	}

	w = htmxPost(t, r.handler, url.Values{"step": {"2"}, "role": {"engineer"}})
	wantBody(t, w, http.StatusOK, "STEP 3/3", "email=a@example.com", "role=engineer")

	// Back to step 1: its answer is still there.
	wantBody(t, get(t, r.handler, "/signup?step=1"), http.StatusOK, "STEP 1/3", "email=a@example.com", "role=engineer")

	// Change step 1; step 2 comes back with its stored answer.
	w = htmxPost(t, r.handler, url.Values{"step": {"1"}, "email": {"b@example.com"}})
	wantBody(t, w, http.StatusOK, "STEP 2/3", "email=b@example.com", "role=engineer")

	// A rejected resubmission of step 2 shows the submitted (empty) role, and
	// keeps the stored one for later.
	w = htmxPost(t, r.handler, url.Values{"step": {"2"}, "role": {""}})
	wantBody(t, w, http.StatusUnprocessableEntity, "role= ", "ERR role=pick a role")
	wantBody(t, get(t, r.handler, "/signup?step=3"), http.StatusOK, "role=engineer")

	w = htmxPost(t, r.handler, url.Values{"step": {"3"}, "plan": {"team"}, "csrf_token": {"tok"}})
	if w.Code != http.StatusOK || w.Header().Get("HX-Redirect") != "/welcome" {
		t.Fatalf("last step = %d HX-Redirect %q, want 200 and /welcome", w.Code, w.Header().Get("HX-Redirect"))
	}
	if !strings.Contains(w.Header().Get("HX-Trigger"), "showToast") {
		t.Fatalf("HX-Trigger = %q, want showToast", w.Header().Get("HX-Trigger"))
	}

	if len(*calls) != 1 {
		t.Fatalf("Complete ran %d times, want 1", len(*calls))
	}
	want := url.Values{"email": {"b@example.com"}, "role": {"engineer"}, "plan": {"team"}}
	if got := (*calls)[0]; got.Encode() != want.Encode() {
		t.Fatalf("Complete received %v, want %v", got, want)
	}
	if len(r.store.data) != 0 {
		t.Fatalf("answers not cleared after completion: %v", r.store.data)
	}
	// The wizard starts over.
	wantBody(t, get(t, r.handler, "/signup?step=2"), http.StatusSeeOther)
}

// A form posted without htmx completes with a 303 to SuccessURL.
func TestWizardCompletesWithSeeOtherWithoutHTMX(t *testing.T) {
	r := &storeRouter{store: newMemStore()}
	signup(t, r, nil)
	for _, form := range []url.Values{
		{"step": {"1"}, "email": {"a@example.com"}},
		{"step": {"2"}, "role": {"ops"}},
	} {
		if w := postForm(t, r.handler, "/signup", form); w.Code != http.StatusOK {
			t.Fatalf("step %s = %d", form.Get("step"), w.Code)
		}
	}
	w := postForm(t, r.handler, "/signup", url.Values{"step": {"3"}})
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/welcome" {
		t.Fatalf("last step = %d Location %q, want 303 /welcome", w.Code, w.Header().Get("Location"))
	}
}

// A step cannot be reached before the steps ahead of it have answers: a GET
// redirects to the first unanswered step and a POST renders it, so Complete
// never sees a partial set.
func TestWizardCannotSkipSteps(t *testing.T) {
	r := &storeRouter{store: newMemStore()}
	calls := signup(t, r, nil)

	w := get(t, r.handler, "/signup?step=3")
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/signup?step=1" {
		t.Fatalf("GET step 3 first = %d Location %q, want 303 to step 1", w.Code, w.Header().Get("Location"))
	}

	w = htmxPost(t, r.handler, url.Values{"step": {"3"}, "plan": {"team"}})
	wantBody(t, w, http.StatusOK, "STEP 1/3")
	if got := w.Header().Get("HX-Push-Url"); got != "/signup?step=1" {
		t.Fatalf("HX-Push-Url = %q, want /signup?step=1", got)
	}

	htmxPost(t, r.handler, url.Values{"step": {"1"}, "email": {"a@example.com"}})
	wantBody(t, htmxPost(t, r.handler, url.Values{"step": {"3"}}), http.StatusOK, "STEP 2/3")
	if len(*calls) != 0 {
		t.Fatalf("Complete ran with a skipped step: %v", *calls)
	}
}

// Fields limits a step to the fields it lists, and a later step cannot
// replace an earlier step's validated answer.
func TestWizardKeepsOnlyEachStepsFields(t *testing.T) {
	r := &storeRouter{store: newMemStore()}
	calls := signup(t, r, nil)

	htmxPost(t, r.handler, url.Values{"step": {"1"}, "email": {"a@example.com"}, "admin": {"true"}})
	htmxPost(t, r.handler, url.Values{"step": {"2"}, "role": {"ops"}, "email": {"evil"}})
	htmxPost(t, r.handler, url.Values{"step": {"3"}, "plan": {"free"}})

	if len(*calls) != 1 {
		t.Fatalf("Complete ran %d times, want 1", len(*calls))
	}
	got := (*calls)[0]
	if got.Has("admin") {
		t.Errorf("a field step 1 does not list reached Complete: %v", got)
	}
	if got.Get("email") != "a@example.com" {
		t.Errorf("email = %q, want step 1's validated answer", got.Get("email"))
	}
	if got.Has("step") || got.Has("csrf_token") {
		t.Errorf("reserved fields reached Complete: %v", got)
	}
}

// A failing Complete is a 500 through the pipeline that keeps every answer,
// so submitting the last step again completes.
func TestWizardFailedCompleteKeepsAnswers(t *testing.T) {
	r := &storeRouter{store: newMemStore()}
	fail := true
	calls := signup(t, r, func(url.Values) error {
		if fail {
			return errors.New("mail server down")
		}
		return nil
	})
	htmxPost(t, r.handler, url.Values{"step": {"1"}, "email": {"a@example.com"}})
	htmxPost(t, r.handler, url.Values{"step": {"2"}, "role": {"ops"}})

	if w := htmxPost(t, r.handler, url.Values{"step": {"3"}, "plan": {"team"}}); w.Code != http.StatusInternalServerError {
		t.Fatalf("failed Complete = %d, want 500", w.Code)
	}
	if len(r.store.data) == 0 {
		t.Fatal("answers were cleared after a failed Complete")
	}
	wantBody(t, get(t, r.handler, "/signup?step=3"), http.StatusOK, "plan=team")

	fail = false
	if w := htmxPost(t, r.handler, url.Values{"step": {"3"}, "plan": {"team"}}); w.Header().Get("HX-Redirect") != "/welcome" {
		t.Fatalf("retry = %d, want the redirect", w.Code)
	}
	if len(*calls) != 2 || (*calls)[1].Get("email") != "a@example.com" {
		t.Fatalf("Complete calls = %v", *calls)
	}
	if len(r.store.data) != 0 {
		t.Fatal("answers not cleared after the retry completed")
	}
}

// Without a wizard store, every request is a 500 whose cause names the
// missing sessions: through the pipeline when the host has one, and logged
// otherwise.
func TestWizardWithoutStoreIsClear500(t *testing.T) {
	t.Run("host has no store", func(t *testing.T) {
		r := &storeRouter{}
		signup(t, r, nil)
		for _, w := range []*httptest.ResponseRecorder{
			get(t, r.handler, "/signup"),
			htmxPost(t, r.handler, url.Values{"step": {"1"}, "email": {"a@example.com"}}),
		} {
			if w.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500", w.Code)
			}
		}
		for _, err := range r.errs {
			if !errors.Is(err, ErrNoWizardStore) || !strings.Contains(err.Error(), "SessionMiddleware") {
				t.Fatalf("pipeline error = %v, want ErrNoWizardStore naming the session middleware", err)
			}
		}
	})

	t.Run("host without the capability or a pipeline", func(t *testing.T) {
		logs := captureLog(t)

		r := &stubRouter{}
		signup(t, r, nil)
		if w := get(t, r.handler, "/signup"); w.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", w.Code)
		}
		if !strings.Contains(logs.String(), "UseSessions") {
			t.Fatalf("log = %q, want it to say sessions are required", logs.String())
		}
	})
}

// A store that cannot be read is a 500; saved answers that do not decode are
// dropped and the wizard starts over.
func TestWizardStoreFailures(t *testing.T) {
	r := &storeRouter{store: newMemStore()}
	signup(t, r, nil)

	r.store.loadErr = errors.New("disk on fire")
	if w := get(t, r.handler, "/signup"); w.Code != http.StatusInternalServerError {
		t.Fatalf("load error = %d, want 500", w.Code)
	}

	r.store.loadErr = nil
	r.store.data["hyperchi.wizard:/signup"] = []byte("{not json")
	captureLog(t)
	wantBody(t, get(t, r.handler, "/signup"), http.StatusOK, "STEP 1/3")

	// Answers saved for a different number of steps also start over.
	stale, err := json.Marshal(wizardState{Answers: []url.Values{{"email": {"x@y.z"}}}})
	if err != nil {
		t.Fatal(err)
	}
	r.store.data["hyperchi.wizard:/signup"] = stale
	wantBody(t, get(t, r.handler, "/signup?step=1"), http.StatusOK, "email= ")
}

// SchemaStep reports a validator.Schema's failures per field.
func TestSchemaStep(t *testing.T) {
	check := SchemaStep(validator.NewSchema().
		Field("email", validator.String().Required().Email()).
		Field("name", validator.String().Required()))

	got := check(url.Values{"email": {"nope"}, "name": {""}})
	if len(got["email"]) != 1 || len(got["name"]) != 1 {
		t.Fatalf("SchemaStep = %v, want one message for email and one for name", got)
	}
	if got := check(url.Values{"email": {"a@example.com"}, "name": {"Ada"}}); len(got) != 0 {
		t.Fatalf("SchemaStep on valid input = %v, want none", got)
	}
}

// A wizard with no steps, or no Complete to receive the answers, is a setup
// mistake caught at registration.
func TestMultiStepFormPanicsOnBadConfig(t *testing.T) {
	for name, cfg := range map[string]MultiStepFormConfig{
		"no steps":    {Complete: func(*http.Request, url.Values) error { return nil }},
		"no Complete": {Steps: []FormStep{{Template: "s1"}}},
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("MultiStepForm did not panic")
				}
			}()
			NewPatternManager(&stubRouter{}, nil).MultiStepForm("/wiz", cfg)
		})
	}
}
