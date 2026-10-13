package database

import (
	"strings"
	"testing"
)

func TestTxRejectsUnsafeIdentifiers(t *testing.T) {
	db := newTestDB(t)
	if _, err := db.Exec(`CREATE TABLE users(id INTEGER PRIMARY KEY, name TEXT, role TEXT); INSERT INTO users VALUES(1,'Ada','user'),(2,'Grace','user')`); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*Tx) error{
		"update column": func(tx *Tx) error {
			return tx.Update("users", 1, map[string]any{"name = ?, role = 'admin' --": "evil"})
		},
		"create column": func(tx *Tx) error {
			_, err := tx.Create("users", map[string]any{"name) VALUES ('evil'); --": "evil"})
			return err
		},
		"create table": func(tx *Tx) error { _, err := tx.Create("users --", map[string]any{"name": "evil"}); return err },
		"update table": func(tx *Tx) error { return tx.Update("users --", 1, map[string]any{"name": "evil"}) },
		"delete table": func(tx *Tx) error { return tx.Delete("users --", 1) },
		"count table":  func(tx *Tx) error { _, err := tx.Count("users --", ""); return err },
		"exists table": func(tx *Tx) error { _, err := tx.Exists("users --", ""); return err },
	}
	for name, operation := range cases {
		t.Run(name, func(t *testing.T) {
			err := db.Tx(operation)
			if err == nil || !strings.Contains(err.Error(), "identifier") {
				t.Errorf("want identifier error, got %v", err)
			}
		})
	}
	count, err := db.Count("users", "role = ?", "user")
	if err != nil || count != 2 {
		t.Fatalf("rows changed: count=%d err=%v", count, err)
	}
	if err := db.Tx(func(tx *Tx) error {
		id, err := tx.Create("users", map[string]any{"name": "Lin", "role": "user"})
		if err != nil {
			return err
		}
		if err := tx.Update("users", id, map[string]any{"name": "Lin's"}); err != nil {
			return err
		}
		return tx.Delete("users", id)
	}); err != nil {
		t.Fatal(err)
	}
}
