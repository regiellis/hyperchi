package hyperchi

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"html/template"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	cws "github.com/coder/websocket"
	"github.com/regiellis/hyperchi/hyperchi/extensions/sse"
)

func gunzipBody(t *testing.T, body []byte) string {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("gzip header: %v", err)
	}
	out, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("gunzip: %v", err)
	}
	if err := zr.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return string(out)
}

func hasVaryAcceptEncoding(h http.Header) bool {
	for _, v := range h.Values("Vary") {
		for _, token := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(token), "Accept-Encoding") {
				return true
			}
		}
	}
	return false
}

func TestCompressNegotiatesAndSkips(t *testing.T) {
	large := strings.Repeat("<p>hello compress</p>", 200) // 4200 bytes
	png := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0}, 4096)...)

	cases := []struct {
		name     string
		accept   string
		header   map[string]string
		status   int
		body     string
		gzip     bool
		vary     bool
		wantType string
	}{
		{name: "large html", accept: "gzip", header: map[string]string{"Content-Type": "text/html; charset=utf-8"}, body: large, gzip: true, vary: true},
		{name: "small body", accept: "gzip", header: map[string]string{"Content-Type": "text/html"}, body: "<p>tiny</p>", vary: true},
		{name: "gzip refused with q=0", accept: "gzip;q=0", header: map[string]string{"Content-Type": "text/html"}, body: large, vary: true},
		{name: "gzip refused with q=0.000", accept: "br, gzip;q=0.000", header: map[string]string{"Content-Type": "text/html"}, body: large, vary: true},
		{name: "no accept-encoding", header: map[string]string{"Content-Type": "text/html"}, body: large, vary: true},
		{name: "wildcard", accept: "*", header: map[string]string{"Content-Type": "text/css"}, body: large, gzip: true, vary: true},
		{name: "uppercase and weighted", accept: "deflate, GZIP;Q=0.5", header: map[string]string{"Content-Type": "application/json"}, body: large, gzip: true, vary: true},
		{name: "already encoded", accept: "gzip", header: map[string]string{"Content-Type": "text/html", "Content-Encoding": "br"}, body: large},
		{name: "png", accept: "gzip", header: map[string]string{"Content-Type": "image/png"}, body: string(png)},
		{name: "svg", accept: "gzip", header: map[string]string{"Content-Type": "image/svg+xml"}, body: large, gzip: true, vary: true},
		{name: "woff2", accept: "gzip", header: map[string]string{"Content-Type": "font/woff2"}, body: large},
		{name: "video", accept: "gzip", header: map[string]string{"Content-Type": "video/mp4"}, body: large},
		{name: "audio", accept: "gzip", header: map[string]string{"Content-Type": "audio/mpeg"}, body: large},
		{name: "zip", accept: "gzip", header: map[string]string{"Content-Type": "application/zip"}, body: large},
		{name: "event stream", accept: "gzip", header: map[string]string{"Content-Type": "text/event-stream"}, body: large},
		{name: "sniffed png", accept: "gzip", body: string(png), wantType: "image/png"},
		{name: "sniffed html", accept: "gzip", body: "<html>" + large, gzip: true, vary: true, wantType: "text/html; charset=utf-8"},
		{name: "not found keeps status", accept: "gzip", header: map[string]string{"Content-Type": "text/html"}, status: 404, body: large, gzip: true, vary: true},
		{name: "partial content", accept: "gzip", header: map[string]string{"Content-Type": "text/plain", "Content-Range": "bytes 0-4199/9000"}, status: 206, body: large},
		{name: "no content", accept: "gzip", status: 204},
		{name: "not modified", accept: "gzip", header: map[string]string{"Content-Type": "text/html"}, status: 304},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := CompressWithOptions(CompressOptions{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				for k, v := range tc.header {
					w.Header().Set(k, v)
				}
				if tc.status != 0 {
					w.WriteHeader(tc.status)
				}
				if tc.body != "" {
					if _, err := io.WriteString(w, tc.body); err != nil {
						t.Errorf("write: %v", err)
					}
				}
			}))
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.accept != "" {
				req.Header.Set("Accept-Encoding", tc.accept)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			wantStatus := tc.status
			if wantStatus == 0 {
				wantStatus = http.StatusOK
			}
			if rec.Code != wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, wantStatus)
			}
			gotGzip := rec.Header().Get("Content-Encoding") == "gzip"
			if gotGzip != tc.gzip {
				t.Fatalf("Content-Encoding = %q, want gzip=%v", rec.Header().Get("Content-Encoding"), tc.gzip)
			}
			body := rec.Body.String()
			if gotGzip {
				body = gunzipBody(t, rec.Body.Bytes())
			}
			if body != tc.body {
				t.Errorf("body differs: got %d bytes, want %d", len(body), len(tc.body))
			}
			if got := hasVaryAcceptEncoding(rec.Header()); got != tc.vary {
				t.Errorf("Vary: Accept-Encoding = %v, want %v (Vary %q)", got, tc.vary, rec.Header().Values("Vary"))
			}
			if tc.wantType != "" && rec.Header().Get("Content-Type") != tc.wantType {
				t.Errorf("Content-Type = %q, want %q", rec.Header().Get("Content-Type"), tc.wantType)
			}
			if tc.header["Content-Encoding"] == "br" && rec.Header().Get("Content-Encoding") != "br" {
				t.Errorf("existing Content-Encoding replaced: %q", rec.Header().Get("Content-Encoding"))
			}
		})
	}
}

