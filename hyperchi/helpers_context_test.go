package hyperchi

import (
	"html/template"
	"net/http/httptest"
	"testing"

	"github.com/regiellis/hyperchi/hyperchi/helpers"
)

func TestHelperTemplateMaps(t *testing.T) {
	for _, data := range []any{map[string]any{"Title": "<hello>"}, H{"Title": "<hello>"}, helpers.JSON{"Title": "<hello>"}} {
		renderer := &TemplateRenderer{Name: "test", Template: template.Must(template.New("test").Parse(`{{.Title}}`))}
		result, err := renderer.Render(data, nil, httptest.NewRequest("GET", "/", nil))
		if err != nil || result != "&lt;hello&gt;" {
			t.Fatalf("%T: %q %v", data, result, err)
		}
	}
}

func TestContextQueriesAndBufferedJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	c := New().NewContext(rec, httptest.NewRequest("GET", "/?q=&tag=a&tag=b", nil))
	if c.QueryDefault("q", "all") != "all" || c.QueryDefault("missing", "all") != "all" || len(c.Queries("tag")) != 2 {
		t.Fatal("query helpers")
	}
	if err := c.JSON(H{"invalid": make(chan int)}); err == nil {
		t.Fatal("invalid JSON accepted")
	}
	if rec.Body.Len() != 0 || rec.Header().Get("Content-Type") != "" {
		t.Fatal("failed encoding changed response")
	}
	if err := c.JSON(H{"ok": true}); err != nil {
		t.Fatal(err)
	}
	if rec.Body.String() != "{\"ok\":true}\n" {
		t.Fatal(rec.Body.String())
	}
}
