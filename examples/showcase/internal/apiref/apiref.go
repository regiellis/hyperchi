// Package apiref builds the documentation's API reference from Go source: every
// exported function, method, and type in chosen files, with its signature and
// the first sentence of its doc comment. The showcase embeds the result, so the
// deployed site needs no framework source; a test regenerates it to catch drift.
package apiref

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/doc"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Entry is one exported declaration.
type Entry struct {
	Name      string `json:"name"`      // "Bind", "Context.Param", "Str.Upper"
	Signature string `json:"signature"` // the declaration without its body
	Summary   string `json:"summary"`   // first sentence of the doc comment
}

// Group is one category of the reference.
type Group struct {
	Name    string  `json:"name"`
	Import  string  `json:"import"`
	Entries []Entry `json:"entries"`
}

// Source names a package directory and the category of each of its files.
// Files absent from Categories are an error, so a new file cannot be left out.
type Source struct {
	Dir        string            // relative to the repository root
	Import     string            // the import path readers write
	Categories map[string]string // file name -> category
	Order      []string          // category order on the page
	Skip       []string          // files that hold no reader-facing API
}

// Sources is the helpers reference: the Context's request and response helpers,
// then the helpers package.
var Sources = []Source{
	{
		Dir:    "hyperchi",
		Import: "github.com/regiellis/hyperchi/hyperchi",
		Categories: map[string]string{
			"context.go":   "Request (Context)",
			"respond.go":   "Responses (Context)",
			"bind.go":      "Input binding",
			"httperror.go": "Errors and statuses",
		},
		Order: []string{"Request (Context)", "Responses (Context)", "Input binding", "Errors and statuses"},
	},
	{
		Dir:    "hyperchi/helpers",
		Import: "github.com/regiellis/hyperchi/hyperchi/helpers",
		Categories: map[string]string{
			"strings.go":     "Strings",
			"collections.go": "Collections",
			"json_http.go":   "JSON and HTTP client",
			"errors.go":      "Error handling",
			"pointers.go":    "Pointers and values",
			"files.go":       "Files",
			"env.go":         "Environment",
			"logging.go":     "Logging",
			"profiler.go":    "Profiler",
			"air.go":         "Live reload (Air)",
			"deployment.go":  "Deployment",
		},
		Order: []string{"Strings", "Collections", "JSON and HTTP client", "Error handling", "Pointers and values", "Files", "Environment", "Logging", "Profiler", "Live reload (Air)", "Deployment"},
	},
}

// Build reads every source under root and returns the reference in page order.
func Build(root string) ([]Group, error) {
	var groups []Group
	for _, src := range Sources {
		g, err := buildSource(root, src)
		if err != nil {
			return nil, err
		}
		groups = append(groups, g...)
	}
	return groups, nil
}

func buildSource(root string, src Source) ([]Group, error) {
	dir := filepath.Join(root, src.Dir)
	fset := token.NewFileSet()
	files, err := parseFiles(fset, dir)
	if err != nil {
		return nil, err
	}
	byCategory := map[string][]Entry{}
	for name, file := range files {
		category, ok := src.Categories[name]
		if !ok {
			if src.Dir == "hyperchi/helpers" {
				return nil, fmt.Errorf("apiref: %s/%s has no category; add it to Sources", src.Dir, name)
			}
			continue
		}
		entries, err := fileEntries(fset, file)
		if err != nil {
			return nil, fmt.Errorf("apiref: %s/%s: %w", src.Dir, name, err)
		}
		byCategory[category] = append(byCategory[category], entries...)
	}
	var groups []Group
	for _, category := range src.Order {
		entries := byCategory[category]
		if len(entries) == 0 {
			return nil, fmt.Errorf("apiref: category %q in %s is empty", category, src.Dir)
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
		groups = append(groups, Group{Name: category, Import: src.Import, Entries: entries})
	}
	return groups, nil
}

// parseFiles parses the non-test Go files of dir, keyed by file name.
func parseFiles(fset *token.FileSet, dir string) (map[string]*ast.File, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		return nil, err
	}
	files := map[string]*ast.File{}
	for _, path := range matches {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return nil, err
		}
		files[filepath.Base(path)] = file
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("apiref: no Go files in %s", dir)
	}
	return files, nil
}

