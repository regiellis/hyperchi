// An in-memory KVStore, for development, tests, and data that need not
// survive a restart.

package hyperchi

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

// MemoryStore is an in-memory KVStore, safe for concurrent use. Values are
// copied in and out. A minute-interval goroutine deletes expired keys until
// Close. Create one with NewMemoryStore.
type MemoryStore struct {
	data   map[string][]byte
	expiry map[string]time.Time
	mutex  sync.RWMutex
	closed bool
	stopCh chan struct{} // closed by Close() to stop the cleanup goroutine promptly
}

// NewMemoryStore returns an empty store and starts its cleanup goroutine; call
// Close to stop it.
func NewMemoryStore() *MemoryStore {
	store := &MemoryStore{
		data:   make(map[string][]byte),
		expiry: make(map[string]time.Time),
		stopCh: make(chan struct{}),
	}

	go store.cleanupExpired()

	return store
}

// Get returns a copy of key's value, or ErrKeyNotFound when it is missing or
// expired.
func (s *MemoryStore) Get(key string) ([]byte, error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	if s.closed {
		return nil, ErrStoreClosed
	}

	// Only the read lock is held, so the maps must not change here: an expired
	// key reads as absent and the cleanup goroutine deletes it later.
	if expiry, hasExpiry := s.expiry[key]; hasExpiry && time.Now().After(expiry) {
		return nil, ErrKeyNotFound
	}

	value, exists := s.data[key]
	if !exists {
		return nil, ErrKeyNotFound
	}

	result := make([]byte, len(value))
	copy(result, value)
	return result, nil
}

// Set stores a copy of value under key with no expiry.
func (s *MemoryStore) Set(key string, value []byte) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if s.closed {
		return ErrStoreClosed
	}

	valueCopy := make([]byte, len(value))
	copy(valueCopy, value)
	s.data[key] = valueCopy

	delete(s.expiry, key)

	return nil
}

// Delete removes key. A missing key is not an error.
func (s *MemoryStore) Delete(key string) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if s.closed {
		return ErrStoreClosed
	}

	delete(s.data, key)
	delete(s.expiry, key)
	return nil
}

// Has reports whether key holds an unexpired value. A closed store has
// nothing.
func (s *MemoryStore) Has(key string) bool {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	if s.closed {
		return false
	}

	if expiry, hasExpiry := s.expiry[key]; hasExpiry && time.Now().After(expiry) {
		return false
	}

	_, exists := s.data[key]
	return exists
}

// Keys returns the unexpired keys that start with prefix, in no order.
func (s *MemoryStore) Keys(prefix string) ([]string, error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	if s.closed {
		return nil, ErrStoreClosed
	}

	var keys []string
	now := time.Now()

	for key := range s.data {
		if expiry, hasExpiry := s.expiry[key]; hasExpiry && now.After(expiry) {
			continue
		}

		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}

	return keys, nil
}

// Close drops all data and stops the cleanup goroutine. Later calls return
// ErrStoreClosed (or false, 0). Closing twice is safe.
func (s *MemoryStore) Close() error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if s.closed {
		return nil // stopCh is already closed
	}

	s.closed = true
	s.data = nil
	s.expiry = nil
	close(s.stopCh)
	return nil
}

// Increment is IncrementBy(key, 1).
func (s *MemoryStore) Increment(key string) (int64, error) {
	return s.IncrementBy(key, 1)
}

// Decrement is IncrementBy(key, -1).
func (s *MemoryStore) Decrement(key string) (int64, error) {
	return s.IncrementBy(key, -1)
}

// IncrementBy atomically adds delta to the counter under key and returns the
// new value. The counter is stored as a JSON number. A missing, expired, or
// non-numeric value counts as 0, and the result has no expiry.
func (s *MemoryStore) IncrementBy(key string, delta int64) (int64, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if s.closed {
		return 0, ErrStoreClosed
	}

	var currentValue int64 = 0
	if valueBytes, exists := s.data[key]; exists {
		if expiry, hasExpiry := s.expiry[key]; hasExpiry && time.Now().After(expiry) {
			delete(s.data, key)
			delete(s.expiry, key)
		} else {
			if err := json.Unmarshal(valueBytes, &currentValue); err != nil {
				currentValue = 0
			}
		}
	}

	newValue := currentValue + delta

	newValueBytes, err := json.Marshal(newValue)
	if err != nil {
		return 0, fmt.Errorf("failed to marshal counter value: %w", err)
	}

	s.data[key] = newValueBytes
	delete(s.expiry, key)

	return newValue, nil
}

