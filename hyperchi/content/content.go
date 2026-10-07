// Package content loads Markdown, MDX, HTML, and text files into typed,
// queryable collections for HyperChi applications.
//
// Each immediate subdirectory of the base directory is a collection, and every
// supported file under it (.md, .mdx, .html, .htm, .txt) becomes a Content whose
// front matter lands in Content.Data. A collection may declare a ContentSchema
// that checks types and constraints, fills defaults, and transforms loaded
// files. Collections answer filter, search, sort, pagination, and field
// selection queries through Collection.Query.
//
//	layer := content.NewContentLayer(&content.ContentConfig{BaseDir: "content"})
//	layer.DefineCollection(content.CollectionConfig{Name: "blog", Schema: content.BlogPostSchema()})
//	if err := layer.LoadContent(); err != nil {
//		log.Fatal(err)
//	}
//	defer layer.Close()
//	blog, _ := layer.GetCollection("blog")
//	page, err := blog.Query(content.ContentQuery{
//		Where: map[string]any{"published": true},
//		Sort:  []content.SortCriteria{{Field: "publishedAt", Order: "desc"}},
//		Limit: 10,
//	})
//
// With ContentConfig.WatchForChanges set, LoadContent also starts a polling
// watcher that reloads the layer when files change; Close stops it.
// ContentConfig.FS reads an fs.FS (a go:embed directory) instead of the disk,
// and ContentConfig.Markdown swaps in a full Markdown renderer.
//
// In a HyperChi app, app.Content.LoadDir and LoadFS run this loader and serve
// the result through app.Content and ContentRoutes, reloading in development.
package content

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// ContentType is a content file format, named by its extension.
type ContentType string

// The content types the loader recognizes. .htm files load as ContentTypeHTML.
const (
	ContentTypeMarkdown ContentType = "md"
	ContentTypeMDX      ContentType = "mdx"
	ContentTypeHTML     ContentType = "html"
	ContentTypeText     ContentType = "txt"
)

// Content is one loaded file. The loader fills the fixed fields; front matter
// lands in Data.
type Content struct {
	// ID is the file's path below the base directory, lowercased, without its
	// extension, with separators, spaces, and underscores turned into dashes.
	// Slug is the base filename without its extension. HTML is Body rendered
	// for its type. CreatedAt and UpdatedAt are both the file's modification
	// time.
	ID         string        `json:"id"`
	Slug       string        `json:"slug"`
	Collection string        `json:"collection"`
	Path       string        `json:"path"`
	Body       string        `json:"body"`
	HTML       template.HTML `json:"html"`
	CreatedAt  time.Time     `json:"createdAt"`
	UpdatedAt  time.Time     `json:"updatedAt"`

	// Data holds the front matter fields as bool, int, float64, []string, or
	// string values.
	Data map[string]any `json:"data"`
}

// ContentSchema describes the front matter a collection expects. AddContent
// checks Required and Fields, fills defaults, runs Validators, then applies
// Transformer. Build one with NewSchema.
type ContentSchema struct {
	Name        string
	Fields      map[string]FieldDefinition
	Required    []string
	Validators  []ValidationFunc
	Transformer TransformFunc
}

// FieldDefinition describes one front matter field. Constraints holds the
// keys minLength, maxLength (characters or elements), min, and max (numbers).
type FieldDefinition struct {
	Type        FieldType
	Required    bool
	Default     any
	Validator   func(any) error
	Description string
	Constraints map[string]any
}

// FieldType is the type a FieldDefinition requires of its value.
type FieldType string

// The field types a schema can require. FieldTypeFloat accepts integers, and
// FieldTypeTime accepts a date string, which validation converts to time.Time.
const (
	FieldTypeString FieldType = "string"
	FieldTypeInt    FieldType = "int"
	FieldTypeFloat  FieldType = "float"
	FieldTypeBool   FieldType = "bool"
	FieldTypeTime   FieldType = "time"
	FieldTypeSlice  FieldType = "slice"
	FieldTypeMap    FieldType = "map"
	FieldTypeAny    FieldType = "any"
)

// ValidationFunc checks a whole Content after its fields pass. Its error is
// returned as is.
type ValidationFunc func(*Content) error

// TransformFunc rewrites a Content after validation. The returned Content is
// the one stored.
type TransformFunc func(*Content) *Content

// Collection is a named set of Content keyed by ID, with an optional schema.
// It has no lock: mutate it only before sharing it. ContentLayer never mutates
// a Collection it has handed out; a reload installs a new one.
type Collection struct {
	Name     string              `json:"name"`
	Path     string              `json:"path"`
	Schema   *ContentSchema      `json:"schema"`
	Type     ContentType         `json:"type"`
	Contents map[string]*Content `json:"contents"`
}

// CollectionConfig declares a collection for NewCollection or
// ContentLayer.DefineCollection.
type CollectionConfig struct {
	Name   string
	Path   string
	Schema *ContentSchema
	Type   ContentType
}

// ContentQuery selects, orders, and pages a collection's content.
//
// Where matches each field by equality, or for a string pattern, by a
// case-insensitive "*x*" (contains), "*x" (ends with), or "x*" (starts with);
// a []any or []string value matches a slice field sharing any element. Search
// is a case-insensitive substring match over Body and string fields. Sort
// compares numbers, times, and strings; the fields id, slug, path, createdAt,
// and updatedAt read the Content fields. Limit 0 returns every match. Select
// keeps only the named Data fields (plus "body" and "html").
type ContentQuery struct {
	Collection string         `json:"collection"`
	Where      map[string]any `json:"where"`
	Sort       []SortCriteria `json:"sort"`
	Limit      int            `json:"limit"`
	Offset     int            `json:"offset"`
	Select     []string       `json:"select"`
	Search     string         `json:"search"`
}

// SortCriteria orders by one field. Order is "desc" for descending; anything
// else sorts ascending.
type SortCriteria struct {
	Field string `json:"field"`
	Order string `json:"order"` // "asc" or "desc"
}

// ContentResult is one page of a Query.
type ContentResult struct {
	Contents   []*Content `json:"contents"`
	Total      int        `json:"total"`
	Page       int        `json:"page"`
	PageSize   int        `json:"pageSize"`
	TotalPages int        `json:"totalPages"`
	HasNext    bool       `json:"hasNext"`
	HasPrev    bool       `json:"hasPrev"`
}

// ContentLayer holds the collections of one content directory. Its methods are
// safe for concurrent use: a reload swaps whole collections in under a lock and
// never mutates a *Collection that a caller already holds, so fetch collections
// through GetCollection when you need the current state.
type ContentLayer struct {
	mu          sync.RWMutex
	collections map[string]*Collection
	declared    map[string]bool // names registered through DefineCollection
	fromDisk    map[string]bool // names the last load populated from files
	loader      *ContentLoader
	config      *ContentConfig
	reloadMu    sync.Mutex // serializes whole reloads (read files, then swap)

	watchCancel context.CancelFunc
	watchDone   chan struct{}
}

// DefaultWatchInterval is how often the content watcher polls when
// ContentConfig.WatchInterval is zero.
const DefaultWatchInterval = 2 * time.Second

