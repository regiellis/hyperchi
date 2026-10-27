package helpers

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// JSON is a decoded JSON object with typed getters. Getters return a zero
// value or the given default when a key is missing or holds another type.
type JSON map[string]any

// NewJSON returns an empty JSON object.
func NewJSON() JSON {
	return make(JSON)
}

// FromJSON parses a JSON object. JSON null becomes an empty object.
func FromJSON(jsonStr string) (JSON, error) {
	var result JSON
	err := json.Unmarshal([]byte(jsonStr), &result)
	if err == nil && result == nil {
		result = NewJSON()
	}
	return result, err
}

// FromJSONOr parses a JSON object, or returns an empty one when jsonStr is
// not a valid object.
func FromJSONOr(jsonStr string) JSON {
	result, err := FromJSON(jsonStr)
	if err != nil {
		return NewJSON()
	}
	return result
}

// Set stores value at key and returns j, allocating a map when j is nil.
// Keep the returned map when the receiver may be nil.
func (j JSON) Set(key string, value any) JSON {
	if j == nil {
		j = NewJSON()
	}
	j[key] = value
	return j
}

// Get returns the value at key, or nil when it is missing.
func (j JSON) Get(key string) any {
	return j[key]
}

// GetString returns the string at key, or "" when it is missing or not a
// string.
func (j JSON) GetString(key string) string {
	if v, ok := j[key].(string); ok {
		return v
	}
	return ""
}

// GetStringOr returns the string at key, or defaultValue when it is missing
// or not a string.
func (j JSON) GetStringOr(key, defaultValue string) string {
	if v, ok := j[key].(string); ok {
		return v
	}
	return defaultValue
}

// GetInt returns GetIntOr(key, 0).
func (j JSON) GetInt(key string) int { return j.GetIntOr(key, 0) }

// GetIntOr returns the value at key as an int. It accepts every Go integer
// type, float32 and float64 (truncated toward zero), json.Number, and a
// base-10 integer string. A value that does not fit in an int, a NaN or
// infinite float, or any other type returns defaultValue.
func (j JSON) GetIntOr(key string, defaultValue int) int {
	switch v := j[key].(type) {
	case int:
		return v
	case int8:
		return int(v)
	case int16:
		return int(v)
	case int32:
		return int(v)
	case int64:
		if n, ok := intFromInt64(v); ok {
			return n
		}
	case uint:
		if n, ok := intFromUint64(uint64(v)); ok {
			return n
		}
	case uint8:
		return int(v)
	case uint16:
		return int(v)
	case uint32:
		if n, ok := intFromUint64(uint64(v)); ok {
			return n
		}
	case uint64:
		if n, ok := intFromUint64(v); ok {
			return n
		}
	case float32:
		if n, ok := intFromFloat(float64(v)); ok {
			return n
		}
	case float64:
		if n, ok := intFromFloat(v); ok {
			return n
		}
	case json.Number:
		if n, err := v.Int64(); err == nil {
			if i, ok := intFromInt64(n); ok {
				return i
			}
		}
	case string:
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return defaultValue
}

// intFromInt64 converts v to int when it fits (always on 64-bit platforms).
func intFromInt64(v int64) (int, bool) {
	n := int(v)
	return n, int64(n) == v
}

// intFromUint64 converts v to int when it does not exceed math.MaxInt.
func intFromUint64(v uint64) (int, bool) {
	if v > math.MaxInt {
		return 0, false
	}
	return int(v), true
}

// intFromFloat truncates v toward zero when the result fits in an int. The
// positive bound is exclusive, avoiding overflow at MaxInt on 64-bit systems.
func intFromFloat(v float64) (int, bool) {
	bound := math.Ldexp(1, strconv.IntSize-1)
	if math.IsNaN(v) || v < -bound || v >= bound {
		return 0, false
	}
	return int(v), true
}

// GetFloat returns GetFloatOr(key, 0).
func (j JSON) GetFloat(key string) float64 { return j.GetFloatOr(key, 0) }

// GetFloatOr returns the value at key as a float64. It accepts float32,
// float64, every Go integer type, json.Number, and a numeric string; any other
// type returns defaultValue.
func (j JSON) GetFloatOr(key string, defaultValue float64) float64 {
	switch v := j[key].(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int8:
		return float64(v)
	case int16:
		return float64(v)
	case int32:
		return float64(v)
	case int64:
		return float64(v)
	case uint:
		return float64(v)
	case uint8:
		return float64(v)
	case uint16:
		return float64(v)
	case uint32:
		return float64(v)
	case uint64:
		return float64(v)
	case json.Number:
		if n, err := v.Float64(); err == nil {
			return n
		}
	case string:
		if n, err := strconv.ParseFloat(v, 64); err == nil {
			return n
		}
	}
	return defaultValue
}

// GetBool returns the bool at key, or false when it is missing or not a
// bool. Strings such as "true" are not converted.
func (j JSON) GetBool(key string) bool {
	if v, ok := j[key].(bool); ok {
		return v
	}
	return false
}

// GetBoolOr returns the bool at key, or defaultValue when it is missing or
// not a bool.
func (j JSON) GetBoolOr(key string, defaultValue bool) bool {
	if v, ok := j[key].(bool); ok {
		return v
	}
	return defaultValue
}

// GetSlice returns the array at key, or an empty slice when it is missing or
// not a []any.
func (j JSON) GetSlice(key string) []any {
	if v, ok := j[key].([]any); ok {
		return v
	}
	return []any{}
}

// GetJSON returns the nested object at key, or an empty JSON when it is
// missing, nil, or not an object. The result shares the nested map, so
// writes to it change j.
func (j JSON) GetJSON(key string) JSON {
	switch v := j[key].(type) {
	case JSON:
		if v != nil {
			return v
		}
	case map[string]any:
		if v != nil {
			return JSON(v)
		}
	}
	return NewJSON()
}

// Has reports whether key is present, even with a null value.
func (j JSON) Has(key string) bool {
	_, exists := j[key]
	return exists
}

// Delete removes key and returns j.
func (j JSON) Delete(key string) JSON {
	delete(j, key)
	return j
}

// Keys returns the keys in no particular order.
func (j JSON) Keys() []string {
	keys := make([]string, 0, len(j))
	for k := range j {
		keys = append(keys, k)
	}
	return keys
}

// Values returns the values in no particular order.
func (j JSON) Values() []any {
	values := make([]any, 0, len(j))
	for _, v := range j {
		values = append(values, v)
	}
	return values
}

// ToString encodes j as compact JSON, or "{}" when encoding fails.
func (j JSON) ToString() string {
	bytes, err := json.Marshal(j)
	if err != nil {
		return "{}"
	}
	return string(bytes)
}

// ToPrettyString encodes j as JSON indented by two spaces, or "{}" when
// encoding fails.
func (j JSON) ToPrettyString() string {
	bytes, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return "{}"
	}
	return string(bytes)
}

