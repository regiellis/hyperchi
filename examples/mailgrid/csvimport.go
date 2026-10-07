package main

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net/mail"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Import limits. The upload size limit is configured on the server; these
// bound what one file may contain.
const (
	maxImportRows   = 20000
	maxImportErrors = 500
	maxFieldValue   = 2000
	maxNameLength   = 200
	maxEmailLength  = 254
)

// ImportRow is a valid recipient read from the file.
type ImportRow struct {
	Line   int
	Email  string
	Name   string
	Fields map[string]string
}

// ImportProblem is a row the import skipped, with the reason.
type ImportProblem struct {
	Line    int
	Email   string
	Message string
}

// ParsedCSV is what a file holds before it touches the database.
type ParsedCSV struct {
	// Columns are the merge fields the file adds beyond email and name.
	Columns []string
	Rows    []ImportRow
	// Problems are rows that were skipped: invalid email, malformed CSV,
	// wrong column count, or a value over the size limits.
	Problems []ImportProblem
	// Duplicates are rows whose email already appeared earlier in the file.
	Duplicates []ImportProblem
	// Truncated reports that the file had more problems than are listed.
	Truncated bool
}

var nonField = regexp.MustCompile(`[^a-z0-9_]+`)
var fieldName = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

// normalizeColumn turns a CSV header into a merge-field name:
// "First Name" becomes first_name.
func normalizeColumn(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	h = nonField.ReplaceAllString(h, "_")
	return strings.Trim(h, "_")
}

// errHeader is a file the import cannot read at all.
type errHeader struct{ msg string }

// Error returns the message shown to the person importing.
func (e errHeader) Error() string { return e.msg }

// parseRecipientsCSV reads a CSV file with a header row. It needs an email
// column; name is optional, and every other column becomes a merge field.
// Problems with single rows are collected and the rest of the file still
// imports. A file without a usable header is an error.
func parseRecipientsCSV(r io.Reader) (*ParsedCSV, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	data = bytes.TrimPrefix(data, []byte("\uFEFF"))
	if !utf8.Valid(data) {
		return nil, errHeader{"The file is not UTF-8 text. Export it from your spreadsheet as CSV UTF-8."}
	}
	cr := csv.NewReader(bytes.NewReader(data))
	cr.FieldsPerRecord = -1
	cr.TrimLeadingSpace = true

	header, err := cr.Read()
	if errors.Is(err, io.EOF) {
		return nil, errHeader{"The file is empty."}
	}
	if err != nil {
		return nil, errHeader{fmt.Sprintf("The header row could not be read: %v.", err)}
	}
	cols := make([]string, len(header))
	emailCol, nameCol := -1, -1
	seen := map[string]bool{}
	out := &ParsedCSV{}
	for i, h := range header {
		name := normalizeColumn(h)
		if !fieldName.MatchString(name) {
			return nil, errHeader{fmt.Sprintf("Column %d (%q) has no usable name. Use letters, digits, and underscores.", i+1, h)}
		}
		if seen[name] {
			return nil, errHeader{fmt.Sprintf("Column %q appears twice.", name)}
		}
		seen[name] = true
		cols[i] = name
		switch name {
		case "email":
			emailCol = i
		case "name":
			nameCol = i
		default:
			out.Columns = append(out.Columns, name)
		}
	}
	if emailCol < 0 {
		return nil, errHeader{"The file has no email column. The first row must name the columns, one of them email."}
	}

	firstLine := map[string]int{}
	problem := func(list *[]ImportProblem, p ImportProblem) {
		if len(out.Problems)+len(out.Duplicates) >= maxImportErrors {
			out.Truncated = true
			return
		}
		*list = append(*list, p)
	}
	for {
		record, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		var parseErr *csv.ParseError
		if errors.As(err, &parseErr) {
			problem(&out.Problems, ImportProblem{Line: parseErr.Line, Message: "Malformed CSV: " + parseErr.Err.Error() + "."})
			continue
		}
		if err != nil {
			return nil, err
		}
		line, _ := cr.FieldPos(0)
		if isBlank(record) {
			continue
		}
		if len(out.Rows) >= maxImportRows {
			return nil, errHeader{fmt.Sprintf("The file has more than %d recipients. Split it into smaller files.", maxImportRows)}
		}
		if len(record) != len(cols) {
			problem(&out.Problems, ImportProblem{Line: line, Message: fmt.Sprintf("Expected %d columns, found %d.", len(cols), len(record))})
			continue
		}
		raw := strings.TrimSpace(record[emailCol])
		email, msg := normalizeEmail(raw)
		if msg != "" {
			problem(&out.Problems, ImportProblem{Line: line, Email: raw, Message: msg})
			continue
		}
		if first, dup := firstLine[email]; dup {
			problem(&out.Duplicates, ImportProblem{Line: line, Email: email, Message: fmt.Sprintf("Same email as line %d; skipped.", first)})
			continue
		}
		row := ImportRow{Line: line, Email: email, Fields: map[string]string{}}
		if nameCol >= 0 {
			row.Name = strings.TrimSpace(record[nameCol])
		}
		if utf8.RuneCountInString(row.Name) > maxNameLength {
			problem(&out.Problems, ImportProblem{Line: line, Email: email, Message: fmt.Sprintf("Name is longer than %d characters.", maxNameLength)})
			continue
		}
		tooLong := ""
		for i, v := range record {
			if i == emailCol || i == nameCol {
				continue
			}
			v = strings.TrimSpace(v)
			if utf8.RuneCountInString(v) > maxFieldValue {
				tooLong = cols[i]
				break
			}
			if v != "" {
				row.Fields[cols[i]] = v
			}
		}
		if tooLong != "" {
			problem(&out.Problems, ImportProblem{Line: line, Email: email, Message: fmt.Sprintf("%s is longer than %d characters.", tooLong, maxFieldValue)})
			continue
		}
		firstLine[email] = line
		out.Rows = append(out.Rows, row)
	}
	return out, nil
}

func isBlank(record []string) bool {
	for _, v := range record {
		if strings.TrimSpace(v) != "" {
			return false
		}
	}
	return true
}

// normalizeEmail checks a bare address (no display name) and lowercases it,
// so the same mailbox typed twice is one recipient. It returns a message for
// the person importing when the address is not usable.
func normalizeEmail(raw string) (string, string) {
	if raw == "" {
		return "", "Email is empty."
	}
	if len(raw) > maxEmailLength {
		return "", "Email is too long."
	}
	addr, err := mail.ParseAddress(raw)
	if err != nil || addr.Name != "" || addr.Address != raw {
		return "", "Not a valid email address."
	}
	at := strings.LastIndex(raw, "@")
	if at < 1 || !strings.Contains(raw[at+1:], ".") || strings.HasSuffix(raw, ".") {
		return "", "Not a valid email address."
	}
	return strings.ToLower(raw), ""
}
