package assets

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// offlineConfig is a development config rooted in dir whose bundles are
// already on disk, so Initialize never downloads.
func offlineConfig(t *testing.T, dir string) *Config {
	t.Helper()
	cfg, _ := quietConfig(filepath.Join(dir, "assets"), filepath.Join(dir, "static"))
	cfg.StaticDir = filepath.Join(dir, "static")
	cfg.EnableTailwind, cfg.EnableHTMX, cfg.EnableHeroicons = true, true, true
	for _, rel := range []string{"css/tailwind.min.css", "js/tailwindcss.min.js", "js/htmx.min.js"} {
		writeTestFile(t, filepath.Join(cfg.StaticDir, filepath.FromSlash(rel)), "cached")
	}
	return cfg
}

func TestConfigs(t *testing.T) {
	dev := DefaultConfig()
	if dev.EnableMinify || !dev.EnableWatch || !dev.HotReload || dev.SourceDir != "assets" || dev.StaticDir != "static" {
		t.Errorf("DefaultConfig = %+v", dev)
	}
	prod := ProductionConfig()
	if !prod.EnableMinify || prod.EnableWatch || prod.HotReload {
		t.Errorf("ProductionConfig = %+v", prod)
	}
	if New().GetConfig() != *DefaultConfig() {
		t.Error("New does not use DefaultConfig")
	}
}

func TestInitializeCreatesDirsAndStartsWatcher(t *testing.T) {
	dir := t.TempDir()
	cfg := offlineConfig(t, dir)
	cfg.EnableWatch = true
	p := NewWithConfig(cfg)
	w := p.Watcher()
	if w == nil {
		t.Fatal("EnableWatch did not create a watcher")
	}
	w.dirs = nil // nothing to poll in this test

	if err := p.Initialize(); err != nil {
		t.Fatal(err)
	}
	for _, sub := range []string{"css", "js", "images", "icons", "icons/heroicons"} {
		if fi, err := os.Stat(filepath.Join(cfg.StaticDir, filepath.FromSlash(sub))); err != nil || !fi.IsDir() {
			t.Errorf("%s not created: %v", sub, err)
		}
	}
	if !w.IsRunning() {
		t.Error("Initialize did not start the watcher")
	}
	// Initialize reports a watcher that cannot start instead of logging it.
	if err := p.Initialize(); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Errorf("second Initialize = %v, want the start error", err)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if w.IsRunning() {
		t.Error("Close left the watcher running")
	}
}

func TestInitializeReportsErrors(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "file")
	writeTestFile(t, blocker, "x")
	p := NewWithConfig(&Config{StaticDir: filepath.Join(blocker, "static")})
	if err := p.Initialize(); err == nil {
		t.Error("an uncreatable static directory was accepted")
	}

	// The heroicons directory cannot be created when a file is in the way.
	cfg := offlineConfig(t, filepath.Join(dir, "site"))
	cfg.EnableTailwind, cfg.EnableHTMX = false, false
	writeTestFile(t, filepath.Join(cfg.StaticDir, "icons", "heroicons"), "in the way")
	if err := NewWithConfig(cfg).Initialize(); err == nil || !strings.Contains(err.Error(), "Heroicons") {
		t.Errorf("Initialize = %v, want the Heroicons error", err)
	}
}

func TestPipelineProcessesFiles(t *testing.T) {
	dir := t.TempDir()
	cfg := offlineConfig(t, dir)
	p := NewWithConfig(cfg)
	if p.Watcher() != nil {
		t.Fatal("watcher created without EnableWatch")
	}
	if err := p.Close(); err != nil {
		t.Fatalf("Close without a watcher = %v", err)
	}

	css := filepath.Join(cfg.SourceDir, "app.css")
	writeTestFile(t, css, "@tailwind base;\nbody { margin: 0; }\n")
	js := filepath.Join(cfg.SourceDir, "app.js")
	writeTestFile(t, js, "let a = 1 // one\n")
	png := filepath.Join(cfg.SourceDir, "images", "a.png")
	writePNG(t, png, 4, 4)

	if err := p.ProcessCSS(css); err != nil {
		t.Fatal(err)
	}
	if err := p.ProcessJS(js); err != nil {
		t.Fatal(err)
	}
	if err := p.OptimizeImages(filepath.Join(cfg.SourceDir, "images")); err != nil {
		t.Fatal(err)
	}
	if err := p.OptimizeImage(png); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(cfg.OutputDir, "css", "app.css"))
	if err != nil || !strings.Contains(string(got), "@import '/static/css/tailwind.min.css';") {
		t.Errorf("compiled CSS = %q, %v", got, err)
	}
	for _, rel := range []string{"js/app.js", "images/a.png"} {
		if _, err := os.Stat(filepath.Join(cfg.OutputDir, filepath.FromSlash(rel))); err != nil {
			t.Errorf("%s: %v", rel, err)
		}
	}

	p.EnableProduction()
	if err := p.ProcessJS(js); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(cfg.OutputDir, "js", "app.min.js")); err != nil || strings.Contains(string(got), "one") {
		t.Errorf("production JS = %q, %v", got, err)
	}
}

func TestPipelineSetters(t *testing.T) {
	p := NewWithConfig(&Config{})
	p.EnableTailwind().EnableHTMX().EnableHeroicons().EnableMinify().EnableWatch()
	cfg := p.GetConfig()
	if !cfg.EnableTailwind || !cfg.EnableHTMX || !cfg.EnableHeroicons || !cfg.EnableMinify || !cfg.EnableWatch {
		t.Errorf("config = %+v", cfg)
	}
	w := p.Watcher()
	if w == nil || p.EnableWatch().Watcher() != w {
		t.Error("EnableWatch must create one watcher and keep it")
	}

	p.config.HotReload = true
	p.EnableProduction()
	cfg = p.GetConfig()
	if !cfg.EnableMinify || cfg.EnableWatch || cfg.HotReload {
		t.Errorf("after EnableProduction = %+v", cfg)
	}

	// GetConfig returns a copy.
	cfg.EnableMinify = false
	if !p.GetConfig().EnableMinify {
		t.Error("changing the GetConfig copy changed the pipeline")
	}
	if !reflect.DeepEqual(p.GetConfig(), *p.config) {
		t.Error("GetConfig differs from the pipeline's config")
	}
}
