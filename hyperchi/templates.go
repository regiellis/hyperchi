package hyperchi

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// LoadTemplates parses the files matching a glob pattern, replacing any loaded
// set. A pattern that matches nothing is an error.
//
// "**" matches at any depth. filepath.Glob has no "**" operator and treats it
// as "*", so "templates/**/*.tmpl" would match one level deep and silently drop
// deeper files; the pattern is expanded by walking the tree instead.
//
// A template is named by its base filename with the extension, as ParseFiles
// names it. Prefer an explicit {{define "name"}} in each file so the name does
// not depend on where the file lives; Template describes how a name is
// resolved at render time.
func (g *HyperChi) LoadTemplates(pattern string) error {
	files, err := resolveTemplatePattern(pattern)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("hyperchi: no templates matched %q", pattern)
	}

	tmpl, err := template.New("").Funcs(g.funcMap).ParseFiles(files...)
	if err != nil {
		return err
	}
	g.templates = tmpl
	g.templatesLoaded = true
	return nil
}

// LoadTemplatesFS loads templates from fsys, for apps that embed their
// templates and so do not depend on the working directory. pattern uses
// slash-separated fs.FS paths and follows LoadTemplates: "**" matches at any
// depth, nothing matching is an error, and a template without a define is
// named by its base filename.
func (g *HyperChi) LoadTemplatesFS(fsys fs.FS, pattern string) error {
	files, err := resolveTemplatePatternFS(fsys, pattern)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("hyperchi: no templates matched %q", pattern)
	}
	tmpl := template.New("").Funcs(g.funcMap)
	for _, file := range files {
		src, err := fs.ReadFile(fsys, file)
		if err != nil {
			return fmt.Errorf("hyperchi: read template %s: %w", file, err)
		}
		if _, err := tmpl.New(path.Base(file)).Parse(string(src)); err != nil {
			return fmt.Errorf("hyperchi: parse template %s: %w", file, err)
		}
	}
	g.templates = tmpl
	g.templatesLoaded = true
	return nil
}