// Clone returns a shallow copy: nested maps and slices are shared.
func (j JSON) Clone() JSON {
	clone := make(JSON)
	for k, v := range j {
		clone[k] = v
	}
	return clone
}

// Merge copies every key of other into j, overwriting existing keys, and
// returns j. Keep the returned map when j may be nil.
func (j JSON) Merge(other JSON) JSON {
	if j == nil {
		j = NewJSON()
	}
	for k, v := range other {
		j[k] = v
	}
	return j
}

// ToJSON encodes v as compact JSON, or "{}" when encoding fails.
func ToJSON(v any) string {
	bytes, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(bytes)
}

// ToPrettyJSON encodes v as JSON indented by two spaces, or "{}" when
// encoding fails.
func ToPrettyJSON(v any) string {
	bytes, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "{}"
	}
	return string(bytes)
}

// ParseJSON decodes jsonStr into a new T.
func ParseJSON[T any](jsonStr string) (T, error) {
	var result T
	err := json.Unmarshal([]byte(jsonStr), &result)
	return result, err
}

// ParseJSONOr decodes jsonStr into a new T, or returns defaultValue when
// decoding fails.
func ParseJSONOr[T any](jsonStr string, defaultValue T) T {
	result, err := ParseJSON[T](jsonStr)
	if err != nil {
		return defaultValue
	}
	return result
}

// HTTPClient wraps http.Client with a base URL and default headers. A
// response with a 4xx or 5xx status is not an error; check
// HTTPResponse.IsSuccess.
//
// Configure the client before sharing it. The With methods mutate it and
// must not run concurrently with requests.
type HTTPClient struct {
	client  *http.Client
	baseURL string
	headers map[string]string
}

// NewHTTPClient returns an HTTPClient with a 30-second timeout and no
// default headers.
func NewHTTPClient() *HTTPClient {
	return &HTTPClient{
		client:  &http.Client{Timeout: 30 * time.Second},
		headers: make(map[string]string),
	}
}