// ContentConfig configures a ContentLayer.
type ContentConfig struct {
	// BaseDir is the directory whose subdirectories are collections.
	BaseDir string
	// WatchForChanges makes LoadContent start a polling watcher that reloads
	// the layer when content files change. Stop it with ContentLayer.Close.
	WatchForChanges bool
	// WatchInterval is the watcher's polling period (DefaultWatchInterval when zero).
	WatchInterval time.Duration
	// OnReload, when set, is called from the watcher goroutine after each pass
	// that reloaded content or failed. changed lists the content files that
	// triggered the pass, and err joins scan and reload errors. When it is nil,
	// errors go to slog.Default(). It must not call Close.
	OnReload func(changed []string, err error)
	// FS, when set, is read instead of the disk: BaseDir is then a
	// slash-separated directory inside it ("" or "." for its root), and
	// Content.Path and Collection.Path are paths inside FS. Use it for content
	// embedded with go:embed. WatchForChanges applies only to BaseDir on disk,
	// so a layer reading an FS never starts a watcher.
	FS fs.FS
	// Markdown, when set, renders the body of .md and .mdx files (front matter
	// already removed) in place of the built-in subset, for example through
	// modules/markdown. Its output is trusted as HTML. A file it fails on is
	// kept with the error in Data["_error"], like a file that cannot be read.
	Markdown MarkdownFunc
}

// MarkdownFunc renders a Markdown body to HTML; see ContentConfig.Markdown.
type MarkdownFunc func(body string) (template.HTML, error)

// DefaultContentConfig returns the configuration NewContentLayer uses for a
// nil config: BaseDir "content" with watching on.
func DefaultContentConfig() *ContentConfig {
	return &ContentConfig{
		BaseDir:         "content",
		WatchForChanges: true,
	}
}

// ContentError reports a content failure. ValidateSchema returns one with
// Type "validation" and Path set to the file.
type ContentError struct {
	Type    string
	Message string
	Path    string
	Line    int
	Column  int
	Err     error
}

// Error formats the error as "<Type> error in <Path>: <Message>", leaving out
// the path when it is empty.
func (e *ContentError) Error() string {
	if e.Path != "" {
		return e.Type + " error in " + e.Path + ": " + e.Message
	}
	return e.Type + " error: " + e.Message
}

// ContentEvent describes a change to a content file. Nothing in this package
// produces one; it is a shape for applications that publish change events.
type ContentEvent struct {
	Type       string    `json:"type"` // "created", "updated", "deleted"
	Path       string    `json:"path"`
	Collection string    `json:"collection"`
	ContentID  string    `json:"contentId"`
	Timestamp  time.Time `json:"timestamp"`
}

// ContentStats summarizes a collection; see Collection.GetStats.
type ContentStats struct {
	TotalContent int            `json:"totalContent"`
	Collections  int            `json:"collections"`
	CustomStats  map[string]int `json:"customStats"` // <field>_count and <field>_max_count per schema field
	LastUpdated  time.Time      `json:"lastUpdated"`
}

// ContentIndex is a search index entry for content. Nothing in this package
// builds one; it is a shape for applications that keep their own index.
type ContentIndex struct {
	ID         string         `json:"id"`
	Collection string         `json:"collection"`
	Fields     map[string]any `json:"fields"` // Flexible indexing
	CreatedAt  time.Time      `json:"createdAt"`
	UpdatedAt  time.Time      `json:"updatedAt"`
}

// ContentLoader reads content files from a base directory on disk, or from a
// root inside an fs.FS.
type ContentLoader struct {
	baseDir  string // empty (with no fsys) means LoadAll returns no collections
	fsys     fs.FS  // when set, baseDir is a slash-separated root inside it
	markdown MarkdownFunc
}

// source returns the file system to walk and the root inside it. ok is false
// when the loader has nothing to read.
func (l *ContentLoader) source() (fsys fs.FS, root string, ok bool, err error) {
	if l.fsys == nil {
		if l.baseDir == "" {
			return nil, "", false, nil
		}
		return os.DirFS(l.baseDir), ".", true, nil
	}
	root = l.baseDir
	if root == "" {
		root = "."
	}
	root = path.Clean(root)
	if !fs.ValidPath(root) {
		return nil, "", false, fmt.Errorf("content: invalid root %q in file system", l.baseDir)
	}
	return l.fsys, root, true, nil
}

// displayPath turns a slash-separated path relative to the root into the path
// stored in Content.Path and Collection.Path: an OS path under the base
// directory, or a path inside the file system.
func (l *ContentLoader) displayPath(root, rel string) string {
	if l.fsys == nil {
		return filepath.Join(l.baseDir, filepath.FromSlash(rel))
	}
	return path.Join(root, rel)
}

// LoadAll reads every content file under the base directory. Each immediate
// subdirectory is a collection. Files directly under the base are skipped. A
// file that cannot be read is kept as a Content whose Data holds "_error". A
// collection's Type is the type most of its files have (ties go to Markdown,
// then MDX, HTML, text), so it does not depend on walk order.
func (l *ContentLoader) LoadAll() (map[string]*Collection, error) {
	collections := make(map[string]*Collection)
	if l == nil {
		return collections, nil
	}

	fsys, root, ok, err := l.source()
	if err != nil || !ok {
		return collections, err
	}

	fi, err := fs.Stat(fsys, root)
	if err != nil || !fi.IsDir() {
		// A missing base directory is an empty site, not an error.
		return collections, nil
	}

	typeCounts := make(map[string]map[ContentType]int)
	err = fs.WalkDir(fsys, root, func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			// Skip unreadable entries and keep walking.
			return nil
		}
		if d.IsDir() {
			return nil
		}

		if !isContentFile(name) {
			return nil
		}
		ext := strings.ToLower(path.Ext(name))

		rel := name
		if root != "." {
			rel = strings.TrimPrefix(name, root+"/")
		}
		parts := strings.Split(rel, "/")
		if len(parts) < 2 {
			// Skip files directly under base: a file needs a collection directory.
			return nil
		}
		collectionName := parts[0]
		coll, ok := collections[collectionName]
		if !ok {
			coll = NewCollection(CollectionConfig{
				Name: collectionName,
				Path: l.displayPath(root, collectionName),
			})
			collections[collectionName] = coll
			typeCounts[collectionName] = make(map[ContentType]int)
		}
		typeCounts[collectionName][extToType(ext)]++

		display := l.displayPath(root, rel)
		content, perr := l.parseContentFile(fsys, name, collectionName, display, rel, ext)
		if perr != nil {
			// Keep the file visible, with the error in Data["_error"].
			content = &Content{
				ID:         sanitizeID(rel),
				Slug:       strings.TrimSuffix(path.Base(name), path.Ext(name)),
				Collection: collectionName,
				Path:       display,
				Body:       "",
				HTML:       template.HTML(""),
				CreatedAt:  time.Now(),
				UpdatedAt:  time.Now(),
				Data:       map[string]any{"_error": perr.Error()},
			}
		}
		// Loader-built collections carry no schema, so AddContent only fails if
		// that changes; surface it rather than dropping the file silently.
		if err := coll.AddContent(content); err != nil {
			return fmt.Errorf("add %s: %w", display, err)
		}
		return nil
	})
	for name, coll := range collections {
		coll.Type = majorityType(typeCounts[name])
	}
	if err != nil {
		return collections, err
	}
	return collections, nil
}

// contentTypeOrder breaks ties in majorityType.
var contentTypeOrder = []ContentType{ContentTypeMarkdown, ContentTypeMDX, ContentTypeHTML, ContentTypeText}

// majorityType returns the type most files in a collection have. A tie goes to
// the type listed first in contentTypeOrder (Markdown, MDX, HTML, text).
func majorityType(counts map[ContentType]int) ContentType {
	best, bestN := ContentTypeMarkdown, 0
	for _, t := range contentTypeOrder {
		if counts[t] > bestN {
			best, bestN = t, counts[t]
		}
	}
	return best
}

