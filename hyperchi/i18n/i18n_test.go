package i18n_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/regiellis/hyperchi/hyperchi/i18n"
)

func bundle(t *testing.T) *i18n.Bundle {
	t.Helper()
	b, err := i18n.New(i18n.Config{Default: "en", Locales: []string{"en", "fr", "fr-CA"}, Negotiate: true,
		Catalogs: map[string]map[string]string{"en": {"hello": "Hello", "only": "Fallback"}, "fr": {"hello": "Bonjour"}}})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestResolution(t *testing.T) {
	t.Parallel()
	b := bundle(t)
	for _, tt := range []struct{ name, path, header, want string }{
		{"path wins", "/fr-ca/news", "en", "fr-ca"},
		{"quality", "/news", "fr;q=0.4,en;q=0.8", "en"},
		{"parent", "/news", "fr-CH,en;q=0.5", "fr"},
		{"zero ignored", "/news", "fr;q=0,en;q=0.1", "en"},
		{"invalid quality", "/news", "fr;q=NaN", "en"},
		{"unsupported", "/de/news", "de", "en"},
		{"segment boundary", "/french", "en", "en"},
		{"stable preference", "/", "fr,en", "fr"},
		{"wildcard", "/", "*", "en"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", tt.path, nil)
			r.Header.Set("Accept-Language", tt.header)
			if got := b.Resolve(r); got != tt.want {
				t.Fatalf("got %q want %q", got, tt.want)
			}
		})
	}
}

func TestPaths(t *testing.T) {
	t.Parallel()
	b := bundle(t)
	for _, tt := range []struct{ name, path, want string }{
		{"root", "/", "/fr/"}, {"replace", "/en/news?q=a%20b#top", "/fr/news?q=a%20b#top"},
		{"escape", "/fr-ca/caf%C3%A9/", "/fr/caf%C3%A9/"}, {"bare locale", "/en", "/fr"},
		{"boundary", "/english", "/fr/english"}, {"external", "https://evil.test/", ""},
		{"host", "//evil.test/", ""}, {"dot", "/a/%2e%2e/b", ""},
		{"slash", "/a%2fb", ""}, {"backslash", "/a%5cb", ""}, {"relative", "news", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := b.Path("fr", tt.path)
			if tt.want == "" {
				if err == nil {
					t.Fatalf("accepted %q", tt.path)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("got %q, %v want %q", got, err, tt.want)
			}
		})
	}
	if _, err := b.Path("de", "/"); err == nil {
		t.Fatal("unsupported locale accepted")
	}
}

func TestCatalogIsolationAndFallback(t *testing.T) {
	t.Parallel()
	messages := map[string]string{"hello": "Hello"}
	b, err := i18n.New(i18n.Config{Default: "en", Locales: []string{"en", "fr", "fr-ca"}, Catalogs: map[string]map[string]string{
		"en": messages, "fr": {"parent": "Parent", "empty": ""}, "fr-ca": {"local": "Local"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	messages["hello"] = "Changed"
	locales := b.Locales()
	locales[0] = "de"
	for _, tt := range []struct{ name, key, want string }{
		{"default", "hello", "Hello"}, {"parent", "parent", "Parent"}, {"local", "local", "Local"},
		{"missing", "missing", "missing"}, {"empty", "empty", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := b.Text("fr-ca", tt.key); got != tt.want {
				t.Fatalf("got %q", got)
			}
		})
	}
	if b.Match("en") != "en" {
		t.Fatal("locales leaked")
	}
}

func TestMiddleware(t *testing.T) {
	t.Parallel()
	b := bundle(t)
	for _, path := range []string{"/", "/fr/news"} {
		t.Run(path, func(t *testing.T) {
			r := httptest.NewRequest("GET", path, nil)
			r.Header.Set("Accept-Language", "fr")
			w := httptest.NewRecorder()
			w.Header().Set("Vary", "Accept-Encoding")
			b.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if i18n.Locale(r.Context()) != "fr" {
					t.Error("context locale missing")
				}
				if r.URL.Path != path {
					t.Error("request rewritten")
				}
				w.WriteHeader(204)
			})).ServeHTTP(w, r)
			if w.Header().Get("Content-Language") != "fr" {
				t.Fatal("language missing")
			}
			if len(w.Header().Values("Vary")) != map[bool]int{true: 2, false: 1}[path == "/"] {
				t.Fatal("incorrect vary")
			}
		})
	}
}

func TestInvalidConfig(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name string
		cfg  i18n.Config
	}{
		{"empty", i18n.Config{}}, {"default missing", i18n.Config{Default: "en", Locales: []string{"fr"}}},
		{"duplicate", i18n.Config{Default: "en", Locales: []string{"en", "EN"}}},
		{"bad tag", i18n.Config{Default: "en_US", Locales: []string{"en_US"}}},
		{"catalog", i18n.Config{Default: "en", Locales: []string{"en"}, Catalogs: map[string]map[string]string{"fr": {}}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := i18n.New(tt.cfg); err == nil {
				t.Fatal("accepted invalid config")
			}
		})
	}
}

func ExampleBundle() {
	b, err := i18n.New(i18n.Config{Default: "en", Locales: []string{"en", "fr"},
		Catalogs: map[string]map[string]string{"en": {"hello": "Hello"}, "fr": {"hello": "Bonjour"}}})
	if err != nil {
		panic(err)
	}
	path, err := b.Path("fr", "/en/news?sort=new")
	if err != nil {
		panic(err)
	}
	fmt.Println(path)
	fmt.Println(b.Text("fr-ca", "hello"))
	// Output:
	// /fr/news?sort=new
	// Bonjour
}

func TestNegotiationIsOptIn(t *testing.T) {
	t.Parallel()
	b, err := i18n.New(i18n.Config{Default: "en", Locales: []string{"en", "fr"}})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/news", nil)
	r.Header.Set("Accept-Language", "fr")
	w := httptest.NewRecorder()
	b.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if i18n.Locale(r.Context()) != "en" {
			t.Error("negotiated without opt-in")
		}
	})).ServeHTTP(w, r)
	if w.Header().Get("Vary") != "" {
		t.Fatal("unnecessary language variation")
	}
}
