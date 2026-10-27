package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"html"
	"html/template"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/regiellis/hyperchi/hyperchi/database"
	"github.com/regiellis/hyperchi/modules/markdown"
)

// schemaVersion is stored in PRAGMA user_version. The index is derived data,
// so a database at any other version is dropped and rebuilt from the library.
const schemaVersion = 1

var schema = []string{
	`CREATE TABLE documents (
		id INTEGER PRIMARY KEY,
		path TEXT NOT NULL UNIQUE,
		title TEXT NOT NULL,
		type TEXT NOT NULL,
		tags TEXT NOT NULL DEFAULT '',
		size INTEGER NOT NULL,
		mtime INTEGER NOT NULL,
		searchable INTEGER NOT NULL,
		reason TEXT NOT NULL DEFAULT ''
	)`,
	`CREATE TABLE document_tags (
		doc_id INTEGER NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
		tag TEXT NOT NULL,
		PRIMARY KEY (doc_id, tag)
	)`,
	`CREATE INDEX document_tags_tag ON document_tags(tag)`,
	`CREATE INDEX documents_mtime ON documents(mtime)`,
	// rowid is documents.id. Column weights for bm25 are set in searchSQL.
	`CREATE VIRTUAL TABLE documents_fts USING fts5(
		title, path, tags, body,
		tokenize = 'unicode61 remove_diacritics 2',
		prefix = '2 3'
	)`,
}

// errNotIndexed means a path is not a document in the index.
var errNotIndexed = errors.New("not in the library index")

// Index keeps a SQLite FTS5 index of a library directory in step with the files.
// Writes (Sync, Apply) are serialized; searches run concurrently with them.
type Index struct {
	db       *database.DB
	dir      string   // absolute library root
	root     *os.Root // every file read goes through it, so nothing outside dir is reachable
	md       *markdown.Renderer
	maxBytes int64

	mu       sync.Mutex // serializes index writes
	statusMu sync.Mutex
	status   Status

	// onStatus receives every status change. Set it before any scan runs.
	onStatus func(Status)
}

// Status is what the index status panel shows.
type Status struct {
	Root     string
	Files    int // every listed file
	Indexed  int // files whose contents are searchable
	Scanning bool
	LastScan time.Time
	LastPass string // "full scan" or "file change"
	Changed  int    // files added, changed, or removed in the last pass
	Errors   []string
}

// Skipped is the number of listed files whose contents are not searchable.
func (s Status) Skipped() int { return s.Files - s.Indexed }

const maxStatusErrors = 10

// OpenIndex opens the library at dir and prepares the index tables in db.
func OpenIndex(db *database.DB, dir string, maxBytes int64) (*Index, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("library directory: %w", err)
	}
	root, err := os.OpenRoot(abs)
	if err != nil {
		return nil, fmt.Errorf("open library directory: %w", err)
	}
	ix := &Index{db: db, dir: abs, root: root, md: markdown.New(), maxBytes: maxBytes, status: Status{Root: abs}}
	if err := ix.migrate(); err != nil {
		return nil, errors.Join(err, root.Close())
	}
	return ix, nil
}

// Close releases the library directory handle. The database belongs to the app.
func (ix *Index) Close() error { return ix.root.Close() }

func (ix *Index) migrate() error {
	var version int
	if err := ix.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return fmt.Errorf("read index schema version: %w", err)
	}
	if version == schemaVersion {
		return nil
	}
	return ix.db.Tx(func(tx *database.Tx) error {
		for _, table := range []string{"documents_fts", "document_tags", "documents"} {
			if _, err := tx.Exec(`DROP TABLE IF EXISTS ` + table); err != nil {
				return fmt.Errorf("drop %s: %w", table, err)
			}
		}
		for _, stmt := range schema {
			if _, err := tx.Exec(stmt); err != nil {
				return fmt.Errorf("create index schema: %w", err)
			}
		}
		if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, schemaVersion)); err != nil {
			return fmt.Errorf("set index schema version: %w", err)
		}
		return nil
	})
}

