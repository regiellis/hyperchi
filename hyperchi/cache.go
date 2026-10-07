package hyperchi

import (
	"container/list"
	"sync"
	"time"

	"github.com/regiellis/hyperchi/hyperchi/config"
)

// CacheManager (app.Cache) is an in-process cache with expiring entries, an
// optional cap on the number of entries, and hit and miss statistics. It is
// safe for concurrent use. config.CacheConfig sets it up: the default TTL,
// MaxEntries (storing past the cap evicts the least recently used entry),
// CleanupTime (how often Set sweeps out expired entries), and Enabled (off
// stores nothing). For a cache in a shared or persistent KV store, use KVCache.
type CacheManager struct {
	mu          sync.Mutex
	entries     map[string]*list.Element // values are *cacheEntry
	recency     *list.List               // most recently used at the front
	hits        map[string]int64
	misses      map[string]int64
	hitTimes    map[string][]int64 // recent hit timestamps per key
	missTimes   map[string][]int64 // recent miss timestamps per key
	totalHits   int64
	totalMisses int64
	startTime   int64 // Unix seconds at creation or the last ClearStats
	enabled     bool
	maxEntries  int
	defaultTTL  time.Duration
	cleanup     time.Duration
	lastSweep   time.Time
	now         func() time.Time
}

type cacheEntry struct {
	key     string
	value   any
	expires time.Time // zero: never
}

// newCacheManager returns an empty cache configured from cacheConfig.
func newCacheManager(cacheConfig *config.CacheConfig) *CacheManager {
	now := time.Now()
	return &CacheManager{
		entries:    make(map[string]*list.Element),
		recency:    list.New(),
		hits:       make(map[string]int64),
		misses:     make(map[string]int64),
		hitTimes:   make(map[string][]int64),
		missTimes:  make(map[string][]int64),
		startTime:  now.Unix(),
		enabled:    cacheConfig.Enabled,
		maxEntries: cacheConfig.MaxEntries,
		defaultTTL: cacheConfig.TTL,
		cleanup:    cacheConfig.CleanupTime,
		lastSweep:  now,
		now:        time.Now,
	}
}

// Set stores value under key for ttl. A ttl of zero uses the configured TTL,
// and a negative ttl never expires. Storing a new key in a full cache first
// drops expired entries, then the least recently used ones. With the cache
// disabled, Set stores nothing.
func (cm *CacheManager) Set(key string, value any, ttl time.Duration) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	if !cm.enabled {
		return
	}
	now := cm.now()
	if cm.cleanup > 0 && now.Sub(cm.lastSweep) >= cm.cleanup {
		cm.sweep(now)
	}
	if ttl == 0 {
		ttl = cm.defaultTTL
	}
	var expires time.Time
	if ttl > 0 {
		expires = now.Add(ttl)
	}

	if el, ok := cm.entries[key]; ok {
		entry := el.Value.(*cacheEntry)
		entry.value, entry.expires = value, expires
		cm.recency.MoveToFront(el)
		return
	}
	if cm.maxEntries > 0 && len(cm.entries) >= cm.maxEntries {
		cm.sweep(now)
		for len(cm.entries) >= cm.maxEntries {
			cm.remove(cm.recency.Back())
		}
	}
	cm.entries[key] = cm.recency.PushFront(&cacheEntry{key: key, value: value, expires: expires})
}

// Get returns the value under key and records a hit or a miss. An expired
// entry is a miss and is removed.
func (cm *CacheManager) Get(key string) (any, bool) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	el, ok := cm.entries[key]
	if ok && el.Value.(*cacheEntry).expired(cm.now()) {
		cm.remove(el)
		ok = false
	}
	if !ok {
		cm.recordMiss(key)
		return nil, false
	}
	cm.recency.MoveToFront(el)
	cm.recordHit(key)
	return el.Value.(*cacheEntry).value, true
}

// Delete removes key.
func (cm *CacheManager) Delete(key string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	if el, ok := cm.entries[key]; ok {
		cm.remove(el)
	}
}

// Clear removes every entry. Statistics are kept; see ClearStats.
func (cm *CacheManager) Clear() {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.entries = make(map[string]*list.Element)
	cm.recency.Init()
}

func (e *cacheEntry) expired(now time.Time) bool {
	return !e.expires.IsZero() && !now.Before(e.expires)
}

// remove drops one entry. Caller must hold cm.mu.
func (cm *CacheManager) remove(el *list.Element) {
	delete(cm.entries, el.Value.(*cacheEntry).key)
	cm.recency.Remove(el)
}

// sweep removes every expired entry. Caller must hold cm.mu.
func (cm *CacheManager) sweep(now time.Time) {
	cm.lastSweep = now
	for el := cm.recency.Front(); el != nil; {
		next := el.Next()
		if el.Value.(*cacheEntry).expired(now) {
			cm.remove(el)
		}
		el = next
	}
}