// WithTimeout sets the total time limit for each request.
func (h *HTTPClient) WithTimeout(timeout time.Duration) *HTTPClient {
	h.client.Timeout = timeout
	return h
}

// WithBaseURL sets the URL that relative request paths are joined to. An
// absolute http:// or https:// path ignores it.
func (h *HTTPClient) WithBaseURL(baseURL string) *HTTPClient {
	h.baseURL = strings.TrimSuffix(baseURL, "/")
	return h
}

// WithHeader sets a header sent with every request.
func (h *HTTPClient) WithHeader(key, value string) *HTTPClient {
	h.headers[key] = value
	return h
}

// WithHeaders sets several headers sent with every request.
func (h *HTTPClient) WithHeaders(headers map[string]string) *HTTPClient {
	for k, v := range headers {
		h.headers[k] = v
	}
	return h
}

// WithJSONHeaders sends Content-Type and Accept as application/json.
func (h *HTTPClient) WithJSONHeaders() *HTTPClient {
	return h.WithHeader("Content-Type", "application/json").
		WithHeader("Accept", "application/json")
}

// WithAuth sends HTTP Basic credentials with every request.
func (h *HTTPClient) WithAuth(username, password string) *HTTPClient {
	return h.WithHeader("Authorization", "Basic "+basicAuth(username, password))
}

// WithBearerToken sends "Authorization: Bearer <token>" with every request.
func (h *HTTPClient) WithBearerToken(token string) *HTTPClient {
	return h.WithHeader("Authorization", "Bearer "+token)
}

// buildURL joins path to the base URL unless path is already absolute.
func (h *HTTPClient) buildURL(path string) string {
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		return path
	}
	if h.baseURL == "" {
		return path
	}
	return h.baseURL + "/" + strings.TrimPrefix(path, "/")
}

// request sends one request with the client's headers.
func (h *HTTPClient) request(ctx context.Context, method, path string, body io.Reader) (*HTTPResponse, error) {
	req, err := http.NewRequestWithContext(ctx, method, h.buildURL(path), body)
	if err != nil {
		return nil, err
	}

	for k, v := range h.headers {
		req.Header.Set(k, v)
	}

	resp, err := h.client.Do(req)
	if err != nil {
		return nil, err
	}

	return &HTTPResponse{resp: resp}, nil
}

// Get sends a GET request. The caller must read or Close the response.
func (h *HTTPClient) Get(path string) (*HTTPResponse, error) {
	return h.GetWithContext(context.Background(), path)
}

// GetWithContext is Get with the caller's cancellation and deadline.
func (h *HTTPClient) GetWithContext(ctx context.Context, path string) (*HTTPResponse, error) {
	return h.request(ctx, "GET", path, nil)
}

// Post sends a POST request. A string body is sent as is; any other non-nil
// body is JSON-encoded. Content-Type is not set unless the client has
// WithJSONHeaders or WithHeader.
func (h *HTTPClient) Post(path string, body any) (*HTTPResponse, error) {
	return h.PostWithContext(context.Background(), path, body)
}

// PostWithContext is Post with the caller's cancellation and deadline.
func (h *HTTPClient) PostWithContext(ctx context.Context, path string, body any) (*HTTPResponse, error) {
	var reader io.Reader
	if body != nil {
		if bodyStr, ok := body.(string); ok {
			reader = strings.NewReader(bodyStr)
		} else {
			jsonBytes, err := json.Marshal(body)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal JSON body for POST request: %w", err)
			}
			reader = bytes.NewReader(jsonBytes)
		}
	}
	return h.request(ctx, "POST", path, reader)
}

// Put sends a PUT request, encoding body as Post does.
func (h *HTTPClient) Put(path string, body any) (*HTTPResponse, error) {
	return h.PutWithContext(context.Background(), path, body)
}

// PutWithContext is Put with the caller's cancellation and deadline.
func (h *HTTPClient) PutWithContext(ctx context.Context, path string, body any) (*HTTPResponse, error) {
	var reader io.Reader
	if body != nil {
		if bodyStr, ok := body.(string); ok {
			reader = strings.NewReader(bodyStr)
		} else {
			jsonBytes, err := json.Marshal(body)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal JSON body for PUT request: %w", err)
			}
			reader = bytes.NewReader(jsonBytes)
		}
	}
	return h.request(ctx, "PUT", path, reader)
}

// Delete sends a DELETE request.
func (h *HTTPClient) Delete(path string) (*HTTPResponse, error) {
	return h.DeleteWithContext(context.Background(), path)
}

