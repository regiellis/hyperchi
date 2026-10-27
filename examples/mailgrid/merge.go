package main

import (
	"html"
	"regexp"
	"sort"
	"strings"
)

// Letter formats.
const (
	formatText = "text"
	formatHTML = "html"
)

// mergeTag is the one merge syntax: {{name}}, with optional spaces inside
// the braces. A field name is a letter or underscore followed by letters,
// digits, or underscores. Anything else between braces is left as written.
// This is a deliberate substitute for text/template: a letter can only insert
// values, never call functions or loop.
var mergeTag = regexp.MustCompile(`\{\{\s*([A-Za-z_][A-Za-z0-9_]*)\s*\}\}`)

// mergeFields lists the distinct fields the texts use, in order of first use.
// Names are lowercased, matching how imported CSV columns are stored.
func mergeFields(texts ...string) []string {
	var fields []string
	seen := map[string]bool{}
	for _, t := range texts {
		for _, m := range mergeTag.FindAllStringSubmatch(t, -1) {
			name := strings.ToLower(m[1])
			if !seen[name] {
				seen[name] = true
				fields = append(fields, name)
			}
		}
	}
	return fields
}

// Merged is a letter rendered for one recipient.
type Merged struct {
	Subject string
	Body    string
	// Missing lists the fields the letter uses that this recipient has no
	// value for. They rendered as empty text.
	Missing []string
}

// mergeLetter renders subject and body with values. In an HTML letter every
// value is HTML-escaped before it is inserted; the letter's own markup is
// trusted, because the team wrote it. The subject is always plain text, and
// line breaks in it become spaces so a value cannot add a mail header.
func mergeLetter(subject, body, format string, values map[string]string) Merged {
	missing := map[string]bool{}
	s := merge(subject, values, false, missing)
	s = strings.Join(strings.Fields(strings.NewReplacer("\r", " ", "\n", " ").Replace(s)), " ")
	b := merge(body, values, format == formatHTML, missing)
	out := Merged{Subject: s, Body: b}
	for f := range missing {
		out.Missing = append(out.Missing, f)
	}
	sort.Strings(out.Missing)
	return out
}

func merge(tpl string, values map[string]string, escape bool, missing map[string]bool) string {
	return mergeTag.ReplaceAllStringFunc(tpl, func(tag string) string {
		name := strings.ToLower(mergeTag.FindStringSubmatch(tag)[1])
		v := values[name]
		if strings.TrimSpace(v) == "" {
			missing[name] = true
		}
		if escape {
			return html.EscapeString(v)
		}
		return v
	})
}
