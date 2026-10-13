package database

import "testing"

// Without ORDER BY, SQLite returns rows in whatever order the chosen index
// gives, so pages could repeat or skip rows. Paginate orders by rowid, which is
// insertion order for an ordinary table.
func TestPaginateHasAStableOrder(t *testing.T) {
	db := newTestDB(t)
	if _, err := db.Exec(`CREATE TABLE people (id INTEGER PRIMARY KEY, name TEXT, team TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE INDEX people_team_name ON people (team, name)`); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"zoe", "mia", "ann", "kai", "bea"} {
		if _, err := db.Exec(`INSERT INTO people (name, team) VALUES (?, 'a')`, name); err != nil {
			t.Fatal(err)
		}
	}
	_, first, err := Paginate(db, "people", 1, 3, "team = ?", "a")
	if err != nil {
		t.Fatal(err)
	}
	_, second, err := Paginate(db, "people", 2, 3, "team = ?", "a")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, row := range append(first, second...) {
		got = append(got, row["name"].(string))
	}
	want := []string{"zoe", "mia", "ann", "kai", "bea"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("pages in index order %v, want insertion order %v", got, want)
		}
	}
}