func TestAcceptsGzip(t *testing.T) {
	cases := map[string]bool{
		"":                      false,
		"gzip":                  true,
		"x-gzip":                true,
		"gzip;q=0":              false,
		"gzip; q=0":             false,
		"gzip;q=0.001":          true,
		"gzip;q=1.5":            false, // out of range
		"gzip;q=NaN":            false,
		"gzip;q=abc":            false,
		"identity":              false,
		"deflate, br":           false,
		"*":                     true,
		"*;q=0":                 false,
		"*;q=0, gzip":           true,
		"gzip;q=0, *":           false,
		"gzip;level=1":          true,
		" GZip ; Q=0.8 , br":    true,
		"br;q=1, gzip;q=0, *;q": false,
	}
	for header, want := range cases {
		if got := acceptsGzip([]string{header}); got != want {
			t.Errorf("acceptsGzip(%q) = %v, want %v", header, got, want)
		}
	}
	if !acceptsGzip([]string{"br", "gzip"}) {
		t.Error("gzip in a second Accept-Encoding line was ignored")
	}
}

func TestCompressHeadersOnCompressedResponse(t *testing.T) {
	body := strings.Repeat("a", 4096)
	h := CompressWithOptions(CompressOptions{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("ETag", `"abc"`)
		w.Header().Set("Vary", "Origin")
		// Many small writes cross the minimum together.
		for i := 0; i < len(body); i += 64 {
			if _, err := io.WriteString(w, body[i:i+64]); err != nil {
				t.Errorf("write: %v", err)
			}
		}
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	hdr := rec.Header()
	if hdr.Get("Content-Encoding") != "gzip" {
		t.Fatalf("not compressed: %v", hdr)
	}
	if hdr.Get("Content-Length") != "" || hdr.Get("Accept-Ranges") != "" {
		t.Errorf("Content-Length %q, Accept-Ranges %q should be dropped", hdr.Get("Content-Length"), hdr.Get("Accept-Ranges"))
	}
	if hdr.Get("ETag") != `W/"abc"` {
		t.Errorf("ETag = %q, want a weak tag", hdr.Get("ETag"))
	}
	if !slices.Equal(hdr.Values("Vary"), []string{"Origin", "Accept-Encoding"}) {
		t.Errorf("Vary = %q", hdr.Values("Vary"))
	}
	if got := gunzipBody(t, rec.Body.Bytes()); got != body {
		t.Errorf("round trip lost data: %d bytes", len(got))
	}

	// Identity keeps Content-Length, and an existing Vary token is not repeated.
	small := CompressWithOptions(CompressOptions{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Content-Length", "5")
		w.Header().Set("Vary", "accept-encoding")
		if _, err := io.WriteString(w, "small"); err != nil {
			t.Errorf("write: %v", err)
		}
	}))
	rec = httptest.NewRecorder()
	small.ServeHTTP(rec, req)
	if rec.Header().Get("Content-Length") != "5" || len(rec.Header().Values("Vary")) != 1 {
		t.Errorf("identity headers = %v", rec.Header())
	}
}

func TestCompressOptions(t *testing.T) {
	body := strings.Repeat("b", 300)
	serve := func(options CompressOptions) *httptest.ResponseRecorder {
		h := CompressWithOptions(options)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			if _, err := io.WriteString(w, body); err != nil {
				t.Errorf("write: %v", err)
			}
		}))
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Accept-Encoding", "gzip")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	if rec := serve(CompressOptions{}); rec.Header().Get("Content-Encoding") != "" {
		t.Error("300 bytes compressed under the default 1 KiB minimum")
	}
	rec := serve(CompressOptions{MinSize: 256, Level: gzip.BestSpeed})
	if rec.Header().Get("Content-Encoding") != "gzip" || gunzipBody(t, rec.Body.Bytes()) != body {
		t.Error("MinSize 256 did not compress 300 bytes")
	}

	for _, bad := range []CompressOptions{{Level: 10}, {Level: -3}, {MinSize: -1}} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("CompressWithOptions(%+v) did not panic", bad)
				}
			}()
			CompressWithOptions(bad)
		}()
	}
}

