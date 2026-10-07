// The KVStore interfaces, the registry of named stores, and KVHelper. The
// backends are MemoryStore (kv_memory.go) and BBoltStore (kv_bbolt.go).

package hyperchi

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

// KVStore is a key-value storage backend. Get returns ErrKeyNotFound for a
// missing key, and every method returns ErrStoreClosed (or false) after Close.
// Implementations are safe for concurrent use.
type KVStore interface {
	Get(key string) ([]byte, error)
	Set(key string, value []byte) error
	Delete(key string) error
	Has(key string) bool

	Keys(prefix string) ([]string, error)
	Close() error

	// Counters are atomic and stored as JSON numbers, so KVHelper.GetInt
	// and GetJSON read them.
	Increment(key string) (int64, error)
	Decrement(key string) (int64, error)
	IncrementBy(key string, delta int64) (int64, error)
}

// KVOperation is one step of AdvancedKVStore.Batch.
type KVOperation struct {
	Op    string // "SET", "DELETE", "INCREMENT"
	Key   string
	Value []byte
	Delta int64 // for INCREMENT
}

// AdvancedKVStore is a KVStore with batches, TTLs, and prefix operations.
// KVSessionStore and KVCache use the TTLs when a store has them.
type AdvancedKVStore interface {
	KVStore

	Batch(ops []KVOperation) error

	SetTTL(key string, value []byte, ttl time.Duration) error
	// GetTTL returns the time left before key expires. A key that is
	// missing, expired, or stored without an expiry returns ErrKeyNotFound.
	GetTTL(key string) (time.Duration, error)

	DeletePrefix(prefix string) error
	CountPrefix(prefix string) (int, error)
}

// KVRegistry holds named KV stores. It is safe for concurrent use.
type KVRegistry struct {
	stores map[string]KVStore
	mutex  sync.RWMutex
}

// NewKVRegistry returns an empty registry.
func NewKVRegistry() *KVRegistry {
	return &KVRegistry{
		stores: make(map[string]KVStore),
	}
}

// Register adds store under name. A name already in use is an error.
func (r *KVRegistry) Register(name string, store KVStore) error {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	if _, exists := r.stores[name]; exists {
		return fmt.Errorf("KV store '%s' already registered", name)
	}

	r.stores[name] = store
	return nil
}

// Get returns the store registered as name, or an error matching
// ErrStoreNotFound.
func (r *KVRegistry) Get(name string) (KVStore, error) {
	r.mutex.RLock()
	defer r.mutex.RUnlock()

	store, exists := r.stores[name]
	if !exists {
		return nil, fmt.Errorf("hyperchi: KV store %q: %w", name, ErrStoreNotFound)
	}

	return store, nil
}

// List returns the registered store names, in no order.
func (r *KVRegistry) List() []string {
	r.mutex.RLock()
	defer r.mutex.RUnlock()

	names := make([]string, 0, len(r.stores))
	for name := range r.stores {
		names = append(names, name)
	}
	return names
}

// Close closes every registered store and returns their errors combined. The
// stores stay registered.
func (r *KVRegistry) Close() error {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	var errors []string
	for name, store := range r.stores {
		if err := store.Close(); err != nil {
			errors = append(errors, fmt.Sprintf("%s: %v", name, err))
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("errors closing stores: %v", errors)
	}

	return nil
}

// KVHelper reads and writes strings and JSON values in a KVStore.
type KVHelper struct {
	store KVStore
}

// NewKVHelper returns a helper for store.
func NewKVHelper(store KVStore) *KVHelper {
	return &KVHelper{store: store}
}

// GetString returns the value under key as a string.
func (h *KVHelper) GetString(key string) (string, error) {
	data, err := h.store.Get(key)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// SetString stores value under key.
func (h *KVHelper) SetString(key, value string) error {
	return h.store.Set(key, []byte(value))
}

// GetJSON decodes the JSON value under key into v.
func (h *KVHelper) GetJSON(key string, v any) error {
	data, err := h.store.Get(key)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

// SetJSON stores v under key as JSON.
func (h *KVHelper) SetJSON(key string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return h.store.Set(key, data)
}

// GetInt returns the JSON number under key, such as a counter Increment
// stored.
func (h *KVHelper) GetInt(key string) (int64, error) {
	var value int64
	err := h.GetJSON(key, &value)
	return value, err
}

// SetInt stores value under key as a JSON number.
func (h *KVHelper) SetInt(key string, value int64) error {
	return h.SetJSON(key, value)
}

var (
	// ErrKeyNotFound is returned for a missing or expired key.
	ErrKeyNotFound = errors.New("key not found")
	// ErrStoreNotFound is wrapped by KVRegistry.Get for an unknown store name.
	ErrStoreNotFound = errors.New("store not found")
	// ErrStoreClosed is returned by a store's methods after Close.
	ErrStoreClosed = errors.New("store is closed")
)