// isContentFile reports whether path has an extension the loader reads.
func isContentFile(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".mdx", ".html", ".htm", ".txt":
		return true
	default:
		return false
	}
}

// FileWatcher polls a directory tree and reports which files were created,
// modified, or removed between scans. It needs no platform file-notification
// support. Scan and Watch are not safe for concurrent use on one FileWatcher.
type FileWatcher struct {
	dir   string
	state map[string]fileStamp // nil until the first Scan records a baseline
}

// fileStamp is what a scan compares to decide whether a file changed.
type fileStamp struct {
	mod  time.Time
	size int64
}

// NewFileWatcher returns a watcher for the tree rooted at dir.
func NewFileWatcher(dir string) *FileWatcher {
	return &FileWatcher{dir: dir}
}

// skipWatched reports whether the watcher ignores an entry: hidden files and
// directories (.git, .DS_Store, editor swap files) and node_modules trees.
func skipWatched(d fs.DirEntry) bool {
	name := d.Name()
	return strings.HasPrefix(name, ".") || (d.IsDir() && name == "node_modules")
}

// Scan walks the tree once and returns, sorted, the paths created, modified, or
// removed since the previous Scan. The first call records a baseline and
// returns no paths. A missing root counts as an empty tree. Entries the walk
// cannot read are reported in the error (joined). The paths it could compare
// are still returned, and files under an unreadable directory are not
// reported as removed. Hidden entries and node_modules are skipped.
func (fw *FileWatcher) Scan() ([]string, error) {
	seen := make(map[string]fileStamp, len(fw.state))
	var errs []error
	err := filepath.WalkDir(fw.dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == fw.dir && errors.Is(err, fs.ErrNotExist) {
				return filepath.SkipAll
			}
			errs = append(errs, err)
			return nil
		}
		if path != fw.dir && skipWatched(d) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			// A file removed mid-walk is picked up by the deletion check below.
			if !errors.Is(err, fs.ErrNotExist) {
				errs = append(errs, err)
			}
			return nil
		}
		seen[path] = fileStamp{mod: info.ModTime(), size: info.Size()}
		return nil
	})
	if err != nil {
		errs = append(errs, err)
	}

	var changed []string
	if fw.state != nil {
		for path, stamp := range seen {
			if prev, ok := fw.state[path]; !ok || !prev.mod.Equal(stamp.mod) || prev.size != stamp.size {
				changed = append(changed, path)
			}
		}
		for path, prev := range fw.state {
			if _, ok := seen[path]; ok {
				continue
			}
			if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
				changed = append(changed, path)
				continue
			}
			// Unreadable this pass but still present: keep the old stamp.
			seen[path] = prev
		}
	}
	fw.state = seen
	sort.Strings(changed)
	return changed, errors.Join(errs...)
}

// Watch scans every interval until ctx is done, then returns nil. It records a
// baseline first if no Scan has run yet. onChange receives each pass that found
// changes or failed. It runs on the calling goroutine, so a slow onChange
// delays the next scan rather than overlapping it. Watch returns an error only
// for a non-positive interval or a nil onChange.
func (fw *FileWatcher) Watch(ctx context.Context, interval time.Duration, onChange func(changed []string, err error)) error {
	if interval <= 0 {
		return fmt.Errorf("content: watch interval must be positive, got %v", interval)
	}
	if onChange == nil {
		return errors.New("content: watch needs an onChange callback")
	}
	if fw.state == nil {
		if _, err := fw.Scan(); err != nil {
			onChange(nil, err)
		}
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if changed, err := fw.Scan(); len(changed) > 0 || err != nil {
				onChange(changed, err)
			}
		}
	}
}

// parseContentFile reads the file name in fsys and returns a Content with its
// front matter parsed and its body rendered to HTML. display is the path
// stored in Content.Path. rel is the slash-separated path relative to the
// loader's root; the ID derives from it, so a base directory written with a
// trailing separator yields the same IDs. Files from an fs.FS without
// modification times (embed.FS) get zero CreatedAt and UpdatedAt.
func (l *ContentLoader) parseContentFile(fsys fs.FS, name, collection, display, rel, ext string) (*Content, error) {
	b, err := fs.ReadFile(fsys, name)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", display, err)
	}
	fm, body, err := parseFrontmatter(string(b))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", display, err)
	}

	info, err := fs.Stat(fsys, name)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", display, err)
	}
	mod := info.ModTime()
	id := sanitizeID(rel)
	slug := strings.TrimSuffix(path.Base(name), path.Ext(name))

	var html template.HTML
	if l.markdown != nil && (ext == ".md" || ext == ".mdx") {
		if html, err = l.markdown(body); err != nil {
			return nil, fmt.Errorf("render %s: %w", display, err)
		}
	} else {
		html = renderToHTML(ext, body)
	}

	return &Content{
		ID:         id,
		Slug:       slug,
		Collection: collection,
		Path:       display,
		Body:       body,
		HTML:       html,
		CreatedAt:  mod,
		UpdatedAt:  mod,
		Data:       fm,
	}, nil
}

// extToType maps a file extension to its ContentType, defaulting to Markdown.
func extToType(ext string) ContentType {
	switch strings.ToLower(ext) {
	case ".md":
		return ContentTypeMarkdown
	case ".mdx":
		return ContentTypeMDX
	case ".html", ".htm":
		return ContentTypeHTML
	case ".txt":
		return ContentTypeText
	default:
		return ContentTypeMarkdown
	}
}

// parseFrontmatter reads a "---" delimited header of flat "key: value" lines;
// "#" lines are comments. Both delimiters must sit on their own line, so "---"
// inside a value does not end the header. A header with no closing delimiter
// is treated as body text. A line with no key, or a key given twice, is an
// error rather than a value silently dropped or overwritten.
func parseFrontmatter(s string) (map[string]any, string, error) {
	fm := map[string]any{}
	first, rest, ok := strings.Cut(s, "\n")
	if !ok || strings.TrimRight(first, "\r ") != "---" {
		return fm, s, nil
	}
	lines := strings.SplitAfter(rest, "\n")
	for i, line := range lines {
		if strings.TrimRight(line, "\r\n ") != "---" {
			continue
		}
		for n, h := range lines[:i] {
			h = strings.TrimSpace(h)
			if h == "" || strings.HasPrefix(h, "#") {
				continue
			}
			k, v, found := strings.Cut(h, ":")
			k = strings.TrimSpace(k)
			if !found || k == "" {
				return nil, "", fmt.Errorf("front matter line %d: %q is not a \"key: value\" line", n+2, h)
			}
			if _, dup := fm[k]; dup {
				return nil, "", fmt.Errorf("front matter line %d: key %q appears twice", n+2, k)
			}
			fm[k] = parseScalar(v)
		}
		return fm, strings.TrimLeft(strings.Join(lines[i+1:], ""), "\r\n"), nil
	}
	return fm, s, nil
}

// parseScalar types a front-matter value as bool, int, float64, a []string
// for "[a, b]", or string. A value wrapped in matching quotes is always a
// string, so "02134" keeps its leading zero and "1.0" stays text.
func parseScalar(v string) any {
	v = strings.TrimSpace(v)
	if n := len(v); n >= 2 && (v[0] == '"' || v[0] == '\'') && v[n-1] == v[0] {
		return v[1 : n-1]
	}
	v = strings.Trim(v, "\"'")
	if v == "true" {
		return true
	}
	if v == "false" {
		return false
	}
	if iv, err := strconv.Atoi(v); err == nil {
		return iv
	}
	if fv, err := strconv.ParseFloat(v, 64); err == nil {
		return fv
	}
	if strings.HasPrefix(v, "[") && strings.HasSuffix(v, "]") {
		items := strings.Split(strings.Trim(v, "[]"), ",")
		out := make([]string, 0, len(items))
		for _, it := range items {
			it = strings.TrimSpace(strings.Trim(it, "\"'"))
			if it != "" {
				out = append(out, it)
			}
		}
		return out
	}
	return v
}

