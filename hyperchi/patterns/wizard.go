package patterns

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strconv"

	"github.com/regiellis/hyperchi/hyperchi/response"
	"github.com/regiellis/hyperchi/hyperchi/validator"
)

// ErrNoWizardStore reports a request with nowhere to keep a multi-step form's
// answers. HyperChi returns it when the wizard's route does not run behind
// session middleware.
var ErrNoWizardStore = errors.New("multi-step forms keep answers in the user's session: call app.UseSessions and install app.SessionMiddleware() before the wizard's route")

// WizardStore keeps a multi-step form's answers between requests. The host
// app hands one out per request (see WizardStoreHost); HyperChi's writes to
// the request's KV session.
type WizardStore interface {
	// Load returns the data saved under key, or nil when nothing is saved.
	Load(key string) ([]byte, error)
	// Save replaces the data saved under key.
	Save(key string, data []byte) error
	// Delete removes key. Deleting a missing key is not an error.
	Delete(key string) error
}

// WizardStoreHost is the host capability MultiStepForm needs: the store for
// one request's answers. It returns an error wrapping ErrNoWizardStore when
// the request has none. HyperChi's pattern host implements it on top of KV
// sessions; a custom host (a bare chi router) implements it to use
// MultiStepForm.
type WizardStoreHost interface {
	WizardStore(r *http.Request) (WizardStore, error)
}

// StepValidator checks one step's submitted fields and returns the messages
// for each field that failed, keyed by field name. An empty result accepts
// the step. The shape matches hyperchi.FieldErrors, and SchemaStep adapts a
// validator.Schema.
type StepValidator func(values url.Values) map[string][]string

// SchemaStep returns a StepValidator that checks a step against schema. A
// field submitted once is validated as a string, a repeated field as a
// []string. Fields the schema does not name are not checked.
func SchemaStep(schema *validator.Schema) StepValidator {
	return func(values url.Values) map[string][]string {
		data := make(map[string]any, len(values))
		for key, vals := range values {
			if len(vals) == 1 {
				data[key] = vals[0]
			} else {
				data[key] = slices.Clone(vals)
			}
		}
		result := schema.Validate(data)
		if result.Valid {
			return nil
		}
		fields := make(map[string][]string, len(result.Errors))
		for _, e := range result.Errors {
			fields[e.Field] = append(fields[e.Field], e.Message)
		}
		return fields
	}
}

// MultiStepFormConfig configures MultiStepForm. Steps must not be empty and
// Complete is required.
type MultiStepFormConfig struct {
	Steps      []FormStep `json:"steps"`
	SuccessURL string     `json:"success_url"`

	// Complete receives every accepted answer once the last step passes,
	// before the redirect to SuccessURL. Answers from earlier steps win when
	// two steps submit the same field. An error is answered with a 500 and the
	// answers are kept, so the user can submit the last step again.
	Complete func(r *http.Request, answers url.Values) error `json:"-"`
}

// FormStep is one step of a MultiStepForm. Template renders the step; the
// whole FormStep reaches it as "step_info", so Name and Title are for the
// template to show.
type FormStep struct {
	Name     string `json:"name"`
	Title    string `json:"title"`
	Template string `json:"template"`

	// Fields lists the form fields this step keeps. Other submitted fields are
	// dropped before Validate runs. When empty, the step keeps every submitted
	// field except "step" and "csrf_token". Listing them keeps a crafted POST
	// from adding fields the step's validator never checked.
	Fields []string `json:"fields"`

	// Validate checks the step's fields. Nil accepts any submission.
	Validate StepValidator `json:"-"`
}

// reservedFields are the form fields the wizard itself reads, never kept as
// answers.
var reservedFields = []string{"step", "csrf_token"}

// MultiStepForm serves a wizard at pattern that keeps the user's answers in
// their session between steps. The route must run behind session middleware
// (in HyperChi, app.UseSessions and app.SessionMiddleware()); without it each
// request is a 500 through the error pipeline whose cause is ErrNoWizardStore.
//
// A GET renders step ?step=N (default 1). A POST carries the current step in
// the "step" form field. A step that is not a number from 1 to len(Steps) is a
// 400. Every step's template gets:
//
//   - "step", "total_steps", "step_info" (the FormStep), and "progress";
//   - "values": the answers so far as url.Values ({{.values.Get "email"}}),
//     with this step's own answers, or the values just submitted, taking
//     precedence;
//   - "errors": per-field messages, map[string][]string, empty unless the
//     step was rejected ({{range index .errors "email"}}).
//
// A submitted step is checked by its Validate. A rejected step renders again
// with status 422, its messages and submitted values, and a
// showValidationError trigger carrying the messages; nothing is stored. An
// accepted step is stored and the response is the next step, with its URL
// pushed and a nextStep trigger. Going back (a GET of an earlier step) shows
// the stored answers, and submitting it again replaces them.
//
// A step can only be reached once every step before it has answers: a GET of
// a later step redirects (303) to the first unanswered one, and a POST of a
// later step renders that step instead. When the last step passes, Complete
// receives all the answers, the stored answers are cleared, and the response
// redirects to SuccessURL: HX-Redirect with a showToast trigger for htmx, a
// 303 otherwise.
//
// MultiStepForm panics when Steps is empty or Complete is nil.
func (pm *PatternManager) MultiStepForm(pattern string, config MultiStepFormConfig) {
	if len(config.Steps) == 0 {
		panic(fmt.Sprintf("patterns: MultiStepForm %s: no steps", pattern))
	}
	if config.Complete == nil {
		panic(fmt.Sprintf("patterns: MultiStepForm %s: Complete is required, or the answers would be thrown away", pattern))
	}
	wz := &wizard{
		pm:      pm,
		pattern: pattern,
		config:  config,
		key:     "hyperchi.wizard:" + pattern,
	}
	pm.app.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			wz.post(w, r)
			return
		}
		wz.get(w, r)
	})
}

