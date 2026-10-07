package validator

import (
	"context"
	"errors"
	"html/template"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// NaN and infinities are not numbers a form can mean. NaN would pass every Min
// and Max check, because all its comparisons are false.
func TestNumberRejectsNonFiniteValues(t *testing.T) {
	v := Number().Min(0).Max(10)
	for _, in := range []any{"NaN", "nan", "Inf", "-Inf", "+Infinity", math.NaN(), math.Inf(1), float32(math.Inf(-1))} {
		if err := v.Validate(in); err == nil {
			t.Errorf("Validate(%v) accepted a non-finite number", in)
		}
	}
	if err := Number().Validate("NaN"); err == nil {
		t.Error("NaN accepted with no bounds at all")
	}
	if err := v.Validate("5"); err != nil {
		t.Errorf("Validate(5) = %v", err)
	}
}

// MultipleOf works for steps below 1e-6 and values above the int range, where
// scaling to an integer would divide by zero or overflow.
func TestNumberMultipleOf(t *testing.T) {
	tests := []struct {
		step  float64
		value any
		valid bool
	}{
		{5, 15, true},
		{5, 16, false},
		{5, -10, true},
		{0.1, 0.3, true},
		{0.1, 0.7, true},
		{0.1, 0.35, false},
		{0.01, "19.99", true},
		{0.25, 1.75, true},
		{1e-7, 3e-7, true}, // a scaled integer remainder divides by zero here
		{1e-7, 3.5e-7, false},
		{3, 9999999999999.0, true}, // 3 * 3333333333333, past the old overflow point
		{3, 1e13, false},           // 1e13 mod 3 is 1
		{7, 7e15, true},
		{-5, 15, true},
		{0.5, 0, true},
	}
	for _, tc := range tests {
		err := Number().MultipleOf(tc.step).Validate(tc.value)
		if (err == nil) != tc.valid {
			t.Errorf("MultipleOf(%g).Validate(%v) = %v, want valid=%t", tc.step, tc.value, err, tc.valid)
		}
	}

	for _, step := range []float64{0, math.NaN(), math.Inf(1)} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("MultipleOf(%g) did not panic", step)
				}
			}()
			Number().MultipleOf(step)
		}()
	}
}

// The integer check holds for values too large to convert to int.
func TestNumberIntegerLargeValues(t *testing.T) {
	if err := Int().Validate(1e20); err != nil {
		t.Errorf("1e20 is an integer: %v", err)
	}
	if err := Int().Validate(1e20 + 0.5); err != nil {
		t.Errorf("1e20+0.5 rounds to an integer in float64: %v", err)
	}
	if err := Int().Validate(2.5); err == nil {
		t.Error("2.5 accepted as an integer")
	}
}

// Length rules count characters (runes), not UTF-8 bytes.
func TestStringLengthCountsRunes(t *testing.T) {
	tests := []struct {
		v     *StringValidator
		in    string
		valid bool
	}{
		{String().MaxLength(4), "café", true}, // 5 bytes
		{String().MaxLength(3), "日本語", true},  // 9 bytes
		{String().MaxLength(2), "日本語", false},
		{String().MinLength(4), "日本語", false},
		{String().MinLength(3), "日本語", true},
		{String().Length(5), "héllo", true},
		{String().Length(5), "hello!", false},
		{String().MaxLength(1), "👍", true},
	}
	for _, tc := range tests {
		if err := tc.v.Validate(tc.in); (err == nil) != tc.valid {
			t.Errorf("Validate(%q) = %v, want valid=%t", tc.in, err, tc.valid)
		}
	}
}

