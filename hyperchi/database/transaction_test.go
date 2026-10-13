package database

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"
	"testing"
)

func countUsers(t *testing.T, db *DB) int {
	t.Helper()
	n, err := db.Count("users", "")
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	return n
}

func TestTxCommitsOnSuccess(t *testing.T) {
	db := seedUsers(t)
	var newID int64
	err := db.Tx(func(tx *Tx) error {
		id, err := tx.Create("users", map[string]any{"name": "Hedy", "age": 41})
		if err != nil {
			return err
		}
		newID = id
		// Writes are visible inside the transaction before commit.
		n, err := tx.Count("users", "name = ?", "Hedy")
		if err != nil || n != 1 {
			return fmt.Errorf("in-tx count = %d, %w", n, err)
		}
		return tx.Update("users", id, map[string]any{"age": 42})
	})
	if err != nil {
		t.Fatalf("Tx: %v", err)
	}
	var age int
	if err := db.Get(&age, "SELECT age FROM users WHERE id = ?", newID); err != nil || age != 42 {
		t.Fatalf("committed age = %d, %v; want 42", age, err)
	}
}

func TestTxRollsBackOnError(t *testing.T) {
	db := seedUsers(t)
	sentinel := errors.New("abort")
	err := db.Tx(func(tx *Tx) error {
		if _, err := tx.Create("users", map[string]any{"name": "Ghost"}); err != nil {
			return err
		}
		if err := tx.Delete("users", 1); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("Tx error = %v, want the callback's error", err)
	}
	if n := countUsers(t, db); n != 3 {
		t.Fatalf("users = %d after rollback, want 3", n)
	}
	if ok, err := db.Exists("users", "name = ?", "Ghost"); err != nil || ok {
		t.Fatalf("rolled-back insert visible: %v, %v", ok, err)
	}
}

func TestTxRollsBackAndRepanicsOnPanic(t *testing.T) {
	db := seedUsers(t)
	var buf bytes.Buffer
	db.logger = log.New(&buf, "", 0)

	func() {
		defer func() {
			if r := recover(); r != "boom" {
				t.Fatalf("recovered %v, want the original panic value", r)
			}
		}()
		err := db.Tx(func(tx *Tx) error {
			if _, err := tx.Create("users", map[string]any{"name": "Ghost"}); err != nil {
				return err
			}
			panic("boom")
		})
		t.Fatalf("Tx returned %v instead of re-panicking", err)
	}()

	if n := countUsers(t, db); n != 3 {
		t.Fatalf("users = %d after panic, want 3 (rolled back)", n)
	}
	if buf.Len() != 0 {
		t.Fatalf("rollback after panic logged a failure: %q", buf.String())
	}
	// The pool is usable afterwards: the panicking tx released its connection.
	if err := db.Tx(func(tx *Tx) error { return tx.Delete("users", 3) }); err != nil {
		t.Fatalf("Tx after panic: %v", err)
	}
}

func TestTxSurfacesCommitErrors(t *testing.T) {
	db := seedUsers(t)
	var buf bytes.Buffer
	db.logger = log.New(&buf, "", 0)
	// Committing inside the callback leaves nothing for Tx to commit; the
	// second commit must be reported, not swallowed.
	err := db.Tx(func(tx *Tx) error { return tx.Commit() })
	if !errors.Is(err, sql.ErrTxDone) {
		t.Fatalf("Tx after an explicit commit = %v, want sql.ErrTxDone", err)
	}
	err = db.Tx(func(tx *Tx) error {
		if _, err := tx.Create("users", map[string]any{"name": "Gone"}); err != nil {
			return err
		}
		return tx.Rollback()
	})
	if !errors.Is(err, sql.ErrTxDone) {
		t.Fatalf("Tx after an explicit rollback = %v, want sql.ErrTxDone", err)
	}
	if ok, err := db.Exists("users", "name = ?", "Gone"); err != nil || ok {
		t.Fatalf("explicitly rolled-back insert visible: %v, %v", ok, err)
	}
	// The transaction was already over, so the cleanup rollback has nothing
	// to undo; that is not a rollback failure worth logging.
	if buf.Len() != 0 {
		t.Fatalf("spurious rollback log: %q", buf.String())
	}
}

func TestTxContext(t *testing.T) {
	db := seedUsers(t)

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	err := db.TxContext(cancelled, func(*Tx) error { called = true; return nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("TxContext(cancelled) = %v, want context.Canceled", err)
	}
	if called {
		t.Fatal("callback ran for a transaction that never began")
	}

	// Cancelling mid-transaction rolls it back.
	ctx, cancelMid := context.WithCancel(context.Background())
	err = db.TxContext(ctx, func(tx *Tx) error {
		if _, err := tx.Create("users", map[string]any{"name": "Midway"}); err != nil {
			return err
		}
		cancelMid()
		return nil
	})
	if err == nil {
		t.Fatal("TxContext committed after its context was cancelled")
	}
	if ok, err := db.Exists("users", "name = ?", "Midway"); err != nil || ok {
		t.Fatalf("insert from a cancelled tx visible: %v, %v", ok, err)
	}
}

func TestTxReadHelpers(t *testing.T) {
	db := seedUsers(t)
	err := db.Tx(func(tx *Tx) error {
		var name string
		if err := tx.Get(&name, "SELECT name FROM users WHERE id = ?", 2); err != nil || name != "Grace" {
			return fmt.Errorf("Get = %q, %w", name, err)
		}
		if err := tx.Get(&name, "SELECT name FROM users WHERE id = ?", 99); !errors.Is(err, ErrNoRows) {
			return fmt.Errorf("Get missing = %w, want ErrNoRows", err)
		}
		if err := tx.Get(&name, "SELECT nope FROM users"); err == nil {
			return errors.New("Get with a bad query returned no error")
		}

		row, err := tx.GetMap("SELECT id, name FROM users WHERE id = ?", 3)
		if err != nil || row["name"] != "Lin" || row["id"] != int64(3) {
			return fmt.Errorf("GetMap = %#v, %w", row, err)
		}
		if _, err := tx.GetMap("SELECT * FROM users WHERE id = 99"); !errors.Is(err, ErrNoRows) {
			return fmt.Errorf("GetMap missing = %w, want ErrNoRows", err)
		}
		if _, err := tx.GetMap("SELECT nope FROM users"); err == nil {
			return errors.New("GetMap with a bad query returned no error")
		}

		rows, err := tx.List("SELECT name FROM users WHERE age > ? ORDER BY name", 30)
		if err != nil || len(rows) != 2 || rows[0]["name"] != "Ada" || rows[1]["name"] != "Grace" {
			return fmt.Errorf("List = %v, %w", rows, err)
		}
		if _, err := tx.List("SELECT nope FROM users"); err == nil {
			return errors.New("List with a bad query returned no error")
		}

		var age int
		if err := tx.QueryRow("SELECT age FROM users WHERE name = ?", "Lin").Scan(&age); err != nil || age != 29 {
			return fmt.Errorf("QueryRow = %d, %w", age, err)
		}
		q, err := tx.Query("SELECT id FROM users")
		if err != nil {
			return err
		}
		if err := q.Close(); err != nil {
			return err
		}

		ok, err := tx.Exists("users", "age > ?", 40)
		if err != nil || !ok {
			return fmt.Errorf("Exists = %v, %w", ok, err)
		}
		ok, err = tx.Exists("users", "age > ?", 100)
		if err != nil || ok {
			return fmt.Errorf("Exists none = %v, %w", ok, err)
		}
		if _, err := tx.Create("users", nil); err == nil {
			return errors.New("tx.Create accepted no data")
		}
		if err := tx.Update("users", 1, map[string]any{}); err == nil {
			return errors.New("tx.Update accepted no data")
		}
		if _, err := tx.Create("missing", map[string]any{"name": "x"}); err == nil {
			return errors.New("tx.Create into a missing table returned no error")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestTxDebugLogReportsTheQuery checks the transaction logger reports the
// query and claims no duration: it runs before the query, so it cannot
// measure one.
func TestTxDebugLogReportsTheQuery(t *testing.T) {
	db := seedUsers(t)
	var buf bytes.Buffer
	db.logger = log.New(&buf, "", 0)

	if err := db.Tx(func(tx *Tx) error { _, err := tx.Exec("SELECT ?", 1); return err }); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Fatalf("logged with Debug off: %q", buf.String())
	}

	db.config.Debug = true
	if err := db.Tx(func(tx *Tx) error { _, err := tx.Exec("SELECT ?", 7); return err }); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	if !strings.Contains(got, "[TX] Query: SELECT ? [7]") {
		t.Fatalf("tx debug log = %q", got)
	}
	if strings.Contains(got, "took") {
		t.Fatalf("tx debug log claims an unmeasured duration: %q", got)
	}
}
