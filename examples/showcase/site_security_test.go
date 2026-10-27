package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestSiteBoundary(t *testing.T) {
	tests := []struct {
		name, method, path, host, origin, fetch, body string
		status                                        int
	}{
		{"same origin", "POST", "/activity/send", "localhost:9999", "http://localhost:9999", "", "message=hi", 204},
		{"foreign form", "POST", "/activity/send", "localhost:9999", "https://evil.example", "", "", 403},
		{"foreign metadata", "POST", "/waitlist/save", "localhost:9999", "", "cross-site", "", 403},
		{"rebind host", "GET", "/", "evil.example:9999", "", "", "", 421},
		{"different scheme", "POST", "/activity/send", "localhost:9999", "https://localhost:9999", "", "", 403},
		{"oversized form", "POST", "/waitlist", "localhost:9999", "", "", strings.Repeat("a", 16385), 413},
		{"oversized upload", "POST", "/upload", "localhost:9999", "", "", strings.Repeat("a", 2*1024*1024+64*1024+1), 413},
		{"arbitrary room", "GET", "/ws?rooms=secret", "localhost:9999", "", "", "", 400},
		{"additional room", "GET", "/ws?rooms=activity&rooms=secret", "localhost:9999", "", "", "", 400},
		{"public room", "GET", "/ws?rooms=activity", "localhost:9999", "", "", "", 204},
		{"foreign stream", "GET", "/sse?channels=metrics", "localhost:9999", "", "cross-site", "", 403},
		{"debug endpoint", "GET", "/__hyperchi/hot-reload", "localhost:9999", "", "", "", 404},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := newSitePolicy("127.0.0.1", "9999", "")
			if err != nil {
				t.Fatal(err)
			}
			h := p.middleware(docsApp(t, ""))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
			r := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
			r.Host = tt.host
			r.ContentLength = -1
			r.Header.Set("Origin", tt.origin)
			r.Header.Set("Sec-Fetch-Site", tt.fetch)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tt.status {
				t.Fatalf("status %d want %d: %s", w.Code, tt.status, w.Body.String())
			}
			if w.Header().Get("Content-Security-Policy") == "" {
				t.Fatal("missing CSP")
			}
		})
	}
}

// fakeMachine makes interfaceAddrs return a LAN, a tailnet, a loopback, and a
// link-local address for the length of the test.
func fakeMachine(t *testing.T) {
	t.Helper()
	prev := interfaceAddrs
	interfaceAddrs = func() ([]net.Addr, error) {
		var addrs []net.Addr
		for _, cidr := range []string{"192.168.4.59/24", "100.77.49.6/32", "127.0.0.1/8", "fe80::1/64"} {
			ip, ipNet, err := net.ParseCIDR(cidr)
			if err != nil {
				return nil, err
			}
			addrs = append(addrs, &net.IPNet{IP: ip, Mask: ipNet.Mask})
		}
		return addrs, nil
	}
	t.Cleanup(func() { interfaceAddrs = prev })
}

func TestPublicOriginMustBeHTTPS(t *testing.T) {
	for _, origin := range []string{"http://example.com", "https://example.com/path", "https://user@example.com"} {
		if _, err := newSitePolicy("0.0.0.0", "9999", origin); err == nil {
			t.Errorf("accepted %s", origin)
		}
	}
	if _, err := newSitePolicy("0.0.0.0", "9999", "https://example.com"); err != nil {
		t.Fatal(err)
	}
}

