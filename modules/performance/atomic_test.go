package performance

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/regiellis/hyperchi/hyperchi"
)

func performanceTestStores(t *testing.T) map[string]hyperchi.KVStore {
	t.Helper()
	memory := hyperchi.NewMemoryAdvancedStore()
	bolt, err := hyperchi.NewBBoltAdvancedStore(t.TempDir()+"/kv.db", "test")
	if err != nil {
		t.Fatal(err)
	}
	badger, err := NewBadgerAdvancedStore(DefaultBadgerConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	stores := map[string]hyperchi.KVStore{"memory": memory, "bbolt": bolt, "badger": badger}
	t.Cleanup(func() {
		for _, store := range stores {
			if err := store.Close(); err != nil {
				t.Error(err)
			}
		}
	})
	return stores
}

func TestQueueClaimsAcrossInstances(t *testing.T) {
	for name, store := range performanceTestStores(t) {
		t.Run(name, func(t *testing.T) {
			registry := hyperchi.NewKVRegistry()
			if err := registry.Register("test", store); err != nil {
				t.Fatal(err)
			}
			var queues []*JobQueue
			for range 4 {
				q, err := NewJobQueue(registry, JobQueueConfig{Backend: "test"})
				if err != nil {
					t.Fatal(err)
				}
				queues = append(queues, q)
			}
			const jobs = 40
			for i := range jobs {
				if _, err := queues[0].PushWithOptions("test", nil, JobOptions{ID: fmt.Sprint(i)}); err != nil {
					t.Fatal(err)
				}
			}
			var seen sync.Map
			var claimed atomic.Int32
			var wg sync.WaitGroup
			for _, q := range queues {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for range jobs {
						job, err := q.getNextJob()
						if errors.Is(err, hyperchi.ErrKeyNotFound) {
							continue
						}
						if err != nil {
							t.Error(err)
							return
						}
						if _, loaded := seen.LoadOrStore(job.ID, true); loaded {
							t.Errorf("duplicate claim %s", job.ID)
						}
						claimed.Add(1)
					}
				}()
			}
			wg.Wait()
			if claimed.Load() != jobs {
				t.Fatalf("claimed %d, want %d", claimed.Load(), jobs)
			}
		})
	}
}

func TestRateLimitCountersAcrossInstances(t *testing.T) {
	for name, store := range performanceTestStores(t) {
		t.Run(name, func(t *testing.T) {
			registry := hyperchi.NewKVRegistry()
			if err := registry.Register("test", store); err != nil {
				t.Fatal(err)
			}
			for _, algorithm := range []RateLimitAlgorithm{FixedWindow, SlidingWindow} {
				t.Run(fmt.Sprint(algorithm), func(t *testing.T) {
					var limiters []*RateLimiter
					for range 4 {
						rl, err := NewRateLimiter(registry, RateLimitConfig{Backend: "test", Algorithm: algorithm, KeyPrefix: fmt.Sprintf("test%d:", algorithm)})
						if err != nil {
							t.Fatal(err)
						}
						limiters = append(limiters, rl)
					}
					var allowed atomic.Int32
					var wg sync.WaitGroup
					for i := range 200 {
						wg.Add(1)
						go func() {
							defer wg.Done()
							result, err := limiters[i%len(limiters)].Check(RateLimitRule{MaxRequests: 20, WindowSize: time.Hour}, "user")
							if err != nil {
								t.Error(err)
								return
							}
							if result.Allowed {
								allowed.Add(1)
							}
						}()
					}
					wg.Wait()
					if allowed.Load() != 20 {
						t.Fatalf("allowed %d, want 20", allowed.Load())
					}
				})
			}
		})
	}
}

// legacyStore embeds only hyperchi.AdvancedKVStore, which hides the optional
// atomic methods and forces the in-process fallback.
type legacyStore struct{ hyperchi.AdvancedKVStore }

func TestLegacyStoreFallbackWithinInstance(t *testing.T) {
	memory := hyperchi.NewMemoryAdvancedStore()
	defer func() {
		if err := memory.Close(); err != nil {
			t.Error(err)
		}
	}()
	registry := hyperchi.NewKVRegistry()
	if err := registry.Register("legacy", legacyStore{memory}); err != nil {
		t.Fatal(err)
	}
	queue, err := NewJobQueue(registry, JobQueueConfig{Backend: "legacy"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := queue.Push("work", nil); err != nil {
		t.Fatal(err)
	}
	limiter, err := NewRateLimiter(registry, RateLimitConfig{Backend: "legacy"})
	if err != nil {
		t.Fatal(err)
	}
	var claims, allowed atomic.Int32
	var wg sync.WaitGroup
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := queue.getNextJob(); err == nil {
				claims.Add(1)
			} else if !errors.Is(err, hyperchi.ErrKeyNotFound) {
				t.Error(err)
			}
			result, err := limiter.Check(RateLimitRule{MaxRequests: 10, WindowSize: time.Hour}, "user")
			if err != nil {
				t.Error(err)
				return
			}
			if result.Allowed {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()
	if claims.Load() != 1 {
		t.Errorf("claims = %d", claims.Load())
	}
	if allowed.Load() != 10 {
		t.Errorf("allowed = %d", allowed.Load())
	}
}
