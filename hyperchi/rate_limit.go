package hyperchi

import (
	"fmt"
	"math"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"
)

// RateLimitConfig configures a bounded, process-local token bucket per key.
// Without KeyGenerator or TrustedProxies, only the transport peer is used.
type RateLimitConfig struct {
	// RequestsPerWindow per WindowDuration is the sustained rate (default 100
	// a minute).
	RequestsPerWindow int
	WindowDuration    time.Duration
	// KeyGenerator replaces the client IP as the bucket key.
	KeyGenerator func(*http.Request) string
	// ErrorHandler answers a denial; the default is a plain-text 429.
	ErrorHandler func(http.ResponseWriter, *http.Request, time.Duration)
	// IncludeHeaders sends X-RateLimit-Limit and X-RateLimit-Remaining.
	IncludeHeaders bool
	// SkipPaths (exact paths) and SkipMethods are never limited.
	SkipPaths   []string
	SkipMethods []string
	// TrustedProxies are CIDRs whose X-Forwarded-For entries are believed.
	TrustedProxies []string
	// Burst is the bucket size (default RequestsPerWindow).
	Burst int
	// RefillInterval is the time per token (default
	// WindowDuration/RequestsPerWindow).
	RefillInterval time.Duration
	// MaxKeys caps the tracked clients (default 10000). When the table is full
	// and no key is idle, new clients are denied.
	MaxKeys int
}

// ClientInfo is unused here. It remains so code written against the
// middleware package's limiter still compiles.
type ClientInfo struct {
	Count                 int
	WindowStart, LastSeen time.Time
}

type rateBucket struct {
	tokens  float64
	updated time.Time
}

// RateLimiter is a token-bucket limiter keyed by client. It is safe for
// concurrent use and starts no goroutines: idle keys are reclaimed when the
// key table fills.
type RateLimiter struct {
	config  RateLimitConfig
	mu      sync.Mutex
	buckets map[string]rateBucket
	proxies []netip.Prefix
	now     func() time.Time
}

// DefaultRateLimitConfig returns 100 requests a minute per client, with rate
// headers and up to 10000 tracked clients.
func DefaultRateLimitConfig() *RateLimitConfig {
	return &RateLimitConfig{RequestsPerWindow: 100, WindowDuration: time.Minute, IncludeHeaders: true, MaxKeys: 10000}
}

// NewRateLimiterE returns a limiter for a copy of cfg (DefaultRateLimitConfig
// when nil), filling zero fields with their defaults. Negative values and
// malformed proxy CIDRs are errors.
func NewRateLimiterE(cfg *RateLimitConfig) (*RateLimiter, error) {
	if cfg == nil {
		cfg = DefaultRateLimitConfig()
	}
	c := *cfg
	if c.RequestsPerWindow == 0 {
		c.RequestsPerWindow = 100
	}
	if c.WindowDuration == 0 {
		c.WindowDuration = time.Minute
	}
	if c.Burst == 0 {
		c.Burst = c.RequestsPerWindow
	}
	if c.MaxKeys == 0 {
		c.MaxKeys = 10000
	}
	if c.RequestsPerWindow < 0 || c.WindowDuration < 0 || c.Burst < 1 || c.MaxKeys < 1 || c.RefillInterval < 0 {
		return nil, fmt.Errorf("invalid rate limit configuration")
	}
	if c.RefillInterval == 0 {
		c.RefillInterval = c.WindowDuration / time.Duration(c.RequestsPerWindow)
	}
	if c.RefillInterval <= 0 {
		return nil, fmt.Errorf("rate limit refill interval is too small")
	}
	c.SkipPaths = append([]string(nil), c.SkipPaths...)
	c.SkipMethods = append([]string(nil), c.SkipMethods...)
	c.TrustedProxies = append([]string(nil), c.TrustedProxies...)
	l := &RateLimiter{config: c, buckets: make(map[string]rateBucket), now: time.Now}
	for _, cidr := range c.TrustedProxies {
		p, err := netip.ParsePrefix(strings.TrimSpace(cidr))
		if err != nil {
			return nil, fmt.Errorf("trusted proxy %q: %w", cidr, err)
		}
		l.proxies = append(l.proxies, p)
	}
	return l, nil
}

// NewRateLimiter panics on invalid options; use NewRateLimiterE for configuration input.
func NewRateLimiter(cfg *RateLimitConfig) *RateLimiter {
	l, err := NewRateLimiterE(cfg)
	if err != nil {
		panic(err)
	}
	return l
}

func (l *RateLimiter) trusted(ip netip.Addr) bool {
	for _, prefix := range l.proxies {
		if prefix.Contains(ip) {
			return true
		}
	}
	return false
}

func (l *RateLimiter) key(r *http.Request) string {
	if l.config.KeyGenerator != nil {
		return l.config.KeyGenerator(r)
	}
	peer := r.RemoteAddr
	if host, _, err := net.SplitHostPort(peer); err == nil {
		peer = host
	}
	ip, err := netip.ParseAddr(peer)
	if err != nil || !l.trusted(ip.WithZone("").Unmap()) {
		return peer
	}
	// Walk from the immediate peer toward the client. The leftmost entries are
	// client-supplied, so the first untrusted address from the right wins.
	chain := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
	for i := len(chain) - 1; i >= 0; i-- {
		candidate, err := netip.ParseAddr(strings.TrimSpace(chain[i]))
		if err != nil {
			return peer
		}
		candidate = candidate.WithZone("").Unmap()
		if !l.trusted(candidate) {
			return candidate.String()
		}
	}
	return peer
}

