package database

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/regiellis/hyperchi/hyperchi/helpers"
)

func TestSlugGeneratorGenerate(t *testing.T) {
	cases := map[string]string{
		"My Blog Post Title":           "my-blog-post-title",
		"  Hello,   World!!  ":         "hello-world",
		"Already-a-slug":               "already-a-slug",
		"Go 1.26 -- released":          "go-1-26-released",
		"__under_scores__":             "under-scores",
		"MiXeD CaSe 42":                "mixed-case-42",
		"'; DROP TABLE posts; --":      "drop-table-posts",
		"":                             "",
		"---":                          "",
		"!!!":                          "",
		"trailing punctuation...?":     "trailing-punctuation",
		"tabs\tand\nnewlines":          "tabs-and-newlines",
		"multiple     spaces   inside": "multiple-spaces-inside",
	}
	for in, want := range cases {
		if got := (SlugGenerator{}).Generate(in); got != want {
			t.Errorf("Generate(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestSlugGeneratorUsesHelpersSlugify checks the database slug follows
// helpers.Slugify, so a title gives the same slug everywhere and accented
// letters are transliterated ("Crème" to "creme"), not dropped.
func TestSlugGeneratorUsesHelpersSlugify(t *testing.T) {
	for _, in := range []string{
		"Crème Brûlée à la carte",
		"Straße über Ærø",
		"my blog post: part 2!",
		"",
	} {
		if got, want := (SlugGenerator{}).Generate(in), helpers.Slugify(in); got != want {
			t.Errorf("Generate(%q) = %q, helpers.Slugify = %q", in, got, want)
		}
	}
	if got := (SlugGenerator{}).Generate("Crème Brûlée"); got != "creme-brulee" {
		t.Errorf("Generate(Crème Brûlée) = %q, want creme-brulee", got)
	}
	if got := (SlugGenerator{}).Generate("iPhone Review"); got != "iphone-review" {
		t.Errorf("Generate(iPhone Review) = %q, want iphone-review", got)
	}
}

func TestSlugGeneratorUniqueSlug(t *testing.T) {
	db := newTestDB(t)
	if _, err := db.Exec(`CREATE TABLE posts (id INTEGER PRIMARY KEY, slug TEXT UNIQUE)`); err != nil {
		t.Fatal(err)
	}
	sg := SlugGenerator{}
	unique := func(excludeID ...any) string {
		t.Helper()
		slug, err := sg.UniqueSlug("Hello World", "posts", "slug", db, excludeID...)
		if err != nil {
			t.Fatalf("UniqueSlug: %v", err)
		}
		return slug
	}

	if got := unique(); got != "hello-world" {
		t.Fatalf("free slug = %q, want hello-world", got)
	}
	for _, s := range []string{"hello-world", "hello-world-2"} {
		if _, err := db.Create("posts", map[string]any{"slug": s}); err != nil {
			t.Fatal(err)
		}
	}
	if got := unique(); got != "hello-world-3" {
		t.Fatalf("taken slug = %q, want hello-world-3", got)
	}
	// Updating row 1 may keep its own slug.
	if got := unique(1); got != "hello-world" {
		t.Fatalf("slug excluding its own row = %q, want hello-world", got)
	}
	// Excluding row 2 frees "-2" but "hello-world" is still row 1's.
	if got := unique(2); got != "hello-world-2" {
		t.Fatalf("slug excluding row 2 = %q, want hello-world-2", got)
	}
	// A nil exclude id is ignored rather than bound as "id != NULL" (which
	// matches nothing and would make every slug look free).
	if got := unique(nil); got != "hello-world-3" {
		t.Fatalf("slug with nil exclude = %q, want hello-world-3", got)
	}

	// An unsafe table name is rejected by the identifier guard and never runs.
	if slug, err := sg.UniqueSlug("x", "posts; DROP TABLE posts", "slug", db); err == nil {
		t.Fatalf("UniqueSlug with an unsafe table = %q, want an error", slug)
	}
	if ok, err := db.Exists("sqlite_master", "type = 'table' AND name = ?", "posts"); err != nil || !ok {
		t.Fatalf("posts table gone after an unsafe UniqueSlug table: %v, %v", ok, err)
	}
}

// TestUniqueSlugReportsDatabaseErrors checks UniqueSlug returns a failed
// lookup as an error. Treating it as "slug is free" would return a slug that
// collides on insert or lands in a non-unique column as a duplicate.
func TestUniqueSlugReportsDatabaseErrors(t *testing.T) {
	db := newTestDB(t)
	if slug, err := (SlugGenerator{}).UniqueSlug("Hello", "missing_table", "slug", db); err == nil {
		t.Fatalf("UniqueSlug on a missing table = %q, nil; want the database error", slug)
	}

	closed := newTestDB(t)
	if err := closed.Close(); err != nil {
		t.Fatal(err)
	}
	if slug, err := (SlugGenerator{}).UniqueSlug("Hello", "posts", "slug", closed); err == nil {
		t.Fatalf("UniqueSlug on a closed database = %q, nil; want the database error", slug)
	}
}

func TestDateHelper(t *testing.T) {
	var dh DateHelper
	ts := time.Date(2026, 3, 4, 5, 6, 7, 999, time.UTC)
	if got := dh.Format(ts); got != "2026-03-04 05:06:07" {
		t.Fatalf("Format = %q", got)
	}
	parsed, err := dh.Parse("2026-03-04 05:06:07")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !parsed.Equal(ts.Truncate(time.Second)) {
		t.Fatalf("Parse = %v, want %v", parsed, ts.Truncate(time.Second))
	}
	for _, bad := range []string{"", "2026-03-04", "2026-03-04T05:06:07Z", "2026-13-04 05:06:07"} {
		if _, err := dh.Parse(bad); err == nil {
			t.Errorf("Parse(%q) accepted", bad)
		}
	}
	if !regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$`).MatchString(dh.Now()) {
		t.Errorf("Now = %q", dh.Now())
	}
	if _, err := dh.Parse(dh.Now()); err != nil {
		t.Errorf("Now does not round-trip through Parse: %v", err)
	}
	if !regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`).MatchString(dh.Today()) {
		t.Errorf("Today = %q", dh.Today())
	}
}

func TestBackupCopiesTheDatabase(t *testing.T) {
	db := seedUsers(t)
	backup := NewBackup(db)

	size, err := backup.GetSize()
	if err != nil {
		t.Fatalf("GetSize: %v", err)
	}
	var pages int64
	if err := db.Get(&pages, "PRAGMA page_count"); err != nil {
		t.Fatal(err)
	}
	if size != pages*4096 || size <= 0 {
		t.Fatalf("GetSize = %d, want page_count(%d) * 4096", size, pages)
	}

	dir := t.TempDir()
	for _, name := range []string{"copy.db", "it's a copy.db"} {
		t.Run(name, func(t *testing.T) {
			dest := filepath.Join(dir, name)
			if err := backup.Create(dest); err != nil {
				t.Fatalf("Create: %v", err)
			}
			copyDB, err := NewDB(&DBConfig{Path: dest, MaxOpenConns: 1, MaxIdleConns: 1})
			if err != nil {
				t.Fatalf("open backup: %v", err)
			}
			t.Cleanup(func() {
				if err := copyDB.Close(); err != nil {
					t.Errorf("close backup: %v", err)
				}
			})
			names, err := ListStruct[userRow](copyDB, "SELECT id, name, age FROM users ORDER BY id")
			if err != nil {
				t.Fatalf("read backup: %v", err)
			}
			if len(names) != 3 || names[0].Name != "Ada" || names[2].Name != "Lin" {
				t.Fatalf("backup rows = %+v", names)
			}
		})
	}

	// VACUUM INTO refuses to overwrite an existing database file.
	if err := backup.Create(filepath.Join(dir, "copy.db")); err == nil {
		t.Fatal("Create overwrote an existing backup")
	}
	if err := backup.Create(filepath.Join(dir, "missing-dir", "x.db")); err == nil {
		t.Fatal("Create into a missing directory returned no error")
	}
	if _, err := os.Stat(filepath.Join(dir, "missing-dir")); !os.IsNotExist(err) {
		t.Fatalf("Create made a directory it was not asked to: %v", err)
	}
}

func TestBackupGetTableInfo(t *testing.T) {
	db := newTestDB(t)
	info, err := NewBackup(db).GetTableInfo()
	if err != nil || len(info) != 0 {
		t.Fatalf("GetTableInfo on an empty database = %v, %v", info, err)
	}
	if err := db.AutoMigrate("CREATE TABLE zeta (id INTEGER)", "CREATE TABLE alpha (id INTEGER)"); err != nil {
		t.Fatal(err)
	}
	info, err = NewBackup(db).GetTableInfo()
	if err != nil {
		t.Fatalf("GetTableInfo: %v", err)
	}
	if len(info) != 2 || info[0]["name"] != "alpha" || info[1]["name"] != "zeta" {
		t.Fatalf("GetTableInfo = %v, want alpha then zeta", info)
	}
	if info[0]["sql"] != "CREATE TABLE alpha (id INTEGER)" {
		t.Fatalf("sql = %#v", info[0]["sql"])
	}
}
