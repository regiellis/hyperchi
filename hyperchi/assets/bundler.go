// The bundler downloads pinned builds of Tailwind CSS and HTMX from jsDelivr
// into StaticDir, checking each against a pinned SHA-256, and writes a
// Heroicons manifest, without npm. Files that already exist are kept, so
// commit them to avoid fetching at build time.
//
//	bundler := NewBundler(&Config{StaticDir: "static"})
//	if err := bundler.BundleHTMX(); err != nil { // writes static/js/htmx.min.js
//		log.Fatal(err)
//	}

package assets

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime/debug"
	"time"
)

// Bundler writes the third-party files Initialize bundles into StaticDir.
type Bundler struct {
	config *Config
}

// NewBundler returns a Bundler that reads StaticDir from config.
func NewBundler(config *Config) *Bundler {
	return &Bundler{
		config: config,
	}
}

// bundlerHTTPClient bounds CDN downloads with a timeout. The default client
// has none, so a stalled CDN would hang startup.
var bundlerHTTPClient = &http.Client{Timeout: 30 * time.Second}

// Download URLs of the bundled files. Each names a file published in the npm
// package, not a file the CDN generates (jsDelivr minifies on request, and a
// generated file can change), so its SHA-256 below stays valid.
const (
	TailwindCSSCDN = "https://cdn.jsdelivr.net/npm/tailwindcss@4.1.11/index.css"
	TailwindJSCDN  = "https://cdn.jsdelivr.net/npm/@tailwindcss/browser@4.1.11/dist/index.global.js"
	HTMXCDN        = "https://cdn.jsdelivr.net/npm/htmx.org@4.0.0/dist/htmx.min.js"
)

// SHA-256 digests of the files above, checked against jsDelivr's published
// package hashes on 2026-10-05. A download that does not match is rejected, so
// a compromised or changed CDN response never reaches StaticDir.
const (
	tailwindCSSSHA256 = "7935ac31063856f181a19c8b904fe14635f0c226ce8952043464e33723da5dd3"
	tailwindJSSHA256  = "7933e213b39f3434b04eb24d219f279596a9c7d9f7833f7e3a8db3cebe7e69d3"
	htmxSHA256        = "e484d9171a9db30a39c8f16e3d709d4137f3211c659f8e6125816635033d593f"
)

// maxDownloadSize bounds a bundled asset; the pinned files are well under it.
const maxDownloadSize = 8 << 20

// BundleTailwind downloads Tailwind CSS v4's stylesheet to
// StaticDir/css/tailwind.min.css and its browser build to
// StaticDir/js/tailwindcss.min.js, skipping a file that already exists.
func (b *Bundler) BundleTailwind() error {
	// The filename must stay in sync with the @tailwind import rewrite in
	// compiler.go, which points at /static/css/tailwind.min.css.
	cssPath := filepath.Join(b.config.StaticDir, "css", "tailwind.min.css")
	if !b.fileExists(cssPath) {
		if err := b.downloadFile(TailwindCSSCDN, tailwindCSSSHA256, cssPath); err != nil {
			return fmt.Errorf("failed to download Tailwind CSS: %w", err)
		}
	}

	jsPath := filepath.Join(b.config.StaticDir, "js", "tailwindcss.min.js")
	if !b.fileExists(jsPath) {
		if err := b.downloadFile(TailwindJSCDN, tailwindJSSHA256, jsPath); err != nil {
			return fmt.Errorf("failed to download Tailwind JS: %w", err)
		}
	}

	return nil
}

// BundleHTMX downloads htmx to StaticDir/js/htmx.min.js unless that file
// already exists.
func (b *Bundler) BundleHTMX() error {
	outputPath := filepath.Join(b.config.StaticDir, "js", "htmx.min.js")

	if b.fileExists(outputPath) {
		return nil
	}

	return b.downloadFile(HTMXCDN, htmxSHA256, outputPath)
}

// BundleHeroicons writes StaticDir/icons/heroicons/manifest.json, a JSON
// object with "version" (the hyperchi module version that wrote it, see
// moduleVersion) and "icons" (name to SVG markup for a small set of Heroicons
// v2 outline icons). An existing manifest is left alone.
func (b *Bundler) BundleHeroicons() error {
	iconsDir := filepath.Join(b.config.StaticDir, "icons", "heroicons")
	if err := os.MkdirAll(iconsDir, 0755); err != nil {
		return fmt.Errorf("failed to create heroicons directory: %w", err)
	}

	manifestPath := filepath.Join(iconsDir, "manifest.json")
	if b.fileExists(manifestPath) {
		return nil
	}
	var manifest bytes.Buffer
	enc := json.NewEncoder(&manifest)
	enc.SetEscapeHTML(false) // keep the SVG markup readable
	enc.SetIndent("", "  ")
	if err := enc.Encode(map[string]any{
		"version": moduleVersion(debug.ReadBuildInfo()),
		"icons":   heroiconsOutline,
	}); err != nil {
		return fmt.Errorf("failed to encode heroicons manifest: %w", err)
	}
	if err := os.WriteFile(manifestPath, manifest.Bytes(), 0644); err != nil {
		return fmt.Errorf("failed to write heroicons manifest: %w", err)
	}
	return nil
}

