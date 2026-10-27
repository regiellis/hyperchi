// A KVStore on bbolt, the pure-Go embedded key/value database with ACID
// transactions, for data that must survive a restart.

package hyperchi

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"go.etcd.io/bbolt"
)

// expirySuffix marks the companion key that stores a TTL deadline for a
// value written via SetTTL. Expiry keys are internal: Keys() filters them
// out and Get/Has treat a past deadline as "key not found" (lazy expiry).
const expirySuffix = ":__expiry__"

// BBoltStore is a KVStore in one bucket of a bbolt file. bbolt locks the file,
// so only one process can open it. Counters are stored as JSON numbers, as in
// MemoryStore, so KVHelper.GetInt reads them. Create one with NewBBoltStore.
type BBoltStore struct {
	db         *bbolt.DB
	bucketName []byte
	closed     atomic.Bool
}

// NewBBoltStore opens (or creates, mode 0600) the bbolt file at dbPath and its
// bucketName bucket ("default" when empty). It fails when the file stays
// locked by another process for a second.
func NewBBoltStore(dbPath string, bucketName string) (*BBoltStore, error) {
	if bucketName == "" {
		bucketName = "default"
	}

	if err := os.MkdirAll(filepath.Dir(dbPath), 0o750); err != nil {
		return nil, fmt.Errorf("failed to create BBolt directory: %w", err)
	}
	db, err := bbolt.Open(dbPath, 0600, &bbolt.Options{
		Timeout: 1 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to open BBolt database: %w", err)
	}

	store := &BBoltStore{
		db:         db,
		bucketName: []byte(bucketName),
	}

	err = db.Update(func(tx *bbolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists(store.bucketName)
		return err
	})
	if err != nil {
		if closeErr := db.Close(); closeErr != nil {
			log.Printf("hyperchi: failed to close BBolt database after bucket creation failure: %v", closeErr)
		}
		return nil, fmt.Errorf("failed to create bucket: %w", err)
	}

	return store, nil
}

// isExpiredLocked reports whether key has a TTL marker that is in the past.
// Must be called inside a bbolt transaction on b.
func isExpiredLocked(b *bbolt.Bucket, key string) bool {
	expiryBytes := b.Get([]byte(key + expirySuffix))
	if len(expiryBytes) != 8 {
		return false
	}
	expiryTime := int64(binary.BigEndian.Uint64(expiryBytes))
	return time.Now().Unix() >= expiryTime
}

// deleteExpired removes an expired key and its TTL marker. A key rewritten
// since it was read as expired is kept.
func (s *BBoltStore) deleteExpired(key string) error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket(s.bucketName)
		if b == nil {
			return nil
		}
		if !isExpiredLocked(b, key) {
			// Re-check under the write lock: the key may have been rewritten.
			return nil
		}
		if err := b.Delete([]byte(key)); err != nil {
			return err
		}
		return b.Delete([]byte(key + expirySuffix))
	})
}

// Get retrieves a value by key. Values written with SetTTL whose deadline
// has passed are treated as missing and removed lazily.
func (s *BBoltStore) Get(key string) ([]byte, error) {
	if s.closed.Load() {
		return nil, ErrStoreClosed
	}

	var value []byte
	expired := false
	err := s.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket(s.bucketName)
		if b == nil {
			return fmt.Errorf("bucket not found")
		}

		if isExpiredLocked(b, key) {
			expired = true
			return ErrKeyNotFound
		}

		v := b.Get([]byte(key))
		if v == nil {
			return ErrKeyNotFound
		}

		// Copy the value since it's only valid during the transaction
		value = make([]byte, len(v))
		copy(value, v)
		return nil
	})

	if expired {
		// The key already reads as missing; a failed delete only leaves it
		// for the next read to remove.
		if delErr := s.deleteExpired(key); delErr != nil {
			log.Printf("hyperchi: bbolt: remove expired key %q: %v", key, delErr)
		}
	}
	return value, err
}

// Set stores value under key and clears any TTL.
func (s *BBoltStore) Set(key string, value []byte) error {
	if s.closed.Load() {
		return ErrStoreClosed
	}

	return s.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket(s.bucketName)
		if b == nil {
			return fmt.Errorf("bucket not found")
		}

		if err := b.Delete([]byte(key + expirySuffix)); err != nil {
			return err
		}
		return b.Put([]byte(key), value)
	})
}

// Delete removes key and its TTL. A missing key is not an error.
func (s *BBoltStore) Delete(key string) error {
	if s.closed.Load() {
		return ErrStoreClosed
	}

	return s.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket(s.bucketName)
		if b == nil {
			return fmt.Errorf("bucket not found")
		}

		if err := b.Delete([]byte(key + expirySuffix)); err != nil {
			return err
		}
		return b.Delete([]byte(key))
	})
}