// ParseTime accepts only unambiguous ISO 8601 layouts. Guessing a slash date
// such as "05/03/2026" would fix one reading, and the other could never be
// entered for any day up to 12.
func TestParseTimeRejectsAmbiguousSlashDates(t *testing.T) {
	for _, in := range []string{"05/03/2026", "13/03/2026", "03/13/2026", "2026/03/05", "March 5 2026"} {
		if got, err := ParseTime(in); err == nil {
			t.Errorf("ParseTime(%q) = %v, want an error", in, got)
		}
	}
	tests := map[string]time.Time{
		"2026-03-05":                    time.Date(2026, 3, 5, 0, 0, 0, 0, time.UTC),
		"2026-03-05 14:30:00":           time.Date(2026, 3, 5, 14, 30, 0, 0, time.UTC),
		"2026-03-05T14:30:00":           time.Date(2026, 3, 5, 14, 30, 0, 0, time.UTC),
		"2026-03-05T14:30":              time.Date(2026, 3, 5, 14, 30, 0, 0, time.UTC),
		"2026-03-05T14:30:00Z":          time.Date(2026, 3, 5, 14, 30, 0, 0, time.UTC),
		"2026-03-05T14:30:00.123+02:00": time.Date(2026, 3, 5, 12, 30, 0, 123e6, time.UTC),
	}
	for in, want := range tests {
		got, err := ParseTime(in)
		if err != nil || !got.Equal(want) {
			t.Errorf("ParseTime(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	now := time.Now()
	if got, err := ParseTime(now); err != nil || !got.Equal(now) {
		t.Error("ParseTime(time.Time)")
	}
	if _, err := ParseTime(42); err == nil {
		t.Error("ParseTime(int) accepted")
	}

	// Slash formats are opt-in through an explicit layout.
	us := Date().Format("01/02/2006")
	if got := us.Transform("05/03/2026"); got != time.Date(2026, 5, 3, 0, 0, 0, 0, time.UTC) {
		t.Errorf("US layout = %v", got)
	}
	eu := Date().Format("02/01/2006")
	if got := eu.Transform("05/03/2026"); got != time.Date(2026, 3, 5, 0, 0, 0, 0, time.UTC) {
		t.Errorf("day-first layout = %v", got)
	}
	if err := eu.Validate("13/03/2026"); err != nil {
		t.Errorf("day-first 13/03 = %v", err)
	}
	if err := Date().Validate("05/03/2026"); err == nil {
		t.Error("Date() without a layout accepted a slash date")
	}
}

// The middleware validates only requests that carry a form. GET, HEAD, and
// OPTIONS (a CORS preflight) pass through instead of getting a 422.
func TestFormValidationMiddlewareSkipsBodylessMethods(t *testing.T) {
	schema := NewSchema().Field("name", String().Required())
	reached := 0
	h := FormValidationMiddleware(schema)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached++
		w.WriteHeader(http.StatusNoContent)
	}))
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, "/", nil))
		if rec.Code != http.StatusNoContent {
			t.Errorf("%s = %d, want the handler's 204", method, rec.Code)
		}
	}
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, "/", nil))
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s with no name = %d, want 422", method, rec.Code)
		}
	}
	if reached != 3 {
		t.Errorf("handler reached %d times, want 3", reached)
	}
}

