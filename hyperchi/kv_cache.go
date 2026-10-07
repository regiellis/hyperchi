// A TTL cache on any KVStore, with tag-based invalidation and helpers for
// rendered templates and fragments.

package hyperchi

import (
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"slices"
	"strings"
	"sync"
	"time"
)

// KVCache stores values with expiry times in a KVStore. Values round-trip
// through JSON, so a struct reads back as map[string]any and a number as
// float64; []byte is kept as []byte. Create one with NewKVCache.
type KVCache struct {
	store      KVStore
	keyPrefix  string
	defaultTTL time.Duration

	// tagMu serializes read-modify-write cycles on the tag index. Without it,
	// concurrent tagged Sets lose tag mappings and InvalidateByTag misses
	// entries, so stale values survive invalidation.
	tagMu sync.Mutex
}

// KVCacheConfig configures NewKVCache.
type KVCacheConfig struct {
	Backend    string        // Name of a store in the KVRegistry
	KeyPrefix  string        // Key prefix for cache keys (default: "cache:")
	DefaultTTL time.Duration // Default TTL for cache entries (default: 1 hour)
}

// NewKVCache returns a cache on the registry's config.Backend store, with the
// defaults noted on KVCacheConfig. It fails when no such store is registered.
func NewKVCache(registry *KVRegistry, config KVCacheConfig) (*KVCache, error) {
	store, err := registry.Get(config.Backend)
	if err != nil {
		return nil, fmt.Errorf("failed to get KV store '%s': %w", config.Backend, err)
	}

	if config.KeyPrefix == "" {
		config.KeyPrefix = "cache:"
	}
	if config.DefaultTTL == 0 {
		config.DefaultTTL = 1 * time.Hour
	}

	return &KVCache{
		store:      store,
		keyPrefix:  config.KeyPrefix,
		defaultTTL: config.DefaultTTL,
	}, nil
}

// CacheEntry is the stored form of one cached value.
type CacheEntry struct {
	Key       string    `json:"key"`
	Value     any       `json:"value"`
	ValueType string    `json:"value_type,omitempty"` // "bytes" distinguishes byte slices from strings.
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
	Tags      []string  `json:"tags,omitempty"`
}

// IsExpired reports whether ExpiresAt has passed.
func (e *CacheEntry) IsExpired() bool {
	return time.Now().After(e.ExpiresAt)
}

// Get returns the value under key and true, or false when it is missing,
// expired, or unreadable. Expired and unreadable entries are deleted.
func (c *KVCache) Get(key string) (any, bool) {
	cacheKey := c.keyPrefix + key
	data, err := c.store.Get(cacheKey)
	if err != nil {
		return nil, false
	}

	var entry CacheEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		if delErr := c.store.Delete(cacheKey); delErr != nil {
			log.Printf("hyperchi: cache delete error for corrupted entry %q: %v", cacheKey, delErr)
		}
		return nil, false
	}

	if entry.IsExpired() {
		if delErr := c.store.Delete(cacheKey); delErr != nil {
			log.Printf("hyperchi: cache delete error for expired entry %q: %v", cacheKey, delErr)
		}
		return nil, false
	}

	if entry.ValueType == "bytes" {
		if entry.Value == nil {
			return []byte(nil), true
		}
		encoded, ok := entry.Value.(string)
		if !ok {
			return nil, false
		}
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return nil, false
		}
		return decoded, true
	}
	return entry.Value, true
}

// GetString returns the value under key when it is a string.
func (c *KVCache) GetString(key string) (string, bool) {
	value, exists := c.Get(key)
	if !exists {
		return "", false
	}

	if str, ok := value.(string); ok {
		return str, true
	}
	return "", false
}

// GetBytes returns the value under key as bytes: a []byte as stored, a string
// converted, and anything else as its JSON encoding.
func (c *KVCache) GetBytes(key string) ([]byte, bool) {
	value, exists := c.Get(key)
	if !exists {
		return nil, false
	}

	switch v := value.(type) {
	case []byte:
		return v, true
	case string:
		return []byte(v), true
	default:
		if data, err := json.Marshal(v); err == nil {
			return data, true
		}
		return nil, false
	}
}

// Set stores value under key for the default TTL.
func (c *KVCache) Set(key string, value any) error {
	return c.SetTTL(key, value, c.defaultTTL)
}

// SetTTL stores value under key for ttl.
func (c *KVCache) SetTTL(key string, value any, ttl time.Duration) error {
	return c.SetWithTags(key, value, ttl, nil)
}

