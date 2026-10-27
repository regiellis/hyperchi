package hyperchi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/mail"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const maxJSONBody = 1 << 20 // 1 MiB

// maxMultipartBody caps a whole multipart body and is also the part of it held
// in memory. A variable so tests can lower it.
var maxMultipartBody int64 = 32 << 20

// Bind decodes the request into a new T and validates it. JSON bodies decode
// by json tags; forms, multipart forms, and query strings decode by form tags.
// A malformed body is a 400 error, a JSON body over 1 MiB or a multipart body
// over 32 MiB is a 413, and a failed check is a 422 error with per-field
// messages, so a handler can return the error as is:
//
//	type NewUser struct {
//		Name  string `form:"name" validate:"required,max=80"`
//		Email string `form:"email" validate:"required,email"`
//		Age   *int   `form:"age" validate:"min=13"`
//	}
//
//	in, err := hyperchi.Bind[NewUser](c)
//	if err != nil {
//		return err
//	}
//
// Validation rules: required, min=N, max=N (length for strings and slices,
// value for numbers), email, url, oneof=a b c. A struct that also has a
// Validate() error method runs it after the tag rules pass. Bind[map[string]any]
// returns the raw JSON object or form values without validation.
func Bind[T any](c *Context) (T, error) {
	var v T
	err := c.Bind(&v)
	return v, err
}

// Bind decodes and validates the request into dst, a pointer to a struct.
// A *map[string]any receives the raw JSON object or form values instead, with
// no validation. See the package-level Bind.
func (c *Context) Bind(dst any) error {
	if m, ok := dst.(*map[string]any); ok {
		return c.bindMap(m)
	}
	rv := reflect.ValueOf(dst)
	if rv.Kind() != reflect.Pointer || rv.IsNil() || rv.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("hyperchi: Bind needs a non-nil pointer to a struct, got %T", dst)
	}
	fields, err := c.decode(rv.Elem())
	if err != nil {
		return err
	}
	return finishBind(dst, rv, fields)
}

// finishBind runs the validate tags and the optional Validate method after
// decoding, merging tag failures with the decode's conversion messages.
func finishBind(dst any, rv reflect.Value, fields map[string][]string) error {
	checked, err := validateStruct(rv.Elem())
	if err != nil {
		return err
	}
	for name, msgs := range checked {
		if _, unreadable := fields[name]; !unreadable {
			if fields == nil {
				fields = map[string][]string{}
			}
			fields[name] = msgs
		}
	}
	if len(fields) > 0 {
		return Unprocessable("Please correct the highlighted fields.", fields)
	}
	if v, ok := dst.(interface{ Validate() error }); ok {
		if err := v.Validate(); err != nil {
			var he *HTTPError
			if errors.As(err, &he) {
				return he
			}
			return NewHTTPError(http.StatusUnprocessableEntity, err.Error()).Wrap(err)
		}
	}
	return nil
}

// ValidateSchema checks the submitted form or JSON body against a schema
// registered on app.Schemas, returning the cleaned data, or a 422 error with
// per-field messages. An unregistered name is a programming error (500).
func (c *Context) ValidateSchema(name string) (map[string]any, error) {
	var data map[string]any
	if isJSON(c.r) {
		if err := c.decodeJSONMap(&data); err != nil {
			return nil, err
		}
	} else {
		form, err := ParseForm(c.r)
		if err != nil {
			return nil, BadRequest("The form could not be read.").Wrap(err)
		}
		data = form
	}
	schema, ok := c.app.Schemas.Get(name)
	if !ok {
		return nil, fmt.Errorf("hyperchi: no schema registered as %q", name)
	}
	result := schema.Validate(data)
	if !result.Valid {
		return nil, Unprocessable("Please correct the highlighted fields.", fieldMessages(result.Errors))
	}
	return result.Data, nil
}