func TestCompressPassesHeadAndUpgradeThrough(t *testing.T) {
	var sawWrapped []bool
	h := CompressWithOptions(CompressOptions{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, wrapped := w.(*compressWriter)
		sawWrapped = append(sawWrapped, wrapped)
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Content-Length", "4096")
	}))
	head := httptest.NewRequest(http.MethodHead, "/", nil)
	head.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, head)
	if rec.Header().Get("Content-Encoding") != "" || rec.Header().Get("Vary") != "" || rec.Header().Get("Content-Length") != "4096" {
		t.Errorf("HEAD headers changed: %v", rec.Header())
	}

	upgrade := httptest.NewRequest(http.MethodGet, "/ws", nil)
	upgrade.Header.Set("Accept-Encoding", "gzip")
	upgrade.Header.Set("Connection", "keep-alive, Upgrade")
	upgrade.Header.Set("Upgrade", "websocket")
	h.ServeHTTP(httptest.NewRecorder(), upgrade)

	plain := httptest.NewRequest(http.MethodGet, "/", nil)
	h.ServeHTTP(httptest.NewRecorder(), plain)

	if !slices.Equal(sawWrapped, []bool{false, false, true}) {
		t.Errorf("handler saw the compress writer on [HEAD, upgrade, GET] = %v", sawWrapped)
	}
}

func TestCompressWriteHeaderOnceAndEmptyBody(t *testing.T) {
	h := CompressWithOptions(CompressOptions{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		w.WriteHeader(http.StatusTeapot)
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated || rec.Body.Len() != 0 || rec.Header().Get("Content-Encoding") != "" {
		t.Errorf("got %d, %d bytes, CE %q", rec.Code, rec.Body.Len(), rec.Header().Get("Content-Encoding"))
	}

	silent := CompressWithOptions(CompressOptions{})(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	rec = httptest.NewRecorder()
	silent.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.Len() != 0 || len(rec.Header()) != 0 {
		t.Errorf("silent handler produced %d %v", rec.Code, rec.Header())
	}
}

// Pooled gzip writers are reused across concurrent responses; each body must
// still decode to its own content.
func TestCompressPoolUnderConcurrency(t *testing.T) {
	h := CompressWithOptions(CompressOptions{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		if _, err := io.WriteString(w, strings.Repeat(r.URL.Query().Get("c"), 2048)); err != nil {
			t.Errorf("write: %v", err)
		}
	}))
	var wg sync.WaitGroup
	for i := range 32 {
		wg.Go(func() {
			c := string(rune('a' + i%26))
			req := httptest.NewRequest(http.MethodGet, "/?c="+c, nil)
			req.Header.Set("Accept-Encoding", "gzip")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			zr, err := gzip.NewReader(rec.Body)
			if err != nil {
				t.Errorf("gzip: %v", err)
				return
			}
			got, err := io.ReadAll(zr)
			if err != nil || string(got) != strings.Repeat(c, 2048) {
				t.Errorf("request %s: %d bytes, err %v", c, len(got), err)
			}
		})
	}
	wg.Wait()
}

// streamServer serves h and returns a client that leaves Accept-Encoding to
// the caller, so the response body arrives exactly as the server encoded it.
func streamServer(t *testing.T, h http.Handler) (*httptest.Server, *http.Client) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	transport := &http.Transport{DisableCompression: true}
	t.Cleanup(transport.CloseIdleConnections)
	return srv, &http.Client{Transport: transport}
}

func getStream(t *testing.T, ctx context.Context, client *http.Client, url string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept-Encoding", "gzip")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	t.Cleanup(func() {
		if err := resp.Body.Close(); err != nil {
			t.Logf("close body: %v", err)
		}
	})
	return resp
}

