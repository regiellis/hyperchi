package hyperchi

import (
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// defaultCompressMinSize is the smallest body Compress gzips by default. Below
// about a kilobyte the gzip header and the CPU cost outweigh the savings.
const defaultCompressMinSize = 1024

// CompressOptions configures CompressWithOptions. The zero value is the
// default: gzip.DefaultCompression and a 1 KiB minimum.
type CompressOptions struct {
	// Level is the gzip level: 1 (fastest) to 9 (smallest), or
	// gzip.HuffmanOnly. Zero means gzip.DefaultCompression.
	Level int
	// MinSize is the smallest body, in bytes, worth compressing. Zero means
	// 1024. A smaller body is sent as is.
	MinSize int
}

// CompressWithOptions returns middleware that gzips responses for clients
// that accept it. It uses the standard library only. Brotli or zstd would need
// a third-party encoder, so neither is offered.
//
// A response is compressed when the request's Accept-Encoding allows gzip
// (gzip or *, with a q above 0) and none of these apply:
//   - The request is HEAD, CONNECT, or a WebSocket upgrade. These pass through
//     untouched, Hijack included.
//   - The status is 1xx, 204, 206, or 304.
//   - The response sets its own Content-Encoding or Content-Range.
//   - The type is text/event-stream, so SSE streams flow uncompressed.
//   - The type is already compressed: images other than SVG, BMP, and icons,
//     video, audio, WOFF and WOFF2 fonts, and archives such as zip, gzip,
//     bzip2, xz, 7z, rar, and zstd.
//   - The body is under MinSize bytes. The body is buffered until it reaches
//     MinSize or the handler returns or flushes, so a response that flushes
//     early is sent uncompressed.
//
// A compressed response loses Content-Length and Accept-Ranges, and a strong
// ETag becomes weak, since the bytes differ from the identity representation.
// Every response that could be compressed carries Vary: Accept-Encoding,
// whether or not this one was. Flush passes through, flushing the gzip stream
// first, and Unwrap exposes the underlying writer to http.ResponseController.
// A missing Content-Type is sniffed from the first bytes before compressing,
// as net/http would. It panics on an invalid Level or a negative MinSize.
//
// Compressing a page that both reflects request input and carries a fixed
// secret over HTTPS exposes the secret to the BREACH attack. The CSRF token
// from Middleware.CSRF is masked afresh on every render, so it is safe here.
// Any other per-user secret a page renders unmasked next to reflected input (a
// session-bound value, an API key) is not: keep such pages off compressed
// routes.
func CompressWithOptions(options CompressOptions) func(http.Handler) http.Handler {
	level := options.Level
	if level == 0 {
		level = gzip.DefaultCompression
	}
	if _, err := gzip.NewWriterLevel(io.Discard, level); err != nil {
		panic(fmt.Sprintf("hyperchi: Compress: %v", err))
	}
	minSize := options.MinSize
	if minSize < 0 {
		panic(fmt.Sprintf("hyperchi: Compress: negative MinSize %d", minSize))
	}
	if minSize == 0 {
		minSize = defaultCompressMinSize
	}
	pool := &sync.Pool{New: func() any {
		gz, err := gzip.NewWriterLevel(io.Discard, level)
		if err != nil {
			panic(fmt.Sprintf("hyperchi: Compress: %v", err)) // level was checked above
		}
		return gz
	}}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodHead || r.Method == http.MethodConnect || isUpgradeRequest(r) {
				next.ServeHTTP(w, r)
				return
			}
			cw := &compressWriter{
				ResponseWriter: w,
				pool:           pool,
				minSize:        minSize,
				accepts:        acceptsGzip(r.Header.Values("Accept-Encoding")),
			}
			// Not deferred: if the handler panics, nothing buffered is sent,
			// so a recovery middleware outside this one can still write a
			// clean error response.
			next.ServeHTTP(cw, r)
			if err := cw.finish(); err != nil {
				log.Printf("hyperchi: compress %s %s: %v", r.Method, r.URL.Path, err)
			}
		})
	}
}

// isUpgradeRequest reports whether r asks to switch protocols (a WebSocket
// handshake), which needs the connection itself rather than a body.
func isUpgradeRequest(r *http.Request) bool {
	if r.Header.Get("Upgrade") == "" {
		return false
	}
	for _, value := range r.Header.Values("Connection") {
		for _, token := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(token), "upgrade") {
				return true
			}
		}
	}
	return false
}