// DeleteWithContext is Delete with the caller's cancellation and deadline.
func (h *HTTPClient) DeleteWithContext(ctx context.Context, path string) (*HTTPResponse, error) {
	return h.request(ctx, "DELETE", path, nil)
}

// Patch sends a PATCH request, encoding body as Post does.
func (h *HTTPClient) Patch(path string, body any) (*HTTPResponse, error) {
	return h.PatchWithContext(context.Background(), path, body)
}

// PatchWithContext is Patch with the caller's cancellation and deadline.
func (h *HTTPClient) PatchWithContext(ctx context.Context, path string, body any) (*HTTPResponse, error) {
	var reader io.Reader
	if body != nil {
		if bodyStr, ok := body.(string); ok {
			reader = strings.NewReader(bodyStr)
		} else {
			jsonBytes, err := json.Marshal(body)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal JSON body for PATCH request: %w", err)
			}
			reader = bytes.NewReader(jsonBytes)
		}
	}
	return h.request(ctx, "PATCH", path, reader)
}

// PostForm sends data as an application/x-www-form-urlencoded POST.
func (h *HTTPClient) PostForm(path string, data url.Values) (*HTTPResponse, error) {
	return h.PostFormWithContext(context.Background(), path, data)
}

// PostFormWithContext is PostForm with the caller's cancellation and
// deadline.
func (h *HTTPClient) PostFormWithContext(ctx context.Context, path string, data url.Values) (*HTTPResponse, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", h.buildURL(path), strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}

	for k, v := range h.headers {
		req.Header.Set(k, v)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := h.client.Do(req)
	if err != nil {
		return nil, err
	}

	return &HTTPResponse{resp: resp}, nil
}

// HTTPResponse wraps an http.Response. Text, JSON, and Parse read and close
// the body, so call at most one of them; otherwise call Close.
type HTTPResponse struct {
	resp *http.Response
}

// StatusCode returns the HTTP status code.
func (r *HTTPResponse) StatusCode() int {
	return r.resp.StatusCode
}

// IsOK reports whether the status is exactly 200.
func (r *HTTPResponse) IsOK() bool {
	return r.resp.StatusCode == 200
}

// IsSuccess reports whether the status is 2xx.
func (r *HTTPResponse) IsSuccess() bool {
	return r.resp.StatusCode >= 200 && r.resp.StatusCode < 300
}

// IsError reports whether the status is 400 or above.
func (r *HTTPResponse) IsError() bool {
	return r.resp.StatusCode >= 400
}

// Text reads and closes the body and returns it as a string. A nil response
// or body returns an error, and so does a body that fails to close.
func (r *HTTPResponse) Text() (string, error) {
	if r == nil || r.resp == nil || r.resp.Body == nil {
		return "", fmt.Errorf("response body is empty")
	}
	body, err := io.ReadAll(r.resp.Body)
	if err := errors.Join(err, r.closeBody()); err != nil {
		return "", err
	}
	return string(body), nil
}

// closeBody closes the body, wrapping a failure.
func (r *HTTPResponse) closeBody() error {
	if err := r.resp.Body.Close(); err != nil {
		return fmt.Errorf("closing response body: %w", err)
	}
	return nil
}

// TextOr returns Text, or defaultValue when reading fails.
func (r *HTTPResponse) TextOr(defaultValue string) string {
	text, err := r.Text()
	if err != nil {
		return defaultValue
	}
	return text
}

// JSON reads and closes the body and parses it as a JSON object. The status
// is not checked, so an error response's JSON body parses too.
func (r *HTTPResponse) JSON() (JSON, error) {
	text, err := r.Text()
	if err != nil {
		return NewJSON(), err
	}
	return FromJSON(text)
}

// JSONOr returns JSON, or an empty object when reading or parsing fails.
func (r *HTTPResponse) JSONOr() JSON {
	json, err := r.JSON()
	if err != nil {
		return NewJSON()
	}
	return json
}

// Parse decodes the JSON body into v and closes the body. A nil response or
// body returns an error, and so does a body that fails to close.
func (r *HTTPResponse) Parse(v any) error {
	if r == nil || r.resp == nil || r.resp.Body == nil {
		return fmt.Errorf("response body is empty")
	}
	err := json.NewDecoder(r.resp.Body).Decode(v)
	if err != nil {
		err = fmt.Errorf("failed to parse response body: %w", err)
	}
	return errors.Join(err, r.closeBody())
}

// Header returns the first value of the response header key.
func (r *HTTPResponse) Header(key string) string {
	return r.resp.Header.Get(key)
}

// Headers returns the response headers.
func (r *HTTPResponse) Headers() map[string][]string {
	return r.resp.Header
}

// Close closes the response body. It is safe on a nil response.
func (r *HTTPResponse) Close() error {
	if r == nil || r.resp == nil || r.resp.Body == nil {
		return nil
	}
	return r.resp.Body.Close()
}

// Get sends a GET request with a new default HTTPClient.
func Get(url string) (*HTTPResponse, error) {
	return NewHTTPClient().Get(url)
}

// Post sends body as a JSON POST with a new default HTTPClient.
func Post(url string, body any) (*HTTPResponse, error) {
	return NewHTTPClient().WithJSONHeaders().Post(url, body)
}

// Put sends body as a JSON PUT with a new default HTTPClient.
func Put(url string, body any) (*HTTPResponse, error) {
	return NewHTTPClient().WithJSONHeaders().Put(url, body)
}

// Delete sends a DELETE request with a new default HTTPClient.
func Delete(url string) (*HTTPResponse, error) {
	return NewHTTPClient().Delete(url)
}

// PostForm sends data as a form POST with a new default HTTPClient.
func PostForm(url string, data url.Values) (*HTTPResponse, error) {
	return NewHTTPClient().PostForm(url, data)
}

// FetchJSON GETs url and parses the body as a JSON object. The status is not
// checked.
func FetchJSON(url string) (JSON, error) {
	resp, err := Get(url)
	if err != nil {
		return NewJSON(), err
	}
	return resp.JSON()
}

// FetchJSONOr is FetchJSON, returning an empty object on any error
// (request, body read, or decode).
func FetchJSONOr(url string) JSON {
	json, err := FetchJSON(url)
	if err != nil {
		return NewJSON()
	}
	return json
}

// FetchText GETs url and returns the body as a string. The status is not
// checked.
func FetchText(url string) (string, error) {
	resp, err := Get(url)
	if err != nil {
		return "", err
	}
	return resp.Text()
}

// FetchTextOr is FetchText, returning defaultValue on any error.
func FetchTextOr(url, defaultValue string) string {
	text, err := FetchText(url)
	if err != nil {
		return defaultValue
	}
	return text
}

// basicAuth encodes username and password as RFC 7617 Basic credentials.
func basicAuth(username, password string) string {
	return base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
}

// QueryParams builds a URL query string. Its setters return the receiver
// for chaining.
type QueryParams struct {
	values url.Values
}

// NewQueryParams returns an empty QueryParams.
func NewQueryParams() *QueryParams {
	return &QueryParams{values: make(url.Values)}
}

// Set sets key to value, replacing earlier values.
func (qp *QueryParams) Set(key, value string) *QueryParams {
	qp.values.Set(key, value)
	return qp
}

// Add adds value to key, keeping earlier values.
func (qp *QueryParams) Add(key, value string) *QueryParams {
	qp.values.Add(key, value)
	return qp
}

// SetInt sets key to the decimal form of value.
func (qp *QueryParams) SetInt(key string, value int) *QueryParams {
	qp.values.Set(key, strconv.Itoa(value))
	return qp
}

// SetBool sets key to "true" or "false".
func (qp *QueryParams) SetBool(key string, value bool) *QueryParams {
	qp.values.Set(key, strconv.FormatBool(value))
	return qp
}

// SetIf sets key to value when condition is true.
func (qp *QueryParams) SetIf(condition bool, key, value string) *QueryParams {
	if condition {
		qp.values.Set(key, value)
	}
	return qp
}

// SetIfNotEmpty sets key to value when value is not "".
func (qp *QueryParams) SetIfNotEmpty(key, value string) *QueryParams {
	if value != "" {
		qp.values.Set(key, value)
	}
	return qp
}

// String returns the encoded query, sorted by key, without a leading "?".
func (qp *QueryParams) String() string {
	return qp.values.Encode()
}

// Values returns the underlying url.Values, not a copy.
func (qp *QueryParams) Values() url.Values {
	return qp.values
}

// URL appends the query to baseURL with "?", or with "&" when baseURL
// already has a query. With no parameters it returns baseURL unchanged.
func (qp *QueryParams) URL(baseURL string) string {
	if len(qp.values) == 0 {
		return baseURL
	}
	separator := "?"
	if strings.Contains(baseURL, "?") {
		separator = "&"
	}
	return baseURL + separator + qp.String()
}
