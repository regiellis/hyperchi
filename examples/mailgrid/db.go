package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/regiellis/hyperchi/hyperchi/database"
)

// migrations create the schema. Every statement is idempotent, so they run on
// each boot. Tables that need composite keys or cascades are plain SQL; the
// builder covers the simple ones.
func migrations() []string {
	return []string{
		database.Table("users").ID().
			String("email").NotNull().Unique().
			String("name").NotNull().Default("").
			String("password_hash").NotNull().
			CreatedAt().Build(),
		database.Table("lists").ID().
			String("name").NotNull().
			Integer("created_by").NotNull().Default(0).
			CreatedAt().Build(),
		`CREATE TABLE IF NOT EXISTS recipients (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			list_id INTEGER NOT NULL REFERENCES lists(id) ON DELETE CASCADE,
			email TEXT NOT NULL,
			name TEXT NOT NULL DEFAULT '',
			fields TEXT NOT NULL DEFAULT '{}',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			UNIQUE (list_id, email)
		)`,
		`CREATE TABLE IF NOT EXISTS letters (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			title TEXT NOT NULL,
			subject TEXT NOT NULL,
			body TEXT NOT NULL,
			format TEXT NOT NULL DEFAULT 'text' CHECK (format IN ('text', 'html')),
			strict INTEGER NOT NULL DEFAULT 0,
			created_by INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS sendouts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			letter_id INTEGER NOT NULL REFERENCES letters(id),
			list_id INTEGER NOT NULL REFERENCES lists(id),
			status TEXT NOT NULL DEFAULT 'new',
			current_email TEXT NOT NULL DEFAULT '',
			error TEXT NOT NULL DEFAULT '',
			created_by INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			started_at DATETIME,
			finished_at DATETIME,
			UNIQUE (letter_id, list_id)
		)`,
		`CREATE TABLE IF NOT EXISTS outbox (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			sendout_id INTEGER NOT NULL REFERENCES sendouts(id),
			letter_id INTEGER NOT NULL,
			list_id INTEGER NOT NULL,
			recipient_id INTEGER REFERENCES recipients(id) ON DELETE SET NULL,
			email TEXT NOT NULL,
			name TEXT NOT NULL DEFAULT '',
			fields TEXT NOT NULL DEFAULT '{}',
			status TEXT NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'rendered', 'failed')),
			subject TEXT NOT NULL DEFAULT '',
			body TEXT NOT NULL DEFAULT '',
			format TEXT NOT NULL DEFAULT 'text',
			error TEXT NOT NULL DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			rendered_at DATETIME,
			UNIQUE (letter_id, list_id, recipient_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_outbox_sendout_status ON outbox (sendout_id, status, id)`,
		database.Table("audit_log").ID().
			Integer("user_id").NotNull().Default(0).
			String("user_email").NotNull().Default("").
			String("action").NotNull().
			Text("detail").NotNull().Default("").
			CreatedAt().Build(),
		database.Table("settings").ID().
			String("key").NotNull().Unique().
			Text("value").NotNull().Default("").
			Build(),
	}
}

// User is a team member who can sign in.
type User struct {
	ID           int64     `db:"id"`
	Email        string    `db:"email"`
	Name         string    `db:"name"`
	PasswordHash string    `db:"password_hash"`
	CreatedAt    time.Time `db:"created_at"`
}

// List is a named set of recipients.
type List struct {
	ID         int64     `db:"id"`
	Name       string    `db:"name"`
	CreatedAt  time.Time `db:"created_at"`
	Recipients int       `db:"recipients"`
}

// Recipient is one row of a list. Fields holds the merge fields from the
// imported CSV beyond email and name.
type Recipient struct {
	ID         int64     `db:"id"`
	ListID     int64     `db:"list_id"`
	Email      string    `db:"email"`
	Name       string    `db:"name"`
	FieldsJSON string    `db:"fields"`
	CreatedAt  time.Time `db:"created_at"`
}

// Fields decodes the stored merge fields.
func (r Recipient) Fields() (map[string]string, error) {
	return decodeFields(r.FieldsJSON)
}

// Values is everything a letter can merge for this recipient.
func (r Recipient) Values() (map[string]string, error) {
	fields, err := r.Fields()
	if err != nil {
		return nil, err
	}
	fields["email"] = r.Email
	fields["name"] = r.Name
	return fields, nil
}

func decodeFields(raw string) (map[string]string, error) {
	fields := map[string]string{}
	if raw == "" {
		return fields, nil
	}
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		return nil, fmt.Errorf("decode merge fields: %w", err)
	}
	return fields, nil
}

