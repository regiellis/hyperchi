package hyperchi

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path"
	"strings"
	"sync"
	"time"
)

// StaticOptions controls publication and caching. Directories and dotfiles are
// never published. Filesystem symlinks may resolve only within the configured root.
//
// A request whose ?v= matches the file's current content version, the one the
// asset template function and HyperChi.AssetURL write, is sent
// "public, max-age=31536000, immutable": that URL's content cannot change.
// Any other request gets CacheControl. CachePolicy, when set, decides every
// request, and Development overrides everything.
type StaticOptions struct {
	Development  bool                       // send Cache-Control: no-cache, overriding the rest
	CacheControl string                     // default "public, max-age=3600"
	CachePolicy  func(*http.Request) string // per-request Cache-Control, overriding CacheControl and the versioned policy
	OnError      StatusHandler              // renders 404, 405, and 500; the app's pipeline when mounted by the app
}

const (
	staticDefaultCache   = "public, max-age=3600"
	staticImmutableCache = "public, max-age=31536000, immutable"
	staticETagLen        = 20 // hex digits of the SHA-256 in a content-hash ETag
)

// NewStaticHandler serves the regular files under root for GET and HEAD,
// through os.Root so paths and symlinks stay inside it. It fails at once when
// root cannot be opened. Each request opens and closes its own root, so the
// handler needs no cleanup.
func NewStaticHandler(root string, options StaticOptions) (http.Handler, error) {
	return newStaticHandler(root, options, newContentHashes())
}

func newStaticHandler(root string, options StaticOptions, hashes *contentHashes) (http.Handler, error) {
	check, err := os.OpenRoot(root)
	if err != nil {
		return nil, fmt.Errorf("static root: %w", err)
	}
	if err := check.Close(); err != nil {
		return nil, fmt.Errorf("close static root: %w", err)
	}
	fail := staticFailer(options)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name, ok := staticName(w, r, fail)
		if !ok {
			return
		}
		confined, err := os.OpenRoot(root)
		if err != nil {
			fail(w, r, 404, err)
			return
		}
		defer func() {
			if err := confined.Close(); err != nil {
				log.Printf("hyperchi: close static root: %v", err)
			}
		}()
		serveStaticFile(w, r, confined.FS(), name, options, fail, hashes)
	}), nil
}

// NewStaticFSHandler serves regular files from fsys with the same rules as
// NewStaticHandler. Files without a modification time (an embed.FS) get a
// content-hash ETag so conditional requests still work; fsys is treated as
// immutable, as an embed.FS is.
func NewStaticFSHandler(fsys fs.FS, options StaticOptions) http.Handler {
	return newStaticFSHandler(fsys, options, newContentHashes())
}

func newStaticFSHandler(fsys fs.FS, options StaticOptions, hashes *contentHashes) http.Handler {
	fail := staticFailer(options)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if name, ok := staticName(w, r, fail); ok {
			serveStaticFile(w, r, fsys, name, options, fail, hashes)
		}
	})
}

type staticFail func(w http.ResponseWriter, r *http.Request, code int, err error)

func staticFailer(options StaticOptions) staticFail {
	return func(w http.ResponseWriter, r *http.Request, code int, err error) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Robots-Tag", "noindex")
		if r.Method == http.MethodHead {
			w = headResponseWriter{w}
		}
		if options.OnError != nil {
			options.OnError(w, r, code, err)
		} else {
			http.Error(w, http.StatusText(code), code)
		}
	}
}

// staticName checks the method and returns the requested file name, refusing
// directories, dotfiles, and anything that is not a clean relative path.
func staticName(w http.ResponseWriter, r *http.Request, fail staticFail) (string, bool) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		fail(w, r, 405, nil)
		return "", false
	}
	name := strings.TrimPrefix(r.URL.Path, "/")
	if !validStaticName(name) {
		fail(w, r, 404, nil)
		return "", false
	}
	return name, true
}

// validStaticName reports whether name is a file name the static handlers
// publish: a clean, slash-separated relative path with no dotfile segment.
func validStaticName(name string) bool {
	if name == "" || !fs.ValidPath(name) || strings.Contains(name, "\\") || path.Clean(name) != name {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if strings.HasPrefix(part, ".") {
			return false
		}
	}
	return true
}