// hyperchiModule is the module path whose version moduleVersion reports.
const hyperchiModule = "github.com/regiellis/hyperchi"

// moduleVersion returns the version of the hyperchi module in the running
// binary: the main module's version when hyperchi is the main module (tests,
// the CLI), otherwise the version of the hyperchi dependency, following a
// replace directive. It returns "(devel)" when the build carries no version,
// as with a local replace or a binary built without module support.
func moduleVersion(info *debug.BuildInfo, ok bool) string {
	if !ok || info == nil {
		return "(devel)"
	}
	orDevel := func(v string) string {
		if v == "" {
			return "(devel)"
		}
		return v
	}
	if info.Main.Path == hyperchiModule {
		return orDevel(info.Main.Version)
	}
	for _, dep := range info.Deps {
		if dep.Path != hyperchiModule {
			continue
		}
		if dep.Replace != nil {
			return orDevel(dep.Replace.Version)
		}
		return orDevel(dep.Version)
	}
	return "(devel)"
}

// heroiconsOutline holds the Heroicons v2 outline icons the bundled manifest
// carries, keyed by icon name.
var heroiconsOutline = map[string]string{
	"check":        `<svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" stroke-width="1.5" stroke="currentColor" class="w-6 h-6"><path stroke-linecap="round" stroke-linejoin="round" d="m4.5 12.75 6 6 9-13.5" /></svg>`,
	"x-mark":       `<svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" stroke-width="1.5" stroke="currentColor" class="w-6 h-6"><path stroke-linecap="round" stroke-linejoin="round" d="M6 18 18 6M6 6l12 12" /></svg>`,
	"plus":         `<svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" stroke-width="1.5" stroke="currentColor" class="w-6 h-6"><path stroke-linecap="round" stroke-linejoin="round" d="M12 4.5v15m7.5-7.5h-15" /></svg>`,
	"minus":        `<svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" stroke-width="1.5" stroke="currentColor" class="w-6 h-6"><path stroke-linecap="round" stroke-linejoin="round" d="M5 12h14" /></svg>`,
	"home":         `<svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" stroke-width="1.5" stroke="currentColor" class="w-6 h-6"><path stroke-linecap="round" stroke-linejoin="round" d="m2.25 12 8.25-8.25a1.125 1.125 0 0 1 1.59 0L21 12M4.5 9.75v10.125c0 .621.504 1.125 1.125 1.125H9.75v-4.875c0-.621.504-1.125 1.125-1.125h2.25c.621 0 1.125.504 1.125 1.125V21h4.125c.621 0 1.125-.504 1.125-1.125V9.75M8.25 21h8.25" /></svg>`,
	"user":         `<svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" stroke-width="1.5" stroke="currentColor" class="w-6 h-6"><path stroke-linecap="round" stroke-linejoin="round" d="M15.75 6a3.75 3.75 0 1 1-7.5 0 3.75 3.75 0 0 1 7.5 0ZM4.501 20.118a7.5 7.5 0 0 1 14.998 0A17.933 17.933 0 0 1 12 21.75c-2.676 0-5.216-.584-7.499-1.632Z" /></svg>`,
	"cog":          `<svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" stroke-width="1.5" stroke="currentColor" class="w-6 h-6"><path stroke-linecap="round" stroke-linejoin="round" d="M9.594 3.94c.09-.542.56-.94 1.11-.94h2.593c.55 0 1.02.398 1.11.94l.213 1.281c.063.374.313.686.645.87.074.04.147.083.22.127.325.196.72.257 1.075.124l1.217-.456a1.125 1.125 0 0 1 1.37.49l1.296 2.247a1.125 1.125 0 0 1-.26 1.431l-1.003.827c-.293.241-.438.613-.43.992a6.932 6.932 0 0 1 0 .255c-.008.378.137.75.43.991l1.004.827c.424.35.534.955.26 1.43l-1.298 2.247a1.125 1.125 0 0 1-1.369.491l-1.217-.456c-.355-.133-.75-.072-1.076.124a6.47 6.47 0 0 1-.22.128c-.331.183-.581.495-.644.869l-.213 1.281c-.09.543-.56.94-1.11.94h-2.594c-.55 0-1.019-.398-1.11-.94l-.213-1.281c-.062-.374-.312-.686-.644-.87a6.52 6.52 0 0 1-.22-.127c-.325-.196-.72-.257-1.076-.124l-1.217.456a1.125 1.125 0 0 1-1.369-.49l-1.297-2.247a1.125 1.125 0 0 1 .26-1.431l1.004-.827c.292-.24.437-.613.43-.991a6.932 6.932 0 0 1 0-.255c.007-.38-.138-.751-.43-.992l-1.004-.827a1.125 1.125 0 0 1-.26-1.43l1.297-2.247a1.125 1.125 0 0 1 1.37-.491l1.216.456c.356.133.751.072 1.076-.124.072-.044.146-.086.22-.128.332-.183.582-.495.644-.869l.214-1.28Z" /><path stroke-linecap="round" stroke-linejoin="round" d="M15 12a3 3 0 1 1-6 0 3 3 0 0 1 6 0Z" /></svg>`,
	"arrow-right":  `<svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" stroke-width="1.5" stroke="currentColor" class="w-6 h-6"><path stroke-linecap="round" stroke-linejoin="round" d="M13.5 4.5 21 12m0 0-7.5 7.5M21 12H3" /></svg>`,
	"arrow-left":   `<svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" stroke-width="1.5" stroke="currentColor" class="w-6 h-6"><path stroke-linecap="round" stroke-linejoin="round" d="M10.5 19.5 3 12m0 0 7.5-7.5M3 12h18" /></svg>`,
	"chevron-down": `<svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" stroke-width="1.5" stroke="currentColor" class="w-6 h-6"><path stroke-linecap="round" stroke-linejoin="round" d="m19.5 8.25-7.5 7.5-7.5-7.5" /></svg>`,
	"chevron-up":   `<svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" stroke-width="1.5" stroke="currentColor" class="w-6 h-6"><path stroke-linecap="round" stroke-linejoin="round" d="m4.5 15.75 7.5-7.5 7.5 7.5" /></svg>`,
}