// resolveTemplatePatternFS is resolveTemplatePattern for an fs.FS.
func resolveTemplatePatternFS(fsys fs.FS, pattern string) ([]string, error) {
	if !strings.Contains(pattern, "**") {
		files, err := fs.Glob(fsys, pattern)
		if err != nil {
			return nil, err
		}
		sort.Strings(files)
		return files, nil
	}
	prefix, rest, _ := strings.Cut(pattern, "**")
	root := path.Dir(prefix)
	if root == "" {
		root = "."
	}
	suffix := strings.TrimPrefix(rest, "/")
	if suffix == "" {
		suffix = "*"
	}
	var files []string
	err := fs.WalkDir(fsys, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		ok, matchErr := path.Match(suffix, path.Base(p))
		if matchErr != nil {
			return matchErr
		}
		if ok {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("hyperchi: walking %q for pattern %q: %w", root, pattern, err)
	}
	sort.Strings(files)
	return files, nil
}

// resolveTemplatePattern expands a glob into a file list, adding the recursive
// "**" that filepath.Glob lacks.
//
// The list is sorted so parsing is deterministic: with ParseFiles, the last of
// two files sharing a base name wins, and an unstable order would change the
// winner between builds.
func resolveTemplatePattern(pattern string) ([]string, error) {
	if !strings.Contains(pattern, "**") {
		files, err := filepath.Glob(pattern)
		if err != nil {
			return nil, err
		}
		sort.Strings(files)
		return files, nil
	}

	// Split around the first "**" so the walk is rooted at the fixed prefix
	// and the remainder becomes the per-file match.
	prefix, rest, _ := strings.Cut(pattern, "**")
	root := filepath.Dir(prefix)
	if root == "" || root == "." {
		root = "."
	}
	suffix := strings.TrimPrefix(rest, string(filepath.Separator))
	if suffix == "" {
		suffix = "*"
	}

	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		// Match the suffix against the base name so "**/*.tmpl" means "any
		// .tmpl at any depth", which is what people write it expecting.
		ok, matchErr := filepath.Match(suffix, filepath.Base(path))
		if matchErr != nil {
			return matchErr
		}
		if ok {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("hyperchi: walking %q for pattern %q: %w", root, pattern, err)
	}

	sort.Strings(files)
	return files, nil
}

// autoDiscoverTemplate looks for name with a template extension in templates/,
// views/, and the working directory, and parses every match under the bare
// name. It does nothing once templates were loaded explicitly, and its error
// lists every path it tried.
func (g *HyperChi) autoDiscoverTemplate(name string) error {
	if g.templatesLoaded {
		return nil
	}

	searchDirs := []string{
		"templates",
		"views",
		".",
	}

	extensions := []string{
		".html",
		".tmpl",
		".htm",
		".md",
		".mdx",
	}

	baseName := name
	for _, ext := range extensions {
		if strings.HasSuffix(name, ext) {
			baseName = strings.TrimSuffix(name, ext)
			break
		}
	}

	var foundFiles []string
	for _, dir := range searchDirs {
		for _, ext := range extensions {
			path := filepath.Join(dir, baseName+ext)
			if _, err := os.Stat(path); err == nil {
				foundFiles = append(foundFiles, path)
			}
		}
	}

	if len(foundFiles) == 0 {
		searchPaths := []string{}
		for _, dir := range searchDirs {
			for _, ext := range extensions {
				searchPaths = append(searchPaths, filepath.Join(dir, baseName+ext))
			}
		}
		return fmt.Errorf("template '%s' not found. Searched:\n  - %s\n\nCall app.LoadTemplates() to load templates from a custom location",
			name, strings.Join(searchPaths, "\n  - "))
	}

	if g.templates == nil {
		g.templates = template.New("").Funcs(g.funcMap)
	}

	for _, file := range foundFiles {
		content, err := os.ReadFile(file)
		if err != nil {
			return fmt.Errorf("failed to read template %s: %w", file, err)
		}

		_, err = g.templates.New(baseName).Parse(string(content))
		if err != nil {
			return fmt.Errorf("failed to parse template %s: %w", file, err)
		}
	}

	return nil
}

// LoadTemplatesWithLayout loads templates from the working directory by role,
// in this order: layouts/ as "layout-<name>", components/ as
// "component-<name>", and fragments/ as "fragment-<name>" (each looked up
// under templates/, views/, and the root), then every other .html, .tmpl,
// .htm, .md, and .mdx file under templates/, views/, and pages/ by its bare
// name, then the JSON files in data/ into app.Data.
func (g *HyperChi) LoadTemplatesWithLayout() error {
	return g.discoverTemplatesStructure()
}

// discoverTemplatesStructure implements LoadTemplatesWithLayout.
func (g *HyperChi) discoverTemplatesStructure() error {
	if g.templates == nil {
		g.templates = template.New("").Funcs(g.funcMap)
	}

	if err := g.loadLayoutTemplates(); err != nil {
		return fmt.Errorf("failed to load layout templates: %w", err)
	}

	if err := g.loadComponentTemplates(); err != nil {
		return fmt.Errorf("failed to load component templates: %w", err)
	}

	if err := g.loadFragmentTemplates(); err != nil {
		return fmt.Errorf("failed to load fragment templates: %w", err)
	}

	if err := g.loadPageTemplates(); err != nil {
		return fmt.Errorf("failed to load page templates: %w", err)
	}

	if err := g.loadDataFiles(); err != nil {
		return fmt.Errorf("failed to load data files: %w", err)
	}

	g.templatesLoaded = true
	return nil
}

// loadLayoutTemplates loads templates/layouts, views/layouts, and layouts.
func (g *HyperChi) loadLayoutTemplates() error {
	layoutDirs := []string{
		"templates/layouts",
		"views/layouts",
		"layouts",
	}

	extensions := []string{".html", ".tmpl", ".htm"}

	for _, dir := range layoutDirs {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			continue
		}

		for _, ext := range extensions {
			pattern := filepath.Join(dir, "*"+ext)
			matches, err := filepath.Glob(pattern)
			if err != nil {
				continue
			}

			for _, file := range matches {
				if err := g.loadTemplateFile(file, "layout"); err != nil {
					return err
				}
			}
		}
	}

	return nil
}

// loadComponentTemplates loads templates/components, views/components, and
// components.
func (g *HyperChi) loadComponentTemplates() error {
	componentDirs := []string{
		"templates/components",
		"views/components",
		"components",
	}

	extensions := []string{".html", ".tmpl", ".htm"}

	for _, dir := range componentDirs {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			continue
		}

		for _, ext := range extensions {
			pattern := filepath.Join(dir, "*"+ext)
			matches, err := filepath.Glob(pattern)
			if err != nil {
				continue
			}

			for _, file := range matches {
				if err := g.loadTemplateFile(file, "component"); err != nil {
					return err
				}
			}
		}
	}

	return nil
}

// loadFragmentTemplates loads templates/fragments, views/fragments, and
// fragments.
func (g *HyperChi) loadFragmentTemplates() error {
	fragmentDirs := []string{
		"templates/fragments",
		"views/fragments",
		"fragments",
	}

	extensions := []string{".html", ".tmpl", ".htm"}

	for _, dir := range fragmentDirs {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			continue
		}

		for _, ext := range extensions {
			pattern := filepath.Join(dir, "*"+ext)
			matches, err := filepath.Glob(pattern)
			if err != nil {
				continue
			}

			for _, file := range matches {
				if err := g.loadTemplateFile(file, "fragment"); err != nil {
					return err
				}
			}
		}
	}

	return nil
}