// openStaticFile opens name in fsys and returns it with its info and a
// seekable view of its content. The caller closes the file. On failure the
// status says how to answer: 404 for anything that cannot be opened as a
// regular file (missing, a directory, a path os.Root refuses), 500 when a
// regular file cannot be read.
func openStaticFile(fsys fs.FS, name string) (fs.File, fs.FileInfo, io.ReadSeeker, int, error) {
	f, err := fsys.Open(name)
	if err != nil {
		return nil, nil, nil, http.StatusNotFound, err
	}
	info, err := f.Stat()
	if err == nil && !info.Mode().IsRegular() {
		err = fmt.Errorf("%s: not a regular file", name)
	}
	if err != nil {
		return nil, nil, nil, http.StatusNotFound, errors.Join(err, f.Close())
	}
	if content, ok := f.(io.ReadSeeker); ok {
		return f, info, content, 0, nil
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, nil, nil, http.StatusInternalServerError, errors.Join(fmt.Errorf("read %s: %w", name, err), f.Close())
	}
	return f, info, bytes.NewReader(data), 0, nil
}

func serveStaticFile(w http.ResponseWriter, r *http.Request, fsys fs.FS, name string, options StaticOptions, fail staticFail, hashes *contentHashes) {
	f, info, content, code, err := openStaticFile(fsys, name)
	if err != nil {
		fail(w, r, code, err)
		return
	}
	defer func() {
		if err := f.Close(); err != nil {
			log.Printf("hyperchi: close static file: %v", err)
		}
	}()
	version := requestedVersion(r, options)
	var sum string
	if info.ModTime().IsZero() || version != "" {
		if sum, err = hashes.sum(name, info, content); err != nil {
			fail(w, r, 500, err)
			return
		}
	}
	if info.ModTime().IsZero() {
		w.Header().Set("ETag", `"`+sum[:staticETagLen]+`"`)
	}
	cache := options.CacheControl
	if cache == "" {
		cache = staticDefaultCache
	}
	if version != "" && version == sum[:assetVersionLen] {
		cache = staticImmutableCache
	}
	if options.CachePolicy != nil {
		cache = options.CachePolicy(r)
	}
	if options.Development {
		cache = "no-cache"
	}
	w.Header().Set("Cache-Control", cache)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, info.Name(), info.ModTime(), content)
}

// requestedVersion returns the request's ?v= when it could earn the immutable
// policy: the right length, and neither Development nor CachePolicy deciding
// the header anyway. Anything else is "", so a junk ?v= never costs a hash.
func requestedVersion(r *http.Request, options StaticOptions) string {
	if options.Development || options.CachePolicy != nil {
		return ""
	}
	v := r.URL.Query().Get("v")
	if len(v) != assetVersionLen {
		return ""
	}
	return v
}

// contentHashes caches the hex SHA-256 of files by name. Each entry remembers
// the size and modification time it was computed for, so a file that changed
// on disk is hashed again. Entries exist only for regular files that were
// opened, so the cache is bounded by the files a mount serves.
type contentHashes struct {
	mu      sync.Mutex
	entries map[string]contentHash
}

type contentHash struct {
	size    int64
	modTime time.Time
	sum     string
}

func newContentHashes() *contentHashes {
	return &contentHashes{entries: make(map[string]contentHash)}
}

// sum returns the hash of name's content, hashing content (and rewinding it
// for serving) unless an entry for the same size and modification time exists.
func (h *contentHashes) sum(name string, info fs.FileInfo, content io.ReadSeeker) (string, error) {
	h.mu.Lock()
	entry, ok := h.entries[name]
	h.mu.Unlock()
	if ok && entry.size == info.Size() && entry.modTime.Equal(info.ModTime()) {
		return entry.sum, nil
	}
	digest := sha256.New()
	if _, err := io.Copy(digest, content); err != nil {
		return "", fmt.Errorf("hash %s: %w", name, err)
	}
	if _, err := content.Seek(0, io.SeekStart); err != nil {
		return "", fmt.Errorf("rewind %s: %w", name, err)
	}
	entry = contentHash{size: info.Size(), modTime: info.ModTime(), sum: hex.EncodeToString(digest.Sum(nil))}
	h.mu.Lock()
	h.entries[name] = entry
	h.mu.Unlock()
	return entry.sum, nil
}