// A dev run bound to 0.0.0.0 answers on every address of the machine, so a
// client reaches it by LAN or tailnet IP. Production sets PUBLIC_ORIGIN and
// answers only that origin and loopback, whatever the machine's addresses.
func TestDevBindAnswersThisMachineOnly(t *testing.T) {
	fakeMachine(t)
	dev, err := newSitePolicy("0.0.0.0", "9999", "")
	if err != nil {
		t.Fatal(err)
	}
	prod, err := newSitePolicy("0.0.0.0", "9999", "https://example.com")
	if err != nil {
		t.Fatal(err)
	}
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	devHandler := dev.middleware(docsApp(t, ""))(ok)
	prodHandler := prod.middleware(docsApp(t, ""))(ok)
	for _, tc := range []struct {
		host          string
		dev, prodCode int
	}{
		{"192.168.4.59:9999", 204, 421},
		{"100.77.49.6:9999", 204, 421},
		{"localhost:9999", 204, 204},
		{"example.com", 421, 204},
		{"evil.example:9999", 421, 421},
		{"[fe80::1]:9999", 421, 421},
	} {
		for _, run := range []struct {
			name string
			h    http.Handler
			want int
		}{{"dev", devHandler, tc.dev}, {"prod", prodHandler, tc.prodCode}} {
			r := httptest.NewRequest("GET", "/", nil)
			r.Host = tc.host
			w := httptest.NewRecorder()
			run.h.ServeHTTP(w, r)
			if w.Code != run.want {
				t.Errorf("%s Host %s: got %d want %d", run.name, tc.host, w.Code, run.want)
			}
		}
	}

	p, err := newSitePolicy("192.168.4.59", "9999", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Origins(); !slices.Contains(got, "http://192.168.4.59:9999") || slices.Contains(got, "http://100.77.49.6:9999") {
		t.Errorf("a specific bind answers that address only: %v", got)
	}
}

func TestRateLimitsIgnoreForwardingHeaders(t *testing.T) {
	p, _ := newSitePolicy("127.0.0.1", "9999", "")
	h := p.middleware(docsApp(t, ""))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	for i := 0; i < 31; i++ {
		r := httptest.NewRequest("POST", "http://localhost:9999/activity/send", strings.NewReader("message=hi"))
		r.Header.Set("X-Forwarded-For", strings.Repeat("1", i+1))
		r.Header.Set("X-Real-IP", strings.Repeat("2", i+1))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		want := 204
		if i == 30 {
			want = 429
		}
		if w.Code != want {
			t.Fatalf("request %d status %d want %d", i, w.Code, want)
		}
	}
	if !p.allow("192.0.2.1", true, time.Now().Add(2*time.Minute)) {
		t.Fatal("rate window did not reset")
	}
}

func TestRealtimeAndUploadBudgets(t *testing.T) {
	p, _ := newSitePolicy("127.0.0.1", "9999", "")
	for i := 0; i < 6; i++ {
		if !p.acquire("peer", true) {
			t.Fatal("early rejection")
		}
	}
	if p.acquire("peer", true) {
		t.Fatal("connection budget exceeded")
	}
	p.release("peer", true)
	if !p.acquire("peer", true) {
		t.Fatal("slot not released")
	}
	for i := 0; i < 4; i++ {
		if !p.acquire("peer", false) {
			t.Fatal("early upload rejection")
		}
	}
	if p.acquire("peer", false) {
		t.Fatal("upload budget exceeded")
	}
}

func TestStaticAssetsCannotListOrEscape(t *testing.T) {
	app := docsApp(t, testOrigin)
	for _, path := range []string{"/static/", "/static/js/", "/static/.env", "/static/../main.go", "/static/js/../../go.mod"} {
		w := httptest.NewRecorder()
		app.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 404 {
			t.Errorf("%s: status %d", path, w.Code)
		}
	}
	w := httptest.NewRecorder()
	app.ServeHTTP(w, httptest.NewRequest("GET", "/static/js/site.js", nil))
	if w.Code != 200 {
		t.Errorf("public asset status %d", w.Code)
	}
}

// Pages link static files through the asset function, and the versioned URL
// is served with a year-long immutable Cache-Control.
func TestStaticAssetsAreVersioned(t *testing.T) {
	app := docsApp(t, testOrigin)
	url, err := app.AssetURL("/static/css/showcase.css")
	if err != nil {
		t.Fatal(err)
	}
	if page := get(t, app, "/", false).Body.String(); !strings.Contains(page, `href="`+url+`"`) {
		t.Fatalf("home page does not link %s", url)
	}
	w := get(t, app, url, false)
	if w.Code != 200 || w.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatalf("versioned asset: status %d, Cache-Control %q", w.Code, w.Header().Get("Cache-Control"))
	}
}

