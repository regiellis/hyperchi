package database

import (
	"database/sql"
	"fmt"
	"log"
	"strings"
)

// Tx is a transaction started by DB.Tx or DB.TxContext. Its methods mirror
// DB's and run inside the transaction. Like sql.Tx, it is not meant for use
// from several goroutines at once.
type Tx struct {
	tx     *sql.Tx
	db     *DB
	logger *log.Logger
}

// Query runs a query in the transaction.
func (tx *Tx) Query(query string, args ...any) (*sql.Rows, error) {
	tx.logQuery(query, args...)
	return tx.tx.Query(query, args...)
}

// QueryRow runs a query that returns at most one row in the transaction.
func (tx *Tx) QueryRow(query string, args ...any) *sql.Row {
	tx.logQuery(query, args...)
	return tx.tx.QueryRow(query, args...)
}

// Exec runs a statement that returns no rows in the transaction.
func (tx *Tx) Exec(query string, args ...any) (sql.Result, error) {
	tx.logQuery(query, args...)
	return tx.tx.Exec(query, args...)
}

// Get is DB.Get in the transaction.
func (tx *Tx) Get(dest any, query string, args ...any) (err error) {
	tx.logQuery(query, args...)

	rows, err := tx.tx.Query(query, args...)
	if err != nil {
		return err
	}
	defer closeRows(rows, &err)

	if !rows.Next() {
		return noRowErr(rows)
	}

	return rows.Scan(dest)
}

// GetMap is DB.GetMap in the transaction.
func (tx *Tx) GetMap(query string, args ...any) (_ map[string]any, err error) {
	tx.logQuery(query, args...)

	rows, err := tx.tx.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows, &err)

	if !rows.Next() {
		return nil, noRowErr(rows)
	}

	return tx.scanRowToMap(rows)
}

// List is DB.List in the transaction.
func (tx *Tx) List(query string, args ...any) (_ []map[string]any, err error) {
	tx.logQuery(query, args...)

	rows, err := tx.tx.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows, &err)

	var results []map[string]any
	for rows.Next() {
		row, err := tx.scanRowToMap(rows)
		if err != nil {
			return nil, err
		}
		results = append(results, row)
	}

	return results, rows.Err()
}

// scanRowToMap scans the current row into a map, as DB.scanRowToMap does.
func (tx *Tx) scanRowToMap(rows *sql.Rows) (map[string]any, error) {
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

// Create is DB.Create in the transaction, with the same identifier checks.
func (tx *Tx) Create(table string, data map[string]any) (int64, error) {
	if err := validateIdentifier("table", table); err != nil {
		return 0, err
	}
	if len(data) == 0 {
		return 0, fmt.Errorf("no data provided")
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

	result, err := tx.Exec(query, values...)
	if err != nil {
		return 0, err
	}

	return result.LastInsertId()
}

// Update is DB.Update in the transaction, with the same identifier checks.
func (tx *Tx) Update(table string, id any, data map[string]any) error {
	if err := validateIdentifier("table", table); err != nil {
		return err
	}
	if len(data) == 0 {
		return fmt.Errorf("no data provided")
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

	_, err := tx.Exec(query, values...)
	return err
}

// Delete is DB.Delete in the transaction.
func (tx *Tx) Delete(table string, id any) error {
	if err := validateIdentifier("table", table); err != nil {
		return err
	}
	query := fmt.Sprintf("DELETE FROM %s WHERE id = ?", table)
	_, err := tx.Exec(query, id)
	return err
}

// Count is DB.Count in the transaction; where is raw SQL, as there.
func (tx *Tx) Count(table string, where string, args ...any) (int, error) {
	if err := validateIdentifier("table", table); err != nil {
		return 0, err
	}
	query := fmt.Sprintf("SELECT COUNT(*) FROM %s", table)
	if where != "" {
		query += " WHERE " + where
	}

	var count int
	err := tx.Get(&count, query, args...)
	return count, err
}

// Exists is DB.Exists in the transaction.
func (tx *Tx) Exists(table string, where string, args ...any) (bool, error) {
	count, err := tx.Count(table, where, args...)
	return count > 0, err
}

// Rollback rolls back the transaction. If fn then returns nil, DB.Tx's
// commit returns sql.ErrTxDone; return an error from fn instead.
func (tx *Tx) Rollback() error {
	return tx.tx.Rollback()
}

// Commit commits the transaction before fn returns. If fn then returns nil,
// DB.Tx's own commit returns sql.ErrTxDone.
func (tx *Tx) Commit() error {
	return tx.tx.Commit()
}

// logQuery logs the statement when Debug is set. Like DB.logQuery it runs
// before the query executes, so it reports no duration.
func (tx *Tx) logQuery(query string, args ...any) {
	if tx.db.config.Debug {
		tx.logger.Printf("[TX] Query: %s %v", query, args)
	}
}