// acceptsGzip reports whether the Accept-Encoding values allow gzip. An
// explicit gzip (or x-gzip) entry decides, and "*" decides otherwise. A q of 0, or
// one that does not parse, refuses.
func acceptsGzip(values []string) bool {
	gzipQ, anyQ := -1.0, -1.0
	for _, value := range values {
		for _, entry := range strings.Split(value, ",") {
			coding, params, _ := strings.Cut(entry, ";")
			q := 1.0
			for _, param := range strings.Split(params, ";") {
				key, val, ok := strings.Cut(param, "=")
				if !ok || !strings.EqualFold(strings.TrimSpace(key), "q") {
					continue
				}
				parsed, err := strconv.ParseFloat(strings.TrimSpace(val), 64)
				if err != nil || !(parsed >= 0 && parsed <= 1) {
					parsed = 0
				}
				q = parsed
			}
			switch strings.ToLower(strings.TrimSpace(coding)) {
			case "gzip", "x-gzip":
				gzipQ = max(gzipQ, q)
			case "*":
				anyQ = max(anyQ, q)
			}
		}
	}
	if gzipQ >= 0 {
		return gzipQ > 0
	}
	return anyQ > 0
}

// incompressibleTypes are media types whose content is already compressed.
var incompressibleTypes = map[string]bool{
	"application/zip":              true,
	"application/gzip":             true,
	"application/x-gzip":           true,
	"application/x-bzip2":          true,
	"application/x-xz":             true,
	"application/x-7z-compressed":  true,
	"application/vnd.rar":          true,
	"application/x-rar-compressed": true,
	"application/zstd":             true,
	"application/x-zstd":           true,
	"application/font-woff":        true,
	"font/woff":                    true,
	"font/woff2":                   true,
	"text/event-stream":            true, // not compressed data, but a stream that must not be buffered
}

// compressibleImages are the image types that are not compressed already.
var compressibleImages = map[string]bool{
	"image/svg+xml":            true,
	"image/bmp":                true,
	"image/x-icon":             true,
	"image/vnd.microsoft.icon": true,
}

// compressible reports whether a response with this status and header could
// be gzipped. known is false only while the content type is still unset, so
// the caller can wait for body bytes to sniff.
func compressible(status int, h http.Header) (ok, known bool) {
	switch {
	case status < 200, status == http.StatusNoContent, status == http.StatusPartialContent, status == http.StatusNotModified:
		return false, true
	case h.Get("Content-Encoding") != "", h.Get("Content-Range") != "":
		return false, true
	}
	values, typed := h["Content-Type"]
	if !typed {
		return false, false
	}
	if len(values) == 0 || values[0] == "" {
		return false, true
	}
	media, _, _ := strings.Cut(values[0], ";")
	media = strings.ToLower(strings.TrimSpace(media))
	if incompressibleTypes[media] {
		return false, true
	}
	if major, _, _ := strings.Cut(media, "/"); major == "video" || major == "audio" || (major == "image" && !compressibleImages[media]) {
		return false, true
	}
	return true, true
}

type compressMode int

const (
	compressUndecided compressMode = iota
	compressIdentity
	compressGzip
)

// compressWriter buffers the start of a body until it knows whether to gzip
// it, then commits to gzip or identity for the rest of the response.
type compressWriter struct {
	http.ResponseWriter
	pool    *sync.Pool
	minSize int
	accepts bool // the client allows gzip

	status int // status set by the handler; 0 before WriteHeader or Write
	buf    []byte
	mode   compressMode
	gz     *gzip.Writer
	err    error // first write failure; later writes return it
}

// WriteHeader records the status. Informational (1xx) statuses pass straight
// through. A repeated call is ignored.
func (cw *compressWriter) WriteHeader(code int) {
	if code >= 100 && code <= 199 {
		cw.ResponseWriter.WriteHeader(code)
		return
	}
	if cw.status != 0 {
		return
	}
	cw.status = code
	cw.fail(cw.decide(false))
}

