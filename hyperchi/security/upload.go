package security

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"
)

// FileUploadConfig sets the limits UploadValidator enforces. An empty
// AllowedTypes or AllowedExtensions list allows any value.
//
// The size limits are one budget. Each file may be up to MaxFileSize bytes,
// all files together up to MaxTotalSize, and the whole request body up to
// MaxTotalSize plus 1 MiB for multipart boundaries, part headers, and other
// fields. A zero or negative MaxFileSize means 10 MiB, MaxFiles means 1, and
// MaxTotalSize means MaxFiles * MaxFileSize.
type FileUploadConfig struct {
	MaxFileSize       int64    // bytes per file
	MaxTotalSize      int64    // bytes for all files together
	AllowedTypes      []string // declared MIME types, matched exactly
	AllowedExtensions []string // extensions with the dot, matched case-insensitively
	MaxFiles          int      // files per request
	RequireAuth       bool     // require a user ID set by RequireAuth or WithUserID
}

// UploadLimitError reports an upload over a size limit. UploadMiddleware
// answers it with 413 and every other failure with 400.
type UploadLimitError struct {
	Scope string // "body", "file", or "total"
	Name  string // the file's client name, for Scope "file"
	Limit int64  // the limit in bytes
}

// Error returns the message shown to the client.
func (e *UploadLimitError) Error() string {
	switch e.Scope {
	case "file":
		return fmt.Sprintf("File %s exceeds size limit (%d bytes)", e.Name, e.Limit)
	case "total":
		return fmt.Sprintf("Total upload size exceeds limit (%d bytes)", e.Limit)
	default:
		return fmt.Sprintf("Request body exceeds limit (%d bytes)", e.Limit)
	}
}

// uploadLimits are a config's size limits with the zero-value defaults
// applied.
type uploadLimits struct {
	fileSize  int64
	totalSize int64
	files     int
	body      int64
}

// multipartOverhead is the body allowance on top of the files for boundaries,
// part headers, and non-file fields.
const multipartOverhead = 1 << 20

func (c *FileUploadConfig) limits() uploadLimits {
	l := uploadLimits{fileSize: c.MaxFileSize, totalSize: c.MaxTotalSize, files: c.MaxFiles}
	if l.fileSize < 1 {
		l.fileSize = 10 << 20
	}
	if l.files < 1 {
		l.files = 1
	}
	if l.totalSize < 1 {
		l.totalSize = int64(l.files) * l.fileSize
	}
	l.body = l.totalSize + multipartOverhead
	return l
}

// DefaultUploadConfig returns a 10 MB, 5-file limit for common image types,
// plain text, and PDF, with authentication required.
func DefaultUploadConfig() *FileUploadConfig {
	return &FileUploadConfig{
		MaxFileSize: 10 * 1024 * 1024,
		AllowedTypes: []string{
			"image/jpeg",
			"image/png",
			"image/gif",
			"image/webp",
			"text/plain",
			"application/pdf",
		},
		AllowedExtensions: []string{
			".jpg", ".jpeg", ".png", ".gif", ".webp",
			".txt", ".pdf",
		},
		MaxFiles:    5,
		RequireAuth: true,
	}
}

// UploadValidationResult is the outcome of ValidateUpload. Files holds only
// the files that passed, and Errors holds one message per failure. Err joins
// the same failures as errors: errors.As finds an *UploadLimitError when a
// size limit failed, whether ValidateUpload's own body limit or
// UploadMiddleware's http.MaxBytesReader cut the request off. SaveAll stores
// the files that passed.
type UploadValidationResult struct {
	Valid     bool            `json:"valid"`
	Errors    []string        `json:"errors,omitempty"`
	Files     []ValidatedFile `json:"files,omitempty"`
	TotalSize int64           `json:"total_size"`
	Err       error           `json:"-"`

	fileLimit int64 // the per-file limit the files were checked against
}

// uploadFailure is a failure whose text is a sentence for the client, kept
// as written in UploadValidationResult.Errors. err is the cause, if any.
type uploadFailure struct {
	msg string
	err error
}

func (f *uploadFailure) Error() string {
	if f.err != nil {
		return f.msg + ": " + f.err.Error()
	}
	return f.msg
}

func (f *uploadFailure) Unwrap() error { return f.err }

// failure returns an *uploadFailure with a formatted message.
func failure(cause error, format string, args ...any) error {
	return &uploadFailure{msg: fmt.Sprintf(format, args...), err: cause}
}