// downloadFile downloads url and saves it to outputPath only when its SHA-256
// equals wantSHA256 (hex). The file is staged beside the target and renamed
// into place, so a failed or rejected download leaves nothing behind.
func (b *Bundler) downloadFile(url, wantSHA256, outputPath string) (err error) {
	if wantSHA256 == "" {
		return fmt.Errorf("download %s: no SHA-256 to verify it against", url)
	}
	dir := filepath.Dir(outputPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}

	resp, err := bundlerHTTPClient.Get(url)
	if err != nil {
		return fmt.Errorf("failed to download %s: %w", url, err)
	}
	defer func() {
		// The body has been read (or the download already failed), so a
		// close error loses nothing. Log it rather than drop it.
		if cerr := resp.Body.Close(); cerr != nil {
			b.config.logger().Debug("assets: closing download body", "url", url, "err", cerr)
		}
	}()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to download %s: status %d", url, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxDownloadSize+1))
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", url, err)
	}
	if len(body) > maxDownloadSize {
		return fmt.Errorf("download %s: larger than %d bytes", url, maxDownloadSize)
	}
	sum := sha256.Sum256(body)
	if got := hex.EncodeToString(sum[:]); got != wantSHA256 {
		return fmt.Errorf("download %s: SHA-256 %s does not match the pinned %s", url, got, wantSHA256)
	}

	tmp, err := os.CreateTemp(dir, ".download-*")
	if err != nil {
		return fmt.Errorf("failed to stage %s: %w", outputPath, err)
	}
	staged := tmp.Name()
	defer func() {
		if err != nil {
			if rerr := os.Remove(staged); rerr != nil && !os.IsNotExist(rerr) {
				b.config.logger().Debug("assets: removing staged download", "path", staged, "err", rerr)
			}
		}
	}()
	if _, werr := tmp.Write(body); werr != nil {
		if cerr := tmp.Close(); cerr != nil {
			return fmt.Errorf("failed to write %s: %w (close: %v)", outputPath, werr, cerr)
		}
		return fmt.Errorf("failed to write %s: %w", outputPath, werr)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("failed to write %s: %w", outputPath, err)
	}
	if err := os.Chmod(staged, 0644); err != nil {
		return fmt.Errorf("failed to set mode on %s: %w", outputPath, err)
	}
	if err := os.Rename(staged, outputPath); err != nil {
		return fmt.Errorf("failed to save %s: %w", outputPath, err)
	}
	return nil
}

// fileExists reports whether path can be stat'ed.
func (b *Bundler) fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// GetAssetPath returns the URL path of a bundled asset, "/static/<type>/<name>".
// It always uses forward slashes, whatever the operating system.
func (b *Bundler) GetAssetPath(assetType, name string) string {
	return path.Join("/static", assetType, name)
}

// GetTailwindPath returns the URL path of the bundled Tailwind browser build.
func (b *Bundler) GetTailwindPath() string {
	return b.GetAssetPath("js", "tailwindcss.min.js")
}

// GetHTMXPath returns the URL path of the bundled htmx.
func (b *Bundler) GetHTMXPath() string {
	return b.GetAssetPath("js", "htmx.min.js")
}

// GetHeroiconsPath returns the URL path of the Heroicons directory.
func (b *Bundler) GetHeroiconsPath() string {
	return b.GetAssetPath("icons", "heroicons")
}
