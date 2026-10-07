package helpers

import (
	"bufio"
	"bytes"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// File and directory helpers. Functions that can fail return an error; the
// Or variants (ReadFileOr) fall back on any error instead.

// ReadFile reads a whole file as a string.
func ReadFile(filename string) (string, error) {
	content, err := os.ReadFile(filename)
	if err != nil {
		return "", err
	}
	return string(content), nil
}

// ReadFileOr reads a file, returning defaultContent when it cannot be read
// for any reason (missing, unreadable, a directory). Use ReadFile when the
// caller must tell those cases apart.
func ReadFileOr(filename, defaultContent string) string {
	content, err := ReadFile(filename)
	if err != nil {
		return defaultContent
	}
	return content
}

// ReadFileLines reads a file and returns its lines without line endings
// ("\n" or "\r\n"). An empty file returns an empty, non-nil slice. Lines
// may be any length.
func ReadFileLines(filename string) ([]string, error) {
	content, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	lines := []string{}
	scanner := bufio.NewScanner(bytes.NewReader(content))
	scanner.Buffer(make([]byte, 0, min(len(content)+1, 64*1024)), len(content)+1)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return lines, nil
}

// WriteFile writes content to filename with mode 0644, creating parent
// directories as needed. An existing file is truncated.
func WriteFile(filename, content string) error {
	dir := filepath.Dir(filename)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	return os.WriteFile(filename, []byte(content), 0644)
}

// writeSecretFile writes content readable and writable by the owner only
// (0600), creating parent directories. The mode is applied even when the file
// already existed, so a .env file never keeps a looser mode from before.
func writeSecretFile(filename, content string) error {
	if err := os.MkdirAll(filepath.Dir(filename), 0755); err != nil {
		return err
	}
	if err := os.WriteFile(filename, []byte(content), 0600); err != nil {
		return err
	}
	return os.Chmod(filename, 0600)
}

// AppendFile appends content to filename, creating it with mode 0644 if it
// does not exist.
func AppendFile(filename, content string) error {
	file, err := os.OpenFile(filename, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}

	_, writeErr := file.WriteString(content)
	// A close error after a write can mean the data never reached disk, so
	// it is returned, not discarded.
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

// AppendLine appends line to filename, adding a trailing newline when line
// lacks one.
func AppendLine(filename, line string) error {
	if !strings.HasSuffix(line, "\n") {
		line += "\n"
	}
	return AppendFile(filename, line)
}

// FileExists reports whether filename can be stat'd. It is true for a
// directory too; use IsFile to require a non-directory.
func FileExists(filename string) bool {
	_, err := os.Stat(filename)
	return err == nil
}

// FileNotExists is !FileExists(filename).
func FileNotExists(filename string) bool {
	return !FileExists(filename)
}

// IsDir reports whether path exists and is a directory.
func IsDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// IsFile reports whether path exists and is not a directory.
func IsFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// FileSize returns the size of filename in bytes, or 0 when it cannot be
// stat'd.
func FileSize(filename string) int64 {
	info, err := os.Stat(filename)
	if err != nil {
		return 0
	}
	return info.Size()
}

// CreateDir creates path and any missing parents with mode 0755. An
// existing directory is not an error.
func CreateDir(path string) error {
	return os.MkdirAll(path, 0755)
}

// RemoveFile removes filename, or an empty directory.
func RemoveFile(filename string) error {
	return os.Remove(filename)
}

// RemoveDir removes path and everything under it. A missing path is not an
// error.
func RemoveDir(path string) error {
	return os.RemoveAll(path)
}

// CopyFile copies the contents of src to dst, creating dst's parent
// directories and truncating an existing dst. File mode is not copied.
func CopyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	// The source is read-only, so a close error cannot lose data.
	defer func() {
		if cerr := sourceFile.Close(); cerr != nil {
			log.Printf("hyperchi: failed to close source file %s: %v", src, cerr)
		}
	}()

	if err := CreateDir(filepath.Dir(dst)); err != nil {
		return err
	}

	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}

	_, copyErr := io.Copy(destFile, sourceFile)
	// A close error after a write can mean the copy never reached disk, so
	// it is returned, not discarded.
	closeErr := destFile.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

// MoveFile renames src to dst, creating dst's parent directories. It uses
// os.Rename, so it fails across filesystems.
func MoveFile(src, dst string) error {
	if err := CreateDir(filepath.Dir(dst)); err != nil {
		return err
	}

	return os.Rename(src, dst)
}

// ListFiles returns the paths (dir joined with the name) of the
// non-directory entries in dir, not recursing. It returns an empty slice
// when dir cannot be read.
func ListFiles(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return []string{}
	}

	var files []string
	for _, entry := range entries {
		if !entry.IsDir() {
			files = append(files, filepath.Join(dir, entry.Name()))
		}
	}
	return files
}

