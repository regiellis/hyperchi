package database

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestQueryBuilderBuild(t *testing.T) {
	cases := []struct {
		name  string
		qb    *QueryBuilder
		query string
		args  []any
	}{
		{
			name:  "default star",
			qb:    Select().From("users"),
			query: "SELECT * FROM users",
		},
		{
			name:  "columns",
			qb:    Select("id", "name").From("users"),
			query: "SELECT id, name FROM users",
		},
		{
			name:  "where and",
			qb:    Select().From("users").Where("active = ?", true).And("age > ?", 18),
			query: "SELECT * FROM users WHERE active = ? AND age > ?",
			args:  []any{true, 18},
		},
		{
			name:  "or wraps the previous conditions",
			qb:    Select().From("users").Where("a = ?", 1).And("b = ?", 2).Or("c = ?", 3),
			query: "SELECT * FROM users WHERE (a = ? AND b = ?) OR (c = ?)",
			args:  []any{1, 2, 3},
		},
		{
			name:  "or as the first condition",
			qb:    Select().From("users").Or("c = ?", 3),
			query: "SELECT * FROM users WHERE c = ?",
			args:  []any{3},
		},
		{
			name: "joins",
			qb: Select("users.name", "posts.title").From("users").
				Join("posts", "posts.user_id = users.id").
				LeftJoin("comments", "comments.post_id = posts.id"),
			query: "SELECT users.name, posts.title FROM users JOIN posts ON posts.user_id = users.id LEFT JOIN comments ON comments.post_id = posts.id",
		},
		{
			name:  "order by defaults to ASC and normalizes case",
			qb:    Select().From("users").OrderBy("name").OrderBy("age", " desc "),
			query: "SELECT * FROM users ORDER BY name ASC, age DESC",
		},
		{
			name:  "limit and offset",
			qb:    Select().From("users").Limit(10).Offset(20),
			query: "SELECT * FROM users LIMIT 10 OFFSET 20",
		},
		{
			name:  "page computes the offset",
			qb:    Select().From("users").Page(3, 25),
			query: "SELECT * FROM users LIMIT 25 OFFSET 50",
		},
		{
			name:  "page below one is page one",
			qb:    Select().From("users").Page(0, 5),
			query: "SELECT * FROM users LIMIT 5",
		},
		{
			// SQLite only accepts OFFSET after a LIMIT; -1 means "no limit".
			name:  "offset without a limit",
			qb:    Select().From("users").Offset(5),
			query: "SELECT * FROM users LIMIT -1 OFFSET 5",
		},
		{
			name: "every clause in order",
			qb: Select("id").From("users").Join("posts", "posts.user_id = users.id").
				Where("posts.published = ?", true).OrderBy("id", "DESC").Limit(5).Offset(5),
			query: "SELECT id FROM users JOIN posts ON posts.user_id = users.id WHERE posts.published = ? ORDER BY id DESC LIMIT 5 OFFSET 5",
			args:  []any{true},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			query, args := tc.qb.Build()
			if query != tc.query {
				t.Errorf("query\n got: %s\nwant: %s", query, tc.query)
			}
			if len(args) != 0 || len(tc.args) != 0 {
				if !reflect.DeepEqual(args, tc.args) {
					t.Errorf("args = %#v, want %#v", args, tc.args)
				}
			}
		})
	}
}

func TestQueryBuilderWithoutTableBuildsNothing(t *testing.T) {
	query, args := Select("id").Where("a = ?", 1).Build()
	if query != "" || args != nil {
		t.Fatalf("Build without From = %q, %v; want empty", query, args)
	}
}

