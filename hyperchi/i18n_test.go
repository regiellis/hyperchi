package hyperchi

import (
	"net/http/httptest"
	"testing"

	"github.com/regiellis/hyperchi/hyperchi/i18n"
)

func TestLocaleMiddlewareRoutes(t *testing.T) {
	t.Parallel()
	app := toolbarApp(t, true)
	bundle, err := i18n.New(i18n.Config{Default: "en", Locales: []string{"en", "fr"},
		Catalogs: map[string]map[string]string{"fr": {"hello": "Bonjour"}}})
	if err != nil {
		t.Fatal(err)
	}
	app.UseGlobal(bundle.Middleware)
	app.Get("/{locale}/hello", func(c *Context) error { return c.Text(bundle.Text(c.Locale(), "hello")) })
	w := httptest.NewRecorder()
	app.ServeHTTP(w, httptest.NewRequest("GET", "/fr/hello", nil))
	if w.Code != 200 || w.Body.String() != "Bonjour" || w.Header().Get("Content-Language") != "fr" {
		t.Fatalf("response: %d %q %v", w.Code, w.Body.String(), w.Header())
	}
}
