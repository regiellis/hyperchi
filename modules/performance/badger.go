package performance

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dgraph-io/badger/v4"
	"github.com/regiellis/hyperchi/hyperchi"
)

// KVOperation is one step of a BadgerAdvancedStore.Batch. It is the
// framework's type, so a batch built for any hyperchi.AdvancedKVStore runs
// here unchanged.
type KVOperation = hyperchi.KVOperation

// The Badger stores serve every framework KV interface, so sessions, the
// cache, the rate limiter, and the job queue use their TTLs and atomic
// operations.
var (
	_ hyperchi.KVStore         = (*BadgerStore)(nil)
	_ hyperchi.AtomicTakeStore = (*BadgerStore)(nil)
	_ hyperchi.AdvancedKVStore = (*BadgerAdvancedStore)(nil)
	_ hyperchi.AtomicTTLStore  = (*BadgerAdvancedStore)(nil)
	_ TTLStore                 = (*BadgerAdvancedStore)(nil)
)

// BadgerStore is a hyperchi.KVStore on BadgerDB. Counters are stored as JSON
// decimal text, as the KVStore contract asks, so hyperchi.KVHelper.GetInt
// reads them; 8-byte big-endian counters written by earlier versions still
// read and are rewritten as JSON on their next change. It is safe for
// concurrent use, including Close: Close waits for operations in flight, and
// later ones return hyperchi.ErrStoreClosed.
type BadgerStore struct {
	db *badger.DB

	mu        sync.RWMutex // held for reading by every operation, for writing by Close
	closed    bool         // guarded by mu
	closeOnce sync.Once
	closeErr  error
	stopGC    chan struct{} // closed by Close to end runGC
	gcDone    chan struct{} // closed when runGC returns
}

// BadgerConfig holds the Badger options NewBadgerStore sets. Sizes are bytes.
type BadgerConfig struct {
	Dir                string // key and LSM files
	ValueDir           string // value log files; empty uses Dir
	SyncWrites         bool   // fsync every write: slower, survives a crash
	NumVersionsToKeep  int
	CompactL0OnClose   bool
	ValueLogFileSize   int64
	MemTableSize       int64
	BlockCacheSize     int64
	IndexCacheSize     int64
	NumMemtables       int
	NumLevelZeroTables int // L0 tables before compaction starts
	NumCompactors      int // compaction goroutines: 0 turns compaction off, 1 and negatives are refused

	Logger   *slog.Logger // receives Badger's log lines; nil uses slog.Default()
	LogLevel slog.Leveler // lowest level passed to Logger; nil means slog.LevelWarn
}

// DefaultBadgerConfig returns a config for dir with SyncWrites off, one
// version per key, and 256 MiB value log files and block cache.
func DefaultBadgerConfig(dir string) BadgerConfig {
	return BadgerConfig{
		Dir:                dir,
		SyncWrites:         false,
		NumVersionsToKeep:  1,
		CompactL0OnClose:   true,
		ValueLogFileSize:   1 << 28,   // 256MB
		MemTableSize:       64 << 20,  // 64MB
		BlockCacheSize:     256 << 20, // 256MB
		IndexCacheSize:     100 << 20, // 100MB
		NumMemtables:       5,
		NumLevelZeroTables: 5,
		NumCompactors:      4,
	}
}

// NewBadgerStore opens the database and starts a goroutine that runs value
// log garbage collection every five minutes until Close. Badger's log lines go
// to config.Logger through slog, at config.LogLevel and above.
func NewBadgerStore(config BadgerConfig) (*BadgerStore, error) {
	// Badger refuses 1 compactor with an error and panics on a negative count.
	if config.NumCompactors == 1 || config.NumCompactors < 0 {
		return nil, fmt.Errorf("performance: NumCompactors is %d: Badger needs 0 (compaction off) or at least 2", config.NumCompactors)
	}

	opts := badger.DefaultOptions(config.Dir).
		WithLogger(newSlogBadgerLogger(config.Logger, config.LogLevel))

	if config.ValueDir != "" {
		opts = opts.WithValueDir(config.ValueDir)
	}

	opts = opts.
		WithSyncWrites(config.SyncWrites).
		WithNumVersionsToKeep(config.NumVersionsToKeep).
		WithCompactL0OnClose(config.CompactL0OnClose).
		WithValueLogFileSize(config.ValueLogFileSize).
		WithMemTableSize(config.MemTableSize).
		WithBlockCacheSize(config.BlockCacheSize).
		WithIndexCacheSize(config.IndexCacheSize).
		WithNumMemtables(config.NumMemtables).
		WithNumLevelZeroTables(config.NumLevelZeroTables).
		WithNumCompactors(config.NumCompactors)

	db, err := badger.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("failed to open BadgerDB: %w", err)
	}

	store := &BadgerStore{
		db:     db,
		stopGC: make(chan struct{}),
		gcDone: make(chan struct{}),
	}

	go store.runGC(5 * time.Minute)

	return store, nil
}