// renderToHTML renders a body by type: HTML is trusted and passed through,
// text is escaped inside <pre>, and Markdown goes through simpleMarkdown.
func renderToHTML(ext, body string) template.HTML {
	switch strings.ToLower(ext) {
	case ".html", ".htm":
		return template.HTML(body)
	case ".txt":
		esc := template.HTMLEscapeString(body)
		return template.HTML("<pre>" + esc + "</pre>")
	case ".md", ".mdx":
		return template.HTML(simpleMarkdown(body))
	default:
		return template.HTML(template.HTMLEscapeString(body))
	}
}

// simpleMarkdown renders headings, fenced code blocks, "- " lists, and
// paragraphs, escaping all text. It has no inline formatting or links; use
// modules/markdown for full Markdown.
func simpleMarkdown(md string) string {
	lines := strings.Split(md, "\n")
	var b strings.Builder
	inCode := false
	inList := false
	para := []string{}

	flushPara := func() {
		if len(para) == 0 {
			return
		}
		p := strings.Join(para, " ")
		b.WriteString("<p>" + template.HTMLEscapeString(strings.TrimSpace(p)) + "</p>\n")
		para = para[:0]
	}

	closeList := func() {
		if inList {
			b.WriteString("</ul>\n")
			inList = false
		}
	}

	for _, ln := range lines {
		l := strings.TrimRight(ln, "\r")
		if strings.HasPrefix(l, "```") {
			if inCode {
				b.WriteString("</code></pre>\n")
				inCode = false
			} else {
				closeList()
				flushPara()
				b.WriteString("<pre><code>")
				inCode = true
			}
			continue
		}
		if inCode {
			b.WriteString(template.HTMLEscapeString(l) + "\n")
			continue
		}

		if strings.TrimSpace(l) == "" {
			// A blank line ends the paragraph or list.
			closeList()
			flushPara()
			continue
		}

		if strings.HasPrefix(l, "###### ") {
			closeList()
			flushPara()
			b.WriteString("<h6>" + template.HTMLEscapeString(strings.TrimPrefix(l, "###### ")) + "</h6>\n")
			continue
		}
		if strings.HasPrefix(l, "##### ") {
			closeList()
			flushPara()
			b.WriteString("<h5>" + template.HTMLEscapeString(strings.TrimPrefix(l, "##### ")) + "</h5>\n")
			continue
		}
		if strings.HasPrefix(l, "#### ") {
			closeList()
			flushPara()
			b.WriteString("<h4>" + template.HTMLEscapeString(strings.TrimPrefix(l, "#### ")) + "</h4>\n")
			continue
		}
		if strings.HasPrefix(l, "### ") {
			closeList()
			flushPara()
			b.WriteString("<h3>" + template.HTMLEscapeString(strings.TrimPrefix(l, "### ")) + "</h3>\n")
			continue
		}
		if strings.HasPrefix(l, "## ") {
			closeList()
			flushPara()
			b.WriteString("<h2>" + template.HTMLEscapeString(strings.TrimPrefix(l, "## ")) + "</h2>\n")
			continue
		}
		if strings.HasPrefix(l, "# ") {
			closeList()
			flushPara()
			b.WriteString("<h1>" + template.HTMLEscapeString(strings.TrimPrefix(l, "# ")) + "</h1>\n")
			continue
		}

		// A list item, indented or not.
		if trimmed := strings.TrimSpace(l); strings.HasPrefix(trimmed, "- ") {
			if !inList {
				flushPara()
				b.WriteString("<ul>\n")
				inList = true
			}
			item := strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))
			b.WriteString("<li>" + template.HTMLEscapeString(item) + "</li>\n")
			continue
		}

		// Text after a list closes it, so output keeps source order.
		closeList()
		para = append(para, l)
	}
	if inCode {
		// An unterminated fence still yields balanced markup.
		b.WriteString("</code></pre>\n")
	}
	closeList()
	flushPara()
	return b.String()
}

// sanitizeID turns a relative path, slash or OS separated, into a Content ID
// (see Content.ID).
func sanitizeID(rel string) string {
	base := strings.TrimSuffix(rel, filepath.Ext(rel))
	base = strings.ReplaceAll(base, "/", "-")
	base = strings.ReplaceAll(base, string(os.PathSeparator), "-")
	base = strings.ReplaceAll(base, " ", "-")
	base = strings.ReplaceAll(base, "_", "-")
	return strings.ToLower(base)
}

// Get returns the Data field, or nil.
func (c *Content) Get(field string) any {
	return c.Data[field]
}

// GetString returns the Data field if it is a string, else "".
func (c *Content) GetString(field string) string {
	if val, ok := c.Data[field].(string); ok {
		return val
	}
	return ""
}

// GetInt returns the Data field if it is an int, else 0.
func (c *Content) GetInt(field string) int {
	if val, ok := c.Data[field].(int); ok {
		return val
	}
	return 0
}

// GetBool returns the Data field if it is a bool, else false.
func (c *Content) GetBool(field string) bool {
	if val, ok := c.Data[field].(bool); ok {
		return val
	}
	return false
}

// GetTime returns a time Data field. A string in one of the forms ParseTime
// accepts is parsed, so dates from front matter read correctly even in a
// collection without a schema. Anything else yields the zero time.
func (c *Content) GetTime(field string) time.Time {
	switch val := c.Data[field].(type) {
	case time.Time:
		return val
	case string:
		if t, ok := ParseTime(val); ok {
			return t
		}
	}
	return time.Time{}
}

// timeLayouts are the date forms ParseTime accepts. Each puts the year first
// and the month in the middle, so none can be misread as another.
var timeLayouts = []string{
	time.RFC3339, // also accepts fractional seconds
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05",
	"2006-01-02T15:04",
	"2006-01-02 15:04",
	"2006-01-02",
}

// ParseTime parses an unambiguous date or datetime: RFC 3339
// ("2024-01-15T10:00:00Z"), a date ("2024-01-15"), or a date with a time
// ("2024-01-15 10:00", optionally with seconds or a "T" separator). Values
// without a zone are UTC. Surrounding whitespace is ignored.
func ParseTime(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	for _, layout := range timeLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// GetSlice returns a slice Data field as []any, or nil. Typed slices (front
// matter lists parse as []string) are converted element by element.
func (c *Content) GetSlice(field string) []any {
	switch val := c.Data[field].(type) {
	case nil:
		return nil
	case []any:
		return val
	default:
		rv := reflect.ValueOf(val)
		if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
			return nil
		}
		out := make([]any, rv.Len())
		for i := range out {
			out[i] = rv.Index(i).Interface()
		}
		return out
	}
}

// GetStringSlice returns a []string Data field, or the string elements of a
// []any one, else nil.
func (c *Content) GetStringSlice(field string) []string {
	if val, ok := c.Data[field].([]string); ok {
		return val
	}
	if val, ok := c.Data[field].([]any); ok {
		result := make([]string, 0, len(val))
		for _, v := range val {
			if str, ok := v.(string); ok {
				result = append(result, str)
			}
		}
		return result
	}
	return nil
}