// Stats returns the entry count (counting expired entries not yet removed),
// total hits and misses, overall and recent hit rates, the five most-hit keys,
// and uptime in seconds.
func (cm *CacheManager) Stats() map[string]any {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	hitRate := float64(0)
	if cm.totalHits+cm.totalMisses > 0 {
		hitRate = float64(cm.totalHits) / float64(cm.totalHits+cm.totalMisses)
	}

	topKeys := cm.getTopKeys(5)

	recentHitRate := cm.getRecentHitRate()

	return map[string]any{
		"items":         len(cm.entries),
		"totalHits":     cm.totalHits,
		"totalMisses":   cm.totalMisses,
		"hitRate":       hitRate,
		"recentHitRate": recentHitRate,
		"topKeys":       topKeys,
		"uptime":        time.Now().Unix() - cm.startTime,
		"memoryKeys":    len(cm.hits),
	}
}

// getTopKeys returns up to limit tracked keys with the most hits. Caller must
// hold cm.mu.
func (cm *CacheManager) getTopKeys(limit int) []map[string]any {
	type keyStats struct {
		key    string
		hits   int64
		misses int64
	}

	var stats []keyStats
	for key, hits := range cm.hits {
		misses := cm.misses[key]
		stats = append(stats, keyStats{
			key:    key,
			hits:   hits,
			misses: misses,
		})
	}

	// A selection sort: the tracked set is capped at maxTrackedStatKeys.
	for i := 0; i < len(stats); i++ {
		for j := i + 1; j < len(stats); j++ {
			if stats[j].hits > stats[i].hits {
				stats[i], stats[j] = stats[j], stats[i]
			}
		}
	}

	result := make([]map[string]any, 0, limit)
	for i := 0; i < len(stats) && i < limit; i++ {
		rate := float64(0)
		if stats[i].hits+stats[i].misses > 0 {
			rate = float64(stats[i].hits) / float64(stats[i].hits+stats[i].misses)
		}

		result = append(result, map[string]any{
			"key":     stats[i].key,
			"hits":    stats[i].hits,
			"misses":  stats[i].misses,
			"hitRate": rate,
		})
	}

	return result
}

// getRecentHitRate is the hit rate over the timestamps kept per key (the last
// statTimestampWindow of each). Caller must hold cm.mu.
func (cm *CacheManager) getRecentHitRate() float64 {
	recentHits := int64(0)
	recentMisses := int64(0)

	for _, times := range cm.hitTimes {
		recentHits += int64(len(times))
	}
	for _, times := range cm.missTimes {
		recentMisses += int64(len(times))
	}

	if recentHits+recentMisses == 0 {
		return 0.0
	}

	return float64(recentHits) / float64(recentHits+recentMisses)
}

// maxTrackedStatKeys bounds how many distinct keys carry detailed per-key
// hit/miss telemetry. Global counters are always accurate; only the per-key
// breakdown (used for "top keys") is capped to prevent unbounded memory
// growth with high-cardinality cache keys.
const maxTrackedStatKeys = 1000

// statTimestampWindow is the number of recent timestamps retained per key.
const statTimestampWindow = 100

// ClearStats resets every statistic and the uptime clock.
func (cm *CacheManager) ClearStats() {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.hits = make(map[string]int64)
	cm.misses = make(map[string]int64)
	cm.hitTimes = make(map[string][]int64)
	cm.missTimes = make(map[string][]int64)
	cm.totalHits = 0
	cm.totalMisses = 0
	cm.startTime = time.Now().Unix()
}

// RecordHit counts a hit on key for a cache outside the manager, such as the
// ContentManager's item cache.
func (cm *CacheManager) RecordHit(key string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.recordHit(key)
}

// RecordMiss counts a miss on key, as RecordHit counts a hit.
func (cm *CacheManager) RecordMiss(key string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.recordMiss(key)
}

// recordHit records a cache hit. Caller must hold cm.mu.
func (cm *CacheManager) recordHit(key string) {
	cm.totalHits++
	// Only track per-key detail for already-tracked keys or while under the
	// cap; otherwise the global counter above still captures the hit.
	if _, tracked := cm.hits[key]; !tracked && len(cm.hits) >= maxTrackedStatKeys {
		return
	}
	now := time.Now().Unix()
	cm.hits[key]++
	cm.hitTimes[key] = append(cm.hitTimes[key], now)
	if len(cm.hitTimes[key]) > statTimestampWindow {
		// Copy into a fresh backing array so the old one can be collected.
		trimmed := make([]int64, statTimestampWindow)
		copy(trimmed, cm.hitTimes[key][len(cm.hitTimes[key])-statTimestampWindow:])
		cm.hitTimes[key] = trimmed
	}
}

// recordMiss records a cache miss. Caller must hold cm.mu.
func (cm *CacheManager) recordMiss(key string) {
	cm.totalMisses++
	if _, tracked := cm.misses[key]; !tracked && len(cm.misses) >= maxTrackedStatKeys {
		return
	}
	now := time.Now().Unix()
	cm.misses[key]++
	cm.missTimes[key] = append(cm.missTimes[key], now)
	if len(cm.missTimes[key]) > statTimestampWindow {
		trimmed := make([]int64, statTimestampWindow)
		copy(trimmed, cm.missTimes[key][len(cm.missTimes[key])-statTimestampWindow:])
		cm.missTimes[key] = trimmed
	}
}
