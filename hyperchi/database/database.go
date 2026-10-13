// Package database wraps database/sql for SQLite, using the pure-Go
// modernc.org/sqlite driver. NewDB applies PocketBase-style connection PRAGMAs
// (WAL, foreign keys, busy timeout) to every pooled connection, and the
// package adds CRUD helpers, a SELECT builder, pagination, migrations, and
// struct scanning.
//
// Query builder:
//
//	query, args := Select("id", "name", "email").
//		From("users").
//		Where("active = ?", true).
//		And("age > ?", 18).
//		OrderBy("name").
//		Page(1, 10).
//		Build()
//
// Pagination:
//
//	paginator, results, err := Paginate(db, "users", 1, 10, "active = ?", true)
//	if err != nil {
//		return err
//	}
//	// paginator holds Page, PerPage, Total, TotalPages, HasNext, HasPrev
//
// Uniqueness check, typically from a bound struct's Validate method:
//
//	taken, err := IsTaken(db, "users", "email", email)
//	if err != nil {
//		return err
//	}
//	if taken {
//		return errors.New("that email is already registered")
//	}
//
// Unique slug:
//
//	slug, err := SlugGenerator{}.UniqueSlug("My Blog Post Title", "posts", "slug", db)
//	if err != nil {
//		return err
//	}
//	// slug is "my-blog-post-title", or "my-blog-post-title-2" if that is taken
package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite" // registers the pure-Go "sqlite" driver
)

// ErrNoRows is returned when a query that expects a row finds none. It is
// sql.ErrNoRows, so errors.Is matches either, and a caller checking for "not
// found" need not import database/sql to name it.
var ErrNoRows = sql.ErrNoRows

// noRowErr is the error for a single-row read whose rows.Next returned false.
// Next also returns false when stepping the query failed, so the iteration
// error wins: reporting a failed query as ErrNoRows turns a 500 into a 404.
func noRowErr(rows *sql.Rows) error {
	if err := rows.Err(); err != nil {
		return err
	}
	return ErrNoRows
}

// sqliteDriverName is the name modernc.org/sqlite registers itself under. The
// driver is pure Go, so the framework builds with CGO_ENABLED=0.
const sqliteDriverName = "sqlite"

// connectionPragma is one PocketBase-style PRAGMA, passed in the DSN so the
// driver applies it to every pooled connection: database/sql opens connections
// lazily, and most of these (foreign_keys, busy_timeout, synchronous,
// cache_size, ...) are per-connection state that a one-off Exec on the pool
// would leave unset on the others.
type connectionPragma struct {
	name  string // PRAGMA name
	key   string // the driver's validated shorthand DSN key, or "" for _pragma
	value string
}

// connectionPragmas are the defaults NewDB adds to the DSN. A DSN that sets the
// same PRAGMA itself keeps its own value, and verifyPragmas still fails the
// open if that turns WAL or foreign keys off.
//
// The driver applies DSN keys in a fixed order, not the order written here:
// _busy_timeout and _auto_vacuum first, then the _pragma values sorted by text,
// then _foreign_keys, _journal_mode, and _synchronous. auto_vacuum is file-level
// and only takes effect before the first table exists, so it uses the shorthand
// key that runs first; journal_mode uses the one that runs after every _pragma.
// Applying them at connect time keeps them correct when a Lazy database is
// created by its first query. Setting auto_vacuum also fixes the page size of a
// new database, so page_size(4096) restates SQLite's default and a DSN cannot
// pick another page size here: create such a database outside NewDB first.
var connectionPragmas = []connectionPragma{
	{"auto_vacuum", "_auto_vacuum", "INCREMENTAL"}, // incremental vacuum (new databases only)
	{"page_size", "", "4096"},                      // 4KB pages (new databases only)
	{"cache_size", "", "-64000"},                   // 64MB page cache
	{"temp_store", "", "MEMORY"},                   // keep temp tables/indices in memory
	{"mmap_size", "", "268435456"},                 // 256MB memory-mapped I/O
	{"wal_autocheckpoint", "", "1000"},             // checkpoint every 1000 pages
	{"foreign_keys", "_foreign_keys", "1"},         // enforce FK constraints (SQLite defaults to off)
	{"journal_mode", "_journal_mode", "WAL"},       // better write concurrency
	{"synchronous", "_synchronous", "NORMAL"},      // balance durability and speed under WAL
}