func (c *Context) bindMap(dst *map[string]any) error {
	if isJSON(c.r) {
		return c.decodeJSONMap(dst)
	}
	mt, _, _ := mime.ParseMediaType(c.r.Header.Get("Content-Type"))
	if mt == "multipart/form-data" {
		if err := c.parseMultipart(); err != nil {
			return err
		}
	} else if err := c.r.ParseForm(); err != nil {
		return BadRequest("The form could not be read.").Wrap(err)
	}
	m := make(map[string]any, len(c.r.Form))
	for key, values := range c.r.Form {
		if len(values) == 1 {
			m[key] = values[0]
		} else {
			m[key] = values
		}
	}
	*dst = m
	return nil
}

// decodeJSONMap reads a JSON object body into dst with the same size limit
// and error responses as Bind.
func (c *Context) decodeJSONMap(dst *map[string]any) error {
	c.r.Body = http.MaxBytesReader(c.w, c.r.Body, maxJSONBody)
	if err := json.NewDecoder(c.r.Body).Decode(dst); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return NewHTTPError(http.StatusRequestEntityTooLarge, "The request body is too large.")
		}
		return BadRequest("The request body is not valid JSON.").Wrap(err)
	}
	return nil
}

// parseMultipart parses a multipart body of at most maxMultipartBody bytes,
// answering a larger one with 413 instead of spilling it to disk.
func (c *Context) parseMultipart() error {
	c.r.Body = http.MaxBytesReader(c.w, c.r.Body, maxMultipartBody)
	if err := c.r.ParseMultipartForm(maxMultipartBody); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return NewHTTPError(http.StatusRequestEntityTooLarge, "The request body is too large.")
		}
		return BadRequest("The form could not be read.").Wrap(err)
	}
	return nil
}

func isJSON(r *http.Request) bool {
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return err == nil && (mt == "application/json" || strings.HasSuffix(mt, "+json"))
}

// decode fills the struct and returns per-field conversion errors.
func (c *Context) decode(v reflect.Value) (map[string][]string, error) {
	r := c.r
	if isJSON(r) {
		r.Body = http.MaxBytesReader(c.w, r.Body, maxJSONBody)
		if err := json.NewDecoder(r.Body).Decode(v.Addr().Interface()); err != nil {
			if errors.Is(err, io.EOF) {
				return nil, BadRequest("The request body is empty.")
			}
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				return nil, NewHTTPError(http.StatusRequestEntityTooLarge, "The request body is too large.")
			}
			return nil, BadRequest("The request body is not valid JSON.").Wrap(err)
		}
		return nil, nil
	}

	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	var files map[string][]*multipart.FileHeader
	if mt == "multipart/form-data" {
		if err := c.parseMultipart(); err != nil {
			return nil, err
		}
		files = r.MultipartForm.File
	} else if err := r.ParseForm(); err != nil {
		return nil, BadRequest("The form could not be read.").Wrap(err)
	}
	fields := map[string][]string{}
	if err := decodeForm(v, r.Form, files, fields); err != nil {
		return nil, err
	}
	return fields, nil
}

var (
	timeType       = reflect.TypeOf(time.Time{})
	fileHeaderType = reflect.TypeOf((*multipart.FileHeader)(nil))
)

func decodeForm(v reflect.Value, values url.Values, files map[string][]*multipart.FileHeader, fields map[string][]string) error {
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		fv := v.Field(i)
		if sf.Anonymous && sf.Type.Kind() == reflect.Struct {
			if err := decodeForm(fv, values, files, fields); err != nil {
				return err
			}
			continue
		}
		name := fieldName(sf)
		if name == "" || !sf.IsExported() {
			continue
		}
		switch {
		case sf.Type == fileHeaderType:
			if fh := files[name]; len(fh) > 0 {
				fv.Set(reflect.ValueOf(fh[0]))
			}
			continue
		case sf.Type == reflect.SliceOf(fileHeaderType):
			if fh := files[name]; len(fh) > 0 {
				fv.Set(reflect.ValueOf(fh))
			}
			continue
		}
		raw, present := values[name]
		if !present || len(raw) == 0 {
			continue
		}
		if msg, err := setField(fv, raw); err != nil {
			return fmt.Errorf("hyperchi: Bind field %s: %w", sf.Name, err)
		} else if msg != "" {
			fields[name] = append(fields[name], msg)
		}
	}
	return nil
}

