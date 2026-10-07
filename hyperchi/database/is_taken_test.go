package database

import "testing"

func TestIsTaken(t *testing.T) {
	db := newTestDB(t)
	if _, err := db.Exec("CREATE TABLE users (id INTEGER PRIMARY KEY, email TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO users (id, email) VALUES (1, 'ada@example.com')"); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		email   string
		exclude []any
		want    bool
	}{
		{"ada@example.com", nil, true},
		{"grace@example.com", nil, false},
		{"ada@example.com", []any{1}, false}, // updating the same row
	}
	for _, tc := range cases {
		got, err := IsTaken(db, "users", "email", tc.email, tc.exclude...)
		if err != nil || got != tc.want {
			t.Errorf("IsTaken(%q, %v) = %v, %v; want %v", tc.email, tc.exclude, got, err, tc.want)
		}
	}
	if _, err := IsTaken(db, "users", "email; DROP TABLE users", "x"); err == nil {
		t.Error("an unsafe column name was accepted")
	}
}
