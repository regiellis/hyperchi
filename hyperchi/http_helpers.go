package hyperchi

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"
)

// WithContext returns a copy of r whose context carries value under key, for
// middleware to hand values to templates (they appear as {{.key}}) and to
// c.Value. The values live in one map per request chain: a key added here is
// also visible through earlier requests that share that map.
//
//	updatedRequest := WithContext(r, "user_id", 123)
//	updatedRequest = WithContext(updatedRequest, "user_role", "admin")
func WithContext(r *http.Request, key string, value any) *http.Request {
	ctx := r.Context()
	if existing := ctx.Value(hxValuesCtxKey); existing != nil {
		if values, ok := existing.(map[string]any); ok {
			values[key] = value
			return r.WithContext(context.WithValue(ctx, hxValuesCtxKey, values))
		}
	}

	values := map[string]any{key: value}
	return r.WithContext(context.WithValue(ctx, hxValuesCtxKey, values))
}

// H is a shorthand for map[string]any, for template data and JSON bodies.
type H map[string]any

// ParseForm returns r's POST, PUT, or PATCH form body as a map: a single value
// as a string, a repeated one as a []string. In a Handler, use Bind.
func ParseForm(r *http.Request) (map[string]any, error) {
	if err := r.ParseForm(); err != nil {
		return nil, err
	}

	result := make(map[string]any)
	for key, values := range r.PostForm {
		if len(values) == 1 {
			result[key] = values[0]
		} else {
			result[key] = values
		}
	}
	return result, nil
}

// ParseJSON decodes r's body into v. A field v does not declare is an error,
// as is a nil body. In a Handler, use Bind.
func ParseJSON(r *http.Request, v any) error {
	if r.Body == nil {
		return fmt.Errorf("request body is empty")
	}

	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(v); err != nil {
		return fmt.Errorf("failed to parse JSON: %w", err)
	}

	return nil
}

// WriteJSON writes v as a JSON response, with <, >, and & escaped. An encoding
// failure is logged, answered with a 500, and returned. In a Handler, use
// c.JSON, which encodes before sending any header.
func WriteJSON(w http.ResponseWriter, v any) error {
	w.Header().Set("Content-Type", "application/json")

	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(true)

	if err := encoder.Encode(v); err != nil {
		log.Printf("hyperchi: WriteJSON: encode %T: %v", v, err)
		http.Error(w, `{"error": "internal server error"}`, http.StatusInternalServerError)
		return fmt.Errorf("failed to encode JSON response: %w", err)
	}

	return nil
}

// GetParam returns a chi route parameter. In a Handler, use c.Param.
func GetParam(r *http.Request, key string) string {
	return chi.URLParam(r, key)
}

// GetQuery returns the first value of a query parameter, or "".
func GetQuery(r *http.Request, key string) string {
	return r.URL.Query().Get(key)
}

// GetQueryDefault returns a query parameter, or defaultValue when it is
// missing or empty.
func GetQueryDefault(r *http.Request, key, defaultValue string) string {
	if value := r.URL.Query().Get(key); value != "" {
		return value
	}
	return defaultValue
}

// GetQueries returns every value of a query parameter.
func GetQueries(r *http.Request, key string) []string {
	return r.URL.Query()[key]
}

// BuildURL sets params on path's query string. A path that does not parse is
// returned unchanged.
func BuildURL(path string, params map[string]string) string {
	u, err := url.Parse(path)
	if err != nil {
		return path
	}

	q := u.Query()
	for key, value := range params {
		q.Set(key, value)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// IsHXRequest reports whether r carries HX-Request: true. In a Handler, use
// c.IsHTMX.
func IsHXRequest(r *http.Request) bool {
	return isHTMXRequest(r)
}

// GetHXTarget returns the HX-Target request header, the element htmx will
// swap into, as "tagName#id" (such as "div#content").
func GetHXTarget(r *http.Request) string {
	if r == nil {
		return ""
	}
	return r.Header.Get("HX-Target")
}

// GetHXSource returns the HX-Source request header, the element that issued
// the request, as "tagName#id" (such as "button#submit"). htmx 4 sends it in
// place of htmx 2's HX-Trigger request header.
func GetHXSource(r *http.Request) string {
	if r == nil {
		return ""
	}
	return r.Header.Get("HX-Source")
}

// GetHXCurrentURL returns the HX-Current-URL request header: the page the
// browser was on when htmx sent the request.
func GetHXCurrentURL(r *http.Request) string {
	if r == nil {
		return ""
	}
	return r.Header.Get("HX-Current-URL")
}

// GetHXRequestType returns the htmx 4 HX-Request-Type header value, either
// "full" (full page request) or "partial" (htmx fragment request). Empty
// when the request is not from htmx.
func GetHXRequestType(r *http.Request) string {
	if r == nil {
		return ""
	}
	return r.Header.Get("HX-Request-Type")
}

// IsHXBoosted reports whether r is a boosted navigation: a link or form that
// hx-boost sent through htmx. It replaces the page body without a full
// reload, so it needs the whole page.
func IsHXBoosted(r *http.Request) bool {
	if r == nil {
		return false
	}
	return r.Header.Get("HX-Boosted") == "true"
}

// IsHXHistoryRestoreRequest reports whether r restores a history entry after
// the back or forward button. Answer it with the full page, not a fragment.
func IsHXHistoryRestoreRequest(r *http.Request) bool {
	if r == nil {
		return false
	}
	return r.Header.Get("HX-History-Restore-Request") == "true"
}

// SetHXTrigger sets the HX-Trigger response header. In a Handler, use
// c.HX().Trigger.
func SetHXTrigger(w http.ResponseWriter, trigger string) {
	w.Header().Set("HX-Trigger", trigger)
}

// SetHXPushURL sets the HX-Push-Url response header. In a Handler, use
// c.HX().PushURL.
func SetHXPushURL(w http.ResponseWriter, url string) {
	w.Header().Set("HX-Push-Url", url)
}

// SetHXRedirect sets the HX-Redirect response header. In a Handler, use
// c.Redirect.
func SetHXRedirect(w http.ResponseWriter, url string) {
	w.Header().Set("HX-Redirect", url)
}

// SetHXRefresh sets HX-Refresh: true. In a Handler, use c.HX().Refresh.
func SetHXRefresh(w http.ResponseWriter) {
	w.Header().Set("HX-Refresh", "true")
}

// SetHXReplaceURL sets the HX-Replace-Url response header. In a Handler, use
// c.HX().ReplaceURL.
func SetHXReplaceURL(w http.ResponseWriter, url string) {
	w.Header().Set("HX-Replace-Url", url)
}

// SetHXRetarget sets the HX-Retarget response header. In a Handler, use
// c.HX().Retarget.
func SetHXRetarget(w http.ResponseWriter, target string) {
	w.Header().Set("HX-Retarget", target)
}

// SetHXReswap sets the HX-Reswap response header. In a Handler, use
// c.HX().Reswap.
func SetHXReswap(w http.ResponseWriter, swap string) {
	w.Header().Set("HX-Reswap", swap)
}

// ErrorResponse is a JSON error body for code that writes its own. The
// framework's error pipeline does not use it; DefaultError sends
// {"error": ..., "fields": ...}.
type ErrorResponse struct {
	Error   string         `json:"error"`
	Message string         `json:"message"`
	Code    int            `json:"code"`
	Details map[string]any `json:"details,omitempty"`
	Stack   string         `json:"stack,omitempty"`
}