// setField converts form values into fv. A value the user typed wrong comes
// back as a message; a field type Bind cannot fill is an error.
func setField(fv reflect.Value, raw []string) (string, error) {
	if fv.Kind() == reflect.Slice && fv.Type().Elem().Kind() != reflect.Uint8 {
		out := reflect.MakeSlice(fv.Type(), 0, len(raw))
		for _, s := range raw {
			elem := reflect.New(fv.Type().Elem()).Elem()
			if msg, err := setScalar(elem, s); err != nil || msg != "" {
				return msg, err
			}
			out = reflect.Append(out, elem)
		}
		fv.Set(out)
		return "", nil
	}
	if fv.Kind() == reflect.Pointer {
		if raw[0] == "" {
			return "", nil
		}
		elem := reflect.New(fv.Type().Elem())
		if msg, err := setScalar(elem.Elem(), raw[0]); err != nil || msg != "" {
			return msg, err
		}
		fv.Set(elem)
		return "", nil
	}
	return setScalar(fv, raw[0])
}

func setScalar(fv reflect.Value, s string) (string, error) {
	if fv.Type() == timeType {
		if s == "" {
			return "", nil
		}
		for _, layout := range []string{time.RFC3339, "2006-01-02T15:04", "2006-01-02"} {
			if t, err := time.Parse(layout, s); err == nil {
				fv.Set(reflect.ValueOf(t))
				return "", nil
			}
		}
		return "must be a valid date", nil
	}
	switch fv.Kind() {
	case reflect.String:
		fv.SetString(s)
	case reflect.Bool:
		switch strings.ToLower(s) {
		case "", "0", "false", "off", "no":
			fv.SetBool(false)
		default:
			fv.SetBool(true)
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if s == "" {
			return "", nil
		}
		n, err := strconv.ParseInt(strings.TrimSpace(s), 10, fv.Type().Bits())
		if err != nil {
			return "must be a whole number", nil
		}
		fv.SetInt(n)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if s == "" {
			return "", nil
		}
		n, err := strconv.ParseUint(strings.TrimSpace(s), 10, fv.Type().Bits())
		if err != nil {
			return "must be a whole number", nil
		}
		fv.SetUint(n)
	case reflect.Float32, reflect.Float64:
		if s == "" {
			return "", nil
		}
		n, err := strconv.ParseFloat(strings.TrimSpace(s), fv.Type().Bits())
		if err != nil {
			return "must be a number", nil
		}
		fv.SetFloat(n)
	default:
		return "", fmt.Errorf("unsupported type %s", fv.Type())
	}
	return "", nil
}

// fieldName is the form and error key for a struct field: its form tag, else
// its json tag, else the field name in lower case. "-" skips the field.
func fieldName(sf reflect.StructField) string {
	for _, key := range []string{"form", "json"} {
		if tag, ok := sf.Tag.Lookup(key); ok {
			name, _, _ := strings.Cut(tag, ",")
			if name == "-" {
				return ""
			}
			if name != "" {
				return name
			}
		}
	}
	return strings.ToLower(sf.Name)
}

// validateStruct applies validate tags and returns per-field messages. A rule
// it does not know is an error, so a typo in a tag fails loudly.
func validateStruct(v reflect.Value) (map[string][]string, error) {
	fields := map[string][]string{}
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		fv := v.Field(i)
		if sf.Anonymous && sf.Type.Kind() == reflect.Struct {
			nested, err := validateStruct(fv)
			if err != nil {
				return nil, err
			}
			for k, msgs := range nested {
				fields[k] = append(fields[k], msgs...)
			}
			continue
		}
		tag := sf.Tag.Get("validate")
		if tag == "" || !sf.IsExported() {
			continue
		}
		name := fieldName(sf)
		if name == "" {
			name = strings.ToLower(sf.Name)
		}
		msgs, err := checkField(fv, tag)
		if err != nil {
			return nil, fmt.Errorf("hyperchi: validate tag on %s.%s: %w", t.Name(), sf.Name, err)
		}
		if len(msgs) > 0 {
			fields[name] = msgs
		}
	}
	return fields, nil
}

