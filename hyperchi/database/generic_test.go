package database

import (
	"errors"
	"fmt"
	"testing"
)

type userRow struct {
	ID       int64  `db:"id"`
	Name     string `db:"name"`
	Age      int    `db:"age"`
	Ignored  string `db:"-"`
	internal string
}

// untaggedRow maps columns by exact field name when there is no db tag.
type untaggedRow struct {
	id   int64
	Name string
}

func TestGetStructAndListStruct(t *testing.T) {
	db := seedUsers(t)

	u, err := GetStruct[userRow](db, "SELECT id, name, age FROM users WHERE id = ?", 2)
	if err != nil {
		t.Fatalf("GetStruct: %v", err)
	}
	if u.ID != 2 || u.Name != "Grace" || u.Age != 45 {
		t.Fatalf("GetStruct = %+v", u)
	}

	// Extra columns, and columns whose field is skipped or unexported, are
	// read and discarded rather than failing the scan.
	u, err = GetStruct[userRow](db, "SELECT id, name, age, 'x' AS Ignored, 'y' AS internal, 1 AS extra FROM users WHERE id = 1")
	if err != nil {
		t.Fatalf("GetStruct with extra columns: %v", err)
	}
	if u.Name != "Ada" || u.Ignored != "" || u.internal != "" {
		t.Fatalf("GetStruct = %+v", u)
	}

	un, err := GetStruct[untaggedRow](db, "SELECT id, name AS Name FROM users WHERE id = 3")
	if err != nil {
		t.Fatalf("GetStruct untagged: %v", err)
	}
	if un.Name != "Lin" || un.id != 0 {
		t.Fatalf("GetStruct untagged = %+v", un)
	}

	if _, err := GetStruct[userRow](db, "SELECT id FROM users WHERE id = 99"); !errors.Is(err, ErrNoRows) {
		t.Fatalf("GetStruct missing = %v, want ErrNoRows", err)
	}
	if _, err := GetStruct[userRow](db, "SELECT nope FROM users"); err == nil {
		t.Fatal("GetStruct with a bad query returned no error")
	}
	if _, err := GetStruct[int](db, "SELECT 1"); err == nil {
		t.Fatal("GetStruct into a non-struct returned no error")
	}
	// NULL into a non-nullable field is a scan error, not a silent zero.
	if _, err := GetStruct[userRow](db, "SELECT NULL AS name"); err == nil {
		t.Fatal("GetStruct scanned NULL into a string")
	}

	all, err := ListStruct[userRow](db, "SELECT id, name, age FROM users WHERE age > ? ORDER BY age", 30)
	if err != nil {
		t.Fatalf("ListStruct: %v", err)
	}
	if len(all) != 2 || all[0].Name != "Ada" || all[1].Name != "Grace" {
		t.Fatalf("ListStruct = %+v", all)
	}
	none, err := ListStruct[userRow](db, "SELECT id FROM users WHERE id < 0")
	if err != nil || len(none) != 0 {
		t.Fatalf("ListStruct none = %+v, %v", none, err)
	}
	if _, err := ListStruct[userRow](db, "SELECT nope FROM users"); err == nil {
		t.Fatal("ListStruct with a bad query returned no error")
	}
	if _, err := ListStruct[string](db, "SELECT name FROM users"); err == nil {
		t.Fatal("ListStruct into a non-struct returned no error")
	}
}

func TestGenericTxHelpers(t *testing.T) {
	db := seedUsers(t)
	err := db.Tx(func(tx *Tx) error {
		if _, err := tx.Create("users", map[string]any{"name": "Hedy", "age": 41}); err != nil {
			return err
		}
		u, err := GetStructTx[userRow](tx, "SELECT id, name, age FROM users WHERE name = ?", "Hedy")
		if err != nil || u.ID != 4 || u.Age != 41 {
			return fmt.Errorf("GetStructTx = %+v, %w", u, err)
		}
		if _, err := GetStructTx[userRow](tx, "SELECT id FROM users WHERE id = 99"); !errors.Is(err, ErrNoRows) {
			return fmt.Errorf("GetStructTx missing = %w, want ErrNoRows", err)
		}
		if _, err := GetStructTx[userRow](tx, "SELECT nope FROM users"); err == nil {
			return errors.New("GetStructTx with a bad query returned no error")
		}
		if _, err := GetStructTx[int](tx, "SELECT 1"); err == nil {
			return errors.New("GetStructTx into a non-struct returned no error")
		}

		all, err := ListStructTx[userRow](tx, "SELECT id, name, age FROM users ORDER BY id")
		if err != nil || len(all) != 4 || all[3].Name != "Hedy" {
			return fmt.Errorf("ListStructTx = %+v, %w", all, err)
		}
		if _, err := ListStructTx[userRow](tx, "SELECT nope FROM users"); err == nil {
			return errors.New("ListStructTx with a bad query returned no error")
		}
		if _, err := ListStructTx[int](tx, "SELECT id FROM users"); err == nil {
			return errors.New("ListStructTx into a non-struct returned no error")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestModel(t *testing.T) {
	db := seedUsers(t)
	users := NewModel[userRow](db, "users")

	u, err := users.Get(1)
	if err != nil || u.Name != "Ada" {
		t.Fatalf("Get = %+v, %v", u, err)
	}
	if _, err := users.Get(99); !errors.Is(err, ErrNoRows) {
		t.Fatalf("Get missing = %v, want ErrNoRows", err)
	}

	all, err := users.List()
	if err != nil || len(all) != 3 {
		t.Fatalf("List = %+v, %v", all, err)
	}

	older, err := users.Where("age > ? ORDER BY age DESC", 30)
	if err != nil || len(older) != 2 || older[0].Name != "Grace" {
		t.Fatalf("Where = %+v, %v", older, err)
	}

	first, err := users.First("age < ?", 40)
	if err != nil || first.Name != "Ada" {
		t.Fatalf("First = %+v, %v", first, err)
	}
	if _, err := users.First("age > ?", 100); !errors.Is(err, ErrNoRows) {
		t.Fatalf("First none = %v, want ErrNoRows", err)
	}

	if n, err := users.Count(); err != nil || n != 3 {
		t.Fatalf("Count = %d, %v", n, err)
	}
	if n, err := users.CountWhere("age > ?", 30); err != nil || n != 2 {
		t.Fatalf("CountWhere = %d, %v", n, err)
	}
	if ok, err := users.Exists(); err != nil || !ok {
		t.Fatalf("Exists = %v, %v", ok, err)
	}
	if ok, err := users.ExistsWhere("name = ?", "nobody"); err != nil || ok {
		t.Fatalf("ExistsWhere = %v, %v", ok, err)
	}

	empty := NewModel[userRow](db, "users")
	if _, err := db.Exec("DELETE FROM users"); err != nil {
		t.Fatal(err)
	}
	if ok, err := empty.Exists(); err != nil || ok {
		t.Fatalf("Exists on an empty table = %v, %v", ok, err)
	}
	if rows, err := empty.List(); err != nil || len(rows) != 0 {
		t.Fatalf("List on an empty table = %+v, %v", rows, err)
	}
}
