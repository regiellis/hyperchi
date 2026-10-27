package database

import (
	"context"
	"errors"
	"testing"
)

func TestStructQueriesHonorTheContext(t *testing.T) {
	db := newTestDB(t)
	if _, err := db.Exec(`CREATE TABLE notes (id INTEGER PRIMARY KEY, body TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO notes (body) VALUES ('a'), ('b')`); err != nil {
		t.Fatal(err)
	}
	type note struct {
		ID   int    `db:"id"`
		Body string `db:"body"`
	}
	ctx := context.Background()
	one, err := GetStructContext[note](ctx, db, `SELECT id, body FROM notes WHERE id = ?`, 2)
	if err != nil || one.Body != "b" {
		t.Fatalf("GetStructContext = %+v, %v", one, err)
	}
	all, err := ListStructContext[note](ctx, db, `SELECT id, body FROM notes ORDER BY id`)
	if err != nil || len(all) != 2 {
		t.Fatalf("ListStructContext = %+v, %v", all, err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := GetStructContext[note](cancelled, db, `SELECT id, body FROM notes`); !errors.Is(err, context.Canceled) {
		t.Fatalf("GetStructContext with a cancelled context: %v", err)
	}
	if _, err := ListStructContext[note](cancelled, db, `SELECT id, body FROM notes`); !errors.Is(err, context.Canceled) {
		t.Fatalf("ListStructContext with a cancelled context: %v", err)
	}
}
