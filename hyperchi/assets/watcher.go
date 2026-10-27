// The watcher polls the asset source directories, recompiles or re-optimizes
// a file when it changes, and tells connected browsers to reload over a
// WebSocket.
//
//	watcher := NewWatcher(config, pipeline)
//	if err := watcher.Start(); err != nil {
//		log.Fatal(err)
//	}
//	http.HandleFunc("/__hyperchi/hot-reload", watcher.HandleWebSocket)
//	// ... and at shutdown:
//	if err := watcher.Stop(); err != nil {
//		log.Print(err)
//	}

package assets

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// defaultPollInterval is how often a started watcher scans for changes.
const defaultPollInterval = time.Second

// Watcher polls directories for changed assets, rebuilds them through its
// pipeline, and notifies hot-reload clients. Its methods are safe for
// concurrent use.
type Watcher struct {
	config   *Config
	pipeline *AssetPipeline

	mutex   sync.RWMutex
	running bool
	stop    chan struct{}
	done    chan struct{}
	dirs    []string

	interval   time.Duration // poll period, which tests shorten
	afterCheck func()        // test hook, called after each poll

	acceptOpts     *websocket.AcceptOptions
	wsClients      map[*websocket.Conn]bool
	wsClientsMutex sync.RWMutex
}

// NewWatcher creates a watcher that polls config.SourceDir plus "assets",
// "styles", and "images" in the working directory (duplicates removed).
// Change the list with AddWatchDirectory and RemoveWatchDirectory.
func NewWatcher(config *Config, pipeline *AssetPipeline) *Watcher {
	w := &Watcher{
		config:   config,
		pipeline: pipeline,
		interval: defaultPollInterval,
		// Same-origin only: the page that opens this socket is served by this
		// server, so a handshake from another site is a hijack attempt.
		acceptOpts: &websocket.AcceptOptions{},
		wsClients:  make(map[*websocket.Conn]bool),
	}
	for _, dir := range []string{config.SourceDir, "assets", "styles", "images"} {
		w.addDir(dir)
	}
	return w
}

// addDir appends dir unless it is empty or already listed. The caller holds
// w.mutex or owns w exclusively.
func (w *Watcher) addDir(dir string) bool {
	if dir == "" {
		return false
	}
	dir = filepath.Clean(dir)
	if slices.Contains(w.dirs, dir) {
		return false
	}
	w.dirs = append(w.dirs, dir)
	return true
}

// Start begins polling for changes in a new goroutine. Stop ends it.
func (w *Watcher) Start() error {
	w.mutex.Lock()
	defer w.mutex.Unlock()

	if w.running {
		return fmt.Errorf("watcher is already running")
	}
	w.running = true

	// Fresh channels per run: Stop closes them.
	w.stop = make(chan struct{})
	w.done = make(chan struct{})
	go w.pollForChanges(w.stop, w.done)

	w.config.logger().Info("asset watcher started", "dirs", w.dirs)
	return nil
}

// Stop ends polling, waits for the polling goroutine to exit, and closes the
// hot-reload connections. Stopping a watcher that is not running does nothing.
func (w *Watcher) Stop() error {
	w.mutex.Lock()
	if !w.running {
		w.mutex.Unlock()
		return nil
	}
	w.running = false
	// Close (never send): a send is lost when the poller is mid-scan.
	close(w.stop)
	done := w.done
	w.mutex.Unlock()

	// Wait without holding the lock: the poller reads the directory list.
	<-done

	w.wsClientsMutex.Lock()
	clients := w.wsClients
	w.wsClients = make(map[*websocket.Conn]bool)
	w.wsClientsMutex.Unlock()
	for conn := range clients {
		if err := conn.Close(websocket.StatusGoingAway, "watcher stopped"); err != nil {
			w.config.logger().Debug("closing hot reload connection", "err", err)
		}
	}

	w.config.logger().Info("asset watcher stopped")
	return nil
}

// pollForChanges scans on every tick until stop is closed, then closes done.
func (w *Watcher) pollForChanges(stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	lastMods := make(map[string]time.Time)
	w.scanFiles(lastMods)

	for {
		select {
		case <-ticker.C:
			w.checkForChanges(lastMods)
			if w.afterCheck != nil {
				w.afterCheck()
			}
		case <-stop:
			return
		}
	}
}

// watchDirs returns a copy of the directory list.
func (w *Watcher) watchDirs() []string {
	w.mutex.RLock()
	defer w.mutex.RUnlock()
	return slices.Clone(w.dirs)
}

// scanFiles records the modification time of every watched file without
// reacting to it.
func (w *Watcher) scanFiles(lastMods map[string]time.Time) {
	for _, dir := range w.watchDirs() {
		w.walk(dir, func(path string, info os.FileInfo) {
			lastMods[path] = info.ModTime()
		})
	}
}

