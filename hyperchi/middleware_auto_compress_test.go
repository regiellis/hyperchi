package hyperchi

import (
	"bufio"
	"context"
	"html/template"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	cws "github.com/coder/websocket"
	"github.com/regiellis/hyperchi/hyperchi/extensions/sse"
)

// productionAutoApp is a production app with AutoMiddleware and nothing else,
// so compression comes from AutoMiddleware alone.
func productionAutoApp(t *testing.T) *HyperChi {
	t.Helper()
	app := productionTestApp(t)
	app.AutoMiddleware()
	return app
}

func gzipPageRequest() *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/page", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	return req
}

func TestAutoMiddlewareGzipsHTMLInProduction(t *testing.T) {
	app := productionAutoApp(t)
	page := "<html><body>" + strings.Repeat("<p>hello</p>", 400) + "</body></html>"
	app.Get("/page", func(c *Context) error { return c.HTML(template.HTML(page)) })

	w := serve(app, gzipPageRequest())
	if w.Code != http.StatusOK || w.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("GET /page = %d, Content-Encoding %q", w.Code, w.Header().Get("Content-Encoding"))
	}
	if !hasVaryAcceptEncoding(w.Header()) {
		t.Errorf("Vary = %v, want Accept-Encoding", w.Header().Values("Vary"))
	}
	if got := gunzipBody(t, w.Body.Bytes()); got != page {
		t.Fatalf("gunzipped page is %d bytes, want %d", len(got), len(page))
	}
}

// Development leaves responses uncompressed so they read plainly in tools.
func TestAutoMiddlewareDoesNotCompressInDevelopment(t *testing.T) {
	app := newTemplateApp(t, false)
	app.AutoMiddleware()
	app.Get("/page", func(c *Context) error { return c.HTML(template.HTML(strings.Repeat("<p>hello</p>", 400))) })
	if w := serve(app, gzipPageRequest()); w.Header().Get("Content-Encoding") != "" {
		t.Fatalf("development response Content-Encoding %q, want none", w.Header().Get("Content-Encoding"))
	}
}

// Apps that followed the old advice add Compress after AutoMiddleware. The
// outer layer must leave the inner one's gzip alone, not wrap it twice.
func TestAutoMiddlewareWithExtraCompressGzipsOnce(t *testing.T) {
	app := productionAutoApp(t)
	app.Use(app.Middleware.Compress())
	page := strings.Repeat("<p>hello</p>", 400)
	app.Get("/page", func(c *Context) error { return c.HTML(template.HTML(page)) })
	w := serve(app, gzipPageRequest())
	if w.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("Content-Encoding %q, want gzip", w.Header().Get("Content-Encoding"))
	}
	if got := gunzipBody(t, w.Body.Bytes()); got != page {
		t.Fatalf("one gunzip did not recover the page: %d bytes", len(got))
	}
}

// A masked CSRF token in a compressed page still validates after the client
// decompresses it.
func TestAutoMiddlewareCompressedPageCarriesAWorkingCSRFToken(t *testing.T) {
	app := productionAutoApp(t)
	app.Use(app.Middleware.CSRF())
	app.Get("/page", func(c *Context) error {
		return c.HTML(template.HTML(`<input name="csrf_token" value="` + c.CSRFToken() + `">` + strings.Repeat("<p>pad</p>", 300)))
	})
	app.Post("/save", func(c *Context) error { return c.Text("saved") })

	w := serve(app, gzipPageRequest())
	if w.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("Content-Encoding %q, want gzip", w.Header().Get("Content-Encoding"))
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies = %v", cookies)
	}
	body := gunzipBody(t, w.Body.Bytes())
	_, rest, _ := strings.Cut(body, `value="`)
	token, _, _ := strings.Cut(rest, `"`)
	if token == "" || strings.Contains(body, cookies[0].Value) {
		t.Fatalf("page token %q; page must not carry the raw cookie value", token)
	}
	req := httptest.NewRequest(http.MethodPost, "/save", strings.NewReader("csrf_token="+token))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookies[0])
	if got := serve(app, req); got.Code != http.StatusOK {
		t.Fatalf("POST with the page's token = %d", got.Code)
	}
}

func TestAutoMiddlewareProductionLeavesSSEStreaming(t *testing.T) {
	app := productionAutoApp(t)
	app.Extensions().EnableSSE("/sse")
	srv, client := streamServer(t, app)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp := getStream(t, ctx, client, srv.URL+"/sse?channels=news")
	if resp.Header.Get("Content-Encoding") != "" || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("SSE headers = %v", resp.Header)
	}
	body := bufio.NewReader(resp.Body)
	readSSEEvent(t, body, "connected")

	big := strings.Repeat("x", 4096)
	app.Extensions().GetSSEHub().Broadcast("news", sse.SSEMessage{Event: "big", Data: big})
	if got := readSSEEvent(t, body, "big"); got != big {
		t.Fatalf("big event data = %d bytes, want %d plain bytes", len(got), len(big))
	}
}

func TestAutoMiddlewareProductionLeavesWebSocketUpgrade(t *testing.T) {
	app := productionAutoApp(t)
	app.WebSocket("/ws", func(s *Socket) error {
		return s.SendHTML("#log", "beforeend", template.HTML(strings.Repeat("<p>hi</p>", 300)))
	})
	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, resp, err := cws.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/ws", &cws.DialOptions{
		HTTPHeader: http.Header{"Accept-Encoding": {"gzip"}},
	})
	if resp != nil && resp.Body != nil {
		if _, rerr := io.Copy(io.Discard, resp.Body); rerr != nil {
			t.Errorf("drain handshake body: %v", rerr)
		}
		if cerr := resp.Body.Close(); cerr != nil {
			t.Errorf("close handshake body: %v", cerr)
		}
	}
	if err != nil {
		t.Fatalf("dial through production AutoMiddleware: %v", err)
	}
	t.Cleanup(func() {
		if err := conn.CloseNow(); err != nil {
			t.Logf("close: %v", err)
		}
	})
	if resp.StatusCode != http.StatusSwitchingProtocols || resp.Header.Get("Content-Encoding") != "" {
		t.Fatalf("handshake = %d %v", resp.StatusCode, resp.Header)
	}
	msg := readWire(t, ctx, conn)
	if msg.Target != "#log" || !strings.Contains(msg.Content, "<p>hi</p>") {
		t.Fatalf("message = %+v", msg)
	}
}
