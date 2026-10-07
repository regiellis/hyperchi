package database

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/regiellis/hyperchi/hyperchi/helpers"
)

// identifierPattern matches a safe, unquoted SQL identifier: a letter or
// underscore followed by letters, digits, and underscores. It excludes spaces,
// quotes, dots, and other punctuation that could inject SQL when the
// identifier is interpolated.
var identifierPattern = regexp.MustCompile(`^` + ident + `$`)

// ident is the unquoted identifier syntax the patterns in this file share.
const ident = `[A-Za-z_][A-Za-z0-9_]*`

// selectColumnPattern matches the column forms Select accepts: "*", "t.*", a
// column or "t.column", or a COUNT/SUM/AVG/MIN/MAX/TOTAL aggregate over "*" or
// an optional DISTINCT column, each but the stars optionally followed by
// "AS alias". Keywords and function names are case-insensitive.
var selectColumnPattern = regexp.MustCompile(
	`^(?i)\s*(?:(?:` + ident + `\.)?\*|(?:(?:` + ident + `\.)?` + ident +
		`|(?:COUNT|SUM|AVG|MIN|MAX|TOTAL)\(\s*(?:\*|(?:DISTINCT\s+)?(?:` + ident + `\.)?` + ident + `)\s*\))` +
		`(?:\s+AS\s+` + ident + `)?)\s*$`)

// validateIdentifier returns an error if name is not a safe SQL identifier.
// Identifiers (table and column names) cannot be parameterized via placeholders,
// so they must be validated against a strict allowlist before being
// concatenated into a query.
func validateIdentifier(kind, name string) error {
	if !identifierPattern.MatchString(name) {
		return fmt.Errorf("invalid %s identifier %q: must match %s", kind, name, identifierPattern.String())
	}
	return nil
}

// mustValidateIdentifier panics if name is not a safe SQL identifier. The
// fluent builder methods use it because they return no error. Identifiers
// must never come directly from untrusted input.
func mustValidateIdentifier(kind, name string) {
	if err := validateIdentifier(kind, name); err != nil {
		panic(err)
	}
}

// validateOrderDirection normalizes an ORDER BY direction to "ASC" or "DESC".
// Any other value (including attempted injection) yields an error.
func validateOrderDirection(direction string) (string, error) {
	switch strings.ToUpper(strings.TrimSpace(direction)) {
	case "", "ASC":
		return "ASC", nil
	case "DESC":
		return "DESC", nil
	default:
		return "", fmt.Errorf("invalid ORDER BY direction %q: must be ASC or DESC", direction)
	}
}

// QueryBuilder builds a SELECT statement. Start one with Select. Methods that
// take an identifier panic on an unsafe one; condition strings are raw SQL
// (see Where).
type QueryBuilder struct {
	table      string
	selectCols []string
	whereCond  []string
	whereArgs  []any
	orderBy    []string
	limitVal   int
	offsetVal  int
	joins      []string
}

// Select starts a SELECT query builder; no columns means "*".
//
// Each column is interpolated into the query, so it must take one of these
// forms, or Select panics: "*", "posts.*", "name", "posts.title", an aggregate
// such as "COUNT(*)", "MAX(age)", or "COUNT(DISTINCT users.id)" (COUNT, SUM,
// AVG, MIN, MAX, TOTAL), and any of those but the stars followed by
// "AS alias". Write any other expression as raw SQL with DB.List.
func Select(columns ...string) *QueryBuilder {
	for _, col := range columns {
		if !selectColumnPattern.MatchString(col) {
			panic(fmt.Errorf("invalid SELECT column %q: want *, table.*, column, table.column, or COUNT/SUM/AVG/MIN/MAX/TOTAL(...), optionally AS alias", col))
		}
	}
	qb := &QueryBuilder{
		selectCols: columns,
	}
	if len(columns) == 0 {
		qb.selectCols = []string{"*"}
	}
	return qb
}