// Letter is a subject and body with merge fields.
type Letter struct {
	ID        int64     `db:"id"`
	Title     string    `db:"title"`
	Subject   string    `db:"subject"`
	Body      string    `db:"body"`
	Format    string    `db:"format"`
	Strict    bool      `db:"strict"`
	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
}

// IsHTML reports an HTML letter, whose merge values are escaped.
func (l Letter) IsHTML() bool { return l.Format == formatHTML }

// Sendout runs one letter against one list. There is at most one per pair;
// running it again renders only recipients not already in the outbox.
type Sendout struct {
	ID           int64      `db:"id"`
	LetterID     int64      `db:"letter_id"`
	ListID       int64      `db:"list_id"`
	Status       string     `db:"status"`
	CurrentEmail string     `db:"current_email"`
	Error        string     `db:"error"`
	CreatedAt    time.Time  `db:"created_at"`
	StartedAt    *time.Time `db:"started_at"`
	FinishedAt   *time.Time `db:"finished_at"`
	LetterTitle  string     `db:"letter_title"`
	ListName     string     `db:"list_name"`
}

// Send-out statuses.
const (
	statusNew         = "new"
	statusRunning     = "running"
	statusDone        = "done"
	statusCancelled   = "cancelled"
	statusInterrupted = "interrupted"
	statusFailed      = "failed"
)

// OutboxItem is one rendered (or queued, or failed) letter.
type OutboxItem struct {
	ID         int64      `db:"id"`
	SendoutID  int64      `db:"sendout_id"`
	Email      string     `db:"email"`
	Name       string     `db:"name"`
	FieldsJSON string     `db:"fields"`
	Status     string     `db:"status"`
	Subject    string     `db:"subject"`
	Body       string     `db:"body"`
	Format     string     `db:"format"`
	Error      string     `db:"error"`
	CreatedAt  time.Time  `db:"created_at"`
	RenderedAt *time.Time `db:"rendered_at"`
}

// AuditEntry records who did what.
type AuditEntry struct {
	ID        int64     `db:"id"`
	UserEmail string    `db:"user_email"`
	Action    string    `db:"action"`
	Detail    string    `db:"detail"`
	CreatedAt time.Time `db:"created_at"`
}

// errNotFound marks a missing row so handlers can answer 404.
var errNotFound = errors.New("not found")

// one maps database.ErrNoRows to errNotFound.
func one[T any](db *database.DB, query string, args ...any) (T, error) {
	return oneContext[T](context.Background(), db, query, args...)
}

// oneContext is one bound to ctx: a request's context, or a send-out job's,
// so cancelling the job stops its query.
func oneContext[T any](ctx context.Context, db *database.DB, query string, args ...any) (T, error) {
	v, err := database.GetStructContext[T](ctx, db, query, args...)
	if errors.Is(err, database.ErrNoRows) || errors.Is(err, sql.ErrNoRows) {
		return v, errNotFound
	}
	return v, err
}

const sendoutSelect = `SELECT s.id, s.letter_id, s.list_id, s.status, s.current_email, s.error,
	s.created_at, s.started_at, s.finished_at, l.title AS letter_title, li.name AS list_name
	FROM sendouts s JOIN letters l ON l.id = s.letter_id JOIN lists li ON li.id = s.list_id`

func getSendout(ctx context.Context, db *database.DB, id int64) (Sendout, error) {
	return oneContext[Sendout](ctx, db, sendoutSelect+` WHERE s.id = ?`, id)
}

func getLetter(ctx context.Context, db *database.DB, id int64) (Letter, error) {
	return oneContext[Letter](ctx, db, `SELECT * FROM letters WHERE id = ?`, id)
}

func getList(db *database.DB, id int64) (List, error) {
	return one[List](db, `SELECT l.id, l.name, l.created_at,
		(SELECT COUNT(*) FROM recipients r WHERE r.list_id = l.id) AS recipients
		FROM lists l WHERE l.id = ?`, id)
}

func getRecipient(db *database.DB, id int64) (Recipient, error) {
	return one[Recipient](db, `SELECT * FROM recipients WHERE id = ?`, id)
}

func allLists(db *database.DB) ([]List, error) {
	return database.ListStruct[List](db, `SELECT l.id, l.name, l.created_at,
		(SELECT COUNT(*) FROM recipients r WHERE r.list_id = l.id) AS recipients
		FROM lists l ORDER BY l.name, l.id`)
}

func allLetters(db *database.DB) ([]Letter, error) {
	return database.ListStruct[Letter](db, `SELECT * FROM letters ORDER BY updated_at DESC, id DESC`)
}

