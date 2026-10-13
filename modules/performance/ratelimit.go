package performance

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/regiellis/hyperchi/hyperchi"
	"github.com/regiellis/hyperchi/hyperchi/security"
)

// RateLimiter implements rate limiting using KV store backends.
// Built-in advanced stores update fixed/sliding window counters atomically across
// limiter instances. Custom TTL stores should implement hyperchi.AtomicTTLStore;
// the fallback protects only calls through this limiter instance. Token buckets
// must use one limiter instance per key prefix and backend.
type RateLimiter struct {
	store     hyperchi.KVStore
	keyPrefix string
	algorithm RateLimitAlgorithm
	userID    func(*http.Request) string
	app       *hyperchi.HyperChi // answers refusals when set
	now       func() time.Time   // time.Now; tests replace it
	counterMu sync.Mutex         // custom-store counter fallback
	bucketMu  sync.Mutex         // token bucket updates within this limiter
}

// RateLimitConfig configures a RateLimiter. The rules go to
// RateLimitMiddleware, or to Check one at a time.
type RateLimitConfig struct {
	Backend   string             `json:"backend"`    // name of a store in the KV registry
	KeyPrefix string             `json:"key_prefix"` // default "ratelimit:"
	Algorithm RateLimitAlgorithm `json:"algorithm"`

	// UserID names the user a PerUser rule counts against. The default reads
	// the verified user ID that security.RequireAuth or security.WithUserID
	// stored in the request context, so the authentication middleware must
	// run before the limiter.
	UserID func(*http.Request) string `json:"-"`

	// App, when set, answers refusals (429, and 500 for a store failure or
	// invalid rules) through app.RespondStatus, so they render like every
	// other error: OnError, or DefaultError's JSON, htmx fragment, or page.
	// Install sets it. Without an app, the middleware answers with plain
	// text.
	App *hyperchi.HyperChi `json:"-"`
}

// RateLimitRule limits the requests that match Path and Method.
type RateLimitRule struct {
	Path        string        `json:"path"`         // exact path, "*", or a prefix ending in "*"
	Method      string        `json:"method"`       // empty matches every method
	Rate        string        `json:"rate"`         // "requests/period", such as "100/minute"; see ParseRateString
	PerIP       bool          `json:"per_ip"`       // separate counter per client IP
	PerUser     bool          `json:"per_user"`     // separate counter per user (anonymous requests count per client IP)
	Burst       int           `json:"burst"`        // token bucket capacity; 0 uses MaxRequests
	WindowSize  time.Duration `json:"window_size"`  // set from Rate when MaxRequests is 0
	MaxRequests int           `json:"max_requests"` // set from Rate when 0
}

// RateLimitAlgorithm selects how a RateLimiter counts requests.
type RateLimitAlgorithm int

// Rate limiting algorithms. SlidingWindow counts in one-minute buckets, so it
// is approximate below a minute.
const (
	FixedWindow RateLimitAlgorithm = iota
	SlidingWindow
	TokenBucket
)

// RateLimitResult is the outcome of one Check. TotalHits and TotalRequests
// are the requests counted in the window. TokenBucket counts tokens, not
// requests, and leaves them 0.
type RateLimitResult struct {
	Allowed       bool          `json:"allowed"`
	Remaining     int64         `json:"remaining"`
	ResetTime     time.Time     `json:"reset_time"`
	RetryAfter    time.Duration `json:"retry_after,omitempty"`
	TotalHits     int64         `json:"total_hits"`
	TotalRequests int64         `json:"total_requests"`
}

// NewRateLimiter returns a limiter over the store named config.Backend, or an
// error if the registry has no such store.
func NewRateLimiter(registry *hyperchi.KVRegistry, config RateLimitConfig) (*RateLimiter, error) {
	store, err := registry.Get(config.Backend)
	if err != nil {
		return nil, fmt.Errorf("failed to get KV store '%s': %w", config.Backend, err)
	}

	if config.KeyPrefix == "" {
		config.KeyPrefix = "ratelimit:"
	}
	if config.UserID == nil {
		config.UserID = func(r *http.Request) string { return security.GetUserIDFromContext(r.Context()) }
	}

	return &RateLimiter{
		store:     store,
		keyPrefix: config.KeyPrefix,
		algorithm: config.Algorithm,
		userID:    config.UserID,
		app:       config.App,
		now:       time.Now,
	}, nil
}