// dsnPragmaKeys maps each shorthand DSN key the driver accepts, aliases
// included, to the PRAGMA it sets.
var dsnPragmaKeys = map[string]string{
	"_busy_timeout": "busy_timeout",
	"_timeout":      "busy_timeout",
	"_foreign_keys": "foreign_keys",
	"_fk":           "foreign_keys",
	"_journal_mode": "journal_mode",
	"_journal":      "journal_mode",
	"_synchronous":  "synchronous",
	"_sync":         "synchronous",
	"_auto_vacuum":  "auto_vacuum",
	"_vacuum":       "auto_vacuum",
	"_query_only":   "query_only",
}

// pragmaName returns the lowercased PRAGMA name of a _pragma DSN value such as
// "busy_timeout(5000)" or "journal_mode = WAL".
func pragmaName(v string) string {
	name, _, _ := strings.Cut(v, "(")
	name, _, _ = strings.Cut(name, "=")
	return strings.ToLower(strings.TrimSpace(name))
}

// buildDSN returns path with the framework's DSN options merged into whatever
// query the caller supplied. Timeout always sets busy_timeout, replacing any
// value in the DSN; every other default applies only where the DSN is silent.
// Times are written in SQLite's own text format (_time_format=sqlite) unless
// the DSN picks a format, so SQLite's date functions can read stored values.
func buildDSN(path string, timeout time.Duration) (string, error) {
	base, rawQuery, _ := strings.Cut(path, "?")
	query, err := url.ParseQuery(rawQuery)
	if err != nil {
		return "", fmt.Errorf("database DSN: %w", err)
	}

	set := map[string]bool{}
	for key, pragma := range dsnPragmaKeys {
		if query.Has(key) {
			set[pragma] = true
		}
	}
	var pragmas []string
	for _, v := range query["_pragma"] {
		name := pragmaName(v)
		if name == "busy_timeout" {
			continue
		}
		set[name] = true
		pragmas = append(pragmas, v)
	}
	query.Del("_pragma")
	query.Del("_timeout")

	millis := max(int64(1), timeout.Milliseconds())
	query.Set("_busy_timeout", strconv.FormatInt(millis, 10))
	for _, p := range connectionPragmas {
		if set[p.name] {
			continue
		}
		if p.key != "" {
			query.Set(p.key, p.value)
		} else {
			pragmas = append(pragmas, p.name+"("+p.value+")")
		}
	}
	query["_pragma"] = pragmas
	if !query.Has("_time_format") && !query.Has("_time_integer_format") {
		query.Set("_time_format", "sqlite")
	}
	return base + "?" + query.Encode(), nil
}

// DBConfig configures NewDB. The defaults noted below are DefaultDBConfig's;
// NewDB also treats a zero Timeout as 30s.
type DBConfig struct {
	Path         string        // Database file path, optionally with DSN options after "?" (default: "./app.db")
	Timeout      time.Duration // Startup and SQLite busy timeout (default: 30s)
	MaxOpenConns int           // Max open connections (default: 10)
	MaxIdleConns int           // Max idle connections (default: 5)
	Debug        bool          // Log every statement before it runs
	// Lazy defers creating the database file until the first query, so an app
	// that never uses the database never creates one. A path that is a
	// directory still fails here, and an existing file is pinged and verified
	// up front as without Lazy.
	Lazy bool
}

// DefaultDBConfig returns the configuration NewDB uses when given nil.
func DefaultDBConfig() *DBConfig {
	return &DBConfig{
		Path:         "./app.db",
		Timeout:      30 * time.Second,
		MaxOpenConns: 10,
		MaxIdleConns: 5,
		Debug:        false,
	}
}

// DB is a SQLite connection pool opened by NewDB. It is safe for concurrent use.
type DB struct {
	conn   *sql.DB
	config *DBConfig
	mu     sync.RWMutex
	logger *log.Logger
}