// Set stores a Data field, creating Data if it is nil.
func (c *Content) Set(field string, value any) {
	if c.Data == nil {
		c.Data = make(map[string]any)
	}
	c.Data[field] = value
}

// Has reports whether Data has the field, even with a nil value.
func (c *Content) Has(field string) bool {
	_, exists := c.Data[field]
	return exists
}

// ValidateSchema checks c against schema and returns the first failure as a
// *ContentError (or a schema validator's own error). It modifies c: missing
// fields get their defaults, and date strings in time fields become
// time.Time.
func (c *Content) ValidateSchema(schema *ContentSchema) error {
	for _, field := range schema.Required {
		if !c.Has(field) {
			return &ContentError{
				Type:    "validation",
				Message: "required field missing: " + field,
				Path:    c.Path,
			}
		}
	}

	for fieldName, fieldDef := range schema.Fields {
		if !c.Has(fieldName) {
			if fieldDef.Required {
				return &ContentError{
					Type:    "validation",
					Message: "required field missing: " + fieldName,
					Path:    c.Path,
				}
			}
			if fieldDef.Default != nil {
				c.Set(fieldName, fieldDef.Default)
			}
			continue
		}

		value := c.Get(fieldName)

		// Front matter has no date type, so a time-typed field accepts a date
		// string and stores the parsed time. Other fields keep strings as text.
		if str, ok := value.(string); ok && fieldDef.Type == FieldTypeTime {
			if t, ok := ParseTime(str); ok {
				c.Set(fieldName, t)
				value = t
			}
		}

		if err := validateFieldType(value, fieldDef.Type); err != nil {
			return &ContentError{
				Type:    "validation",
				Message: "invalid type for field " + fieldName + ": " + err.Error(),
				Path:    c.Path,
			}
		}

		if err := validateConstraints(value, fieldDef.Constraints); err != nil {
			return &ContentError{
				Type:    "validation",
				Message: "constraint violation for field " + fieldName + ": " + err.Error(),
				Path:    c.Path,
			}
		}

		if fieldDef.Validator != nil {
			if err := fieldDef.Validator(value); err != nil {
				return &ContentError{
					Type:    "validation",
					Message: "validation failed for field " + fieldName + ": " + err.Error(),
					Path:    c.Path,
				}
			}
		}
	}

	for _, validator := range schema.Validators {
		if err := validator(c); err != nil {
			return err
		}
	}

	return nil
}

// SchemaBuilder builds a ContentSchema. Start one with NewSchema.
type SchemaBuilder struct {
	schema *ContentSchema
}

// NewSchema starts a schema with the given name.
func NewSchema(name string) *SchemaBuilder {
	return &SchemaBuilder{
		schema: &ContentSchema{
			Name:   name,
			Fields: make(map[string]FieldDefinition),
		},
	}
}

// Field starts a field definition. It is stored when FieldBuilder.Field
// starts the next one or FieldBuilder.Build finishes it.
func (sb *SchemaBuilder) Field(name string, fieldType FieldType) *FieldBuilder {
	return &FieldBuilder{
		schemaBuilder: sb,
		fieldName:     name,
		fieldDef: FieldDefinition{
			Type:        fieldType,
			Constraints: make(map[string]any),
		},
	}
}

// Required adds field names that must be present.
func (sb *SchemaBuilder) Required(fields ...string) *SchemaBuilder {
	sb.schema.Required = append(sb.schema.Required, fields...)
	return sb
}

// Validate adds a schema-level validator.
func (sb *SchemaBuilder) Validate(fn ValidationFunc) *SchemaBuilder {
	sb.schema.Validators = append(sb.schema.Validators, fn)
	return sb
}

// Transform sets the schema's transformer, replacing any earlier one.
func (sb *SchemaBuilder) Transform(fn TransformFunc) *SchemaBuilder {
	sb.schema.Transformer = fn
	return sb
}

// Build returns the schema.
func (sb *SchemaBuilder) Build() *ContentSchema {
	return sb.schema
}

// FieldBuilder builds one FieldDefinition for a SchemaBuilder.
type FieldBuilder struct {
	schemaBuilder *SchemaBuilder
	fieldName     string
	fieldDef      FieldDefinition
}

// Required marks the field as required.
func (fb *FieldBuilder) Required() *FieldBuilder {
	fb.fieldDef.Required = true
	return fb
}

// Default sets the value stored when the field is missing.
func (fb *FieldBuilder) Default(value any) *FieldBuilder {
	fb.fieldDef.Default = value
	return fb
}

// Validate sets the field's validator, replacing any earlier one.
func (fb *FieldBuilder) Validate(fn func(any) error) *FieldBuilder {
	fb.fieldDef.Validator = fn
	return fb
}

// Description sets the field's description.
func (fb *FieldBuilder) Description(desc string) *FieldBuilder {
	fb.fieldDef.Description = desc
	return fb
}

// MinLength sets the minimum length: characters for a string, elements for a
// slice.
func (fb *FieldBuilder) MinLength(min int) *FieldBuilder {
	fb.fieldDef.Constraints["minLength"] = min
	return fb
}

// MaxLength sets the maximum length: characters for a string, elements for a
// slice.
func (fb *FieldBuilder) MaxLength(max int) *FieldBuilder {
	fb.fieldDef.Constraints["maxLength"] = max
	return fb
}

// Min sets the minimum for a numeric value. Non-numeric values pass.
func (fb *FieldBuilder) Min(min any) *FieldBuilder {
	fb.fieldDef.Constraints["min"] = min
	return fb
}

// Max sets the maximum for a numeric value. Non-numeric values pass.
func (fb *FieldBuilder) Max(max any) *FieldBuilder {
	fb.fieldDef.Constraints["max"] = max
	return fb
}

// Field stores this field and starts the next one.
func (fb *FieldBuilder) Field(name string, fieldType FieldType) *FieldBuilder {
	fb.schemaBuilder.schema.Fields[fb.fieldName] = fb.fieldDef

	return fb.schemaBuilder.Field(name, fieldType)
}

// Build stores this field and returns the schema builder.
func (fb *FieldBuilder) Build() *SchemaBuilder {
	fb.schemaBuilder.schema.Fields[fb.fieldName] = fb.fieldDef
	return fb.schemaBuilder
}

// NewCollection returns an empty collection.
func NewCollection(config CollectionConfig) *Collection {
	return &Collection{
		Name:     config.Name,
		Path:     config.Path,
		Schema:   config.Schema,
		Type:     config.Type,
		Contents: make(map[string]*Content),
	}
}

// AddContent validates content against the schema, applies the
// transformer, and stores the result under its ID, replacing any content with
// that ID. A validation error leaves the collection unchanged.
func (c *Collection) AddContent(content *Content) error {
	if c.Schema != nil {
		if err := content.ValidateSchema(c.Schema); err != nil {
			return err
		}
	}

	if c.Schema != nil && c.Schema.Transformer != nil {
		content = c.Schema.Transformer(content)
	}

	content.Collection = c.Name

	c.Contents[content.ID] = content

	return nil
}

// RemoveContent deletes the content with the given ID, if any.
func (c *Collection) RemoveContent(id string) {
	delete(c.Contents, id)
}

// GetContent returns the content with the given ID.
func (c *Collection) GetContent(id string) (*Content, bool) {
	content, exists := c.Contents[id]
	return content, exists
}

// GetContentBySlug returns content with the given slug. Slugs are not
// unique across subdirectories, so with duplicates any one may be returned.
func (c *Collection) GetContentBySlug(slug string) (*Content, bool) {
	for _, content := range c.Contents {
		if content.Slug == slug {
			return content, true
		}
	}
	return nil, false
}