// Check counts one request for identifier under rule and reports whether it
// is allowed. rule.MaxRequests and rule.WindowSize must already be set. An
// unknown algorithm falls back to FixedWindow.
func (rl *RateLimiter) Check(rule RateLimitRule, identifier string) (*RateLimitResult, error) {
	switch rl.algorithm {
	case FixedWindow:
		return rl.checkFixedWindow(rule, identifier)
	case SlidingWindow:
		return rl.checkSlidingWindow(rule, identifier)
	case TokenBucket:
		return rl.checkTokenBucket(rule, identifier)
	default:
		return rl.checkFixedWindow(rule, identifier)
	}
}

// checkFixedWindow counts requests in windows aligned to rule.WindowSize.
func (rl *RateLimiter) checkFixedWindow(rule RateLimitRule, identifier string) (*RateLimitResult, error) {
	now := rl.now()
	window := now.Truncate(rule.WindowSize)
	key := rl.windowKey(identifier, window, rule.WindowSize)

	count, err := rl.incrementCounter(key, rule.WindowSize+time.Minute)
	if err != nil {
		return nil, fmt.Errorf("failed to increment counter: %w", err)
	}

	allowed := count <= int64(rule.MaxRequests)
	remaining := max(int64(rule.MaxRequests)-count, 0)

	resetTime := window.Add(rule.WindowSize)
	retryAfter := max(resetTime.Sub(now), 0)

	return &RateLimitResult{
		Allowed:       allowed,
		Remaining:     remaining,
		ResetTime:     resetTime,
		RetryAfter:    retryAfter,
		TotalHits:     count,
		TotalRequests: count,
	}, nil
}

// checkSlidingWindow sums one-minute counters across the last
// rule.WindowSize, so it approximates a true sliding window.
func (rl *RateLimiter) checkSlidingWindow(rule RateLimitRule, identifier string) (*RateLimitResult, error) {
	now := rl.now()
	windowStart := now.Add(-rule.WindowSize)

	currentMinute := now.Truncate(time.Minute)

	var totalCount int64
	var currentMinuteCount int64

	for i := 0; i < int(rule.WindowSize.Minutes())+1; i++ {
		minute := currentMinute.Add(-time.Duration(i) * time.Minute)
		if minute.Before(windowStart) {
			break
		}

		key := rl.windowKey(identifier, minute, rule.WindowSize)
		count, err := rl.readCounter(key)
		if err != nil {
			return nil, err
		}

		totalCount += count
		if i == 0 {
			currentMinuteCount = count
		}
	}

	currentKey := rl.windowKey(identifier, currentMinute, rule.WindowSize)
	newCount, err := rl.incrementCounter(currentKey, rule.WindowSize+time.Minute)
	if err != nil {
		return nil, fmt.Errorf("failed to increment counter: %w", err)
	}

	// Swap the minute's pre-increment count for the incremented one.
	totalCount = totalCount - currentMinuteCount + newCount

	allowed := totalCount <= int64(rule.MaxRequests)
	remaining := max(int64(rule.MaxRequests)-totalCount, 0)

	resetTime := now.Add(rule.WindowSize)
	retryAfter := time.Duration(0)
	if !allowed {
		retryAfter = time.Minute
	}

	return &RateLimitResult{
		Allowed:       allowed,
		Remaining:     remaining,
		ResetTime:     resetTime,
		RetryAfter:    retryAfter,
		TotalHits:     totalCount,
		TotalRequests: totalCount,
	}, nil
}

// windowKey names the counter for identifier that starts at start and counts
// toward a window of size. The key ends in two Unix seconds, the start and
// the moment the counter stops mattering (start + size + one minute of
// grace), so CleanupExpired can delete stale counters on stores without TTLs
// whatever the window's size:
//
//	<prefix><identifier>:<start>:<expires>
func (rl *RateLimiter) windowKey(identifier string, start time.Time, size time.Duration) string {
	return fmt.Sprintf("%s%s:%d:%d", rl.keyPrefix, identifier, start.Unix(), start.Add(size+time.Minute).Unix())
}

