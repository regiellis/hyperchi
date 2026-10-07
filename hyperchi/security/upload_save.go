package security

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"maps"
	"mime/multipart"
	"net/http"
	"os"
	"regexp"
)

// SaveOptions configures SaveUpload and UploadValidationResult.SaveAll.
type SaveOptions struct {
	// MaxSize is the most bytes stored for one file, checked again while the
	// file is written. Zero or negative means 10 MiB for SaveUpload, and the
	// validator's per-file limit for SaveAll.
	MaxSize int64

	// Extensions maps a sniffed content type ("image/png", without
	// parameters) to the extension a stored file gets (".png"). A file whose
	// sniffed type is not listed is refused. Nil means
	// DefaultSaveExtensions(). Each extension is a dot and 1 to 10 lowercase
	// letters or digits. Markup and script types (HTML, XML, SVG,
	// JavaScript) are refused even when listed.
	Extensions map[string]string
}

// SavedFile describes a stored upload.
type SavedFile struct {
	// Name is the stored file's name inside the directory: random hex and an
	// extension from SaveOptions.Extensions.
	Name string `json:"name"`
	// Size is the number of bytes written.
	Size int64 `json:"size"`
	// ContentType is the type sniffed from the file's first 512 bytes.
	ContentType string `json:"content_type"`
	// OriginalName is the client's filename, cleaned like
	// ValidatedFile.SafeFilename. Show it; never use it as a path.
	OriginalName string `json:"original_name"`
}

// defaultSaveMaxSize is SaveUpload's per-file limit when SaveOptions.MaxSize
// is not positive, matching FileUploadConfig's default.
const defaultSaveMaxSize = 10 << 20

// DefaultSaveExtensions returns a new map from each type DefaultUploadConfig
// allows to its extension. Copy and extend it to store other types:
//
//	exts := security.DefaultSaveExtensions()
//	exts["audio/mpeg"] = ".mp3"
func DefaultSaveExtensions() map[string]string {
	return map[string]string{
		"image/jpeg":      ".jpg",
		"image/png":       ".png",
		"image/gif":       ".gif",
		"image/webp":      ".webp",
		"text/plain":      ".txt",
		"application/pdf": ".pdf",
	}
}

// validExtension is the shape SaveOptions.Extensions values must have, so a
// mapping can never put a separator or ".." into a stored name.
var validExtension = regexp.MustCompile(`^\.[a-z0-9]{1,10}$`)

// activeContentTypes are sniffed types a browser may run as markup or script.
// They are never stored, whatever the extension map says.
var activeContentTypes = map[string]bool{
	"text/html":              true,
	"text/xml":               true,
	"application/xml":        true,
	"application/xhtml+xml":  true,
	"image/svg+xml":          true,
	"application/javascript": true,
	"text/javascript":        true,
}

// newStoredName returns a random name for a stored file, without extension.
// Tests replace it to force a collision.
var newStoredName = func() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generating a file name: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// SaveUpload stores fh under dir and reports what it stored. Nothing the
// client sent becomes part of the path: the file is named with 128 random
// bits plus the extension SaveOptions.Extensions gives the type sniffed from
// its content (not its filename or declared type), and it is written through
// an os.Root on dir, so it cannot land outside dir even through a symlink.
// SaveUpload creates dir (0750) when missing, never overwrites an existing
// file, writes with mode 0640 (less under a stricter umask), and copies in a
// stream, failing with an *UploadLimitError once more than MaxSize bytes
// arrive. On any error after the file is created, the partial file is
// removed.
//
// SaveUpload does not run ValidateUpload's checks; run them first (or use
// UploadMiddleware and SaveAll). Code that serves a stored file should set
// the Content-Type from its extension and send "X-Content-Type-Options:
// nosniff".
func SaveUpload(dir string, fh *multipart.FileHeader, opts SaveOptions) (SavedFile, error) {
	root, err := openUploadRoot(dir)
	if err != nil {
		return SavedFile{}, err
	}
	saved, err := saveFile(root, fh, opts.withDefaults(defaultSaveMaxSize))
	if cerr := root.Close(); cerr != nil && err == nil {
		return SavedFile{}, fmt.Errorf("security: closing upload directory: %w", cerr)
	}
	return saved, err
}

