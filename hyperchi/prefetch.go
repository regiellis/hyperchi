package hyperchi

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	_ "embed"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"time"
)

//go:embed extensions/scripts/hyperchi-prefetch.js
var prefetchJS []byte

// EnablePrefetch serves the opt-in browser prefetch runtime. Call once on the
// root app during setup, before serving traffic. Templates include
// {{prefetchScript}} and mark safe GET links data-hc-prefetch="hover",
// "viewport", or "both". Unmarked links are never prefetched.
//
// The runtime warms the browser HTTP cache; it does not override navigation or
// keep a separate response cache. Responses must have appropriate Cache-Control
// for reuse. This method adds Vary for the standard HTMX request headers to
// prevent full pages and fragments from sharing a cache entry. Handlers varying
// on additional headers must declare them too.
func (g *HyperChi) EnablePrefetch() error {
	if g.routePrefix != "" {
		return fmt.Errorf("enable prefetch on the root app")
	}
	if g.prefetchEnabled {
		return nil
	}
	g.prefetchEnabled = true
	g.UseGlobal(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writer := &prefetchVaryWriter{ResponseWriter: w}
			writer.vary()
			next.ServeHTTP(writer, r)
			writer.vary() // Also cover handlers that only set headers.

		})
	})
	tag := fmt.Sprintf(`"%x"`, sha256.Sum256(prefetchJS))
	g.Get("/hyperchi/js/prefetch.js", func(c *Context) error {
		c.SetHeader("Content-Type", "text/javascript; charset=utf-8").SetHeader("Cache-Control", "public, max-age=3600").SetHeader("ETag", tag)
		http.ServeContent(c.w, c.r, "prefetch.js", time.Time{}, bytes.NewReader(prefetchJS))
		return nil
	})
	return nil
}

// PrefetchScript returns the external runtime script tag when enabled, or
// empty HTML. Put {{prefetchScript}} after htmx in the full-page layout.
func (g *HyperChi) PrefetchScript() template.HTML {
	if !g.prefetchEnabled {
		return ""
	}
	return `<script defer src="/hyperchi/js/prefetch.js"></script>`
}

// Add Vary at the write boundary so an inner CORS/handler header assignment
// cannot discard the page/fragment distinction. ResponseController traverses
// Unwrap for deadlines and other optional operations.
type prefetchVaryWriter struct{ http.ResponseWriter }

func (w *prefetchVaryWriter) vary() {
	for _, name := range []string{"HX-Request", "HX-Request-Type", "HX-Boosted", "HX-Target", "HX-Source", "HX-Current-URL"} {
		addVary(w.Header(), name)
	}
}
func (w *prefetchVaryWriter) WriteHeader(status int)      { w.vary(); w.ResponseWriter.WriteHeader(status) }
func (w *prefetchVaryWriter) Write(b []byte) (int, error) { w.vary(); return w.ResponseWriter.Write(b) }
func (w *prefetchVaryWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *prefetchVaryWriter) Flush()                      { _ = w.FlushError() }
func (w *prefetchVaryWriter) FlushError() error {
	w.vary()
	return http.NewResponseController(w.ResponseWriter).Flush()
}
func (w *prefetchVaryWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(w.ResponseWriter).Hijack()
}
func (w *prefetchVaryWriter) Push(target string, options *http.PushOptions) error {
	if p, ok := w.ResponseWriter.(http.Pusher); ok {
		return p.Push(target, options)
	}
	return http.ErrNotSupported
}
