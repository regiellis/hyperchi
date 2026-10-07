package performance

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/regiellis/hyperchi/hyperchi"
	"github.com/regiellis/hyperchi/hyperchi/security"
)

func newTestLimiter(t *testing.T, store hyperchi.KVStore, config RateLimitConfig) *RateLimiter {
	t.Helper()
	registry := hyperchi.NewKVRegistry()
	if err := registry.Register("test", store); err != nil {
		t.Fatal(err)
	}
	config.Backend = "test"
	limiter, err := NewRateLimiter(registry, config)
	if err != nil {
		t.Fatal(err)
	}
	return limiter
}

func memoryStore(t *testing.T) *hyperchi.MemoryAdvancedStore {
	t.Helper()
	store := hyperchi.NewMemoryAdvancedStore()
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	return store
}

func TestPerUserRuleKeysEachUser(t *testing.T) {
	limiter := newTestLimiter(t, memoryStore(t), RateLimitConfig{})
	handler := limiter.RateLimitMiddleware([]RateLimitRule{{Path: "*", Rate: "1/hour", PerUser: true}})(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	request := func(user, addr string) int {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = addr
		if user != "" {
			r = r.WithContext(security.WithUserID(r.Context(), user))
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w.Code
	}

	// Both users share one address, so only the user ID tells them apart.
	steps := []struct {
		user, addr string
		want       int
	}{
		{"alice", "192.0.2.1:1", http.StatusOK},
		{"alice", "192.0.2.1:1", http.StatusTooManyRequests},
		{"bob", "192.0.2.1:1", http.StatusOK},
		{"bob", "192.0.2.1:1", http.StatusTooManyRequests},
		{"", "192.0.2.2:1", http.StatusOK}, // anonymous: counted per client IP
		{"", "192.0.2.3:1", http.StatusOK},
		{"", "192.0.2.2:1", http.StatusTooManyRequests},
	}
	for i, step := range steps {
		if got := request(step.user, step.addr); got != step.want {
			t.Fatalf("step %d (%q from %s): status %d, want %d", i, step.user, step.addr, got, step.want)
		}
	}
}

func TestTokenBucketKeepsFractionalRefill(t *testing.T) {
	limiter := newTestLimiter(t, memoryStore(t), RateLimitConfig{Algorithm: TokenBucket})
	now := time.Unix(1_700_000_000, 0)
	limiter.now = func() time.Time { return now }
	rule := RateLimitRule{MaxRequests: 60, WindowSize: time.Minute, Burst: 1} // one token per second

	allowed := 0
	for range 20 { // a request every half second for ten seconds
		result, err := limiter.Check(rule, "client")
		if err != nil {
			t.Fatal(err)
		}
		if result.Allowed {
			allowed++
		}
		now = now.Add(500 * time.Millisecond)
	}
	if allowed != 10 {
		t.Fatalf("allowed %d of 20 half-second requests at one token per second, want 10", allowed)
	}
}

func TestTokenBucketZeroBurstUsesMaxRequests(t *testing.T) {
	limiter := newTestLimiter(t, memoryStore(t), RateLimitConfig{Algorithm: TokenBucket})
	rule := RateLimitRule{MaxRequests: 3, WindowSize: time.Hour}
	for i := range 4 {
		result, err := limiter.Check(rule, "client")
		if err != nil {
			t.Fatal(err)
		}
		if result.Allowed != (i < 3) {
			t.Fatalf("request %d allowed = %v", i, result.Allowed)
		}
	}
}

// failingStore makes chosen writes fail. Embedding only hyperchi.KVStore
// hides the TTL and atomic methods of the store underneath.
type failingStore struct {
	hyperchi.KVStore
	failSet    func(key string) bool // nil never fails
	failDelete bool
}

func failAll(string) bool { return true }

var errInjected = errors.New("injected store failure")

func (s failingStore) Set(key string, value []byte) error {
	if s.failSet != nil && s.failSet(key) {
		return errInjected
	}
	return s.KVStore.Set(key, value)
}

func (s failingStore) Delete(key string) error {
	if s.failDelete {
		return errInjected
	}
	return s.KVStore.Delete(key)
}

func TestTokenBucketReportsStoreErrors(t *testing.T) {
	limiter := newTestLimiter(t, failingStore{KVStore: memoryStore(t), failSet: failAll}, RateLimitConfig{Algorithm: TokenBucket})
	if _, err := limiter.Check(RateLimitRule{MaxRequests: 5, WindowSize: time.Minute}, "client"); !errors.Is(err, errInjected) {
		t.Fatalf("Check error = %v, want the store's", err)
	}
}

func TestSlidingWindowRejectsCorruptCounter(t *testing.T) {
	store := memoryStore(t)
	limiter := newTestLimiter(t, store, RateLimitConfig{Algorithm: SlidingWindow, KeyPrefix: "rl:"})
	now := time.Unix(1_700_000_040, 0)
	limiter.now = func() time.Time { return now }
	previous := now.Truncate(time.Minute).Add(-time.Minute)
	if err := store.Set(limiter.windowKey("client", previous, 5*time.Minute), []byte("garbage")); err != nil {
		t.Fatal(err)
	}
	if _, err := limiter.Check(RateLimitRule{MaxRequests: 5, WindowSize: 5 * time.Minute}, "client"); err == nil {
		t.Fatal("a corrupt counter was read as zero")
	}
}

func TestCleanupExpiredReportsDeleteErrors(t *testing.T) {
	plain := hyperchi.NewMemoryStore()
	t.Cleanup(func() {
		if err := plain.Close(); err != nil {
			t.Error(err)
		}
	})
	limiter := newTestLimiter(t, failingStore{KVStore: plain, failDelete: true}, RateLimitConfig{KeyPrefix: "rl:"})
	old := time.Now().Add(-2 * time.Hour).Truncate(time.Minute)
	if err := plain.Set(limiter.windowKey("client", old, time.Minute), []byte("3")); err != nil {
		t.Fatal(err)
	}
	if err := limiter.CleanupExpired(); !errors.Is(err, errInjected) {
		t.Fatalf("CleanupExpired error = %v, want the store's", err)
	}
}

func TestRateLimitStatsCountOnlyWindows(t *testing.T) {
	limiter := newTestLimiter(t, memoryStore(t), RateLimitConfig{KeyPrefix: "rl:"})
	rule := RateLimitRule{MaxRequests: 10, WindowSize: time.Hour}
	for range 3 {
		if _, err := limiter.Check(rule, "client"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := limiter.Check(rule, "client2"); err != nil { // shares the "client" prefix
		t.Fatal(err)
	}
	stats, err := limiter.GetStats("client")
	if err != nil {
		t.Fatal(err)
	}
	if stats.TotalRequests != 3 || stats.ActiveWindows != 1 {
		t.Fatalf("stats = %+v, want 3 requests in 1 window", stats)
	}
}

func TestInstalledLimiterRefusesThroughErrorPipeline(t *testing.T) {
	app := hyperchi.New()
	m := New(Config{Dir: t.TempDir(), Workers: 2, EnableRateLimit: true, RateLimitRules: []RateLimitRule{{Path: "/limited", Rate: "1/hour"}}})
	if err := m.Install(app); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := app.Close(); err != nil {
			t.Error(err)
		}
	}()
	var seen error
	app.OnError(func(c *hyperchi.Context, err error) {
		seen = err
		app.DefaultError(c, err)
	})
	app.Get("/limited", func(c *hyperchi.Context) error { return c.Text("ok") })

	for i, want := range []int{http.StatusOK, http.StatusTooManyRequests} {
		w := httptest.NewRecorder()
		app.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/limited", nil))
		if w.Code != want {
			t.Fatalf("request %d: status %d, want %d", i+1, w.Code, want)
		}
		if want != http.StatusTooManyRequests {
			continue
		}
		var he *hyperchi.HTTPError
		if !errors.As(seen, &he) || he.Code != http.StatusTooManyRequests {
			t.Fatalf("OnError saw %v, want a 429 *HTTPError", seen)
		}
		if w.Header().Get("Retry-After") == "" || w.Header().Get("X-RateLimit-Limit") != "1" {
			t.Fatalf("rate limit headers lost: %v", w.Header())
		}
	}
}

func TestLimiterStoreFailureReachesOnError(t *testing.T) {
	app := hyperchi.New()
	defer func() {
		if err := app.Close(); err != nil {
			t.Error(err)
		}
	}()
	var seen error
	app.OnError(func(c *hyperchi.Context, err error) {
		seen = err
		app.DefaultError(c, err)
	})
	limiter := newTestLimiter(t, failingStore{KVStore: memoryStore(t), failSet: failAll}, RateLimitConfig{Algorithm: TokenBucket, App: app})
	handler := limiter.RateLimitMiddleware([]RateLimitRule{{Path: "*", Rate: "5/minute"}})(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("refused request reached the handler") }))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	var he *hyperchi.HTTPError
	if w.Code != http.StatusInternalServerError || !errors.As(seen, &he) || he.Code != http.StatusInternalServerError || !errors.Is(seen, errInjected) {
		t.Fatalf("status %d, OnError saw %v; want a 500 wrapping the store error", w.Code, seen)
	}
}

func TestStandaloneLimiterAnswersPlainText(t *testing.T) {
	limiter := newTestLimiter(t, memoryStore(t), RateLimitConfig{})
	handler := limiter.RateLimitMiddleware([]RateLimitRule{{Path: "*", Rate: "1/hour"}})(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	for range 2 {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
		if w.Code == http.StatusTooManyRequests {
			if body := w.Body.String(); body != "Rate limit exceeded\n" || w.Header().Get("Retry-After") == "" {
				t.Fatalf("standalone refusal: %q, headers %v", body, w.Header())
			}
			return
		}
	}
	t.Fatal("second request was not refused")
}

func TestCleanupExpiredKeepsCountersOfLongWindows(t *testing.T) {
	for name, algorithm := range map[string]RateLimitAlgorithm{"fixed": FixedWindow, "sliding": SlidingWindow} {
		t.Run(name, func(t *testing.T) {
			plain := hyperchi.NewMemoryStore()
			t.Cleanup(func() {
				if err := plain.Close(); err != nil {
					t.Error(err)
				}
			})
			limiter := newTestLimiter(t, failingStore{KVStore: plain}, RateLimitConfig{KeyPrefix: "rl:", Algorithm: algorithm})
			start := time.Unix(1_700_000_000, 0).Truncate(2 * time.Hour)
			now := start
			limiter.now = func() time.Time { return now }
			rule := RateLimitRule{MaxRequests: 2, WindowSize: 2 * time.Hour}

			if _, err := limiter.Check(rule, "client"); err != nil {
				t.Fatal(err)
			}
			now = start.Add(90 * time.Minute)
			if err := limiter.CleanupExpired(); err != nil {
				t.Fatal(err)
			}
			result, err := limiter.Check(rule, "client")
			if err != nil {
				t.Fatal(err)
			}
			if result.TotalHits != 2 {
				t.Fatalf("after cleanup inside the window: %d hits, want 2", result.TotalHits)
			}

			now = start.Add(4 * time.Hour)
			if err := limiter.CleanupExpired(); err != nil {
				t.Fatal(err)
			}
			keys, err := plain.Keys("rl:")
			if err != nil {
				t.Fatal(err)
			}
			if len(keys) != 0 {
				t.Fatalf("expired counters kept: %v", keys)
			}
		})
	}
}