// From sets the table name. The name is interpolated into the query
// (identifiers cannot be bound as parameters), so it must be a plain
// identifier; anything else panics.
func (qb *QueryBuilder) From(table string) *QueryBuilder {
	mustValidateIdentifier("table", table)
	qb.table = table
	return qb
}

// Where adds a WHERE condition.
//
// SECURITY: the condition string is concatenated verbatim into the SQL and is
// NOT validated. It MUST be a developer-controlled, static SQL fragment using
// "?" placeholders for any user-supplied values; never interpolate user input
// into the condition string. Pass values via args, which are bound as
// parameters. For example:
//
//	qb.Where("status = ? AND owner_id = ?", status, ownerID)
func (qb *QueryBuilder) Where(condition string, args ...any) *QueryBuilder {
	qb.whereCond = append(qb.whereCond, condition)
	qb.whereArgs = append(qb.whereArgs, args...)
	return qb
}

// And is Where: conditions are joined with AND.
func (qb *QueryBuilder) And(condition string, args ...any) *QueryBuilder {
	return qb.Where(condition, args...)
}

// Or joins condition with OR to every condition added so far, giving
// "(previous) OR (condition)". With no earlier condition it acts as Where.
func (qb *QueryBuilder) Or(condition string, args ...any) *QueryBuilder {
	if len(qb.whereCond) > 0 {
		prevConditions := strings.Join(qb.whereCond, " AND ")
		qb.whereCond = []string{fmt.Sprintf("(%s) OR (%s)", prevConditions, condition)}
		qb.whereArgs = append(qb.whereArgs, args...)
	} else {
		qb.whereCond = append(qb.whereCond, condition)
		qb.whereArgs = append(qb.whereArgs, args...)
	}
	return qb
}

// Join adds a JOIN clause.
//
// The table is validated against the identifier allowlist, as in From; an
// invalid name panics. The ON condition follows the Where contract: a
// developer-controlled SQL fragment, never user input.
func (qb *QueryBuilder) Join(table, condition string) *QueryBuilder {
	mustValidateIdentifier("JOIN table", table)
	qb.joins = append(qb.joins, fmt.Sprintf("JOIN %s ON %s", table, condition))
	return qb
}

// LeftJoin adds a LEFT JOIN clause. See Join for the table/condition contract.
func (qb *QueryBuilder) LeftJoin(table, condition string) *QueryBuilder {
	mustValidateIdentifier("LEFT JOIN table", table)
	qb.joins = append(qb.joins, fmt.Sprintf("LEFT JOIN %s ON %s", table, condition))
	return qb
}

// OrderBy adds an ORDER BY clause.
//
// The column and direction are concatenated directly into the SQL (they cannot
// be parameterized), and ORDER BY clauses are commonly driven by user-supplied
// sort parameters. Both are therefore validated against a strict allowlist:
// the column must be a plain SQL identifier and the direction must be ASC or
// DESC. Invalid input panics with a clear message rather than producing an
// injectable query.
func (qb *QueryBuilder) OrderBy(column string, direction ...string) *QueryBuilder {
	mustValidateIdentifier("ORDER BY column", column)

	rawDir := ""
	if len(direction) > 0 {
		rawDir = direction[0]
	}
	dir, err := validateOrderDirection(rawDir)
	if err != nil {
		panic(err)
	}

	qb.orderBy = append(qb.orderBy, fmt.Sprintf("%s %s", column, dir))
	return qb
}

// Limit sets the LIMIT; zero or less means none.
func (qb *QueryBuilder) Limit(limit int) *QueryBuilder {
	qb.limitVal = limit
	return qb
}

// Offset sets the OFFSET; zero or less means none.
func (qb *QueryBuilder) Offset(offset int) *QueryBuilder {
	qb.offsetVal = offset
	return qb
}

// Page sets the limit and offset for a 1-indexed page, replacing any Limit
// or Offset. A page below 1 is treated as 1.
func (qb *QueryBuilder) Page(page, perPage int) *QueryBuilder {
	if page < 1 {
		page = 1
	}
	qb.limitVal = perPage
	qb.offsetVal = (page - 1) * perPage
	return qb
}

