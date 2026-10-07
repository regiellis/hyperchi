package database

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"log"
	"strings"
	"testing"
)

// seedUsers creates a users table with three rows and returns the db.
func seedUsers(t *testing.T) *DB {
	t.Helper()
	db := newTestDB(t)
	if err := db.AutoMigrate(
		`CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT NOT NULL, age INTEGER, bio BLOB)`,
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	for _, u := range []map[string]any{
		{"name": "Ada", "age": 36},
		{"name": "Grace", "age": 45},
		{"name": "Lin", "age": 29},
	} {
		if _, err := db.Create("users", u); err != nil {
			t.Fatalf("seed %v: %v", u, err)
		}
	}
	return db
}

func TestCreateReturnsSequentialIDsAndStoresValues(t *testing.T) {
	db := seedUsers(t)

	id, err := db.Create("users", map[string]any{"name": "Hedy", "age": 41, "bio": []byte("inventor")})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if id != 4 {
		t.Fatalf("id = %d, want 4", id)
	}

	row, err := db.GetMap("SELECT name, age, bio FROM users WHERE id = ?", id)
	if err != nil {
		t.Fatalf("GetMap: %v", err)
	}
	if row["name"] != "Hedy" || row["age"] != int64(41) {
		t.Fatalf("row = %#v", row)
	}
	// []byte columns come back as strings so templates can print them.
	if row["bio"] != "inventor" {
		t.Fatalf("bio = %#v (%T), want string \"inventor\"", row["bio"], row["bio"])
	}
}

func TestCreateAndUpdateRejectEmptyData(t *testing.T) {
	db := seedUsers(t)
	for name, data := range map[string]map[string]any{"nil": nil, "empty": {}} {
		t.Run(name, func(t *testing.T) {
			if _, err := db.Create("users", data); err == nil {
				t.Error("Create accepted no data")
			}
			if err := db.Update("users", 1, data); err == nil {
				t.Error("Update accepted no data")
			}
		})
	}
}

func TestCreateSurfacesConstraintErrors(t *testing.T) {
	db := seedUsers(t)
	if _, err := db.Create("users", map[string]any{"age": 1}); err == nil {
		t.Fatal("NOT NULL violation was not reported")
	}
	if _, err := db.Create("missing", map[string]any{"name": "x"}); err == nil {
		t.Fatal("insert into a missing table was not reported")
	}
}

func TestUpdateTouchesOnlyTheTargetRow(t *testing.T) {
	db := seedUsers(t)
	if err := db.Update("users", 2, map[string]any{"name": "Grace H", "age": 46}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	rows, err := db.List("SELECT id, name, age FROM users ORDER BY id")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []struct {
		name string
		age  int64
	}{{"Ada", 36}, {"Grace H", 46}, {"Lin", 29}}
	if len(rows) != len(want) {
		t.Fatalf("got %d rows, want %d", len(rows), len(want))
	}
	for i, w := range want {
		if rows[i]["name"] != w.name || rows[i]["age"] != w.age {
			t.Errorf("row %d = %#v, want %s/%d", i, rows[i], w.name, w.age)
		}
	}
}

func TestDeleteMissingIDIsNotAnError(t *testing.T) {
	db := seedUsers(t)
	if err := db.Delete("users", 999); err != nil {
		t.Fatalf("Delete of a missing id: %v", err)
	}
	n, err := db.Count("users", "")
	if err != nil || n != 3 {
		t.Fatalf("Count = %d, %v; want 3", n, err)
	}
}

func TestCountAndExists(t *testing.T) {
	db := seedUsers(t)
	cases := []struct {
		name   string
		where  string
		args   []any
		count  int
		exists bool
	}{
		{"all rows", "", nil, 3, true},
		{"one match", "name = ?", []any{"Ada"}, 1, true},
		{"range", "age > ?", []any{30}, 2, true},
		{"two params", "age > ? AND age < ?", []any{30, 40}, 1, true},
		{"no match", "name = ?", []any{"nobody"}, 0, false},
		// A value that looks like SQL is bound, never interpolated.
		{"bound injection text", "name = ?", []any{"' OR 1=1 --"}, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n, err := db.Count("users", tc.where, tc.args...)
			if err != nil || n != tc.count {
				t.Fatalf("Count = %d, %v; want %d", n, err, tc.count)
			}
			ok, err := db.Exists("users", tc.where, tc.args...)
			if err != nil || ok != tc.exists {
				t.Fatalf("Exists = %v, %v; want %v", ok, err, tc.exists)
			}
		})
	}
	if _, err := db.Count("users", "no_such_column = ?", 1); err == nil {
		t.Error("Count with a bad where clause returned no error")
	}
	if _, err := db.Exists("users --", ""); err == nil {
		t.Error("Exists accepted an unsafe table name")
	}
}

func TestGetScansFirstRowAndReportsNoRows(t *testing.T) {
	db := seedUsers(t)

	var name string
	if err := db.Get(&name, "SELECT name FROM users WHERE age = ?", 45); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if name != "Grace" {
		t.Fatalf("name = %q", name)
	}

	err := db.Get(&name, "SELECT name FROM users WHERE id = ?", 999)
	if !errors.Is(err, ErrNoRows) || !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("Get on no rows = %v, want ErrNoRows", err)
	}
	if _, err := db.GetMap("SELECT * FROM users WHERE id = ?", 999); !errors.Is(err, ErrNoRows) {
		t.Fatalf("GetMap on no rows = %v, want ErrNoRows", err)
	}

	if err := db.Get(&name, "SELECT nope FROM users"); err == nil {
		t.Fatal("Get with an invalid query returned no error")
	}
	if _, err := db.GetMap("SELECT nope FROM users"); err == nil {
		t.Fatal("GetMap with an invalid query returned no error")
	}
	if _, err := db.List("SELECT nope FROM users"); err == nil {
		t.Fatal("List with an invalid query returned no error")
	}
}