// checkForChanges handles every watched file that is new or modified since
// the last scan.
func (w *Watcher) checkForChanges(lastMods map[string]time.Time) {
	for _, dir := range w.watchDirs() {
		w.walk(dir, func(path string, info os.FileInfo) {
			if lastMod, exists := lastMods[path]; !exists || info.ModTime().After(lastMod) {
				lastMods[path] = info.ModTime()
				w.handleFileChange(path)
			}
		})
	}
}

// walk calls fn for each watchable file under dir. Exclusions apply to the
// path below dir, so a project that itself lives under /tmp is still watched.
// A missing directory is skipped quietly, since most of the default
// directories are optional. Other walk errors are logged and the walk
// continues.
func (w *Watcher) walk(dir string, fn func(path string, info os.FileInfo)) {
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			if path == dir && os.IsNotExist(err) {
				return filepath.SkipDir
			}
			w.config.logger().Warn("asset watcher cannot read path", "path", path, "err", err)
			return nil
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			rel = path
		}
		if w.isWatchableFile(rel) {
			fn(path, info)
		}
		return nil
	})
	if err != nil {
		w.config.logger().Warn("asset watcher walk failed", "dir", dir, "err", err)
	}
}

// handleFileChange rebuilds path by its extension, or only reloads the page
// for a template. Sass and TypeScript files reach the compiler, which rejects
// them, so their change is logged as a failed rebuild.
func (w *Watcher) handleFileChange(path string) {
	w.config.logger().Debug("asset changed", "path", path)

	switch strings.ToLower(filepath.Ext(path)) {
	case ".css", ".scss", ".sass":
		w.rebuild(path, "css", w.pipeline.ProcessCSS)
	case ".js", ".ts":
		w.rebuild(path, "js", w.pipeline.ProcessJS)
	case ".svg", ".png", ".jpg", ".jpeg", ".gif", ".webp":
		w.rebuild(path, "image", w.pipeline.OptimizeImage)
	case ".html", ".tmpl", ".tpl":
		// Templates are not built here. The page only needs reloading.
		w.triggerHotReload("page")
	default:
		w.config.logger().Debug("ignoring change to unsupported file type", "path", path)
	}
}

// rebuild runs build on path and, when it succeeds and hot reload is on,
// tells browsers to reload assetType.
func (w *Watcher) rebuild(path, assetType string, build func(string) error) {
	if err := build(path); err != nil {
		w.config.logger().Error("asset rebuild failed", "path", path, "err", err)
		return
	}
	w.config.logger().Debug("asset rebuilt", "path", path)
	w.triggerHotReload(assetType)
}

// reloadMessage is what a hot-reload client receives.
type reloadMessage struct {
	Type      string `json:"type"`
	AssetType string `json:"assetType,omitempty"`
	Timestamp int64  `json:"timestamp,omitempty"`
}

// triggerHotReload sends a reload message to every client when HotReload is on.
func (w *Watcher) triggerHotReload(assetType string) {
	if !w.config.HotReload {
		return
	}

	message := reloadMessage{Type: "reload", AssetType: assetType, Timestamp: time.Now().Unix()}

	// Snapshot the client set under the lock, write outside it: each write
	// can block up to 5s, and holding the map lock across N slow clients
	// would stall connect/disconnect for the whole window.
	w.wsClientsMutex.RLock()
	clients := make([]*websocket.Conn, 0, len(w.wsClients))
	for client := range w.wsClients {
		clients = append(clients, client)
	}
	w.wsClientsMutex.RUnlock()

	var disconnected []*websocket.Conn
	for _, client := range clients {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := wsjson.Write(ctx, client, message)
		cancel()
		if err != nil {
			w.config.logger().Debug("hot reload client unreachable", "err", err)
			disconnected = append(disconnected, client)
		}
	}

	w.wsClientsMutex.Lock()
	for _, client := range disconnected {
		delete(w.wsClients, client)
	}
	notified := len(w.wsClients)
	w.wsClientsMutex.Unlock()
	for _, client := range disconnected {
		if err := client.CloseNow(); err != nil {
			w.config.logger().Debug("closing hot reload connection", "err", err)
		}
	}

	w.config.logger().Debug("hot reload triggered", "assetType", assetType, "clients", notified)
}

// HandleWebSocket serves a hot-reload client. Once the client is registered it
// receives {"type":"connected"}, then a {"type":"reload"} message after each
// rebuild. The handler returns when the client disconnects.
func (w *Watcher) HandleWebSocket(rw http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(rw, r, w.acceptOpts)
	if err != nil {
		w.config.logger().Warn("hot reload upgrade failed", "err", err)
		return
	}

	w.wsClientsMutex.Lock()
	w.wsClients[conn] = true
	total := len(w.wsClients)
	w.wsClientsMutex.Unlock()
	w.config.logger().Debug("hot reload client connected", "clients", total)

	defer func() {
		w.wsClientsMutex.Lock()
		delete(w.wsClients, conn)
		remaining := len(w.wsClients)
		w.wsClientsMutex.Unlock()
		if err := conn.CloseNow(); err != nil {
			w.config.logger().Debug("closing hot reload connection", "err", err)
		}
		w.config.logger().Debug("hot reload client disconnected", "clients", remaining)
	}()

	ctx := r.Context()
	hello, cancel := context.WithTimeout(ctx, 5*time.Second)
	err = wsjson.Write(hello, conn, reloadMessage{Type: "connected"})
	cancel()
	if err != nil {
		return
	}

	// Keep the connection open. The read loop ends when the client goes away.
	for {
		if _, _, err := conn.Read(ctx); err != nil {
			return
		}
	}
}

