package database

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestEveryConnectionPragmaApplied checks each default NewDB puts in the DSN,
// not only the three verifyPragmas guards, on a fresh database.
func TestEveryConnectionPragmaApplied(t *testing.T) {
	db := newTestDB(t)
	want := map[string]string{
		"auto_vacuum":        "2",
		"page_size":          "4096",
		"cache_size":         "-64000",
		"temp_store":         "2",
		"mmap_size":          "268435456",
		"wal_autocheckpoint": "1000",
		"foreign_keys":       "1",
		"journal_mode":       "wal",
		"synchronous":        "1",
		"busy_timeout":       "30000",
	}
	for name, value := range want {
		var got string
		if err := db.Get(&got, "PRAGMA "+name); err != nil {
			t.Fatalf("PRAGMA %s: %v", name, err)
		}
		if !strings.EqualFold(got, value) {
			t.Errorf("PRAGMA %s = %s, want %s", name, got, value)
		}
	}
}

// TestDSNPragmaOverridesDefault checks a DSN that sets a PRAGMA, by _pragma or
// by shorthand key, keeps its own value instead of the default.
func TestDSNPragmaOverridesDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "override.db")
	db, err := NewDB(&DBConfig{Path: path + "?_pragma=cache_size(-2000)&_vacuum=NONE&_sync=FULL"})
	if err != nil {
		t.Fatal(err)
	}
	closeDB(t, db)
	if _, err := db.Exec("CREATE TABLE t (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	want := map[string]int{"cache_size": -2000, "auto_vacuum": 0, "synchronous": 2}
	for name, value := range want {
		var got int
		if err := db.Get(&got, "PRAGMA "+name); err != nil || got != value {
			t.Errorf("PRAGMA %s = %d, %v; want the DSN's %d", name, got, err, value)
		}
	}
}

// TestDSNThatDisablesGuardsFailsFast checks a DSN cannot quietly turn off WAL
// or foreign keys: the open fails instead.
func TestDSNThatDisablesGuardsFailsFast(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]string{
		"?_journal_mode=DELETE":         "journal_mode",
		"?_pragma=journal_mode(DELETE)": "journal_mode",
		"?_fk=0":                        "foreign_keys",
		"?_pragma=foreign_keys(0)":      "foreign_keys",
	}
	i := 0
	for query, want := range cases {
		i++
		path := filepath.Join(dir, strings.Repeat("x", i)+".db")
		db, err := NewDB(&DBConfig{Path: path + query})
		if err == nil {
			closeDB(t, db)
			t.Fatalf("NewDB(%s) opened", query)
		}
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("NewDB(%s) = %v, want a %s error", query, err, want)
		}
	}
}

// TestTimesUseSQLiteTextFormat checks time.Time values are stored in SQLite's
// text format, so datetime() comparisons and rows written by earlier releases
// keep working, and read back as time.Time from a DATETIME column.
func TestTimesUseSQLiteTextFormat(t *testing.T) {
	db := newTestDB(t)
	if _, err := db.Exec("CREATE TABLE ev (id INTEGER PRIMARY KEY, at DATETIME)"); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 5, 13, 4, 5, 0, time.UTC)
	if _, err := db.Create("ev", map[string]any{"at": at}); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := db.Get(&raw, "SELECT CAST(at AS TEXT) FROM ev"); err != nil {
		t.Fatal(err)
	}
	if raw != "2026-10-05 13:04:05+00:00" {
		t.Fatalf("stored time = %q, want SQLite text format", raw)
	}
	var got time.Time
	if err := db.Get(&got, "SELECT at FROM ev"); err != nil {
		t.Fatal(err)
	}
	if !got.Equal(at) {
		t.Fatalf("read back %v, want %v", got, at)
	}
	var later int
	if err := db.Get(&later, "SELECT datetime(at) > '2026-10-05 13:00:00' FROM ev"); err != nil || later != 1 {
		t.Fatalf("datetime(at) comparison = %d, %v", later, err)
	}
}

func TestBuildDSN(t *testing.T) {
	dsn, err := buildDSN("app.db?_time_format=datetime&_pragma=Busy_Timeout = 9&_txlock=immediate", 1500*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	base, query, _ := strings.Cut(dsn, "?")
	if base != "app.db" {
		t.Fatalf("base = %q", base)
	}
	for _, want := range []string{"_busy_timeout=1500", "_time_format=datetime", "_txlock=immediate", "_journal_mode=WAL"} {
		if !strings.Contains(query, want) {
			t.Errorf("DSN %q lacks %q", dsn, want)
		}
	}
	if strings.Contains(strings.ToLower(query), "busy_timeout+%3d") || strings.Contains(query, "Busy_Timeout") {
		t.Errorf("DSN %q kept the caller's busy_timeout pragma", dsn)
	}

	dsn, err = buildDSN(":memory:", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dsn, "_busy_timeout=1&") || !strings.Contains(dsn, "_time_format=sqlite") {
		t.Fatalf("DSN %q: want a 1ms floor on busy_timeout and the sqlite time format", dsn)
	}
	if _, err := buildDSN("app.db?_time_integer_format=unix", time.Second); err != nil {
		t.Fatal(err)
	}
	if dsn, _ := buildDSN("app.db?_time_integer_format=unix", time.Second); strings.Contains(dsn, "_time_format") {
		t.Fatalf("DSN %q added _time_format next to _time_integer_format", dsn)
	}
}

func TestPragmaName(t *testing.T) {
	cases := map[string]string{
		"busy_timeout(5)":      "busy_timeout",
		" Journal_Mode = WAL ": "journal_mode",
		"foreign_keys":         "foreign_keys",
		"main.page_size(4096)": "main.page_size",
		"cache_size=-2000":     "cache_size",
	}
	for in, want := range cases {
		if got := pragmaName(in); got != want {
			t.Errorf("pragmaName(%q) = %q, want %q", in, got, want)
		}
	}
}
