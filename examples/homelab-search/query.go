package main

import (
	"strings"
	"unicode"
)

const (
	maxQueryRunes = 200 // longer input is cut, not rejected
	maxQueryTerms = 8   // words and phrases after the eighth are dropped
)

// boundQuery collapses whitespace and caps the query length in characters. A
// trailing space survives, because it tells matchExpr the last word is done.
func boundQuery(q string) string {
	if r := []rune(q); len(r) > maxQueryRunes {
		q = string(r[:maxQueryRunes])
	}
	trailing := strings.HasSuffix(q, " ")
	q = strings.Join(strings.Fields(q), " ")
	if trailing && q != "" {
		q += " "
	}
	return q
}

// matchExpr turns what a person typed into an FTS5 MATCH expression. User
// input is never passed through as FTS5 syntax: every word becomes a quoted
// string, so operators (AND, OR, NOT, NEAR), column filters (title:),
// parentheses, and carets in the input are plain text. What it does accept:
//
//   - Words. Every word must match somewhere in the document.
//   - "Quoted phrases", matched in order. An unclosed quote runs to the end.
//   - A trailing * on a word or phrase for a prefix match (backu* finds backup).
//   - Punctuation inside a word joins a phrase: nginx.conf, docker-compose.
//
// The last word is matched as a prefix while it is still being typed (no
// trailing space), so results appear before a word is finished. An input with
// no letters or digits yields "", which callers treat as no query.
func matchExpr(input string) string {
	runes := []rune(input)
	if len(runes) > maxQueryRunes {
		runes = runes[:maxQueryRunes]
	}
	var groups []string
	lastBare := -1 // index in groups of a bare word the user may still be typing
	add := func(tokens []string, prefix bool) {
		if len(tokens) == 0 || len(groups) == maxQueryTerms {
			return
		}
		g := `"` + strings.Join(tokens, " ") + `"`
		if prefix {
			g += "*"
		}
		groups = append(groups, g)
	}
	for i := 0; i < len(runes); {
		switch {
		case unicode.IsSpace(runes[i]):
			i++
		case runes[i] == '"':
			end := i + 1
			for end < len(runes) && runes[end] != '"' {
				end++
			}
			tokens := wordTokens(runes[i+1 : min(end, len(runes))])
			i = end + 1 // past the closing quote, or past the end
			prefix := i < len(runes) && runes[i] == '*'
			if prefix {
				i++
			}
			add(tokens, prefix)
			lastBare = -1
		default:
			end := i
			for end < len(runes) && !unicode.IsSpace(runes[end]) && runes[end] != '"' {
				end++
			}
			word := runes[i:end]
			i = end
			prefix := len(word) > 0 && word[len(word)-1] == '*'
			before := len(groups)
			add(wordTokens(word), prefix)
			lastBare = -1
			if len(groups) > before && !prefix {
				lastBare = len(groups) - 1
			}
		}
	}
	// As-you-type: a final bare word with no space after it is a prefix.
	if lastBare == len(groups)-1 && lastBare >= 0 && !unicode.IsSpace(runes[len(runes)-1]) {
		groups[lastBare] += "*"
	}
	return strings.Join(groups, " ")
}

// wordTokens splits text into runs of letters and digits, the same way the
// index's unicode61 tokenizer does. Tokens contain no quotes or operators, so
// they are safe inside an FTS5 string.
func wordTokens(text []rune) []string {
	return strings.FieldsFunc(string(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}