// fail records err as a failure.
func (r *UploadValidationResult) fail(err error) {
	r.Valid = false
	r.Errors = append(r.Errors, err.Error())
	r.Err = errors.Join(r.Err, err)
}

// tooLarge reports whether a size limit caused the failure.
func (r *UploadValidationResult) tooLarge() bool {
	var limit *UploadLimitError
	return errors.As(r.Err, &limit)
}

// ValidatedFile describes one uploaded file. Filename is the client's name and
// must not be used as a path. SafeFilename is that name cleaned for display;
// store files with SaveUpload or UploadValidationResult.SaveAll, which pick
// their own names.
type ValidatedFile struct {
	Filename     string `json:"filename"`
	Size         int64  `json:"size"`
	ContentType  string `json:"content_type"`
	Extension    string `json:"extension"`
	SafeFilename string `json:"safe_filename"`

	header *multipart.FileHeader // the upload, for SaveAll
}

// ctxKey is private so this package's context keys cannot collide with others.
type ctxKey int

const (
	ctxKeyUploadValidation ctxKey = iota
)

// UploadValidator checks multipart uploads against a FileUploadConfig.
type UploadValidator struct {
	config *FileUploadConfig
}

// NewUploadValidator returns a validator for config, or for
// DefaultUploadConfig when config is nil.
func NewUploadValidator(config *FileUploadConfig) *UploadValidator {
	if config == nil {
		config = DefaultUploadConfig()
	}
	return &UploadValidator{
		config: config,
	}
}

// limitedBody fails a read past limit bytes with an *UploadLimitError, so a
// cut-off body is reported as too large instead of malformed.
type limitedBody struct {
	r         io.Reader
	remaining int64
	limit     int64
}

func (l *limitedBody) Read(p []byte) (int, error) {
	if l.remaining <= 0 {
		// Probe one byte: a body exactly at the limit ends here cleanly.
		var probe [1]byte
		n, err := l.r.Read(probe[:])
		if n > 0 {
			return 0, &UploadLimitError{Scope: "body", Limit: l.limit}
		}
		return 0, err
	}
	if int64(len(p)) > l.remaining {
		p = p[:l.remaining]
	}
	n, err := l.r.Read(p)
	l.remaining -= int64(n)
	return n, err
}

// ValidateUpload parses r as a multipart form and checks each file's size,
// extension, declared MIME type, and sniffed content, plus the file count and
// combined size (see FileUploadConfig for the limits). With RequireAuth set it
// fails at once unless the request context carries a verified user ID (see
// WithUserID). A token in the request is not enough. It wraps r.Body in the
// body limit and parses the form, so call it before anything else reads the
// body.
func (uv *UploadValidator) ValidateUpload(r *http.Request) *UploadValidationResult {
	result := &UploadValidationResult{
		Valid:  true,
		Files:  []ValidatedFile{},
		Errors: []string{},
	}

	if uv.config.RequireAuth {
		if GetUserIDFromContext(r.Context()) == "" {
			result.fail(failure(nil, "Authentication required for file uploads"))
			return result
		}
	}

	limits := uv.config.limits()
	result.fileLimit = limits.fileSize

	// Cap the body before parsing so one huge body or many large parts cannot
	// exhaust memory or disk. ParseMultipartForm's argument only sets how much
	// is held in memory before spilling to disk. It does not limit the request.
	// UploadMiddleware has already wrapped the body in http.MaxBytesReader;
	// this limit covers direct callers, which have no ResponseWriter.
	r.Body = struct {
		io.Reader
		io.Closer
	}{&limitedBody{r: r.Body, remaining: limits.body, limit: limits.body}, r.Body}

	if err := r.ParseMultipartForm(limits.fileSize); err != nil {
		var limit *UploadLimitError
		var maxBytes *http.MaxBytesError
		if errors.As(err, &limit) || errors.As(err, &maxBytes) {
			result.fail(&UploadLimitError{Scope: "body", Limit: limits.body})
		} else {
			result.fail(failure(err, "Failed to parse multipart form"))
		}
		return result
	}

	totalFiles := 0
	var totalSize int64

files:
	for _, fileHeaders := range r.MultipartForm.File {
		for _, fileHeader := range fileHeaders {
			totalFiles++
			if totalFiles > limits.files {
				result.fail(failure(nil, "Too many files (max %d allowed)", limits.files))
				break files
			}
			totalSize += fileHeader.Size

			validatedFile, fileErrors := uv.validateFile(fileHeader, limits)
			if len(fileErrors) > 0 {
				for _, err := range fileErrors {
					result.fail(err)
				}
			} else {
				result.Files = append(result.Files, validatedFile)
				result.TotalSize += validatedFile.Size
			}
		}
	}

	// Every file counts toward the total, accepted or not.
	if totalSize > limits.totalSize {
		result.fail(&UploadLimitError{Scope: "total", Limit: limits.totalSize})
	}

	return result
}

