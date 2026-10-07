package hyperchi

import (
	"sync"
	"testing"
	"time"

	"github.com/regiellis/hyperchi/hyperchi/config"
)

// TestEventBusConcurrentAccess runs On, Off, Emit, and EventStats
// concurrently. Unguarded, the subscribers map races and can hit Go's fatal
// "concurrent map read and map write". Run with -race.
func TestEventBusConcurrentAccess(t *testing.T) {
	eb := newEventBus()

	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines * 4)

	for i := 0; i < goroutines; i++ {
		go func() { defer wg.Done(); eb.On("evt", func(any) {}) }()
		go func() { defer wg.Done(); eb.Emit("evt", 42) }()
		go func() { defer wg.Done(); _ = eb.EventStats() }()
		go func() { defer wg.Done(); _ = eb.SubscriberCount("evt") }()
	}

	wg.Wait()
}

// TestEventBusEmitInvokesHandlers checks that Emit, which copies the
// subscriber list under the lock, still reaches every handler.
func TestEventBusEmitInvokesHandlers(t *testing.T) {
	eb := newEventBus()

	var mu sync.Mutex
	var count int
	var wg sync.WaitGroup

	const handlers = 5
	wg.Add(handlers)
	for i := 0; i < handlers; i++ {
		eb.On("ping", func(any) {
			mu.Lock()
			count++
			mu.Unlock()
			wg.Done()
		})
	}

	eb.Emit("ping", nil)
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if count != handlers {
		t.Fatalf("expected %d handler invocations, got %d", handlers, count)
	}
}

// TestCacheManagerConcurrentAccess runs Set, Get, Delete, Stats, and
// RecordHit concurrently. Unguarded, the shared maps race and can hit a fatal
// "concurrent map writes".
func TestCacheManagerConcurrentAccess(t *testing.T) {
	cm := newCacheManager(&config.CacheConfig{Enabled: true, MaxEntries: 2, CleanupTime: time.Nanosecond})

	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines * 5)

	for i := 0; i < goroutines; i++ {
		key := "k" + itoa(i%3)
		go func() { defer wg.Done(); cm.Set(key, 1, time.Minute) }()
		go func() { defer wg.Done(); cm.Get(key) }()
		go func() { defer wg.Done(); cm.Delete(key) }()
		go func() { defer wg.Done(); _ = cm.Stats() }()
		go func() { defer wg.Done(); cm.RecordHit(key) }()
	}

	wg.Wait()
}

// TestCacheManagerStatKeyCap verifies per-key telemetry is bounded so
// high-cardinality keys cannot grow the stat maps without limit, while the
// global counters remain accurate.
func TestCacheManagerStatKeyCap(t *testing.T) {
	cm := newCacheManager(&config.CacheConfig{})

	const distinctKeys = maxTrackedStatKeys + 500
	for i := 0; i < distinctKeys; i++ {
		cm.RecordHit("key-" + itoa(i))
	}

	cm.mu.Lock()
	trackedKeys := len(cm.hits)
	total := cm.totalHits
	cm.mu.Unlock()

	if trackedKeys > maxTrackedStatKeys {
		t.Fatalf("per-key stat map exceeded cap: %d > %d", trackedKeys, maxTrackedStatKeys)
	}
	if total != int64(distinctKeys) {
		t.Fatalf("global hit counter should be exact: got %d want %d", total, distinctKeys)
	}
}

// itoa formats a non-negative int for test keys without strconv.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
