package main

import (
	"archive/zip"
	"bytes"
	"crypto/rand"
	"encoding/csv"
	"encoding/hex"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net/mail"
	"regexp"
	"strings"
	"time"

	"github.com/regiellis/hyperchi/hyperchi"
	"github.com/regiellis/hyperchi/hyperchi/database"
)

func (s *Server) outboxPage(c *hyperchi.Context) error {
	sid, err := id(c)
	if err != nil {
		return err
	}
	pr, err := loadProgress(c.Ctx(), s.db, sid)
	if err != nil {
		return notFound(err, "send-out")
	}
	status := c.Query("status")
	where, args := "sendout_id = ?", []any{sid}
	switch status {
	case "rendered", "failed", "queued":
		where += " AND status = ?"
		args = append(args, status)
	default:
		status = ""
	}
	total, err := s.db.Count("outbox", where, args...)
	if err != nil {
		return err
	}
	p := database.NewPaginator(c.QueryInt("page", 1), outboxPerPage, total)
	query, qargs := database.Select("id", "sendout_id", "email", "name", "status", "subject", "error", "created_at", "rendered_at").
		From("outbox").Where(where, args...).OrderBy("id", "ASC").Limit(p.PerPage).Offset(p.Offset()).Build()
	items, err := database.ListStruct[OutboxItem](s.db, query, qargs...)
	if err != nil {
		return err
	}
	table := outboxTable(pr.Sendout, status, p, items)
	if partial(c) {
		return render(c, table)
	}
	return s.page(c, "Outbox", "sendouts", outboxView(pr, status, table))
}

func (s *Server) outboxItem(c *hyperchi.Context) (OutboxItem, error) {
	oid, err := id(c)
	if err != nil {
		return OutboxItem{}, err
	}
	item, err := one[OutboxItem](s.db, `SELECT * FROM outbox WHERE id = ?`, oid)
	return item, notFound(err, "letter")
}

func (s *Server) outboxItemPage(c *hyperchi.Context) error {
	item, err := s.outboxItem(c)
	if err != nil {
		return err
	}
	so, err := getSendout(c.Ctx(), s.db, item.SendoutID)
	if err != nil {
		return err
	}
	return s.page(c, item.Subject, "sendouts", outboxItemView(so, item, s.opts.From))
}

// outboxItemEML downloads one rendered letter as an .eml file.
func (s *Server) outboxItemEML(c *hyperchi.Context) error {
	item, err := s.outboxItem(c)
	if err != nil {
		return err
	}
	if item.Status != "rendered" {
		return hyperchi.NotFound("This letter has not been rendered.")
	}
	msg, err := buildEML(s.opts.From, item)
	if err != nil {
		return err
	}
	return c.SetHeader("Content-Disposition", attachment(emlName(item))).Blob("message/rfc822", msg)
}