// Has reports whether key holds an unexpired value.
func (s *BBoltStore) Has(key string) bool {
	if s.closed.Load() {
		return false
	}

	exists := false
	if err := s.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket(s.bucketName)
		if b == nil {
			return nil
		}

		if isExpiredLocked(b, key) {
			return nil
		}

		v := b.Get([]byte(key))
		exists = v != nil
		return nil
	}); err != nil {
		return false
	}

	return exists
}

// Keys returns the keys that start with prefix, in byte order. Internal TTL
// markers are left out so callers never see or delete them. Expired keys not
// yet removed are included.
func (s *BBoltStore) Keys(prefix string) ([]string, error) {
	if s.closed.Load() {
		return nil, ErrStoreClosed
	}

	var keys []string
	err := s.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket(s.bucketName)
		if b == nil {
			return fmt.Errorf("bucket not found")
		}

		c := b.Cursor()
		prefixBytes := []byte(prefix)

		for k, _ := c.Seek(prefixBytes); k != nil && strings.HasPrefix(string(k), prefix); k, _ = c.Next() {
			if strings.HasSuffix(string(k), expirySuffix) {
				continue
			}
			keys = append(keys, string(k))
		}

		return nil
	})

	return keys, err
}

// Close closes the database file. Closing twice is safe.
func (s *BBoltStore) Close() error {
	if !s.closed.CompareAndSwap(false, true) {
		return nil
	}
	return s.db.Close()
}

// Increment is IncrementBy(key, 1).
func (s *BBoltStore) Increment(key string) (int64, error) {
	return s.IncrementBy(key, 1)
}

// Decrement is IncrementBy(key, -1).
func (s *BBoltStore) Decrement(key string) (int64, error) {
	return s.IncrementBy(key, -1)
}

// IncrementBy atomically adds delta to the counter under key and returns the
// new value, as MemoryStore does: the counter is stored as a JSON number, a
// missing, expired, or non-numeric value counts as 0, and the result has no
// expiry. An 8-byte big-endian value, the encoding earlier versions wrote,
// still reads as its number.
func (s *BBoltStore) IncrementBy(key string, delta int64) (int64, error) {
	if s.closed.Load() {
		return 0, ErrStoreClosed
	}

	var newValue int64
	err := s.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket(s.bucketName)
		if b == nil {
			return fmt.Errorf("bucket not found")
		}

		var err error
		newValue, err = incrementCounter(b, key, delta)
		return err
	})

	return newValue, err
}

// incrementCounter adds delta to the counter under key inside a write
// transaction on b, stores it as a JSON number with no expiry, and returns it.
func incrementCounter(b *bbolt.Bucket, key string, delta int64) (int64, error) {
	var current int64
	if value := b.Get([]byte(key)); value != nil && !isExpiredLocked(b, key) {
		var err error
		if current, err = decodeCounter(value); err != nil {
			current = 0 // not a number: counts as 0, as in MemoryStore
		}
	}
	next := current + delta
	encoded, err := json.Marshal(next)
	if err != nil {
		return 0, fmt.Errorf("encode counter: %w", err)
	}
	if err := b.Delete([]byte(key + expirySuffix)); err != nil {
		return 0, err
	}
	if err := b.Put([]byte(key), encoded); err != nil {
		return 0, err
	}
	return next, nil
}

// decodeCounter reads a counter stored as a JSON number, or as the 8-byte
// big-endian integer earlier versions of BBoltStore wrote.
func decodeCounter(value []byte) (int64, error) {
	var n int64
	err := json.Unmarshal(value, &n)
	if err == nil {
		return n, nil
	}
	if len(value) == 8 {
		return int64(binary.BigEndian.Uint64(value)), nil
	}
	return 0, fmt.Errorf("decode counter: %w", err)
}

// BBoltAdvancedStore is a BBoltStore that also implements AdvancedKVStore:
// TTLs, batches, and prefix operations.
type BBoltAdvancedStore struct {
	*BBoltStore
}

// NewBBoltAdvancedStore opens a store as NewBBoltStore does.
func NewBBoltAdvancedStore(dbPath string, bucketName string) (*BBoltAdvancedStore, error) {
	store, err := NewBBoltStore(dbPath, bucketName)
	if err != nil {
		return nil, err
	}

	return &BBoltAdvancedStore{BBoltStore: store}, nil
}