// Build returns the SQL and its bound arguments. It returns "" and nil when
// From was never called.
func (qb *QueryBuilder) Build() (string, []any) {
	if qb.table == "" {
		return "", nil
	}

	query := fmt.Sprintf("SELECT %s FROM %s", strings.Join(qb.selectCols, ", "), qb.table)

	if len(qb.joins) > 0 {
		query += " " + strings.Join(qb.joins, " ")
	}

	if len(qb.whereCond) > 0 {
		query += " WHERE " + strings.Join(qb.whereCond, " AND ")
	}

	if len(qb.orderBy) > 0 {
		query += " ORDER BY " + strings.Join(qb.orderBy, ", ")
	}

	// SQLite only accepts OFFSET after a LIMIT, and LIMIT -1 means "no
	// limit", so an offset alone still gets one.
	if qb.limitVal > 0 {
		query += fmt.Sprintf(" LIMIT %d", qb.limitVal)
	} else if qb.offsetVal > 0 {
		query += " LIMIT -1"
	}
	if qb.offsetVal > 0 {
		query += fmt.Sprintf(" OFFSET %d", qb.offsetVal)
	}

	return query, qb.whereArgs
}

// Paginator is page metadata, as Paginate returns it.
type Paginator struct {
	Page       int  `json:"page"`
	PerPage    int  `json:"per_page"`
	Total      int  `json:"total"`
	TotalPages int  `json:"total_pages"`
	HasNext    bool `json:"has_next"`
	HasPrev    bool `json:"has_prev"`
}

// NewPaginator computes the metadata for page of total rows. A page below 1
// becomes 1, a perPage below 1 becomes 10, and TotalPages is at least 1.
func NewPaginator(page, perPage, total int) *Paginator {
	if page < 1 {
		page = 1
	}
	if perPage < 1 {
		perPage = 10
	}

	totalPages := (total + perPage - 1) / perPage
	if totalPages < 1 {
		totalPages = 1
	}

	return &Paginator{
		Page:       page,
		PerPage:    perPage,
		Total:      total,
		TotalPages: totalPages,
		HasNext:    page < totalPages,
		HasPrev:    page > 1,
	}
}

// Offset returns the row offset of the current page.
func (p *Paginator) Offset() int {
	return (p.Page - 1) * p.PerPage
}

// Paginate returns one page of table's rows, ordered by rowid, with its
// metadata. table and where follow the DB.Count contract: where is raw SQL
// with "?" placeholders, or "" for every row.
func Paginate(db *DB, table string, page, perPage int, where string, args ...any) (*Paginator, []map[string]any, error) {
	total, err := db.Count(table, where, args...)
	if err != nil {
		return nil, nil, err
	}

	paginator := NewPaginator(page, perPage, total)

	qb := Select().From(table)
	if where != "" {
		qb.Where(where, args...)
	}
	// Order by rowid (insertion order for an ordinary table): without an ORDER
	// BY, SQLite returns rows in whatever order the chosen index gives, so pages
	// could repeat or skip rows. Use the query builder for another order.
	qb.OrderBy("rowid")
	// Use the paginator's normalized page size: a raw perPage below 1 would
	// drop the LIMIT and return every row under metadata that says 10.
	qb.Limit(paginator.PerPage).Offset(paginator.Offset())

	query, queryArgs := qb.Build()
	results, err := db.List(query, queryArgs...)
	if err != nil {
		return nil, nil, err
	}

	return paginator, results, nil
}

// SlugGenerator is the database-aware wrapper around helpers.Slugify: Generate
// applies Slugify's rule, and UniqueSlug adds the numeric suffix a unique
// column needs.
type SlugGenerator struct{}

// Generate creates a URL-friendly slug from text with helpers.Slugify, so
// separators, case handling ("iPhone" to "iphone"), and transliteration
// ("Crème" to "creme") match the rest of the framework.
func (SlugGenerator) Generate(text string) string {
	return helpers.Slugify(text)
}