// wizard serves one MultiStepForm.
type wizard struct {
	pm      *PatternManager
	pattern string
	config  MultiStepFormConfig
	key     string // the store key for this wizard's answers
}

// wizardState is what a wizard keeps in its store: each step's accepted
// answers, nil for a step not answered yet.
type wizardState struct {
	Answers []url.Values `json:"answers"`
}

// firstUnanswered returns the 1-based number of the first step without
// answers, or len(Answers)+1 when every step has them.
func (s *wizardState) firstUnanswered() int {
	for i, a := range s.Answers {
		if a == nil {
			return i + 1
		}
	}
	return len(s.Answers) + 1
}

// merged returns every step's answers in one set. A field answered by two
// steps keeps the earlier step's values. Step skip (1-based) is left out,
// so a step's own values can be laid over the rest; 0 skips nothing.
func (s *wizardState) merged(skip int) url.Values {
	out := url.Values{}
	for i, answers := range s.Answers {
		if i+1 == skip {
			continue
		}
		for key, vals := range answers {
			if _, taken := out[key]; !taken {
				out[key] = slices.Clone(vals)
			}
		}
	}
	return out
}

// view returns the values a step's template shows: the other steps' answers
// with own laid over them.
func (s *wizardState) view(step int, own url.Values) url.Values {
	values := s.merged(step)
	for key, vals := range own {
		values[key] = slices.Clone(vals)
	}
	return values
}

// get renders the requested step with the answers stored so far.
func (wz *wizard) get(w http.ResponseWriter, r *http.Request) {
	step := 1
	if stepParam := r.URL.Query().Get("step"); stepParam != "" {
		var err error
		if step, err = parseStep(stepParam, len(wz.config.Steps)); err != nil {
			wz.pm.fail(w, r, http.StatusBadRequest, "Invalid step", err)
			return
		}
	}

	_, state, ok := wz.load(w, r)
	if !ok {
		return
	}
	if first := state.firstUnanswered(); step > first {
		http.Redirect(w, r, wz.stepURL(first), http.StatusSeeOther)
		return
	}

	wz.render(w, r, response.New(w), step, state.view(step, state.Answers[step-1]), nil)
}

// post validates and stores one step, then answers with the next step or
// completes the wizard.
func (wz *wizard) post(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		wz.pm.fail(w, r, http.StatusBadRequest, "Invalid form data", err)
		return
	}
	step, err := parseStep(r.FormValue("step"), len(wz.config.Steps))
	if err != nil {
		wz.pm.fail(w, r, http.StatusBadRequest, "Invalid step", err)
		return
	}

	store, state, ok := wz.load(w, r)
	if !ok {
		return
	}

	// An earlier step has no answers: it was skipped, or the session expired
	// and took them. Show that step instead of accepting this one.
	if first := state.firstUnanswered(); step > first {
		wz.render(w, r, response.New(w).PushURL(wz.stepURL(first)), first, state.view(first, nil), nil)
		return
	}

	formStep := wz.config.Steps[step-1]
	answers := stepAnswers(r.PostForm, formStep.Fields)
	if formStep.Validate != nil {
		if fieldErrors := formStep.Validate(answers); len(fieldErrors) > 0 {
			// 422 tells clients and logs the step was rejected. htmx 4 still
			// swaps the re-rendered step in place and fires the trigger.
			res := response.New(w).
				Status(http.StatusUnprocessableEntity).
				Trigger(map[string]any{"showValidationError": fieldErrors})
			wz.render(w, r, res, step, state.view(step, answers), fieldErrors)
			return
		}
	}
	state.Answers[step-1] = answers

	if step < len(wz.config.Steps) {
		if err := wz.save(store, state); err != nil {
			wz.pm.fail(w, r, http.StatusInternalServerError, "Saving form answers failed", err)
			return
		}
		// The POST response is the next step itself, so an hx-target on the
		// wizard container swaps it in with no client listener. PushURL keeps
		// the address bar and history in step.
		next := step + 1
		res := response.New(w).PushURL(wz.stepURL(next)).Trigger("nextStep")
		wz.render(w, r, res, next, state.view(next, state.Answers[next-1]), nil)
		return
	}

	if err := wz.config.Complete(r, state.merged(0)); err != nil {
		// Keep every answer, the last step's included, for a second attempt.
		if serr := wz.save(store, state); serr != nil {
			log.Printf("patterns: MultiStepForm %s: saving answers after a failed Complete: %v", wz.pattern, serr)
		}
		wz.pm.fail(w, r, http.StatusInternalServerError, "Completing the form failed", err)
		return
	}
	// Complete has run, so the redirect still happens: failing here would
	// invite the user to submit the same answers twice.
	if err := store.Delete(wz.key); err != nil {
		log.Printf("patterns: MultiStepForm %s: clearing answers after completion: %v", wz.pattern, err)
	}

	if r.Header.Get("HX-Request") != "true" {
		http.Redirect(w, r, wz.config.SuccessURL, http.StatusSeeOther)
		return
	}
	response.New(w).
		Redirect(wz.config.SuccessURL).
		Trigger(map[string]any{
			"showToast": map[string]any{
				"level":   "success",
				"message": "Form completed successfully!",
			},
		})
	// An explicit header, so middleware that acts on the first write (session
	// saving) sees this response.
	w.WriteHeader(http.StatusOK)
}

