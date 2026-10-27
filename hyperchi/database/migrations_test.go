package database

import (
	"sort"
	"strings"
	"testing"

	"github.com/regiellis/hyperchi/hyperchi/security"
)

func TestMigrationBuilderBuild(t *testing.T) {
	got := Table("items").
		ID().
		String("sku", 32).NotNull().NotNull().Unique().Unique().Index().
		String("title").
		Text("body").
		Integer("qty").Default(0).
		Real("price").Default(9.5).
		Boolean("visible").
		Timestamp("seen_at").
		Integer("owner_id").
		CreatedAt().
		ForeignKey("owner_id", "owners", "id").
		Build()

	want := "CREATE TABLE IF NOT EXISTS items (\n" +
		"  id INTEGER PRIMARY KEY AUTOINCREMENT,\n" +
		"  sku VARCHAR(32) NOT NULL UNIQUE,\n" +
		"  title TEXT,\n" +
		"  body TEXT,\n" +
		"  qty INTEGER DEFAULT 0,\n" +
		"  price REAL DEFAULT 9.5,\n" +
		"  visible INTEGER DEFAULT 0,\n" +
		"  seen_at DATETIME,\n" +
		"  owner_id INTEGER,\n" +
		"  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,\n" +
		"  FOREIGN KEY (owner_id) REFERENCES owners(id)\n" +
		");\n" +
		"CREATE INDEX IF NOT EXISTS idx_items_sku ON items (sku)"
	if got != want {
		t.Fatalf("Build\n got:\n%s\nwant:\n%s", got, want)
	}
}

func TestMigrationBuilderEdgeCases(t *testing.T) {
	if got := Table("empty").Build(); got != "" {
		t.Fatalf("Build with no columns = %q, want empty", got)
	}
	// Modifiers before any column are no-ops, not panics.
	if got := Table("t").NotNull().Unique().Default(1).Index().Build(); got != "" {
		t.Fatalf("modifiers without a column = %q, want empty", got)
	}
	if got := Table("t").String("name", 0).Build(); !strings.Contains(got, "name TEXT") {
		t.Fatalf("String with length 0 = %q, want TEXT", got)
	}
	if got := Table("t").Timestamps().Build(); !strings.Contains(got, "created_at DATETIME") || !strings.Contains(got, "updated_at DATETIME") {
		t.Fatalf("Timestamps = %q", got)
	}
}

// TestMigrationDefaultsRoundTrip applies builder defaults to a real table and
// reads them back. A string default containing a quote must stay inside its
// SQL literal rather than break (or extend) the statement.
func TestMigrationDefaultsRoundTrip(t *testing.T) {
	db := newTestDB(t)
	migration := Table("prefs").
		ID().
		String("greeting").Default("it's here").
		String("payload").Default("x'); DROP TABLE prefs; --").
		Integer("count").Default(int64(7)).
		Boolean("enabled").Default(true).
		Boolean("disabled").Default(false).
		String("other").Default(struct{ A int }{3}).
		Build()
	if err := db.Migrate(migration); err != nil {
		t.Fatalf("Migrate(%s): %v", migration, err)
	}
	if _, err := db.Exec("INSERT INTO prefs DEFAULT VALUES"); err != nil {
		t.Fatalf("insert defaults: %v", err)
	}
	row, err := db.GetMap("SELECT greeting, payload, count, enabled, disabled, other FROM prefs")
	if err != nil {
		t.Fatalf("GetMap: %v", err)
	}
	want := map[string]any{
		"greeting": "it's here",
		"payload":  "x'); DROP TABLE prefs; --",
		"count":    int64(7),
		"enabled":  int64(1),
		"disabled": int64(0),
		"other":    "{3}",
	}
	for k, v := range want {
		if row[k] != v {
			t.Errorf("%s = %#v, want %#v", k, row[k], v)
		}
	}
}

func commonTables() []string {
	return []string{"categories", "comments", "files", "posts", "sessions", "settings", "tags", "users"}
}