// UniqueSlug returns the slug for text, with "-2", "-3", ... appended until no
// row in table has it in column, ignoring the row with id excludeID when one
// is given (for updates). A database error is returned rather than treated as
// "slug is free". column must be a plain identifier; anything else panics, as
// it is a programmer error.
func (sg SlugGenerator) UniqueSlug(text, table, column string, db *DB, excludeID ...any) (string, error) {
	mustValidateIdentifier("column", column)

	baseSlug := sg.Generate(text)
	slug := baseSlug
	for counter := 2; ; counter++ {
		taken, err := IsTaken(db, table, column, slug, excludeID...)
		if err != nil {
			return "", fmt.Errorf("checking slug %q: %w", slug, err)
		}
		if !taken {
			return slug, nil
		}
		slug = fmt.Sprintf("%s-%d", baseSlug, counter)
	}
}

// DateHelper formats and parses times in SQLite's text layout,
// "2006-01-02 15:04:05".
type DateHelper struct{}

// Format formats t's wall clock in that layout; the zone is not written.
func (DateHelper) Format(t time.Time) string {
	return t.Format("2006-01-02 15:04:05")
}

// Parse parses a datetime in that layout; the result is in UTC.
func (DateHelper) Parse(datetime string) (time.Time, error) {
	return time.Parse("2006-01-02 15:04:05", datetime)
}

// Now returns the current local time in that layout.
func (DateHelper) Now() string {
	return time.Now().Format("2006-01-02 15:04:05")
}

// Today returns today's local date as "2006-01-02".
func (DateHelper) Today() string {
	return time.Now().Format("2006-01-02")
}

// Backup copies and inspects a database.
type Backup struct {
	db *DB
}

// NewBackup returns a Backup for db.
func NewBackup(db *DB) *Backup {
	return &Backup{db: db}
}

// Create creates a backup of the database using SQLite's VACUUM INTO.
//
// The destination path is a SQL string literal here (VACUUM INTO does not accept
// a bound parameter), so embedded single quotes are escaped by doubling to
// prevent the path from breaking out of the literal.
func (b *Backup) Create(backupPath string) error {
	escaped := strings.ReplaceAll(backupPath, "'", "''")
	query := fmt.Sprintf("VACUUM INTO '%s'", escaped)
	_, err := b.db.Exec(query)
	return err
}

// GetSize returns the database size in bytes, page count times page size.
// It does not include the WAL file.
func (b *Backup) GetSize() (int64, error) {
	var size int64
	err := b.db.Get(&size, "SELECT page_count * page_size as size FROM pragma_page_count(), pragma_page_size()")
	return size, err
}

// GetTableInfo returns the name and CREATE statement of every table, sorted
// by name.
func (b *Backup) GetTableInfo() ([]map[string]any, error) {
	return b.db.List("SELECT name, sql FROM sqlite_master WHERE type='table' ORDER BY name")
}

// IsTaken reports whether table already has a row whose column equals value,
// ignoring the row with id excludeID when one is given (for updates). Field
// rules (required, length, format) belong in validate tags on the struct that
// hyperchi.Bind fills; this covers the one check that needs the database.
func IsTaken(db *DB, table, column, value string, excludeID ...any) (bool, error) {
	return IsTakenContext(context.Background(), db, table, column, value, excludeID...)
}

// IsTakenContext is IsTaken bound to ctx, usually the request's (c.Ctx()), so
// a cancelled request stops the query.
func IsTakenContext(ctx context.Context, db *DB, table, column, value string, excludeID ...any) (bool, error) {
	if err := validateIdentifier("table", table); err != nil {
		return false, err
	}
	if err := validateIdentifier("column", column); err != nil {
		return false, err
	}
	query := fmt.Sprintf("SELECT EXISTS(SELECT 1 FROM %s WHERE %s = ?", table, column)
	args := []any{value}
	if len(excludeID) > 0 && excludeID[0] != nil {
		query += " AND id != ?"
		args = append(args, excludeID[0])
	}
	query += ")"
	var taken bool
	if err := db.QueryRowContext(ctx, query, args...).Scan(&taken); err != nil {
		return false, err
	}
	return taken, nil
}
