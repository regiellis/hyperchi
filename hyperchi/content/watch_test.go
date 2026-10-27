package content

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// waitTimeout bounds how long a test waits for the watcher goroutine. It is a
// failure deadline, not a synchronization delay: tests proceed as soon as the
// channel they wait on delivers.
const waitTimeout = 10 * time.Second

func TestFileWatcherScan(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.md", "a")
	writeFile(t, root, "sub/b.md", "b")
	fw := NewFileWatcher(root)

	changed, err := fw.Scan()
	if err != nil || changed != nil {
		t.Fatalf("baseline scan = %v, %v; want no paths", changed, err)
	}
	if changed, err := fw.Scan(); err != nil || changed != nil {
		t.Fatalf("unchanged tree = %v, %v", changed, err)
	}

	writeFile(t, root, "sub/b.md", "b grew")
	writeFile(t, root, "c.md", "new")
	if err := os.Remove(filepath.Join(root, "a.md")); err != nil {
		t.Fatal(err)
	}
	changed, err = fw.Scan()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(root, "a.md"), filepath.Join(root, "c.md"), filepath.Join(root, "sub", "b.md")}
	if !reflect.DeepEqual(changed, want) {
		t.Fatalf("changed = %v, want %v (created, modified, removed; sorted)", changed, want)
	}
	if changed, err := fw.Scan(); err != nil || changed != nil {
		t.Fatalf("second scan after changes = %v, %v", changed, err)
	}
}

func TestFileWatcherScanMissingRootIsEmpty(t *testing.T) {
	root := filepath.Join(t.TempDir(), "later")
	fw := NewFileWatcher(root)
	if changed, err := fw.Scan(); err != nil || changed != nil {
		t.Fatalf("missing root = %v, %v; want an empty baseline", changed, err)
	}
	writeFile(t, root, "posts/a.md", "a")
	changed, err := fw.Scan()
	if err != nil || !reflect.DeepEqual(changed, []string{filepath.Join(root, "posts", "a.md")}) {
		t.Fatalf("after the root appears = %v, %v", changed, err)
	}
}

// Scan reports an unreadable directory as an error and does not mistake the
// files it can no longer see for deletions.
func TestFileWatcherScanReportsWalkErrors(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions, so the walk cannot fail here")
	}
	root := t.TempDir()
	writeFile(t, root, "locked/a.md", "a")
	writeFile(t, root, "open/b.md", "b")
	fw := NewFileWatcher(root)
	if _, err := fw.Scan(); err != nil {
		t.Fatal(err)
	}

	locked := filepath.Join(root, "locked")
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(locked, 0o755); err != nil {
			t.Error(err)
		}
	})
	writeFile(t, root, "open/b.md", "b changed")

	changed, err := fw.Scan()
	if err == nil || !strings.Contains(err.Error(), "locked") {
		t.Fatalf("err = %v, want the unreadable directory reported", err)
	}
	if want := []string{filepath.Join(root, "open", "b.md")}; !reflect.DeepEqual(changed, want) {
		t.Fatalf("changed = %v, want only %v (locked/a.md is unreadable, not removed)", changed, want)
	}
}

func TestFileWatcherWatchStopsOnCancel(t *testing.T) {
	root := t.TempDir()
	fw := NewFileWatcher(root)
	if _, err := fw.Scan(); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	got := make(chan []string, 16)
	done := make(chan error, 1)
	go func() {
		done <- fw.Watch(ctx, time.Millisecond, func(changed []string, err error) {
			if err != nil {
				t.Errorf("scan error: %v", err)
			}
			got <- changed
		})
	}()

	writeFile(t, root, "a.md", "a")
	select {
	case changed := <-got:
		if !reflect.DeepEqual(changed, []string{filepath.Join(root, "a.md")}) {
			t.Errorf("changed = %v", changed)
		}
	case <-time.After(waitTimeout):
		t.Fatal("watcher never reported the new file")
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Watch returned %v after cancel, want nil", err)
		}
	case <-time.After(waitTimeout):
		t.Fatal("Watch did not return after its context was cancelled")
	}
}

func TestFileWatcherWatchRejectsBadArguments(t *testing.T) {
	fw := NewFileWatcher(t.TempDir())
	ctx := context.Background()
	if err := fw.Watch(ctx, 0, func([]string, error) {}); err == nil {
		t.Error("zero interval accepted")
	}
	if err := fw.Watch(ctx, time.Second, nil); err == nil {
		t.Error("nil callback accepted")
	}
}

// reloadEvent is one OnReload call.
type reloadEvent struct {
	changed []string
	err     error
}