// Write buffers p until the response is committed, then writes through the
// gzip stream or directly.
func (cw *compressWriter) Write(p []byte) (int, error) {
	if cw.err != nil {
		return 0, cw.err
	}
	if cw.status == 0 {
		cw.status = http.StatusOK
	}
	switch cw.mode {
	case compressIdentity:
		return cw.ResponseWriter.Write(p)
	case compressGzip:
		n, err := cw.gz.Write(p)
		cw.fail(err)
		return n, err
	}
	cw.buf = append(cw.buf, p...)
	if err := cw.decide(false); err != nil {
		cw.fail(err)
		return 0, err
	}
	return len(p), nil
}

// Flush commits the response (uncompressed when the minimum size has not been
// reached), flushes any gzip stream, and flushes the underlying writer.
func (cw *compressWriter) Flush() {
	cw.fail(cw.FlushError())
}

// FlushError is Flush with its error, for http.ResponseController.
func (cw *compressWriter) FlushError() error {
	if cw.err != nil {
		return cw.err
	}
	if cw.mode == compressUndecided {
		if cw.status == 0 {
			cw.status = http.StatusOK
		}
		if err := cw.decide(true); err != nil {
			cw.fail(err)
			return err
		}
	}
	if cw.mode == compressGzip {
		if err := cw.gz.Flush(); err != nil {
			cw.fail(err)
			return err
		}
	}
	err := http.NewResponseController(cw.ResponseWriter).Flush()
	if errors.Is(err, http.ErrNotSupported) {
		return nil // nothing to flush beyond what was written
	}
	return err
}

// Unwrap exposes the underlying writer to http.ResponseController.
func (cw *compressWriter) Unwrap() http.ResponseWriter {
	return cw.ResponseWriter
}

func (cw *compressWriter) fail(err error) {
	if err != nil && cw.err == nil {
		cw.err = err
	}
}

// decide commits the response once enough is known. final means no more body
// is coming before the bytes must go out (the handler returned or flushed).
func (cw *compressWriter) decide(final bool) error {
	h := cw.Header()
	if _, typed := h["Content-Type"]; !typed && len(cw.buf) > 0 {
		h.Set("Content-Type", http.DetectContentType(cw.buf))
	}
	ok, known := compressible(cw.status, h)
	switch {
	case !known && !final:
		return nil
	case !ok:
		return cw.commit(false, false)
	case !cw.accepts:
		return cw.commit(false, true)
	case len(cw.buf) >= cw.minSize:
		return cw.commit(true, true)
	case final:
		return cw.commit(false, true)
	}
	return nil
}

// commit sends the header and any buffered bytes, gzipped or not.
func (cw *compressWriter) commit(compress, vary bool) error {
	h := cw.Header()
	if vary {
		addVary(h, "Accept-Encoding")
	}
	cw.mode = compressIdentity
	if compress {
		cw.mode = compressGzip
		h.Set("Content-Encoding", "gzip")
		h.Del("Content-Length")
		h.Del("Accept-Ranges")
		if etag := h.Get("ETag"); etag != "" && !strings.HasPrefix(etag, "W/") {
			h.Set("ETag", "W/"+etag)
		}
		cw.gz = cw.pool.Get().(*gzip.Writer)
		cw.gz.Reset(cw.ResponseWriter)
	}
	cw.ResponseWriter.WriteHeader(cw.status)
	buffered := cw.buf
	cw.buf = nil
	if len(buffered) == 0 {
		return nil
	}
	var err error
	if compress {
		_, err = cw.gz.Write(buffered)
	} else {
		_, err = cw.ResponseWriter.Write(buffered)
	}
	return err
}

// finish commits whatever the handler left buffered and closes the gzip
// stream, returning its writer to the pool.
func (cw *compressWriter) finish() error {
	if cw.mode == compressUndecided {
		if cw.status == 0 {
			return cw.err // the handler wrote nothing; net/http sends its default
		}
		cw.fail(cw.decide(true))
	}
	if cw.mode == compressGzip && cw.gz != nil {
		cw.fail(cw.gz.Close())
		cw.gz.Reset(io.Discard)
		cw.pool.Put(cw.gz)
		cw.gz = nil
	}
	return cw.err
}

// addVary adds token to the Vary header unless it, or "*", is already listed.
func addVary(h http.Header, token string) {
	for _, value := range h.Values("Vary") {
		for _, existing := range strings.Split(value, ",") {
			existing = strings.TrimSpace(existing)
			if existing == "*" || strings.EqualFold(existing, token) {
				return
			}
		}
	}
	h.Add("Vary", token)
}
