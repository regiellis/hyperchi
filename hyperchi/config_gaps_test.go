package hyperchi

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/regiellis/hyperchi/hyperchi/config"
)

// NewProduction reads the environment like NewFromEnv, so SECRET_KEY and the
// rest of the deployment's settings apply, and forces production mode.
func TestNewProductionReadsEnvironment(t *testing.T) {
	t.Setenv("ENVIRONMENT", "development")
	t.Setenv("SECRET_KEY", strings.Repeat("k", 32))
	t.Setenv("DB_DISABLED", "true")
	t.Setenv("LOG_LEVEL", "debug")
	app := NewProduction()
	t.Cleanup(func() {
		if err := app.Close(); err != nil {
			t.Error(err)
		}
	})
	if !app.Config.IsProduction() {
		t.Errorf("environment %q, want production", app.Config.Server.Environment)
	}
	if app.Config.Security.SecretKey == "" || !app.Config.Database.Disabled {
		t.Error("NewProduction ignored SECRET_KEY or DB_DISABLED")
	}
	if app.Config.Development.LogLevel != "debug" || app.Config.Development.Debug {
		t.Errorf("log level %q debug %v", app.Config.Development.LogLevel, app.Config.Development.Debug)
	}
}

// An empty address serves on the configured host and port.
func TestServeEmptyAddressUsesConfig(t *testing.T) {
	cfg := config.Default()
	cfg.Database.Disabled = true
	cfg.Server.Host = "256.0.0.1" // unbindable, so the error names the address used
	cfg.Server.Port = 18999
	app := NewWithConfig(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := app.ServeContext(ctx, "")
	if err == nil || !strings.Contains(err.Error(), "256.0.0.1") {
		t.Fatalf("ServeContext(\"\") = %v, want a bind error for the configured address", err)
	}
}

// A bbolt store creates the directory its file lives in.
func TestBBoltStoreCreatesParentDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data", "nested", "kv.db")
	store, err := NewBBoltStore(path, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
}
