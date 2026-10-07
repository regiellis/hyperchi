package hyperchi

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.etcd.io/bbolt"
)

func newBBoltPrefixStore(t *testing.T) *BBoltAdvancedStore {
	t.Helper()
	store, err := NewBBoltAdvancedStore(filepath.Join(t.TempDir(), "kv.db"), "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})
	return store
}

// rawKeys returns every key in the store's bucket that starts with prefix,
// TTL markers included.
func rawKeys(t *testing.T, store *BBoltAdvancedStore, prefix string) []string {
	t.Helper()
	var keys []string
	err := store.db.View(func(tx *bbolt.Tx) error {
		c := tx.Bucket(store.bucketName).Cursor()
		for k, _ := c.Seek([]byte(prefix)); k != nil && strings.HasPrefix(string(k), prefix); k, _ = c.Next() {
			keys = append(keys, string(k))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return keys
}

// CountPrefix counts the live keys, as MemoryAdvancedStore does: not the TTL
// markers, and not keys whose deadline has passed.
func TestBBoltCountPrefixCountsOnlyLiveKeys(t *testing.T) {
	store := newBBoltPrefixStore(t)
	if err := store.Set("user:1", []byte("a")); err != nil {
		t.Fatal(err)
	}
	if err := store.SetTTL("user:2", []byte("b"), time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := store.SetTTL("user:3", []byte("c"), -time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := store.Set("other", []byte("d")); err != nil {
		t.Fatal(err)
	}

	n, err := store.CountPrefix("user:")
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("CountPrefix(user:) = %d, want 2 (user:1 and user:2)", n)
	}
}

// DeletePrefix removes every key under the prefix and their TTL markers,
// however many there are, and leaves other keys alone.
func TestBBoltDeletePrefixRemovesEveryKey(t *testing.T) {
	store := newBBoltPrefixStore(t)
	const total = 500
	ops := make([]KVOperation, 0, total)
	for i := range total {
		ops = append(ops, KVOperation{Op: "SET", Key: fmt.Sprintf("item:%04d", i), Value: []byte("v")})
	}
	if err := store.Batch(ops); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < total; i += 3 {
		if err := store.SetTTL(fmt.Sprintf("item:%04d", i), []byte("v"), time.Hour); err != nil {
			t.Fatal(err)
		}
	}
	for _, key := range []string{"item", "itemz", "other"} {
		if err := store.Set(key, []byte("keep")); err != nil {
			t.Fatal(err)
		}
	}

	if err := store.DeletePrefix("item:"); err != nil {
		t.Fatal(err)
	}
	if left := rawKeys(t, store, "item:"); len(left) != 0 {
		t.Fatalf("DeletePrefix left %d of the keys under item:, first %q", len(left), left[0])
	}
	for _, key := range []string{"item", "itemz", "other"} {
		if !store.Has(key) {
			t.Errorf("DeletePrefix(item:) removed %q", key)
		}
	}
}

// A key outside the prefix keeps its TTL even when its marker's name falls
// under the prefix: "user:1"'s marker is "user:1:__expiry__".
func TestBBoltDeletePrefixKeepsOutsideKeysTTL(t *testing.T) {
	store := newBBoltPrefixStore(t)
	if err := store.SetTTL("user:1", []byte("v"), -time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := store.Set("user:1:name", []byte("n")); err != nil {
		t.Fatal(err)
	}

	if err := store.DeletePrefix("user:1:"); err != nil {
		t.Fatal(err)
	}
	if store.Has("user:1:name") {
		t.Error("user:1:name survived DeletePrefix(user:1:)")
	}
	if _, err := store.Get("user:1"); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("Get(user:1) after DeletePrefix(user:1:) = %v, want ErrKeyNotFound: its TTL marker was deleted", err)
	}
}
