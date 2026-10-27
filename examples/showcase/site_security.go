package main

import (
	"bytes"
	"embed"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/regiellis/hyperchi/hyperchi"
)

// Embedding prevents runtime directory listings and symlink escapes into the
// server's filesystem. Only checked-in public assets belong under static/.
// setupRoutes serves it with app.StaticFS, which never lists a directory or
// serves a dotfile, and versions its URLs for the asset template function.
//
//go:embed static
var publicAssets embed.FS

type requestWindow struct {
	until         time.Time
	reads, writes int
}

type sitePolicy struct {
	origins              map[string]string // exact Host -> canonical browser origin
	mu                   sync.Mutex
	peers                map[string]*requestWindow
	global               requestWindow
	active               map[string]int
	connections, uploads int
}

// Origins returns every origin the policy answers, sorted. A bind to 0.0.0.0
// listens on each interface, but only these hosts get past the Host check.
func (p *sitePolicy) Origins() []string {
	origins := make([]string, 0, len(p.origins))
	for _, o := range p.origins {
		origins = append(origins, o)
	}
	slices.Sort(origins)
	return origins
}

// interfaceAddrs lists this machine's addresses; tests replace it.
var interfaceAddrs = net.InterfaceAddrs

// machineHosts returns bind itself, or for an unspecified bind (0.0.0.0, ::)
// every interface address except link-local ones, which a browser cannot use
// without a zone.
func machineHosts(bind net.IP) ([]string, error) {
	if !bind.IsUnspecified() {
		return []string{bind.String()}, nil
	}
	addrs, err := interfaceAddrs()
	if err != nil {
		return nil, fmt.Errorf("list this machine's addresses: %w", err)
	}
	var hosts []string
	for _, a := range addrs {
		ipNet, ok := a.(*net.IPNet)
		if !ok || ipNet.IP.IsLoopback() || ipNet.IP.IsLinkLocalUnicast() {
			continue
		}
		hosts = append(hosts, ipNet.IP.String())
	}
	return hosts, nil
}

func newSitePolicy(host, port, publicOrigin string) (*sitePolicy, error) {
	return newSitePolicyWithPreview(host, port, publicOrigin, "")
}

func newSitePolicyWithPreview(host, port, publicOrigin, previewOrigin string) (*sitePolicy, error) {
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return nil, fmt.Errorf("PORT must be 1 through 65535")
	}
	p := &sitePolicy{origins: make(map[string]string), peers: make(map[string]*requestWindow), active: make(map[string]int)}
	ip := net.ParseIP(host)
	if host != "localhost" && ip == nil {
		return nil, fmt.Errorf("HOST must be localhost or an IP address")
	}
	for _, local := range []string{"localhost", "127.0.0.1", "::1"} {
		authority := net.JoinHostPort(local, port)
		p.origins[authority] = "http://" + authority
	}
	// Without PUBLIC_ORIGIN this is a dev run: answer plain HTTP on the address
	// it binds, or on every address of this machine for 0.0.0.0, so a client on
	// the LAN or the tailnet can open it. Production sets PUBLIC_ORIGIN and
	// answers only that origin and loopback.
	if publicOrigin == "" && host != "localhost" && !ip.IsLoopback() {
		hosts, err := machineHosts(ip)
		if err != nil {
			return nil, err
		}
		for _, h := range hosts {
			authority := net.JoinHostPort(h, port)
			p.origins[authority] = "http://" + authority
		}
	}
	if publicOrigin != "" {
		u, err := url.Parse(publicOrigin)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
			return nil, fmt.Errorf("PUBLIC_ORIGIN must be an HTTPS origin without a path, e.g. https://hyperchi.example")
		}
		p.origins[u.Host] = publicOrigin
	}
	if previewOrigin != "" {
		u, err := url.Parse(previewOrigin)
		if err != nil || u.Scheme != "http" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Port() != port {
			return nil, fmt.Errorf("PREVIEW_ORIGIN must be an HTTP Tailscale origin with the server port")
		}
		_, tailnet, _ := net.ParseCIDR("100.64.0.0/10")
		if !tailnet.Contains(net.ParseIP(u.Hostname())) && !strings.HasSuffix(u.Hostname(), ".ts.net") {
			return nil, fmt.Errorf("PREVIEW_ORIGIN must name a Tailscale IP or ts.net hostname")
		}
		p.origins[u.Host] = previewOrigin
	}
	return p, nil
}

// middleware enforces the policy in front of app. Every refusal renders
// through app's error pipeline, like the framework's own denials.
func (p *sitePolicy) middleware(app *hyperchi.HyperChi) func(http.Handler) http.Handler {
	csrf := http.NewCrossOriginProtection()
	deny := func(w http.ResponseWriter, r *http.Request, code int, msg string) {
		app.RespondStatus(w, r, code, hyperchi.NewHTTPError(code, msg))
	}
	return func(next http.Handler) http.Handler {
		return p.handler(next, csrf, deny)
	}
}

