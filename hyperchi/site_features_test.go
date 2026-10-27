package hyperchi

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/regiellis/hyperchi/hyperchi/config"
)

func TestPrerenderConditionalAndErrorResponses(t *testing.T) {
	app := &HyperChi{}
	dir := t.TempDir()
	writeTemplate(t, dir, "page.tmpl", `{{define "page"}}<p>{{.title}}</p>{{end}}{{define "layout-base"}}<main>{{.content}}</main>{{end}}`)
	if err := app.LoadTemplates(filepath.Join(dir, "*.tmpl")); err != nil {
		t.Fatal(err)
	}
	data := H{"title": "<script>"}
	h, err := app.PreRender("page", data, PreRenderOptions{Layout: "base"})
	if err != nil {
		t.Fatal(err)
	}
	data["title"] = "changed"
	w := httptest.NewRecorder()
	serveH := app.ToHTTP(h)
	serveH(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != 200 || w.Body.String() != `<main><p>&lt;script&gt;</p></main>` {
		t.Fatalf("%d %q", w.Code, w.Body.String())
	}
	tag := w.Header().Get("ETag")
	for _, method := range []string{"GET", "HEAD"} {
		r := httptest.NewRequest(method, "/", nil)
		r.Header.Set("If-None-Match", tag)
		w = httptest.NewRecorder()
		serveH(w, r)
		if w.Code != 304 || w.Body.Len() != 0 {
			t.Errorf("%s conditional: %d %q", method, w.Code, w.Body.String())
		}
	}
	w = httptest.NewRecorder()
	serveH(w, httptest.NewRequest("HEAD", "/", nil))
	if w.Code != 200 || w.Body.Len() != 0 {
		t.Error("HEAD did not preserve status and omit body")
	}
	if _, err := app.PreRender("page", nil, PreRenderOptions{Layout: "missing"}); err == nil {
		t.Error("missing layout accepted")
	}
	errorPage, err := app.PreRender("page", nil, PreRenderOptions{Status: 404})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("If-None-Match", "*")
	w = httptest.NewRecorder()
	app.ToHTTP(errorPage)(w, r)
	if w.Code != 404 || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("ETag") != "" {
		t.Error("error document became cacheable or conditional")
	}
}

func TestStaticPublicationConfinement(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "public")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"public/a.txt": "published", "public/.hidden": "hidden", "secret": "secret"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(dir, "secret"), filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	h, err := NewStaticHandler(root, StaticOptions{OnError: func(w http.ResponseWriter, _ *http.Request, code int, _ error) {
		w.WriteHeader(code)
		if _, err := fmt.Fprint(w, "branded"); err != nil {
			t.Error(err)
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, path string
		status       int
	}{{"GET", "/a.txt", 200}, {"HEAD", "/a.txt", 200}, {"POST", "/a.txt", 405}, {"GET", "/", 404}, {"GET", "/.hidden", 404}, {"GET", "/../secret", 404}, {"GET", "/%2e%2e/secret", 404}, {"GET", "/escape", 404}, {"GET", "/missing", 404}, {"HEAD", "/missing", 404}} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
		if w.Code != tc.status {
			t.Errorf("%s %s: %d", tc.method, tc.path, w.Code)
		}
		if tc.method == "HEAD" && w.Body.Len() != 0 {
			t.Error("HEAD body")
		}
		if tc.status >= 400 && (w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Robots-Tag") != "noindex") {
			t.Error("error headers")
		}
		if tc.status >= 400 && tc.method != "HEAD" && w.Body.String() != "branded" {
			t.Error("missing error callback")
		}
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/a.txt", nil)
	r.Header.Set("Range", "bytes=0-2")
	h.ServeHTTP(w, r)
	if w.Code != 206 || w.Body.String() != "pub" {
		t.Fatalf("range: %d %q", w.Code, w.Body.String())
	}
}

func TestUnifiedErrorsAndRecovery(t *testing.T) {
	app := productionTestApp(t)
	app.OnError(func(c *Context, err error) {
		code := http.StatusInternalServerError
		var he *HTTPError
		if errors.As(err, &he) {
			code = he.Code
		}
		if err := c.Status(code).Text(fmt.Sprintf("branded %d", code)); err != nil {
			t.Error(err)
		}
	})
	dir := t.TempDir()
	writeTemplate(t, dir, "ok.tmpl", `{{define "ok"}}ok{{end}}`)
	if err := app.LoadTemplates(filepath.Join(dir, "*.tmpl")); err != nil {
		t.Fatal(err)
	}
	for _, h := range []http.Handler{
		app.ToHTTP(func(*Context) error { return errors.New("private") }),
		app.ToHTTP(func(c *Context) error { return c.Fragment("missing", nil) }),
		app.Middleware.Recovery()(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("private") })),
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
		if w.Code != 500 || w.Body.String() != "branded 500" || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%d %q", w.Code, w.Body.String())
		}
	}
	for _, abort := range []bool{false, true} {
		func() {
			defer func() {
				if rec := recover(); rec != http.ErrAbortHandler {
					t.Errorf("got panic %v, want ErrAbortHandler", rec)
				}
			}()
			app.Middleware.Recovery()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if abort {
					panic(http.ErrAbortHandler)
				}
				w.WriteHeader(200)
				panic("late")
			})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
		}()
	}
}