// NewDB opens the SQLite database at config.Path, creating its directory, with
// the connection PRAGMAs set on every pooled connection. It pings the database
// and returns an error if foreign keys or WAL did not take effect; with Lazy
// and no file yet, both checks wait and the first query creates the file. A
// nil config uses DefaultDBConfig.
func NewDB(config *DBConfig) (*DB, error) {
	if config == nil {
		config = DefaultDBConfig()
	}

	owned := *config
	config = &owned
	if config.Timeout == 0 {
		config.Timeout = 30 * time.Second
	}
	if config.Timeout < 0 {
		return nil, fmt.Errorf("database timeout cannot be negative")
	}

	dir := filepath.Dir(config.Path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create database directory: %w", err)
	}

	dsn, err := buildDSN(config.Path, config.Timeout)
	if err != nil {
		return nil, err
	}
	conn, err := sql.Open(sqliteDriverName, dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	conn.SetMaxOpenConns(config.MaxOpenConns)
	conn.SetMaxIdleConns(config.MaxIdleConns)
	conn.SetConnMaxLifetime(time.Hour)
	conn.SetConnMaxIdleTime(5 * time.Minute)

	db := &DB{
		conn:   conn,
		config: config,
		logger: log.New(os.Stdout, "[DB] ", log.LstdFlags),
	}
	if config.Lazy {
		info, statErr := os.Stat(config.Path)
		switch {
		case statErr == nil && info.IsDir():
			if cerr := conn.Close(); cerr != nil {
				log.Printf("hyperchi: error closing database connection: %v", cerr)
			}
			return nil, fmt.Errorf("database path %s is a directory", config.Path)
		case errors.Is(statErr, os.ErrNotExist):
			// Nothing to verify yet; the first query creates the file.
			return db, nil
		}
		// The file exists, so checking it now creates nothing: fall through.
	}

	ctx, cancel := context.WithTimeout(context.Background(), config.Timeout)
	defer cancel()
	if err := conn.PingContext(ctx); err != nil {
		if cerr := conn.Close(); cerr != nil {
			log.Printf("hyperchi: error closing database connection after failed ping: %v", cerr)
		}
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	// Fail fast if the critical PRAGMAs did not take effect.
	if err := db.verifyPragmas(); err != nil {
		if cerr := conn.Close(); cerr != nil {
			log.Printf("hyperchi: error closing database connection after failed pragma verification: %v", cerr)
		}
		return nil, fmt.Errorf("database pragma verification failed: %w", err)
	}

	return db, nil
}

// verifyPragmas checks that foreign keys and WAL took effect, so a driver or
// DSN change that drops them fails the open instead of silently disabling
// foreign-key enforcement and write concurrency. In-memory databases skip the
// WAL check: they do not support it.
func (db *DB) verifyPragmas() error {
	var foreignKeys int
	if err := db.conn.QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		return fmt.Errorf("reading foreign_keys pragma: %w", err)
	}
	if foreignKeys != 1 {
		return fmt.Errorf("foreign_keys pragma not enabled (got %d): the DSN turned it off or connection PRAGMAs are not being applied", foreignKeys)
	}

	if !isInMemory(db.config.Path) {
		var journalMode string
		if err := db.conn.QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil {
			return fmt.Errorf("reading journal_mode pragma: %w", err)
		}
		if !strings.EqualFold(journalMode, "wal") {
			return fmt.Errorf("journal_mode is %q, expected WAL: the DSN set another mode or connection PRAGMAs are not being applied", journalMode)
		}
	}

	return nil
}

// isInMemory reports whether the configured path is a SQLite in-memory database.
func isInMemory(path string) bool {
	return path == ":memory:" || strings.HasPrefix(path, "file::memory:") || strings.Contains(path, "mode=memory")
}

// Close closes the database connection
func (db *DB) Close() error {
	db.mu.Lock()
	defer db.mu.Unlock()

	if db.conn != nil {
		return db.conn.Close()
	}
	return nil
}

// Raw returns the underlying sql.DB for calls this wrapper does not cover.
func (db *DB) Raw() *sql.DB {
	return db.conn
}

// Ping verifies the database connection is alive
func (db *DB) Ping() error {
	return db.conn.Ping()
}

// Query executes a query and returns rows
func (db *DB) Query(query string, args ...any) (*sql.Rows, error) {
	db.logQuery(query, args...)
	return db.conn.Query(query, args...)
}

// QueryRow executes a query that returns a single row
func (db *DB) QueryRow(query string, args ...any) *sql.Row {
	db.logQuery(query, args...)
	return db.conn.QueryRow(query, args...)
}

// Exec executes a query without returning rows
func (db *DB) Exec(query string, args ...any) (sql.Result, error) {
	db.logQuery(query, args...)
	return db.conn.Exec(query, args...)
}

// QueryContext is Query with cancellation/deadline support. Pass the request
// context (c.Ctx()) so a disconnected client releases its pool connection
// instead of holding it for the full query.
func (db *DB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	db.logQuery(query, args...)
	return db.conn.QueryContext(ctx, query, args...)
}

// QueryRowContext is QueryRow with cancellation/deadline support.
func (db *DB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	db.logQuery(query, args...)
	return db.conn.QueryRowContext(ctx, query, args...)
}

// ExecContext is Exec with cancellation/deadline support.
func (db *DB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	db.logQuery(query, args...)
	return db.conn.ExecContext(ctx, query, args...)
}

// PingContext is Ping with cancellation/deadline support.
func (db *DB) PingContext(ctx context.Context) error {
	return db.conn.PingContext(ctx)
}

// Get runs query and scans the first row into dest, so the query must return
// one column. It returns ErrNoRows when there is no row.
func (db *DB) Get(dest any, query string, args ...any) (err error) {
	db.logQuery(query, args...)

	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return err
	}
	defer closeRows(rows, &err)

	if !rows.Next() {
		return noRowErr(rows)
	}

	return rows.Scan(dest)
}

