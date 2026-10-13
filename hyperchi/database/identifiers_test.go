package database

import (
	"fmt"
	"strings"
	"testing"
)

// TestSelectAcceptsDocumentedColumnForms runs every column form Select
// documents against a real table, so the allowlist and SQLite agree.
func TestSelectAcceptsDocumentedColumnForms(t *testing.T) {
	db := seedUsers(t)
	for _, col := range []string{
		"*",
		"users.*",
		"name",
		"users.name",
		"name AS who",
		"users.name as who",
		"COUNT(*)",
		"count(*) AS n",
		"MAX(age)",
		"min( users.age ) AS youngest",
		"COUNT(DISTINCT name)",
		"sum(age)",
		"AVG(age) AS mean",
		"TOTAL(users.age)",
		"  name  ",
	} {
		query, args := Select(col).From("users").Build()
		if _, err := db.List(query, args...); err != nil {
			t.Errorf("Select(%q): %s failed: %v", col, query, err)
		}
	}

	query, _ := Select("users.name", "COUNT(*) AS n").From("users").Build()
	if query != "SELECT users.name, COUNT(*) AS n FROM users" {
		t.Fatalf("query = %q", query)
	}
}

// TestSelectRejectsUnsafeColumns checks Select panics on a column outside its
// allowlist, since columns are interpolated into the query.
func TestSelectRejectsUnsafeColumns(t *testing.T) {
	for _, col := range []string{
		"",
		"name; DROP TABLE users",
		"name FROM users; DROP TABLE users --",
		"(SELECT password FROM users)",
		"name who",
		"name AS",
		"name AS who; --",
		"* AS everything",
		"users.* AS x",
		"a.b.c",
		"COALESCE(name, 'x')",
		"COUNT(*) FILTER (WHERE 1)",
		"COUNT(name, age)",
		"LOWER(name)",
		"'literal'",
		"1",
		"name -- comment",
	} {
		mustPanic(t, "Select("+col+")", func() { Select(col) })
	}
	mustPanic(t, "second column", func() { Select("id", "name; DROP TABLE users") })
}

// TestMigrationBuilderRejectsUnsafeNames checks the migration builder panics
// on unsafe table, column, and foreign-key names, which it interpolates into
// CREATE TABLE and CREATE INDEX.
func TestMigrationBuilderRejectsUnsafeNames(t *testing.T) {
	bad := "x); DROP TABLE users; --"
	cases := map[string]func(){
		"table":       func() { Table(bad) },
		"string":      func() { Table("t").String(bad) },
		"string len":  func() { Table("t").String(bad, 10) },
		"text":        func() { Table("t").Text(bad) },
		"integer":     func() { Table("t").Integer(bad) },
		"real":        func() { Table("t").Real(bad) },
		"boolean":     func() { Table("t").Boolean(bad) },
		"timestamp":   func() { Table("t").Timestamp(bad) },
		"fk column":   func() { Table("t").ForeignKey(bad, "users", "id") },
		"fk table":    func() { Table("t").ForeignKey("user_id", bad, "id") },
		"fk ref col":  func() { Table("t").ForeignKey("user_id", "users", bad) },
		"spaced name": func() { Table("t").Integer("user id") },
		"dotted name": func() { Table("main.t") },
	}
	for name, fn := range cases {
		mustPanic(t, name, fn)
	}

	// The panic names what was wrong.
	defer func() {
		if r := recover(); r == nil || !strings.Contains(fmt.Sprint(r), "foreign key table") {
			t.Fatalf("panic = %v, want it to name the foreign key table", r)
		}
	}()
	Table("t").ForeignKey("user_id", "users u", "id")
}