// ListDirs returns the paths of the subdirectories of dir, not recursing.
// It returns an empty slice when dir cannot be read.
func ListDirs(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return []string{}
	}

	var dirs []string
	for _, entry := range entries {
		if entry.IsDir() {
			dirs = append(dirs, filepath.Join(dir, entry.Name()))
		}
	}
	return dirs
}

// ListAll returns the paths of every entry in dir, not recursing. It
// returns an empty slice when dir cannot be read.
func ListAll(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return []string{}
	}

	var items []string
	for _, entry := range entries {
		items = append(items, filepath.Join(dir, entry.Name()))
	}
	return items
}

// WalkFiles returns the path of every non-directory under root, recursively.
// Entries it cannot read, including a missing root, are skipped, so the
// result holds whatever could be reached.
func WalkFiles(root string) []string {
	var files []string
	if err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // Continue on errors
		}
		if !d.IsDir() {
			files = append(files, path)
		}
		return nil
	}); err != nil {
		log.Printf("hyperchi: failed to walk directory %s: %v", root, err)
	}
	return files
}

// WalkDirs returns the path of every directory under root, recursively,
// excluding root itself. Unreadable entries are skipped.
func WalkDirs(root string) []string {
	var dirs []string
	if err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // Continue on errors
		}
		if d.IsDir() && path != root {
			dirs = append(dirs, path)
		}
		return nil
	}); err != nil {
		log.Printf("hyperchi: failed to walk directory %s: %v", root, err)
	}
	return dirs
}

// FindFiles returns every non-directory under root whose base name matches
// pattern (filepath.Match syntax). Unreadable entries are skipped, and a
// malformed pattern matches nothing.
func FindFiles(root, pattern string) []string {
	var matches []string
	if err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // Continue on errors
		}
		if !d.IsDir() {
			matched, err := filepath.Match(pattern, filepath.Base(path))
			if err == nil && matched {
				matches = append(matches, path)
			}
		}
		return nil
	}); err != nil {
		log.Printf("hyperchi: failed to walk directory %s: %v", root, err)
	}
	return matches
}

// TempFile creates an empty file in os.TempDir with a name built from
// pattern (see os.CreateTemp) and returns its path. The caller removes it.
func TempFile(pattern string) (string, error) {
	file, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", err
	}
	path := file.Name()
	if err := file.Close(); err != nil {
		return "", err
	}
	return path, nil
}

// TempDir creates a directory in os.TempDir (see os.MkdirTemp) and returns
// its path. The caller removes it.
func TempDir(pattern string) (string, error) {
	return os.MkdirTemp("", pattern)
}

// FileHelper chains operations on one file path. The first failing
// operation stores its error and every later operation is skipped; read it
// with Error.
type FileHelper struct {
	path string
	err  error
}

// File returns a FileHelper for path.
func File(path string) *FileHelper {
	return &FileHelper{path: path}
}