// load returns the request's store and this wizard's saved state. It answers
// the request itself and returns false when there is no store or it cannot
// be read. Saved state that does not decode, or was saved for a different
// number of steps, is dropped and the wizard starts over.
func (wz *wizard) load(w http.ResponseWriter, r *http.Request) (WizardStore, *wizardState, bool) {
	var store WizardStore
	err := ErrNoWizardStore
	if host, ok := wz.pm.app.(WizardStoreHost); ok {
		store, err = host.WizardStore(r)
		if err == nil && store == nil {
			err = ErrNoWizardStore
		}
	}
	if err != nil {
		wz.pm.fail(w, r, http.StatusInternalServerError, "Multi-step form has no session", err)
		return nil, nil, false
	}

	data, err := store.Load(wz.key)
	if err != nil {
		wz.pm.fail(w, r, http.StatusInternalServerError, "Loading form answers failed", err)
		return nil, nil, false
	}

	state := &wizardState{Answers: make([]url.Values, len(wz.config.Steps))}
	if len(data) == 0 {
		return store, state, true
	}
	var saved wizardState
	if err := json.Unmarshal(data, &saved); err != nil {
		log.Printf("patterns: MultiStepForm %s: dropping unreadable saved answers: %v", wz.pattern, err)
		return store, state, true
	}
	if len(saved.Answers) != len(wz.config.Steps) {
		return store, state, true
	}
	return store, &saved, true
}

// save writes state to store.
func (wz *wizard) save(store WizardStore, state *wizardState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("encoding answers: %w", err)
	}
	return store.Save(wz.key, data)
}

// render writes step through res, which carries any status and HX headers
// already chosen.
func (wz *wizard) render(w http.ResponseWriter, r *http.Request, res *response.Response, step int, values url.Values, fieldErrors map[string][]string) {
	html, err := wz.pm.renderTemplate(wz.config.Steps[step-1].Template, stepData(wz.config, step, values, fieldErrors))
	if err != nil {
		wz.pm.fail(w, r, http.StatusInternalServerError, "Template error", err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if _, err := res.WriteString(html); err != nil {
		log.Printf("patterns: MultiStepForm %s: writing step %d: %v", wz.pattern, step, err)
	}
}

// stepURL is the address of step on this wizard.
func (wz *wizard) stepURL(step int) string {
	return wz.pattern + "?step=" + strconv.Itoa(step)
}

// stepAnswers picks the step's answers out of a submitted form: the listed
// fields, or every field but the reserved ones when fields is empty. The
// result is never nil, so a step with no fields still counts as answered.
func stepAnswers(form url.Values, fields []string) url.Values {
	out := url.Values{}
	if len(fields) > 0 {
		for _, field := range fields {
			if vals, ok := form[field]; ok {
				out[field] = slices.Clone(vals)
			}
		}
		return out
	}
	for key, vals := range form {
		if !slices.Contains(reservedFields, key) {
			out[key] = slices.Clone(vals)
		}
	}
	return out
}

// parseStep reads a 1-based step number and checks it against the number of
// steps.
func parseStep(value string, steps int) (int, error) {
	step, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("invalid step %q: %w", value, err)
	}
	if step < 1 || step > steps {
		return 0, fmt.Errorf("step %d is outside 1 to %d", step, steps)
	}
	return step, nil
}

// stepData builds the render context for one wizard step.
func stepData(config MultiStepFormConfig, step int, values url.Values, fieldErrors map[string][]string) map[string]any {
	errs := map[string][]string{}
	maps.Copy(errs, fieldErrors)
	return map[string]any{
		"step":        step,
		"total_steps": len(config.Steps),
		"step_info":   config.Steps[step-1],
		"progress":    float64(step-1) / float64(len(config.Steps)) * 100,
		"values":      values,
		"errors":      errs,
	}
}