// listFields returns the merge fields any recipient on the list carries,
// sorted, after the two every recipient has.
func listFields(db *database.DB, listID int64) ([]string, error) {
	rows, err := database.ListStruct[struct {
		Key string `db:"key"`
	}](db, `SELECT DISTINCT j.key AS key FROM recipients r, json_each(r.fields) j
		WHERE r.list_id = ? ORDER BY j.key`, listID)
	if err != nil {
		return nil, err
	}
	fields := []string{"email", "name"}
	for _, r := range rows {
		fields = append(fields, r.Key)
	}
	return fields, nil
}

// FieldGap reports how many recipients on a list have no value for a merge
// field a letter uses.
type FieldGap struct {
	Field   string
	Missing int
	Total   int
}

// fieldGaps checks every field the letter uses against the list.
func fieldGaps(db *database.DB, listID int64, fields []string) ([]FieldGap, error) {
	total, err := db.Count("recipients", "list_id = ?", listID)
	if err != nil {
		return nil, err
	}
	var gaps []FieldGap
	for _, f := range fields {
		var missing int
		switch f {
		case "email":
			continue
		case "name":
			missing, err = db.Count("recipients", "list_id = ? AND name = ''", listID)
		default:
			// f is a merge-field identifier (letters, digits, underscore), so
			// the JSON path built from it cannot carry anything else.
			missing, err = db.Count("recipients",
				"list_id = ? AND COALESCE(json_extract(fields, ?), '') = ''", listID, "$."+f)
		}
		if err != nil {
			return nil, err
		}
		if missing > 0 {
			gaps = append(gaps, FieldGap{Field: f, Missing: missing, Total: total})
		}
	}
	return gaps, nil
}

// recipientPage is one page of a list's recipients, filtered by q.
func recipientPage(db *database.DB, listID int64, q string, page int) (*database.Paginator, []Recipient, error) {
	where := "list_id = ?"
	args := []any{listID}
	if q = strings.TrimSpace(q); q != "" {
		like := "%" + escapeLike(strings.ToLower(q)) + "%"
		where += ` AND (email LIKE ? ESCAPE '\' OR LOWER(name) LIKE ? ESCAPE '\')`
		args = append(args, like, like)
	}
	total, err := db.Count("recipients", where, args...)
	if err != nil {
		return nil, nil, err
	}
	p := database.NewPaginator(page, recipientsPerPage, total)
	query, qargs := database.Select("id", "list_id", "email", "name", "fields", "created_at").
		From("recipients").Where(where, args...).OrderBy("email", "ASC").
		Limit(p.PerPage).Offset(p.Offset()).Build()
	rows, err := database.ListStruct[Recipient](db, query, qargs...)
	return p, rows, err
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// Progress is a send-out's state as the outbox records it.
type Progress struct {
	Sendout  Sendout
	Total    int
	Rendered int
	Failed   int
	Queued   int
	Failures []OutboxItem
}

// Done is how many recipients the job has finished, either way.
func (p Progress) Done() int { return p.Rendered + p.Failed }

// Running reports a send-out whose job is still working.
func (p Progress) Running() bool { return p.Sendout.Status == statusRunning }

func loadProgress(ctx context.Context, db *database.DB, id int64) (Progress, error) {
	so, err := getSendout(ctx, db, id)
	if err != nil {
		return Progress{}, err
	}
	p := Progress{Sendout: so}
	rows, err := db.QueryContext(ctx, `SELECT status, COUNT(*) FROM outbox WHERE sendout_id = ? GROUP BY status`, id)
	if err != nil {
		return p, err
	}
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			return p, errors.Join(err, rows.Close())
		}
		switch status {
		case "rendered":
			p.Rendered = n
		case "failed":
			p.Failed = n
		case "queued":
			p.Queued = n
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return p, err
	}
	p.Total = p.Rendered + p.Failed + p.Queued
	p.Failures, err = database.ListStructContext[OutboxItem](ctx, db, `SELECT * FROM outbox
		WHERE sendout_id = ? AND status = 'failed' ORDER BY id DESC LIMIT 5`, id)
	return p, err
}

// audit records an action. A failure to write it fails the request: an
// action nobody can trace is worse than one that did not happen.
func audit(ctx context.Context, db *database.DB, u *User, action, detail string) error {
	var id int64
	var email string
	if u != nil {
		id, email = u.ID, u.Email
	}
	_, err := db.ExecContext(ctx, `INSERT INTO audit_log (user_id, user_email, action, detail, created_at)
		VALUES (?, ?, ?, ?, ?)`, id, email, action, detail, time.Now().UTC())
	return err
}

func getSetting(db *database.DB, key string) (string, error) {
	row, err := one[struct {
		Value string `db:"value"`
	}](db, `SELECT value FROM settings WHERE key = ?`, key)
	if errors.Is(err, errNotFound) {
		return "", nil
	}
	return row.Value, err
}

func setSetting(tx *database.Tx, key, value string) error {
	_, err := tx.Exec(`INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}
