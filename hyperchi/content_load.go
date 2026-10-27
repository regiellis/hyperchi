package hyperchi

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/regiellis/hyperchi/hyperchi/content"
	"github.com/regiellis/hyperchi/hyperchi/integrations"
)

// contentIntegrationName is the integration that ties content watchers to the
// app's lifecycle, so app.Close stops them.
const contentIntegrationName = "hyperchi.content"

// ContentOption configures ContentManager.LoadDir and LoadFS.
type ContentOption func(*contentLoadConfig)

type contentLoadConfig struct {
	collections []content.CollectionConfig
	markdown    content.MarkdownFunc
	interval    time.Duration
}

// WithContentCollection declares a collection before loading, as
// content.ContentLayer.DefineCollection does. Every file in the collection's
// directory passes its Schema (defaults, type and constraint checks,
// validators, transform); files that fail are left out and reported. A
// declared collection with no directory loads empty.
func WithContentCollection(cfg content.CollectionConfig) ContentOption {
	return func(c *contentLoadConfig) { c.collections = append(c.collections, cfg) }
}

// WithMarkdown renders the body of .md and .mdx files with fn in place of the
// content package's small built-in subset (see content.ContentConfig.Markdown).
// A file fn fails on is left out and reported.
func WithMarkdown(fn content.MarkdownFunc) ContentOption {
	return func(c *contentLoadConfig) { c.markdown = fn }
}

// WithContentWatchInterval sets how often LoadDir polls the directory for
// changes in development. Zero means content.DefaultWatchInterval.
func WithContentWatchInterval(d time.Duration) ContentOption {
	return func(c *contentLoadConfig) { c.interval = d }
}

// ContentLoadError reports what LoadDir or LoadFS could not load: one error
// per file left out (a schema failure, an unreadable file, a duplicate slug),
// or the reason the load failed. Everything else was loaded.
type ContentLoadError struct {
	Source string  // the directory, or the root inside the file system
	Errs   []error // one per problem, in path order where the loader gives one
}

// Error lists every problem, one per line.
func (e *ContentLoadError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "content: loading %s: %d problem", e.Source, len(e.Errs))
	if len(e.Errs) != 1 {
		b.WriteString("s")
	}
	for _, err := range e.Errs {
		b.WriteString("\n\t")
		b.WriteString(err.Error())
	}
	return b.String()
}

// Unwrap returns the individual errors, for errors.Is and errors.As.
func (e *ContentLoadError) Unwrap() []error { return e.Errs }

// contentSource is one LoadDir or LoadFS call: the layer that reads its files
// and the collections it owns in the manager.
type contentSource struct {
	name  string // the directory or FS root, for messages
	key   string // the directory's absolute path; empty for an FS
	layer *content.ContentLayer
	// mu serializes reading the layer and swapping its collections in, so a
	// slower install can never put an older read over a newer one.
	mu sync.Mutex
}

// LoadDir loads every collection under dir into the manager: each immediate
// subdirectory is a collection, and every .md, .mdx, .html, .htm, or .txt
// file anywhere below it is an item (see the content package for front
// matter, rendering, and schemas). Items keep their rendered HTML, front
// matter (as Metadata, with "title" and "description" also in Title and
// Description), slug (the file name without its extension), path, and the
// file's modification time as CreatedAt and UpdatedAt (RFC 3339). Items are
// ordered by path. Get, Query, and ContentRoutes then serve them.
//
// A file that fails its collection's schema, cannot be read or rendered, or
// repeats a slug already used in its collection is left out, and the valid
// files still load. LoadDir returns a *ContentLoadError listing every such
// file. A missing dir is an error.
//
// Loaded collections replace any collection of the same name, and replace
// themselves as a unit: a request sees the old collection or the new one,
// never a mix. Changes made with Create, Update, or Delete to a loaded
// collection last until it reloads, and Create does not apply the content
// schema declared with WithContentCollection.
//
// In development (app.IsDevelopment), LoadDir also polls dir for changes and
// reloads it after a content file is created, changed, or removed. Reload
// failures go to the app's logger: files that fail are left out as above, and
// a reload that fails outright keeps the last content. Each load and reload
// emits "content:loaded" on app.Events with the source, the changed files,
// and the error. app.Close stops the polling. In production LoadDir loads
// once. Loading the same directory again replaces the earlier load and its
// watcher. A LoadDir after the app has started serving or closed loads once
// and logs that it is not watching.
func (cm *ContentManager) LoadDir(dir string, opts ...ContentOption) error {
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("content: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("content: %s is not a directory", dir)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("content: %w", err)
	}
	cfg := newContentLoadConfig(opts)

	watch := cm.app != nil && cm.app.isDevelopment()
	if watch {
		if err := cm.registerWatchers(); err != nil {
			cm.logError("content: not watching %s: %v", dir, err)
			watch = false
		}
	}

	src := &contentSource{name: dir, key: abs}
	src.layer = content.NewContentLayer(&content.ContentConfig{
		BaseDir:         dir,
		WatchForChanges: watch,
		WatchInterval:   cfg.interval,
		Markdown:        cfg.markdown,
		OnReload: func(changed []string, err error) {
			cm.reloaded(src, changed, err)
		},
	})
	return cm.load(src, cfg, watch)
}

