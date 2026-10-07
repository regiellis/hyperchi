package assets

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// waitTimeout bounds how long a test waits on a goroutine. It is a failure
// deadline: tests continue as soon as the awaited signal arrives.
const waitTimeout = 10 * time.Second

// quietConfig returns a config whose logger writes to a buffer, so tests can
// assert on log output and keep the test log clean.
func quietConfig(src, out string) (*Config, *bytes.Buffer) {
	var buf bytes.Buffer
	return &Config{
		SourceDir: src,
		OutputDir: out,
		Logger:    slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}, &buf
}

// newTestWatcher returns a watcher over src alone, writing to out.
func newTestWatcher(t *testing.T, src, out string) (*Watcher, *Config) {
	t.Helper()
	cfg, logs := quietConfig(src, out)
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("watcher log:\n%s", logs.String())
		}
	})
	w := NewWatcher(cfg, NewWithConfig(cfg))
	w.dirs = []string{src}
	return w, cfg
}

func writeTestFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Exclusions match whole path segments, so .tmpl files and templates/ are
// watched even though they contain "tmp" and "temp".
func TestIsWatchableFileMatchesSegments(t *testing.T) {
	w := NewWatcher(&Config{}, nil)
	for path, want := range map[string]bool{
		"templates/index.tmpl":          true,
		"assets/templates/page.html":    true,
		"views/layout.tpl":              true,
		"assets/temporary/app.css":      true,
		"assets/contemporary.css":       true,
		"./assets/app.css":              true,
		"assets/../assets/app.js":       true,
		"assets/tmp/app.css":            false,
		"assets/temp/app.css":           false,
		"tmp/app.css":                   false,
		"node_modules/htmx/htmx.js":     false,
		"vendor/lib/x.js":               false,
		"assets/.cache/app.css":         false,
		".git/hooks/x.js":               false,
		"assets/.hidden.css":            false,
		"assets/tmp.css":                true, // a file named tmp is not a directory
		"assets/app.go":                 false,
		"assets/IMAGE.PNG":              true,
		filepath.Join("a", "b", "c.ts"): true,
	} {
		if got := w.isWatchableFile(path); got != want {
			t.Errorf("isWatchableFile(%q) = %t, want %t", path, got, want)
		}
	}
}

func TestWatcherCheckForChangesRebuilds(t *testing.T) {
	// The project sits under a directory named tmp: exclusions apply below the
	// watched directory, not to its ancestors.
	root := filepath.Join(t.TempDir(), "tmp", "project")
	src, out := filepath.Join(root, "src"), filepath.Join(root, "out")
	writeTestFile(t, filepath.Join(src, "css", "old.css"), "a{}")
	w, _ := newTestWatcher(t, src, out)

	lastMods := map[string]time.Time{}
	w.scanFiles(lastMods)
	if len(lastMods) != 1 {
		t.Fatalf("baseline = %v", lastMods)
	}
	w.checkForChanges(lastMods)
	if _, err := os.Stat(filepath.Join(out, "css", "old.css")); !os.IsNotExist(err) {
		t.Fatal("an unchanged file was rebuilt")
	}

	writeTestFile(t, filepath.Join(src, "css", "app.css"), "body { color: red; }")
	writeTestFile(t, filepath.Join(src, "js", "app.js"), "let a = 1")
	writeTestFile(t, filepath.Join(src, "templates", "page.tmpl"), "{{.}}")
	writeTestFile(t, filepath.Join(src, "notes.txt"), "ignored")
	writeTestFile(t, filepath.Join(src, "tmp", "scratch.css"), "ignored")
	w.checkForChanges(lastMods)

	for _, rel := range []string{"css/app.css", "js/app.js"} {
		if _, err := os.Stat(filepath.Join(out, filepath.FromSlash(rel))); err != nil {
			t.Errorf("%s not rebuilt: %v", rel, err)
		}
	}
	if _, ok := lastMods[filepath.Join(src, "templates", "page.tmpl")]; !ok {
		t.Error("a template under templates/ is not watched")
	}
	for _, rel := range []string{"notes.txt", "tmp/scratch.css"} {
		if _, ok := lastMods[filepath.Join(src, filepath.FromSlash(rel))]; ok {
			t.Errorf("%s is watched", rel)
		}
	}
}

func TestWatcherLogsRebuildFailures(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	cfg, logs := quietConfig(src, filepath.Join(root, "out"))
	cfg.EnableMinify = true
	w := NewWatcher(cfg, NewWithConfig(cfg))
	w.dirs = []string{src}
	writeTestFile(t, filepath.Join(src, "bad.js"), "const = = =;")
	w.checkForChanges(map[string]time.Time{})
	if !strings.Contains(logs.String(), "asset rebuild failed") || !strings.Contains(logs.String(), "bad.js") {
		t.Fatalf("log = %q, want the failed rebuild reported", logs.String())
	}
}

