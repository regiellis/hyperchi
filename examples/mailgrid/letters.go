package main

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/regiellis/hyperchi/hyperchi"
	"github.com/regiellis/hyperchi/hyperchi/database"
)

// LetterForm is the compose form.
type LetterForm struct {
	Title   string `form:"title" validate:"required,max=120"`
	Subject string `form:"subject" validate:"required,max=200"`
	Body    string `form:"body" validate:"required,max=100000"`
	Format  string `form:"format" validate:"required,oneof=text html"`
	Strict  bool   `form:"strict"`
}

// Validate checks that every merge tag names a usable field. A tag such as
// {{ first name }} is not a merge field and would be sent as typed.
func (f LetterForm) Validate() error {
	for _, part := range []struct{ name, text string }{{"subject", f.Subject}, {"body", f.Body}} {
		if bad := strayTag(part.text); bad != "" {
			return hyperchi.Unprocessable("Please correct the highlighted fields.", map[string][]string{
				part.name: {fmt.Sprintf("%s is not a merge field. Use letters, digits, and underscores, like {{first_name}}.", bad)},
			})
		}
	}
	return nil
}

// strayTag finds text between double braces that is not a valid merge tag.
func strayTag(s string) string {
	rest := mergeTag.ReplaceAllString(s, "")
	start := strings.Index(rest, "{{")
	if start < 0 {
		return ""
	}
	end := strings.Index(rest[start:], "}}")
	if end < 0 {
		return rest[start:min(len(rest), start+30)]
	}
	return rest[start : start+end+2]
}

func (s *Server) lettersPage(c *hyperchi.Context) error {
	letters, err := allLetters(s.db)
	if err != nil {
		return err
	}
	return s.page(c, "Letters", "letters", lettersView(letters))
}

func (s *Server) newLetterPage(c *hyperchi.Context) error {
	lists, err := allLists(s.db)
	if err != nil {
		return err
	}
	form := LetterForm{Format: formatText, Subject: "Hello {{name}}", Body: "Dear {{name}},\n\n"}
	return s.page(c, "New letter", "letters", composeView(0, form, nil, lists, c.CSRFToken()))
}

func (s *Server) createLetter(c *hyperchi.Context) error {
	in, err := hyperchi.Bind[LetterForm](c)
	if err != nil {
		return s.letterFormError(c, 0, in, err)
	}
	now := time.Now().UTC()
	lid, err := s.db.Create("letters", map[string]any{
		"title": in.Title, "subject": in.Subject, "body": in.Body, "format": in.Format, "strict": in.Strict,
		"created_by": currentUser(c).ID, "created_at": now, "updated_at": now,
	})
	if err != nil {
		return err
	}
	if err := audit(c.Ctx(), s.db, currentUser(c), "wrote a letter", in.Title); err != nil {
		return err
	}
	return c.Redirect(fmt.Sprintf("/letters/%d", lid))
}

// letterFormError re-renders the form with field messages for a 422, and
// passes any other error on.
func (s *Server) letterFormError(c *hyperchi.Context, letterID int64, in LetterForm, err error) error {
	fields := hyperchi.FieldErrors(err)
	if fields == nil {
		return err
	}
	lists, lerr := allLists(s.db)
	if lerr != nil {
		return lerr
	}
	return s.rejected(c, "Letter", "letters", letterForm(letterID, in, fields, lists, c.CSRFToken()))
}

func (s *Server) letterPage(c *hyperchi.Context) error {
	lid, err := id(c)
	if err != nil {
		return err
	}
	l, err := getLetter(c.Ctx(), s.db, lid)
	if err != nil {
		return notFound(err, "letter")
	}
	lists, err := allLists(s.db)
	if err != nil {
		return err
	}
	sendouts, err := database.ListStruct[Sendout](s.db, sendoutSelect+` WHERE s.letter_id = ? ORDER BY s.id DESC`, lid)
	if err != nil {
		return err
	}
	form := LetterForm{Title: l.Title, Subject: l.Subject, Body: l.Body, Format: l.Format, Strict: l.Strict}
	return s.page(c, l.Title, "letters", letterView(l, form, lists, sendouts, c.CSRFToken()))
}

func (s *Server) updateLetter(c *hyperchi.Context) error {
	lid, err := id(c)
	if err != nil {
		return err
	}
	if _, err := getLetter(c.Ctx(), s.db, lid); err != nil {
		return notFound(err, "letter")
	}
	in, err := hyperchi.Bind[LetterForm](c)
	if err != nil {
		return s.letterFormError(c, lid, in, err)
	}
	if err := s.db.Update("letters", lid, map[string]any{
		"title": in.Title, "subject": in.Subject, "body": in.Body, "format": in.Format, "strict": in.Strict,
		"updated_at": time.Now().UTC(),
	}); err != nil {
		return err
	}
	if err := audit(c.Ctx(), s.db, currentUser(c), "edited a letter", in.Title); err != nil {
		return err
	}
	return c.Redirect(fmt.Sprintf("/letters/%d", lid))
}

// Preview is a letter merged for one recipient, with the checks a person
// wants before a send-out.
type Preview struct {
	Fields    []string
	Recipient *Recipient
	List      *List
	Merged    Merged
	Format    string
	Gaps      []FieldGap
	Strict    bool
}

// previewLetter merges the form as it stands (saved or not) for the chosen
// recipient, or the list's first recipient when none is chosen.
func (s *Server) previewLetter(c *hyperchi.Context) error {
	in, err := hyperchi.Bind[LetterForm](c)
	if err != nil && hyperchi.FieldErrors(err) == nil {
		return err
	}
	p := Preview{Fields: mergeFields(in.Subject, in.Body), Format: in.Format, Strict: in.Strict}
	if p.Format != formatHTML {
		p.Format = formatText
	}
	values := map[string]string{}
	if listID, perr := strconv.ParseInt(c.FormValue("list_id"), 10, 64); perr == nil {
		list, err := getList(s.db, listID)
		if err != nil {
			return notFound(err, "list")
		}
		p.List = &list
		if p.Gaps, err = fieldGaps(s.db, listID, p.Fields); err != nil {
			return err
		}
		r, err := s.previewRecipient(listID, c.FormValue("recipient_id"))
		if err != nil {
			return err
		}
		if r != nil {
			p.Recipient = r
			if values, err = r.Values(); err != nil {
				return err
			}
		}
	}
	p.Merged = mergeLetter(in.Subject, in.Body, p.Format, values)
	return render(c, previewPanel(p))
}

// previewRecipient is the chosen recipient when it is on the list, else the
// list's first recipient, else nil for an empty list.
func (s *Server) previewRecipient(listID int64, raw string) (*Recipient, error) {
	if rid, err := strconv.ParseInt(raw, 10, 64); err == nil {
		r, err := one[Recipient](s.db, `SELECT * FROM recipients WHERE id = ? AND list_id = ?`, rid, listID)
		if err == nil {
			return &r, nil
		}
		if !errors.Is(err, errNotFound) {
			return nil, err
		}
	}
	r, err := one[Recipient](s.db, `SELECT * FROM recipients WHERE list_id = ? ORDER BY email LIMIT 1`, listID)
	if errors.Is(err, errNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}
