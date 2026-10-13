package main

import (
	"path/filepath"
	"testing"

	"github.com/regiellis/hyperchi/hyperchi/database"
)

// TestFTS5Available proves the framework's pure-Go SQLite driver
// (modernc.org/sqlite) is built with FTS5, bm25, and snippet before the index
// relies on them.
func TestFTS5Available(t *testing.T) {
	db, err := database.NewDB(&database.DBConfig{Path: filepath.Join(t.TempDir(), "fts.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := db.Exec(`CREATE VIRTUAL TABLE probe USING fts5(title, body)`); err != nil {
		t.Fatalf("FTS5 is not available in the SQLite driver: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO probe(title, body) VALUES ('nginx', 'reload the proxy'), ('dns', 'unbound resolver')`); err != nil {
		t.Fatal(err)
	}
	var title, snip string
	var rank float64
	err = db.QueryRow(`SELECT title, snippet(probe, 1, '[', ']', '…', 8), bm25(probe)
		FROM probe WHERE probe MATCH ? ORDER BY bm25(probe)`, `"prox"*`).Scan(&title, &snip, &rank)
	if err != nil {
		t.Fatalf("FTS5 query with bm25 and snippet failed: %v", err)
	}
	if title != "nginx" || snip != "reload the [proxy]" || rank >= 0 {
		t.Fatalf("got title=%q snippet=%q rank=%v", title, snip, rank)
	}
}
