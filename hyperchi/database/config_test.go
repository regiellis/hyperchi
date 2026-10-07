package database

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func closeDB(t *testing.T, db *DB) {
	t.Helper()
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})
}

func TestDefaultDBConfig(t *testing.T) {
	cfg := DefaultDBConfig()
	want := DBConfig{Path: "./app.db", Timeout: 30 * time.Second, MaxOpenConns: 10, MaxIdleConns: 5}
	if *cfg != want {
		t.Fatalf("DefaultDBConfig = %+v, want %+v", *cfg, want)
	}
}

func TestNewDBNilConfigUsesDefaults(t *testing.T) {
	t.Chdir(t.TempDir())
	db, err := NewDB(nil)
	if err != nil {
		t.Fatalf("NewDB(nil): %v", err)
	}
	closeDB(t, db)
	if _, err := os.Stat("app.db"); err != nil {
		t.Fatalf("default path not created: %v", err)
	}
	if db.Stats()["max_open_connections"] != 10 {
		t.Fatalf("stats = %v", db.Stats())
	}
}

func TestNewDBDoesNotAliasTheCallersConfig(t *testing.T) {
	cfg := &DBConfig{Path: filepath.Join(t.TempDir(), "a.db"), MaxOpenConns: 1}
	db, err := NewDB(cfg)
	if err != nil {
		t.Fatal(err)
	}
	closeDB(t, db)
	if cfg.Timeout != 0 {
		t.Fatalf("NewDB wrote its default timeout into the caller's config: %v", cfg.Timeout)
	}
	cfg.Debug = true
	if db.config.Debug {
		t.Fatal("a later change to the caller's config reached the open DB")
	}
	var busy int
	if err := db.Get(&busy, "PRAGMA busy_timeout"); err != nil || busy != 30000 {
		t.Fatalf("zero Timeout busy_timeout = %d, %v; want the 30s default", busy, err)
	}
}

func TestNewDBRejectsBadConfig(t *testing.T) {
	dir := t.TempDir()
	notADir := filepath.Join(dir, "file")
	if err := os.WriteFile(notADir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	garbage := filepath.Join(dir, "garbage.db")
	if err := os.WriteFile(garbage, []byte(strings.Repeat("not a database ", 100)), 0o600); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		cfg  DBConfig
		want string
	}{
		{"negative timeout", DBConfig{Path: filepath.Join(dir, "a.db"), Timeout: -time.Second}, "negative"},
		{"parent is a file", DBConfig{Path: filepath.Join(notADir, "sub", "a.db")}, "directory"},
		{"malformed DSN query", DBConfig{Path: filepath.Join(dir, "a.db") + "?%zz"}, "DSN"},
		{"path is a directory", DBConfig{Path: dir}, "ping"},
		{"lazy path is a directory", DBConfig{Path: dir, Lazy: true}, "is a directory"},
		{"not a database", DBConfig{Path: garbage}, "ping"},
		// Lazy only defers creating a file; an existing one is checked now.
		{"lazy existing non-database", DBConfig{Path: garbage, Lazy: true}, "ping"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := tc.cfg
			db, err := NewDB(&cfg)
			if err == nil {
				closeDB(t, db)
				t.Fatal("NewDB accepted the config")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestNewDBLazyWithAnExistingFileVerifiesUpFront(t *testing.T) {
	path := filepath.Join(t.TempDir(), "existing.db")
	first, err := NewDB(&DBConfig{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.Exec("CREATE TABLE notes (id INTEGER PRIMARY KEY, body TEXT)"); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := NewDB(&DBConfig{Path: path, Lazy: true})
	if err != nil {
		t.Fatalf("lazy NewDB on an existing file: %v", err)
	}
	closeDB(t, db)
	if _, err := db.Create("notes", map[string]any{"body": "kept"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
}

func TestNewDBKeepsDSNOptionsButTimeoutWins(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dsn.db")
	dsn := path + "?_pragma=secure_delete(1)&_pragma=busy_timeout(5)&_busy_timeout=6&_timeout=7"
	db, err := NewDB(&DBConfig{Path: dsn, Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	closeDB(t, db)
	var secureDelete, busy int
	if err := db.Get(&secureDelete, "PRAGMA secure_delete"); err != nil || secureDelete != 1 {
		t.Fatalf("secure_delete = %d, %v; want the DSN option applied", secureDelete, err)
	}
	if err := db.Get(&busy, "PRAGMA busy_timeout"); err != nil || busy != 2000 {
		t.Fatalf("busy_timeout = %d, %v; want Timeout (2000) over the DSN value", busy, err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("database file not at the path before '?': %v", err)
	}
}

func TestNewDBInMemory(t *testing.T) {
	// One connection: every pooled ":memory:" connection is its own database.
	db, err := NewDB(&DBConfig{Path: ":memory:", MaxOpenConns: 1, MaxIdleConns: 1})
	if err != nil {
		t.Fatalf("NewDB(:memory:): %v", err)
	}
	closeDB(t, db)
	if _, err := db.Exec("CREATE TABLE t (id INTEGER PRIMARY KEY, parent INTEGER REFERENCES t(id))"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO t (parent) VALUES (42)"); err == nil {
		t.Fatal("foreign keys not enforced on an in-memory database")
	}
}

func TestIsInMemory(t *testing.T) {
	cases := map[string]bool{
		":memory:":                    true,
		"file::memory:":               true,
		"file::memory:?cache=shared":  true,
		"file:test.db?mode=memory":    true,
		"./app.db":                    false,
		"memory.db":                   false,
		"/var/data/:memory:/x.db":     false,
		"file:app.db?cache=shared":    false,
		"file:app.db?mode=rwc&x=test": false,
	}
	for path, want := range cases {
		if got := isInMemory(path); got != want {
			t.Errorf("isInMemory(%q) = %v, want %v", path, got, want)
		}
	}
}

// TestVerifyPragmasDetectsMissingOptimizations opens connections through the
// plain driver, without the DSN NewDB builds, and checks the guard notices.
func TestVerifyPragmasDetectsMissingOptimizations(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name, dsn, want string
	}{
		{"foreign keys off", filepath.Join(dir, "fk.db"), "foreign_keys"},
		{"journal not WAL", filepath.Join(dir, "wal.db") + "?_foreign_keys=1", "journal_mode"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conn, err := sql.Open(sqliteDriverName, tc.dsn)
			if err != nil {
				t.Fatal(err)
			}
			db := &DB{conn: conn, config: &DBConfig{Path: tc.dsn}}
			closeDB(t, db)
			err = db.verifyPragmas()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("verifyPragmas = %v, want a %s error", err, tc.want)
			}
		})
	}
}
