package security

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

var (
	pngBytes  = append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0}, 64)...)
	jpegBytes = append([]byte("\xff\xd8\xff\xe0"), bytes.Repeat([]byte{1}, 64)...)
	htmlBytes = []byte("<!DOCTYPE html><html><script>alert(1)</script></html>")
	textBytes = []byte("just some notes\n")
)

// uploadPart is one file in a multipart test body.
type uploadPart struct {
	filename    string
	contentType string
	body        []byte
}

// multipartRequest builds a POST /upload request carrying parts under the
// field "file".
func multipartRequest(t *testing.T, parts ...uploadPart) *http.Request {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	for _, p := range parts {
		w, err := form.CreatePart(textproto.MIMEHeader{
			"Content-Disposition": {fmt.Sprintf(`form-data; name="file"; filename=%q`, p.filename)},
			"Content-Type":        {p.contentType},
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(p.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/upload", &body)
	r.Header.Set("Content-Type", form.FormDataContentType())
	return r
}

// fileHeader parses one uploaded file and returns its header. The parser
// already strips directories from the client name, so tests that need a
// hostile name set Filename afterwards, as a header from any source could.
func fileHeader(t *testing.T, filename string, body []byte) *multipart.FileHeader {
	t.Helper()
	r := multipartRequest(t, uploadPart{filename: "upload.bin", contentType: "application/octet-stream", body: body})
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := r.MultipartForm.RemoveAll(); err != nil {
			t.Error(err)
		}
	})
	fh := r.MultipartForm.File["file"][0]
	fh.Filename = filename
	return fh
}

// filesUnder lists every regular file and symlink below dir, relative to it.
func filesUnder(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			rel, rerr := filepath.Rel(dir, path)
			if rerr != nil {
				return rerr
			}
			out = append(out, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// fixedName makes newStoredName return name for the rest of the test.
func fixedName(t *testing.T, name string) {
	t.Helper()
	prev := newStoredName
	newStoredName = func() (string, error) { return name, nil }
	t.Cleanup(func() { newStoredName = prev })
}

var storedName = regexp.MustCompile(`^[0-9a-f]{32}\.[a-z0-9]+$`)

// A hostile client filename never reaches the path: the file is stored
// directly under dir with a random name, and the name is only reported back,
// cleaned, for display.
func TestSaveUploadIgnoresClientFilename(t *testing.T) {
	cases := map[string]string{
		"../../etc/passwd":              "passwd",
		"/etc/cron.d/evil.png":          "evil.png",
		"..\\..\\win.ini":               "win.ini",
		"a\x00b.png":                    "ab.png",
		"dir/sub/../photo.png":          "photo.png",
		"..":                            "upload",
		"":                              "upload",
		"x." + strings.Repeat("e", 300): "x",
	}
	for client, display := range cases {
		t.Run(fmt.Sprintf("%q", client), func(t *testing.T) {
			base := t.TempDir()
			dir := filepath.Join(base, "uploads")

			saved, err := SaveUpload(dir, fileHeader(t, client, pngBytes), SaveOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if !storedName.MatchString(saved.Name) || filepath.Ext(saved.Name) != ".png" {
				t.Fatalf("stored name = %q, want random hex plus .png", saved.Name)
			}
			if got := filesUnder(t, base); !slices.Equal(got, []string{filepath.Join("uploads", saved.Name)}) {
				t.Fatalf("files under the temp dir = %v, want only uploads/%s", got, saved.Name)
			}
			if !strings.HasPrefix(saved.OriginalName, display) || strings.ContainsAny(saved.OriginalName, "/\\\x00") {
				t.Fatalf("OriginalName = %q, want a cleaned name starting %q", saved.OriginalName, display)
			}
			if len(saved.OriginalName) > 255 {
				t.Fatalf("OriginalName is %d bytes, want at most 255", len(saved.OriginalName))
			}
		})
	}
}

// The extension comes from the sniffed content, never the filename, and
// markup is refused whatever it is called.
func TestSaveUploadExtensionFromContent(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct {
		filename string
		body     []byte
		ext      string
		typ      string
	}{
		{"photo.png", jpegBytes, ".jpg", "image/jpeg"},
		{"evil.html", pngBytes, ".png", "image/png"},
		{"setup.exe", textBytes, ".txt", "text/plain"},
	} {
		saved, err := SaveUpload(dir, fileHeader(t, tc.filename, tc.body), SaveOptions{})
		if err != nil {
			t.Fatalf("%s: %v", tc.filename, err)
		}
		if filepath.Ext(saved.Name) != tc.ext || saved.ContentType != tc.typ {
			t.Errorf("%s stored as %s (%s), want %s (%s)", tc.filename, saved.Name, saved.ContentType, tc.ext, tc.typ)
		}
	}

	before := filesUnder(t, dir)
	for name, opts := range map[string]SaveOptions{
		"default list":        {},
		"html listed by hand": {Extensions: map[string]string{"text/html": ".html"}},
	} {
		if _, err := SaveUpload(dir, fileHeader(t, "notes.txt", htmlBytes), opts); err == nil {
			t.Errorf("%s: HTML named notes.txt was stored", name)
		}
	}
	// A type missing from the list, and an extension that is not one.
	if _, err := SaveUpload(dir, fileHeader(t, "a.png", pngBytes), SaveOptions{Extensions: map[string]string{"image/gif": ".gif"}}); err == nil {
		t.Error("a PNG was stored with only GIF allowed")
	}
	if _, err := SaveUpload(dir, fileHeader(t, "a.png", pngBytes), SaveOptions{Extensions: map[string]string{"image/png": "/../x"}}); err == nil {
		t.Error("an extension with a separator was used")
	}
	if got := filesUnder(t, dir); !slices.Equal(got, before) {
		t.Fatalf("refused uploads left files: %v, want %v", got, before)
	}
}

// An existing file is never overwritten, a symlink included, and the file
// already there is left alone.
func TestSaveUploadRefusesOverwrite(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "uploads")
	if err := os.Mkdir(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	fixedName(t, "fixed")

	existing := filepath.Join(dir, "fixed.png")
	if err := os.WriteFile(existing, []byte("keep"), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := SaveUpload(dir, fileHeader(t, "a.png", pngBytes), SaveOptions{}); err == nil {
		t.Fatal("SaveUpload overwrote an existing file")
	}
	if got, err := os.ReadFile(existing); err != nil || string(got) != "keep" {
		t.Fatalf("existing file = %q, %v; want it untouched", got, err)
	}

	outside := filepath.Join(base, "outside.txt")
	if err := os.Symlink(outside, filepath.Join(dir, "fixed.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := SaveUpload(dir, fileHeader(t, "a.txt", textBytes), SaveOptions{}); err == nil {
		t.Fatal("SaveUpload wrote through a symlink")
	}
	if _, err := os.Lstat(outside); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the symlink's target was created: %v", err)
	}
}

// The size limit holds while the file is written, whatever the header claims,
// and the partial file is removed.
func TestSaveUploadEnforcesSizeWhileWriting(t *testing.T) {
	dir := t.TempDir()
	big := append(bytes.Clone(pngBytes), bytes.Repeat([]byte{7}, 4096)...)

	fh := fileHeader(t, "big.png", big)
	fh.Size = 10 // a header that understates the file
	_, err := SaveUpload(dir, fh, SaveOptions{MaxSize: 1000})
	var limit *UploadLimitError
	if !errors.As(err, &limit) || limit.Limit != 1000 || limit.Scope != "file" {
		t.Fatalf("err = %v, want an *UploadLimitError for 1000 bytes", err)
	}
	if got := filesUnder(t, dir); len(got) != 0 {
		t.Fatalf("partial file left behind: %v", got)
	}

	// The header's own size is checked before anything is written.
	if _, err := SaveUpload(dir, fileHeader(t, "big.png", big), SaveOptions{MaxSize: 1000}); !errors.As(err, &limit) {
		t.Fatalf("err = %v, want an *UploadLimitError", err)
	}

	// Exactly at the limit is fine.
	saved, err := SaveUpload(dir, fileHeader(t, "ok.png", big), SaveOptions{MaxSize: int64(len(big))})
	if err != nil || saved.Size != int64(len(big)) {
		t.Fatalf("file at the limit = %+v, %v", saved, err)
	}
}

// Stored files are 0640 at most: never executable and never written or read
// by others.
func TestSaveUploadPermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "uploads")
	saved, err := SaveUpload(dir, fileHeader(t, "a.png", pngBytes), SaveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, saved.Name))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm&^0o640 != 0 || perm&0o400 == 0 {
		t.Fatalf("file mode = %v, want 0640 or stricter and owner-readable", perm)
	}
	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if perm := dirInfo.Mode().Perm(); perm&0o007 != 0 {
		t.Fatalf("directory mode = %v, want no access for others", perm)
	}
	got, err := os.ReadFile(filepath.Join(dir, saved.Name))
	if err != nil || !bytes.Equal(got, pngBytes) {
		t.Fatalf("stored bytes differ from the upload (%v)", err)
	}
}

// SaveAll stores every file UploadMiddleware validated, holding them to the
// validator's per-file limit; when one fails, none are kept.
func TestSaveAllAfterUploadMiddleware(t *testing.T) {
	cfg := publicUploadConfig()

	run := func(t *testing.T, dir string, opts SaveOptions, parts ...uploadPart) ([]SavedFile, error) {
		t.Helper()
		var saved []SavedFile
		var saveErr error
		rec := httptest.NewRecorder()
		UploadMiddleware(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			saved, saveErr = GetValidationResult(r).SaveAll(dir, opts)
			w.WriteHeader(http.StatusNoContent)
		})).ServeHTTP(rec, multipartRequest(t, parts...))
		if rec.Code != http.StatusNoContent {
			t.Fatalf("middleware = %d %q", rec.Code, rec.Body.String())
		}
		return saved, saveErr
	}

	dir := t.TempDir()
	saved, err := run(t, dir, SaveOptions{},
		uploadPart{"../a.png", "image/png", pngBytes},
		uploadPart{"notes.txt", "text/plain", textBytes})
	if err != nil {
		t.Fatal(err)
	}
	if len(saved) != 2 || filepath.Ext(saved[0].Name) != ".png" || filepath.Ext(saved[1].Name) != ".txt" {
		t.Fatalf("saved = %+v", saved)
	}
	if saved[0].OriginalName != "a.png" || saved[1].OriginalName != "notes.txt" {
		t.Fatalf("original names = %q, %q", saved[0].OriginalName, saved[1].OriginalName)
	}
	if got := filesUnder(t, dir); len(got) != 2 {
		t.Fatalf("files = %v, want 2", got)
	}

	// The second file's type is not allowed here, so the first is removed.
	empty := t.TempDir()
	saved, err = run(t, empty, SaveOptions{Extensions: map[string]string{"image/png": ".png"}},
		uploadPart{"a.png", "image/png", pngBytes},
		uploadPart{"notes.txt", "text/plain", textBytes})
	if err == nil || saved != nil {
		t.Fatalf("SaveAll = %v, %v; want an error and nothing saved", saved, err)
	}
	if got := filesUnder(t, empty); len(got) != 0 {
		t.Fatalf("files left after a failed SaveAll: %v", got)
	}

	invalid := &UploadValidationResult{Valid: false}
	if _, err := invalid.SaveAll(t.TempDir(), SaveOptions{}); err == nil {
		t.Fatal("SaveAll stored a result that failed validation")
	}
}
