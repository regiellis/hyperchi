package hyperchi

import (
	"net"
	"net/http"

	"github.com/go-chi/chi/v5/middleware"
)

// ClientIP returns the client's IP address without trusting anything the
// client can forge. It is the address a chi ClientIPFrom* middleware stored
// for the request, or else the TCP peer.
//
// AutoMiddleware installs Middleware.ClientIP, which uses the TCP peer, the
// right answer for a server facing the internet directly. Behind a reverse
// proxy, list the proxy's addresses in Config.Security.TrustedProxies so the
// address it forwards is used, never a header a client sent. Without
// AutoMiddleware, install a chi middleware yourself:
//
//	app.Use(middleware.ClientIPFromXFF("10.0.0.0/8")) // the proxy's addresses
func ClientIP(r *http.Request) string {
	if ip := middleware.GetClientIP(r.Context()); ip != "" {
		return ip
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// ClientIP returns the client's IP address; see the package-level ClientIP.
func (c *Context) ClientIP() string { return ClientIP(c.r) }
