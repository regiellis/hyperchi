package helpers

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

// mustRead returns the content of path, failing the test when it cannot be
// read.
func mustRead(t *testing.T, path string) string {
	t.Helper()
	content, err := ReadFile(path)
	if err != nil {
		t.Fatalf("mustRead(t, %s): %v", path, err)
	}
	return content
}

func sorted(paths []string) []string {
	out := append([]string(nil), paths...)
	sort.Strings(out)
	return out
}

func TestReadWriteAppend(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a", "b", "note.txt")

	if err := WriteFile(path, "héllo\nworld"); err != nil {
		t.Fatal(err)
	}
	if mustRead(t, path) != "héllo\nworld" || ReadFileOr(path, "d") != "héllo\nworld" {
		t.Error("ReadFile after WriteFile")
	}
	if err := AppendLine(path, "\nthird"); err != nil {
		t.Fatal(err)
	}
	if err := AppendLine(path, "fourth\n"); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadFileLines(path); err != nil || !reflect.DeepEqual(got, []string{"héllo", "world", "third", "fourth"}) {
		t.Errorf("ReadFileLines = %q, %v", got, err)
	}

	missing := filepath.Join(dir, "missing.txt")
	if ReadFileOr(missing, "d") != "d" {
		t.Error("ReadFileOr falls back on a missing file")
	}
	if got, err := ReadFileLines(missing); err == nil || got != nil {
		t.Errorf("ReadFileLines(missing) = %q, %v; want an error", got, err)
	}
	if err := AppendFile(filepath.Join(dir, "no", "such", "dir.txt"), "x"); err == nil {
		t.Error("AppendFile does not create parent directories")
	}
	created := filepath.Join(dir, "created.txt")
	if err := AppendFile(created, "x"); err != nil || mustRead(t, created) != "x" {
		t.Error("AppendFile creates a missing file")
	}

	// A path whose parent is a regular file cannot be written.
	if err := WriteFile(filepath.Join(path, "child"), "x"); err == nil {
		t.Error("WriteFile under a file accepted")
	}
}

func TestFileStat(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "f.txt")
	if err := WriteFile(file, "12345"); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "nope")

	if !FileExists(file) || !FileExists(dir) || FileExists(missing) || !FileNotExists(missing) {
		t.Error("FileExists")
	}
	if !IsFile(file) || IsFile(dir) || IsFile(missing) {
		t.Error("IsFile")
	}
	if !IsDir(dir) || IsDir(file) || IsDir(missing) {
		t.Error("IsDir")
	}
	if FileSize(file) != 5 || FileSize(missing) != 0 {
		t.Error("FileSize")
	}
}

func TestCopyMoveRemove(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	if err := WriteFile(src, "data"); err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(dir, "copies", "dst.txt")
	if err := CopyFile(src, dst); err != nil || mustRead(t, dst) != "data" || !FileExists(src) {
		t.Fatalf("CopyFile: %v", err)
	}
	if err := CopyFile(filepath.Join(dir, "missing"), dst); err == nil {
		t.Error("CopyFile from a missing source")
	}
	if err := CopyFile(src, filepath.Join(src, "under-file")); err == nil {
		t.Error("CopyFile to a path under a file")
	}

	moved := filepath.Join(dir, "moved", "m.txt")
	if err := MoveFile(dst, moved); err != nil || FileExists(dst) || mustRead(t, moved) != "data" {
		t.Fatalf("MoveFile: %v", err)
	}
	if err := MoveFile(filepath.Join(dir, "missing"), moved); err == nil {
		t.Error("MoveFile from a missing source")
	}

	if err := RemoveFile(moved); err != nil || FileExists(moved) {
		t.Error("RemoveFile")
	}
	if err := RemoveFile(moved); err == nil {
		t.Error("RemoveFile twice")
	}
	if err := CreateDir(filepath.Join(dir, "tree", "leaf")); err != nil || !IsDir(filepath.Join(dir, "tree", "leaf")) {
		t.Error("CreateDir")
	}
	if err := RemoveDir(filepath.Join(dir, "tree")); err != nil || FileExists(filepath.Join(dir, "tree")) {
		t.Error("RemoveDir")
	}
}