func TestWatcherStartStop(t *testing.T) {
	root := t.TempDir()
	src, out := filepath.Join(root, "src"), filepath.Join(root, "out")
	w, _ := newTestWatcher(t, src, out)
	w.interval = time.Millisecond
	polled := make(chan struct{}, 1)
	w.afterCheck = func() {
		select {
		case polled <- struct{}{}:
		default:
		}
	}

	if err := w.Start(); err != nil {
		t.Fatal(err)
	}
	if err := w.Start(); err == nil {
		t.Error("second Start succeeded")
	}
	if !w.IsRunning() {
		t.Error("IsRunning = false after Start")
	}

	// The poller takes its baseline before its first check; write after that
	// check so the new file cannot land in the baseline.
	deadline := time.After(waitTimeout)
	select {
	case <-polled:
	case <-deadline:
		t.Fatal("the watcher never polled")
	}
	writeTestFile(t, filepath.Join(src, "app.css"), "a { color: blue }")
	compiled := filepath.Join(out, "css", "app.css")
	for {
		if _, err := os.Stat(compiled); err == nil {
			break
		}
		select {
		case <-polled:
		case <-deadline:
			t.Fatal("the running watcher never compiled app.css")
		}
	}

	if err := w.Stop(); err != nil {
		t.Fatal(err)
	}
	if w.IsRunning() {
		t.Error("IsRunning = true after Stop")
	}
	if err := w.Stop(); err != nil {
		t.Fatalf("second Stop = %v", err)
	}
	// Stop waited for the poller, so a restart runs exactly one again.
	if err := w.Start(); err != nil {
		t.Fatalf("restart: %v", err)
	}
	if err := w.Stop(); err != nil {
		t.Fatal(err)
	}
}

func TestWatcherMissingDirectoryIsQuiet(t *testing.T) {
	cfg, logs := quietConfig(filepath.Join(t.TempDir(), "absent"), t.TempDir())
	w := NewWatcher(cfg, NewWithConfig(cfg))
	w.dirs = []string{cfg.SourceDir}
	w.scanFiles(map[string]time.Time{})
	if strings.Contains(logs.String(), "level=WARN") {
		t.Fatalf("a missing optional directory was reported: %q", logs.String())
	}
}

func TestWatcherDirectories(t *testing.T) {
	w := NewWatcher(&Config{SourceDir: "assets/"}, nil)
	if got, want := w.GetWatchedDirectories(), []string{"assets", "styles", "images"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("default dirs = %v, want %v (SourceDir deduplicated)", got, want)
	}
	if err := w.AddWatchDirectory("templates/"); err != nil {
		t.Fatal(err)
	}
	if err := w.AddWatchDirectory("templates"); err != nil {
		t.Fatal(err)
	}
	if err := w.AddWatchDirectory(""); err == nil {
		t.Error("empty directory accepted")
	}
	if err := w.RemoveWatchDirectory("styles"); err != nil {
		t.Fatal(err)
	}
	if err := w.RemoveWatchDirectory("styles"); err == nil {
		t.Error("removing an unwatched directory succeeded")
	}
	if got, want := w.GetWatchedDirectories(), []string{"assets", "images", "templates"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("dirs = %v, want %v", got, want)
	}
}

func TestHotReloadWebSocket(t *testing.T) {
	cfg, _ := quietConfig(t.TempDir(), t.TempDir())
	cfg.HotReload = true
	w := NewWatcher(cfg, NewWithConfig(cfg))
	w.dirs = nil // poll nothing: this test only needs Start and Stop
	if err := w.Start(); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(w.HandleWebSocket))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), waitTimeout)
	defer cancel()
	conn, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Body != nil {
		if err := resp.Body.Close(); err != nil {
			t.Fatal(err)
		}
	}

	var msg reloadMessage
	if err := wsjson.Read(ctx, conn, &msg); err != nil || msg.Type != "connected" {
		t.Fatalf("first message = %+v, %v; want connected", msg, err)
	}
	// The server registered the client before saying hello, so this reload
	// reaches it.
	w.triggerHotReload("css")
	if err := wsjson.Read(ctx, conn, &msg); err != nil {
		t.Fatal(err)
	}
	if msg.Type != "reload" || msg.AssetType != "css" || msg.Timestamp == 0 {
		t.Fatalf("reload message = %+v", msg)
	}

	// Stop closes the connection with "going away". The client reads while
	// Stop runs, as a browser does, so the close handshake completes.
	readErr := make(chan error, 1)
	go func() {
		_, _, err := conn.Read(ctx)
		readErr <- err
	}()
	if err := w.Stop(); err != nil {
		t.Fatal(err)
	}
	if err := <-readErr; websocket.CloseStatus(err) != websocket.StatusGoingAway {
		t.Fatalf("read after Stop = %v, want StatusGoingAway", err)
	}
}

func TestTriggerHotReloadDropsDeadClients(t *testing.T) {
	cfg, _ := quietConfig(t.TempDir(), t.TempDir())
	cfg.HotReload = true
	w := NewWatcher(cfg, nil)

	// A peer that accepts and then waits for the client to go away.
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(rw, r, nil)
		if err != nil {
			return
		}
		if _, _, err := c.Read(r.Context()); err == nil {
			t.Error("peer read a message, want the client to disconnect")
		}
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), waitTimeout)
	defer cancel()
	conn, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Body != nil {
		if err := resp.Body.Close(); err != nil {
			t.Fatal(err)
		}
	}
	// A closed connection makes every write fail, like a vanished browser.
	if err := conn.CloseNow(); err != nil {
		t.Fatal(err)
	}
	w.wsClients[conn] = true

	w.triggerHotReload("js")
	if n := len(w.wsClients); n != 0 {
		t.Fatalf("clients after a failed write = %d, want 0", n)
	}
}

func TestHotReloadDisabled(t *testing.T) {
	w := NewWatcher(&Config{}, nil)
	if w.GetHotReloadScript() != "" {
		t.Error("script served with HotReload off")
	}
	w.triggerHotReload("css") // no clients and no hot reload: must not panic

	w.config.HotReload = true
	if s := w.GetHotReloadScript(); !strings.Contains(s, "/__hyperchi/hot-reload") {
		t.Errorf("script = %q", s)
	}
}