// GetAllContent returns all content in the collection ordered by ID, so
// queries without a sort (and ties within one) paginate deterministically.
func (c *Collection) GetAllContent() []*Content {
	contents := make([]*Content, 0, len(c.Contents))
	for _, content := range c.Contents {
		contents = append(contents, content)
	}
	sort.Slice(contents, func(i, j int) bool { return contents[i].ID < contents[j].ID })
	return contents
}

// Query filters, searches, sorts, pages, and selects fields, in that order;
// see ContentQuery. The error is always nil.
func (c *Collection) Query(query ContentQuery) (*ContentResult, error) {
	allContent := c.GetAllContent()

	filtered := c.applyFilters(allContent, query)

	if query.Search != "" {
		filtered = c.applySearch(filtered, query.Search)
	}

	if len(query.Sort) > 0 {
		c.applySorting(filtered, query.Sort)
	}

	total := len(filtered)
	pageSize := query.Limit
	if pageSize <= 0 {
		pageSize = total
	}

	// Clamp the offset into [0, total]. A negative offset would otherwise index
	// the slice out of bounds; an offset past the end yields an empty page.
	start := query.Offset
	if start < 0 {
		start = 0
	}
	if start > total {
		start = total
	}

	end := start + pageSize
	if end > total {
		end = total
	}
	if end < start {
		end = start
	}

	paginatedContent := filtered[start:end]

	if len(query.Select) > 0 {
		paginatedContent = c.applyFieldSelection(paginatedContent, query.Select)
	}

	// pageSize is 0 only when the filtered set is empty; guard the division so an
	// empty collection with no limit does not panic.
	page := 1
	totalPages := 0
	if pageSize > 0 {
		page = (start / pageSize) + 1
		totalPages = total / pageSize
		if total%pageSize > 0 {
			totalPages++
		}
	}

	return &ContentResult{
		Contents:   paginatedContent,
		Total:      total,
		Page:       page,
		PageSize:   pageSize,
		TotalPages: totalPages,
		HasNext:    end < total,
		HasPrev:    start > 0,
	}, nil
}

// applyFilters keeps the contents that match every Where entry.
func (c *Collection) applyFilters(contents []*Content, query ContentQuery) []*Content {
	if len(query.Where) == 0 {
		return contents
	}

	filtered := make([]*Content, 0, len(contents))
	for _, content := range contents {
		if c.matchesFilters(content, query.Where) {
			filtered = append(filtered, content)
		}
	}

	return filtered
}

// matchesFilters reports whether content matches every where entry.
func (c *Collection) matchesFilters(content *Content, where map[string]any) bool {
	for field, expectedValue := range where {
		actualValue := content.Get(field)

		if !c.valuesMatch(actualValue, expectedValue) {
			return false
		}
	}
	return true
}

// valuesMatch reports whether actual matches expected: equal, a wildcard
// string match, or slices sharing an element (see ContentQuery).
func (c *Collection) valuesMatch(actual, expected any) bool {
	if (actual == nil || reflect.ValueOf(actual).Comparable()) &&
		(expected == nil || reflect.ValueOf(expected).Comparable()) && actual == expected {
		return true
	}

	// "*x*" contains, "*x" ends with, "x*" starts with; case-insensitive.
	if expectedStr, ok := expected.(string); ok {
		if strings.HasPrefix(expectedStr, "*") && strings.HasSuffix(expectedStr, "*") {
			search := strings.Trim(expectedStr, "*")
			if actualStr, ok := actual.(string); ok {
				return strings.Contains(strings.ToLower(actualStr), strings.ToLower(search))
			}
		} else if strings.HasPrefix(expectedStr, "*") {
			search := strings.TrimPrefix(expectedStr, "*")
			if actualStr, ok := actual.(string); ok {
				return strings.HasSuffix(strings.ToLower(actualStr), strings.ToLower(search))
			}
		} else if strings.HasSuffix(expectedStr, "*") {
			search := strings.TrimSuffix(expectedStr, "*")
			if actualStr, ok := actual.(string); ok {
				return strings.HasPrefix(strings.ToLower(actualStr), strings.ToLower(search))
			}
		}
	}

	if expectedSlice, ok := expected.([]any); ok {
		if actualSlice, ok := actual.([]any); ok {
			return c.slicesIntersect(actualSlice, expectedSlice)
		}
	}

	if expectedSlice, ok := expected.([]string); ok {
		if actualSlice, ok := actual.([]string); ok {
			return c.stringSlicesIntersect(actualSlice, expectedSlice)
		}
	}

	return false
}

// slicesIntersect reports whether a and b share an element.
func (c *Collection) slicesIntersect(a, b []any) bool {
	for _, aVal := range a {
		for _, bVal := range b {
			if reflect.DeepEqual(aVal, bVal) {
				return true
			}
		}
	}
	return false
}

// stringSlicesIntersect reports whether a and b share an element.
func (c *Collection) stringSlicesIntersect(a, b []string) bool {
	for _, aVal := range a {
		for _, bVal := range b {
			if aVal == bVal {
				return true
			}
		}
	}
	return false
}

// applySearch keeps the contents that contain searchTerm, ignoring case.
func (c *Collection) applySearch(contents []*Content, searchTerm string) []*Content {
	if searchTerm == "" {
		return contents
	}

	searchTerm = strings.ToLower(searchTerm)
	filtered := make([]*Content, 0, len(contents))

	for _, content := range contents {
		if c.contentMatchesSearch(content, searchTerm) {
			filtered = append(filtered, content)
		}
	}

	return filtered
}

// contentMatchesSearch reports whether the body, a string field, or a
// []string element contains searchTerm, which is already lowercased.
func (c *Collection) contentMatchesSearch(content *Content, searchTerm string) bool {
	if strings.Contains(strings.ToLower(content.Body), searchTerm) {
		return true
	}

	for _, value := range content.Data {
		if str, ok := value.(string); ok {
			if strings.Contains(strings.ToLower(str), searchTerm) {
				return true
			}
		}

		if slice, ok := value.([]string); ok {
			for _, item := range slice {
				if strings.Contains(strings.ToLower(item), searchTerm) {
					return true
				}
			}
		}
	}

	return false
}

// applySorting sorts contents in place by each criterion in turn. The sort
// is stable, so ties keep ID order.
func (c *Collection) applySorting(contents []*Content, sortCriteria []SortCriteria) {
	sort.SliceStable(contents, func(i, j int) bool {
		for _, criteria := range sortCriteria {
			valI := c.getSortValue(contents[i], criteria.Field)
			valJ := c.getSortValue(contents[j], criteria.Field)

			comparison := c.compareValues(valI, valJ)

			if comparison != 0 {
				if criteria.Order == "desc" {
					return comparison > 0
				}
				return comparison < 0
			}
		}
		return false
	})
}

// getSortValue returns the Content field for id, slug, path, createdAt, and
// updatedAt, and the Data field otherwise.
func (c *Collection) getSortValue(content *Content, field string) any {
	switch field {
	case "id":
		return content.ID
	case "slug":
		return content.Slug
	case "path":
		return content.Path
	case "createdAt":
		return content.CreatedAt
	case "updatedAt":
		return content.UpdatedAt
	default:
		return content.Get(field)
	}
}