// Status returns the current status.
func (ix *Index) Status() Status {
	ix.statusMu.Lock()
	defer ix.statusMu.Unlock()
	s := ix.status
	s.Errors = append([]string(nil), s.Errors...)
	return s
}

func (ix *Index) setStatus(update func(*Status)) {
	ix.statusMu.Lock()
	update(&ix.status)
	s := ix.status
	s.Errors = append([]string(nil), s.Errors...)
	ix.statusMu.Unlock()
	if ix.onStatus != nil {
		ix.onStatus(s)
	}
}

// finishPass records the outcome of a scan and refreshes the counts.
func (ix *Index) finishPass(ctx context.Context, pass string, changed int, errs []string) {
	files, indexed, err := ix.counts(ctx)
	if err != nil {
		errs = append(errs, "count documents: "+err.Error())
	}
	if len(errs) > maxStatusErrors {
		errs = append(errs[:maxStatusErrors-1], fmt.Sprintf("and %d more", len(errs)-maxStatusErrors+1))
	}
	ix.setStatus(func(s *Status) {
		s.Scanning = false
		s.LastScan = time.Now()
		s.LastPass = pass
		s.Changed = changed
		s.Errors = errs
		if err == nil {
			s.Files, s.Indexed = files, indexed
		}
	})
}