// overflowQuery prepares fine but fails on its first step: abs() of the
// smallest int64 raises "integer overflow". The error surfaces from
// rows.Next/rows.Err, not from Query.
const overflowQuery = "SELECT abs(-9223372036854775807 - 1)"

// TestRowReadersDoNotMaskStepErrorsAsNoRows checks that a query failing while
// stepping is not reported as "not found". Callers branch on ErrNoRows (404 vs
// 500), so masking a real failure as a missing row hides it.
func TestRowReadersDoNotMaskStepErrorsAsNoRows(t *testing.T) {
	db := newTestDB(t)

	check := func(t *testing.T, err error) {
		t.Helper()
		if err == nil || errors.Is(err, ErrNoRows) {
			t.Fatalf("got %v, want the overflow error", err)
		}
		if !strings.Contains(err.Error(), "overflow") {
			t.Fatalf("got %v, want the overflow error", err)
		}
	}

	t.Run("DB.Get", func(t *testing.T) {
		var n int64
		check(t, db.Get(&n, overflowQuery))
	})
	t.Run("DB.GetMap", func(t *testing.T) {
		_, err := db.GetMap(overflowQuery)
		check(t, err)
	})
	t.Run("DB.List", func(t *testing.T) {
		_, err := db.List(overflowQuery)
		check(t, err)
	})
	t.Run("Tx.Get", func(t *testing.T) {
		err := db.Tx(func(tx *Tx) error {
			var n int64
			return tx.Get(&n, overflowQuery)
		})
		check(t, err)
	})
	t.Run("Tx.GetMap", func(t *testing.T) {
		err := db.Tx(func(tx *Tx) error {
			_, err := tx.GetMap(overflowQuery)
			return err
		})
		check(t, err)
	})
	t.Run("GetStruct", func(t *testing.T) {
		_, err := GetStruct[struct{ N int64 }](db, overflowQuery)
		check(t, err)
	})
	t.Run("GetStructTx", func(t *testing.T) {
		err := db.Tx(func(tx *Tx) error {
			_, err := GetStructTx[struct{ N int64 }](tx, overflowQuery)
			return err
		})
		check(t, err)
	})
}

func TestListReturnsNilSliceForNoRows(t *testing.T) {
	db := seedUsers(t)
	rows, err := db.List("SELECT * FROM users WHERE id < 0")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("rows = %v, want none", rows)
	}
}