// With WatchForChanges, LoadContent starts a watcher that reloads the layer,
// and Close stops it.
func TestContentLayerWatchReloadsOnChange(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "posts/a.md", "---\ntitle: A\n---\n")
	events := make(chan reloadEvent, 64)
	cl := NewContentLayer(&ContentConfig{
		BaseDir:         root,
		WatchForChanges: true,
		WatchInterval:   time.Millisecond,
		OnReload: func(changed []string, err error) {
			events <- reloadEvent{changed, err}
		},
	})
	if err := cl.LoadContent(); err != nil {
		t.Fatal(err)
	}

	// Readers run during the reloads, so -race checks the collection swap.
	stop := make(chan struct{})
	var readers sync.WaitGroup
	readers.Add(1)
	go func() {
		defer readers.Done()
		for {
			select {
			case <-stop:
				return
			default:
				if posts, ok := cl.GetCollection("posts"); ok {
					posts.GetAllContent()
				}
				cl.GetAllCollections()
			}
		}
	}()

	writeFile(t, root, "posts/b.md", "---\ntitle: B\n---\n")
	deadline := time.After(waitTimeout)
	for loaded := false; !loaded; {
		select {
		case ev := <-events:
			if ev.err != nil {
				t.Fatalf("reload error: %v", ev.err)
			}
			if len(ev.changed) == 0 || !strings.HasSuffix(ev.changed[0], "b.md") {
				t.Fatalf("changed = %v, want posts/b.md", ev.changed)
			}
			posts, _ := cl.GetCollection("posts")
			if c, ok := posts.GetContent("posts-b"); ok && c.GetString("title") == "B" {
				loaded = true
			}
		case <-deadline:
			t.Fatal("the watcher never reloaded posts/b.md")
		}
	}

	close(stop)
	readers.Wait()
	if err := cl.Close(); err != nil {
		t.Fatal(err)
	}
	if err := cl.Close(); err != nil {
		t.Fatalf("second Close = %v", err)
	}

	// Close waited for the goroutine, so nothing sends now. Drop what it left.
	for len(events) > 0 {
		<-events
	}

	// After Close, LoadContent starts a fresh watcher.
	if err := cl.LoadContent(); err != nil {
		t.Fatal(err)
	}
	writeFile(t, root, "posts/c.md", "c")
	select {
	case ev := <-events:
		if ev.err != nil || len(ev.changed) != 1 || !strings.HasSuffix(ev.changed[0], "c.md") {
			t.Fatalf("event after restart = %+v, want posts/c.md", ev)
		}
	case <-time.After(waitTimeout):
		t.Fatal("restarted watcher never reported posts/c.md")
	}
	if err := cl.Close(); err != nil {
		t.Fatal(err)
	}
}

// handleChanges is the scan step the watcher runs. Calling it directly keeps
// this test free of timing.
func TestContentLayerHandleChanges(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "posts/a.md", "---\ntitle: First\n---\n")
	var events []reloadEvent
	cl := NewContentLayer(&ContentConfig{
		BaseDir:  root,
		OnReload: func(changed []string, err error) { events = append(events, reloadEvent{changed, err}) },
	})
	if err := cl.LoadContent(); err != nil {
		t.Fatal(err)
	}

	writeFile(t, root, "posts/a.md", "---\ntitle: Edited\n---\n")
	cl.handleChanges([]string{filepath.Join(root, "posts", ".a.md.swp")}, nil)
	if len(events) != 0 {
		t.Fatalf("a non-content file triggered a reload: %+v", events)
	}
	posts, _ := cl.GetCollection("posts")
	if posts.Contents["posts-a"].GetString("title") != "First" {
		t.Fatal("content reloaded for a non-content change")
	}

	a := filepath.Join(root, "posts", "a.md")
	cl.handleChanges([]string{a, filepath.Join(root, "posts", "image.png")}, nil)
	if len(events) != 1 || !reflect.DeepEqual(events[0].changed, []string{a}) || events[0].err != nil {
		t.Fatalf("events = %+v, want one reload for a.md", events)
	}
	posts, _ = cl.GetCollection("posts")
	if got := posts.Contents["posts-a"].GetString("title"); got != "Edited" {
		t.Fatalf("title after reload = %q", got)
	}

	scanErr := errors.New("walk failed")
	cl.handleChanges(nil, scanErr)
	if len(events) != 2 || events[1].changed != nil || !errors.Is(events[1].err, scanErr) {
		t.Fatalf("scan error event = %+v", events)
	}
	cl.handleChanges([]string{a}, scanErr)
	if len(events) != 3 || !errors.Is(events[2].err, scanErr) {
		t.Fatalf("scan error alongside a change = %+v", events)
	}
}

