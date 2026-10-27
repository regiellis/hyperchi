package database

import (
	"path/filepath"
	"strings"
	"testing"
)

// newTestDB opens a real file-backed SQLite database in a temp dir.
func newTestDB(t *testing.T) *DB {
	t.Helper()
	cfg := DefaultDBConfig()
	cfg.Path = filepath.Join(t.TempDir(), "test.db")
	db, err := NewDB(cfg)
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// TestConnectionPragmasApplied checks the PocketBase-style PRAGMAs take
// effect. A DSN the driver does not understand is ignored silently, which
// leaves journal_mode=delete and foreign_keys=0.
func TestConnectionPragmasApplied(t *testing.T) {
	db := newTestDB(t)

	var journalMode string
	if err := db.conn.QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatalf("read journal_mode: %v", err)
	}
	if !strings.EqualFold(journalMode, "wal") {
		t.Fatalf("journal_mode = %q, want wal", journalMode)
	}

	var fk int
	if err := db.conn.QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil {
		t.Fatalf("read foreign_keys: %v", err)
	}
	if fk != 1 {
		t.Fatalf("foreign_keys = %d, want 1", fk)
	}

	var busy int
	if err := db.conn.QueryRow("PRAGMA busy_timeout").Scan(&busy); err != nil {
		t.Fatalf("read busy_timeout: %v", err)
	}
	if busy != 30000 {
		t.Fatalf("busy_timeout = %d, want 30000", busy)
	}
}

// TestForeignKeysEnforced checks the foreign_keys PRAGMA is enforced, not
// only reported as set.
func TestForeignKeysEnforced(t *testing.T) {
	db := newTestDB(t)

	if err := db.AutoMigrate(
		`CREATE TABLE parent (id INTEGER PRIMARY KEY)`,
		`CREATE TABLE child (id INTEGER PRIMARY KEY, parent_id INTEGER REFERENCES parent(id))`,
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Insert a child referencing a non-existent parent: must fail with FKs on.
	if _, err := db.Exec(`INSERT INTO child (parent_id) VALUES (999)`); err == nil {
		t.Fatal("expected foreign key constraint violation, got nil — FKs not enforced")
	}
}

// TestPragmasAppliedAcrossPool checks the DSN PRAGMAs reach every pooled
// connection, not just the first. foreign_keys is per-connection state.
func TestPragmasAppliedAcrossPool(t *testing.T) {
	db := newTestDB(t)
	db.conn.SetMaxOpenConns(4)

	for i := 0; i < 20; i++ {
		var fk int
		if err := db.conn.QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil {
			t.Fatalf("read foreign_keys: %v", err)
		}
		if fk != 1 {
			t.Fatalf("foreign_keys = %d on iteration %d, want 1", fk, i)
		}
	}
}

// TestCRUDHappyPath exercises Create/Update/Count/Delete with valid identifiers.
func TestCRUDHappyPath(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(`CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT, age INTEGER)`); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	id, err := db.Create("users", map[string]any{"name": "Ada", "age": 36})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if id <= 0 {
		t.Fatalf("Create returned id %d", id)
	}

	if err := db.Update("users", id, map[string]any{"age": 37}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	n, err := db.Count("users", "age = ?", 37)
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if n != 1 {
		t.Fatalf("Count = %d, want 1", n)
	}

	if err := db.Delete("users", id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	exists, err := db.Exists("users", "id = ?", id)
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if exists {
		t.Fatal("record should have been deleted")
	}
}

// TestWriteHelpersRejectBadIdentifiers checks that table and column names
// that are not safe identifiers (a SQL-injection vector when keys come from
// request bodies) are rejected before reaching the database.
func TestWriteHelpersRejectBadIdentifiers(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(`CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT)`); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	bad := "users; DROP TABLE users--"

	if _, err := db.Create(bad, map[string]any{"name": "x"}); err == nil {
		t.Error("Create accepted an unsafe table name")
	}
	if _, err := db.Create("users", map[string]any{"name; DROP TABLE users--": "x"}); err == nil {
		t.Error("Create accepted an unsafe column name")
	}
	if err := db.Update(bad, 1, map[string]any{"name": "x"}); err == nil {
		t.Error("Update accepted an unsafe table name")
	}
	if err := db.Update("users", 1, map[string]any{"name)=1,(x": "x"}); err == nil {
		t.Error("Update accepted an unsafe column name")
	}
	if err := db.Delete(bad, 1); err == nil {
		t.Error("Delete accepted an unsafe table name")
	}
	if _, err := db.Count(bad, ""); err == nil {
		t.Error("Count accepted an unsafe table name")
	}

	// The table must still exist: no injected statement ran.
	var n int
	if err := db.conn.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='users'`).Scan(&n); err != nil {
		t.Fatalf("check table: %v", err)
	}
	if n != 1 {
		t.Fatal("users table missing — an injection may have executed")
	}
}

// TestBackupHandlesQuoteInPath checks the VACUUM INTO escaping: a path
// containing a single quote must not break out of the SQL string literal.
func TestBackupHandlesQuoteInPath(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(`CREATE TABLE t (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	backupPath := filepath.Join(t.TempDir(), "back'up.db")
	if err := NewBackup(db).Create(backupPath); err != nil {
		t.Fatalf("Backup.Create with quoted path: %v", err)
	}
}
