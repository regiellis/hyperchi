package hyperchi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5/middleware"
)

func TestClientIPIgnoresForwardingHeaders(t *testing.T) {
	app := newTemplateApp(t, false)
	app.AutoMiddleware()
	app.Get("/ip", func(c *Context) error { return c.Text(c.ClientIP() + " " + c.Request().RemoteAddr) })

	req := httptest.NewRequest(http.MethodGet, "/ip", nil)
	req.RemoteAddr = "198.51.100.7:4242"
	req.Header.Set("X-Forwarded-For", "6.6.6.6")
	req.Header.Set("X-Real-IP", "6.6.6.6")
	req.Header.Set("True-Client-IP", "6.6.6.6")
	if body := serve(app, req).Body.String(); body != "198.51.100.7 198.51.100.7:4242" {
		t.Fatalf("client IP or RemoteAddr came from a header: %q", body)
	}
}

func TestClientIPFromDeclaredProxy(t *testing.T) {
	app := newTemplateApp(t, false)
	app.AutoMiddleware()
	app.Use(middleware.ClientIPFromXFF("10.0.0.0/8"))
	app.Get("/ip", func(c *Context) error { return c.Text(c.ClientIP()) })

	req := httptest.NewRequest(http.MethodGet, "/ip", nil)
	req.RemoteAddr = "10.0.0.2:5000"
	// The client forged the first entry; the proxy appended the real peer.
	req.Header.Set("X-Forwarded-For", "6.6.6.6, 203.0.113.9")
	if body := serve(app, req).Body.String(); body != "203.0.113.9" {
		t.Fatalf("client IP behind a declared proxy = %q", body)
	}
}

func TestBuiltInRateLimitIgnoresForgedForwardingHeaders(t *testing.T) {
	app := newTemplateApp(t, false)
	app.Use(app.Middleware.RateLimit(2, time.Minute))
	app.Get("/", func(c *Context) error { return c.Text("ok") })

	codes := make([]int, 0, 3)
	for _, forged := range []string{"1.1.1.1", "2.2.2.2", "3.3.3.3"} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "198.51.100.7:4242"
		req.Header.Set("X-Forwarded-For", forged)
		codes = append(codes, serve(app, req).Code)
	}
	if codes[2] != http.StatusTooManyRequests {
		t.Fatalf("rotating X-Forwarded-For bypassed the limit: %v", codes)
	}
}
