package hyperchi

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
)

// UseKVStore registers store under name. A name already in use is an error.
// CleanupKVStores closes it.
func (g *HyperChi) UseKVStore(name string, store KVStore) error {
	return g.kvRegistry.Register(name, store)
}

// UseKVStoreBBolt opens a BBoltStore (see NewBBoltStore) and registers it.
func (g *HyperChi) UseKVStoreBBolt(name, dbPath, bucketName string) error {
	store, err := NewBBoltStore(dbPath, bucketName)
	if err != nil {
		return fmt.Errorf("failed to create BBolt store: %w", err)
	}
	return g.UseKVStore(name, store)
}

// UseKVStoreBBoltAdvanced opens a BBoltAdvancedStore and registers it.
func (g *HyperChi) UseKVStoreBBoltAdvanced(name, dbPath, bucketName string) error {
	store, err := NewBBoltAdvancedStore(dbPath, bucketName)
	if err != nil {
		return fmt.Errorf("failed to create advanced BBolt store: %w", err)
	}
	return g.UseKVStore(name, store)
}

// UseKVStoreMemory registers a new MemoryStore.
func (g *HyperChi) UseKVStoreMemory(name string) error {
	store := NewMemoryStore()
	return g.UseKVStore(name, store)
}

// UseKVStoreMemoryAdvanced registers a new MemoryAdvancedStore.
func (g *HyperChi) UseKVStoreMemoryAdvanced(name string) error {
	store := NewMemoryAdvancedStore()
	return g.UseKVStore(name, store)
}

// KV returns the store registered as name, or nil (and a log line) when there
// is none.
func (g *HyperChi) KV(name string) KVStore {
	store, err := g.kvRegistry.Get(name)
	if err != nil {
		log.Printf("hyperchi: KV store %q: %v", name, err)
		return nil
	}
	return store
}

// KVHelper returns a KVHelper for the store registered as name, or nil when
// there is none.
func (g *HyperChi) KVHelper(name string) *KVHelper {
	store := g.KV(name)
	if store == nil {
		return nil
	}
	return NewKVHelper(store)
}

// UseSessions creates the app's session store on the registered store
// config.Backend. Install app.SessionMiddleware() afterwards.
func (g *HyperChi) UseSessions(config KVSessionConfig) error {
	sessionStore, err := NewKVSessionStore(g.kvRegistry, config)
	if err != nil {
		return fmt.Errorf("failed to create KV session store: %w", err)
	}

	g.kvSessions = sessionStore
	return nil
}

// UseKVCache creates the app's KVCache on the registered store
// config.Backend.
func (g *HyperChi) UseKVCache(config KVCacheConfig) error {
	cache, err := NewKVCache(g.kvRegistry, config)
	if err != nil {
		return fmt.Errorf("failed to create KV cache: %w", err)
	}

	g.kvCache = cache
	return nil
}

// KVCache returns the cache UseKVCache created, or nil.
func (g *HyperChi) KVCache() *KVCache {
	return g.kvCache
}

// Sessions returns the session store UseSessions created, or nil.
func (g *HyperChi) Sessions() *KVSessionStore {
	return g.kvSessions
}

// SessionMiddleware returns middleware for KV-backed sessions. Call
// UseSessions first: without a session store it panics, because routes
// behind it would run with no session (c.Session() nil) and session-backed
// login and logout would silently do nothing.
func (g *HyperChi) SessionMiddleware() func(http.Handler) http.Handler {
	if g.kvSessions == nil {
		panic("hyperchi: SessionMiddleware: no session store configured; call app.UseSessions first")
	}
	return g.kvSessions.SessionMiddleware()
}

// GetSession returns the request's session, or nil outside SessionMiddleware.
func (g *HyperChi) GetSession(r *http.Request) *KVSession {
	return GetSessionFromContext(r)
}

// autoBBoltPath is where AutoSetupKVStores keeps the persistent store,
// relative to the working directory.
var autoBBoltPath = filepath.Join("data", "hyperchi.db")

// AutoSetupKVStores registers two stores: "memory" (in-memory) and "bbolt"
// (persistent, at data/hyperchi.db under the working directory, creating the
// data directory when missing). Any failure is returned; a store registered
// before the failure stays registered and is closed by CleanupKVStores.
func (g *HyperChi) AutoSetupKVStores() error {
	if err := g.UseKVStoreMemoryAdvanced("memory"); err != nil {
		return fmt.Errorf("hyperchi: set up memory KV store: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(autoBBoltPath), 0o750); err != nil {
		return fmt.Errorf("hyperchi: set up bbolt KV store: %w", err)
	}
	if err := g.UseKVStoreBBoltAdvanced("bbolt", autoBBoltPath, "default"); err != nil {
		return fmt.Errorf("hyperchi: set up bbolt KV store: %w", err)
	}
	return nil
}

// CleanupKVStores closes every registered store (see KVRegistry.Close).
func (g *HyperChi) CleanupKVStores() error {
	if g.kvRegistry != nil {
		return g.kvRegistry.Close()
	}
	return nil
}

// GetKVRegistry returns the app's store registry, for modules.
func (g *HyperChi) GetKVRegistry() *KVRegistry {
	return g.kvRegistry
}
