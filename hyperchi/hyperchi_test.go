package hyperchi

import (
	"bytes"
	"html/template"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestNew(t *testing.T) {
	hx := New()
	if hx == nil {
		t.Fatal("New() returned nil")
	}
	if hx.funcMap == nil {
		t.Fatal("funcMap should be initialized")
	}

	if _, exists := hx.funcMap["json"]; !exists {
		t.Error("Expected 'json' filter to be registered")
	}
}

func TestLoadTemplates(t *testing.T) {
	hx := New()

	tmpDir := t.TempDir()
	templatePath := tmpDir + "/test.tmpl"
	templateContent := `<h1>{{.Title}}</h1>`

	if err := os.WriteFile(templatePath, []byte(templateContent), 0644); err != nil {
		t.Fatalf("Failed to create test template: %v", err)
	}

	err := hx.LoadTemplates(tmpDir + "/*.tmpl")
	if err != nil {
		t.Fatalf("LoadTemplates failed: %v", err)
	}

	if hx.templates == nil {
		t.Fatal("templates should be loaded")
	}
}

func TestRegisterFilter(t *testing.T) {
	hx := New()

	hx.RegisterFilter("uppercase", func(s string) string {
		return strings.ToUpper(s)
	})

	if _, exists := hx.funcMap["uppercase"]; !exists {
		t.Error("Custom filter should be registered")
	}
}

func TestRegisterFilters(t *testing.T) {
	hx := New()

	customFilters := template.FuncMap{
		"reverse": func(s string) string {
			runes := []rune(s)
			for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
				runes[i], runes[j] = runes[j], runes[i]
			}
			return string(runes)
		},
		"repeat": func(s string, n int) string {
			return strings.Repeat(s, n)
		},
	}

	hx.RegisterFilters(customFilters)

	for name := range customFilters {
		if _, exists := hx.funcMap[name]; !exists {
			t.Errorf("Custom filter '%s' should be registered", name)
		}
	}
}

