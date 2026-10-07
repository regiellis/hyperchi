package performance

import (
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/regiellis/hyperchi/hyperchi"
)

func openTestBadger(t *testing.T, config BadgerConfig) *BadgerAdvancedStore {
	t.Helper()
	store, err := NewBadgerAdvancedStore(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	return store
}

func TestBadgerBackupRestoresIntoNewStore(t *testing.T) {
	source := openTestBadger(t, DefaultBadgerConfig(t.TempDir()))
	for key, value := range map[string]string{"a": "alpha", "b": "beta"} {
		if err := source.Set(key, []byte(value)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := source.IncrementBy("count", 7); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "badger.bak")
	if err := source.Backup(path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() == 0 {
		t.Fatal("backup file is empty")
	}
	if leftovers, err := filepath.Glob(path + ".tmp-*"); err != nil || len(leftovers) != 0 {
		t.Fatalf("temporary files left: %v, %v", leftovers, err)
	}

	restored := openTestBadger(t, DefaultBadgerConfig(t.TempDir()))
	if err := restored.Restore(path); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"a": "alpha", "b": "beta"} {
		got, err := restored.Get(key)
		if err != nil || string(got) != want {
			t.Fatalf("%s = %q, %v; want %q", key, got, err, want)
		}
	}
	if count, err := restored.IncrementBy("count", 0); err != nil || count != 7 {
		t.Fatalf("count = %d, %v", count, err)
	}
}

func TestBadgerBackupToMissingDirectoryFails(t *testing.T) {
	store := openTestBadger(t, DefaultBadgerConfig(t.TempDir()))
	if err := store.Backup(filepath.Join(t.TempDir(), "missing", "badger.bak")); err == nil {
		t.Fatal("backup into a missing directory succeeded")
	}
}

// Run under -race: Close used to write the closed flag while other
// goroutines read it without synchronization.
func TestBadgerCloseWhileInUse(t *testing.T) {
	store, err := NewBadgerAdvancedStore(DefaultBadgerConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set("k", []byte("v")); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for {
				if _, err := store.Get("k"); err != nil {
					if !errors.Is(err, hyperchi.ErrStoreClosed) {
						t.Errorf("Get after close: %v", err)
					}
					return
				}
				store.Has("k")
			}
		}()
	}
	close(start)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	if err := store.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestBadgerIncrementRejectsNonCounter(t *testing.T) {
	store := openTestBadger(t, DefaultBadgerConfig(t.TempDir()))
	if err := store.Set("name", []byte("not a number")); err != nil {
		t.Fatal(err)
	}
	if n, err := store.IncrementBy("name", 1); err == nil {
		t.Fatalf("IncrementBy over a string returned %d, nil", n)
	}
	if err := store.Batch([]KVOperation{{Op: "INCREMENT", Key: "name", Delta: 1}}); err == nil {
		t.Fatal("Batch INCREMENT over a string succeeded")
	}
	if got, err := store.Get("name"); err != nil || string(got) != "not a number" {
		t.Fatalf("value changed: %q, %v", got, err)
	}
}

func TestBadgerCountersReadLegacyEncoding(t *testing.T) {
	store := openTestBadger(t, DefaultBadgerConfig(t.TempDir()))
	legacy := make([]byte, 8)
	binary.BigEndian.PutUint64(legacy, 1) // how IncrementBy stored counters before
	if err := store.Set("c", legacy); err != nil {
		t.Fatal(err)
	}
	if n, err := store.IncrementBy("c", 1); err != nil || n != 2 {
		t.Fatalf("IncrementBy = %d, %v", n, err)
	}
	if got, err := store.Get("c"); err != nil || string(got) != "2" {
		t.Fatalf("stored counter = %q, %v; want JSON 2", got, err)
	}
	if n, err := store.IncrementTTL("c", time.Hour); err != nil || n != 3 {
		t.Fatalf("IncrementTTL = %d, %v", n, err)
	}
}

func TestBadgerStatsCountLevelZeroTables(t *testing.T) {
	config := DefaultBadgerConfig(t.TempDir())
	config.CompactL0OnClose = false // leave the flushed memtable on level 0
	first, err := NewBadgerAdvancedStore(config)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"a", "b", "c"} {
		if err := first.Set(key, []byte(key)); err != nil {
			t.Fatal(err)
		}
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	reopened := openTestBadger(t, config)
	stats := reopened.GetStats()
	if stats.NumTables == 0 {
		t.Fatal("no tables after reopening")
	}
	if stats.NumTables0 == 0 || stats.NumTables0 > stats.NumTables {
		t.Fatalf("NumTables0 = %d of %d tables", stats.NumTables0, stats.NumTables)
	}
}

func TestBadgerCountersReadByKVHelper(t *testing.T) {
	var store hyperchi.KVStore = openTestBadger(t, DefaultBadgerConfig(t.TempDir()))
	if _, err := store.IncrementBy("visits", 5); err != nil {
		t.Fatal(err)
	}
	if n, err := hyperchi.NewKVHelper(store).GetInt("visits"); err != nil || n != 5 {
		t.Fatalf("KVHelper.GetInt = %d, %v; want 5", n, err)
	}
}

func TestBadgerThroughFrameworkInterfaces(t *testing.T) {
	var store hyperchi.AdvancedKVStore = openTestBadger(t, DefaultBadgerConfig(t.TempDir()))

	if err := store.SetTTL("session", []byte("data"), time.Hour); err != nil {
		t.Fatal(err)
	}
	if ttl, err := store.GetTTL("session"); err != nil || ttl <= 0 || ttl > time.Hour {
		t.Fatalf("GetTTL = %v, %v; want (0, 1h]", ttl, err)
	}

	if err := store.Set("gone", []byte("x")); err != nil {
		t.Fatal(err)
	}
	err := store.Batch([]hyperchi.KVOperation{
		{Op: "SET", Key: "a", Value: []byte("alpha")},
		{Op: "INCREMENT", Key: "n", Delta: 3},
		{Op: "DELETE", Key: "gone"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := store.Get("a"); err != nil || string(got) != "alpha" {
		t.Fatalf("a = %q, %v", got, err)
	}
	if n, err := hyperchi.NewKVHelper(store).GetInt("n"); err != nil || n != 3 {
		t.Fatalf("n = %d, %v; want 3", n, err)
	}
	if store.Has("gone") {
		t.Fatal("batch DELETE did not apply")
	}

	// The framework's cache stores with SetTTL and clears with DeletePrefix
	// when its store is a hyperchi.AdvancedKVStore.
	registry := hyperchi.NewKVRegistry()
	if err := registry.Register("badger", store); err != nil {
		t.Fatal(err)
	}
	cache, err := hyperchi.NewKVCache(registry, hyperchi.KVCacheConfig{Backend: "badger"})
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.SetTTL("page", "html", time.Minute); err != nil {
		t.Fatal(err)
	}
	if ttl, err := store.GetTTL("cache:page"); err != nil || ttl <= 0 || ttl > time.Minute {
		t.Fatalf("cache entry TTL = %v, %v; want (0, 1m]", ttl, err)
	}
	if err := cache.Clear(); err != nil {
		t.Fatal(err)
	}
	if count, err := store.CountPrefix("cache:"); err != nil || count != 0 {
		t.Fatalf("cache keys after Clear = %d, %v", count, err)
	}
}

// GetTTL follows the framework's stores: ErrKeyNotFound for a key with no
// expiry or no key at all, and the time left for a key set with SetTTL.
func TestGetTTLMatchesFrameworkStores(t *testing.T) {
	for name, kv := range performanceTestStores(t) {
		t.Run(name, func(t *testing.T) {
			store, ok := kv.(hyperchi.AdvancedKVStore)
			if !ok {
				t.Fatalf("%T is not a hyperchi.AdvancedKVStore", kv)
			}
			if err := store.Set("forever", []byte("v")); err != nil {
				t.Fatal(err)
			}
			if ttl, err := store.GetTTL("forever"); !errors.Is(err, hyperchi.ErrKeyNotFound) {
				t.Fatalf("GetTTL(no expiry) = %v, %v; want ErrKeyNotFound", ttl, err)
			}
			if ttl, err := store.GetTTL("missing"); !errors.Is(err, hyperchi.ErrKeyNotFound) {
				t.Fatalf("GetTTL(missing) = %v, %v; want ErrKeyNotFound", ttl, err)
			}
			if err := store.SetTTL("brief", []byte("v"), time.Hour); err != nil {
				t.Fatal(err)
			}
			if ttl, err := store.GetTTL("brief"); err != nil || ttl <= 0 || ttl > time.Hour {
				t.Fatalf("GetTTL(1h) = %v, %v; want (0, 1h]", ttl, err)
			}
		})
	}
}

// captureStderr points os.Stderr at a pipe until the returned function runs,
// which restores it and returns what was written.
func captureStderr(t *testing.T) func() string {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	original := os.Stderr
	os.Stderr = writer
	output := make(chan string, 1)
	go func() {
		data, err := io.ReadAll(reader)
		if err != nil {
			t.Error(err)
		}
		output <- string(data)
	}()
	return func() string {
		os.Stderr = original
		if err := writer.Close(); err != nil {
			t.Error(err)
		}
		got := <-output
		if err := reader.Close(); err != nil {
			t.Error(err)
		}
		return got
	}
}

func TestBadgerWritesNoInfoLinesToStderr(t *testing.T) {
	restore := captureStderr(t)
	store, err := NewBadgerAdvancedStore(DefaultBadgerConfig(t.TempDir()))
	if err != nil {
		restore()
		t.Fatal(err)
	}
	setErr := store.Set("k", []byte("v"))
	closeErr := store.Close()
	written := restore()
	if setErr != nil || closeErr != nil {
		t.Fatal(setErr, closeErr)
	}
	if written != "" {
		t.Fatalf("Badger wrote to stderr:\n%s", written)
	}
}