// validateFile checks one file's size, extension, declared type, and sniffed
// content, and computes its SafeFilename. It returns every failure found.
func (uv *UploadValidator) validateFile(fileHeader *multipart.FileHeader, limits uploadLimits) (ValidatedFile, []error) {
	var errs []error
	validatedFile := ValidatedFile{
		Filename:    fileHeader.Filename,
		Size:        fileHeader.Size,
		ContentType: fileHeader.Header.Get("Content-Type"),
		Extension:   strings.ToLower(filepath.Ext(fileHeader.Filename)),
		header:      fileHeader,
	}

	if fileHeader.Size > limits.fileSize {
		errs = append(errs, &UploadLimitError{Scope: "file", Name: fileHeader.Filename, Limit: limits.fileSize})
	}

	if len(uv.config.AllowedExtensions) > 0 {
		allowed := false
		for _, ext := range uv.config.AllowedExtensions {
			if strings.EqualFold(validatedFile.Extension, ext) {
				allowed = true
				break
			}
		}
		if !allowed {
			errs = append(errs, failure(nil, "File extension %s not allowed for %s",
				validatedFile.Extension, fileHeader.Filename))
		}
	}

	if len(uv.config.AllowedTypes) > 0 {
		allowed := false
		for _, mimeType := range uv.config.AllowedTypes {
			if validatedFile.ContentType == mimeType {
				allowed = true
				break
			}
		}
		if !allowed {
			errs = append(errs, failure(nil, "Content type %s not allowed for %s",
				validatedFile.ContentType, fileHeader.Filename))
		}
	}

	if err := uv.validateFileContent(fileHeader); err != nil {
		errs = append(errs, err)
	}

	validatedFile.SafeFilename = sanitizeFilename(fileHeader.Filename)

	return validatedFile, errs
}

// validateFileContent sniffs the file's first 512 bytes with
// http.DetectContentType and reports a mismatch with the declared type, so a
// client cannot pass markup off as a harmless type by lying in Content-Type.
func (uv *UploadValidator) validateFileContent(fileHeader *multipart.FileHeader) error {
	file, err := fileHeader.Open()
	if err != nil {
		return failure(err, "Cannot open file %s for validation", fileHeader.Filename)
	}
	defer func() { _ = file.Close() }() // best-effort close

	// io.ReadFull, not Read: a single Read may return fewer bytes, and the
	// sniff would then judge a truncated sample.
	buffer := make([]byte, 512)
	n, err := io.ReadFull(file, buffer)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return failure(err, "Cannot read file %s for validation", fileHeader.Filename)
	}

	detectedType := http.DetectContentType(buffer[:n])
	declaredType := fileHeader.Header.Get("Content-Type")

	if !isCompatibleContentType(detectedType, declaredType) {
		return failure(nil, "File %s content type mismatch: declared %s, detected %s",
			fileHeader.Filename, declaredType, detectedType)
	}

	return nil
}

// sanitizeFilename cleans a client filename for display: no directory part,
// no control characters, no "..", path separators, or shell and
// Windows-reserved characters, valid UTF-8 of at most 255 bytes, and never
// empty. It is not a storage name; SaveUpload generates those.
func sanitizeFilename(original string) string {
	filename := filepath.Base(original)

	var cleaned strings.Builder
	for _, r := range filename {
		if r == 0 || r < 32 || r == 127 {
			continue
		}
		cleaned.WriteRune(r)
	}
	filename = cleaned.String()

	unsafe := []string{"..", "/", "\\", ":", "*", "?", "\"", "<", ">", "|", "'", "`", ";", "&", "$", "#", "!", "~"}
	safe := filename
	for _, char := range unsafe {
		safe = strings.ReplaceAll(safe, char, "_")
	}

	for strings.Contains(safe, "__") {
		safe = strings.ReplaceAll(safe, "__", "_")
	}
	safe = strings.Trim(safe, "_. ")

	if safe == "" || safe == "." {
		safe = "upload"
	}

	if len(safe) > 255 {
		// Keep a short extension; a "." followed by hundreds of bytes is not one.
		ext := filepath.Ext(safe)
		if len(ext) > 16 {
			ext = ""
		}
		// The cut may split a character; drop the partial bytes.
		safe = strings.ToValidUTF8(safe[:255-len(ext)], "") + ext
	}

	return safe
}

