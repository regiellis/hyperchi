package hyperchi

import (
	"bytes"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Credentials in request headers never reach an error report, which is
// rendered on development error pages and serialized to JSON.
func TestErrorReportRedactsCredentialHeaders(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/boom", nil)
	sensitive := map[string]string{
		"Authorization":       "Bearer secret-authorization",
		"Proxy-Authorization": "Basic secret-proxy",
		"Cookie":              "session_id=secret-cookie",
		"Set-Cookie":          "session_id=secret-set-cookie",
		"X-Api-Key":           "secret-api-key",
		"X-Auth-Token":        "secret-auth-token",
		"X-Csrf-Token":        "secret-csrf",
	}
	for name, value := range sensitive {
		r.Header.Set(name, value)
	}
	r.Header.Set("Accept", "text/html")

	info := NewErrorPageHandler(true).captureRequestInfo(r)
	for name := range sensitive {
		if got := info.Headers[name]; got != "[REDACTED]" {
			t.Errorf("header %s = %q, want [REDACTED]", name, got)
		}
	}
	if got := info.Headers["Accept"]; got != "text/html" {
		t.Errorf("Accept = %q, want it kept", got)
	}
	body, err := json.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "secret-") {
		t.Errorf("serialized report leaks a credential: %s", body)
	}
}

// A working directory that cannot be read is reported, not dropped.
func TestNewErrorTrackerLogsUnreadableWorkingDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "gone")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Getwd(); err == nil {
		t.Skip("this platform still reports a removed working directory")
	}

	var buf bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(previous) })

	NewErrorTracker(true)
	if !strings.Contains(buf.String(), "working directory") {
		t.Errorf("NewErrorTracker logged %q, want the working directory error", buf.String())
	}
}

// Errorf's %w cause stays out of the client message and in the error chain.
func TestErrorfKeepsCauseOutOfMessage(t *testing.T) {
	cause := errors.New("dial db: password=hunter2")
	cases := []struct {
		format string
		args   []any
		want   string
		wraps  bool
	}{
		{"could not load user %d: %w", []any{42, cause}, "could not load user 42", true},
		{"load %w failed", []any{cause}, "load failed", true},
		{"%w", []any{cause}, "Service Unavailable", true},
		{"%[2]d%% of %[1]w", []any{cause, 50}, "50% of", true},
		{"no cause here: %s", []any{"plain"}, "no cause here: plain", false},
	}
	for _, tc := range cases {
		he := Errorf(http.StatusServiceUnavailable, tc.format, tc.args...)
		if he.Message != tc.want {
			t.Errorf("Errorf(%q).Message = %q, want %q", tc.format, he.Message, tc.want)
		}
		if errors.Is(he, cause) != tc.wraps {
			t.Errorf("Errorf(%q) wraps the cause = %v, want %v", tc.format, !tc.wraps, tc.wraps)
		}
	}

	app := noDBApp(t)
	app.Configure().Env("production") // development pages show the cause on purpose
	app.Get("/user", func(c *Context) error {
		return Errorf(http.StatusServiceUnavailable, "could not load user: %w", cause)
	})
	for _, accept := range []string{"application/json", "text/html", "htmx"} {
		req := httptest.NewRequest(http.MethodGet, "/user", nil)
		if accept == "htmx" {
			req.Header.Set("HX-Request", "true")
		} else {
			req.Header.Set("Accept", accept)
		}
		w := httptest.NewRecorder()
		app.ServeHTTP(w, req)
		if strings.Contains(w.Body.String(), "hunter2") {
			t.Errorf("%s response leaks the cause: %s", accept, w.Body.String())
		}
	}
}

// A query string can carry credentials (reset links, OAuth callbacks, signed
// URLs). The report's URL and Query keep the parameter names and redact the
// values whose names look secret, by the rule header redaction uses.
func TestErrorReportRedactsSensitiveQueryParameters(t *testing.T) {
	target := "/callback?page=2&access_token=secret-at&api_key=secret-key&X-Amz-Signature=secret-sig" +
		"&session_id=secret-sess&csrf=secret-csrf&password=secret-pw&client_secret=secret-cs&q=hello+world"
	r := httptest.NewRequest(http.MethodGet, target, nil)

	info := NewErrorPageHandler(true).captureRequestInfo(r)
	body, err := json.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "secret-") {
		t.Fatalf("report leaks a query credential: %s", body)
	}
	for _, name := range []string{"access_token", "api_key", "X-Amz-Signature", "session_id", "csrf", "password", "client_secret"} {
		if got := info.Query[name]; got != "[REDACTED]" {
			t.Errorf("Query[%s] = %q, want [REDACTED]", name, got)
		}
		if !strings.Contains(info.URL, name+"=") {
			t.Errorf("URL %q lost the parameter name %s", info.URL, name)
		}
	}
	if info.Query["page"] != "2" || info.Query["q"] != "hello world" {
		t.Errorf("ordinary parameters changed: %v", info.Query)
	}
	if !strings.HasPrefix(info.URL, "/callback?page=2&") || !strings.HasSuffix(info.URL, "&q=hello+world") {
		t.Errorf("URL = %q, want path and ordinary parameters kept in order", info.URL)
	}
}
