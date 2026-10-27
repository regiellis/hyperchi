package database

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLazyDBCreatesTheFileOnFirstQueryWithPragmas(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lazy.db")
	db, err := NewDB(&DBConfig{Path: path, MaxOpenConns: 1, MaxIdleConns: 1, Lazy: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("lazy NewDB touched the file before any query: %v", err)
	}

	if _, err := db.Exec("CREATE TABLE notes (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("first query did not create the file: %v", err)
	}
	if err := db.verifyPragmas(); err != nil {
		t.Fatalf("pragmas on a lazily created database: %v", err)
	}
	var pageSize, autoVacuum int
	if err := db.QueryRow("PRAGMA page_size").Scan(&pageSize); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("PRAGMA auto_vacuum").Scan(&autoVacuum); err != nil {
		t.Fatal(err)
	}
	if pageSize != 4096 || autoVacuum != 2 {
		t.Fatalf("page_size=%d auto_vacuum=%d, want 4096 and 2 (incremental)", pageSize, autoVacuum)
	}
}