// Get returns a copy of the value for key, or hyperchi.ErrKeyNotFound.
func (s *BadgerStore) Get(key string) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil, hyperchi.ErrStoreClosed
	}

	var value []byte
	err := s.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte(key))
		if err != nil {
			if err == badger.ErrKeyNotFound {
				return hyperchi.ErrKeyNotFound
			}
			return err
		}

		return item.Value(func(val []byte) error {
			// val is valid only inside the transaction.
			value = make([]byte, len(val))
			copy(value, val)
			return nil
		})
	})

	return value, err
}

// Set stores value under key with no expiry.
func (s *BadgerStore) Set(key string, value []byte) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return hyperchi.ErrStoreClosed
	}

	return s.db.Update(func(txn *badger.Txn) error {
		return txn.Set([]byte(key), value)
	})
}

// Delete removes key. Deleting a missing key is not an error.
func (s *BadgerStore) Delete(key string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return hyperchi.ErrStoreClosed
	}

	return s.db.Update(func(txn *badger.Txn) error {
		return txn.Delete([]byte(key))
	})
}

// Has reports whether key exists. The KVStore interface has no error return,
// so a read error counts as absent. Use Get to tell the two apart.
func (s *BadgerStore) Has(key string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return false
	}

	err := s.db.View(func(txn *badger.Txn) error {
		_, err := txn.Get([]byte(key))
		return err
	})
	return err == nil
}

// Keys returns every key that starts with prefix, in key order.
func (s *BadgerStore) Keys(prefix string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil, hyperchi.ErrStoreClosed
	}

	var keys []string
	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.PrefetchValues = false
		it := txn.NewIterator(opts)
		defer it.Close()

		prefixBytes := []byte(prefix)
		for it.Seek(prefixBytes); it.Valid() && strings.HasPrefix(string(it.Item().Key()), prefix); it.Next() {
			key := make([]byte, len(it.Item().Key()))
			copy(key, it.Item().Key())
			keys = append(keys, string(key))
		}

		return nil
	})

	return keys, err
}

// Close stops value log garbage collection, waits for operations in flight,
// and closes the database. Later calls return the first call's result.
// Iterators from Scan must be closed first.
func (s *BadgerStore) Close() error {
	s.closeOnce.Do(func() {
		close(s.stopGC)
		<-s.gcDone
		s.mu.Lock()
		defer s.mu.Unlock()
		s.closed = true
		s.closeErr = s.db.Close()
	})
	return s.closeErr
}

// Increment adds 1 to the counter at key and returns the new value.
func (s *BadgerStore) Increment(key string) (int64, error) {
	return s.IncrementBy(key, 1)
}

// Decrement subtracts 1 from the counter at key and returns the new value.
func (s *BadgerStore) Decrement(key string) (int64, error) {
	return s.IncrementBy(key, -1)
}

// IncrementBy adds delta to the counter at key in one transaction and returns
// the new value. A missing key starts at 0. An existing value that is not a
// counter (see decodeCounter) is an error. A transaction conflict is returned
// as an error, not retried.
func (s *BadgerStore) IncrementBy(key string, delta int64) (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return 0, hyperchi.ErrStoreClosed
	}

	var newValue int64
	err := s.db.Update(func(txn *badger.Txn) error {
		currentValue, err := readCounter(txn, key)
		if err != nil {
			return err
		}
		newValue = currentValue + delta
		return txn.Set([]byte(key), encodeCounter(newValue))
	})

	return newValue, err
}

// readCounter returns the counter stored at key in txn, or 0 when it is missing.
func readCounter(txn *badger.Txn, key string) (int64, error) {
	item, err := txn.Get([]byte(key))
	if errors.Is(err, badger.ErrKeyNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	value, err := item.ValueCopy(nil)
	if err != nil {
		return 0, err
	}
	count, err := decodeCounter(value)
	if err != nil {
		return 0, fmt.Errorf("counter %q: %w", key, err)
	}
	return count, nil
}

// decodeCounter reads a counter stored as JSON decimal text, or as the 8-byte
// big-endian integer earlier versions of IncrementBy wrote. JSON is tried
// first. A big-endian value starts with a byte JSON rejects unless the
// counter is above 2^56.
func decodeCounter(value []byte) (int64, error) {
	var count int64
	err := json.Unmarshal(value, &count)
	if err == nil {
		return count, nil
	}
	if len(value) == 8 {
		return int64(binary.BigEndian.Uint64(value)), nil
	}
	return 0, fmt.Errorf("decode counter: %w", err)
}

// encodeCounter is the JSON decimal form every counter is written in.
func encodeCounter(count int64) []byte {
	return strconv.AppendInt(nil, count, 10)
}

// runGC runs value log GC every interval until Close closes stopGC.
func (s *BadgerStore) runGC(interval time.Duration) {
	defer close(s.gcDone)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopGC:
			return
		case <-ticker.C:
			s.collectValueLog()
		}
	}
}