func (ix *Index) counts(ctx context.Context) (files, indexed int, err error) {
	err = ix.db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(searchable), 0) FROM documents`).Scan(&files, &indexed)
	return files, indexed, err
}

// Sync walks the whole library and brings the index in line with it: new and
// changed files are indexed, files that are gone are removed. It is safe to
// run at any time and is what "rescan now" runs.
func (ix *Index) Sync(ctx context.Context) error {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	ix.setStatus(func(s *Status) { s.Scanning = true })

	var errs []string
	seen := map[string]fs.FileInfo{}
	var unreadable []string // directories whose files must not be dropped as missing
	walkErr := fs.WalkDir(ix.root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == "." {
				return err
			}
			errs = append(errs, err.Error())
			if d != nil && d.IsDir() {
				unreadable = append(unreadable, p+"/")
			}
			return nil
		}
		if p != "." && strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		// Stat through the root: a symlink is followed only while it stays inside
		// the library, so a link to /etc is reported, never indexed.
		info, err := ix.root.Stat(p)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", p, err))
			return nil
		}
		if info.Mode().IsRegular() {
			seen[p] = info
		}
		return nil
	})
	if walkErr != nil {
		ix.finishPass(ctx, "full scan", 0, append(errs, "walk library: "+walkErr.Error()))
		return walkErr
	}

	known, err := ix.stamps(ctx)
	if err != nil {
		ix.finishPass(ctx, "full scan", 0, append(errs, err.Error()))
		return err
	}
	changed := 0
	for p, info := range seen {
		if k, ok := known[p]; ok && k.size == info.Size() && k.mtime == info.ModTime().UnixNano() {
			continue
		}
		if err := ix.indexFile(ctx, p, info); err != nil {
			errs = append(errs, err.Error())
			continue
		}
		changed++
	}
	for p := range known {
		if _, ok := seen[p]; ok || underAny(p, unreadable) {
			continue
		}
		if err := ix.remove(ctx, p); err != nil {
			errs = append(errs, err.Error())
			continue
		}
		changed++
	}
	ix.finishPass(ctx, "full scan", changed, errs)
	return nil
}

func underAny(p string, dirs []string) bool {
	for _, d := range dirs {
		if strings.HasPrefix(p, d) {
			return true
		}
	}
	return false
}

// Apply updates the index for paths a FileWatcher reported (absolute paths
// under the library root) and records watchErr, if any, in the status.
func (ix *Index) Apply(ctx context.Context, changed []string, watchErr error) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	var errs []string
	if watchErr != nil {
		errs = append(errs, watchErr.Error())
	}
	n := 0
	for _, abs := range changed {
		rel, err := filepath.Rel(ix.dir, abs)
		if err != nil {
			continue
		}
		rel = filepath.ToSlash(rel)
		if !fs.ValidPath(rel) || rel == "." {
			continue
		}
		info, err := ix.root.Stat(rel)
		switch {
		case errors.Is(err, fs.ErrNotExist) || (err == nil && !info.Mode().IsRegular()):
			err = ix.remove(ctx, rel)
		case err != nil:
			// Unreadable or a link out of the library: report it, keep the old entry.
		default:
			err = ix.indexFile(ctx, rel, info)
		}
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		n++
	}
	ix.finishPass(ctx, "file change", n, errs)
}

type stamp struct {
	size  int64
	mtime int64
}

func (ix *Index) stamps(ctx context.Context) (map[string]stamp, error) {
	rows, err := ix.db.QueryContext(ctx, `SELECT path, size, mtime FROM documents`)
	if err != nil {
		return nil, fmt.Errorf("read index: %w", err)
	}
	known := map[string]stamp{}
	for rows.Next() {
		var p string
		var s stamp
		if err := rows.Scan(&p, &s.size, &s.mtime); err != nil {
			return nil, errors.Join(fmt.Errorf("read index: %w", err), rows.Close())
		}
		known[p] = s
	}
	if err := rows.Err(); err != nil {
		return nil, errors.Join(fmt.Errorf("read index: %w", err), rows.Close())
	}
	return known, rows.Close()
}

// readLimited reads at most limit+1 bytes of a library file, so a caller can
// tell a file over the limit without reading all of it.
func (ix *Index) readLimited(rel string, limit int64) ([]byte, error) {
	f, err := ix.root.Open(rel)
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	return data, errors.Join(err, f.Close())
}

// indexFile reads one file and writes its document row, tags, and FTS entry.
func (ix *Index) indexFile(ctx context.Context, rel string, info fs.FileInfo) error {
	doc := Document{
		Path: rel, Title: fileTitle(rel), Type: classify(rel),
		Size: info.Size(), Modified: info.ModTime(),
	}
	tags := pathTags(rel)
	body := ""
	switch {
	case doc.Type == TypeOther:
		doc.Reason = reasonUnsupported
	case info.Size() > ix.maxBytes:
		doc.Reason = reasonTooLarge
	default:
		data, err := ix.readLimited(rel, ix.maxBytes)
		if err != nil {
			return fmt.Errorf("%s: %w", rel, err)
		}
		switch {
		case int64(len(data)) > ix.maxBytes:
			doc.Reason = reasonTooLarge
		case looksBinary(data):
			doc.Reason = reasonBinary
		case doc.Type == TypeMarkdown:
			parsed, err := parseMarkdown(ix.md, rel, data)
			if err != nil {
				// Index the source text rather than lose the file.
				body = string(data)
				break
			}
			doc.Title = parsed.Title
			tags = mergeTags(parsed.Tags, tags)
			body = parsed.Doc.Text()
		default:
			body = string(data)
		}
	}
	doc.Searchable = doc.Reason == ""
	doc.Tags = tags
	return ix.write(ctx, doc, cleanText(body))
}

func (ix *Index) write(ctx context.Context, doc Document, body string) error {
	tagText := strings.Join(doc.Tags, " ")
	title := cleanText(doc.Title)
	err := ix.db.TxContext(ctx, func(tx *database.Tx) error {
		var id int64
		err := tx.QueryRow(`INSERT INTO documents (path, title, type, tags, size, mtime, searchable, reason)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(path) DO UPDATE SET title = excluded.title, type = excluded.type, tags = excluded.tags,
				size = excluded.size, mtime = excluded.mtime, searchable = excluded.searchable, reason = excluded.reason
			RETURNING id`,
			doc.Path, title, doc.Type, tagText, doc.Size, doc.Modified.UnixNano(), doc.Searchable, doc.Reason,
		).Scan(&id)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM documents_fts WHERE rowid = ?`, id); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO documents_fts (rowid, title, path, tags, body) VALUES (?, ?, ?, ?, ?)`,
			id, title, cleanText(doc.Path), tagText, body); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM document_tags WHERE doc_id = ?`, id); err != nil {
			return err
		}
		for _, tag := range doc.Tags {
			if _, err := tx.Exec(`INSERT INTO document_tags (doc_id, tag) VALUES (?, ?)`, id, tag); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("index %s: %w", doc.Path, err)
	}
	return nil
}