func checkField(fv reflect.Value, tag string) ([]string, error) {
	if fv.Kind() == reflect.Pointer {
		if fv.IsNil() {
			if hasRule(tag, "required") {
				return []string{"is required"}, nil
			}
			return nil, nil
		}
		fv = fv.Elem()
	}
	empty := isEmptyValue(fv)
	var msgs []string
	for _, rule := range strings.Split(tag, ",") {
		rule = strings.TrimSpace(rule)
		if rule == "" {
			continue
		}
		name, arg, _ := strings.Cut(rule, "=")
		switch name {
		case "required":
			if empty {
				return []string{"is required"}, nil
			}
		case "min", "max":
			n, err := strconv.ParseFloat(arg, 64)
			if err != nil {
				return nil, fmt.Errorf("%s needs a number, got %q", name, arg)
			}
			if msg := checkBound(fv, name, n, empty); msg != "" {
				msgs = append(msgs, msg)
			}
		case "email":
			if !empty && !validEmail(fv.String()) {
				msgs = append(msgs, "must be a valid email address")
			}
		case "url":
			if !empty && !validURL(fv.String()) {
				msgs = append(msgs, "must be a valid URL")
			}
		case "oneof":
			options := strings.Fields(arg)
			if !empty && !oneOf(fv, options) {
				msgs = append(msgs, "must be one of: "+strings.Join(options, ", "))
			}
		default:
			return nil, fmt.Errorf("unknown rule %q", name)
		}
	}
	return msgs, nil
}

func hasRule(tag, rule string) bool {
	for _, r := range strings.Split(tag, ",") {
		if strings.TrimSpace(r) == rule {
			return true
		}
	}
	return false
}

func isEmptyValue(v reflect.Value) bool {
	if v.Type() == timeType {
		return v.Interface().(time.Time).IsZero()
	}
	switch v.Kind() {
	case reflect.String:
		return strings.TrimSpace(v.String()) == ""
	case reflect.Slice, reflect.Map:
		return v.Len() == 0
	case reflect.Pointer, reflect.Interface:
		return v.IsNil()
	default:
		return v.IsZero()
	}
}

// checkBound applies min or max: a length for strings and slices, a value for
// numbers. An empty optional string or slice skips the check.
func checkBound(v reflect.Value, rule string, n float64, empty bool) string {
	var got float64
	unit := ""
	switch v.Kind() {
	case reflect.String:
		if empty {
			return ""
		}
		got, unit = float64(utf8.RuneCountInString(v.String())), " characters"
	case reflect.Slice, reflect.Map:
		if empty {
			return ""
		}
		got, unit = float64(v.Len()), " items"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		got = float64(v.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		got = float64(v.Uint())
	case reflect.Float32, reflect.Float64:
		got = v.Float()
	default:
		return ""
	}
	bound := strconv.FormatFloat(n, 'f', -1, 64)
	if rule == "min" && got < n {
		return "must be at least " + bound + unit
	}
	if rule == "max" && got > n {
		return "must be at most " + bound + unit
	}
	return ""
}

func validEmail(s string) bool {
	addr, err := mail.ParseAddress(s)
	return err == nil && addr.Address == s
}

func validURL(s string) bool {
	u, err := url.ParseRequestURI(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

func oneOf(v reflect.Value, options []string) bool {
	got := fmt.Sprint(v.Interface())
	for _, o := range options {
		if got == o {
			return true
		}
	}
	return false
}