// collectValueLog rewrites value log files until none is half discardable.
// RunValueLogGC returns ErrNoRewrite when nothing qualifies and ErrRejected
// when another GC runs. Anything else is logged.
func (s *BadgerStore) collectValueLog() {
	for {
		err := s.db.RunValueLogGC(0.5)
		if err == nil {
			continue
		}
		if !errors.Is(err, badger.ErrNoRewrite) && !errors.Is(err, badger.ErrRejected) {
			log.Printf("performance: badger value log GC: %v", err)
		}
		return
	}
}

// BadgerAdvancedStore adds batches, TTLs, prefix operations, and iteration to
// BadgerStore.
type BadgerAdvancedStore struct {
	*BadgerStore
}

// NewBadgerAdvancedStore opens a BadgerStore and wraps it.
func NewBadgerAdvancedStore(config BadgerConfig) (*BadgerAdvancedStore, error) {
	store, err := NewBadgerStore(config)
	if err != nil {
		return nil, err
	}

	return &BadgerAdvancedStore{BadgerStore: store}, nil
}

// Batch applies ops in one transaction: all of them or none. An unknown Op,
// or an INCREMENT of a value that is not a counter, aborts the batch.
func (s *BadgerAdvancedStore) Batch(ops []KVOperation) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return hyperchi.ErrStoreClosed
	}

	return s.db.Update(func(txn *badger.Txn) error {
		for _, op := range ops {
			switch op.Op {
			case "SET":
				if err := txn.Set([]byte(op.Key), op.Value); err != nil {
					return err
				}

			case "DELETE":
				if err := txn.Delete([]byte(op.Key)); err != nil {
					return err
				}

			case "INCREMENT":
				currentValue, err := readCounter(txn, op.Key)
				if err != nil {
					return err
				}
				if err := txn.Set([]byte(op.Key), encodeCounter(currentValue+op.Delta)); err != nil {
					return err
				}

			default:
				return fmt.Errorf("unknown operation: %s", op.Op)
			}
		}

		return nil
	})
}

// SetTTL stores value under key; Badger drops it after ttl.
func (s *BadgerAdvancedStore) SetTTL(key string, value []byte, ttl time.Duration) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return hyperchi.ErrStoreClosed
	}

	return s.db.Update(func(txn *badger.Txn) error {
		entry := badger.NewEntry([]byte(key), value).WithTTL(ttl)
		return txn.SetEntry(entry)
	})
}

// GetTTL returns the time left before key expires. Like the framework's
// memory and bbolt stores, it returns hyperchi.ErrKeyNotFound for a key with
// no expiry as well as for a missing or expired one.
func (s *BadgerAdvancedStore) GetTTL(key string) (time.Duration, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return 0, hyperchi.ErrStoreClosed
	}

	var ttl time.Duration
	err := s.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte(key))
		if err != nil {
			if err == badger.ErrKeyNotFound {
				return hyperchi.ErrKeyNotFound
			}
			return err
		}

		expiresAt := item.ExpiresAt()
		if expiresAt == 0 {
			return hyperchi.ErrKeyNotFound
		}

		remaining := time.Until(time.Unix(int64(expiresAt), 0))
		if remaining <= 0 {
			ttl = 0
		} else {
			ttl = remaining
		}

		return nil
	})

	return ttl, err
}

// DeletePrefix deletes every key that starts with prefix in one transaction,
// so a very large prefix can exceed Badger's transaction size limit.
func (s *BadgerAdvancedStore) DeletePrefix(prefix string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return hyperchi.ErrStoreClosed
	}

	return s.db.Update(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.PrefetchValues = false
		it := txn.NewIterator(opts)
		defer it.Close()

		prefixBytes := []byte(prefix)
		var keysToDelete [][]byte

		for it.Seek(prefixBytes); it.Valid() && strings.HasPrefix(string(it.Item().Key()), prefix); it.Next() {
			key := make([]byte, len(it.Item().Key()))
			copy(key, it.Item().Key())
			keysToDelete = append(keysToDelete, key)
		}

		for _, key := range keysToDelete {
			if err := txn.Delete(key); err != nil {
				return err
			}
		}

		return nil
	})
}

