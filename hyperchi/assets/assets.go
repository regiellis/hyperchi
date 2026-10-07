// Package assets copies, minifies, and optimizes the static files of a
// HyperChi application, and pushes hot-reload messages to browsers during
// development.
//
// What it does:
//   - Compiler: copies CSS and JavaScript into OutputDir/css and OutputDir/js,
//     minifying them with github.com/tdewolff/minify when EnableMinify is set,
//     and rewriting "@tailwind" directives into an import of the bundled
//     Tailwind CSS. It does not resolve imports, transpile, tree-shake, split
//     code, purge unused CSS, or write source maps; Sass (.scss, .sass) and
//     TypeScript (.ts) sources are rejected with an error naming the file.
//   - Bundler: downloads pinned builds of Tailwind CSS and HTMX from jsDelivr
//     into StaticDir, verified against pinned SHA-256 digests (once, skipping
//     files that already exist), and writes a small Heroicons manifest.
//   - Optimizer: re-encodes JPEG and PNG files, downscaling them to MaxWidth,
//     minifies SVG files with github.com/tdewolff/minify, checks that WebP files
//     decode, and copies everything else. Go has no WebP encoder in the
//     standard library or golang.org/x/image, so nothing is converted to WebP.
//   - Watcher: polls the source directories and recompiles or re-optimizes a
//     file when it changes, then tells connected browsers to reload over a
//     WebSocket.
//
// Basic use:
//
//	cfg := assets.DefaultConfig()
//	cfg.EnableTailwind, cfg.EnableHTMX = false, false // skip the CDN downloads
//	pipeline := assets.NewWithConfig(cfg)
//	if err := pipeline.Initialize(); err != nil {
//		log.Fatal(err)
//	}
//	defer func() {
//		if err := pipeline.Close(); err != nil {
//			log.Print(err)
//		}
//	}()
//	if err := pipeline.ProcessCSS("assets/css/app.css"); err != nil {
//		log.Fatal(err)
//	}
package assets

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
)

// AssetPipeline ties the compiler, optimizer, bundler, and optional watcher to
// one Config. Its methods are safe for concurrent use.
type AssetPipeline struct {
	config    *Config
	compiler  *Compiler
	optimizer *Optimizer
	bundler   *Bundler
	watcher   *Watcher
	mutex     sync.RWMutex
}

// Config holds configuration for the asset pipeline.
//
//	config := assets.DefaultConfig()    // development defaults
//	config := assets.ProductionConfig() // minify, no watching or hot reload
//	pipeline := assets.NewWithConfig(config)
type Config struct {
	// StaticDir receives the bundled third-party files (Tailwind, HTMX,
	// Heroicons) and the directories Initialize creates.
	StaticDir string
	// SourceDir holds unprocessed assets. Optimized images keep their path
	// relative to it, and the watcher polls it.
	SourceDir string
	// OutputDir receives compiled CSS and JS (OutputDir/css, OutputDir/js)
	// and optimized images.
	OutputDir string

	// EnableTailwind, EnableHTMX, and EnableHeroicons make Initialize bundle
	// those libraries. EnableTailwind also turns on the "@tailwind" rewrite.
	EnableTailwind  bool
	EnableHTMX      bool
	EnableHeroicons bool
	// EnableMinify minifies CSS, JS, and SVG output and adds ".min" to
	// compiled file names.
	EnableMinify bool
	// EnableWatch makes NewWithConfig create a Watcher and Initialize start it.
	EnableWatch bool
	// HotReload makes the watcher notify browsers after it rebuilds a file.
	HotReload bool

	// MaxWidth downscales wider JPEG and PNG images to this width, keeping
	// the aspect ratio (0 keeps the original size).
	MaxWidth int
	// JPEGQuality is the JPEG encoding quality, 1 to 100 (85 when zero).
	JPEGQuality int

	// Logger receives the watcher's progress and errors. Nil means
	// slog.Default().
	Logger *slog.Logger
}

// minify reports whether output should be minified.
func (c *Config) minify() bool {
	return c.EnableMinify
}