func (ix *Index) remove(ctx context.Context, rel string) error {
	err := ix.db.TxContext(ctx, func(tx *database.Tx) error {
		if _, err := tx.Exec(`DELETE FROM documents_fts WHERE rowid IN (SELECT id FROM documents WHERE path = ?)`, rel); err != nil {
			return err
		}
		_, err := tx.Exec(`DELETE FROM documents WHERE path = ?`, rel)
		return err
	})
	if err != nil {
		return fmt.Errorf("remove %s: %w", rel, err)
	}
	return nil
}

// Document is one listed library file.
type Document struct {
	Path       string
	Title      string
	Type       string
	Tags       []string
	Size       int64
	Modified   time.Time
	Searchable bool
	Reason     string // why the contents are not searchable, when they are not
}

// Lookup returns the indexed document at a slash-separated library path, or
// errNotIndexed. Only indexed paths are ever opened, which is the first of two
// guards against path traversal; reading through os.Root is the second.
func (ix *Index) Lookup(ctx context.Context, rel string) (Document, error) {
	var d Document
	var tags string
	var mtime int64
	err := ix.db.QueryRowContext(ctx, `SELECT path, title, type, tags, size, mtime, searchable, reason
		FROM documents WHERE path = ?`, rel).Scan(&d.Path, &d.Title, &d.Type, &tags, &d.Size, &mtime, &d.Searchable, &d.Reason)
	if errors.Is(err, sql.ErrNoRows) {
		return d, errNotIndexed
	}
	if err != nil {
		return d, err
	}
	d.Tags = strings.Fields(tags)
	d.Modified = time.Unix(0, mtime)
	return d, nil
}

// SearchParams is one search. An empty Query lists recently changed documents.
type SearchParams struct {
	Query string
	Type  string
	Tag   string
	Limit int
}

// Hit is one search result. TitleHTML and Snippet are escaped HTML with the
// matching words in <mark>.
type Hit struct {
	Document
	TitleHTML template.HTML
	Snippet   template.HTML
}

// Results is a page of hits. More reports hits beyond Limit.
type Results struct {
	Hits []Hit
	More bool
	// Searched is false when the query had no words to search for, so the
	// results are the recent-documents list.
	Searched bool
}

// highlight and snippet mark matches with \x01 and \x02, characters that
// cleanText strips from everything indexed, so the markers cannot be forged
// by a document.
const searchSQL = `SELECT d.path, d.title, d.type, d.tags, d.size, d.mtime, d.searchable, d.reason,
	highlight(documents_fts, 0, char(1), char(2)),
	snippet(documents_fts, 3, char(1), char(2), '…', 24)
	FROM documents_fts JOIN documents d ON d.id = documents_fts.rowid
	WHERE documents_fts MATCH ?`

const browseSQL = `SELECT d.path, d.title, d.type, d.tags, d.size, d.mtime, d.searchable, d.reason, d.title, ''
	FROM documents d WHERE 1 = 1`