func (p *sitePolicy) handler(next http.Handler, csrf *http.CrossOriginProtection, deny func(http.ResponseWriter, *http.Request, int, string)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Release", releaseVersion)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		// hc-* expressions require eval. Never render visitor input as executable
		// markup; inline scripts and third-party scripts remain prohibited.
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-eval'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		origin, allowed := p.origins[r.Host]
		if !allowed {
			deny(w, r, http.StatusMisdirectedRequest, "Unrecognized host")
			return
		}
		if strings.HasPrefix(origin, "https://") {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		}
		if len(r.URL.RawQuery) > 2048 {
			deny(w, r, http.StatusRequestURITooLong, "Query too large")
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" && r.Method != "POST" {
			deny(w, r, http.StatusMethodNotAllowed, "Method not allowed")
			return
		}
		// Check the scheme and port as well as the host, including realtime GETs.
		if o := r.Header.Get("Origin"); o != "" && o != origin {
			deny(w, r, http.StatusForbidden, "Cross-origin request denied")
			return
		}
		if err := csrf.Check(r); err != nil {
			deny(w, r, http.StatusForbidden, "Cross-origin request denied")
			return
		}
		realtime := r.URL.Path == "/sse" || r.URL.Path == "/ws"
		if realtime {
			if r.Method != "GET" {
				deny(w, r, http.StatusMethodNotAllowed, "Method not allowed")
				return
			}
			if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
				deny(w, r, http.StatusForbidden, "Cross-origin request denied")
				return
			}
			want := "channels=metrics"
			if r.URL.Path == "/ws" {
				want = "rooms=activity"
			}
			if r.URL.RawQuery != want {
				deny(w, r, http.StatusBadRequest, "Unknown realtime subscription")
				return
			}
		}
		// No debug metadata or development endpoints are part of the public site.
		if strings.HasPrefix(r.URL.Path, "/__hyperchi/") || r.URL.Path == "/htmx/extensions" {
			deny(w, r, http.StatusNotFound, "Not found")
			return
		}
		peer, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			peer = r.RemoteAddr
		}
		write := r.Method == "POST"
		if !strings.HasPrefix(r.URL.Path, "/static/") && !strings.HasPrefix(r.URL.Path, "/hyperchi/js/") {
			if !p.allow(peer, write, time.Now()) {
				w.Header().Set("Retry-After", "60")
				deny(w, r, http.StatusTooManyRequests, "Too many requests; try again shortly")
				return
			}
		}
		upload := r.URL.Path == "/upload" && write
		if realtime || upload {
			if !p.acquire(peer, realtime) {
				w.Header().Set("Retry-After", "10")
				deny(w, r, http.StatusTooManyRequests, "Demo is busy; try again shortly")
				return
			}
			defer p.release(peer, realtime)
		}
		if write {
			limit := int64(16 * 1024)
			if upload {
				limit = 2*1024*1024 + 64*1024
			}
			// Read once within a strict budget, including chunked bodies. Downstream
			// pattern handlers cannot ignore a MaxBytesReader error and act on partial data.
			r.Body = http.MaxBytesReader(w, r.Body, limit)
			body, err := io.ReadAll(r.Body)
			_ = r.Body.Close()
			if err != nil {
				deny(w, r, http.StatusRequestEntityTooLarge, "Request body invalid or too large")
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
		}
		next.ServeHTTP(w, r)
	})
}

// Fixed windows keep both memory and demo work bounded. RemoteAddr is the TCP
// peer: forwarding headers are deliberately ignored. Behind a proxy these are
// conservative shared limits; enforce per-visitor limits at that trusted proxy.
func (p *sitePolicy) allow(peer string, write bool, now time.Time) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !now.Before(p.global.until) {
		p.global = requestWindow{until: now.Add(time.Minute)}
	}
	entry := p.peers[peer]
	if entry == nil {
		if len(p.peers) >= 2048 {
			for key, old := range p.peers {
				if !now.Before(old.until) {
					delete(p.peers, key)
				}
			}
			if len(p.peers) >= 2048 {
				return false
			}
		}
		entry = &requestWindow{}
		p.peers[peer] = entry
	}
	if !now.Before(entry.until) {
		*entry = requestWindow{until: now.Add(time.Minute)}
	}
	if entry.reads >= 300 || p.global.reads >= 3000 || (write && (entry.writes >= 30 || p.global.writes >= 120)) {
		return false
	}
	entry.reads++
	p.global.reads++
	if write {
		entry.writes++
		p.global.writes++
	}
	return true
}

func (p *sitePolicy) acquire(peer string, realtime bool) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if realtime {
		if p.connections >= 64 || p.active[peer] >= 6 {
			return false
		}
		p.connections++
		p.active[peer]++
	} else {
		if p.uploads >= 4 {
			return false
		}
		p.uploads++
	}
	return true
}

func (p *sitePolicy) release(peer string, realtime bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if realtime {
		p.connections--
		p.active[peer]--
		if p.active[peer] == 0 {
			delete(p.active, peer)
		}
	} else {
		p.uploads--
	}
}