// SetWithTags stores value under key for ttl and indexes it under tags for
// InvalidateByTag. A failed tag index write is logged, not returned.
func (c *KVCache) SetWithTags(key string, value any, ttl time.Duration, tags []string) error {
	now := time.Now()
	entry := CacheEntry{
		Key:       key,
		Value:     value,
		CreatedAt: now,
		ExpiresAt: now.Add(ttl),
		Tags:      tags,
	}
	if _, ok := value.([]byte); ok {
		entry.ValueType = "bytes"
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("failed to marshal cache entry: %w", err)
	}

	cacheKey := c.keyPrefix + key

	if advancedStore, ok := c.store.(AdvancedKVStore); ok {
		err = advancedStore.SetTTL(cacheKey, data, ttl)
	} else {
		err = c.store.Set(cacheKey, data)
	}

	if err != nil {
		return fmt.Errorf("failed to set cache entry: %w", err)
	}

	if len(tags) > 0 {
		c.indexTags(key, tags)
	}

	return nil
}

// Delete removes key and its tag index entries.
func (c *KVCache) Delete(key string) error {
	cacheKey := c.keyPrefix + key

	c.removeTagMappings(key)

	return c.store.Delete(cacheKey)
}

// Exists reports whether Get would find key.
func (c *KVCache) Exists(key string) bool {
	_, exists := c.Get(key)
	return exists
}

// Clear removes every key under the cache's prefix, tag indexes included.
func (c *KVCache) Clear() error {
	if advancedStore, ok := c.store.(AdvancedKVStore); ok {
		return advancedStore.DeletePrefix(c.keyPrefix)
	}

	keys, err := c.store.Keys(c.keyPrefix)
	if err != nil {
		return err
	}

	for _, key := range keys {
		if err := c.store.Delete(key); err != nil {
			return err
		}
	}

	return nil
}

// InvalidateByTag removes every entry stored with tag, then the tag's index.
// An unknown tag is not an error; a failed entry delete is logged.
func (c *KVCache) InvalidateByTag(tag string) error {
	tagKey := c.keyPrefix + "tag:" + tag
	data, err := c.store.Get(tagKey)
	if err != nil {
		return nil
	}

	var keys []string
	if err := json.Unmarshal(data, &keys); err != nil {
		return err
	}

	for _, key := range keys {
		if delErr := c.Delete(key); delErr != nil {
			log.Printf("hyperchi: cache delete error for tagged key %q: %v", key, delErr)
		}
	}

	return c.store.Delete(tagKey)
}

// InvalidateByTags calls InvalidateByTag for each tag, stopping at the first
// error.
func (c *KVCache) InvalidateByTags(tags []string) error {
	for _, tag := range tags {
		if err := c.InvalidateByTag(tag); err != nil {
			return err
		}
	}
	return nil
}

// GetOrSet returns the cached value under key, or calls compute and caches its
// result for the default TTL. Concurrent misses each call compute.
func (c *KVCache) GetOrSet(key string, compute func() (any, error)) (any, error) {
	return c.GetOrSetTTL(key, c.defaultTTL, compute)
}

// GetOrSetTTL is GetOrSet with ttl. A compute error is returned and nothing
// is cached; a failed cache write is logged and the computed value returned.
func (c *KVCache) GetOrSetTTL(key string, ttl time.Duration, compute func() (any, error)) (any, error) {
	if value, exists := c.Get(key); exists {
		return value, nil
	}

	value, err := compute()
	if err != nil {
		return nil, err
	}

	if err := c.SetTTL(key, value, ttl); err != nil {
		log.Printf("hyperchi: cache set error: %v", err)
	}

	return value, nil
}

// indexTags adds key to each tag's index ("<prefix>tag:<tag>", a JSON list of
// keys).
func (c *KVCache) indexTags(key string, tags []string) {
	c.tagMu.Lock()
	defer c.tagMu.Unlock()

	for _, tag := range tags {
		tagKey := c.keyPrefix + "tag:" + tag

		var keys []string
		if data, err := c.store.Get(tagKey); err == nil {
			if err := json.Unmarshal(data, &keys); err != nil {
				keys = nil
			}
		}

		if slices.Contains(keys, key) {
			continue
		}

		keys = append(keys, key)
		data, err := json.Marshal(keys)
		if err != nil {
			log.Printf("hyperchi: cache tag index marshal error for %q: %v", tag, err)
			continue
		}
		if err := c.store.Set(tagKey, data); err != nil {
			log.Printf("hyperchi: cache tag index write error for %q: %v", tag, err)
		}
	}
}

// removeTagMappings removes key from every tag index, deleting an index that
// becomes empty. It reads every tag index.
func (c *KVCache) removeTagMappings(key string) {
	c.tagMu.Lock()
	defer c.tagMu.Unlock()

	tagKeys, err := c.store.Keys(c.keyPrefix + "tag:")
	if err != nil {
		return
	}

	for _, tagKey := range tagKeys {
		data, err := c.store.Get(tagKey)
		if err != nil {
			continue
		}

		var keys []string
		if err := json.Unmarshal(data, &keys); err != nil {
			continue
		}

		var newKeys []string
		for _, k := range keys {
			if k != key {
				newKeys = append(newKeys, k)
			}
		}

		if len(newKeys) == 0 {
			if err := c.store.Delete(tagKey); err != nil {
				log.Printf("hyperchi: cache tag index delete error for %q: %v", tagKey, err)
			}
		} else if data, err := json.Marshal(newKeys); err == nil {
			if err := c.store.Set(tagKey, data); err != nil {
				log.Printf("hyperchi: cache tag index write error for %q: %v", tagKey, err)
			}
		}
	}
}

