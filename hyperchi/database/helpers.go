package database

import (
	"fmt"
	"strings"
	"time"

	"github.com/regiellis/hyperchi/hyperchi/security"
)

// MigrationBuilder builds a CREATE TABLE IF NOT EXISTS statement and its
// indexes. Start one with Table. Modifiers such as NotNull, Default, Unique,
// and Index apply to the column added last.
type MigrationBuilder struct {
	tableName string
	columns   []string
	options   []string
}

// Table starts a migration for the named table.
//
// Table, column, and foreign-key names are interpolated into the DDL, so each
// must be a plain identifier (letters, digits, underscores); anything else
// panics, as in QueryBuilder.From.
func Table(name string) *MigrationBuilder {
	mustValidateIdentifier("table", name)
	return &MigrationBuilder{
		tableName: name,
		columns:   []string{},
		options:   []string{},
	}
}

// ID adds an "id INTEGER PRIMARY KEY AUTOINCREMENT" column.
func (mb *MigrationBuilder) ID() *MigrationBuilder {
	mb.columns = append(mb.columns, "id INTEGER PRIMARY KEY AUTOINCREMENT")
	return mb
}

// String adds a VARCHAR(length) column, or TEXT when no positive length is
// given. SQLite does not enforce the length.
func (mb *MigrationBuilder) String(name string, length ...int) *MigrationBuilder {
	mustValidateIdentifier("column", name)
	if len(length) > 0 && length[0] > 0 {
		mb.columns = append(mb.columns, fmt.Sprintf("%s VARCHAR(%d)", name, length[0]))
	} else {
		mb.columns = append(mb.columns, fmt.Sprintf("%s TEXT", name))
	}
	return mb
}

// Text adds a TEXT column.
func (mb *MigrationBuilder) Text(name string) *MigrationBuilder {
	mustValidateIdentifier("column", name)
	mb.columns = append(mb.columns, fmt.Sprintf("%s TEXT", name))
	return mb
}

// Integer adds an INTEGER column.
func (mb *MigrationBuilder) Integer(name string) *MigrationBuilder {
	mustValidateIdentifier("column", name)
	mb.columns = append(mb.columns, fmt.Sprintf("%s INTEGER", name))
	return mb
}

// Real adds a REAL column.
func (mb *MigrationBuilder) Real(name string) *MigrationBuilder {
	mustValidateIdentifier("column", name)
	mb.columns = append(mb.columns, fmt.Sprintf("%s REAL", name))
	return mb
}

// Boolean adds an INTEGER column with DEFAULT 0, as SQLite has no boolean type.
func (mb *MigrationBuilder) Boolean(name string) *MigrationBuilder {
	mustValidateIdentifier("column", name)
	mb.columns = append(mb.columns, fmt.Sprintf("%s INTEGER DEFAULT 0", name))
	return mb
}

// Timestamp adds a DATETIME column.
func (mb *MigrationBuilder) Timestamp(name string) *MigrationBuilder {
	mustValidateIdentifier("column", name)
	mb.columns = append(mb.columns, fmt.Sprintf("%s DATETIME", name))
	return mb
}

// CreatedAt adds created_at DATETIME DEFAULT CURRENT_TIMESTAMP.
func (mb *MigrationBuilder) CreatedAt() *MigrationBuilder {
	mb.columns = append(mb.columns, "created_at DATETIME DEFAULT CURRENT_TIMESTAMP")
	return mb
}

// UpdatedAt adds updated_at DATETIME DEFAULT CURRENT_TIMESTAMP. Nothing
// updates it on UPDATE; set it when writing a row.
func (mb *MigrationBuilder) UpdatedAt() *MigrationBuilder {
	mb.columns = append(mb.columns, "updated_at DATETIME DEFAULT CURRENT_TIMESTAMP")
	return mb
}

// Timestamps adds the created_at and updated_at columns.
func (mb *MigrationBuilder) Timestamps() *MigrationBuilder {
	return mb.CreatedAt().UpdatedAt()
}

// NotNull makes the last added column NOT NULL.
func (mb *MigrationBuilder) NotNull() *MigrationBuilder {
	if len(mb.columns) > 0 {
		lastIndex := len(mb.columns) - 1
		if !strings.Contains(mb.columns[lastIndex], "NOT NULL") {
			mb.columns[lastIndex] += " NOT NULL"
		}
	}
	return mb
}