// Search runs p against the index, ranked by bm25 with title matches weighted
// over tags, path, and body.
func (ix *Index) Search(ctx context.Context, p SearchParams) (Results, error) {
	if p.Limit <= 0 {
		p.Limit = 50
	}
	expr := matchExpr(p.Query)
	query, args := browseSQL, []any{}
	order := ` ORDER BY d.mtime DESC, d.path`
	if expr != "" {
		query, args = searchSQL, []any{expr}
		order = ` ORDER BY bm25(documents_fts, 10.0, 4.0, 6.0, 1.0), d.path`
	}
	if p.Type != "" {
		query += ` AND d.type = ?`
		args = append(args, p.Type)
	}
	if p.Tag != "" {
		query += ` AND EXISTS (SELECT 1 FROM document_tags t WHERE t.doc_id = d.id AND t.tag = ?)`
		args = append(args, p.Tag)
	}
	query += order + ` LIMIT ?`
	args = append(args, p.Limit+1)

	rows, err := ix.db.QueryContext(ctx, query, args...)
	if err != nil {
		return Results{}, fmt.Errorf("search: %w", err)
	}
	res := Results{Searched: expr != ""}
	for rows.Next() {
		var h Hit
		var tags, title, snip string
		var mtime int64
		if err := rows.Scan(&h.Path, &h.Title, &h.Type, &tags, &h.Size, &mtime, &h.Searchable, &h.Reason, &title, &snip); err != nil {
			return Results{}, errors.Join(fmt.Errorf("search: %w", err), rows.Close())
		}
		h.Tags = strings.Fields(tags)
		h.Modified = time.Unix(0, mtime)
		h.TitleHTML = markHTML(title)
		h.Snippet = markHTML(strings.Join(strings.Fields(snip), " "))
		res.Hits = append(res.Hits, h)
	}
	if err := rows.Err(); err != nil {
		return Results{}, errors.Join(fmt.Errorf("search: %w", err), rows.Close())
	}
	if err := rows.Close(); err != nil {
		return Results{}, err
	}
	if len(res.Hits) > p.Limit {
		res.Hits, res.More = res.Hits[:p.Limit], true
	}
	return res, nil
}

// markHTML escapes s and turns the \x01 and \x02 markers into <mark> tags. A
// stray end marker is dropped and an open mark is closed, so the output is
// always balanced.
func markHTML(s string) template.HTML {
	var b strings.Builder
	open := false
	for {
		i := strings.IndexAny(s, "\x01\x02")
		if i < 0 {
			b.WriteString(html.EscapeString(s))
			break
		}
		b.WriteString(html.EscapeString(s[:i]))
		switch {
		case s[i] == '\x01' && !open:
			b.WriteString("<mark>")
			open = true
		case s[i] == '\x02' && open:
			b.WriteString("</mark>")
			open = false
		}
		s = s[i+1:]
	}
	if open {
		b.WriteString("</mark>")
	}
	return template.HTML(b.String()) // all text above is escaped; only <mark> is added
}

// Facet is a filter value and how many documents have it.
type Facet struct {
	Name  string
	Count int
}

// Facets returns the document types and the most used tags, for the filters.
func (ix *Index) Facets(ctx context.Context) (types, tags []Facet, err error) {
	types, err = ix.facet(ctx, `SELECT type, COUNT(*) FROM documents GROUP BY type ORDER BY type`)
	if err != nil {
		return nil, nil, err
	}
	tags, err = ix.facet(ctx, `SELECT tag, COUNT(*) AS n FROM document_tags GROUP BY tag ORDER BY n DESC, tag LIMIT 200`)
	return types, tags, err
}

func (ix *Index) facet(ctx context.Context, query string) ([]Facet, error) {
	rows, err := ix.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("facets: %w", err)
	}
	var out []Facet
	for rows.Next() {
		var f Facet
		if err := rows.Scan(&f.Name, &f.Count); err != nil {
			return nil, errors.Join(fmt.Errorf("facets: %w", err), rows.Close())
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.Join(fmt.Errorf("facets: %w", err), rows.Close())
	}
	return out, rows.Close()
}
