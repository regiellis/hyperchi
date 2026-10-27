package patterns

import (
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"testing"
)

// pipelineRouter stands in for a host app with an error pipeline: it records
// the failures the patterns hand to it.
type pipelineRouter struct {
	stubRouter
	codes []int
	errs  []error
}

func (p *pipelineRouter) RespondStatus(w http.ResponseWriter, _ *http.Request, code int, err error) {
	p.codes = append(p.codes, code)
	p.errs = append(p.errs, err)
	w.WriteHeader(code)
}

// newWizard registers a two-step wizard on r with step templates that print
// their number.
func newWizard(t *testing.T, r HTTPRouter) {
	t.Helper()
	pm := NewPatternManager(r, nil)
	pm.templates["s1"] = template.Must(template.New("s1").Parse("STEP {{.step}}"))
	pm.templates["s2"] = template.Must(template.New("s2").Parse("STEP {{.step}}"))
	pm.MultiStepForm("/wiz", MultiStepFormConfig{
		Steps:      []FormStep{{Template: "s1"}, {Template: "s2"}},
		SuccessURL: "/done",
		Complete:   func(*http.Request, url.Values) error { return nil },
	})
}

// A step that is not a number, or is out of range, is a 400 answered through
// the host's error pipeline, for GET and POST alike. A GET without a step
// shows step 1.
func TestMultiStepFormBadStepIs400ThroughPipeline(t *testing.T) {
	r := &storeRouter{store: newMemStore()}
	newWizard(t, r)

	for _, target := range []string{"/wiz?step=abc", "/wiz?step=0", "/wiz?step=3", "/wiz?step=-1"} {
		w := httptest.NewRecorder()
		r.handler(w, httptest.NewRequest(http.MethodGet, target, nil))
		if w.Code != http.StatusBadRequest {
			t.Errorf("GET %s = %d, want 400", target, w.Code)
		}
	}
	for _, step := range []string{"abc", "", "3"} {
		if w := postForm(t, r.handler, "/wiz", url.Values{"step": {step}}); w.Code != http.StatusBadRequest {
			t.Errorf("POST step=%q = %d, want 400", step, w.Code)
		}
	}
	if want := slices.Repeat([]int{http.StatusBadRequest}, 7); !slices.Equal(r.codes, want) {
		t.Fatalf("pipeline saw %v, want %v", r.codes, want)
	}

	w := httptest.NewRecorder()
	r.handler(w, httptest.NewRequest(http.MethodGet, "/wiz", nil))
	if w.Code != http.StatusOK || w.Body.String() != "STEP 1" {
		t.Fatalf("GET /wiz = %d %q, want step 1", w.Code, w.Body.String())
	}
}

// Without an error pipeline the patterns still answer 400.
func TestMultiStepFormBadStepWithoutPipeline(t *testing.T) {
	r := &stubRouter{}
	newWizard(t, r)
	w := httptest.NewRecorder()
	r.handler(w, httptest.NewRequest(http.MethodGet, "/wiz?step=abc", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("GET ?step=abc = %d, want 400", w.Code)
	}
}

// Every configuration field is read by the patterns: a field that silently
// does nothing misleads its caller.
func TestConfigFieldsAreAllRead(t *testing.T) {
	cases := []struct {
		typ  any
		want []string
	}{
		{PatternConfig{}, []string{"DefaultPollInterval", "InfiniteScrollPageSize"}},
		{MultiStepFormConfig{}, []string{"Steps", "SuccessURL", "Complete"}},
		{FormStep{}, []string{"Name", "Title", "Template", "Fields", "Validate"}},
		{PollingConfig{}, []string{"DataFunc", "Template", "MaxDuration"}},
	}
	for _, tc := range cases {
		typ := reflect.TypeOf(tc.typ)
		var got []string
		for f := range typ.Fields() {
			got = append(got, f.Name)
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s fields = %v, want only the fields the patterns read: %v", typ.Name(), got, tc.want)
		}
	}
}