// Default sets a default value for the last added column. Strings (and any
// other non-numeric value, via its %v form) become SQL string literals with
// embedded single quotes doubled, so a value cannot end the literal early.
func (mb *MigrationBuilder) Default(value any) *MigrationBuilder {
	if len(mb.columns) > 0 {
		lastIndex := len(mb.columns) - 1
		switch v := value.(type) {
		case string:
			mb.columns[lastIndex] += " DEFAULT " + sqlStringLiteral(v)
		case int, int64, float64:
			mb.columns[lastIndex] += fmt.Sprintf(" DEFAULT %v", v)
		case bool:
			if v {
				mb.columns[lastIndex] += " DEFAULT 1"
			} else {
				mb.columns[lastIndex] += " DEFAULT 0"
			}
		default:
			mb.columns[lastIndex] += " DEFAULT " + sqlStringLiteral(fmt.Sprint(v))
		}
	}
	return mb
}

// sqlStringLiteral quotes s as a SQL string literal, doubling embedded quotes.
func sqlStringLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// Unique makes the last added column UNIQUE.
func (mb *MigrationBuilder) Unique() *MigrationBuilder {
	if len(mb.columns) > 0 {
		lastIndex := len(mb.columns) - 1
		if !strings.Contains(mb.columns[lastIndex], "UNIQUE") {
			mb.columns[lastIndex] += " UNIQUE"
		}
	}
	return mb
}

// Index adds a CREATE INDEX statement, named idx_<table>_<column>, for the
// last added column.
func (mb *MigrationBuilder) Index() *MigrationBuilder {
	if len(mb.columns) > 0 {
		parts := strings.Fields(mb.columns[len(mb.columns)-1])
		if len(parts) > 0 {
			columnName := parts[0]
			indexName := fmt.Sprintf("idx_%s_%s", mb.tableName, columnName)
			// IF NOT EXISTS matches the table's CREATE TABLE IF NOT EXISTS, so
			// a migration re-run on every boot does not fail on the index.
			mb.options = append(mb.options, fmt.Sprintf("CREATE INDEX IF NOT EXISTS %s ON %s (%s)", indexName, mb.tableName, columnName))
		}
	}
	return mb
}

// ForeignKey adds a FOREIGN KEY constraint. All three names must be plain
// identifiers; anything else panics.
func (mb *MigrationBuilder) ForeignKey(column, refTable, refColumn string) *MigrationBuilder {
	mustValidateIdentifier("foreign key column", column)
	mustValidateIdentifier("foreign key table", refTable)
	mustValidateIdentifier("foreign key referenced column", refColumn)
	constraint := fmt.Sprintf("FOREIGN KEY (%s) REFERENCES %s(%s)", column, refTable, refColumn)
	mb.options = append(mb.options, constraint)
	return mb
}