// latest returns the most recent hash recorded for name without checking the
// file again.
func (h *contentHashes) latest(name string) (string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	entry, ok := h.entries[name]
	return entry.sum, ok
}

// StaticWithOptions serves dir at pattern, which must have the form
// "/prefix/*", with NewStaticHandler's rules. Failures render through the
// app's error pipeline unless options.OnError is set. The mount is recorded
// for the asset template function and AssetURL. Inside a Group the files are
// served, and recorded, under the group's prefix; a group whose pattern has a
// URL parameter cannot hold a static mount. Configure before serving.
func (g *HyperChi) StaticWithOptions(pattern, dir string, options StaticOptions) error {
	if err := g.checkStaticPattern(pattern); err != nil {
		return err
	}
	hashes := newContentHashes()
	h, err := newStaticHandler(dir, g.staticOptions(options), hashes)
	if err != nil {
		return err
	}
	g.mountStatic(pattern, h, &staticMount{
		open:       diskStaticFS(dir),
		hashes:     hashes,
		revalidate: options.Development || g.isDevelopment(),
	})
	return nil
}

// StaticFS mounts fsys (for example an embedded directory, via fs.Sub) at
// pattern, with the rules of StaticWithOptions, including those for a Group.
// Configure before serving.
func (g *HyperChi) StaticFS(pattern string, fsys fs.FS, options StaticOptions) error {
	if err := g.checkStaticPattern(pattern); err != nil {
		return err
	}
	if fsys == nil {
		return fmt.Errorf("hyperchi: StaticFS %s: nil filesystem", pattern)
	}
	hashes := newContentHashes()
	g.mountStatic(pattern, newStaticFSHandler(fsys, g.staticOptions(options), hashes), &staticMount{
		open:       func() (fs.FS, func() error, error) { return fsys, func() error { return nil }, nil },
		hashes:     hashes,
		revalidate: options.Development || g.isDevelopment(),
	})
	return nil
}

// diskStaticFS opens dir through os.Root, as NewStaticHandler does, so
// hashing for AssetURL follows the same confinement as serving.
func diskStaticFS(dir string) func() (fs.FS, func() error, error) {
	return func() (fs.FS, func() error, error) {
		root, err := os.OpenRoot(dir)
		if err != nil {
			return nil, nil, fmt.Errorf("static root: %w", err)
		}
		return root.FS(), root.Close, nil
	}
}

func (g *HyperChi) checkStaticPattern(pattern string) error {
	if !strings.HasPrefix(pattern, "/") || !strings.HasSuffix(pattern, "/*") || strings.Count(pattern, "*") != 1 {
		return fmt.Errorf("static pattern must have the form /prefix/*")
	}
	// The handler strips a literal prefix, and AssetURL needs one to build
	// URLs, so a parameter in the group's pattern or the mount's cannot work.
	if strings.ContainsAny(g.routePrefix+pattern, "{}") {
		return fmt.Errorf("hyperchi: static mount %s%s: a static prefix cannot contain a URL parameter", g.routePrefix, pattern)
	}
	return nil
}

func (g *HyperChi) staticOptions(options StaticOptions) StaticOptions {
	if options.OnError == nil {
		options.OnError = func(w http.ResponseWriter, r *http.Request, code int, err error) {
			g.respondStatus(w, r, code, err)
		}
	}
	return options
}

// mountStatic routes pattern on this router and strips the full URL prefix,
// the group's included: chi matches a sub-router on the remaining path but
// leaves r.URL.Path whole, so stripping pattern alone would never match.
func (g *HyperChi) mountStatic(pattern string, h http.Handler, mount *staticMount) {
	prefix := g.routePrefix + strings.TrimSuffix(pattern, "*")
	stripped := http.StripPrefix(prefix, h)
	g.router.Method(http.MethodGet, pattern, stripped)
	g.router.Method(http.MethodHead, pattern, stripped)
	mount.prefix = prefix
	g.recordStaticMount(mount)
}