// TestQueryBuilderGeneratesRunnableSQL executes builder output, so a clause
// SQLite rejects fails here even if the string looks plausible.
func TestQueryBuilderGeneratesRunnableSQL(t *testing.T) {
	db := seedUsers(t)
	cases := []struct {
		name string
		qb   *QueryBuilder
		want []string
	}{
		{"order desc", Select("name").From("users").OrderBy("age", "DESC"), []string{"Grace", "Ada", "Lin"}},
		{"where", Select("name").From("users").Where("age < ?", 40).OrderBy("name"), []string{"Ada", "Lin"}},
		{"or", Select("name").From("users").Where("name = ?", "Ada").Or("age = ?", 29).OrderBy("name"), []string{"Ada", "Lin"}},
		{"page 2", Select("name").From("users").OrderBy("id").Page(2, 2), []string{"Lin"}},
		{"offset only", Select("name").From("users").OrderBy("id").Offset(1), []string{"Grace", "Lin"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			query, args := tc.qb.Build()
			rows, err := db.List(query, args...)
			if err != nil {
				t.Fatalf("%s: %v", query, err)
			}
			var got []string
			for _, r := range rows {
				got = append(got, fmt.Sprint(r["name"]))
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("%s: got %v, want %v", query, got, tc.want)
			}
		})
	}
}

func mustPanic(t *testing.T, name string, fn func()) {
	t.Helper()
	defer func() {
		r := recover()
		if r == nil {
			t.Errorf("%s: expected a panic", name)
			return
		}
		if !strings.Contains(fmt.Sprint(r), "invalid") {
			t.Errorf("%s: panic %v does not name the invalid input", name, r)
		}
	}()
	fn()
}

// TestQueryBuilderRejectsUnsafeIdentifiers covers the builder's injection
// guards: every identifier it interpolates must be a plain identifier.
func TestQueryBuilderRejectsUnsafeIdentifiers(t *testing.T) {
	cases := map[string]func(){
		"from":            func() { Select().From("users; DROP TABLE users--") },
		"from alias":      func() { Select().From("users u") },
		"order column":    func() { Select().From("users").OrderBy("name; DROP TABLE users") },
		"order dotted":    func() { Select().From("users").OrderBy("u.name") },
		"order direction": func() { Select().From("users").OrderBy("name", "DESC; DROP TABLE users") },
		"join table":      func() { Select().From("users").Join("posts; DROP TABLE users--", "1=1") },
		"left join table": func() { Select().From("users").LeftJoin("posts p JOIN secrets", "1=1") },
		"unique slug col": func() {
			if _, err := (SlugGenerator{}).UniqueSlug("x", "posts", "slug = slug OR 1", nil); err != nil {
				t.Errorf("UniqueSlug returned %v instead of panicking", err)
			}
		},
		"new model": func() { NewModel[struct{}](nil, "users --") },
	}
	for name, fn := range cases {
		mustPanic(t, name, fn)
	}
}

func TestValidateIdentifier(t *testing.T) {
	cases := []struct {
		name string
		ok   bool
	}{
		{"users", true},
		{"_private", true},
		{"Table_2", true},
		{"", false},
		{"2users", false},
		{"users u", false},
		{"public.users", false},
		{`"users"`, false},
		{"users;", false},
		{"users--", false},
		{"na\x00me", false},
		{"usérs", false},
	}
	for _, tc := range cases {
		err := validateIdentifier("table", tc.name)
		if (err == nil) != tc.ok {
			t.Errorf("validateIdentifier(%q) err = %v, want ok=%v", tc.name, err, tc.ok)
		}
	}
}