// fileEntries returns the exported functions, methods on exported types, and
// exported types declared in file.
func fileEntries(fset *token.FileSet, file *ast.File) ([]Entry, error) {
	var entries []Entry
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if !d.Name.IsExported() {
				continue
			}
			name := d.Name.Name
			if d.Recv != nil {
				recv := receiverType(d.Recv)
				if recv == "" || !ast.IsExported(recv) {
					continue
				}
				name = recv + "." + name
			}
			sig, err := render(fset, &ast.FuncDecl{Recv: d.Recv, Name: d.Name, Type: d.Type})
			if err != nil {
				return nil, err
			}
			entries = append(entries, Entry{Name: name, Signature: sig, Summary: summary(d.Doc)})
		case *ast.GenDecl:
			if d.Tok != token.TYPE {
				continue
			}
			for _, spec := range d.Specs {
				ts := spec.(*ast.TypeSpec)
				if !ts.Name.IsExported() {
					continue
				}
				comment := ts.Doc
				if comment == nil && len(d.Specs) == 1 {
					comment = d.Doc
				}
				entries = append(entries, Entry{Name: ts.Name.Name, Signature: typeHeader(fset, ts), Summary: summary(comment)})
			}
		}
	}
	return entries, nil
}

func receiverType(recv *ast.FieldList) string {
	if recv == nil || len(recv.List) == 0 {
		return ""
	}
	expr := recv.List[0].Type
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.IndexExpr:
		if id, ok := t.X.(*ast.Ident); ok {
			return id.Name
		}
	case *ast.IndexListExpr:
		if id, ok := t.X.(*ast.Ident); ok {
			return id.Name
		}
	}
	return ""
}

// typeHeader renders "type Name underlying" for simple types and
// "type Name struct" or "type Name interface" for composite ones, so the
// reference stays one line per entry.
func typeHeader(fset *token.FileSet, ts *ast.TypeSpec) string {
	params := ""
	if ts.TypeParams != nil {
		if s, err := render(fset, ts.TypeParams); err == nil {
			params = "[" + strings.TrimSuffix(strings.TrimPrefix(s, "("), ")") + "]"
		}
	}
	assign := ""
	if ts.Assign.IsValid() {
		assign = "= "
	}
	switch ts.Type.(type) {
	case *ast.StructType:
		return "type " + ts.Name.Name + params + " " + assign + "struct"
	case *ast.InterfaceType:
		return "type " + ts.Name.Name + params + " " + assign + "interface"
	}
	underlying, err := render(fset, ts.Type)
	if err != nil {
		return "type " + ts.Name.Name + params
	}
	return "type " + ts.Name.Name + params + " " + assign + underlying
}

func render(fset *token.FileSet, node any) (string, error) {
	var buf bytes.Buffer
	if err := (&printer.Config{Mode: printer.RawFormat}).Fprint(&buf, fset, node); err != nil {
		return "", err
	}
	return strings.Join(strings.Fields(buf.String()), " "), nil
}

var synopsis = new(doc.Package)

func summary(cg *ast.CommentGroup) string {
	if cg == nil {
		return ""
	}
	return synopsis.Synopsis(cg.Text())
}

// Encode renders groups as the indented JSON the showcase embeds.
func Encode(groups []Group) ([]byte, error) {
	out, err := json.MarshalIndent(groups, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// WriteFile builds the reference from root and writes it to path.
func WriteFile(root, path string) error {
	groups, err := Build(root)
	if err != nil {
		return err
	}
	out, err := Encode(groups)
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o644)
}
