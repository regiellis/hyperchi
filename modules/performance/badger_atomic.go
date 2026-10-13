package performance

import (
	"errors"
	"runtime"
	"time"

	"github.com/dgraph-io/badger/v4"
	"github.com/regiellis/hyperchi/hyperchi"
)

// updateRetry retries optimistic transaction conflicts. The callback must have
// no external side effects because Badger may require it to execute again.
// The caller holds s.mu for reading.
func (s *BadgerStore) updateRetry(fn func(*badger.Txn) error) error {
	for {
		err := s.db.Update(fn)
		if !errors.Is(err, badger.ErrConflict) {
			return err
		}
		runtime.Gosched()
	}
}

// GetAndDelete consumes a value in one transaction across all users of this
// Badger database. Badger itself exclusively locks the writable database directory.
func (s *BadgerStore) GetAndDelete(key string) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil, hyperchi.ErrStoreClosed
	}

	var value []byte
	err := s.updateRetry(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte(key))
		if errors.Is(err, badger.ErrKeyNotFound) {
			return hyperchi.ErrKeyNotFound
		}
		if err != nil {
			return err
		}
		value, err = item.ValueCopy(nil)
		if err != nil {
			return err
		}
		return txn.Delete([]byte(key))
	})
	return value, err
}

// IncrementTTL increments and expires a counter in one retryable transaction.
// The counter is stored as JSON decimal text.
func (s *BadgerAdvancedStore) IncrementTTL(key string, ttl time.Duration) (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return 0, hyperchi.ErrStoreClosed
	}

	var count int64
	err := s.updateRetry(func(txn *badger.Txn) error {
		current, err := readCounter(txn, key)
		if err != nil {
			return err
		}
		count = current + 1
		return txn.SetEntry(badger.NewEntry([]byte(key), encodeCounter(count)).WithTTL(ttl))
	})
	return count, err
}