// A compressed stream that flushes must deliver what it has so far, while the
// handler is still running.
func TestCompressFlushDeliversGzipBeforeHandlerReturns(t *testing.T) {
	release := make(chan struct{})
	first := strings.Repeat("<li>row</li>", 200)
	srv, client := streamServer(t, CompressWithOptions(CompressOptions{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		if _, err := io.WriteString(w, first); err != nil {
			t.Errorf("write: %v", err)
			return
		}
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Errorf("flush: %v", err)
			return
		}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		if _, err := io.WriteString(w, "<li>last</li>"); err != nil {
			t.Errorf("write: %v", err)
		}
	})))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp := getStream(t, ctx, client, srv.URL)
	if resp.Header.Get("Content-Encoding") != "gzip" {
		t.Fatalf("Content-Encoding = %q", resp.Header.Get("Content-Encoding"))
	}
	zr, err := gzip.NewReader(resp.Body)
	if err != nil {
		t.Fatalf("gzip header: %v", err)
	}
	got := make([]byte, len(first))
	if _, err := io.ReadFull(zr, got); err != nil {
		t.Fatalf("flushed data did not arrive: %v", err)
	}
	if string(got) != first {
		t.Fatal("flushed data differs")
	}
	close(release)
	rest, err := io.ReadAll(zr)
	if err != nil || string(rest) != "<li>last</li>" {
		t.Fatalf("tail = %q, %v", rest, err)
	}
}

// A response that flushes before reaching the minimum is a stream: it goes out
// uncompressed and immediately.
func TestCompressEarlyFlushSendsIdentity(t *testing.T) {
	release := make(chan struct{})
	srv, client := streamServer(t, CompressWithOptions(CompressOptions{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		if _, err := io.WriteString(w, "{\"n\":1}\n"); err != nil {
			t.Errorf("write: %v", err)
			return
		}
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		if _, err := io.WriteString(w, strings.Repeat("{\"n\":2}\n", 500)); err != nil {
			t.Errorf("write: %v", err)
		}
	})))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp := getStream(t, ctx, client, srv.URL)
	if resp.Header.Get("Content-Encoding") != "" || !hasVaryAcceptEncoding(resp.Header) {
		t.Fatalf("headers = %v", resp.Header)
	}
	line, err := bufio.NewReader(resp.Body).ReadString('\n')
	if err != nil || line != "{\"n\":1}\n" {
		t.Fatalf("first line = %q, %v", line, err)
	}
	close(release)
}

// readSSEEvent reads lines until it has seen the named event's data line.
func readSSEEvent(t *testing.T, r *bufio.Reader, event string) string {
	t.Helper()
	inEvent := false
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("waiting for %q: %v", event, err)
		}
		line = strings.TrimSuffix(line, "\n")
		switch {
		case line == "event: "+event:
			inEvent = true
		case inEvent && strings.HasPrefix(line, "data: "):
			return strings.TrimPrefix(line, "data: ")
		case line == "":
			inEvent = false
		}
	}
}

// compressedApp is a development app with AutoMiddleware and Compress, the
// order the documentation recommends.
func compressedApp(t *testing.T) *HyperChi {
	t.Helper()
	app := New()
	t.Cleanup(func() {
		if err := app.CloseDB(); err != nil {
			t.Errorf("close db: %v", err)
		}
	})
	app.AutoMiddleware()
	app.Use(app.Middleware.Compress())
	return app
}

// SSE through AutoMiddleware and Compress: events arrive uncompressed and as
// they are sent, including one well over the compression minimum.
func TestCompressLeavesSSEStreaming(t *testing.T) {
	app := compressedApp(t)
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

// A WebSocket upgrade through AutoMiddleware and Compress still hijacks the
// connection and exchanges messages.
func TestCompressLeavesWebSocketUpgrade(t *testing.T) {
	app := compressedApp(t)
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
		if cerr := resp.Body.Close(); cerr != nil {
			t.Errorf("close handshake body: %v", cerr)
		}
	}
	if err != nil {
		t.Fatalf("dial through Compress: %v", err)
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

// A panic before anything is written reaches Recovery with nothing sent, so
// the client gets the error page rather than a broken gzip stream.
func TestCompressPanicLeavesRecoveryAClean500(t *testing.T) {
	app := compressedApp(t)
	app.Get("/boom", func(c *Context) error {
		c.Response().Header().Set("Content-Type", "text/html")
		if _, err := io.WriteString(c.Response(), "<p>partial</p>"); err != nil {
			return err
		}
		panic("boom")
	})
	req := httptest.NewRequest(http.MethodGet, "/boom", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError || rec.Header().Get("Content-Encoding") == "gzip" {
		t.Fatalf("got %d CE %q", rec.Code, rec.Header().Get("Content-Encoding"))
	}
	if strings.Contains(rec.Body.String(), "<p>partial</p>") {
		t.Error("the buffered partial body leaked ahead of the error page")
	}
}