// GetMap runs query and returns the first row as a column-to-value map, with
// []byte values converted to strings. It returns ErrNoRows when there is no row.
func (db *DB) GetMap(query string, args ...any) (_ map[string]any, err error) {
	db.logQuery(query, args...)

	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows, &err)

	if !rows.Next() {
		return nil, noRowErr(rows)
	}

	return db.scanRowToMap(rows)
}

// List runs query and returns every row as a map, as GetMap does. No rows
// gives a nil slice.
func (db *DB) List(query string, args ...any) (_ []map[string]any, err error) {
	db.logQuery(query, args...)

	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows, &err)

	var results []map[string]any
	for rows.Next() {
		row, err := db.scanRowToMap(rows)
		if err != nil {
			return nil, err
		}
		results = append(results, row)
	}

	return results, rows.Err()
}

// scanRowToMap scans the current row into a map keyed by column name.
func (db *DB) scanRowToMap(rows *sql.Rows) (map[string]any, error) {
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	values := make([]any, len(columns))
	valuePtrs := make([]any, len(columns))
	for i := range values {
		valuePtrs[i] = &values[i]
	}

	if err := rows.Scan(valuePtrs...); err != nil {
		return nil, err
	}

	result := make(map[string]any)
	for i, col := range columns {
		val := values[i]
		if b, ok := val.([]byte); ok {
			result[col] = string(b)
		} else {
			result[col] = val
		}
	}

	return result, nil
}

// Create inserts a new record and returns the inserted ID.
//
// The table name and every column key in data are validated against the
// safe-identifier allowlist before being interpolated into SQL: these cannot be
// passed as bound placeholders, and data keys are commonly derived from decoded
// request bodies, making them an injection vector otherwise. Values are always
// bound via placeholders.
func (db *DB) Create(table string, data map[string]any) (int64, error) {
	if len(data) == 0 {
		return 0, fmt.Errorf("no data provided")
	}
	if err := validateIdentifier("table", table); err != nil {
		return 0, err
	}

	var columns []string
	var placeholders []string
	var values []any

	for column, value := range data {
		if err := validateIdentifier("column", column); err != nil {
			return 0, err
		}
		columns = append(columns, column)
		placeholders = append(placeholders, "?")
		values = append(values, value)
	}

	query := fmt.Sprintf(
		"INSERT INTO %s (%s) VALUES (%s)",
		table,
		strings.Join(columns, ", "),
		strings.Join(placeholders, ", "),
	)

	result, err := db.Exec(query, values...)
	if err != nil {
		return 0, err
	}

	return result.LastInsertId()
}

// Update updates the record with the given id.
//
// The table name and every column key in data are validated against the
// safe-identifier allowlist (see Create); values are bound via placeholders.
func (db *DB) Update(table string, id any, data map[string]any) error {
	if len(data) == 0 {
		return fmt.Errorf("no data provided")
	}
	if err := validateIdentifier("table", table); err != nil {
		return err
	}

	var setParts []string
	var values []any

	for column, value := range data {
		if err := validateIdentifier("column", column); err != nil {
			return err
		}
		setParts = append(setParts, column+" = ?")
		values = append(values, value)
	}
	values = append(values, id)

	query := fmt.Sprintf(
		"UPDATE %s SET %s WHERE id = ?",
		table,
		strings.Join(setParts, ", "),
	)

	_, err := db.Exec(query, values...)
	return err
}

