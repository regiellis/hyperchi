package hyperchi

import (
	"testing"
	"time"

	"github.com/regiellis/hyperchi/hyperchi/config"
)

// testCache returns a cache on a clock the test moves by hand.
func testCache(cfg config.CacheConfig) (*CacheManager, *time.Time) {
	cm := newCacheManager(&cfg)
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	cm.now = func() time.Time { return now }
	cm.lastSweep = now
	return cm, &now
}

func TestCacheManagerExpiresEntries(t *testing.T) {
	cm, now := testCache(config.CacheConfig{Enabled: true, TTL: time.Hour})
	cm.Set("own", "a", time.Minute)
	cm.Set("default", "b", 0)
	cm.Set("forever", "c", -1)

	*now = now.Add(time.Minute)
	if _, ok := cm.Get("own"); ok {
		t.Error("entry outlived its TTL")
	}
	if v, ok := cm.Get("default"); !ok || v != "b" {
		t.Errorf("default-TTL entry = %v, %v before the hour is up", v, ok)
	}
	*now = now.Add(time.Hour)
	if _, ok := cm.Get("default"); ok {
		t.Error("entry outlived the configured TTL")
	}
	*now = now.Add(1000 * time.Hour)
	if v, ok := cm.Get("forever"); !ok || v != "c" {
		t.Errorf("entry with a negative TTL expired: %v, %v", v, ok)
	}
	if got := cm.Stats()["items"]; got != 1 {
		t.Errorf("items = %v, want expired entries removed on read", got)
	}
}

func TestCacheManagerEvictsLeastRecentlyUsed(t *testing.T) {
	cm, _ := testCache(config.CacheConfig{Enabled: true, MaxEntries: 2})
	cm.Set("a", 1, 0)
	cm.Set("b", 2, 0)
	if _, ok := cm.Get("a"); !ok { // a is now the most recently used
		t.Fatal("a missing")
	}
	cm.Set("c", 3, 0)
	if _, ok := cm.Get("b"); ok {
		t.Error("b survived although it was the least recently used")
	}
	for _, key := range []string{"a", "c"} {
		if _, ok := cm.Get(key); !ok {
			t.Errorf("%s was evicted", key)
		}
	}
	cm.Set("a", 10, 0) // replacing a key does not evict
	if v, ok := cm.Get("c"); !ok || v != 3 {
		t.Errorf("c = %v, %v after replacing a", v, ok)
	}
}

// A full cache drops expired entries before live ones.
func TestCacheManagerFullCacheDropsExpiredFirst(t *testing.T) {
	cm, now := testCache(config.CacheConfig{Enabled: true, MaxEntries: 2})
	cm.Set("live", 1, -1)
	cm.Set("short", 2, time.Second)
	*now = now.Add(time.Second)
	cm.Set("new", 3, 0)
	if _, ok := cm.Get("live"); !ok {
		t.Error("a live entry was evicted while an expired one was there")
	}
}

func TestCacheManagerCleanupTimeSweeps(t *testing.T) {
	cm, now := testCache(config.CacheConfig{Enabled: true, CleanupTime: time.Minute})
	cm.Set("old", 1, time.Second)
	*now = now.Add(time.Minute)
	cm.Set("trigger", 2, 0)
	if got := cm.Stats()["items"]; got != 1 {
		t.Errorf("items = %v after a sweep, want 1", got)
	}
}

func TestCacheManagerDisabledStoresNothing(t *testing.T) {
	cm, _ := testCache(config.CacheConfig{Enabled: false})
	cm.Set("k", 1, 0)
	if _, ok := cm.Get("k"); ok {
		t.Error("a disabled cache returned a value")
	}
}