// CountPrefix returns the number of keys that start with prefix.
func (s *BadgerAdvancedStore) CountPrefix(prefix string) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return 0, hyperchi.ErrStoreClosed
	}

	count := 0
	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.PrefetchValues = false
		it := txn.NewIterator(opts)
		defer it.Close()

		prefixBytes := []byte(prefix)
		for it.Seek(prefixBytes); it.Valid() && strings.HasPrefix(string(it.Item().Key()), prefix); it.Next() {
			count++
		}

		return nil
	})

	return count, err
}

// Scan returns an iterator over the keys that start with prefix. It holds a
// read transaction open until Close.
func (s *BadgerAdvancedStore) Scan(prefix string) KVIterator {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return &badgerIterator{closed: true}
	}

	txn := s.db.NewTransaction(false)
	opts := badger.DefaultIteratorOptions
	it := txn.NewIterator(opts)

	return &badgerIterator{
		txn:    txn,
		it:     it,
		prefix: prefix,
	}
}

// GetStats returns the LSM and value log sizes, the table count, and the
// level-0 table count. A closed store reports zeros.
func (s *BadgerStore) GetStats() BadgerStats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return BadgerStats{}
	}

	lsm, vlog := s.db.Size()
	tables := s.db.Tables()
	level0 := 0
	for _, table := range tables {
		if table.Level == 0 {
			level0++
		}
	}
	return BadgerStats{
		LSMSize:    lsm,
		VLogSize:   vlog,
		TotalSize:  lsm + vlog,
		NumTables:  len(tables),
		NumTables0: level0,
	}
}

// Backup writes a full backup of the database to path (mode 0600), replacing
// any file there. It writes a temporary file in the same directory and renames
// it into place, so a failed backup leaves path untouched. Read it back with
// Restore.
func (s *BadgerStore) Backup(path string) (err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return hyperchi.ErrStoreClosed
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create backup: %w", err)
	}
	renamed := false
	defer func() {
		if !renamed {
			if removeErr := os.Remove(tmp.Name()); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				err = errors.Join(err, removeErr)
			}
		}
	}()

	if _, err := s.db.Backup(tmp, 0); err != nil {
		return errors.Join(fmt.Errorf("write backup: %w", err), tmp.Close())
	}
	if err := tmp.Sync(); err != nil {
		return errors.Join(fmt.Errorf("sync backup: %w", err), tmp.Close())
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close backup: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("move backup into place: %w", err)
	}
	renamed = true
	return nil
}

// Restore loads a file written by Backup into the store. Keys in the backup
// overwrite keys already present, and other keys are kept.
func (s *BadgerStore) Restore(path string) (err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return hyperchi.ErrStoreClosed
	}

	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open backup: %w", err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			err = errors.Join(err, closeErr)
		}
	}()
	if err := s.db.Load(file, 256); err != nil {
		return fmt.Errorf("load backup: %w", err)
	}
	return nil
}

// KVIterator walks keys in order. Call Next before the first Key or Value,
// and Close when done.
type KVIterator interface {
	Next() bool
	Key() string
	Value() ([]byte, error)
	Close()
}

// badgerIterator is the KVIterator Scan returns.
type badgerIterator struct {
	txn    *badger.Txn
	it     *badger.Iterator
	prefix string
	closed bool
}

// Next moves to the next key under the prefix and reports whether there is one.
func (bi *badgerIterator) Next() bool {
	if bi.closed {
		return false
	}

	if !bi.it.Valid() {
		bi.it.Seek([]byte(bi.prefix))
	} else {
		bi.it.Next()
	}

	return bi.it.Valid() && strings.HasPrefix(string(bi.it.Item().Key()), bi.prefix)
}

// Key returns the current key, or "" when the iterator is done or closed.
func (bi *badgerIterator) Key() string {
	if bi.closed || !bi.it.Valid() {
		return ""
	}
	return string(bi.it.Item().Key())
}

// Value returns a copy of the current value.
func (bi *badgerIterator) Value() ([]byte, error) {
	if bi.closed || !bi.it.Valid() {
		return nil, fmt.Errorf("iterator closed or invalid")
	}

	var value []byte
	err := bi.it.Item().Value(func(val []byte) error {
		value = make([]byte, len(val))
		copy(value, val)
		return nil
	})

	return value, err
}

// Close releases the iterator and its read transaction. It is safe to call twice.
func (bi *badgerIterator) Close() {
	if !bi.closed {
		bi.it.Close()
		bi.txn.Discard()
		bi.closed = true
	}
}

// BadgerStats is what GetStats reports. Sizes are bytes.
type BadgerStats struct {
	LSMSize    int64 `json:"lsm_size"`
	VLogSize   int64 `json:"vlog_size"`
	TotalSize  int64 `json:"total_size"`
	NumTables  int   `json:"num_tables"`
	NumTables0 int   `json:"num_tables_l0"`
}