// SaveAll stores every file that passed validation under dir, as SaveUpload
// does, and returns them in order. With SaveOptions.MaxSize unset, files are
// held to the per-file limit they were validated against. It stores all of
// them or none: when one fails, the files already stored are removed. A
// result that is not Valid is refused.
//
//	result := security.GetValidationResult(r)
//	saved, err := result.SaveAll("uploads", security.SaveOptions{})
func (r *UploadValidationResult) SaveAll(dir string, opts SaveOptions) (saved []SavedFile, err error) {
	if !r.Valid {
		return nil, errors.New("security: SaveAll: the upload did not pass validation")
	}
	fallback := r.fileLimit
	if fallback < 1 {
		fallback = defaultSaveMaxSize
	}
	opts = opts.withDefaults(fallback)

	root, err := openUploadRoot(dir)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			for _, f := range saved {
				if rerr := root.Remove(f.Name); rerr != nil {
					err = errors.Join(err, fmt.Errorf("security: removing %s: %w", f.Name, rerr))
				}
			}
			saved = nil
		}
		if cerr := root.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("security: closing upload directory: %w", cerr)
		}
	}()

	for _, file := range r.Files {
		if file.header == nil {
			return saved, fmt.Errorf("security: SaveAll: %s has no upload to save", file.SafeFilename)
		}
		f, err := saveFile(root, file.header, opts)
		if err != nil {
			return saved, err
		}
		saved = append(saved, f)
	}
	return saved, nil
}

// withDefaults fills in MaxSize (with fallback) and Extensions.
func (o SaveOptions) withDefaults(fallback int64) SaveOptions {
	if o.MaxSize < 1 {
		o.MaxSize = fallback
	}
	if o.Extensions == nil {
		o.Extensions = DefaultSaveExtensions()
	} else {
		o.Extensions = maps.Clone(o.Extensions)
	}
	return o
}

// openUploadRoot creates dir when missing and opens it as a root that no
// path can escape.
func openUploadRoot(dir string) (*os.Root, error) {
	if dir == "" {
		return nil, errors.New("security: no upload directory")
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("security: creating upload directory: %w", err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("security: opening upload directory: %w", err)
	}
	return root, nil
}

// saveFile stores fh in root. opts has its defaults applied.
func saveFile(root *os.Root, fh *multipart.FileHeader, opts SaveOptions) (SavedFile, error) {
	original := sanitizeFilename(fh.Filename)
	if fh.Size > opts.MaxSize {
		return SavedFile{}, &UploadLimitError{Scope: "file", Name: fh.Filename, Limit: opts.MaxSize}
	}

	src, err := fh.Open()
	if err != nil {
		return SavedFile{}, fmt.Errorf("security: opening upload %s: %w", original, err)
	}
	defer func() { _ = src.Close() }() // read-only; a close error loses nothing

	head := make([]byte, 512)
	n, err := io.ReadFull(src, head)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return SavedFile{}, fmt.Errorf("security: reading upload %s: %w", original, err)
	}
	head = head[:n]

	contentType := normalizeMediaType(http.DetectContentType(head))
	ext, err := storedExtension(contentType, opts.Extensions)
	if err != nil {
		return SavedFile{}, fmt.Errorf("security: refusing %s: %w", original, err)
	}

	base, err := newStoredName()
	if err != nil {
		return SavedFile{}, fmt.Errorf("security: %w", err)
	}
	name := base + ext

	// O_EXCL refuses a name that exists, a symlink included.
	dst, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		return SavedFile{}, fmt.Errorf("security: creating %s: %w", name, err)
	}

	written, err := copyLimited(dst, head, src, opts.MaxSize)
	if cerr := dst.Close(); cerr != nil && err == nil {
		err = fmt.Errorf("closing: %w", cerr)
	}
	if err != nil {
		var limit *UploadLimitError
		if errors.As(err, &limit) {
			limit.Name = fh.Filename
		} else {
			err = fmt.Errorf("security: writing %s: %w", name, err)
		}
		if rerr := root.Remove(name); rerr != nil {
			err = errors.Join(err, fmt.Errorf("security: removing partial %s: %w", name, rerr))
		}
		return SavedFile{}, err
	}

	return SavedFile{Name: name, Size: written, ContentType: contentType, OriginalName: original}, nil
}

// storedExtension returns the extension for a sniffed type, refusing active
// content, unlisted types, and malformed extensions.
func storedExtension(contentType string, extensions map[string]string) (string, error) {
	if activeContentTypes[contentType] {
		return "", fmt.Errorf("content type %s is never stored", contentType)
	}
	ext, ok := extensions[contentType]
	if !ok {
		return "", fmt.Errorf("content type %s is not in the allowed list", contentType)
	}
	if !validExtension.MatchString(ext) {
		return "", fmt.Errorf("extension %q for %s is not a dot and 1 to 10 lowercase letters or digits", ext, contentType)
	}
	return ext, nil
}

// copyLimited writes head and then the rest of src to dst, failing with an
// *UploadLimitError as soon as more than limit bytes arrive.
func copyLimited(dst io.Writer, head []byte, src io.Reader, limit int64) (int64, error) {
	if int64(len(head)) > limit {
		return 0, &UploadLimitError{Scope: "file", Limit: limit}
	}
	n, err := dst.Write(head)
	written := int64(n)
	if err != nil {
		return written, err
	}
	// One byte past the limit is enough to know the file is too large.
	rest, err := io.Copy(dst, io.LimitReader(src, limit-written+1))
	written += rest
	if err != nil {
		return written, err
	}
	if written > limit {
		return written, &UploadLimitError{Scope: "file", Limit: limit}
	}
	return written, nil
}