// Stats counts the cache's entries, excluding tag indexes. It reads every
// entry.
func (c *KVCache) Stats() (CacheStats, error) {
	keys, err := c.store.Keys(c.keyPrefix)
	if err != nil {
		return CacheStats{}, err
	}

	stats := CacheStats{
		TotalKeys: len(keys),
	}

	now := time.Now()
	for _, key := range keys {
		if strings.Contains(key, ":tag:") {
			stats.TotalKeys--
			continue
		}

		data, err := c.store.Get(key)
		if err != nil {
			continue
		}

		var entry CacheEntry
		if err := json.Unmarshal(data, &entry); err != nil {
			stats.CorruptedKeys++
			continue
		}

		if now.After(entry.ExpiresAt) {
			stats.ExpiredKeys++
		} else {
			stats.ValidKeys++
		}
	}

	return stats, nil
}

// CleanupExpired deletes expired and unreadable entries. A failed delete is
// logged, not returned.
func (c *KVCache) CleanupExpired() error {
	keys, err := c.store.Keys(c.keyPrefix)
	if err != nil {
		return err
	}

	now := time.Now()
	var expiredKeys []string

	for _, key := range keys {
		if strings.Contains(key, ":tag:") {
			continue
		}

		data, err := c.store.Get(key)
		if err != nil {
			continue
		}

		var entry CacheEntry
		if err := json.Unmarshal(data, &entry); err != nil {
			expiredKeys = append(expiredKeys, key)
			continue
		}

		if now.After(entry.ExpiresAt) {
			expiredKeys = append(expiredKeys, key)
		}
	}

	for _, key := range expiredKeys {
		if delErr := c.store.Delete(key); delErr != nil {
			log.Printf("hyperchi: cache delete error for expired key %q: %v", key, delErr)
		}
	}

	return nil
}

// CacheStats is the result of KVCache.Stats.
type CacheStats struct {
	TotalKeys     int `json:"total_keys"`
	ValidKeys     int `json:"valid_keys"`
	ExpiredKeys   int `json:"expired_keys"`
	CorruptedKeys int `json:"corrupted_keys"`
}

// CacheTemplate caches a template's rendered html for one data value, tagged
// "template" and "template:<name>".
func (c *KVCache) CacheTemplate(templateName string, data any, html string, ttl time.Duration) error {
	key := c.generateTemplateKey(templateName, data)
	return c.SetWithTags(key, html, ttl, []string{"template", "template:" + templateName})
}

// GetCachedTemplate returns the html CacheTemplate stored for templateName and
// data.
func (c *KVCache) GetCachedTemplate(templateName string, data any) (string, bool) {
	key := c.generateTemplateKey(templateName, data)
	if html, exists := c.GetString(key); exists {
		return html, true
	}
	return "", false
}

// InvalidateTemplate removes every cached render of templateName.
func (c *KVCache) InvalidateTemplate(templateName string) error {
	return c.InvalidateByTag("template:" + templateName)
}

// InvalidateAllTemplates removes every cached template render.
func (c *KVCache) InvalidateAllTemplates() error {
	return c.InvalidateByTag("template")
}

// generateTemplateKey keys a render by template name and an MD5 of the name
// and the JSON-encoded data. MD5 only spreads keys here; it is not a security
// boundary.
func (c *KVCache) generateTemplateKey(templateName string, data any) string {
	hasher := md5.New()
	hasher.Write([]byte(templateName))

	if dataBytes, err := json.Marshal(data); err == nil {
		hasher.Write(dataBytes)
	}

	hash := hex.EncodeToString(hasher.Sum(nil))
	return fmt.Sprintf("template:%s:%s", templateName, hash)
}

// CacheFragment caches a rendered fragment's html, tagged "fragment" and
// "fragment:<name>".
func (c *KVCache) CacheFragment(fragmentName string, html string, ttl time.Duration) error {
	key := "fragment:" + fragmentName
	return c.SetWithTags(key, html, ttl, []string{"fragment", "fragment:" + fragmentName})
}

// GetCachedFragment returns the html CacheFragment stored for fragmentName.
func (c *KVCache) GetCachedFragment(fragmentName string) (string, bool) {
	key := "fragment:" + fragmentName
	return c.GetString(key)
}

// InvalidateFragment removes the cached fragment fragmentName.
func (c *KVCache) InvalidateFragment(fragmentName string) error {
	return c.Delete("fragment:" + fragmentName)
}

// InvalidateAllFragments removes every cached fragment.
func (c *KVCache) InvalidateAllFragments() error {
	return c.InvalidateByTag("fragment")
}