// windowExpiry returns the expiry windowKey wrote into key, and false for a
// key that is not a window counter (a token bucket, or another layout).
func windowExpiry(key string) (time.Time, bool) {
	rest, expiresText, found := cutLast(key, ":")
	if !found {
		return time.Time{}, false
	}
	_, startText, found := cutLast(rest, ":")
	if !found {
		return time.Time{}, false
	}
	start, err := strconv.ParseInt(startText, 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	expires, err := strconv.ParseInt(expiresText, 10, 64)
	if err != nil || expires <= start {
		return time.Time{}, false
	}
	return time.Unix(expires, 0), true
}

// cutLast splits s around the last sep.
func cutLast(s, sep string) (before, after string, found bool) {
	i := strings.LastIndex(s, sep)
	if i < 0 {
		return s, "", false
	}
	return s[:i], s[i+len(sep):], true
}

// readCounter returns the window counter at key, or 0 when it is missing or
// expired.
func (rl *RateLimiter) readCounter(key string) (int64, error) {
	value, err := rl.store.Get(key)
	if errors.Is(err, hyperchi.ErrKeyNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("failed to get count: %w", err)
	}
	count, err := decodeCounter(value)
	if err != nil {
		return 0, fmt.Errorf("counter %q: %w", key, err)
	}
	return count, nil
}

// tokenBucket is the stored state of one token bucket.
type tokenBucket struct {
	Tokens  float64 `json:"tokens"`
	Updated int64   `json:"updated"` // Unix nanoseconds of the last refill
}

// checkTokenBucket refills rule.MaxRequests tokens per rule.WindowSize, up to
// rule.Burst (MaxRequests when Burst is 0), and spends one per allowed
// request. Fractional tokens are kept, so traffic faster than one token per
// refill interval still refills. The read-modify-write is guarded only by this
// limiter's mutex, which is why each key prefix and backend needs a single
// limiter.
func (rl *RateLimiter) checkTokenBucket(rule RateLimitRule, identifier string) (*RateLimitResult, error) {
	rl.bucketMu.Lock()
	defer rl.bucketMu.Unlock()
	now := rl.now()
	key := fmt.Sprintf("%s%s:bucket", rl.keyPrefix, identifier)

	burst := float64(rule.Burst)
	if rule.Burst <= 0 {
		burst = float64(rule.MaxRequests)
	}
	rate := float64(rule.MaxRequests) / rule.WindowSize.Seconds() // tokens per second
	if rate <= 0 || math.IsInf(rate, 0) || math.IsNaN(rate) {
		return nil, fmt.Errorf("token bucket needs MaxRequests > 0 and WindowSize > 0, got %d per %v", rule.MaxRequests, rule.WindowSize)
	}

	bucket := tokenBucket{Tokens: burst, Updated: now.UnixNano()} // a new bucket starts full
	data, err := rl.store.Get(key)
	switch {
	case err == nil:
		var stored tokenBucket
		if err := json.Unmarshal(data, &stored); err != nil {
			return nil, fmt.Errorf("token bucket %q: %w", key, err)
		}
		elapsed := now.Sub(time.Unix(0, stored.Updated)).Seconds()
		bucket.Tokens = min(burst, stored.Tokens+max(elapsed, 0)*rate)
	case !errors.Is(err, hyperchi.ErrKeyNotFound):
		return nil, fmt.Errorf("failed to get token bucket: %w", err)
	}

	allowed := bucket.Tokens >= 1
	if allowed {
		bucket.Tokens--
	}

	// The bucket is full again once this much time passes, and a missing
	// bucket starts full, so it may expire then.
	untilFull := time.Duration((burst - bucket.Tokens) / rate * float64(time.Second))
	encoded, err := json.Marshal(bucket)
	if err != nil {
		return nil, err
	}
	if ttlStore, ok := rl.store.(TTLStore); ok {
		err = ttlStore.SetTTL(key, encoded, untilFull+time.Second)
	} else {
		err = rl.store.Set(key, encoded)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to store token bucket: %w", err)
	}

	retryAfter := time.Duration(0)
	if !allowed {
		retryAfter = time.Duration((1 - bucket.Tokens) / rate * float64(time.Second))
	}

	return &RateLimitResult{
		Allowed:    allowed,
		Remaining:  int64(bucket.Tokens),
		ResetTime:  now.Add(untilFull),
		RetryAfter: retryAfter,
	}, nil
}

// ParseRateString parses "requests/period" into a count and a window. The
// period is second, minute, hour, or day (or s, sec, m, min, h, hr, d), or any
// time.ParseDuration string such as "30s".
func ParseRateString(rateStr string) (int, time.Duration, error) {
	parts := strings.Split(rateStr, "/")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid rate format: %s", rateStr)
	}

	requests, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, fmt.Errorf("invalid request count: %s", parts[0])
	}

	var duration time.Duration
	switch parts[1] {
	case "second", "sec", "s":
		duration = time.Second
	case "minute", "min", "m":
		duration = time.Minute
	case "hour", "hr", "h":
		duration = time.Hour
	case "day", "d":
		duration = 24 * time.Hour
	default:
		duration, err = time.ParseDuration(parts[1])
		if err != nil {
			return 0, 0, fmt.Errorf("invalid duration: %s", parts[1])
		}
	}

	return requests, duration, nil
}

