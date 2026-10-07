package database

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"sync"
)

// GetStruct runs query and scans the first row into a T, which must be a
// struct. Columns map to fields by db tag, else by field name; a column with
// no field is discarded. It returns ErrNoRows when there is no row.
func GetStruct[T any](db *DB, query string, args ...any) (T, error) {
	return GetStructContext[T](context.Background(), db, query, args...)
}

// GetStructContext is GetStruct bound to ctx, usually the request's.
func GetStructContext[T any](ctx context.Context, db *DB, query string, args ...any) (result T, err error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return result, err
	}
	defer closeRows(rows, &err)

	if !rows.Next() {
		return result, noRowErr(rows)
	}
	if err := scanIntoStruct(rows, &result); err != nil {
		return result, err
	}
	return result, nil
}

// ListStruct runs query and scans every row into a T, as GetStruct does. No
// rows gives a nil slice.
func ListStruct[T any](db *DB, query string, args ...any) ([]T, error) {
	return ListStructContext[T](context.Background(), db, query, args...)
}

// ListStructContext is ListStruct bound to ctx, usually the request's.
func ListStructContext[T any](ctx context.Context, db *DB, query string, args ...any) (results []T, err error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows, &err)

	for rows.Next() {
		var item T
		if err := scanIntoStruct(rows, &item); err != nil {
			return nil, err
		}
		results = append(results, item)
	}
	return results, rows.Err()
}

// closeRows closes rows and reports a close failure through err when the
// query itself succeeded.
func closeRows(rows *sql.Rows, err *error) {
	if cerr := rows.Close(); cerr != nil && *err == nil {
		*err = cerr
	}
}

// GetStructTx is GetStruct inside tx.
func GetStructTx[T any](tx *Tx, query string, args ...any) (result T, err error) {
	rows, err := tx.Query(query, args...)
	if err != nil {
		return result, err
	}
	defer closeRows(rows, &err)

	if !rows.Next() {
		return result, noRowErr(rows)
	}

	if err := scanIntoStruct(rows, &result); err != nil {
		return result, err
	}

	return result, nil
}

// ListStructTx is ListStruct inside tx.
func ListStructTx[T any](tx *Tx, query string, args ...any) (results []T, err error) {
	rows, err := tx.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows, &err)

	for rows.Next() {
		var item T
		if err := scanIntoStruct(rows, &item); err != nil {
			return nil, err
		}
		results = append(results, item)
	}

	return results, rows.Err()
}

// fieldMaps caches structFields per struct type, so ListStruct reflects over a
// type once rather than once per row.
var fieldMaps sync.Map // reflect.Type -> map[string]int

// structFields maps each column name to the index of the exported field that
// receives it: the field's db tag, else its name. Fields tagged "-" and
// unexported fields get no entry, so their columns are read and discarded.
func structFields(t reflect.Type) map[string]int {
	if cached, ok := fieldMaps.Load(t); ok {
		if fields, ok := cached.(map[string]int); ok {
			return fields
		}
	}
	fields := make(map[string]int, t.NumField())
	for i := range t.NumField() {
		field := t.Field(i)
		if !field.IsExported() {
			continue
		}
		tag := field.Tag.Get("db")
		if tag == "" {
			tag = field.Name
		}
		if tag == "-" {
			continue
		}
		fields[tag] = i
	}
	// The map is never written after this point, so callers that lose the
	// race to store it can read either copy.
	fieldMaps.Store(t, fields)
	return fields
}

// scanIntoStruct scans the current row into the struct dest points to.
func scanIntoStruct(rows *sql.Rows, dest any) error {
	columns, err := rows.Columns()
	if err != nil {
		return err
	}

	v := reflect.ValueOf(dest)
	if v.Kind() != reflect.Pointer || v.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("destination must be a pointer to a struct")
	}
	structValue := v.Elem()
	fields := structFields(structValue.Type())

	scanDests := make([]any, len(columns))
	for i, col := range columns {
		if index, ok := fields[col]; ok {
			scanDests[i] = structValue.Field(index).Addr().Interface()
		} else {
			// No field takes this column: read it and discard it.
			var discard any
			scanDests[i] = &discard
		}
	}

	return rows.Scan(scanDests...)
}

// Model reads rows of one table into T with the struct scanning of GetStruct.
type Model[T any] struct {
	db    *DB
	table string
}

// NewModel returns a Model for table. The table name is interpolated into
// queries (identifiers cannot be bound as parameters), so it must be a plain
// identifier; anything else panics.
func NewModel[T any](db *DB, table string) *Model[T] {
	mustValidateIdentifier("table", table)
	return &Model[T]{
		db:    db,
		table: table,
	}
}

// Get returns the row whose id column equals id, or ErrNoRows.
func (m *Model[T]) Get(id any) (T, error) {
	query := fmt.Sprintf("SELECT * FROM %s WHERE id = ?", m.table)
	return GetStruct[T](m.db, query, id)
}

// List returns every row of the table.
func (m *Model[T]) List() ([]T, error) {
	query := fmt.Sprintf("SELECT * FROM %s", m.table)
	return ListStruct[T](m.db, query)
}

// Where returns the rows matching condition.
//
// SECURITY: condition is concatenated verbatim into the SQL and is NOT
// validated. It MUST be a developer-controlled, static SQL fragment with "?"
// placeholders for user-supplied values, which go in args and are bound as
// parameters. Never interpolate user input into condition.
func (m *Model[T]) Where(condition string, args ...any) ([]T, error) {
	query := fmt.Sprintf("SELECT * FROM %s WHERE %s", m.table, condition)
	return ListStruct[T](m.db, query, args...)
}

// First returns the first row matching condition, or ErrNoRows. condition
// follows the Where contract.
func (m *Model[T]) First(condition string, args ...any) (T, error) {
	query := fmt.Sprintf("SELECT * FROM %s WHERE %s LIMIT 1", m.table, condition)
	return GetStruct[T](m.db, query, args...)
}

// Count returns the number of rows in the table.
func (m *Model[T]) Count() (int, error) {
	return m.db.Count(m.table, "")
}

// CountWhere returns the number of rows matching condition, which follows
// the Where contract.
func (m *Model[T]) CountWhere(condition string, args ...any) (int, error) {
	return m.db.Count(m.table, condition, args...)
}

// Exists reports whether the table has any row.
func (m *Model[T]) Exists() (bool, error) {
	return m.db.Exists(m.table, "")
}

// ExistsWhere reports whether any row matches condition, which follows the
// Where contract.
func (m *Model[T]) ExistsWhere(condition string, args ...any) (bool, error) {
	return m.db.Exists(m.table, condition, args...)
}