func TestValidateOrderDirection(t *testing.T) {
	cases := map[string]string{"": "ASC", "asc": "ASC", " Asc ": "ASC", "DESC": "DESC", "desc": "DESC"}
	for in, want := range cases {
		got, err := validateOrderDirection(in)
		if err != nil || got != want {
			t.Errorf("validateOrderDirection(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"up", "ASC,", "DESC NULLS FIRST", "DESC; --"} {
		if _, err := validateOrderDirection(bad); err == nil {
			t.Errorf("validateOrderDirection(%q) accepted", bad)
		}
	}
}

func TestNewPaginator(t *testing.T) {
	cases := []struct {
		page, perPage, total int
		want                 Paginator
		offset               int
	}{
		{1, 10, 0, Paginator{Page: 1, PerPage: 10, Total: 0, TotalPages: 1}, 0},
		{1, 10, 10, Paginator{Page: 1, PerPage: 10, Total: 10, TotalPages: 1}, 0},
		{1, 10, 11, Paginator{Page: 1, PerPage: 10, Total: 11, TotalPages: 2, HasNext: true}, 0},
		{2, 10, 11, Paginator{Page: 2, PerPage: 10, Total: 11, TotalPages: 2, HasPrev: true}, 10},
		{2, 3, 9, Paginator{Page: 2, PerPage: 3, Total: 9, TotalPages: 3, HasNext: true, HasPrev: true}, 3},
		{0, 0, 25, Paginator{Page: 1, PerPage: 10, Total: 25, TotalPages: 3, HasNext: true}, 0},
		{-4, -1, 5, Paginator{Page: 1, PerPage: 10, Total: 5, TotalPages: 1}, 0},
		{9, 10, 5, Paginator{Page: 9, PerPage: 10, Total: 5, TotalPages: 1, HasPrev: true}, 80},
	}
	for _, tc := range cases {
		p := NewPaginator(tc.page, tc.perPage, tc.total)
		if *p != tc.want {
			t.Errorf("NewPaginator(%d,%d,%d) = %+v, want %+v", tc.page, tc.perPage, tc.total, *p, tc.want)
		}
		if p.Offset() != tc.offset {
			t.Errorf("NewPaginator(%d,%d,%d).Offset() = %d, want %d", tc.page, tc.perPage, tc.total, p.Offset(), tc.offset)
		}
	}
}

func TestPaginate(t *testing.T) {
	db := newTestDB(t)
	if _, err := db.Exec(`CREATE TABLE posts (id INTEGER PRIMARY KEY, published INTEGER)`); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 25; i++ {
		if _, err := db.Create("posts", map[string]any{"published": i%5 != 0}); err != nil {
			t.Fatal(err)
		}
	}

	cases := []struct {
		name          string
		page, perPage int
		where         string
		args          []any
		total, pages  int
		rows          int
		firstID       int64
	}{
		{"first page", 1, 10, "", nil, 25, 3, 10, 1},
		{"last page", 3, 10, "", nil, 25, 3, 5, 21},
		{"past the end", 4, 10, "", nil, 25, 3, 0, 0},
		{"filtered", 2, 10, "published = ?", []any{true}, 20, 2, 10, 13},
		// perPage < 1 falls back to the paginator default of 10; the query
		// must agree with the metadata it returns.
		{"zero per page", 1, 0, "", nil, 25, 3, 10, 1},
		{"zero per page, page 2", 2, 0, "", nil, 25, 3, 10, 11},
		{"negative per page", 3, -5, "", nil, 25, 3, 5, 21},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, rows, err := Paginate(db, "posts", tc.page, tc.perPage, tc.where, tc.args...)
			if err != nil {
				t.Fatalf("Paginate: %v", err)
			}
			if p.Total != tc.total || p.TotalPages != tc.pages {
				t.Errorf("paginator = %+v, want total %d pages %d", *p, tc.total, tc.pages)
			}
			if len(rows) != tc.rows {
				t.Fatalf("got %d rows, want %d", len(rows), tc.rows)
			}
			if len(rows) > p.PerPage {
				t.Errorf("returned %d rows for a page of %d", len(rows), p.PerPage)
			}
			if tc.rows > 0 && rows[0]["id"] != tc.firstID {
				t.Errorf("first id = %v, want %d", rows[0]["id"], tc.firstID)
			}
		})
	}

	if _, _, err := Paginate(db, "posts --", 1, 10, ""); err == nil {
		t.Error("Paginate accepted an unsafe table name")
	}
	if _, _, err := Paginate(db, "posts", 1, 10, "nope = ?", 1); err == nil {
		t.Error("Paginate with a bad where clause returned no error")
	}
}
