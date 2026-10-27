package hyperchi

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

// ensureHotReloadRoute registers /__hyperchi/hot-reload once, when hot reload
// is configured. Route registration calls it, so the route lands after any
// middleware the app added first.
func (g *HyperChi) ensureHotReloadRoute() {
	if !g.hotReloadRouteSetup && g.Config.Assets.HotReload && g.Assets != nil {
		g.router.Get("/__hyperchi/hot-reload", g.Assets.HandleWebSocket)
		g.hotReloadRouteSetup = true
	}
}

// RedirectRoute registers a GET route that redirects to target with code, or
// 302 when code is 0.
func (g *HyperChi) RedirectRoute(pattern, target string, code int) {
	if code == 0 {
		code = 302
	}
	g.router.Get(pattern, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target, code)
	})
}

// FileServer serves dir at pattern like Static, with a public max-age of
// maxAge seconds when maxAge > 0. It panics when dir cannot be served.
func (g *HyperChi) FileServer(pattern, dir string, maxAge int) {
	options := StaticOptions{Development: g.isDevelopment()}
	if maxAge > 0 {
		options.CacheControl = fmt.Sprintf("public, max-age=%d", maxAge)
	}
	if err := g.StaticWithOptions(pattern, dir, options); err != nil {
		panic(err)
	}
}

// Static serves the files in dir at pattern with StaticWithOptions' rules. It
// panics when dir cannot be served; use StaticWithOptions to get the error.
func (g *HyperChi) Static(pattern, dir string) {
	if err := g.StaticWithOptions(pattern, dir, StaticOptions{Development: g.isDevelopment()}); err != nil {
		panic(err)
	}
}

// Group registers routes under pattern with their own middleware (sub.Use).
// The sub-app is a shallow copy of the parent, so every service (DB, KV,
// schemas, events, content, ...) works on it; only route registration targets
// the group. Static mounts made on the sub-app serve under the group's prefix
// and are known to AssetURL by their full path.
func (g *HyperChi) Group(pattern string, fn func(r *HyperChi)) {
	g.router.Route(pattern, func(r chi.Router) {
		subApp := *g
		subApp.router = r
		subApp.routePrefix = strings.TrimSuffix(g.routePrefix+pattern, "/")
		fn(&subApp)
	})
}

// Mount attaches an http.Handler, such as another router, under pattern.
func (g *HyperChi) Mount(pattern string, subRouter http.Handler) {
	g.router.Mount(pattern, subRouter)
}

// Use adds middleware with chi's Use, which panics once a route is
// registered: call it first, or use UseGlobal.
func (g *HyperChi) Use(middlewares ...func(http.Handler) http.Handler) {
	g.router.Use(middlewares...)
}

// UseGlobal adds middleware that wraps the whole router when Serve (or
// ServeHTTP) builds the handler, so it may be called before or after routes
// are registered, unlike chi's Use. Wrappers run in the order added, outermost
// first, ahead of chi's own middleware chain.
func (g *HyperChi) UseGlobal(middlewares ...func(http.Handler) http.Handler) {
	g.deferredMiddleware = append(g.deferredMiddleware, middlewares...)
}

// buildHandler wraps the router with any deferred global middleware (see
// UseGlobal), outermost first, and returns the handler Serve mounts.
func (g *HyperChi) buildHandler() http.Handler {
	var h http.Handler = g.router
	for i := len(g.deferredMiddleware) - 1; i >= 0; i-- {
		h = g.deferredMiddleware[i](h)
	}
	return h
}

// ServeHTTP makes the app an http.Handler. It applies the same UseGlobal
// middleware that Serve does, rebuilding the chain on each request.
func (g *HyperChi) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.buildHandler().ServeHTTP(w, r)
}

// Router returns the chi router, the escape hatch for raw net/http
// registration.
func (g *HyperChi) Router() chi.Router {
	return g.router
}