func (l *RateLimiter) check(r *http.Request) (bool, time.Duration, int) {
	key, now := l.key(r), l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	b, exists := l.buckets[key]
	if !exists {
		if len(l.buckets) >= l.config.MaxKeys {
			fullAfter := float64(l.config.Burst) * float64(l.config.RefillInterval)
			for k, v := range l.buckets {
				if float64(now.Sub(v.updated)) >= fullAfter {
					delete(l.buckets, k)
				}
			}
			if len(l.buckets) >= l.config.MaxKeys {
				return false, l.config.RefillInterval, 0
			}
		}
		b = rateBucket{tokens: float64(l.config.Burst), updated: now}
	}
	elapsed := now.Sub(b.updated)
	if elapsed > 0 {
		b.tokens = math.Min(float64(l.config.Burst), b.tokens+float64(elapsed)/float64(l.config.RefillInterval))
		b.updated = now
	}
	if b.tokens < 1 {
		l.buckets[key] = b
		retry := time.Duration(math.Ceil((1 - b.tokens) * float64(l.config.RefillInterval)))
		return false, retry, 0
	}
	b.tokens--
	l.buckets[key] = b
	return true, 0, int(b.tokens)
}

// Allow takes a token for r's client. When none is left it returns false and
// the wait until the next one.
func (l *RateLimiter) Allow(r *http.Request) (bool, time.Duration) {
	ok, retry, _ := l.check(r)
	return ok, retry
}

// Stop does nothing: the limiter has no goroutine. It remains for callers
// written for limiters that had one.
func (l *RateLimiter) Stop() {}

// Middleware limits each request, sending Retry-After and Cache-Control:
// no-store with a denial.
func (l *RateLimiter) Middleware() func(http.Handler) http.Handler {
	return l.middleware(l.config.ErrorHandler)
}

func (l *RateLimiter) middleware(onDeny func(http.ResponseWriter, *http.Request, time.Duration)) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			for _, p := range l.config.SkipPaths {
				if r.URL.Path == p {
					next.ServeHTTP(w, r)
					return
				}
			}
			for _, m := range l.config.SkipMethods {
				if strings.EqualFold(r.Method, m) {
					next.ServeHTTP(w, r)
					return
				}
			}
			ok, retry, remaining := l.check(r)
			if l.config.IncludeHeaders {
				w.Header().Set("X-RateLimit-Limit", strconv.Itoa(l.config.Burst))
				w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
			}
			if !ok {
				w.Header().Set("Retry-After", strconv.Itoa(max(1, int(math.Ceil(retry.Seconds())))))
				w.Header().Set("Cache-Control", "no-store")
				if onDeny != nil {
					onDeny(w, r, retry)
				} else {
					http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
				}
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RateLimit allows each client n requests per window, keyed by the TCP peer.
// Behind a proxy use RateLimitWithConfig with TrustedProxies, or
// app.Middleware.RateLimit, which reads them from the configuration. It
// panics on invalid values.
func RateLimit(n int, window time.Duration) func(http.Handler) http.Handler {
	c := DefaultRateLimitConfig()
	c.RequestsPerWindow = n
	c.WindowDuration = window
	return RateLimitWithConfig(c)
}

// RateLimitWithConfig is NewRateLimiter(c).Middleware(). It panics on an
// invalid configuration.
func RateLimitWithConfig(c *RateLimitConfig) func(http.Handler) http.Handler {
	return NewRateLimiter(c).Middleware()
}

// EndpointRateLimiter applies a separate limiter to each registered path.
// Requests to other paths pass through. It is safe for concurrent use.
type EndpointRateLimiter struct {
	mu       sync.RWMutex
	limiters map[string]*RateLimiter
}

// NewEndpointRateLimiter returns a limiter with no paths.
func NewEndpointRateLimiter() *EndpointRateLimiter {
	return &EndpointRateLimiter{limiters: make(map[string]*RateLimiter)}
}

// AddEndpoint limits requests whose path is exactly path, replacing any limiter
// already there. It panics on an invalid configuration.
func (e *EndpointRateLimiter) AddEndpoint(path string, c *RateLimitConfig) {
	l := NewRateLimiter(c)
	e.mu.Lock()
	defer e.mu.Unlock()
	e.limiters[path] = l
}

// Stop does nothing; see RateLimiter.Stop.
func (e *EndpointRateLimiter) Stop() {}

// Middleware routes each request to its path's limiter, if any.
func (e *EndpointRateLimiter) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			e.mu.RLock()
			l := e.limiters[r.URL.Path]
			e.mu.RUnlock()
			if l == nil {
				next.ServeHTTP(w, r)
				return
			}
			l.Middleware()(next).ServeHTTP(w, r)
		})
	}
}