// GetHotReloadScript returns a <script> element that connects to
// /__hyperchi/hot-reload, refreshes stylesheets on a CSS reload, and reloads
// the page for any other asset. It returns "" when HotReload is off.
func (w *Watcher) GetHotReloadScript() string {
	if !w.config.HotReload {
		return ""
	}

	return `
<script>
(function() {
	if (typeof window.hyperchiHotReload !== 'undefined') {
		return; // Already loaded
	}

	window.hyperchiHotReload = true;

	function connectWebSocket() {
		const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
		const ws = new WebSocket(protocol + '//' + window.location.host + '/__hyperchi/hot-reload');

		ws.onopen = function() {
			console.log('[HyperChi] Hot reload connected');
		};

		ws.onmessage = function(event) {
			const data = JSON.parse(event.data);
			if (data.type === 'reload') {
				console.log('[HyperChi] Hot reload triggered for:', data.assetType);

				if (data.assetType === 'css') {
					// Reload CSS files
					const links = document.querySelectorAll('link[rel="stylesheet"]');
					links.forEach(function(link) {
						const href = link.href.split('?')[0];
						link.href = href + '?t=' + data.timestamp;
					});
				} else {
					// Reload entire page for JS, images, or other assets
					window.location.reload();
				}
			}
		};

		ws.onclose = function() {
			console.log('[HyperChi] Hot reload disconnected, attempting to reconnect...');
			setTimeout(connectWebSocket, 1000);
		};

		ws.onerror = function(error) {
			console.error('[HyperChi] Hot reload error:', error);
		};
	}

	// Start connection when DOM is ready
	if (document.readyState === 'loading') {
		document.addEventListener('DOMContentLoaded', connectWebSocket);
	} else {
		connectWebSocket();
	}
})();
</script>`
}

// skipSegments are directory names whose contents are never watched.
var skipSegments = map[string]bool{
	"node_modules": true,
	"vendor":       true,
	"tmp":          true,
	"temp":         true,
}

// watchableExts are the extensions the watcher tracks.
var watchableExts = map[string]bool{
	".css": true, ".scss": true, ".sass": true, ".less": true,
	".js": true, ".ts": true, ".jsx": true, ".tsx": true,
	".svg": true, ".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".bmp": true,
	".html": true, ".tmpl": true, ".tpl": true,
}

// isWatchableFile reports whether path, relative to a watched directory,
// should be watched. It compares whole path segments: a directory named
// node_modules, vendor, tmp, or temp, or any hidden segment (".git", ".cache",
// ".file.css"), excludes the path, while names that merely contain those words
// (templates/, index.tmpl) do not.
func (w *Watcher) isWatchableFile(path string) bool {
	segments := strings.Split(filepath.ToSlash(path), "/")
	for i, seg := range segments {
		if seg == "" || seg == "." || seg == ".." {
			continue
		}
		if strings.HasPrefix(seg, ".") {
			return false
		}
		if i < len(segments)-1 && skipSegments[seg] {
			return false
		}
	}
	return watchableExts[strings.ToLower(filepath.Ext(path))]
}

// IsRunning reports whether the watcher is polling.
func (w *Watcher) IsRunning() bool {
	w.mutex.RLock()
	defer w.mutex.RUnlock()
	return w.running
}

// GetWatchedDirectories returns a copy of the watched directory list.
func (w *Watcher) GetWatchedDirectories() []string {
	return w.watchDirs()
}

// AddWatchDirectory adds dir to the watched directories. A running watcher
// picks it up on its next poll and treats the files already in it as new.
func (w *Watcher) AddWatchDirectory(dir string) error {
	if dir == "" {
		return fmt.Errorf("watch directory must not be empty")
	}
	w.mutex.Lock()
	defer w.mutex.Unlock()
	w.addDir(dir)
	return nil
}

// RemoveWatchDirectory stops watching dir. It returns an error if dir is not
// being watched.
func (w *Watcher) RemoveWatchDirectory(dir string) error {
	clean := filepath.Clean(dir)
	w.mutex.Lock()
	defer w.mutex.Unlock()
	i := slices.Index(w.dirs, clean)
	if i < 0 {
		return fmt.Errorf("directory %s is not being watched", dir)
	}
	w.dirs = slices.Delete(w.dirs, i, i+1)
	return nil
}
