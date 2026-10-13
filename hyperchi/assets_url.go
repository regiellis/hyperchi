package hyperchi

import (
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/url"
	"sort"
	"strings"
	"sync"
)

// assetVersionLen is how many hex digits of a file's SHA-256 go in ?v=. 48
// bits is far more than needed to tell one file's versions apart, and keeps
// URLs short.
const assetVersionLen = 12

// staticMount is one StaticFS or StaticWithOptions mount, as AssetURL sees it.
type staticMount struct {
	prefix     string                              // URL prefix with its trailing slash, such as "/static/"
	open       func() (fs.FS, func() error, error) // the mount's files, and how to release them
	hashes     *contentHashes                      // shared with the mount's handler
	revalidate bool                                // check the file on every lookup (development)
}

// assetRegistry is the app's list of static mounts. It is shared by pointer,
// so the copies Group makes see the same mounts.
type assetRegistry struct {
	mu     sync.RWMutex
	mounts []*staticMount // longest prefix first
}

// recordStaticMount remembers a mount for AssetURL under its full URL prefix,
// the group's included. A later mount with the same prefix replaces it.
func (g *HyperChi) recordStaticMount(m *staticMount) {
	reg := g.staticAssets
	if reg == nil {
		return
	}
	reg.mu.Lock()
	defer reg.mu.Unlock()
	kept := reg.mounts[:0]
	for _, existing := range reg.mounts {
		if existing.prefix != m.prefix {
			kept = append(kept, existing)
		}
	}
	reg.mounts = append(kept, m)
	sort.SliceStable(reg.mounts, func(i, j int) bool {
		return len(reg.mounts[i].prefix) > len(reg.mounts[j].prefix)
	})
}

// AssetURL returns urlPath with a version taken from the file's content, for
// example "/static/css/app.css?v=3f2a9c1b7e4d", so the URL changes whenever
// the file does and a browser can cache it for a year. The static handlers
// answer a request carrying the current version with
// "Cache-Control: public, max-age=31536000, immutable".
//
// urlPath is the URL a page would use without the version. It must start with
// the prefix of a mount made with StaticFS, StaticWithOptions, Static, or
// FileServer (a mount inside a Group counts under its full path, such as
// "/admin/static/"), and name a file that mount serves. Anything else is an error: a missing file, a
// directory, a dotfile, a path that is not clean (such as one containing ".."),
// or one carrying a query or fragment.
//
// Each file is hashed once and the version cached. In development (the mount's
// Development option or the app's configuration) the file is checked on every
// call and hashed again when its size or modification time changes, so edits
// show up on the next render. In production a file is treated as fixed until
// the process restarts, unless the static handler serves a newer copy first.
//
// Templates call it as the asset function:
//
//	<link rel="stylesheet" href="{{asset "/static/css/app.css"}}">
func (g *HyperChi) AssetURL(urlPath string) (string, error) {
	if strings.ContainsAny(urlPath, "?#") {
		return "", fmt.Errorf("hyperchi: asset %q: the path must not carry a query or fragment", urlPath)
	}
	decoded, err := url.PathUnescape(urlPath)
	if err != nil {
		return "", fmt.Errorf("hyperchi: asset %q: %w", urlPath, err)
	}
	mount, name := g.staticAssets.match(decoded)
	if mount == nil {
		return "", fmt.Errorf("hyperchi: asset %q: no static mount on the app serves this path", urlPath)
	}
	if !validStaticName(name) {
		return "", fmt.Errorf("hyperchi: asset %q: not a file a static mount serves", urlPath)
	}
	version, err := mount.version(name)
	if err != nil {
		return "", fmt.Errorf("hyperchi: asset %q: %w", urlPath, err)
	}
	return urlPath + "?v=" + version, nil
}

// assetTemplateFunc is the asset template function. Its error stops the
// render, so a page never ships a broken asset reference.
func (g *HyperChi) assetTemplateFunc(urlPath string) (template.URL, error) {
	u, err := g.AssetURL(urlPath)
	if err != nil {
		return "", err
	}
	// The path matched a mount prefix, which starts with "/" and names no
	// scheme or host, and the version is hex.
	return template.URL(u), nil
}

// match returns the mount whose prefix is the longest one urlPath starts with,
// and the file name under it.
func (reg *assetRegistry) match(urlPath string) (*staticMount, string) {
	if reg == nil {
		return nil, ""
	}
	reg.mu.RLock()
	defer reg.mu.RUnlock()
	for _, m := range reg.mounts {
		if name, ok := strings.CutPrefix(urlPath, m.prefix); ok {
			return m, name
		}
	}
	return nil, ""
}

// version returns name's content version, from the cache when the mount does
// not revalidate.
func (m *staticMount) version(name string) (string, error) {
	if !m.revalidate {
		if sum, ok := m.hashes.latest(name); ok {
			return sum[:assetVersionLen], nil
		}
	}
	fsys, release, err := m.open()
	if err != nil {
		return "", err
	}
	sum, err := hashMountFile(fsys, name, m.hashes)
	if rerr := release(); rerr != nil {
		err = errors.Join(err, fmt.Errorf("release static root: %w", rerr))
	}
	if err != nil {
		return "", err
	}
	return sum[:assetVersionLen], nil
}

func hashMountFile(fsys fs.FS, name string, hashes *contentHashes) (string, error) {
	f, info, content, _, err := openStaticFile(fsys, name)
	if err != nil {
		return "", err
	}
	sum, err := hashes.sum(name, info, content)
	if cerr := f.Close(); cerr != nil {
		log.Printf("hyperchi: close static file: %v", cerr)
	}
	return sum, err
}