// logger returns the configured logger or slog.Default().
func (c *Config) logger() *slog.Logger {
	if c != nil && c.Logger != nil {
		return c.Logger
	}
	return slog.Default()
}

// DefaultConfig returns development settings: assets read from "assets",
// output and bundles in "static", Tailwind, HTMX, and Heroicons bundled, file
// watching and hot reload on, minification off.
func DefaultConfig() *Config {
	return &Config{
		StaticDir:       "static",
		SourceDir:       "assets",
		OutputDir:       "static",
		EnableTailwind:  true,
		EnableHTMX:      true,
		EnableHeroicons: true,
		EnableMinify:    false,
		EnableWatch:     true,
		HotReload:       true,
	}
}

// ProductionConfig returns DefaultConfig with minification on and file
// watching and hot reload off.
func ProductionConfig() *Config {
	config := DefaultConfig()
	config.EnableMinify = true
	config.EnableWatch = false
	config.HotReload = false
	return config
}

// New creates an asset pipeline with DefaultConfig.
func New() *AssetPipeline {
	return NewWithConfig(DefaultConfig())
}

// NewWithConfig creates an asset pipeline for config. It creates a Watcher
// only when config.EnableWatch is set. The pipeline keeps and may modify
// config (the Enable methods write to it).
func NewWithConfig(config *Config) *AssetPipeline {
	pipeline := &AssetPipeline{
		config: config,
	}

	pipeline.compiler = NewCompiler(config)
	pipeline.optimizer = NewOptimizer(config)
	pipeline.bundler = NewBundler(config)

	if config.EnableWatch {
		pipeline.watcher = NewWatcher(config, pipeline)
	}

	return pipeline
}

