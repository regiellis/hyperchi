package agentidioms

import (
	"os"
	"path/filepath"
	"testing"
)

func writeGo(t *testing.T, dir, name, src string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestGradeFrameworkCodePasses(t *testing.T) {
	dir := t.TempDir()
	writeGo(t, dir, "main.go", `package main

import (
	"net/http"

	"github.com/regiellis/hyperchi/hyperchi"
)

type signup struct {
	Email string `+"`form:\"email\" validate:\"required,email\"`"+`
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { next.ServeHTTP(w, r) })
}

func routes(app *hyperchi.HyperChi) {
	app.Use(logRequests)
	app.Get("/", func(c *hyperchi.Context) error { return c.View("index", nil) })
	app.Post("/signup", func(c *hyperchi.Context) error {
		in, err := hyperchi.Bind[signup](c)
		if err != nil {
			return err
		}
		return c.HX().Trigger("done").Fragment("thanks", in)
	})
	app.Get("/api/{id}", func(c *hyperchi.Context) error { return hyperchi.NotFound("no") })
}
`)
	rep, err := Grade(dir)
	if err != nil {
		t.Fatal(err)
	}
	rep.Builds = true // build checks need a module; covered by the command
	if !rep.Pass() {
		t.Fatalf("framework code failed: %+v", rep.Violations)
	}
	if rep.Framework["context_handler"] != 3 || rep.Framework["bind"] != 1 || rep.Framework["http_error_value"] != 1 || rep.Middleware != 1 {
		t.Fatalf("framework counts: %+v middleware=%d", rep.Framework, rep.Middleware)
	}
}

func TestGradeFlagsNetHTTPFallbacks(t *testing.T) {
	dir := t.TempDir()
	writeGo(t, dir, "main.go", `package main

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/regiellis/hyperchi/hyperchi"
)

func search(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if q == "" {
		http.Error(w, "missing", http.StatusBadRequest)
		return
	}
	w.Header().Set("HX-Trigger", "searched")
	json.NewEncoder(w).Encode(map[string]string{"q": q})
}

func page(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		return
	}
	hyperchi.WriteJSON(w, hyperchi.H{"title": "x"})
}

func main() {
	r := chi.NewRouter()
	r.Get("/search", search)
	r.Get("/", page)
	http.ListenAndServe(":8080", r)
}
`)
	rep, err := Grade(dir)
	if err != nil {
		t.Fatal(err)
	}
	rep.Builds = true
	want := map[string]int{
		"stdlib_handler": 2, "http_error": 1, "json_encoder": 1, "manual_hx_header": 1, "raw_query": 1,
		"parse_form": 1, "write_json": 1, "listen_and_serve": 1, "chi_new_router": 1,
	}
	for k, n := range want {
		if rep.Violations[k] != n {
			t.Errorf("%s = %d, want %d", k, rep.Violations[k], n)
		}
	}
	if rep.Pass() {
		t.Fatal("net/http code passed")
	}
}

func TestGradeSkipsVendorAndTests(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "vendor", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	bad := "package x\nimport \"net/http\"\nfunc h(w http.ResponseWriter, r *http.Request) { http.Error(w, \"x\", 500) }\n"
	writeGo(t, filepath.Join(dir, "vendor", "x"), "x.go", bad)
	writeGo(t, dir, "main_test.go", "package main\n"+bad[len("package x\n"):])
	rep, err := Grade(dir)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Files != 0 || rep.Violations["stdlib_handler"] != 0 {
		t.Fatalf("graded skipped files: %+v", rep)
	}
}