// compareValues orders a and b: nil first, then strings, times, and numbers
// compared by kind, and anything else by its lowercased string form.
func (c *Collection) compareValues(a, b any) int {
	if a == nil && b == nil {
		return 0
	}
	if a == nil {
		return -1
	}
	if b == nil {
		return 1
	}

	if aStr, ok := a.(string); ok {
		if bStr, ok := b.(string); ok {
			if aStr < bStr {
				return -1
			} else if aStr > bStr {
				return 1
			}
			return 0
		}
	}

	if aTime, ok := a.(time.Time); ok {
		if bTime, ok := b.(time.Time); ok {
			if aTime.Before(bTime) {
				return -1
			} else if aTime.After(bTime) {
				return 1
			}
			return 0
		}
	}

	// Numeric comparison across int and float, since front matter yields int
	// for "10" and float64 for "9.5" in the same field.
	if aNum, ok := toFloat(a); ok {
		if bNum, ok := toFloat(b); ok {
			if aNum < bNum {
				return -1
			} else if aNum > bNum {
				return 1
			}
			return 0
		}
	}

	aStr := strings.ToLower(toString(a))
	bStr := strings.ToLower(toString(b))
	if aStr < bStr {
		return -1
	} else if aStr > bStr {
		return 1
	}
	return 0
}

// applyFieldSelection returns copies of contents carrying the fixed fields
// plus only the selected Data fields, body, and html.
func (c *Collection) applyFieldSelection(contents []*Content, selectFields []string) []*Content {
	result := make([]*Content, len(contents))

	for i, content := range contents {
		newContent := &Content{
			ID:         content.ID,
			Slug:       content.Slug,
			Collection: content.Collection,
			Path:       content.Path,
			CreatedAt:  content.CreatedAt,
			UpdatedAt:  content.UpdatedAt,
			Data:       make(map[string]any),
		}

		for _, field := range selectFields {
			switch field {
			case "body":
				newContent.Body = content.Body
			case "html":
				newContent.HTML = content.HTML
			default:
				if content.Has(field) {
					newContent.Set(field, content.Get(field))
				}
			}
		}

		result[i] = newContent
	}

	return result
}

// GetStats counts the collection's content. For each string or slice field
// in the schema, CustomStats holds <field>_count (distinct values) and
// <field>_max_count (uses of the most common value).
func (c *Collection) GetStats() *ContentStats {
	stats := &ContentStats{
		TotalContent: len(c.Contents),
		Collections:  1,
		CustomStats:  make(map[string]int),
		LastUpdated:  time.Now(),
	}

	if c.Schema != nil {
		for fieldName, fieldDef := range c.Schema.Fields {
			if fieldDef.Type == FieldTypeString || fieldDef.Type == FieldTypeSlice {
				fieldStats := make(map[string]int)

				for _, content := range c.Contents {
					value := content.Get(fieldName)

					switch fieldDef.Type {
					case FieldTypeString:
						if str, ok := value.(string); ok && str != "" {
							fieldStats[str]++
						}
					case FieldTypeSlice:
						if slice, ok := value.([]string); ok {
							for _, item := range slice {
								fieldStats[item]++
							}
						}
					}
				}

				stats.CustomStats[fieldName+"_count"] = len(fieldStats)

				maxCount := 0
				for _, count := range fieldStats {
					if count > maxCount {
						maxCount = count
					}
				}
				stats.CustomStats[fieldName+"_max_count"] = maxCount
			}
		}
	}

	return stats
}

// NewContentLayer returns a layer with no collections loaded. A nil config
// uses DefaultContentConfig.
func NewContentLayer(config *ContentConfig) *ContentLayer {
	if config == nil {
		config = DefaultContentConfig()
	}

	return &ContentLayer{
		collections: make(map[string]*Collection),
		declared:    make(map[string]bool),
		fromDisk:    make(map[string]bool),
		config:      config,
	}
}

// DefineCollection declares a collection. Files later loaded into a collection
// of this name pass through its schema. The returned *Collection is replaced
// on each load, so look the collection up with GetCollection afterwards.
func (cl *ContentLayer) DefineCollection(config CollectionConfig) *Collection {
	collection := NewCollection(config)
	cl.mu.Lock()
	defer cl.mu.Unlock()
	cl.collections[config.Name] = collection
	cl.declared[config.Name] = true
	return collection
}

// GetCollection returns the current collection with the given name.
func (cl *ContentLayer) GetCollection(name string) (*Collection, bool) {
	cl.mu.RLock()
	defer cl.mu.RUnlock()
	collection, exists := cl.collections[name]
	return collection, exists
}

// GetAllCollections returns a snapshot of the collections by name. The map is
// a copy, so later loads do not change it.
func (cl *ContentLayer) GetAllCollections() map[string]*Collection {
	cl.mu.RLock()
	defer cl.mu.RUnlock()
	out := make(map[string]*Collection, len(cl.collections))
	for name, c := range cl.collections {
		out[name] = c
	}
	return out
}

// LoadContent reads every content file under BaseDir into collections, one
// per immediate subdirectory (see ContentLoader.LoadAll). It does nothing
// when BaseDir is empty. This tree gives the collections posts and pages:
//
//	content/
//	├── posts/
//	│   ├── hello-world.md
//	│   └── another-post.md
//	└── pages/
//	    ├── about.md
//	    └── contact.md
//
// A collection already declared with DefineCollection keeps its configuration:
// loaded files pass through its schema (defaults, validation, transform). Files
// that fail validation are left out and reported in the returned error, while
// the valid files are still installed. A collection that was loaded from disk
// and whose directory is now gone is removed (or emptied, if declared).
//
// When WatchForChanges is set, the first LoadContent also starts a watcher
// goroutine that polls BaseDir every WatchInterval and reloads the layer after
// a content file changes. Stop it with Close.
//
// It returns an error when loaded files fail their collection's schema. A
// missing BaseDir loads nothing and is not an error.
func (cl *ContentLayer) LoadContent() error {
	if cl.config == nil || (cl.config.BaseDir == "" && cl.config.FS == nil) {
		return nil
	}

	if cl.config.WatchForChanges && cl.config.FS == nil {
		cl.startWatching()
	}
	return cl.reloadContent()
}

// loaderFor returns the layer's loader, creating it on first use.
func (cl *ContentLayer) loaderFor() *ContentLoader {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	if cl.loader == nil {
		cl.loader = &ContentLoader{
			baseDir:  cl.config.BaseDir,
			fsys:     cl.config.FS,
			markdown: cl.config.Markdown,
		}
	}
	return cl.loader
}

// installCollections swaps loaded collections into the layer. A collection
// with no declaration is installed as loaded. A declared one is rebuilt from
// its declaration so its schema applies to every loaded file. A collection the
// previous load populated from disk but this one did not find is removed, or
// emptied if declared. Validation failures are joined into the returned error.
func (cl *ContentLayer) installCollections(loaded map[string]*Collection) error {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	var errs []error
	for name := range cl.fromDisk {
		if _, ok := loaded[name]; ok {
			continue
		}
		if cl.declared[name] {
			old := cl.collections[name]
			cl.collections[name] = NewCollection(CollectionConfig{
				Name: old.Name, Path: old.Path, Schema: old.Schema, Type: old.Type,
			})
		} else {
			delete(cl.collections, name)
		}
	}
	cl.fromDisk = make(map[string]bool, len(loaded))
	for name, coll := range loaded {
		cl.fromDisk[name] = true
		declared, ok := cl.collections[name]
		if !ok || !cl.declared[name] {
			cl.collections[name] = coll
			continue
		}
		typ := declared.Type
		if typ == "" {
			typ = coll.Type
		}
		fresh := NewCollection(CollectionConfig{
			Name:   declared.Name,
			Path:   declared.Path,
			Schema: declared.Schema,
			Type:   typ,
		})
		for _, content := range coll.GetAllContent() {
			if _, failed := content.Data["_error"].(string); failed {
				// A file that failed to read, parse, or render keeps its own
				// error; validating its empty placeholder against the schema
				// would report a missing field instead.
				content.Collection = fresh.Name
				fresh.Contents[content.ID] = content
				continue
			}
			if err := fresh.AddContent(content); err != nil {
				// A schema's own validator may return an error that does not
				// name the file; name it so the joined error lists every file.
				var ce *ContentError
				if !errors.As(err, &ce) {
					err = fmt.Errorf("%s: %w", content.Path, err)
				}
				errs = append(errs, err)
			}
		}
		cl.collections[name] = fresh
	}
	return errors.Join(errs...)
}