// LoadFS is LoadDir for a file system, such as a go:embed directory: root is
// the slash-separated directory inside fsys whose subdirectories are
// collections ("" or "." for fsys itself). Paths in items are paths inside
// fsys. It loads once in every mode, since an embedded tree cannot change. An
// embed.FS records no modification times, so CreatedAt and UpdatedAt are
// empty; keep dates in front matter.
func (cm *ContentManager) LoadFS(fsys fs.FS, root string, opts ...ContentOption) error {
	if fsys == nil {
		return errors.New("content: LoadFS needs a file system")
	}
	if root == "" {
		root = "."
	}
	info, err := fs.Stat(fsys, root)
	if err != nil {
		return fmt.Errorf("content: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("content: %s is not a directory", root)
	}
	cfg := newContentLoadConfig(opts)
	src := &contentSource{name: root}
	src.layer = content.NewContentLayer(&content.ContentConfig{
		FS:       fsys,
		BaseDir:  root,
		Markdown: cfg.markdown,
	})
	return cm.load(src, cfg, false)
}

func newContentLoadConfig(opts []ContentOption) *contentLoadConfig {
	cfg := &contentLoadConfig{}
	for _, opt := range opts {
		if opt != nil {
			opt(cfg)
		}
	}
	return cfg
}

// load reads src for the first time and installs its collections.
func (cm *ContentManager) load(src *contentSource, cfg *contentLoadConfig, watch bool) error {
	for _, c := range cfg.collections {
		src.layer.DefineCollection(c)
	}
	layerErr := src.layer.LoadContent()
	var rejected *content.LoadRejectedError
	var failures []error
	if errors.As(layerErr, &rejected) {
		// A new load must not replace an existing source with its empty
		// declarations when the candidate graph was rejected. Keep its old
		// watcher as well; callers can retry the explicit load after fixing it.
		if watch {
			if err := src.layer.Close(); err != nil {
				layerErr = errors.Join(layerErr, err)
			}
		}
	} else {
		if watch {
			cm.trackWatcher(src)
		}
		failures = cm.install(src, true)
	}
	err := newContentLoadError(src.name, layerErr, failures)
	cm.events.Emit("content:loaded", map[string]any{
		"source":  src.name,
		"changed": []string(nil),
		"error":   err,
	})
	return err
}

// reloaded is the watcher's callback: after the layer reloaded for changed
// files, it swaps the result in and reports failures to the logger.
func (cm *ContentManager) reloaded(src *contentSource, changed []string, err error) {
	var failures []error
	var rejected *content.LoadRejectedError
	if len(changed) > 0 && !errors.As(err, &rejected) {
		failures = cm.install(src, false)
	}
	loadErr := newContentLoadError(src.name, err, failures)
	if loadErr != nil {
		cm.logError("content: reload: %v", loadErr)
	}
	if len(changed) > 0 {
		cm.events.Emit("content:loaded", map[string]any{
			"source":  src.name,
			"changed": changed,
			"error":   loadErr,
		})
	}
}

// install converts src's current collections and swaps them in under one
// write lock, so readers see all of the old state or all of the new. A
// collection src loaded before but no longer has is removed. takeover lets a
// first load replace a collection another source owns; a reload never takes
// one back. It returns the items it left out.
func (cm *ContentManager) install(src *contentSource, takeover bool) []error {
	src.mu.Lock()
	defer src.mu.Unlock()

	loaded := src.layer.GetAllCollections()
	built := make(map[string]*ContentCollection, len(loaded))
	var failures []error
	for _, name := range slices.Sorted(maps.Keys(loaded)) {
		col, errs := buildContentCollection(loaded[name])
		built[name] = col
		failures = append(failures, errs...)
	}

	cm.mu.Lock()
	defer cm.mu.Unlock()
	for name, owner := range cm.owners {
		if _, ok := built[name]; owner == src && !ok {
			delete(cm.collections, name)
			delete(cm.owners, name)
			cm.dropCachedLocked(name)
		}
	}
	for name, col := range built {
		if owner, ok := cm.owners[name]; ok && owner != src && !takeover {
			continue
		}
		cm.collections[name] = col
		cm.owners[name] = src
		cm.dropCachedLocked(name)
	}
	return failures
}

// dropCachedLocked removes a collection's items from the Get cache. The
// caller holds cm.mu.
func (cm *ContentManager) dropCachedLocked(collection string) {
	maps.DeleteFunc(cm.cache, func(key contentKey, _ *ContentItem) bool {
		return key.collection == collection
	})
}

// buildContentCollection converts a loaded collection, keeping its path
// order. Files the loader kept with an "_error" (unreadable or unrenderable)
// are left out and returned as errors. When files share a slug, the one
// nearest the collection directory (then the first in path order) keeps it
// and the others are left out and reported.
func buildContentCollection(c *content.Collection) (*ContentCollection, []error) {
	docs := c.GetAllContent()
	col := &ContentCollection{Name: c.Name, Path: c.Path, Items: make([]*ContentItem, 0, len(docs))}
	var failures []error

	depth := func(doc *content.Content) int { return strings.Count(filepath.ToSlash(doc.Path), "/") }
	bySlug := make(map[string]*content.Content, len(docs))
	for _, doc := range slices.SortedStableFunc(slices.Values(docs), func(a, b *content.Content) int {
		return depth(a) - depth(b)
	}) {
		if _, failed := doc.Data["_error"].(string); failed {
			continue
		}
		if _, taken := bySlug[doc.Slug]; !taken {
			bySlug[doc.Slug] = doc
		}
	}

	for _, doc := range docs {
		if msg, ok := doc.Data["_error"].(string); ok {
			failures = append(failures, errors.New(msg))
			continue
		}
		if owner := bySlug[doc.Slug]; owner != doc {
			failures = append(failures, fmt.Errorf("%s: slug %q is already used by %s", doc.Path, doc.Slug, owner.Path))
			continue
		}
		metadata := maps.Clone(doc.Data)
		if metadata == nil {
			metadata = map[string]any{}
		}
		col.Items = append(col.Items, &ContentItem{
			Path:        doc.Path,
			Slug:        doc.Slug,
			Title:       doc.GetString("title"),
			Description: doc.GetString("description"),
			Content:     doc.Body,
			HTML:        doc.HTML,
			Metadata:    metadata,
			CreatedAt:   formatContentTime(doc.CreatedAt),
			UpdatedAt:   formatContentTime(doc.UpdatedAt),
		})
	}
	return col, failures
}

// formatContentTime formats t as RFC 3339, or "" for the zero time.
func formatContentTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

// newContentLoadError flattens the layer's error (a joined list of schema
// failures, or one load failure) and the conversion failures into a
// *ContentLoadError, or nil when there are none.
func newContentLoadError(source string, layerErr error, failures []error) error {
	var errs []error
	if layerErr != nil {
		var rejected *content.LoadRejectedError
		var joined interface{ Unwrap() []error }
		if errors.As(layerErr, &rejected) {
			errs = append(errs, layerErr)
		} else if errors.As(layerErr, &joined) {
			errs = append(errs, joined.Unwrap()...)
		} else {
			errs = append(errs, layerErr)
		}
	}
	errs = append(errs, failures...)
	if len(errs) == 0 {
		return nil
	}
	return &ContentLoadError{Source: source, Errs: errs}
}

// registerWatchers ties content watchers to the app's lifecycle the first
// time one starts. It fails once the app has closed, or when the app's
// integrations have started (registration is frozen then).
func (cm *ContentManager) registerWatchers() error {
	cm.watchMu.Lock()
	defer cm.watchMu.Unlock()
	if cm.watchClosed {
		return errors.New("the app is closed")
	}
	if cm.watchRegistered {
		return nil
	}
	if err := cm.app.Integrations().Register(contentIntegrationName, integrations.Hooks{CloseFunc: cm.closeWatchers}); err != nil {
		return err
	}
	cm.watchRegistered = true
	return nil
}

// trackWatcher records src's running watcher so closeWatchers stops it. A
// watcher for a directory already watched replaces the earlier one, which is
// stopped. If the app closed in the meantime, src's watcher stops now.
func (cm *ContentManager) trackWatcher(src *contentSource) {
	cm.watchMu.Lock()
	if cm.watchClosed {
		cm.watchMu.Unlock()
		cm.stopLayer(src)
		return
	}
	if cm.watchers == nil {
		cm.watchers = make(map[string]*contentSource)
	}
	old := cm.watchers[src.key]
	cm.watchers[src.key] = src
	cm.watchMu.Unlock()
	if old != nil {
		cm.stopLayer(old)
	}
}

// stopLayer stops a source's watcher, logging a failure.
func (cm *ContentManager) stopLayer(src *contentSource) {
	if err := src.layer.Close(); err != nil {
		cm.logError("content: stop watching %s: %v", src.name, err)
	}
}

// closeWatchers stops every content watcher and waits for each to exit, or
// for ctx to end. Later LoadDir calls load without watching. app.Close calls
// it through the app's integrations.
func (cm *ContentManager) closeWatchers(ctx context.Context) error {
	cm.watchMu.Lock()
	cm.watchClosed = true
	sources := slices.Collect(maps.Values(cm.watchers))
	cm.watchers = nil
	cm.watchMu.Unlock()

	done := make(chan error, 1)
	go func() {
		var errs []error
		for _, src := range sources {
			if err := src.layer.Close(); err != nil {
				errs = append(errs, fmt.Errorf("stop watching %s: %w", src.name, err))
			}
		}
		done <- errors.Join(errs...)
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return fmt.Errorf("content: stop watchers: %w", ctx.Err())
	}
}

// logError reports through the app's logger, or slog for a manager without
// an app.
func (cm *ContentManager) logError(format string, args ...any) {
	if cm.app != nil && cm.app.Helpers != nil && cm.app.Helpers.Logger != nil {
		cm.app.Helpers.Logger.Error(format, args...)
		return
	}
	slog.Default().Error(fmt.Sprintf(format, args...))
}
