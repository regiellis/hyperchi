package hyperchi

import (
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func atomicTestStores(t *testing.T) map[string]AdvancedKVStore {
	t.Helper()
	bolt, err := NewBBoltAdvancedStore(filepath.Join(t.TempDir(), "kv.db"), "test")
	if err != nil {
		t.Fatal(err)
	}
	memory := NewMemoryAdvancedStore()
	t.Cleanup(func() {
		if err := bolt.Close(); err != nil {
			t.Error(err)
		}
		if err := memory.Close(); err != nil {
			t.Error(err)
		}
	})
	return map[string]AdvancedKVStore{"memory": memory, "bbolt": bolt}
}

func TestAtomicStoreClaimsAndCounters(t *testing.T) {
	for name, store := range atomicTestStores(t) {
		t.Run(name, func(t *testing.T) {
			if err := store.Set("job", []byte("payload")); err != nil {
				t.Fatal(err)
			}
			var claims atomic.Int32
			var wg sync.WaitGroup
			for range 100 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					value, err := store.(AtomicTakeStore).GetAndDelete("job")
					if err == nil {
						claims.Add(1)
						if string(value) != "payload" {
							t.Errorf("value = %q", value)
						}
					} else if !errors.Is(err, ErrKeyNotFound) {
						t.Error(err)
					}
					if _, err := store.(AtomicTTLStore).IncrementTTL("counter", time.Hour); err != nil {
						t.Error(err)
					}
				}()
			}
			wg.Wait()
			if claims.Load() != 1 {
				t.Fatalf("claims = %d", claims.Load())
			}
			value, err := store.Get("counter")
			if err != nil || string(value) != "100" {
				t.Fatalf("counter = %q, %v", value, err)
			}
			if ttl, err := store.GetTTL("counter"); err != nil || ttl <= 0 {
				t.Fatalf("TTL = %v, %v", ttl, err)
			}
			if err := store.SetTTL("expired", []byte("100"), -time.Second); err != nil {
				t.Fatal(err)
			}
			if count, err := store.(AtomicTTLStore).IncrementTTL("expired", time.Hour); err != nil || count != 1 {
				t.Fatalf("expired counter = %d, %v", count, err)
			}
		})
	}
}

func TestBBoltReplacementClearsExpiration(t *testing.T) {
	store, err := NewBBoltAdvancedStore(filepath.Join(t.TempDir(), "kv.db"), "test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	}()
	for _, operation := range []string{"set", "delete", "batch-set", "batch-delete"} {
		t.Run(operation, func(t *testing.T) {
			if err := store.SetTTL("key", []byte("old"), -time.Second); err != nil {
				t.Fatal(err)
			}
			switch operation {
			case "delete":
				err = store.Delete("key")
			case "batch-delete":
				err = store.Batch([]KVOperation{{Op: "DELETE", Key: "key"}})
			case "batch-set":
				err = store.Batch([]KVOperation{{Op: "SET", Key: "key", Value: []byte("new")}})
			}
			if err != nil {
				t.Fatal(err)
			}
			if operation != "batch-set" {
				if err := store.Set("key", []byte("new")); err != nil {
					t.Fatal(err)
				}
			}
			value, err := store.Get("key")
			if err != nil || string(value) != "new" {
				t.Fatalf("replacement = %q, %v", value, err)
			}
		})
	}
}
