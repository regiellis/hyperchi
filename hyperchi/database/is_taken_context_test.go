package database

import (
	"context"
	"errors"
	"testing"
)

func TestIsTakenContextHonorsTheRequestContext(t *testing.T) {
	db := newTestDB(t)
	if _, err := db.Exec(`CREATE TABLE subscribers (id INTEGER PRIMARY KEY, email TEXT UNIQUE)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO subscribers (id, email) VALUES (1, 'a@b.co')`); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if taken, err := IsTakenContext(ctx, db, "subscribers", "email", "a@b.co"); err != nil || !taken {
		t.Fatalf("existing email: %v, %v", taken, err)
	}
	if taken, err := IsTakenContext(ctx, db, "subscribers", "email", "a@b.co", 1); err != nil || taken {
		t.Fatalf("excluding its own row: %v, %v", taken, err)
	}
	if taken, err := IsTakenContext(ctx, db, "subscribers", "email", "new@b.co"); err != nil || taken {
		t.Fatalf("new email: %v, %v", taken, err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := IsTakenContext(cancelled, db, "subscribers", "email", "a@b.co"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled context: err = %v", err)
	}
	if _, err := IsTakenContext(ctx, db, "subscribers; DROP TABLE x", "email", "a"); err == nil {
		t.Fatal("unsafe table name accepted")
	}
}
