package content

import (
	"os"
	"path/filepath"
	"testing"
)

// A content directory under version control has .git (thousands of objects)
// and may have node_modules; the watcher must not walk or report them.
func TestFileWatcherSkipsHiddenAndVendoredTrees(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"post.md", ".git/objects/ab/cdef", ".DS_Store", "node_modules/x/index.js", "drafts/.hidden.md", "drafts/next.md"} {
		path := filepath.Join(dir, f)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	fw := NewFileWatcher(dir)
	if _, err := fw.Scan(); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{".git/objects/ab/cdef", ".DS_Store", "node_modules/x/index.js", "drafts/.hidden.md"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("changed"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "drafts", "next.md"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := fw.Scan()
	if err != nil {
		t.Fatal(err)
	}
	if len(changed) != 1 || filepath.Base(changed[0]) != "next.md" {
		t.Fatalf("changed = %v, want only drafts/next.md", changed)
	}
}