// exportCSV downloads every outbox row of a send-out as CSV.
func (s *Server) exportCSV(c *hyperchi.Context) error {
	sid, err := id(c)
	if err != nil {
		return err
	}
	so, err := getSendout(c.Ctx(), s.db, sid)
	if err != nil {
		return notFound(err, "send-out")
	}
	items, err := database.ListStruct[OutboxItem](s.db, `SELECT * FROM outbox WHERE sendout_id = ? ORDER BY id`, sid)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	if err := w.Write([]string{"id", "email", "name", "status", "subject", "body", "format", "error", "rendered_at"}); err != nil {
		return err
	}
	for _, it := range items {
		rendered := ""
		if it.RenderedAt != nil {
			rendered = it.RenderedAt.UTC().Format(time.RFC3339)
		}
		if err := w.Write([]string{
			fmt.Sprint(it.ID), cell(it.Email), cell(it.Name), it.Status, cell(it.Subject),
			cell(it.Body), it.Format, cell(it.Error), rendered,
		}); err != nil {
			return err
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return err
	}
	if err := audit(c.Ctx(), s.db, currentUser(c), "exported a send-out as CSV",
		fmt.Sprintf("send-out %d (%s to %s)", sid, so.LetterTitle, so.ListName)); err != nil {
		return err
	}
	return c.SetHeader("Content-Disposition", attachment(fmt.Sprintf("sendout-%d.csv", sid))).
		Blob("text/csv; charset=utf-8", buf.Bytes())
}

// cell guards a value that a spreadsheet would run as a formula.
func cell(v string) string {
	if v != "" && strings.ContainsRune("=+-@\t\r", rune(v[0])) {
		return "'" + v
	}
	return v
}

// exportZip downloads every rendered letter of a send-out as .eml files in
// one zip archive.
func (s *Server) exportZip(c *hyperchi.Context) error {
	sid, err := id(c)
	if err != nil {
		return err
	}
	so, err := getSendout(c.Ctx(), s.db, sid)
	if err != nil {
		return notFound(err, "send-out")
	}
	items, err := database.ListStruct[OutboxItem](s.db,
		`SELECT * FROM outbox WHERE sendout_id = ? AND status = 'rendered' ORDER BY id`, sid)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, it := range items {
		msg, err := buildEML(s.opts.From, it)
		if err != nil {
			return err
		}
		w, err := zw.CreateHeader(&zip.FileHeader{Name: emlName(it), Method: zip.Deflate, Modified: time.Now().UTC()})
		if err != nil {
			return err
		}
		if _, err := w.Write(msg); err != nil {
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return err
	}
	if err := audit(c.Ctx(), s.db, currentUser(c), "exported a send-out as .eml files",
		fmt.Sprintf("send-out %d (%s to %s): %d letters", sid, so.LetterTitle, so.ListName, len(items))); err != nil {
		return err
	}
	return c.SetHeader("Content-Disposition", attachment(fmt.Sprintf("sendout-%d-eml.zip", sid))).
		Blob("application/zip", buf.Bytes())
}

func attachment(name string) string {
	return mime.FormatMediaType("attachment", map[string]string{"filename": name})
}

var unsafeName = regexp.MustCompile(`[^a-z0-9._-]+`)

func emlName(it OutboxItem) string {
	return fmt.Sprintf("%05d-%s.eml", it.ID, strings.Trim(unsafeName.ReplaceAllString(strings.ToLower(it.Email), "_"), "_"))
}

// buildEML writes a rendered letter as an RFC 5322 message: encoded
// headers, a quoted-printable UTF-8 body, and a Message-ID. It is a file for
// another system to send; mailgrid never sends it.
func buildEML(from string, it OutboxItem) ([]byte, error) {
	fromAddr, err := mail.ParseAddress(from)
	if err != nil {
		return nil, fmt.Errorf("MAILGRID_FROM %q: %w", from, err)
	}
	to := mail.Address{Name: it.Name, Address: it.Email}
	date := time.Now().UTC()
	if it.RenderedAt != nil {
		date = *it.RenderedAt
	}
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	domain := fromAddr.Address[strings.LastIndex(fromAddr.Address, "@")+1:]
	contentType := "text/plain; charset=utf-8"
	if it.Format == formatHTML {
		contentType = "text/html; charset=utf-8"
	}
	var b bytes.Buffer
	header := func(k, v string) { fmt.Fprintf(&b, "%s: %s\r\n", k, v) }
	header("From", fromAddr.String())
	header("To", to.String())
	header("Subject", mime.QEncoding.Encode("utf-8", oneLine(it.Subject)))
	header("Date", date.Format(time.RFC1123Z))
	header("Message-ID", fmt.Sprintf("<mailgrid.%d.%s@%s>", it.ID, hex.EncodeToString(nonce[:]), domain))
	header("MIME-Version", "1.0")
	header("Content-Type", contentType)
	header("Content-Transfer-Encoding", "quoted-printable")
	header("X-Mailgrid-Sendout", fmt.Sprint(it.SendoutID))
	b.WriteString("\r\n")
	qp := quotedprintable.NewWriter(&b)
	if _, err := qp.Write([]byte(strings.ReplaceAll(strings.ReplaceAll(it.Body, "\r\n", "\n"), "\n", "\r\n"))); err != nil {
		return nil, err
	}
	if err := qp.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(strings.NewReplacer("\r", " ", "\n", " ").Replace(s)), " ")
}
