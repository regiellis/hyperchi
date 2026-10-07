package main

import (
	"context"
	"time"

	"github.com/regiellis/hyperchi/hyperchi/content"
)

// watcher feeds file changes from the framework's polling FileWatcher into
// the index. The FileWatcher compares size and modification time on each
// pass, so it needs no platform notification support and works on network
// mounts. It skips hidden entries (.git, .env) and node_modules, so those
// never reach the index.
type watcher struct {
	ix *Index
	fw *content.FileWatcher
}

func newWatcher(ix *Index) *watcher {
	return &watcher{ix: ix, fw: content.NewFileWatcher(ix.dir)}
}

// baseline records the current tree, so later passes report only changes.
func (w *watcher) baseline() error {
	_, err := w.fw.Scan()
	return err
}

// step runs one watch pass by hand. Tests call it instead of waiting on run.
func (w *watcher) step(ctx context.Context) {
	if changed, err := w.fw.Scan(); len(changed) > 0 || err != nil {
		w.ix.Apply(ctx, changed, err)
	}
}

// run polls every interval until ctx is done.
func (w *watcher) run(ctx context.Context, interval time.Duration) error {
	return w.fw.Watch(ctx, interval, func(changed []string, err error) {
		w.ix.Apply(ctx, changed, err)
	})
}