// Build returns the CREATE TABLE statement followed by its index statements,
// separated by semicolons, or "" when no column was added.
func (mb *MigrationBuilder) Build() string {
	if len(mb.columns) == 0 {
		return ""
	}

	sql := fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (\n", mb.tableName)
	sql += "  " + strings.Join(mb.columns, ",\n  ")

	for _, option := range mb.options {
		if strings.HasPrefix(option, "FOREIGN KEY") {
			sql += ",\n  " + option
		}
	}

	sql += "\n)"

	for _, option := range mb.options {
		if strings.HasPrefix(option, "CREATE INDEX") {
			sql += ";\n" + option
		}
	}

	return sql
}

// CommonMigrations returns CREATE TABLE migrations for common tables.
type CommonMigrations struct{}

// Users returns the users table migration.
func (CommonMigrations) Users() string {
	return Table("users").
		ID().
		String("email").NotNull().Unique().
		String("password_hash").NotNull().
		String("name").NotNull().
		String("avatar_url").
		Boolean("active").Default(true).
		String("role").Default("user").
		Timestamps().
		Build()
}

// Sessions returns the sessions table migration; it references users.
func (CommonMigrations) Sessions() string {
	return Table("sessions").
		ID().
		String("token", 64).NotNull().Unique().Index().
		Integer("user_id").NotNull().
		Timestamp("expires_at").NotNull().
		Timestamps().
		ForeignKey("user_id", "users", "id").
		Build()
}

// Posts returns the posts table migration; it references users.
func (CommonMigrations) Posts() string {
	return Table("posts").
		ID().
		String("title").NotNull().
		String("slug").NotNull().Unique().Index().
		Text("content").NotNull().
		Text("excerpt").
		Integer("user_id").NotNull().
		Boolean("published").Default(false).
		Timestamp("published_at").
		Timestamps().
		ForeignKey("user_id", "users", "id").
		Build()
}

// Categories returns the categories table migration.
func (CommonMigrations) Categories() string {
	return Table("categories").
		ID().
		String("name").NotNull().Unique().
		String("slug").NotNull().Unique().Index().
		Text("description").
		Integer("parent_id").
		Timestamps().
		Build()
}

// Tags returns the tags table migration.
func (CommonMigrations) Tags() string {
	return Table("tags").
		ID().
		String("name").NotNull().Unique().
		String("slug").NotNull().Unique().Index().
		Text("description").
		Timestamps().
		Build()
}

// Comments returns the comments table migration; it references users and posts.
func (CommonMigrations) Comments() string {
	return Table("comments").
		ID().
		Text("content").NotNull().
		Integer("user_id").NotNull().
		Integer("post_id").NotNull().
		Integer("parent_id"). // For threaded comments
		Boolean("approved").Default(false).
		Timestamps().
		ForeignKey("user_id", "users", "id").
		ForeignKey("post_id", "posts", "id").
		Build()
}

// Files returns the migration for a table of uploaded files; it references users.
func (CommonMigrations) Files() string {
	return Table("files").
		ID().
		String("filename").NotNull().
		String("original_name").NotNull().
		String("mime_type").NotNull().
		Integer("size").NotNull().
		String("path").NotNull().
		String("hash", 64). // For file deduplication
		Integer("user_id").
		Timestamps().
		ForeignKey("user_id", "users", "id").
		Build()
}

// Settings returns the migration for a key-value settings table.
func (CommonMigrations) Settings() string {
	return Table("settings").
		ID().
		String("key").NotNull().Unique().Index().
		Text("value").
		String("type").Default("string"). // string, int, bool, json
		Text("description").
		Timestamps().
		Build()
}

// All returns every common migration, ordered so referenced tables come first.
func (cm CommonMigrations) All() []string {
	return []string{
		cm.Users(),
		cm.Sessions(),
		cm.Categories(),
		cm.Tags(),
		cm.Posts(),
		cm.Comments(),
		cm.Files(),
		cm.Settings(),
	}
}

// Seed inserts starter rows into the CommonMigrations tables.
type Seed struct {
	db *DB
}

// NewSeed returns a Seed for db.
func NewSeed(db *DB) *Seed {
	return &Seed{db: db}
}

// AdminUser inserts an admin user with a bcrypt password hash, unless an
// admin with that email exists. It returns an error if the password fails
// security.ValidatePasswordRequirements.
func (s *Seed) AdminUser(email, password, name string) error {
	exists, err := s.db.Exists("users", "role = ? AND email = ?", "admin", email)
	if err != nil {
		return err
	}

	if exists {
		return nil
	}

	if err := security.ValidatePasswordRequirements(password); err != nil {
		return fmt.Errorf("admin password validation failed: %w", err)
	}

	passwordHash, err := security.HashPassword(password)
	if err != nil {
		return fmt.Errorf("failed to hash admin password: %w", err)
	}

	_, err = s.db.Create("users", map[string]any{
		"email":         email,
		"password_hash": passwordHash,
		"name":          name,
		"role":          "admin",
		"active":        true,
		"created_at":    time.Now(),
		"updated_at":    time.Now(),
	})

	return err
}

// DefaultSettings inserts the default site settings, skipping any key that
// already exists.
func (s *Seed) DefaultSettings() error {
	settings := map[string]any{
		"site_title":       "HyperChi Application",
		"site_description": "Built with HyperChi microframework",
		"posts_per_page":   "10",
		"allow_comments":   "true",
		"require_approval": "true",
	}

	for key, value := range settings {
		exists, err := s.db.Exists("settings", "key = ?", key)
		if err != nil {
			return err
		}

		if !exists {
			_, err = s.db.Create("settings", map[string]any{
				"key":        key,
				"value":      value,
				"created_at": time.Now(),
				"updated_at": time.Now(),
			})
			if err != nil {
				return err
			}
		}
	}

	return nil
}
