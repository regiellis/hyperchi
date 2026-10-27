package hyperchi

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// Both stores keep counters as JSON numbers, so KVHelper.GetInt and every
// other reader see the same value whichever backend wrote it.
func TestKVCountersAgreeAcrossStores(t *testing.T) {
	for name, store := range atomicTestStores(t) {
		t.Run(name, func(t *testing.T) {
			helper := NewKVHelper(store)

			for range 3 {
				if _, err := store.Increment("hits"); err != nil {
					t.Fatal(err)
				}
			}
			if got, err := helper.GetInt("hits"); err != nil || got != 3 {
				t.Fatalf("GetInt after Increment = %d, %v; want 3", got, err)
			}

			if err := store.Batch([]KVOperation{{Op: "INCREMENT", Key: "hits", Delta: 2}}); err != nil {
				t.Fatal(err)
			}
			if got, err := helper.GetInt("hits"); err != nil || got != 5 {
				t.Fatalf("GetInt after Batch INCREMENT = %d, %v; want 5", got, err)
			}

			if err := helper.SetInt("seeded", 10); err != nil {
				t.Fatal(err)
			}
			if got, err := store.IncrementBy("seeded", 5); err != nil || got != 15 {
				t.Fatalf("IncrementBy on a SetInt value = %d, %v; want 15", got, err)
			}

			// An expired counter starts again at zero and the result has no TTL.
			if err := store.SetTTL("expired", []byte("7"), -time.Minute); err != nil {
				t.Fatal(err)
			}
			if got, err := store.Increment("expired"); err != nil || got != 1 {
				t.Fatalf("Increment on an expired counter = %d, %v; want 1", got, err)
			}
			if _, err := store.GetTTL("expired"); !errors.Is(err, ErrKeyNotFound) {
				t.Fatalf("GetTTL after Increment = %v, want ErrKeyNotFound (no expiry)", err)
			}
			if got, err := helper.GetInt("expired"); err != nil || got != 1 {
				t.Fatalf("GetInt on the restarted counter = %d, %v; want 1", got, err)
			}
		})
	}
}

// Counters written by an older BBoltStore as 8 raw bytes still count.
func TestBBoltCounterReadsLegacyEncoding(t *testing.T) {
	store := atomicTestStores(t)["bbolt"]
	legacy := make([]byte, 8)
	binary.BigEndian.PutUint64(legacy, 41)
	if err := store.Set("legacy", legacy); err != nil {
		t.Fatal(err)
	}
	if got, err := store.Increment("legacy"); err != nil || got != 42 {
		t.Fatalf("Increment on a legacy counter = %d, %v; want 42", got, err)
	}
	if got, err := NewKVHelper(store).GetInt("legacy"); err != nil || got != 42 {
		t.Fatalf("GetInt after the upgrade = %d, %v; want 42", got, err)
	}
}

// A session reads an int back the same way before and after a store round
// trip.
func TestKVSessionGetIntReadsItsOwnWrites(t *testing.T) {
	store := NewMemoryStore()
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	session, err := NewKVSession(store, "session:", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]any{"int": 7, "int64": int64(8), "uint8": uint8(9), "float": 10.0, "json": json.Number("11")}
	want := map[string]int{"int": 7, "int64": 8, "uint8": 9, "float": 10, "json": 11}
	for key, value := range values {
		session.Set(key, value)
	}
	for key, n := range want {
		if got := session.GetInt(key); got != n {
			t.Errorf("GetInt(%q) in the same request = %d, want %d", key, got, n)
		}
	}

	if err := session.Save(); err != nil {
		t.Fatal(err)
	}
	data, err := store.Get("session:" + session.ID)
	if err != nil {
		t.Fatal(err)
	}
	var loaded KVSession
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatal(err)
	}
	for key, n := range want {
		if got := loaded.GetInt(key); got != n {
			t.Errorf("GetInt(%q) after a round trip = %d, want %d", key, got, n)
		}
	}
	if got := session.GetInt("missing"); got != 0 {
		t.Errorf("GetInt(missing) = %d, want 0", got)
	}
	session.Set("text", "12")
	if got := session.GetInt("text"); got != 0 {
		t.Errorf("GetInt(text) = %d, want 0 for a non-number", got)
	}
}

// Removing an expired key reports a failed write instead of dropping it.
func TestBBoltDeleteExpiredReportsFailure(t *testing.T) {
	store, err := NewBBoltAdvancedStore(filepath.Join(t.TempDir(), "kv.db"), "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetTTL("gone", []byte("v"), -time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := store.deleteExpired("gone"); err != nil {
		t.Fatalf("deleteExpired on an open store: %v", err)
	}
	if err := store.SetTTL("gone", []byte("v"), -time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := store.db.Close(); err != nil {
		t.Fatal(err)
	}
	store.closed.Store(true) // the file is closed; keep Close from closing it again
	if err := store.deleteExpired("gone"); err == nil {
		t.Fatal("deleteExpired on a closed database returned nil")
	}
}