// Read returns the file's contents, or the stored error from an earlier
// step.
func (f *FileHelper) Read() (string, error) {
	if f.err != nil {
		return "", f.err
	}
	content, err := os.ReadFile(f.path)
	return string(content), err
}

// ReadOr returns the file's contents, or defaultContent when Read fails.
func (f *FileHelper) ReadOr(defaultContent string) string {
	content, err := f.Read()
	if err != nil {
		return defaultContent
	}
	return content
}

// Write replaces the file's contents (see WriteFile).
func (f *FileHelper) Write(content string) *FileHelper {
	if f.err != nil {
		return f
	}
	f.err = WriteFile(f.path, content)
	return f
}

// Append appends content to the file (see AppendFile).
func (f *FileHelper) Append(content string) *FileHelper {
	if f.err != nil {
		return f
	}
	f.err = AppendFile(f.path, content)
	return f
}

// Copy copies the file to dst (see CopyFile). The helper keeps its path.
func (f *FileHelper) Copy(dst string) *FileHelper {
	if f.err != nil {
		return f
	}
	f.err = CopyFile(f.path, dst)
	return f
}

// Move renames the file to dst (see MoveFile). On success the helper's path
// becomes dst.
func (f *FileHelper) Move(dst string) *FileHelper {
	if f.err != nil {
		return f
	}
	f.err = MoveFile(f.path, dst)
	if f.err == nil {
		f.path = dst
	}
	return f
}

// Delete removes the file.
func (f *FileHelper) Delete() *FileHelper {
	if f.err != nil {
		return f
	}
	f.err = RemoveFile(f.path)
	return f
}

// Exists reports whether the path exists (see FileExists).
func (f *FileHelper) Exists() bool {
	return FileExists(f.path)
}

// Size returns the file size in bytes, or 0 when it cannot be stat'd.
func (f *FileHelper) Size() int64 {
	return FileSize(f.path)
}

// Error returns the error from the first failed operation, or nil.
func (f *FileHelper) Error() error {
	return f.err
}

// Path returns the file path, which Move updates.
func (f *FileHelper) Path() string {
	return f.path
}

// DirHelper chains operations on one directory path. Like FileHelper, the
// first failing operation stores its error and later ones are skipped.
type DirHelper struct {
	path string
	err  error
}

// Dir returns a DirHelper for path.
func Dir(path string) *DirHelper {
	return &DirHelper{path: path}
}

// Create creates the directory and any missing parents.
func (d *DirHelper) Create() *DirHelper {
	if d.err != nil {
		return d
	}
	d.err = CreateDir(d.path)
	return d
}

// Remove removes the directory and everything under it.
func (d *DirHelper) Remove() *DirHelper {
	if d.err != nil {
		return d
	}
	d.err = RemoveDir(d.path)
	return d
}

// List returns the paths of the directory's entries (see ListAll), or the
// stored error from an earlier step.
func (d *DirHelper) List() ([]string, error) {
	if d.err != nil {
		return nil, d.err
	}
	return ListAll(d.path), nil
}

// Files returns the paths of the directory's non-directory entries.
func (d *DirHelper) Files() []string {
	return ListFiles(d.path)
}

// Dirs returns the paths of the directory's subdirectories.
func (d *DirHelper) Dirs() []string {
	return ListDirs(d.path)
}

// Walk returns every non-directory under the directory (see WalkFiles).
func (d *DirHelper) Walk() []string {
	return WalkFiles(d.path)
}

// Find returns the files under the directory whose base name matches
// pattern (see FindFiles).
func (d *DirHelper) Find(pattern string) []string {
	return FindFiles(d.path, pattern)
}

// Exists reports whether the path exists and is a directory.
func (d *DirHelper) Exists() bool {
	return IsDir(d.path)
}

// Error returns the error from the first failed operation, or nil.
func (d *DirHelper) Error() error {
	return d.err
}

// Path returns the directory path.
func (d *DirHelper) Path() string {
	return d.path
}
