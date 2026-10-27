// Package agentidioms grades a HyperChi project for one thing: whether the code
// an agent wrote uses the framework's API or falls back to plain net/http. It
// reads Go source only (no type checking), so results are deterministic and
// cheap; the agent-eval command runs it over projects agents built from the
// fixed tasks in tasks/.
package agentidioms

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Report counts net/http fallbacks (Violations) and framework calls (Framework)
// in a project's non-test Go files.
type Report struct {
	Dir        string         `json:"dir"`
	Files      int            `json:"files"`
	Violations map[string]int `json:"violations"`
	Framework  map[string]int `json:"framework"`
	Middleware int            `json:"middleware"` // (w, r) functions inside middleware; allowed
	Builds     bool           `json:"builds"`
	BuildError string         `json:"build_error,omitempty"`
}

// Pass reports a project that builds and has no net/http fallbacks.
func (r Report) Pass() bool {
	if !r.Builds {
		return false
	}
	for _, n := range r.Violations {
		if n > 0 {
			return false
		}
	}
	return true
}

// violationNames are the violation kinds, in the order the summary prints them.
var violationNames = []string{
	"stdlib_handler",   // a func(w http.ResponseWriter, r *http.Request) outside middleware
	"http_error",       // http.Error(...)
	"json_encoder",     // json.NewEncoder(...), hand-written JSON responses
	"manual_hx_header", // Header().Set("HX-...", ...)
	"raw_query",        // r.URL.Query()
	"parse_form",       // r.ParseForm() / r.ParseMultipartForm()
	"write_json",       // hyperchi.WriteJSON(...)
	"listen_and_serve", // http.ListenAndServe(...)
	"chi_new_router",   // chi.NewRouter()
}

// ViolationNames lists the violation kinds in summary order.
func ViolationNames() []string { return append([]string(nil), violationNames...) }

// Grade parses every non-test Go file under dir (skipping vendor, node_modules,
// tmp, and dot directories) and counts idioms.
func Grade(dir string) (Report, error) {
	rep := Report{Dir: dir, Violations: map[string]int{}, Framework: map[string]int{}}
	for _, name := range violationNames {
		rep.Violations[name] = 0
	}
	fset := token.NewFileSet()
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if path != dir && (name == "vendor" || name == "node_modules" || name == "tmp" || strings.HasPrefix(name, ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		rep.Files++
		gradeFile(file, &rep)
		return nil
	})
	return rep, err
}

// Build runs go vet over the project and records whether it compiles.
func Build(rep *Report) {
	cmd := exec.Command("go", "vet", "./...")
	cmd.Dir = rep.Dir
	out, err := cmd.CombinedOutput()
	rep.Builds = err == nil
	if err != nil {
		rep.BuildError = strings.TrimSpace(string(out))
	}
}

func gradeFile(file *ast.File, rep *Report) {
	var walk func(n ast.Node, inMiddleware bool)
	walk = func(n ast.Node, inMiddleware bool) {
		ast.Inspect(n, func(node ast.Node) bool {
			switch v := node.(type) {
			case *ast.FuncDecl:
				if v != n {
					walk(v, inMiddleware || takesHandler(v.Type))
					countFunc(v.Type, inMiddleware || takesHandler(v.Type), rep)
					return false
				}
			case *ast.FuncLit:
				if v != n {
					walk(v.Body, inMiddleware || takesHandler(v.Type))
					countFunc(v.Type, inMiddleware || takesHandler(v.Type), rep)
					return false
				}
			case *ast.CallExpr:
				countCall(v, rep)
			}
			return true
		})
	}
	walk(file, false)
}

// countFunc flags a (w http.ResponseWriter, r *http.Request) function; inside
// middleware (a function that takes or returns http.Handler) it is allowed.
func countFunc(ft *ast.FuncType, inMiddleware bool, rep *Report) {
	if isContextHandler(ft) {
		rep.Framework["context_handler"]++
		return
	}
	if !isStdlibHandler(ft) {
		return
	}
	if inMiddleware {
		rep.Middleware++
		return
	}
	rep.Violations["stdlib_handler"]++
}

func isStdlibHandler(ft *ast.FuncType) bool {
	if ft.Params == nil {
		return false
	}
	var types []string
	for _, f := range ft.Params.List {
		n := len(f.Names)
		if n == 0 {
			n = 1
		}
		for i := 0; i < n; i++ {
			types = append(types, exprString(f.Type))
		}
	}
	return len(types) == 2 && types[0] == "http.ResponseWriter" && types[1] == "*http.Request"
}

