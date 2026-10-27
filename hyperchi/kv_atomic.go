package hyperchi

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"time"

	"go.etcd.io/bbolt"
)

// AtomicTakeStore removes a key and returns its value in one atomic operation.
// Missing or expired keys return ErrKeyNotFound. Implementations must serialize
// this operation with other writes, including calls through other store handles.
type AtomicTakeStore interface {
	GetAndDelete(key string) ([]byte, error)
}

// AtomicTTLStore increments a counter and sets its lifetime in one atomic
// operation. The counter is stored as JSON decimal text. An expired key starts
// at zero. Implementations must serialize with all writes to the same backend.
type AtomicTTLStore interface {
	IncrementTTL(key string, ttl time.Duration) (int64, error)
}

// GetAndDelete atomically consumes a value shared by all users of this store.
func (s *MemoryStore) GetAndDelete(key string) ([]byte, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.closed {
		return nil, ErrStoreClosed
	}
	value, ok := s.data[key]
	expiry, expires := s.expiry[key]
	delete(s.data, key)
	delete(s.expiry, key)
	if !ok || (expires && !time.Now().Before(expiry)) {
		return nil, ErrKeyNotFound
	}
	return append([]byte(nil), value...), nil
}

// IncrementTTL atomically increments a counter and refreshes its expiration.
func (s *MemoryAdvancedStore) IncrementTTL(key string, ttl time.Duration) (int64, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.closed {
		return 0, ErrStoreClosed
	}
	var count int64
	if expiry, ok := s.expiry[key]; !ok || time.Now().Before(expiry) {
		if value, ok := s.data[key]; ok {
			if err := json.Unmarshal(value, &count); err != nil {
				return 0, fmt.Errorf("decode counter: %w", err)
			}
		}
	}
	count++
	value, err := json.Marshal(count)
	if err != nil {
		return 0, err
	}
	s.data[key] = value
	s.expiry[key] = time.Now().Add(ttl)
	return count, nil
}

// GetAndDelete consumes a value in one bbolt write transaction. All users of
// the database share the claim; bbolt permits only one writable database opener.
func (s *BBoltStore) GetAndDelete(key string) ([]byte, error) {
	if s.closed.Load() {
		return nil, ErrStoreClosed
	}
	var value []byte
	err := s.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket(s.bucketName)
		if b == nil {
			return fmt.Errorf("bucket not found")
		}
		v := b.Get([]byte(key))
		if v == nil || isExpiredLocked(b, key) {
			return ErrKeyNotFound
		}
		value = append([]byte(nil), v...)
		if err := b.Delete([]byte(key)); err != nil {
			return err
		}
		return b.Delete([]byte(key + expirySuffix))
	})
	return value, err
}

// IncrementTTL increments and expires a counter in one bbolt write transaction.
func (s *BBoltAdvancedStore) IncrementTTL(key string, ttl time.Duration) (int64, error) {
	if s.closed.Load() {
		return 0, ErrStoreClosed
	}
	var count int64
	err := s.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket(s.bucketName)
		if b == nil {
			return fmt.Errorf("bucket not found")
		}
		if value := b.Get([]byte(key)); value != nil && !isExpiredLocked(b, key) {
			var err error
			if count, err = decodeCounter(value); err != nil {
				return err
			}
		}
		count++
		value, err := json.Marshal(count)
		if err != nil {
			return err
		}
		if err := b.Put([]byte(key), value); err != nil {
			return err
		}
		expiry := make([]byte, 8)
		binary.BigEndian.PutUint64(expiry, uint64(time.Now().Add(ttl).Unix()))
		return b.Put([]byte(key+expirySuffix), expiry)
	})
	return count, err
}