// PrepareRateLimitRules returns rules with MaxRequests and WindowSize filled
// from Rate where MaxRequests is 0, or an error naming the first rule whose
// Rate does not parse or whose limit is not positive.
func PrepareRateLimitRules(rules []RateLimitRule) ([]RateLimitRule, error) {
	prepared := make([]RateLimitRule, len(rules))
	for i, rule := range rules {
		if rule.MaxRequests == 0 {
			maxRequests, windowSize, err := ParseRateString(rule.Rate)
			if err != nil {
				return nil, fmt.Errorf("rate limit rule %d (%s %s): %w", i, rule.Method, rule.Path, err)
			}
			rule.MaxRequests = maxRequests
			rule.WindowSize = windowSize
		}
		if rule.MaxRequests <= 0 || rule.WindowSize <= 0 {
			return nil, fmt.Errorf("rate limit rule %d (%s %s): requests and window must be positive", i, rule.Method, rule.Path)
		}
		prepared[i] = rule
	}
	return prepared, nil
}

// RateLimitMiddleware applies the first rule that matches each request and
// sets the X-RateLimit-* headers. A refused request gets 429 with Retry-After
// in whole seconds, rounded up. Invalid rules (see PrepareRateLimitRules)
// answer every request with 500, as does a store error. Validate rules first
// to fail at startup instead. Refusals go through the app's error pipeline
// when RateLimitConfig.App is set, and are plain text otherwise; the headers
// are set either way.
func (rl *RateLimiter) RateLimitMiddleware(rules []RateLimitRule) func(http.Handler) http.Handler {
	prepared, prepareErr := PrepareRateLimitRules(rules)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if prepareErr != nil {
				rl.refuse(w, r, hyperchi.NewHTTPError(http.StatusInternalServerError, "Invalid rate limit configuration").Wrap(prepareErr))
				return
			}

			var rule *RateLimitRule
			for i := range prepared {
				if rl.matchesRule(prepared[i], r) {
					rule = &prepared[i]
					break
				}
			}

			if rule == nil {
				next.ServeHTTP(w, r)
				return
			}

			result, err := rl.Check(*rule, rl.generateIdentifier(*rule, r))
			if err != nil {
				rl.refuse(w, r, hyperchi.NewHTTPError(http.StatusInternalServerError, "Rate limit check failed").Wrap(err))
				return
			}

			w.Header().Set("X-RateLimit-Limit", strconv.Itoa(rule.MaxRequests))
			w.Header().Set("X-RateLimit-Remaining", strconv.FormatInt(result.Remaining, 10))
			w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(result.ResetTime.Unix(), 10))

			if !result.Allowed {
				w.Header().Set("Retry-After", strconv.FormatInt(int64(math.Ceil(result.RetryAfter.Seconds())), 10))
				rl.refuse(w, r, hyperchi.NewHTTPError(http.StatusTooManyRequests, "Rate limit exceeded"))
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// refuse answers r with he: through the app's error pipeline when the limiter
// has an app, else as plain text, which keeps the cause out of the response.
func (rl *RateLimiter) refuse(w http.ResponseWriter, r *http.Request, he *hyperchi.HTTPError) {
	if rl.app != nil {
		rl.app.RespondStatus(w, r, he.Code, he)
		return
	}
	http.Error(w, he.Message, he.Code)
}

// matchesRule reports whether r matches the rule's method and path. A path
// containing "*" matches every path that starts with the text before it.
func (rl *RateLimiter) matchesRule(rule RateLimitRule, r *http.Request) bool {
	if rule.Method != "" && rule.Method != r.Method {
		return false
	}

	if rule.Path != "" {
		if rule.Path == "*" {
			return true
		}

		if strings.Contains(rule.Path, "*") {
			prefix := strings.Split(rule.Path, "*")[0]
			return strings.HasPrefix(r.URL.Path, prefix)
		}

		return r.URL.Path == rule.Path
	}

	return true
}

// generateIdentifier builds the counter key for r from its path and, as the
// rule asks, its method, client IP, and user. A PerUser rule counts an
// anonymous request against its client IP.
func (rl *RateLimiter) generateIdentifier(rule RateLimitRule, r *http.Request) string {
	var parts []string

	parts = append(parts, r.URL.Path)

	if rule.Method != "" {
		parts = append(parts, r.Method)
	}

	if rule.PerIP {
		parts = append(parts, rl.getClientIP(r))
	}

	if rule.PerUser {
		if id := rl.userID(r); id != "" {
			parts = append(parts, "user", id)
		} else {
			parts = append(parts, "anon", rl.getClientIP(r))
		}
	}

	return strings.Join(parts, ":")
}

// getClientIP returns the client IP a chi ClientIPFrom* middleware stored for
// the request, or else the TCP peer. It never reads X-Forwarded-For or
// X-Real-IP: a client sets those freely, so trusting them would hand every
// request a fresh rate-limit bucket. Behind a proxy, configure
// Security.TrustedProxies and install app.Middleware.ClientIP() ahead of it.
func (rl *RateLimiter) getClientIP(r *http.Request) string {
	if ip := middleware.GetClientIP(r.Context()); ip != "" {
		return ip
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// windowKeys returns the unexpired window counter keys stored for
// identifier: its prefix followed by exactly the two segments windowKey
// writes. Token buckets, and longer identifiers sharing the prefix, are
// skipped.
func (rl *RateLimiter) windowKeys(identifier string) ([]string, error) {
	prefix := rl.keyPrefix + identifier + ":"
	keys, err := rl.store.Keys(prefix)
	if err != nil {
		return nil, err
	}
	now := rl.now()
	windows := keys[:0]
	for _, key := range keys {
		if strings.Count(strings.TrimPrefix(key, prefix), ":") != 1 {
			continue
		}
		if expires, ok := windowExpiry(key); ok && now.Before(expires) {
			windows = append(windows, key)
		}
	}
	return windows, nil
}

// GetStats sums the unexpired window counters stored under identifier. Token
// bucket state is not counted.
func (rl *RateLimiter) GetStats(identifier string) (*RateLimitStats, error) {
	keys, err := rl.windowKeys(identifier)
	if err != nil {
		return nil, err
	}

	stats := &RateLimitStats{Identifier: identifier}
	for _, key := range keys {
		value, err := rl.store.Get(key)
		if errors.Is(err, hyperchi.ErrKeyNotFound) {
			continue // expired since Keys listed it
		}
		if err != nil {
			return nil, err
		}
		count, err := decodeCounter(value)
		if err != nil {
			return nil, fmt.Errorf("counter %q: %w", key, err)
		}
		stats.TotalRequests += count
		stats.ActiveWindows++
	}
	return stats, nil
}

// RateLimitStats is what GetStats reports for one identifier.
type RateLimitStats struct {
	Identifier    string `json:"identifier"`
	TotalRequests int64  `json:"total_requests"`
	ActiveWindows int    `json:"active_windows"`
}

// CleanupExpired deletes the window counters whose window has ended, read
// from the expiry each counter's key carries, so a two-hour window keeps its
// count for two hours. Stores that support TTLs expire their own keys, so it
// does nothing for them. Every expired key is attempted, and the delete
// errors are joined.
func (rl *RateLimiter) CleanupExpired() error {
	if _, ok := rl.store.(TTLStore); ok {
		return nil
	}

	keys, err := rl.store.Keys(rl.keyPrefix)
	if err != nil {
		return err
	}

	now := rl.now()
	var errs []error
	for _, key := range keys {
		expires, ok := windowExpiry(key)
		if !ok || now.Before(expires) {
			continue
		}
		if err := rl.store.Delete(key); err != nil {
			errs = append(errs, fmt.Errorf("delete %q: %w", key, err))
		}
	}

	return errors.Join(errs...)
}

// TTLStore is a KV store that can expire keys. The rate limiter and job queue
// use SetTTL when their store has it. hyperchi.AdvancedKVStore includes it.
type TTLStore interface {
	hyperchi.KVStore
	SetTTL(key string, value []byte, ttl time.Duration) error
	GetTTL(key string) (time.Duration, error)
}

// incrementCounter increments key and sets its TTL. On a hyperchi.AtomicTTLStore
// both happen in one store operation; otherwise counterMu makes them atomic
// only for callers of this limiter.
func (rl *RateLimiter) incrementCounter(key string, ttl time.Duration) (int64, error) {
	if store, ok := rl.store.(hyperchi.AtomicTTLStore); ok {
		return store.IncrementTTL(key, ttl)
	}
	rl.counterMu.Lock()
	defer rl.counterMu.Unlock()
	count, err := rl.store.Increment(key)
	if err != nil {
		return 0, err
	}
	if store, ok := rl.store.(TTLStore); ok {
		if err := store.SetTTL(key, []byte(strconv.FormatInt(count, 10)), ttl); err != nil {
			return 0, err
		}
	}
	return count, nil
}