func TestListingAndWalking(t *testing.T) {
	root := t.TempDir()
	for _, f := range []string{"a.go", "b.txt", "sub/c.go", "sub/deep/d.go", "other/e.md"} {
		if err := WriteFile(filepath.Join(root, f), f); err != nil {
			t.Fatal(err)
		}
	}
	j := func(parts ...string) string { return filepath.Join(append([]string{root}, parts...)...) }

	if got := sorted(ListFiles(root)); !reflect.DeepEqual(got, []string{j("a.go"), j("b.txt")}) {
		t.Errorf("ListFiles = %v", got)
	}
	if got := sorted(ListDirs(root)); !reflect.DeepEqual(got, []string{j("other"), j("sub")}) {
		t.Errorf("ListDirs = %v", got)
	}
	if got := len(ListAll(root)); got != 4 {
		t.Errorf("ListAll count = %d", got)
	}
	if got := sorted(WalkFiles(root)); !reflect.DeepEqual(got, []string{j("a.go"), j("b.txt"), j("other", "e.md"), j("sub", "c.go"), j("sub", "deep", "d.go")}) {
		t.Errorf("WalkFiles = %v", got)
	}
	if got := sorted(WalkDirs(root)); !reflect.DeepEqual(got, []string{j("other"), j("sub"), j("sub", "deep")}) {
		t.Errorf("WalkDirs = %v", got)
	}
	if got := sorted(FindFiles(root, "*.go")); !reflect.DeepEqual(got, []string{j("a.go"), j("sub", "c.go"), j("sub", "deep", "d.go")}) {
		t.Errorf("FindFiles = %v", got)
	}
	if got := FindFiles(root, "[bad"); len(got) != 0 {
		t.Errorf("FindFiles with a malformed pattern = %v", got)
	}

	missing := j("missing")
	for name, got := range map[string][]string{"ListFiles": ListFiles(missing), "ListDirs": ListDirs(missing), "ListAll": ListAll(missing)} {
		if got == nil || len(got) != 0 {
			t.Errorf("%s on a missing dir = %#v, want empty", name, got)
		}
	}
	if len(WalkFiles(missing)) != 0 || len(WalkDirs(missing)) != 0 || len(FindFiles(missing, "*")) != 0 {
		t.Error("walking a missing dir")
	}
}

func TestTempFileAndDir(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	path, err := TempFile("hc-*.txt")
	if err != nil || !IsFile(path) {
		t.Fatalf("TempFile = %q, %v", path, err)
	}
	dir, err := TempDir("hc-*")
	if err != nil || !IsDir(dir) {
		t.Fatalf("TempDir = %q, %v", dir, err)
	}
	if _, err := TempFile("bad/pattern"); err == nil {
		t.Error("TempFile with a separator in the pattern")
	}
}

func TestFileHelperChain(t *testing.T) {
	dir := t.TempDir()
	f := File(filepath.Join(dir, "x", "f.txt")).Write("one").Append("+two")
	if f.Error() != nil || !f.Exists() || f.Size() != 7 || f.ReadOr("d") != "one+two" {
		t.Fatalf("Write/Append: %v", f.Error())
	}
	f.Copy(filepath.Join(dir, "copy.txt")).Move(filepath.Join(dir, "moved.txt"))
	if f.Error() != nil || f.Path() != filepath.Join(dir, "moved.txt") || mustRead(t, filepath.Join(dir, "copy.txt")) != "one+two" {
		t.Fatalf("Copy/Move: %v path %s", f.Error(), f.Path())
	}
	if f.Delete().Error() != nil || f.Exists() {
		t.Error("Delete")
	}

	// The first error sticks and later steps are skipped.
	bad := File(filepath.Join(dir, "missing.txt")).Copy(filepath.Join(dir, "never.txt"))
	if bad.Error() == nil {
		t.Fatal("Copy of a missing file")
	}
	bad.Write("x").Append("y").Move(filepath.Join(dir, "m")).Copy(filepath.Join(dir, "c")).Delete()
	if FileExists(filepath.Join(dir, "missing.txt")) || FileExists(filepath.Join(dir, "never.txt")) {
		t.Error("steps after an error should not run")
	}
	if _, err := bad.Read(); err == nil || bad.ReadOr("d") != "d" {
		t.Error("Read after an error")
	}
	if File(filepath.Join(dir, "nope")).Move(filepath.Join(dir, "dest")).Path() != filepath.Join(dir, "nope") {
		t.Error("a failed Move keeps the original path")
	}
}

func TestDirHelperChain(t *testing.T) {
	root := filepath.Join(t.TempDir(), "proj")
	d := Dir(root).Create()
	if d.Error() != nil || !d.Exists() || d.Path() != root {
		t.Fatalf("Create: %v", d.Error())
	}
	for _, f := range []string{"a.go", "sub/b.go", "sub/c.txt"} {
		if err := WriteFile(filepath.Join(root, f), ""); err != nil {
			t.Fatal(err)
		}
	}
	list, err := d.List()
	if err != nil || len(list) != 2 {
		t.Errorf("List = %v, %v", list, err)
	}
	if len(d.Files()) != 1 || len(d.Dirs()) != 1 || len(d.Walk()) != 3 || len(d.Find("*.go")) != 2 {
		t.Error("Files/Dirs/Walk/Find")
	}
	if d.Remove().Error() != nil || d.Exists() {
		t.Error("Remove")
	}

	parent := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(parent, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	bad := Dir(filepath.Join(parent, "child")).Create()
	if bad.Error() == nil {
		t.Fatal("Create under a file")
	}
	if _, err := bad.List(); err == nil {
		t.Error("List after an error")
	}
	if bad.Remove().Error() == nil {
		t.Error("error should stick through Remove")
	}
}