func isContextHandler(ft *ast.FuncType) bool {
	return ft.Params != nil && len(ft.Params.List) == 1 && exprString(ft.Params.List[0].Type) == "*hyperchi.Context"
}

func takesHandler(ft *ast.FuncType) bool {
	for _, list := range []*ast.FieldList{ft.Params, ft.Results} {
		if list == nil {
			continue
		}
		for _, f := range list.List {
			if exprString(f.Type) == "http.Handler" {
				return true
			}
		}
	}
	return false
}

func countCall(call *ast.CallExpr, rep *Report) {
	fun := call.Fun
	switch g := fun.(type) { // generic instantiation: hyperchi.Bind[T](c)
	case *ast.IndexExpr:
		fun = g.X
	case *ast.IndexListExpr:
		fun = g.X
	}
	sel, ok := fun.(*ast.SelectorExpr)
	if !ok {
		return
	}
	name := sel.Sel.Name
	switch pkgName(sel) + "." + name {
	case "http.Error":
		rep.Violations["http_error"]++
	case "json.NewEncoder":
		rep.Violations["json_encoder"]++
	case "hyperchi.WriteJSON":
		rep.Violations["write_json"]++
	case "http.ListenAndServe", "http.ListenAndServeTLS":
		rep.Violations["listen_and_serve"]++
	case "chi.NewRouter":
		rep.Violations["chi_new_router"]++
	case "hyperchi.Bind", "hyperchi.BindMessage":
		rep.Framework["bind"]++
	case "hyperchi.NotFound", "hyperchi.BadRequest", "hyperchi.Unauthorized", "hyperchi.Forbidden",
		"hyperchi.Conflict", "hyperchi.Unprocessable", "hyperchi.Errorf", "hyperchi.NewHTTPError":
		rep.Framework["http_error_value"]++
	}
	switch name {
	case "Set", "Add":
		if inner, ok := sel.X.(*ast.CallExpr); ok {
			if h, ok := inner.Fun.(*ast.SelectorExpr); ok && h.Sel.Name == "Header" && len(call.Args) > 0 {
				if lit, ok := call.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
					if key, err := strconv.Unquote(lit.Value); err == nil && strings.HasPrefix(strings.ToUpper(key), "HX-") {
						rep.Violations["manual_hx_header"]++
					}
				}
			}
		}
	case "Query":
		if inner, ok := sel.X.(*ast.SelectorExpr); ok && inner.Sel.Name == "URL" {
			rep.Violations["raw_query"]++
		}
	case "ParseForm", "ParseMultipartForm":
		rep.Violations["parse_form"]++
	case "View", "Fragment", "Page", "JSON", "Redirect", "HX", "Swap", "Receive":
		rep.Framework[strings.ToLower(name)]++
	case "WebSocket", "Room":
		rep.Framework["realtime"]++
	}
}

func pkgName(sel *ast.SelectorExpr) string {
	if id, ok := sel.X.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

func exprString(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.StarExpr:
		return "*" + exprString(v.X)
	case *ast.SelectorExpr:
		return exprString(v.X) + "." + v.Sel.Name
	default:
		return fmt.Sprintf("%T", e)
	}
}

// Summary renders reports as a Markdown table, one row per task.
func Summary(rows map[string]Report) string {
	names := make([]string, 0, len(rows))
	for name := range rows {
		names = append(names, name)
	}
	sort.Strings(names)
	var b strings.Builder
	fmt.Fprintf(&b, "| Task | Pass | Builds | Context handlers | %s |\n", strings.Join(violationNames, " | "))
	fmt.Fprintf(&b, "|%s\n", strings.Repeat(" --- |", 4+len(violationNames)))
	for _, name := range names {
		r := rows[name]
		fmt.Fprintf(&b, "| %s | %v | %v | %d |", name, r.Pass(), r.Builds, r.Framework["context_handler"])
		for _, v := range violationNames {
			fmt.Fprintf(&b, " %d |", r.Violations[v])
		}
		b.WriteString("\n")
	}
	return b.String()
}

// WriteFile writes content to path, creating parent directories. The command
// uses it for reports.
func WriteFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}