// Batch applies ops ("SET", "DELETE", "INCREMENT") in one bbolt transaction:
// all of them, or none when one fails or is unknown.
func (s *BBoltAdvancedStore) Batch(ops []KVOperation) error {
	if s.closed.Load() {
		return ErrStoreClosed
	}

	return s.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket(s.bucketName)
		if b == nil {
			return fmt.Errorf("bucket not found")
		}

		for _, op := range ops {
			switch op.Op {
			case "SET":
				if err := b.Delete([]byte(op.Key + expirySuffix)); err != nil {
					return err
				}
				if err := b.Put([]byte(op.Key), op.Value); err != nil {
					return err
				}

			case "DELETE":
				if err := b.Delete([]byte(op.Key + expirySuffix)); err != nil {
					return err
				}
				if err := b.Delete([]byte(op.Key)); err != nil {
					return err
				}

			case "INCREMENT":
				if _, err := incrementCounter(b, op.Key, op.Delta); err != nil {
					return err
				}

			default:
				return fmt.Errorf("unknown operation: %s", op.Op)
			}
		}

		return nil
	})
}

// SetTTL stores value under key with a deadline ttl from now, kept in a
// companion marker key at one-second resolution. Expired keys are removed
// lazily, when read.
func (s *BBoltAdvancedStore) SetTTL(key string, value []byte, ttl time.Duration) error {
	if s.closed.Load() {
		return ErrStoreClosed
	}

	expiryKey := key + expirySuffix
	expiryTime := time.Now().Add(ttl).Unix()

	return s.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket(s.bucketName)
		if b == nil {
			return fmt.Errorf("bucket not found")
		}

		if err := b.Put([]byte(key), value); err != nil {
			return err
		}

		expiryBytes := make([]byte, 8)
		binary.BigEndian.PutUint64(expiryBytes, uint64(expiryTime))
		return b.Put([]byte(expiryKey), expiryBytes)
	})
}

// GetTTL returns key's remaining lifetime: ErrKeyNotFound when it has no TTL,
// and 0 once it has expired.
func (s *BBoltAdvancedStore) GetTTL(key string) (time.Duration, error) {
	if s.closed.Load() {
		return 0, ErrStoreClosed
	}

	expiryKey := key + expirySuffix
	var ttl time.Duration

	err := s.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket(s.bucketName)
		if b == nil {
			return fmt.Errorf("bucket not found")
		}

		expiryBytes := b.Get([]byte(expiryKey))
		if expiryBytes == nil {
			return ErrKeyNotFound
		}

		expiryTime := int64(binary.BigEndian.Uint64(expiryBytes))
		remaining := time.Until(time.Unix(expiryTime, 0))

		if remaining <= 0 {
			ttl = 0
		} else {
			ttl = remaining
		}

		return nil
	})

	return ttl, err
}

// DeletePrefix deletes every key that starts with prefix, with their TTL
// markers. A marker whose name starts with prefix but belongs to a key outside
// it (prefix "user:1:" and the marker of "user:1") is kept, so that key keeps
// its deadline.
func (s *BBoltAdvancedStore) DeletePrefix(prefix string) error {
	if s.closed.Load() {
		return ErrStoreClosed
	}

	return s.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket(s.bucketName)
		if b == nil {
			return fmt.Errorf("bucket not found")
		}

		// Collect first, then delete: deleting through the cursor while it
		// walks the bucket depends on how bbolt pages nodes in and out.
		var doomed []string
		c := b.Cursor()
		for k, _ := c.Seek([]byte(prefix)); k != nil && strings.HasPrefix(string(k), prefix); k, _ = c.Next() {
			key := string(k)
			if base, isMarker := strings.CutSuffix(key, expirySuffix); isMarker {
				if strings.HasPrefix(base, prefix) {
					doomed = append(doomed, key)
				}
				continue
			}
			doomed = append(doomed, key, key+expirySuffix)
		}

		for _, key := range doomed {
			if err := b.Delete([]byte(key)); err != nil {
				return err
			}
		}
		return nil
	})
}

// CountPrefix counts the live keys that start with prefix: TTL markers and
// keys whose deadline has passed are left out, as in MemoryAdvancedStore.
func (s *BBoltAdvancedStore) CountPrefix(prefix string) (int, error) {
	if s.closed.Load() {
		return 0, ErrStoreClosed
	}

	var count int
	err := s.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket(s.bucketName)
		if b == nil {
			return fmt.Errorf("bucket not found")
		}

		c := b.Cursor()
		for k, _ := c.Seek([]byte(prefix)); k != nil && strings.HasPrefix(string(k), prefix); k, _ = c.Next() {
			key := string(k)
			if strings.HasSuffix(key, expirySuffix) || isExpiredLocked(b, key) {
				continue
			}
			count++
		}

		return nil
	})

	return count, err
}