// loadPageTemplates loads every template file under templates/, views/, and
// pages/, skipping the layouts, components, and fragments directories that the
// other loaders own. Unreadable entries are skipped.
func (g *HyperChi) loadPageTemplates() error {
	pageDirs := []string{
		"templates",
		"views",
		"pages",
	}

	extensions := []string{".html", ".tmpl", ".htm", ".md", ".mdx"}

	for _, dir := range pageDirs {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			continue
		}

		err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}

			if info.IsDir() {
				if strings.HasSuffix(path, "layouts") || strings.HasSuffix(path, "components") || strings.HasSuffix(path, "fragments") {
					return filepath.SkipDir
				}
				return nil
			}

			for _, ext := range extensions {
				if strings.HasSuffix(path, ext) {
					return g.loadTemplateFile(path, "page")
				}
			}

			return nil
		})

		if err != nil {
			return err
		}
	}

	return nil
}

// loadTemplateFile parses one file under the name generateTemplateName gives
// it.
func (g *HyperChi) loadTemplateFile(filePath, templateType string) error {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read template %s: %w", filePath, err)
	}

	templateName := g.generateTemplateName(filePath, templateType)

	_, err = g.templates.New(templateName).Parse(string(content))
	if err != nil {
		return fmt.Errorf("failed to parse template %s: %w", filePath, err)
	}

	return nil
}

// generateTemplateName names a file by its base name without the extension,
// prefixed with "layout-", "component-", or "fragment-" for those roles.
func (g *HyperChi) generateTemplateName(filePath, templateType string) string {
	base := strings.TrimSuffix(filepath.Base(filePath), filepath.Ext(filePath))

	switch templateType {
	case "layout":
		return "layout-" + base
	case "component":
		return "component-" + base
	case "fragment":
		return "fragment-" + base
	default:
		return base
	}
}

// loadDataFiles loads the data/ directory's JSON files into g.Data (see
// loadDataFile). A missing directory is not an error.
func (g *HyperChi) loadDataFiles() error {
	dataDir := "data"
	if _, err := os.Stat(dataDir); os.IsNotExist(err) {
		return nil
	}

	if g.Data == nil {
		g.Data = make(map[string]any)
	}

	extensions := []string{".json", ".yaml", ".yml"}

	for _, ext := range extensions {
		pattern := filepath.Join(dataDir, "*"+ext)
		matches, err := filepath.Glob(pattern)
		if err != nil {
			continue
		}

		for _, file := range matches {
			if err := g.loadDataFile(file); err != nil {
				return err
			}
		}
	}

	return nil
}

// loadDataFile stores a JSON file's contents in g.Data under its base name.
// YAML files are read and skipped: there is no YAML parser.
func (g *HyperChi) loadDataFile(filePath string) error {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read data file %s: %w", filePath, err)
	}

	base := strings.TrimSuffix(filepath.Base(filePath), filepath.Ext(filePath))

	var data any
	ext := filepath.Ext(filePath)

	switch ext {
	case ".json":
		if err := json.Unmarshal(content, &data); err != nil {
			return fmt.Errorf("failed to parse JSON file %s: %w", filePath, err)
		}
	case ".yaml", ".yml":
		return nil
	}

	g.Data[base] = data
	return nil
}

// RegisterFilter adds a template function. Register filters before
// LoadTemplates; a later call re-parses the loaded set so it can use the new
// function. Call it during setup only: it is not safe while requests render.
func (g *HyperChi) RegisterFilter(name string, fn any) {
	g.funcMap[name] = fn
	if g.templatesLoaded && g.templates != nil {
		g.reloadTemplatesWithFuncs()
	}
}

// RegisterFilters adds several template functions, with the rules of
// RegisterFilter.
func (g *HyperChi) RegisterFilters(funcMap template.FuncMap) {
	for name, fn := range funcMap {
		g.funcMap[name] = fn
	}
	if g.templatesLoaded && g.templates != nil {
		g.reloadTemplatesWithFuncs()
	}
}

// reloadTemplatesWithFuncs rebuilds the template set with the current
// funcMap, because a function must be known when a template is parsed. A
// template that fails to re-parse is logged and left out.
func (g *HyperChi) reloadTemplatesWithFuncs() {
	oldTemplates := g.templates
	newTemplates := template.New("").Funcs(g.funcMap)
	for _, t := range oldTemplates.Templates() {
		if t.Name() == "" {
			continue
		}
		if t.Tree == nil || t.Tree.Root == nil {
			continue
		}
		clone, err := newTemplates.New(t.Name()).Parse(t.Tree.Root.String())
		if err != nil {
			log.Printf("hyperchi: re-parse template %q with new filters: %v", t.Name(), err)
			if clone != nil {
				continue
			}
		}
	}
	g.templates = newTemplates
}
