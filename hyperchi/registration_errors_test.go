package hyperchi

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/regiellis/hyperchi/hyperchi/config"
	"github.com/regiellis/hyperchi/hyperchi/extensions"
	"github.com/regiellis/hyperchi/hyperchi/extensions/sse"
)

func noDBApp(t *testing.T) *HyperChi {
	t.Helper()
	cfg := config.Default()
	cfg.Database.Disabled = true
	app, err := NewWithConfigE(cfg)
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	return app
}

// A built-in extension that fails to register is an error the caller sees,
// not a log line that leaves the app running without SSE.
func TestExtensionRegistrationFailureIsReturned(t *testing.T) {
	app := noDBApp(t)
	registry := extensions.NewRegistry()
	if err := registry.Register(sse.NewSSEExtension()); err != nil {
		t.Fatalf("pre-register sse: %v", err)
	}

	ei, err := newExtensionIntegration(app, registry)
	if err == nil {
		t.Fatal("expected an error when the SSE extension cannot register")
	}
	if ei != nil {
		t.Fatal("expected no integration alongside the error")
	}
	if !strings.Contains(err.Error(), "SSE") {
		t.Fatalf("error does not name the extension: %v", err)
	}
}

func TestNewExtensionIntegrationRegistersCoreExtensions(t *testing.T) {
	app := noDBApp(t)
	ei, err := NewExtensionIntegration(app)
	if err != nil {
		t.Fatalf("NewExtensionIntegration: %v", err)
	}
	if ei.GetSSEHub() == nil || ei.GetWebSocketHub() == nil || ei.GetPatternManager() == nil {
		t.Fatal("core extensions were not set up")
	}
	if got := len(ei.GetRegistry().GetInfo()); got != 3 {
		t.Fatalf("registered %d extensions, want 3", got)
	}
}

// AutoSetupKVStores returns a failed bbolt store, so an app that asks for
// persistence never runs silently without it.
func TestAutoSetupKVStoresReturnsBBoltFailure(t *testing.T) {
	t.Chdir(t.TempDir())
	// A regular file where the data directory belongs makes the store fail.
	if err := os.WriteFile("data", []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	app := noDBApp(t)
	t.Cleanup(func() {
		if err := app.CleanupKVStores(); err != nil {
			t.Errorf("cleanup: %v", err)
		}
	})
	if err := app.AutoSetupKVStores(); err == nil {
		t.Fatal("expected the bbolt failure to be returned")
	}
}

func TestAutoSetupKVStoresCreatesDataDirectory(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	app := noDBApp(t)
	t.Cleanup(func() {
		if err := app.CleanupKVStores(); err != nil {
			t.Errorf("cleanup: %v", err)
		}
	})
	if err := app.AutoSetupKVStores(); err != nil {
		t.Fatalf("AutoSetupKVStores: %v", err)
	}
	for _, name := range []string{"memory", "bbolt"} {
		if app.KV(name) == nil {
			t.Errorf("store %q not registered", name)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "data", "hyperchi.db")); err != nil {
		t.Fatalf("bbolt file not created: %v", err)
	}
}

// KVRegistry.Get wraps ErrStoreNotFound, so errors.Is matches the sentinel
// the package declares.
func TestKVRegistryGetWrapsErrStoreNotFound(t *testing.T) {
	registry := NewKVRegistry()
	_, err := registry.Get("missing")
	if !errors.Is(err, ErrStoreNotFound) {
		t.Fatalf("errors.Is(%v, ErrStoreNotFound) = false", err)
	}
	if !strings.Contains(err.Error(), `"missing"`) {
		t.Fatalf("error does not name the store: %v", err)
	}
}