func TestIsHTMXRequest(t *testing.T) {
	tests := []struct {
		name     string
		headers  map[string]string
		expected bool
	}{
		{
			name:     "HTMX request",
			headers:  map[string]string{"HX-Request": "true"},
			expected: true,
		},
		{
			name:     "Non-HTMX request",
			headers:  map[string]string{},
			expected: false,
		},
		{
			name:     "HTMX header false",
			headers:  map[string]string{"HX-Request": "false"},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			for key, value := range tt.headers {
				req.Header.Set(key, value)
			}

			result := isHTMXRequest(req)
			if result != tt.expected {
				t.Errorf("isHTMXRequest() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

func TestIsDevMode(t *testing.T) {
	tests := []struct {
		name     string
		envMode  string
		expected bool
	}{
		{
			name:     "Development mode",
			envMode:  "development",
			expected: true,
		},
		{
			name:     "Production mode",
			envMode:  "production",
			expected: false,
		},
		{
			name:     "No mode set",
			envMode:  "",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original := os.Getenv("ENVIRONMENT")
			defer func() {
				if err := os.Setenv("ENVIRONMENT", original); err != nil {
					t.Fatalf("failed to restore ENVIRONMENT: %v", err)
				}
			}()

			if err := os.Setenv("ENVIRONMENT", tt.envMode); err != nil {
				t.Fatalf("failed to set ENVIRONMENT: %v", err)
			}

			result := isDevMode()
			if result != tt.expected {
				t.Errorf("isDevMode() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

func TestExtractContext(t *testing.T) {
	r := chi.NewRouter()

	var capturedContext map[string]any
	r.Get("/users/{id}", func(w http.ResponseWriter, r *http.Request) {
		capturedContext = extractContext(r)
	})

	req := httptest.NewRequest("GET", "/users/123?page=1&filter=active", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if capturedContext == nil {
		t.Fatal("Context should be extracted")
	}

	if capturedContext["id"] != "123" {
		t.Errorf("Expected id=123, got %v", capturedContext["id"])
	}

	if capturedContext["page"] != "1" {
		t.Errorf("Expected page=1, got %v", capturedContext["page"])
	}

	if capturedContext["filter"] != "active" {
		t.Errorf("Expected filter=active, got %v", capturedContext["filter"])
	}
}

// A query parameter must not replace a route parameter of the same name in
// template data: /users/7?id=99 renders user 7, as c.Param("id") reports.
func TestExtractContextRouteParamBeatsQuery(t *testing.T) {
	r := chi.NewRouter()
	var got map[string]any
	r.Get("/users/{id}", func(w http.ResponseWriter, r *http.Request) {
		got = extractContext(r)
	})
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/users/7?id=99&tab=posts", nil))
	if got["id"] != "7" {
		t.Errorf("id = %v, want the route value 7", got["id"])
	}
	if got["tab"] != "posts" {
		t.Errorf("tab = %v, want posts", got["tab"])
	}
}

func TestTemplateRenderer(t *testing.T) {
	// Map data is merged into the template's root: .Title, not .Result.Title.
	tmpl := template.Must(template.New("test").Parse(`<h1>{{.Title}}</h1><p>ID: {{.id}}</p>`))

	renderer := &TemplateRenderer{
		Template: tmpl,
		Name:     "test",
	}

	data := map[string]any{
		"Title": "Test Page",
	}

	context := map[string]any{
		"id": "123",
	}

	req := httptest.NewRequest("GET", "/test", nil)

	html, err := renderer.Render(data, context, req)
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}

	expected := `<h1>Test Page</h1><p>ID: 123</p>`
	if html != expected {
		t.Errorf("Expected %q, got %q", expected, html)
	}
}

func TestWriteJSON(t *testing.T) {
	w := httptest.NewRecorder()
	data := map[string]any{
		"message": "success",
		"status":  200,
	}

	err := WriteJSON(w, data)
	if err != nil {
		t.Fatalf("WriteJSON failed: %v", err)
	}

	if w.Header().Get("Content-Type") != "application/json" {
		t.Error("Content-Type should be application/json")
	}

	expected := `{"message":"success","status":200}`
	body := strings.TrimSpace(w.Body.String())
	if body != expected {
		t.Errorf("Expected %q, got %q", expected, body)
	}
}

func TestParseForm(t *testing.T) {
	formData := "name=John&age=30&tags=go&tags=web"
	req := httptest.NewRequest("POST", "/", strings.NewReader(formData))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	result, err := ParseForm(req)
	if err != nil {
		t.Fatalf("ParseForm failed: %v", err)
	}

	if result["name"] != "John" {
		t.Errorf("Expected name=John, got %v", result["name"])
	}

	if result["age"] != "30" {
		t.Errorf("Expected age=30, got %v", result["age"])
	}

	// A repeated field comes back as a []string.
	tags, ok := result["tags"].([]string)
	if !ok {
		t.Error("Tags should be an array")
	}

	if len(tags) != 2 || tags[0] != "go" || tags[1] != "web" {
		t.Errorf("Expected tags=[go, web], got %v", tags)
	}
}

func TestParseJSON(t *testing.T) {
	jsonData := `{"name": "John", "age": 30}`
	req := httptest.NewRequest("POST", "/", bytes.NewReader([]byte(jsonData)))
	req.Header.Set("Content-Type", "application/json")

	var result map[string]any
	err := ParseJSON(req, &result)
	if err != nil {
		t.Fatalf("ParseJSON failed: %v", err)
	}

	if result["name"] != "John" {
		t.Errorf("Expected name=John, got %v", result["name"])
	}

	if result["age"].(float64) != 30 {
		t.Errorf("Expected age=30, got %v", result["age"])
	}
}

func TestGetParam(t *testing.T) {
	r := chi.NewRouter()

	var capturedParam string
	r.Get("/users/{id}", func(w http.ResponseWriter, r *http.Request) {
		capturedParam = GetParam(r, "id")
	})

	req := httptest.NewRequest("GET", "/users/123", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if capturedParam != "123" {
		t.Errorf("Expected param=123, got %v", capturedParam)
	}
}

func TestGetQuery(t *testing.T) {
	req := httptest.NewRequest("GET", "/test?page=2&limit=10", nil)

	page := GetQuery(req, "page")
	if page != "2" {
		t.Errorf("Expected page=2, got %v", page)
	}

	limit := GetQuery(req, "limit")
	if limit != "10" {
		t.Errorf("Expected limit=10, got %v", limit)
	}

	missing := GetQuery(req, "missing")
	if missing != "" {
		t.Errorf("Expected empty string for missing param, got %v", missing)
	}
}

func TestGetQueryDefault(t *testing.T) {
	req := httptest.NewRequest("GET", "/test?page=2", nil)

	page := GetQueryDefault(req, "page", "1")
	if page != "2" {
		t.Errorf("Expected page=2, got %v", page)
	}

	limit := GetQueryDefault(req, "limit", "20")
	if limit != "20" {
		t.Errorf("Expected default limit=20, got %v", limit)
	}
}

func TestBuildURL(t *testing.T) {
	params := map[string]string{
		"page":   "2",
		"filter": "active",
	}

	url := BuildURL("/users", params)

	// Parameter order is not fixed.
	if !strings.Contains(url, "page=2") {
		t.Error("URL should contain page=2")
	}

	if !strings.Contains(url, "filter=active") {
		t.Error("URL should contain filter=active")
	}

	if !strings.HasPrefix(url, "/users?") {
		t.Error("URL should start with /users?")
	}
}

func TestHTMXHelpers(t *testing.T) {
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("HX-Request", "true")
	req.Header.Set("HX-Target", "div#content")
	req.Header.Set("HX-Source", "button#submit")

	if !IsHXRequest(req) {
		t.Error("Should detect HTMX request")
	}

	// htmx 4 sends HX-Target and HX-Source as "tagName#id".
	target := GetHXTarget(req)
	if target != "div#content" {
		t.Errorf("Expected target=div#content, got %v", target)
	}

	source := GetHXSource(req)
	if source != "button#submit" {
		t.Errorf("Expected source=button#submit, got %v", source)
	}
}

func TestHTMX4RequestHelpers(t *testing.T) {
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("HX-Request", "true")
	req.Header.Set("HX-Current-URL", "https://example.com/list")
	req.Header.Set("HX-Request-Type", "partial")
	req.Header.Set("HX-Boosted", "true")
	req.Header.Set("HX-History-Restore-Request", "true")

	if got := GetHXCurrentURL(req); got != "https://example.com/list" {
		t.Errorf("GetHXCurrentURL = %q, want https://example.com/list", got)
	}
	if got := GetHXRequestType(req); got != "partial" {
		t.Errorf("GetHXRequestType = %q, want partial", got)
	}
	if !IsHXBoosted(req) {
		t.Error("IsHXBoosted should be true when HX-Boosted=true")
	}
	if !IsHXHistoryRestoreRequest(req) {
		t.Error("IsHXHistoryRestoreRequest should be true when header=true")
	}

	// A nil request reads as empty.
	if GetHXCurrentURL(nil) != "" || GetHXRequestType(nil) != "" {
		t.Error("nil request should return empty strings")
	}
	if IsHXBoosted(nil) || IsHXHistoryRestoreRequest(nil) {
		t.Error("nil request should return false for boolean checks")
	}
}

func TestHTMXResponseHeaders(t *testing.T) {
	w := httptest.NewRecorder()

	SetHXTrigger(w, "refresh")
	if w.Header().Get("HX-Trigger") != "refresh" {
		t.Error("HX-Trigger header not set correctly")
	}

	SetHXPushURL(w, "/new-url")
	if w.Header().Get("HX-Push-Url") != "/new-url" {
		t.Error("HX-Push-Url header not set correctly")
	}

	SetHXRedirect(w, "/redirect")
	if w.Header().Get("HX-Redirect") != "/redirect" {
		t.Error("HX-Redirect header not set correctly")
	}

	SetHXRefresh(w)
	if w.Header().Get("HX-Refresh") != "true" {
		t.Error("HX-Refresh header not set correctly")
	}

	SetHXReplaceURL(w, "/replace")
	if w.Header().Get("HX-Replace-Url") != "/replace" {
		t.Error("HX-Replace-Url header not set correctly")
	}

	SetHXRetarget(w, "#new-target")
	if w.Header().Get("HX-Retarget") != "#new-target" {
		t.Error("HX-Retarget header not set correctly")
	}

	SetHXReswap(w, "innerHTML")
	if w.Header().Get("HX-Reswap") != "innerHTML" {
		t.Error("HX-Reswap header not set correctly")
	}
}