func TestTailscalePreviewOrigin(t *testing.T) {
	p, err := newSitePolicyWithPreview("0.0.0.0", "9999", "", "http://100.64.0.10:9999")
	if err != nil {
		t.Fatal(err)
	}
	h := p.middleware(docsApp(t, ""))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	for _, origin := range []string{"http://100.64.0.10:9999", "http://evil.example:9999"} {
		r := httptest.NewRequest("POST", "http://100.64.0.10:9999/activity/send", nil)
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		want := 204
		if strings.Contains(origin, "evil") {
			want = 403
		}
		if w.Code != want {
			t.Fatalf("origin %s: got %d want %d", origin, w.Code, want)
		}
	}
	if _, err := newSitePolicyWithPreview("0.0.0.0", "9999", "", "http://example.com:9999"); err == nil {
		t.Fatal("accepted non-tailnet preview")
	}
}

// Origins is what main logs as the addresses to open, so it must list the
// machine's addresses with the loopback ones and nothing the policy refuses.
func TestPolicyOriginsListWhatItAnswers(t *testing.T) {
	fakeMachine(t)
	p, err := newSitePolicy("0.0.0.0", "9999", "")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"http://100.77.49.6:9999", "http://127.0.0.1:9999", "http://192.168.4.59:9999", "http://[::1]:9999", "http://localhost:9999"}
	if got := p.Origins(); !slices.Equal(got, want) {
		t.Fatalf("Origins() = %v, want %v", got, want)
	}
}

// Policy refusals render through the app's error pipeline: no-store, the
// policy's own message, and JSON for an API client.
func TestPolicyDenialsUseTheErrorPipeline(t *testing.T) {
	p, err := newSitePolicy("127.0.0.1", "9999", "")
	if err != nil {
		t.Fatal(err)
	}
	h := p.middleware(docsApp(t, ""))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Host = "evil.example:9999"
	r.Header.Set("Accept", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusMisdirectedRequest {
		t.Fatalf("status %d, want 421", w.Code)
	}
	if cc := w.Header().Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
	if body := w.Body.String(); !strings.Contains(body, "Unrecognized host") || !strings.HasPrefix(strings.TrimSpace(body), "{") {
		t.Errorf("body = %q, want a JSON error with the policy message", body)
	}
}

// Browsers get errors as a page in the site's design; htmx gets a fragment,
// never a whole document swapped into its target.
func TestErrorsRenderInTheSiteDesign(t *testing.T) {
	app := docsApp(t, "")
	get := func(path string, htmx bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.Header.Set("Accept", "text/html")
		if htmx {
			r.Header.Set("HX-Request", "true")
		}
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		return w
	}
	page := get("/no-such-page", false)
	if page.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", page.Code)
	}
	for _, want := range []string{"Nothing lives here.", `id="main"`, `name="robots" content="noindex"`, "/static/css/showcase.css?v="} {
		if !strings.Contains(page.Body.String(), want) {
			t.Errorf("404 page lacks %q", want)
		}
	}
	if frag := get("/no-such-page", true); frag.Code != http.StatusNotFound || strings.Contains(frag.Body.String(), "<html") {
		t.Errorf("htmx 404: status %d, body %q; want a fragment", frag.Code, frag.Body.String())
	}

	p, err := newSitePolicy("127.0.0.1", "9999", "")
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Host = "evil.example:9999"
	w := httptest.NewRecorder()
	p.middleware(app)(app).ServeHTTP(w, r)
	if w.Code != http.StatusMisdirectedRequest || !strings.Contains(w.Body.String(), "Unrecognized host") || !strings.Contains(w.Body.String(), `id="main"`) {
		t.Errorf("policy denial: status %d, body lacks the site page: %.200s", w.Code, w.Body.String())
	}
}