// Initialize creates StaticDir and its css, js, images, and icons
// subdirectories, bundles the enabled libraries (downloading Tailwind and HTMX
// when their files are missing), and starts the watcher when EnableWatch is
// set. Stop the watcher with Close.
func (ap *AssetPipeline) Initialize() error {
	ap.mutex.Lock()
	defer ap.mutex.Unlock()

	dirs := []string{
		ap.config.StaticDir,
		filepath.Join(ap.config.StaticDir, "css"),
		filepath.Join(ap.config.StaticDir, "js"),
		filepath.Join(ap.config.StaticDir, "images"),
		filepath.Join(ap.config.StaticDir, "icons"),
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	if err := ap.bundleDefaults(); err != nil {
		return fmt.Errorf("failed to bundle default assets: %w", err)
	}

	if ap.config.EnableWatch && ap.watcher != nil {
		if err := ap.watcher.Start(); err != nil {
			return fmt.Errorf("failed to start asset watcher: %w", err)
		}
	}

	return nil
}

// bundleDefaults bundles each of Tailwind, HTMX, and Heroicons that is enabled.
func (ap *AssetPipeline) bundleDefaults() error {
	if ap.config.EnableTailwind {
		if err := ap.bundler.BundleTailwind(); err != nil {
			return fmt.Errorf("failed to bundle Tailwind: %w", err)
		}
	}

	if ap.config.EnableHTMX {
		if err := ap.bundler.BundleHTMX(); err != nil {
			return fmt.Errorf("failed to bundle HTMX: %w", err)
		}
	}

	if ap.config.EnableHeroicons {
		if err := ap.bundler.BundleHeroicons(); err != nil {
			return fmt.Errorf("failed to bundle Heroicons: %w", err)
		}
	}

	return nil
}

// ProcessCSS copies one CSS file to OutputDir/css, rewriting "@tailwind"
// directives when EnableTailwind is set and minifying when EnableMinify is set
// (the output is then named name.min.css).
//
//	if err := pipeline.ProcessCSS("assets/css/app.css"); err != nil {
//		log.Printf("CSS compilation failed: %v", err)
//	}
func (ap *AssetPipeline) ProcessCSS(inputPath string) error {
	ap.mutex.RLock()
	defer ap.mutex.RUnlock()

	return ap.compiler.CompileCSS(inputPath)
}

// ProcessJS copies one JavaScript file to OutputDir/js, minifying it when
// EnableMinify is set. Imports are left as written: nothing is bundled or
// transpiled.
func (ap *AssetPipeline) ProcessJS(inputPath string) error {
	ap.mutex.RLock()
	defer ap.mutex.RUnlock()

	return ap.compiler.CompileJS(inputPath)
}

// OptimizeImages optimizes every image under inputDir. See
// Optimizer.OptimizeImage for what each format gets. The first failure stops
// the walk and is returned.
func (ap *AssetPipeline) OptimizeImages(inputDir string) error {
	ap.mutex.RLock()
	defer ap.mutex.RUnlock()

	return ap.optimizer.OptimizeImages(inputDir)
}

// OptimizeImage optimizes one image file. See Optimizer.OptimizeImage.
func (ap *AssetPipeline) OptimizeImage(inputPath string) error {
	ap.mutex.RLock()
	defer ap.mutex.RUnlock()

	return ap.optimizer.OptimizeImage(inputPath)
}

// EnableProduction turns minification on and file watching and hot reload off
// in the pipeline's config, and returns the pipeline for chaining. A watcher
// that is already running keeps running. Stop it with Close.
//
//	pipeline.EnableProduction()
//	if err := pipeline.ProcessCSS("assets/css/app.css"); err != nil {
//		log.Fatal(err)
//	}
func (ap *AssetPipeline) EnableProduction() *AssetPipeline {
	ap.mutex.Lock()
	defer ap.mutex.Unlock()

	ap.config.EnableMinify = true
	ap.config.EnableWatch = false
	ap.config.HotReload = false

	return ap
}

// EnableTailwind sets EnableTailwind, which turns on Tailwind bundling at the
// next Initialize and the "@tailwind" rewrite in ProcessCSS.
func (ap *AssetPipeline) EnableTailwind() *AssetPipeline {
	ap.mutex.Lock()
	defer ap.mutex.Unlock()

	ap.config.EnableTailwind = true
	return ap
}

// EnableHTMX sets EnableHTMX, so the next Initialize bundles HTMX.
func (ap *AssetPipeline) EnableHTMX() *AssetPipeline {
	ap.mutex.Lock()
	defer ap.mutex.Unlock()

	ap.config.EnableHTMX = true
	return ap
}

// EnableHeroicons sets EnableHeroicons, so the next Initialize writes the
// Heroicons manifest.
func (ap *AssetPipeline) EnableHeroicons() *AssetPipeline {
	ap.mutex.Lock()
	defer ap.mutex.Unlock()

	ap.config.EnableHeroicons = true
	return ap
}

// EnableMinify sets EnableMinify for later ProcessCSS, ProcessJS, and SVG
// optimization.
func (ap *AssetPipeline) EnableMinify() *AssetPipeline {
	ap.mutex.Lock()
	defer ap.mutex.Unlock()

	ap.config.EnableMinify = true
	return ap
}

// EnableWatch sets EnableWatch and creates the watcher if there is none. Call
// Initialize (or Watcher().Start) to start it.
func (ap *AssetPipeline) EnableWatch() *AssetPipeline {
	ap.mutex.Lock()
	defer ap.mutex.Unlock()

	ap.config.EnableWatch = true
	if ap.watcher == nil {
		ap.watcher = NewWatcher(ap.config, ap)
	}
	return ap
}

// Watcher returns the pipeline's watcher, or nil when watching is off.
func (ap *AssetPipeline) Watcher() *Watcher {
	ap.mutex.RLock()
	defer ap.mutex.RUnlock()
	return ap.watcher
}

// GetConfig returns a copy of the current configuration, so changing it does
// not affect the pipeline.
func (ap *AssetPipeline) GetConfig() Config {
	ap.mutex.RLock()
	defer ap.mutex.RUnlock()

	return *ap.config
}

// Close stops the watcher, if one is running, and waits for its polling
// goroutine to exit.
func (ap *AssetPipeline) Close() error {
	// Stop outside the pipeline lock: the polling goroutine may be waiting
	// for that lock inside ProcessCSS, and Stop waits for the goroutine.
	watcher := ap.Watcher()
	if watcher != nil {
		return watcher.Stop()
	}
	return nil
}