func TestQueryAndExecPassThrough(t *testing.T) {
	db := seedUsers(t)

	res, err := db.Exec("UPDATE users SET age = age + 1 WHERE age < ?", 40)
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if n, err := res.RowsAffected(); err != nil || n != 2 {
		t.Fatalf("RowsAffected = %d, %v; want 2", n, err)
	}

	var age int
	if err := db.QueryRow("SELECT age FROM users WHERE name = ?", "Ada").Scan(&age); err != nil || age != 37 {
		t.Fatalf("QueryRow age = %d, %v; want 37", age, err)
	}

	rows, err := db.Query("SELECT name FROM users ORDER BY name")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatalf("Scan: %v", err)
		}
		names = append(names, n)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err: %v", err)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("rows.Close: %v", err)
	}
	if strings.Join(names, ",") != "Ada,Grace,Lin" {
		t.Fatalf("names = %v", names)
	}
}

func TestContextVariantsHonorCancellation(t *testing.T) {
	db := seedUsers(t)
	ctx := context.Background()

	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("PingContext: %v", err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE users SET age = ? WHERE id = ?", 50, 1); err != nil {
		t.Fatalf("ExecContext: %v", err)
	}
	var age int
	if err := db.QueryRowContext(ctx, "SELECT age FROM users WHERE id = ?", 1).Scan(&age); err != nil || age != 50 {
		t.Fatalf("QueryRowContext = %d, %v; want 50", age, err)
	}
	rows, err := db.QueryContext(ctx, "SELECT id FROM users")
	if err != nil {
		t.Fatalf("QueryContext: %v", err)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("rows.Close: %v", err)
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := db.PingContext(cancelled); !errors.Is(err, context.Canceled) {
		t.Errorf("PingContext(cancelled) = %v, want context.Canceled", err)
	}
	if _, err := db.ExecContext(cancelled, "UPDATE users SET age = 0"); !errors.Is(err, context.Canceled) {
		t.Errorf("ExecContext(cancelled) = %v, want context.Canceled", err)
	}
	if _, err := db.QueryContext(cancelled, "SELECT id FROM users"); !errors.Is(err, context.Canceled) {
		t.Errorf("QueryContext(cancelled) = %v, want context.Canceled", err)
	}
	if err := db.QueryRowContext(cancelled, "SELECT 1").Scan(&age); !errors.Is(err, context.Canceled) {
		t.Errorf("QueryRowContext(cancelled) = %v, want context.Canceled", err)
	}

	// The cancelled update never ran.
	if n, err := db.Count("users", "age = 0"); err != nil || n != 0 {
		t.Fatalf("cancelled ExecContext changed rows: n=%d err=%v", n, err)
	}
}

func TestDebugLogsQueriesOnlyWhenEnabled(t *testing.T) {
	db := seedUsers(t)
	var buf bytes.Buffer
	db.logger = log.New(&buf, "", 0)

	if _, err := db.Exec("SELECT ?", 1); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Fatalf("logged with Debug off: %q", buf.String())
	}

	db.config.Debug = true
	if _, err := db.Exec("SELECT ?", 7); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); !strings.Contains(got, "Query: SELECT ? [7]") {
		t.Fatalf("debug log = %q", got)
	}
	// The line is written before the query runs, so it must not claim a
	// duration.
	if got := buf.String(); strings.Contains(got, "took") {
		t.Fatalf("debug log reports a duration it cannot measure: %q", got)
	}
}

func TestPingStatsRawAndClose(t *testing.T) {
	db := seedUsers(t)
	if err := db.Ping(); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if db.Raw() == nil {
		t.Fatal("Raw returned nil")
	}
	stats := db.Stats()
	if stats["max_open_connections"] != 10 {
		t.Fatalf("max_open_connections = %v, want 10", stats["max_open_connections"])
	}
	for _, key := range []string{"open_connections", "in_use", "idle", "wait_count", "wait_duration",
		"max_idle_closed", "max_idle_time_closed", "max_lifetime_closed"} {
		if _, ok := stats[key]; !ok {
			t.Errorf("Stats missing %q", key)
		}
	}

	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := db.Ping(); err == nil {
		t.Fatal("Ping after Close succeeded")
	}
	if err := (&DB{}).Close(); err != nil {
		t.Fatalf("Close on a DB with no connection: %v", err)
	}
}
