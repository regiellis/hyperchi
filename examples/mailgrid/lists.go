package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/regiellis/hyperchi/hyperchi"
	"github.com/regiellis/hyperchi/hyperchi/database"
	g "maragu.dev/gomponents"
	h "maragu.dev/gomponents/html"
)

func (s *Server) listsPage(c *hyperchi.Context) error {
	lists, err := allLists(s.db)
	if err != nil {
		return err
	}
	return s.page(c, "Lists", "lists", listsView(lists, NewList{}, nil, c.CSRFToken()))
}

// NewList names a new recipient list.
type NewList struct {
	Name string `form:"name" validate:"required,max=120"`
}

func (s *Server) createList(c *hyperchi.Context) error {
	in, err := hyperchi.Bind[NewList](c)
	if err != nil {
		if fields := hyperchi.FieldErrors(err); fields != nil {
			return s.rejected(c, "Lists", "lists", newListForm(in, fields, c.CSRFToken()))
		}
		return err
	}
	id, err := s.db.Create("lists", map[string]any{
		"name": strings.TrimSpace(in.Name), "created_by": currentUser(c).ID, "created_at": time.Now().UTC(),
	})
	if err != nil {
		return err
	}
	if err := audit(c.Ctx(), s.db, currentUser(c), "created a list", in.Name); err != nil {
		return err
	}
	return c.Redirect(fmt.Sprintf("/lists/%d", id))
}

// listPage is the list with its import form and recipient table. An htmx
// request (search, paging, refresh after a change) gets only the table.
func (s *Server) listPage(c *hyperchi.Context) error {
	listID, err := id(c)
	if err != nil {
		return err
	}
	list, err := getList(s.db, listID)
	if err != nil {
		return notFound(err, "list")
	}
	q := c.Query("q")
	p, rows, err := recipientPage(s.db, listID, q, c.QueryInt("page", 1))
	if err != nil {
		return err
	}
	table := recipientTable(list, q, p, rows)
	if partial(c) {
		return render(c, table)
	}
	fields, err := listFields(s.db, listID)
	if err != nil {
		return err
	}
	return s.page(c, list.Name, "lists", listView(list, fields, s.opts.MaxUpload, table, c.CSRFToken()))
}

// ImportUpload is the CSV upload form.
type ImportUpload struct {
	File *multipart.FileHeader `form:"file"`
}

// ImportReport is what an import did, for the result panel.
type ImportReport struct {
	Parsed   *ParsedCSV
	Added    int
	Existing int
	Message  string
}

// importCSV reads an uploaded CSV, validates and dedupes its rows, and adds
// the new recipients in one transaction. Recipients already on the list are
// left unchanged. htmx swaps the result into the form's panel; a form posted
// without JavaScript gets a page around it.
//
// MaxUpload is a product limit, well under the 32 MiB multipart cap that Bind
// and the CSRF middleware apply. An htmx upload carries its CSRF token in a
// header, so its body is capped here before anything parses it. A no-JS form
// carries the token as a field, which the CSRF middleware reads by parsing
// the body under the framework cap first; the file size check below still
// refuses it.
func (s *Server) importCSV(c *hyperchi.Context) error {
	listID, err := id(c)
	if err != nil {
		return err
	}
	list, err := getList(s.db, listID)
	if err != nil {
		return notFound(err, "list")
	}
	report := func(code int, r ImportReport) error {
		c.Status(code)
		if partial(c) {
			return render(c, importResult(list, r))
		}
		back := fmt.Sprintf("/lists/%d", list.ID)
		return s.page(c, "Import into "+list.Name, "lists", h.Section(h.Class("panel"),
			h.H2(g.Text("Import into "+list.Name)),
			importResult(list, r),
			h.P(h.A(h.Href(back), g.Text("Back to "+list.Name))),
		))
	}
	req := c.Request()
	if req.ContentLength > s.opts.MaxUpload {
		return report(http.StatusRequestEntityTooLarge, ImportReport{Message: tooLarge(s.opts.MaxUpload)})
	}
	req.Body = http.MaxBytesReader(c.Response(), req.Body, s.opts.MaxUpload)
	in, err := hyperchi.Bind[ImportUpload](c)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return report(http.StatusRequestEntityTooLarge, ImportReport{Message: tooLarge(s.opts.MaxUpload)})
		}
		return err
	}
	if in.File == nil || in.File.Size == 0 {
		return report(http.StatusUnprocessableEntity, ImportReport{Message: "Choose a CSV file to import."})
	}
	if in.File.Size > s.opts.MaxUpload {
		return report(http.StatusRequestEntityTooLarge, ImportReport{Message: tooLarge(s.opts.MaxUpload)})
	}
	f, err := in.File.Open()
	if err != nil {
		return err
	}
	parsed, parseErr := parseRecipientsCSV(io.LimitReader(f, s.opts.MaxUpload))
	if err := errors.Join(parseErr, f.Close()); err != nil {
		var bad errHeader
		if errors.As(err, &bad) {
			return report(http.StatusUnprocessableEntity, ImportReport{Message: bad.msg})
		}
		return err
	}
	added, err := addRecipients(s.db, listID, parsed.Rows)
	if err != nil {
		return err
	}
	r := ImportReport{Parsed: parsed, Added: added, Existing: len(parsed.Rows) - added}
	detail := fmt.Sprintf("%s: %d added, %d already on the list, %d duplicates, %d rejected",
		list.Name, r.Added, r.Existing, len(parsed.Duplicates), len(parsed.Problems))
	if err := audit(c.Ctx(), s.db, currentUser(c), "imported recipients", detail); err != nil {
		return err
	}
	c.HX().Trigger("recipients-changed")
	return report(http.StatusOK, r)
}