// cleanupExpired deletes expired keys once a minute until Close.
func (s *MemoryStore) cleanupExpired() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.mutex.Lock()
			if s.closed {
				s.mutex.Unlock()
				return
			}

			now := time.Now()
			var expiredKeys []string

			for key, expiry := range s.expiry {
				if now.After(expiry) {
					expiredKeys = append(expiredKeys, key)
				}
			}

			for _, key := range expiredKeys {
				delete(s.data, key)
				delete(s.expiry, key)
			}

			s.mutex.Unlock()
		}
	}
}

// MemoryAdvancedStore is a MemoryStore that also implements AdvancedKVStore:
// TTLs, batches, and prefix operations.
type MemoryAdvancedStore struct {
	*MemoryStore
}

// NewMemoryAdvancedStore returns an empty store; call Close to stop its
// cleanup goroutine.
func NewMemoryAdvancedStore() *MemoryAdvancedStore {
	return &MemoryAdvancedStore{
		MemoryStore: NewMemoryStore(),
	}
}

// Batch applies ops ("SET", "DELETE", "INCREMENT") in order under one lock, so
// no reader sees part of a batch. An unknown op stops the batch with an error
// and leaves the earlier ops applied.
func (s *MemoryAdvancedStore) Batch(ops []KVOperation) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if s.closed {
		return ErrStoreClosed
	}

	for _, op := range ops {
		switch op.Op {
		case "SET":
			valueCopy := make([]byte, len(op.Value))
			copy(valueCopy, op.Value)
			s.data[op.Key] = valueCopy
			delete(s.expiry, op.Key)

		case "DELETE":
			delete(s.data, op.Key)
			delete(s.expiry, op.Key)

		case "INCREMENT":
			var currentValue int64 = 0
			if valueBytes, exists := s.data[op.Key]; exists {
				if expiry, hasExpiry := s.expiry[op.Key]; hasExpiry && time.Now().After(expiry) {
					delete(s.data, op.Key)
					delete(s.expiry, op.Key)
				} else if err := json.Unmarshal(valueBytes, &currentValue); err != nil {
					log.Printf("hyperchi: failed to unmarshal counter value for key %q: %v", op.Key, err)
				}
			}

			newValue := currentValue + op.Delta

			newValueBytes, err := json.Marshal(newValue)
			if err != nil {
				return fmt.Errorf("failed to marshal counter value: %w", err)
			}

			s.data[op.Key] = newValueBytes
			delete(s.expiry, op.Key)

		default:
			return fmt.Errorf("unknown operation: %s", op.Op)
		}
	}

	return nil
}

// SetTTL stores a copy of value under key, expiring after ttl.
func (s *MemoryAdvancedStore) SetTTL(key string, value []byte, ttl time.Duration) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if s.closed {
		return ErrStoreClosed
	}

	valueCopy := make([]byte, len(value))
	copy(valueCopy, value)
	s.data[key] = valueCopy

	s.expiry[key] = time.Now().Add(ttl)

	return nil
}

// GetTTL returns key's remaining lifetime: ErrKeyNotFound when it has no
// expiry (or does not exist), and 0 once it has expired.
func (s *MemoryAdvancedStore) GetTTL(key string) (time.Duration, error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	if s.closed {
		return 0, ErrStoreClosed
	}

	expiry, hasExpiry := s.expiry[key]
	if !hasExpiry {
		return 0, ErrKeyNotFound
	}

	remaining := time.Until(expiry)
	if remaining <= 0 {
		return 0, nil
	}

	return remaining, nil
}

// DeletePrefix deletes every key that starts with prefix.
func (s *MemoryAdvancedStore) DeletePrefix(prefix string) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if s.closed {
		return ErrStoreClosed
	}

	var keysToDelete []string
	for key := range s.data {
		if strings.HasPrefix(key, prefix) {
			keysToDelete = append(keysToDelete, key)
		}
	}

	for _, key := range keysToDelete {
		delete(s.data, key)
		delete(s.expiry, key)
	}

	return nil
}

// CountPrefix counts the unexpired keys that start with prefix.
func (s *MemoryAdvancedStore) CountPrefix(prefix string) (int, error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	if s.closed {
		return 0, ErrStoreClosed
	}

	count := 0
	now := time.Now()

	for key := range s.data {
		if expiry, hasExpiry := s.expiry[key]; hasExpiry && now.After(expiry) {
			continue
		}

		if strings.HasPrefix(key, prefix) {
			count++
		}
	}

	return count, nil
}

// Size returns the number of unexpired keys, or 0 once closed.
func (s *MemoryStore) Size() int {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	if s.closed {
		return 0
	}

	count := 0
	now := time.Now()

	for key := range s.data {
		if expiry, hasExpiry := s.expiry[key]; hasExpiry && now.After(expiry) {
			continue
		}
		count++
	}

	return count
}

// Clear removes every key.
func (s *MemoryStore) Clear() error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if s.closed {
		return ErrStoreClosed
	}

	s.data = make(map[string][]byte)
	s.expiry = make(map[string]time.Time)
	return nil
}