// The middleware stores the result in the context and answers htmx requests
// with an HTML fragment.
func TestFormValidationMiddlewareResponses(t *testing.T) {
	schema := NewSchema().Field("name", String().MinLength(2).Required())
	var seen map[string]any
	h := FormValidationMiddleware(schema)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if HasValidationErrors(r.Context()) || len(GetValidationErrors(r.Context())) != 0 {
			t.Error("a valid request carries errors")
		}
		seen = GetValidatedData(r.Context())
	}))

	post := func(body string, htmx bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if htmx {
			req.Header.Set("HX-Request", "true")
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	if rec := post("name=Ada", false); rec.Code != http.StatusOK || seen["name"] != "Ada" {
		t.Errorf("valid post: %d %v", rec.Code, seen)
	}
	rec := post("name=%3C", true)
	if rec.Code != http.StatusUnprocessableEntity || rec.Header().Get("Content-Type") != "text/html" {
		t.Fatalf("htmx failure: %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if !strings.Contains(rec.Body.String(), `class="validation-errors"`) || strings.Contains(rec.Body.String(), "<<") {
		t.Errorf("htmx body: %s", rec.Body.String())
	}
	rec = post("name=x", false)
	if rec.Header().Get("Content-Type") != "application/json" || !strings.Contains(rec.Body.String(), `"valid":false`) {
		t.Errorf("json failure: %s", rec.Body.String())
	}

	// A response the client never reads is logged, not ignored or fatal.
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("name=x"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.ServeHTTP(failingWriter{httptest.NewRecorder()}, req)
	req = httptest.NewRequest(http.MethodPost, "/", strings.NewReader("name=x"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	h.ServeHTTP(failingWriter{httptest.NewRecorder()}, req)
}

type failingWriter struct{ *httptest.ResponseRecorder }

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("client gone") }

// Every CommonTemplateSnippets entry parses with GetTemplateFunctions alone
// and executes, with and without errors, so no snippet may call a function
// that FuncMap does not register.
func TestCommonTemplateSnippetsExecute(t *testing.T) {
	funcs := NewTemplateHelpers().GetTemplateFunctions()
	errs := []ValidationError{
		{Field: "email", Message: "is required"},
		{Field: "email", Message: "must be an email"},
		{Field: "name", Message: "too <short>"},
	}
	full := map[string]any{
		"errors": errs, "field": "email", "name": "email", "label": "Email",
		"type": "email", "value": "a@b", "required": true, "placeholder": "you@example.com",
		"rows": 5, "checked": true,
		"options": []map[string]any{{"value": "a@b", "label": "A"}, {"value": "c@d", "label": "C"}},
	}
	empty := map[string]any{"name": "bio", "field": "bio", "label": "Bio"}

	wants := map[string][]string{
		"validation-errors":          {"Please fix", "too &lt;short&gt;"},
		"field-errors":               {"is required<br>must be an email"},
		"form-field-with-errors":     {`class="form-control is-invalid"`, "is required<br>must be an email", "placeholder="},
		"textarea-field-with-errors": {`rows="5"`},
		"select-field-with-errors":   {`<option value="a@b" selected>`},
		"checkbox-field-with-errors": {`value="a@b"`, "checked"},
		"htmx-form-errors":           {`id="validation-errors"`, "must be an email"},
		"htmx-field-errors":          {`id="email-errors"`},
	}
	emptyWants := map[string]string{
		"textarea-field-with-errors": `rows="3"`,
		"checkbox-field-with-errors": `value="1"`,
	}

	for name, src := range CommonTemplateSnippets {
		tmpl, err := template.New(name).Funcs(funcs).Parse(src)
		if err != nil {
			t.Errorf("%s does not parse: %v", name, err)
			continue
		}
		var out strings.Builder
		if err := tmpl.Execute(&out, full); err != nil {
			t.Errorf("%s with errors: %v", name, err)
			continue
		}
		for _, want := range wants[name] {
			if !strings.Contains(out.String(), want) {
				t.Errorf("%s output missing %q:\n%s", name, want, out.String())
			}
		}
		out.Reset()
		if err := tmpl.Execute(&out, empty); err != nil {
			t.Errorf("%s without errors: %v", name, err)
			continue
		}
		if want, ok := emptyWants[name]; ok && !strings.Contains(out.String(), want) {
			t.Errorf("%s default missing %q:\n%s", name, want, out.String())
		}
		if strings.Contains(out.String(), "is-invalid") {
			t.Errorf("%s marks a field invalid with no errors", name)
		}
	}
	if len(wants) != len(CommonTemplateSnippets) {
		t.Errorf("test covers %d snippets, package has %d", len(wants), len(CommonTemplateSnippets))
	}
}

func TestTemplateHelpers(t *testing.T) {
	th := NewTemplateHelpers()
	errs := []ValidationError{{Field: "a", Message: "bad <a>"}, {Field: "a", Message: "worse"}, {Field: "b", Message: "x"}}
	if !th.HasValidationErrors(errs) || th.HasValidationErrors(nil) || len(th.GetValidationErrors(errs)) != 3 {
		t.Error("HasValidationErrors/GetValidationErrors")
	}
	if th.GetFieldError(errs, "b").Message != "x" || th.GetFieldError(errs, "c") != nil {
		t.Error("GetFieldError")
	}
	if len(th.GetFieldErrors(errs, "a")) != 2 || th.HasFieldError(errs, "c") {
		t.Error("GetFieldErrors/HasFieldError")
	}
	if got := th.RenderValidationErrors(errs); !strings.Contains(string(got), "bad &lt;a&gt;") || th.RenderValidationErrors(nil) != "" {
		t.Errorf("RenderValidationErrors = %s", got)
	}
	if got := th.RenderFieldErrors(errs, "a"); !strings.Contains(string(got), "bad &lt;a&gt;<br>worse") || th.RenderFieldErrors(errs, "c") != "" {
		t.Errorf("RenderFieldErrors = %s", got)
	}
	if th.ValidationErrorClass(errs) != "has-validation-errors" || th.ValidationErrorClass(nil) != "" {
		t.Error("ValidationErrorClass")
	}
	if th.FieldErrorClass(errs, "a") != "is-invalid" || th.FieldErrorClass(errs, "c") != "" {
		t.Error("FieldErrorClass")
	}
}

func TestCustomErrorRenderer(t *testing.T) {
	errs := []ValidationError{{Field: "f<", Message: "m<"}, {Field: "f<", Message: "n"}}
	for _, framework := range []string{"bootstrap", "tailwind", "custom"} {
		for _, showNames := range []bool{true, false} {
			opts := DefaultErrorRenderingOptions()
			opts.CSSFramework = framework
			opts.ShowFieldNames = showNames
			r := NewCustomErrorRenderer(opts)
			out := string(r.RenderErrors(errs))
			if !strings.Contains(out, "m&lt;") || strings.Contains(out, "m<") {
				t.Errorf("%s RenderErrors = %s", framework, out)
			}
			if strings.Contains(out, "f&lt;") != showNames {
				t.Errorf("%s ShowFieldNames=%t: %s", framework, showNames, out)
			}
			field := string(r.RenderFieldError(errs, "f<"))
			if !strings.Contains(field, "m&lt;") || !strings.Contains(field, "n") {
				t.Errorf("%s RenderFieldError = %s", framework, field)
			}
			if r.RenderErrors(nil) != "" || r.RenderFieldError(errs, "other") != "" {
				t.Errorf("%s renders empty input", framework)
			}
		}
	}
}

func TestFormValidatorRendering(t *testing.T) {
	fv := NewFormValidator(NewSchema())
	result := ValidationResult{Errors: []ValidationError{{Field: "a", Message: "m<"}, {Field: "a", Message: "n"}}}
	if out := fv.RenderValidationErrors(result); !strings.Contains(string(out), "m&lt;") {
		t.Errorf("RenderValidationErrors = %s", out)
	}
	if out := fv.RenderFieldErrors(result, "a"); !strings.Contains(string(out), "field-error") {
		t.Errorf("RenderFieldErrors = %s", out)
	}
	if fv.RenderValidationErrors(ValidationResult{}) != "" || fv.RenderFieldErrors(result, "b") != "" {
		t.Error("rendering without errors")
	}
}

func TestValidateHTMXForm(t *testing.T) {
	fv := NewFormValidator(NewSchema().Field("name", String().MinLength(2).Required()))
	form := func(body string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return r
	}
	rec := httptest.NewRecorder()
	ok := fv.ValidateHTMXForm(rec, form("name=Ada"))
	if !ok.Valid || rec.Header().Get("HX-Trigger") != "form:validated" || ok.Errors != nil {
		t.Errorf("valid: %+v", ok)
	}
	rec = httptest.NewRecorder()
	bad := fv.ValidateHTMXForm(rec, form("name=x"))
	if bad.Valid || bad.Errors["name"] == "" || rec.Header().Get("HX-Retarget") != "#validation-errors" {
		t.Errorf("invalid: %+v", bad)
	}

	// Repeated keys become a slice; a malformed body is a parse error.
	multi := NewFormValidator(NewSchema().Field("tags", Array(String()).MinItems(2)))
	if res := multi.ValidateForm(form("tags=a&tags=b")); !res.Valid {
		t.Errorf("repeated keys: %+v", res.Errors)
	}
	if res := fv.ValidateForm(form("name=%zz")); res.Valid || res.Errors[0].Code != "parse_error" {
		t.Errorf("malformed form: %+v", res)
	}
	if res := ValidateFormData(form("name=Ada"), fv.schema); !res.Valid {
		t.Error("ValidateFormData")
	}
	jsonReq := func(body string) *http.Request {
		return httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	}
	if res := ValidateJSONData(jsonReq(`{"name":"Ada"}`), fv.schema); !res.Valid {
		t.Error("ValidateJSONData")
	}
	if res := fv.ValidateJSON(jsonReq(`{`)); res.Valid || res.Errors[0].Field != "json" {
		t.Errorf("malformed JSON: %+v", res)
	}
	if IsHTMXRequest(nil) {
		t.Error("IsHTMXRequest(nil)")
	}
}

func TestValidationContext(t *testing.T) {
	ctx := context.Background()
	if HasValidationErrors(ctx) || GetValidationErrors(ctx) != nil || GetValidatedData(ctx) != nil {
		t.Error("an empty context has no result")
	}
	if _, ok := GetValidationResult(ctx); ok {
		t.Error("GetValidationResult on an empty context")
	}
	ctx = SetValidationResult(ctx, ValidationResult{Errors: []ValidationError{{Field: "a"}}})
	if !HasValidationErrors(ctx) || len(GetValidationErrors(ctx)) != 1 {
		t.Error("stored result")
	}
}

func TestSchemaDefaultsAndStructs(t *testing.T) {
	type user struct {
		Name    string `json:"name,omitempty"`
		Email   string `json:"email"`
		Secret  string `json:"-"`
		Age     int
		private string
	}
	schema := NewSchema().
		Field("name", String().Trim().ToUpper().Required()).
		Field("email", Email()).
		Field("Age", Int().Min(0)).
		Field("role", String().Default("guest")).
		Field("Secret", String().Required())
	res := schema.ValidateStruct(&user{Name: " ada ", Email: "a@b.co", Secret: "x", Age: 3, private: "p"})
	if res.Valid || res.GetError("Secret") == nil {
		t.Errorf("a json:\"-\" field is not visible: %+v", res)
	}
	if res.Data["name"] != "ADA" || res.Data["role"] != "guest" || res.Data["Age"] != 3 {
		t.Errorf("transformed data = %v", res.Data)
	}
	if res := schema.ValidateStruct(42); res.Valid {
		t.Error("a non-struct has no fields")
	}
	if res := NewSchema().Field("email", Email()).Validate(map[string]any{"email": "nope"}); res.Valid || res.Errors[0].Value != "nope" {
		t.Errorf("a failed value is kept for programmatic use: %+v", res)
	}
}

func TestValidatorTypes(t *testing.T) {
	if err := String().OneOf("a", "b").Validate("c"); err == nil || !strings.Contains(err.Error(), "one of: a, b") {
		t.Errorf("OneOf = %v", err)
	}
	if String().OneOf("a").Validate("a") != nil || String().Validate(1) == nil {
		t.Error("OneOf match / non-string")
	}
	if String().Message("custom").MinLength(5).Validate("x").Error() != "custom" {
		t.Error("Message")
	}
	if String().Phone().Validate("+1 (555) 010-0000") != nil || URL().Validate("ftp://x") == nil || String().UUID().Validate("x") == nil {
		t.Error("format validators")
	}
	if String().Regex(nil).Pattern(`^\d+$`).Validate("12") != nil || String().ToLower().Transform("AB") != "ab" || String().Transform(3) != 3 {
		t.Error("Pattern/Transform")
	}
	if String().Default("d").GetDefault() != "d" || !String().Required().IsRequired() {
		t.Error("Default/Required")
	}

	if Number().Positive().Validate(0) == nil || Number().Negative().Validate(0) == nil || Number().Negative().Validate(-1) != nil {
		t.Error("Positive/Negative")
	}
	if Float().Range(1, 2).Validate(3) == nil || Float().Validate("x") == nil || Number().Min(5).Validate(1) == nil {
		t.Error("Range/Min/type")
	}
	if Int().Transform("4") != 4 || Float().Transform("1.5") != 1.5 || Number().Transform("x") != "x" {
		t.Error("Number Transform")
	}
	if Number().Default(1).GetDefault() != 1.0 || Number().Message("m").Validate("x").Error() != "m" || !Number().Required().IsRequired() {
		t.Error("Number options")
	}

	if Bool().Validate("maybe") == nil || Bool().Validate("on") != nil || Bool().Transform("yes") != true || Bool().Transform("maybe") != "maybe" {
		t.Error("Boolean")
	}
	if Boolean().Default(true).GetDefault() != true || Boolean().Message("m").Validate([]int{}).Error() != "m" || !Boolean().Required().IsRequired() {
		t.Error("Boolean options")
	}

	day := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	if DateTime().Before(day).Validate("2026-02-01") == nil || Date().After(day).Validate("2026-01-01") == nil {
		t.Error("Before/After")
	}
	if Date().Format("2006-01-02").Validate(day) == nil || Date().Transform("2026-01-02") != day || Date().Transform("x") != "x" {
		t.Error("Date format/transform")
	}
	if Date().Format("2006").Transform("x") != "x" || Date().Default(day).GetDefault() != day || Date().Message("m").Validate("x").Error() != "m" || !Date().Required().IsRequired() {
		t.Error("Date options")
	}

	arr := Array(Int()).MinItems(1).MaxItems(2)
	if arr.Validate([]any{}) == nil || arr.Validate([]any{1, 2, 3}) == nil || arr.Validate("x") == nil {
		t.Error("Array bounds/type")
	}
	if err := arr.Validate([]any{1, "x"}); err == nil || !strings.Contains(err.Error(), "item 1") {
		t.Errorf("Array item error = %v", err)
	}
	if got := Array(Int()).Transform([]any{"1", "2"}); len(got.([]any)) != 2 || got.([]any)[0] != 1 {
		t.Errorf("Array Transform = %v", got)
	}
	if Array(nil).Transform("x") != "x" || Array(nil).Length(1).Validate([]string{"a", "b"}) == nil {
		t.Error("Array options")
	}
	if Array(nil).Default([]any{1}).GetDefault() == nil || Array(nil).Message("m").Validate(1).Error() != "m" || !Array(nil).Required().IsRequired() {
		t.Error("Array settings")
	}

	var base BaseValidator
	if base.Validate(1) != nil || base.Transform(1) != 1 || base.Required().Default(2).Message("m").GetErrorMessage("d") != "m" {
		t.Error("BaseValidator")
	}
}

func TestParsers(t *testing.T) {
	for in, want := range map[any]float64{float32(1.5): 1.5, 2: 2, int32(3): 3, int64(4): 4, "5.5": 5.5} {
		if got, err := ParseNumber(in); err != nil || got != want {
			t.Errorf("ParseNumber(%v) = %v, %v", in, got, err)
		}
	}
	if _, err := ParseNumber(true); err == nil {
		t.Error("ParseNumber(bool)")
	}
	for in, want := range map[any]int{1: 1, int32(2): 2, int64(3): 3, 4.9: 4, float32(5): 5, "6": 6} {
		if got, err := ParseInteger(in); err != nil || got != want {
			t.Errorf("ParseInteger(%v) = %v, %v", in, got, err)
		}
	}
	if _, err := ParseInteger(true); err == nil {
		t.Error("ParseInteger(bool)")
	}
	for in, want := range map[any]bool{true: true, "off": false, "": false, 1: true, 0.0: false} {
		if got, err := ParseBoolean(in); err != nil || got != want {
			t.Errorf("ParseBoolean(%v) = %v, %v", in, got, err)
		}
	}
	if _, err := ParseBoolean([]int{}); err == nil {
		t.Error("ParseBoolean(slice)")
	}
}

func TestValidationErrorAndPredefinedSchemas(t *testing.T) {
	e := ValidationError{Field: "f", Message: "m"}
	if e.Error() != "validation failed for field 'f': m" {
		t.Error(e.Error())
	}
	form := url.Values{"name": {"Ada"}, "email": {"ada@example.com"}, "terms": {"on"}, "age": {"30"}, "status": {"active"}}
	data := map[string]any{}
	for k, v := range form {
		data[k] = v[0]
	}
	if res := CreateUserSchema().Validate(data); !res.Valid {
		t.Errorf("CreateUserSchema: %+v", res.Errors)
	}
	post := map[string]any{"title": "Hello", "content": "long enough body", "tags": []any{"go", "web"}, "publish_date": "2026-01-02"}
	if res := CreatePostSchema().Validate(post); !res.Valid || res.Data["published"] != false {
		t.Errorf("CreatePostSchema: %+v", res)
	}
	for _, schema := range []*Schema{LoginSchema, RegisterSchema, ContactSchema, ProfileSchema} {
		if res := schema.Validate(map[string]any{}); res.Valid && schema != ProfileSchema {
			t.Error("an empty form passes a schema with required fields")
		}
	}
}

// A required field's custom Message replaces "field is required" for a
// missing or blank value too, not only for failed rules.
func TestRequiredUsesTheCustomMessage(t *testing.T) {
	schema := NewSchema().
		Field("name", String().Required().Message("Tell us your name")).
		Field("email", String().Required())
	for _, data := range []map[string]any{{}, {"name": "  ", "email": ""}} {
		got := map[string]string{}
		for _, e := range schema.Validate(data).Errors {
			got[e.Field] = e.Message
		}
		if got["name"] != "Tell us your name" || got["email"] != "field is required" {
			t.Errorf("Validate(%v) messages = %v", data, got)
		}
	}
}