// Without OnReload, reload errors go to slog.Default rather than stdout.
func TestContentLayerReportLogsErrors(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	cl := NewContentLayer(&ContentConfig{BaseDir: "somewhere"})
	cl.report([]string{"x.md"}, nil)
	if buf.Len() != 0 {
		t.Fatalf("a clean reload was logged: %q", buf.String())
	}
	cl.report(nil, errors.New("disk on fire"))
	if out := buf.String(); !strings.Contains(out, "disk on fire") || !strings.Contains(out, "somewhere") {
		t.Fatalf("log = %q, want the error and the directory", out)
	}
}

func TestContentLayerReloadDropsVanishedCollections(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "posts/a.md", "a")
	writeFile(t, root, "drafts/d.md", "d")
	writeFile(t, root, "blog/b.md", "---\nauthor: ann\n---\n")
	cl := NewContentLayer(&ContentConfig{BaseDir: root})
	cl.DefineCollection(CollectionConfig{Name: "blog", Path: "custom/blog", Type: ContentTypeMDX})
	manual := cl.DefineCollection(CollectionConfig{Name: "manual"})
	if err := manual.AddContent(&Content{ID: "m"}); err != nil {
		t.Fatal(err)
	}
	if err := cl.LoadContent(); err != nil {
		t.Fatal(err)
	}

	for _, dir := range []string{"drafts", "blog"} {
		if err := os.RemoveAll(filepath.Join(root, dir)); err != nil {
			t.Fatal(err)
		}
	}
	if err := cl.reloadContent(); err != nil {
		t.Fatal(err)
	}
	if _, ok := cl.GetCollection("drafts"); ok {
		t.Error("a loaded collection whose directory is gone survived the reload")
	}
	blog, ok := cl.GetCollection("blog")
	if !ok || len(blog.Contents) != 0 || blog.Path != "custom/blog" || blog.Type != ContentTypeMDX {
		t.Errorf("declared blog after its files vanished = %+v, want empty with its declaration", blog)
	}
	if m, ok := cl.GetCollection("manual"); !ok || len(m.Contents) != 1 {
		t.Error("a declared collection never loaded from disk lost its content")
	}
	if _, ok := cl.GetCollection("posts"); !ok {
		t.Error("posts disappeared")
	}
}

// A collection's type is the type most of its files have, not the type of
// whichever file the walk reads first.
func TestLoadAllCollectionTypeIsMajority(t *testing.T) {
	root := t.TempDir()
	// a.html sorts first, but two of the three files are Markdown.
	writeFile(t, root, "posts/a.html", "<p>a</p>")
	writeFile(t, root, "posts/b.md", "b")
	writeFile(t, root, "posts/c.md", "c")
	writeFile(t, root, "notes/a.txt", "a")
	writeFile(t, root, "notes/b.txt", "b")
	writeFile(t, root, "notes/c.mdx", "c")

	got, err := (&ContentLoader{baseDir: root}).LoadAll()
	if err != nil {
		t.Fatal(err)
	}
	if got["posts"].Type != ContentTypeMarkdown || got["notes"].Type != ContentTypeText {
		t.Fatalf("types = posts %q, notes %q; want md and txt", got["posts"].Type, got["notes"].Type)
	}

	// A declared collection without a Type takes the loaded one. An explicit
	// Type wins.
	cl := NewContentLayer(&ContentConfig{BaseDir: root})
	cl.DefineCollection(CollectionConfig{Name: "posts"})
	cl.DefineCollection(CollectionConfig{Name: "notes", Type: ContentTypeHTML})
	if err := cl.LoadContent(); err != nil {
		t.Fatal(err)
	}
	posts, _ := cl.GetCollection("posts")
	notes, _ := cl.GetCollection("notes")
	if posts.Type != ContentTypeMarkdown || notes.Type != ContentTypeHTML {
		t.Fatalf("declared types = posts %q, notes %q", posts.Type, notes.Type)
	}
}

func TestMajorityType(t *testing.T) {
	for _, tc := range []struct {
		counts map[ContentType]int
		want   ContentType
	}{
		{nil, ContentTypeMarkdown},
		{map[ContentType]int{ContentTypeHTML: 2, ContentTypeMarkdown: 1}, ContentTypeHTML},
		{map[ContentType]int{ContentTypeText: 1, ContentTypeHTML: 1}, ContentTypeHTML},
		{map[ContentType]int{ContentTypeMDX: 3, ContentTypeMarkdown: 3}, ContentTypeMarkdown},
		{map[ContentType]int{ContentTypeText: 4, ContentTypeMDX: 3}, ContentTypeText},
	} {
		if got := majorityType(tc.counts); got != tc.want {
			t.Errorf("majorityType(%v) = %q, want %q", tc.counts, got, tc.want)
		}
	}
}