// reloadContent reads the whole base directory again and installs the result.
// reloadMu serializes whole reloads, so an older read can never be installed
// over a newer one. Readers only wait for the swap, which holds mu.
func (cl *ContentLayer) reloadContent() error {
	cl.reloadMu.Lock()
	defer cl.reloadMu.Unlock()
	collections, err := cl.loaderFor().LoadAll()
	if err != nil {
		return fmt.Errorf("failed to load content: %w", err)
	}
	if err := cl.installCollections(collections); err != nil {
		return fmt.Errorf("failed to load content: %w", err)
	}
	return nil
}

// startWatching starts the watcher goroutine unless one is running. The
// baseline scan runs before LoadContent reads the files, so a change made while
// they load is seen by the first poll instead of being lost.
func (cl *ContentLayer) startWatching() {
	cl.mu.Lock()
	if cl.watchCancel != nil {
		cl.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	cl.watchCancel, cl.watchDone = cancel, done
	cl.mu.Unlock()

	interval := cl.config.WatchInterval
	if interval <= 0 {
		interval = DefaultWatchInterval
	}
	fw := NewFileWatcher(cl.config.BaseDir)
	if _, err := fw.Scan(); err != nil {
		// Report, but keep watching: a later pass may read the tree.
		cl.report(nil, err)
	}
	go func() {
		defer close(done)
		if err := fw.Watch(ctx, interval, cl.handleChanges); err != nil {
			cl.report(nil, err)
		}
	}()
}

// handleChanges is the watcher's callback: it reloads the layer when a content
// file changed and reports the outcome. Changes to other files (editor swap
// files, images) are ignored.
func (cl *ContentLayer) handleChanges(changed []string, scanErr error) {
	var relevant []string
	for _, path := range changed {
		if isContentFile(path) {
			relevant = append(relevant, path)
		}
	}
	if len(relevant) == 0 {
		if scanErr != nil {
			cl.report(nil, scanErr)
		}
		return
	}
	cl.report(relevant, errors.Join(scanErr, cl.reloadContent()))
}

// report delivers a watcher pass to OnReload, or logs its error when no
// callback is configured.
func (cl *ContentLayer) report(changed []string, err error) {
	if cl.config.OnReload != nil {
		cl.config.OnReload(changed, err)
		return
	}
	if err != nil {
		slog.Default().Error("content: reload failed", "dir", cl.config.BaseDir, "err", err)
	}
}

// Close stops the watcher started by LoadContent and waits for it to exit. It
// is safe to call more than once, and on a layer that never watched. A later
// LoadContent with WatchForChanges set starts a new watcher.
func (cl *ContentLayer) Close() error {
	cl.mu.Lock()
	cancel, done := cl.watchCancel, cl.watchDone
	cl.watchCancel, cl.watchDone = nil, nil
	cl.mu.Unlock()
	if cancel == nil {
		return nil
	}
	cancel()
	<-done
	return nil
}

// validateFieldType returns a *ContentError when value is not of
// expectedType. Floats accept integers.
func validateFieldType(value any, expectedType FieldType) error {
	switch expectedType {
	case FieldTypeString:
		if _, ok := value.(string); !ok {
			return &ContentError{Type: "type", Message: "expected string"}
		}
	case FieldTypeInt:
		if _, ok := value.(int); !ok {
			return &ContentError{Type: "type", Message: "expected int"}
		}
	case FieldTypeFloat:
		// Integers are valid floats: front matter parses "10" as int.
		if _, ok := toFloat(value); !ok {
			return &ContentError{Type: "type", Message: "expected float"}
		}
	case FieldTypeBool:
		if _, ok := value.(bool); !ok {
			return &ContentError{Type: "type", Message: "expected bool"}
		}
	case FieldTypeTime:
		if _, ok := value.(time.Time); !ok {
			return &ContentError{Type: "type", Message: "expected time.Time or a date string (2006-01-02, 2006-01-02 15:04, or RFC 3339)"}
		}
	case FieldTypeSlice:
		if value == nil || reflect.TypeOf(value).Kind() != reflect.Slice {
			return &ContentError{Type: "type", Message: "expected slice"}
		}
	case FieldTypeMap:
		if value == nil || reflect.TypeOf(value).Kind() != reflect.Map {
			return &ContentError{Type: "type", Message: "expected map"}
		}
	case FieldTypeAny:
		// Any value passes.
	}
	return nil
}

// validateConstraints checks the minLength, maxLength, min, and max
// constraints, returning the first violation as a *ContentError.
func validateConstraints(value any, constraints map[string]any) error {
	if minLen, ok := constraints["minLength"]; ok {
		length := getLength(value)
		if min, ok := minLen.(int); ok && length < min {
			return &ContentError{Type: "constraint", Message: "value too short"}
		}
	}

	if maxLen, ok := constraints["maxLength"]; ok {
		length := getLength(value)
		if max, ok := maxLen.(int); ok && length > max {
			return &ContentError{Type: "constraint", Message: "value too long"}
		}
	}

	if minVal, ok := constraints["min"]; ok {
		if !isGreaterOrEqual(value, minVal) {
			return &ContentError{Type: "constraint", Message: "value too small"}
		}
	}

	if maxVal, ok := constraints["max"]; ok {
		if !isLessOrEqual(value, maxVal) {
			return &ContentError{Type: "constraint", Message: "value too large"}
		}
	}

	return nil
}

// getLength returns a string's character count or a slice's length, else 0.
func getLength(value any) int {
	switch v := value.(type) {
	case string:
		return utf8.RuneCountInString(v)
	case []any:
		return len(v)
	case []string:
		return len(v)
	default:
		rv := reflect.ValueOf(value)
		if rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array {
			return rv.Len()
		}
	}
	return 0
}

// isGreaterOrEqual and isLessOrEqual compare any mix of int and float; a
// non-numeric operand is outside the constraint's domain and passes.
func isGreaterOrEqual(value, min any) bool {
	v, vok := toFloat(value)
	m, mok := toFloat(min)
	return !vok || !mok || v >= m
}

func isLessOrEqual(value, max any) bool {
	v, vok := toFloat(value)
	m, mok := toFloat(max)
	return !vok || !mok || v <= m
}

// toFloat widens Go's built-in numeric kinds to float64.
func toFloat(value any) (float64, bool) {
	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(rv.Int()), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return float64(rv.Uint()), true
	case reflect.Float32, reflect.Float64:
		return rv.Float(), true
	default:
		return 0, false
	}
}

// toString formats the scalar kinds compareValues falls back to, and returns
// "" for anything else.
func toString(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case int:
		return fmt.Sprintf("%d", v)
	case float64:
		return fmt.Sprintf("%f", v)
	case bool:
		if v {
			return "true"
		}
		return "false"
	case time.Time:
		return v.Format(time.RFC3339)
	default:
		return ""
	}
}