func TestProxyTrustAndBoundedTokenBuckets(t *testing.T) {
	l, err := NewRateLimiterE(&RateLimitConfig{Burst: 2, RefillInterval: time.Second, MaxKeys: 2, TrustedProxies: []string{"10.0.0.0/8"}})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	l.now = func() time.Time { return now }
	r := httptest.NewRequest("POST", "/", nil)
	r.RemoteAddr = "10.0.0.1:80"
	r.Header.Set("X-Forwarded-For", "forged, 198.51.100.1, 10.0.0.2")
	if got := l.key(r); got != "198.51.100.1" {
		t.Fatalf("key %q", got)
	}
	for i := 0; i < 3; i++ {
		ok, _ := l.Allow(r)
		if ok != (i < 2) {
			t.Errorf("request %d allowed=%v", i, ok)
		}
	}
	now = now.Add(time.Second)
	if ok, _ := l.Allow(r); !ok {
		t.Error("token did not refill")
	}
	for _, peer := range []string{"192.0.2.1:1", "192.0.2.2:2", "192.0.2.3:3"} {
		r.RemoteAddr = peer
		l.Allow(r)
	}
	if len(l.buckets) > 2 {
		t.Error("unbounded key count")
	}
	if _, err := NewRateLimiterE(&RateLimitConfig{TrustedProxies: []string{"bad"}}); err == nil {
		t.Error("invalid proxy accepted")
	}
	// A proxy configuration must not authorize direct peers to spoof XFF.
	cfg := config.Production()
	cfg.Database.Disabled = true
	cfg.Security.TrustedProxies = []string{"10.0.0.0/8"}
	app, err := NewWithConfigE(cfg)
	if err != nil {
		t.Fatal(err)
	}
	h := app.Middleware.ClientIP()(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if got := chimiddleware.GetClientIP(r.Context()); got != "192.0.2.1" {
			t.Errorf("spoofed IP %q", got)
		}
	}))
	r.RemoteAddr = "192.0.2.1:123"
	h.ServeHTTP(httptest.NewRecorder(), r)
	if r.RemoteAddr != "192.0.2.1:123" {
		t.Error("peer mutated")
	}
}

func TestRateLimiterConcurrentBudget(t *testing.T) {
	l := NewRateLimiter(&RateLimitConfig{Burst: 10, RefillInterval: time.Hour, KeyGenerator: func(*http.Request) string { return "global" }})
	var wg sync.WaitGroup
	var mu sync.Mutex
	allowed := 0
	for range 100 {
		wg.Go(func() {
			ok, _ := l.Allow(httptest.NewRequest("POST", "/", nil))
			if ok {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if allowed != 10 {
		t.Fatalf("allowed %d", allowed)
	}
}

func TestProtectedForm(t *testing.T) {
	protect, err := ProtectForm(FormProtectionOptions{MaxBodyBytes: 32, OnError: func(w http.ResponseWriter, _ *http.Request, code int, _ error) {
		w.WriteHeader(code)
		if _, err := fmt.Fprint(w, "branded"); err != nil {
			t.Error(err)
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	h := protect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.PostForm.Get("email") == "" {
			t.Error("form not parsed")
		}
		w.WriteHeader(204)
	}))
	for _, tc := range []struct {
		origin, body, content string
		code                  int
	}{{"https://site.test", "email=a%40b.com", "application/x-www-form-urlencoded", 204}, {"https://evil.test", "email=a", "application/x-www-form-urlencoded", 403}, {"", strings.Repeat("a", 100), "application/x-www-form-urlencoded", 413}, {"", "email=%xx", "application/x-www-form-urlencoded", 400}, {"", "{}", "application/json", 415}} {
		r := httptest.NewRequest("POST", "https://site.test/", strings.NewReader(tc.body))
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("Content-Type", tc.content)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.code {
			t.Errorf("origin=%q body=%q: %d, want %d", tc.origin, tc.body, w.Code, tc.code)
		}
	}
	l := NewRateLimiter(&RateLimitConfig{Burst: 1, RefillInterval: time.Hour, ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ time.Duration) { w.WriteHeader(429) }, KeyGenerator: func(*http.Request) string { return "global" }})
	protect, err = ProtectForm(FormProtectionOptions{RateLimiter: l})
	if err != nil {
		t.Fatal(err)
	}
	h = protect(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	for i := 0; i < 2; i++ {
		r := httptest.NewRequest("POST", "https://site.test/", strings.NewReader(url.Values{"email": {"a@b.com"}}.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if i == 1 && (w.Code != 429 || w.Header().Get("Retry-After") == "") {
			t.Error("missing retry response")
		}
	}
}

func TestProductionPortIsExact(t *testing.T) {
	app := productionTestApp(t)
	app.Config.Server.PortFallback = true
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := ln.Close(); err != nil {
			t.Errorf("close listener: %v", err)
		}
	})
	got, err := app.listen(ln.Addr().String())
	if err == nil {
		if cerr := got.Close(); cerr != nil {
			t.Error(cerr)
		}
		t.Fatal("production silently changed port")
	}
}

func TestServeContextClosesDatabase(t *testing.T) {
	app := productionTestApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := app.ServeContext(ctx, "127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	if err := app.DB.PingContext(context.Background()); err == nil {
		t.Error("database stayed open")
	}
}

func TestContextErrorAfterWriteDoesNotAppendPage(t *testing.T) {
	app := productionTestApp(t)
	w := httptest.NewRecorder()
	app.ToHTTP(func(c *Context) error {
		c.Response().WriteHeader(202)
		if _, err := c.Response().Write([]byte("accepted")); err != nil {
			return err
		}
		return errors.New("late")
	})(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != 202 || w.Body.String() != "accepted" {
		t.Fatalf("committed response overwritten: %d %q", w.Code, w.Body.String())
	}
}