func TestCommonMigrationsApplyAndAreRerunnable(t *testing.T) {
	db := newTestDB(t)
	all := CommonMigrations{}.All()
	if len(all) != len(commonTables()) {
		t.Fatalf("All() has %d migrations, want %d", len(all), len(commonTables()))
	}
	if err := db.AutoMigrate(all...); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	// Apps run their migrations on every boot; the second boot must not fail.
	if err := db.AutoMigrate(all...); err != nil {
		t.Fatalf("AutoMigrate on an already migrated database: %v", err)
	}

	info, err := NewBackup(db).GetTableInfo()
	if err != nil {
		t.Fatalf("GetTableInfo: %v", err)
	}
	var tables []string
	for _, row := range info {
		name, ok := row["name"].(string)
		if !ok {
			t.Fatalf("table name %#v is not a string", row["name"])
		}
		if name != "sqlite_sequence" {
			tables = append(tables, name)
		}
	}
	if strings.Join(tables, ",") != strings.Join(commonTables(), ",") {
		t.Fatalf("tables = %v, want %v (sorted)", tables, commonTables())
	}

	rows, err := db.List("SELECT name FROM sqlite_master WHERE type = 'index' AND name LIKE 'idx_%'")
	if err != nil {
		t.Fatal(err)
	}
	var indexes []string
	for _, r := range rows {
		indexes = append(indexes, r["name"].(string))
	}
	sort.Strings(indexes)
	wantIdx := []string{"idx_categories_slug", "idx_posts_slug", "idx_sessions_token", "idx_settings_key", "idx_tags_slug"}
	if strings.Join(indexes, ",") != strings.Join(wantIdx, ",") {
		t.Fatalf("indexes = %v, want %v", indexes, wantIdx)
	}

	// Column defaults and constraints the migrations promise.
	id, err := db.Create("users", map[string]any{"email": "a@example.com", "password_hash": "h", "name": "A"})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	u, err := db.GetMap("SELECT active, role, created_at FROM users WHERE id = ?", id)
	if err != nil {
		t.Fatal(err)
	}
	if u["active"] != int64(1) || u["role"] != "user" || u["created_at"] == nil {
		t.Fatalf("user defaults = %#v", u)
	}
	if _, err := db.Create("users", map[string]any{"email": "a@example.com", "password_hash": "h", "name": "B"}); err == nil {
		t.Fatal("duplicate email accepted; UNIQUE missing")
	}
	if _, err := db.Create("users", map[string]any{"email": "b@example.com", "name": "B"}); err == nil {
		t.Fatal("missing password_hash accepted; NOT NULL missing")
	}
	if _, err := db.Create("sessions", map[string]any{"token": "t", "user_id": 999, "expires_at": "2030-01-01"}); err == nil {
		t.Fatal("session for a missing user accepted; foreign key missing")
	}
	if _, err := db.Create("sessions", map[string]any{"token": "t", "user_id": id, "expires_at": "2030-01-01"}); err != nil {
		t.Fatalf("valid session: %v", err)
	}
}

func TestAutoMigrateStopsAtTheFirstFailure(t *testing.T) {
	db := newTestDB(t)
	err := db.AutoMigrate(
		"CREATE TABLE a (id INTEGER PRIMARY KEY)",
		"CREATE TABLE broken (",
		"CREATE TABLE c (id INTEGER PRIMARY KEY)",
	)
	if err == nil || !strings.Contains(err.Error(), "migration failed") {
		t.Fatalf("AutoMigrate = %v, want a wrapped migration error", err)
	}
	if ok, err := db.Exists("sqlite_master", "name = ?", "c"); err != nil || ok {
		t.Fatalf("migration after the failure ran: %v, %v", ok, err)
	}
	if ok, err := db.Exists("sqlite_master", "name = ?", "a"); err != nil || !ok {
		t.Fatalf("migration before the failure missing: %v, %v", ok, err)
	}
}

const strongPassword = "Quartz!Lantern-42"

func TestSeedAdminUser(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(CommonMigrations{}.Users()); err != nil {
		t.Fatal(err)
	}
	seed := NewSeed(db)

	if err := seed.AdminUser("admin@example.com", "short", "Admin"); err == nil {
		t.Fatal("weak admin password accepted")
	}
	if n, err := db.Count("users", ""); err != nil || n != 0 {
		t.Fatalf("rejected seed wrote %d users, %v", n, err)
	}

	if err := seed.AdminUser("admin@example.com", strongPassword, "Admin"); err != nil {
		t.Fatalf("AdminUser: %v", err)
	}
	// Idempotent: a second run is a no-op, not a UNIQUE violation.
	if err := seed.AdminUser("admin@example.com", strongPassword, "Admin"); err != nil {
		t.Fatalf("AdminUser second run: %v", err)
	}

	row, err := db.GetMap("SELECT email, name, role, active, password_hash FROM users")
	if err != nil {
		t.Fatal(err)
	}
	if row["role"] != "admin" || row["active"] != int64(1) || row["name"] != "Admin" {
		t.Fatalf("admin row = %#v", row)
	}
	hash, ok := row["password_hash"].(string)
	if !ok || hash == strongPassword {
		t.Fatalf("password stored unhashed: %#v", row["password_hash"])
	}
	if !security.CheckPasswordHash(strongPassword, hash) {
		t.Fatal("stored hash does not verify the seeded password")
	}

	if err := NewSeed(newTestDB(t)).AdminUser("a@example.com", strongPassword, "A"); err == nil {
		t.Fatal("AdminUser without a users table returned no error")
	}
}

func TestSeedDefaultSettings(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(CommonMigrations{}.Settings()); err != nil {
		t.Fatal(err)
	}
	seed := NewSeed(db)
	if err := seed.DefaultSettings(); err != nil {
		t.Fatalf("DefaultSettings: %v", err)
	}
	// An operator-edited value survives a re-seed.
	if _, err := db.Exec("UPDATE settings SET value = ? WHERE key = ?", "My Site", "site_title"); err != nil {
		t.Fatal(err)
	}
	if err := seed.DefaultSettings(); err != nil {
		t.Fatalf("DefaultSettings second run: %v", err)
	}
	if n, err := db.Count("settings", ""); err != nil || n != 5 {
		t.Fatalf("settings = %d, %v; want 5", n, err)
	}
	var title string
	if err := db.Get(&title, "SELECT value FROM settings WHERE key = ?", "site_title"); err != nil || title != "My Site" {
		t.Fatalf("site_title = %q, %v; want the edited value", title, err)
	}

	if err := NewSeed(newTestDB(t)).DefaultSettings(); err == nil {
		t.Fatal("DefaultSettings without a settings table returned no error")
	}
}