// isCompatibleContentType reports whether the sniffed type agrees with the
// declared one.
//
// Sniffed markup or script (HTML, XML, SVG, JavaScript) never matches a
// different declared type. Accepting HTML declared as text/plain would let an
// attacker upload a script payload under a harmless type, which is stored XSS
// once the file is served. Passing this check does not make a file safe to
// serve inline: code that serves uploads must send a Content-Type it derived
// itself, not the client's, with "X-Content-Type-Options: nosniff" and
// preferably "Content-Disposition: attachment". This package does not serve
// files.
func isCompatibleContentType(detected, declared string) bool {
	// Drop parameters such as "; charset=utf-8" before comparing.
	detected = normalizeMediaType(detected)
	declared = normalizeMediaType(declared)

	if detected == declared {
		return true
	}

	switch detected {
	case "text/html", "text/xml", "application/xml",
		"application/xhtml+xml", "image/svg+xml", "application/javascript":
		return false
	}

	// The few harmless mismatches the sniffer produces.
	compatibleTypes := map[string][]string{
		// Text without a clear signature can sniff as octet-stream.
		"text/plain": {"text/plain", "application/octet-stream"},
		"image/jpeg": {"image/jpeg", "image/jpg"},
	}

	if compatible, exists := compatibleTypes[declared]; exists {
		for _, compat := range compatible {
			if detected == compat {
				return true
			}
		}
	}

	return false
}

// normalizeMediaType drops parameters (such as "; charset=utf-8") and
// surrounding whitespace from a media type and lowercases it.
func normalizeMediaType(mediaType string) string {
	if i := strings.IndexByte(mediaType, ';'); i >= 0 {
		mediaType = mediaType[:i]
	}
	return strings.ToLower(strings.TrimSpace(mediaType))
}

// UploadMiddleware returns middleware that runs ValidateUpload on every
// multipart/form-data request and passes other requests through untouched. A
// failed validation gets a plain-text 413 when a size limit failed (the body,
// a file, or the total) and 400 for anything else, a malformed body included.
// The next handler reads the result with GetValidationResult. With
// RequireAuth set, install it after the middleware that authenticates the
// user.
//
//	config := security.DefaultUploadConfig()
//	config.MaxFileSize = 5 * 1024 * 1024
//	app.Post("/upload", upload, security.RequireAuth(authHelper), security.UploadMiddleware(config))
func UploadMiddleware(config *FileUploadConfig) func(http.Handler) http.Handler {
	validator := NewUploadValidator(config)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
				next.ServeHTTP(w, r)
				return
			}

			// http.MaxBytesReader caps the whole body, disk spillover
			// included, and tells the server to close the connection.
			r.Body = http.MaxBytesReader(w, r.Body, validator.config.limits().body)

			result := validator.ValidateUpload(r)
			if !result.Valid {
				status := http.StatusBadRequest
				if result.tooLarge() {
					status = http.StatusRequestEntityTooLarge
				}
				http.Error(w, fmt.Sprintf("File upload validation failed: %s",
					strings.Join(result.Errors, "; ")), status)
				return
			}

			r = r.WithContext(setValidationResult(r.Context(), result))

			next.ServeHTTP(w, r)
		})
	}
}

// GetValidationResult returns the result UploadMiddleware stored in r's
// context, or nil when the middleware did not run or the request was not
// multipart.
func GetValidationResult(r *http.Request) *UploadValidationResult {
	if result, ok := r.Context().Value(ctxKeyUploadValidation).(*UploadValidationResult); ok {
		return result
	}
	return nil
}

// setValidationResult stores result in ctx for GetValidationResult.
func setValidationResult(ctx context.Context, result *UploadValidationResult) context.Context {
	return context.WithValue(ctx, ctxKeyUploadValidation, result)
}