func tooLarge(limit int64) string {
	return fmt.Sprintf("The file is larger than %s. Split it into smaller files.", humanBytes(limit))
}

func humanBytes(n int64) string {
	if n >= 1<<20 {
		return strconv.FormatFloat(float64(n)/(1<<20), 'f', -1, 64) + " MB"
	}
	return strconv.FormatInt(n/1024, 10) + " KB"
}

// addRecipients inserts rows that are not on the list yet and reports how
// many were new.
func addRecipients(db *database.DB, listID int64, rows []ImportRow) (int, error) {
	added := 0
	err := db.Tx(func(tx *database.Tx) error {
		now := time.Now().UTC()
		for _, row := range rows {
			fields, err := json.Marshal(row.Fields)
			if err != nil {
				return err
			}
			res, err := tx.Exec(`INSERT INTO recipients (list_id, email, name, fields, created_at)
				VALUES (?, ?, ?, ?, ?) ON CONFLICT (list_id, email) DO NOTHING`,
				listID, row.Email, row.Name, string(fields), now)
			if err != nil {
				return err
			}
			n, err := res.RowsAffected()
			if err != nil {
				return err
			}
			added += int(n)
		}
		return nil
	})
	return added, err
}

func (s *Server) editRecipient(c *hyperchi.Context) error {
	rid, err := id(c)
	if err != nil {
		return err
	}
	r, err := getRecipient(s.db, rid)
	if err != nil {
		return notFound(err, "recipient")
	}
	fields, err := r.Fields()
	if err != nil {
		return err
	}
	return render(c, recipientEditor(r, fieldList(fields), nil, c.CSRFToken()))
}

// FieldValue is one merge field in the editor.
type FieldValue struct{ Key, Value string }

func fieldList(fields map[string]string) []FieldValue {
	out := make([]FieldValue, 0, len(fields))
	for k, v := range fields {
		out = append(out, FieldValue{k, v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// updateRecipient saves the editor. Merge fields arrive as field.<name>; an
// emptied field is removed.
func (s *Server) updateRecipient(c *hyperchi.Context) error {
	rid, err := id(c)
	if err != nil {
		return err
	}
	r, err := getRecipient(s.db, rid)
	if err != nil {
		return notFound(err, "recipient")
	}
	old, err := r.Fields()
	if err != nil {
		return err
	}
	email, msg := normalizeEmail(strings.TrimSpace(c.FormValue("email")))
	problems := map[string][]string{}
	if msg != "" {
		problems["email"] = []string{msg}
	}
	name := strings.TrimSpace(c.FormValue("name"))
	if len([]rune(name)) > maxNameLength {
		problems["name"] = []string{fmt.Sprintf("Keep the name under %d characters.", maxNameLength)}
	}
	fields := map[string]string{}
	for k := range old {
		if v := strings.TrimSpace(c.FormValue("field." + k)); v != "" {
			if len([]rune(v)) > maxFieldValue {
				problems["field."+k] = []string{fmt.Sprintf("Keep this under %d characters.", maxFieldValue)}
			}
			fields[k] = v
		}
	}
	if msg == "" {
		taken, err := s.db.Exists("recipients", "list_id = ? AND email = ? AND id != ?", r.ListID, email, r.ID)
		if err != nil {
			return err
		}
		if taken {
			problems["email"] = []string{"Another recipient on this list has this email."}
		}
	}
	if len(problems) > 0 {
		r.Email, r.Name = c.FormValue("email"), name
		return render(c.Status(http.StatusUnprocessableEntity), recipientEditor(r, fieldList(mergeInto(old, fields)), problems, c.CSRFToken()))
	}
	encoded, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	if err := s.db.Update("recipients", r.ID, map[string]any{"email": email, "name": name, "fields": string(encoded)}); err != nil {
		return err
	}
	if err := audit(c.Ctx(), s.db, currentUser(c), "edited a recipient", email); err != nil {
		return err
	}
	c.HX().Trigger("recipients-changed")
	return render(c, editorClosed(email))
}

// mergeInto keeps every key of old in the editor, with the submitted values.
func mergeInto(old, submitted map[string]string) map[string]string {
	out := map[string]string{}
	for k := range old {
		out[k] = submitted[k]
	}
	return out
}

func (s *Server) deleteRecipient(c *hyperchi.Context) error {
	rid, err := id(c)
	if err != nil {
		return err
	}
	r, err := getRecipient(s.db, rid)
	if err != nil {
		return notFound(err, "recipient")
	}
	if err := s.db.Delete("recipients", r.ID); err != nil {
		return err
	}
	if err := audit(c.Ctx(), s.db, currentUser(c), "removed a recipient", r.Email); err != nil {
		return err
	}
	return c.HX().Trigger("recipients-changed").Empty()
}

// recipientOptions fills the preview's recipient picker for a list.
func (s *Server) recipientOptions(c *hyperchi.Context) error {
	listID, err := strconv.ParseInt(c.Query("list_id"), 10, 64)
	if err != nil {
		return render(c, recipientSelectOptions(nil, 0))
	}
	rows, err := database.ListStruct[Recipient](s.db,
		`SELECT * FROM recipients WHERE list_id = ? ORDER BY email LIMIT 200`, listID)
	if err != nil {
		return err
	}
	return render(c, recipientSelectOptions(rows, 0))
}