// Delete deletes the record with the given id. The table name is validated
// against the safe-identifier allowlist.
func (db *DB) Delete(table string, id any) error {
	if err := validateIdentifier("table", table); err != nil {
		return err
	}
	query := fmt.Sprintf("DELETE FROM %s WHERE id = ?", table)
	_, err := db.Exec(query, id)
	return err
}

// Count returns the number of records matching the optional where condition.
//
// The table name is validated against the safe-identifier allowlist. The where
// string is raw SQL and is the caller's responsibility: it is developer-
// controlled and must use "?" placeholders for any values. Never interpolate
// user input into it.
func (db *DB) Count(table string, where string, args ...any) (int, error) {
	if err := validateIdentifier("table", table); err != nil {
		return 0, err
	}
	query := fmt.Sprintf("SELECT COUNT(*) FROM %s", table)
	if where != "" {
		query += " WHERE " + where
	}

	var count int
	err := db.Get(&count, query, args...)
	return count, err
}

// Exists checks if any records match the optional where condition. See Count
// for the table/where contract.
func (db *DB) Exists(table string, where string, args ...any) (bool, error) {
	count, err := db.Count(table, where, args...)
	return count > 0, err
}

// Tx runs fn in a transaction. It commits when fn returns nil and rolls back
// when fn returns an error or panics; the panic is re-raised after rollback.
func (db *DB) Tx(fn func(*Tx) error) (txErr error) {
	return db.TxContext(context.Background(), fn)
}

// TxContext executes a function within a transaction whose statements honor
// ctx: cancelling it rolls the transaction back.
func (db *DB) TxContext(ctx context.Context, fn func(*Tx) error) (txErr error) {
	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}

	dbTx := &Tx{
		tx:     tx,
		db:     db,
		logger: db.logger,
	}

	defer func() {
		// ErrTxDone means the transaction already ended (a failed or explicit
		// commit, an explicit rollback, a cancelled context), so there is
		// nothing left to roll back and nothing to report.
		if r := recover(); r != nil {
			if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
				db.logger.Printf("Transaction rollback failed after panic: %v", rbErr)
			}
			panic(r)
		}
		if txErr != nil {
			if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
				db.logger.Printf("Transaction rollback failed: %v", rbErr)
			}
		}
	}()

	if txErr = fn(dbTx); txErr != nil {
		return txErr
	}

	return tx.Commit()
}

// logQuery logs the statement when Debug is set. It runs before the query
// executes, so it reports no duration.
func (db *DB) logQuery(query string, args ...any) {
	if db.config.Debug {
		db.logger.Printf("Query: %s %v", query, args)
	}
}

// Migrate logs and executes one migration statement.
func (db *DB) Migrate(query string) error {
	db.logger.Printf("Running migration: %s", query)
	_, err := db.Exec(query)
	return err
}

// AutoMigrate runs each migration in order and stops at the first failure.
// Migrations are not wrapped in a transaction, so earlier ones stay applied;
// write them with IF NOT EXISTS (as Table does) so a re-run on boot succeeds.
func (db *DB) AutoMigrate(migrations ...string) error {
	for _, migration := range migrations {
		if err := db.Migrate(migration); err != nil {
			return fmt.Errorf("migration failed: %w", err)
		}
	}
	return nil
}

// Stats returns the connection pool statistics from sql.DB.Stats as a map.
func (db *DB) Stats() map[string]any {
	stats := db.conn.Stats()

	return map[string]any{
		"max_open_connections": stats.MaxOpenConnections,
		"open_connections":     stats.OpenConnections,
		"in_use":               stats.InUse,
		"idle":                 stats.Idle,
		"wait_count":           stats.WaitCount,
		"wait_duration":        stats.WaitDuration,
		"max_idle_closed":      stats.MaxIdleClosed,
		"max_idle_time_closed": stats.MaxIdleTimeClosed,
		"max_lifetime_closed":  stats.MaxLifetimeClosed,
	}
}
