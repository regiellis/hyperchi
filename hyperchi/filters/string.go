package filters

import (
	"fmt"
	"html/template"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// titleCase uppercases the first letter of each word, as the deprecated
// strings.Title did. Words are separated by runs of runes that are neither
// letters nor numbers, and the rest of each word is left as is.
func titleCase(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	prevIsSeparator := true
	for _, r := range s {
		if prevIsSeparator && unicode.IsLetter(r) {
			b.WriteRune(unicode.ToTitle(r))
		} else {
			b.WriteRune(r)
		}
		prevIsSeparator = !unicode.IsLetter(r) && !unicode.IsNumber(r)
	}
	return b.String()
}

func registerStringFilters() {
	Register("truncate", truncate)
	Register("truncateWords", truncateWords)
	Register("upper", strings.ToUpper)
	Register("lower", strings.ToLower)
	Register("title", titleCase)
	Register("capitalize", capitalize)
	Register("trim", strings.TrimSpace)
	Register("trimPrefix", trimPrefix)
	Register("trimSuffix", trimSuffix)
	Register("replace", replace)
	Register("replaceAll", strings.ReplaceAll)
	Register("hasPrefix", strings.HasPrefix)
	Register("hasSuffix", strings.HasSuffix)
	Register("split", strings.Split)
	Register("join", join)
	Register("repeat", strings.Repeat)
	Register("slug", slug)
	Register("camelCase", camelCase)
	Register("snakeCase", snakeCase)
	Register("kebabCase", kebabCase)
	Register("pascalCase", pascalCase)
	Register("pluralize", pluralize)
	Register("singularize", singularize)
	Register("wordWrap", wordWrap)
	Register("indent", indent)
	Register("dedent", dedent)
	Register("quote", quote)
	Register("unquote", unquote)
	Register("stripTags", stripTags)
	Register("nl2br", nl2br)
	Register("br2nl", br2nl)
}

func truncate(s string, length int, suffix ...string) string {
	if utf8.RuneCountInString(s) <= length {
		return s
	}

	ellipsis := "..."
	if len(suffix) > 0 {
		ellipsis = suffix[0]
	}

	runes := []rune(s)
	if length > len(ellipsis) {
		return string(runes[:length-len(ellipsis)]) + ellipsis
	}
	return string(runes[:length])
}

func truncateWords(s string, count int, suffix ...string) string {
	words := strings.Fields(s)
	if len(words) <= count {
		return s
	}

	ellipsis := "..."
	if len(suffix) > 0 {
		ellipsis = suffix[0]
	}

	return strings.Join(words[:count], " ") + ellipsis
}

func capitalize(s string) string {
	if s == "" {
		return ""
	}
	r, size := utf8.DecodeRuneInString(s)
	return string(unicode.ToUpper(r)) + s[size:]
}

func trimPrefix(s, prefix string) string {
	return strings.TrimPrefix(s, prefix)
}

func trimSuffix(s, suffix string) string {
	return strings.TrimSuffix(s, suffix)
}

func replace(s, old, new string, n int) string {
	return strings.Replace(s, old, new, n)
}

func join(elems []string, sep string) string {
	return strings.Join(elems, sep)
}

var (
	slugRe      = regexp.MustCompile(`[^\w\s-]`)
	slugSpaceRe = regexp.MustCompile(`[-\s]+`)
	stripTagsRe = regexp.MustCompile(`<[^>]*>`)
)

func slug(s string) string {
	s = strings.ToLower(s)
	s = slugRe.ReplaceAllString(s, "")
	s = slugSpaceRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	return s
}

func camelCase(s string) string {
	words := strings.FieldsFunc(s, func(r rune) bool {
		return r == '_' || r == '-' || r == ' '
	})

	if len(words) == 0 {
		return ""
	}

	result := strings.ToLower(words[0])
	for i := 1; i < len(words); i++ {
		result += titleCase(strings.ToLower(words[i]))
	}
	return result
}

func snakeCase(s string) string {
	var result []rune
	for i, r := range s {
		if unicode.IsUpper(r) && i > 0 {
			result = append(result, '_')
		}
		result = append(result, unicode.ToLower(r))
	}
	return string(result)
}

func kebabCase(s string) string {
	return strings.ReplaceAll(snakeCase(s), "_", "-")
}

func pascalCase(s string) string {
	words := strings.FieldsFunc(s, func(r rune) bool {
		return r == '_' || r == '-' || r == ' '
	})

	result := ""
	for _, word := range words {
		result += titleCase(strings.ToLower(word))
	}
	return result
}

func pluralize(s string, count ...int) string {
	n := 2
	if len(count) > 0 {
		n = count[0]
	}

	if n == 1 {
		return s
	}

	if strings.HasSuffix(s, "y") && !strings.HasSuffix(s, "ay") && !strings.HasSuffix(s, "ey") && !strings.HasSuffix(s, "oy") && !strings.HasSuffix(s, "uy") {
		return s[:len(s)-1] + "ies"
	}
	if strings.HasSuffix(s, "s") || strings.HasSuffix(s, "x") || strings.HasSuffix(s, "z") ||
		strings.HasSuffix(s, "ch") || strings.HasSuffix(s, "sh") {
		return s + "es"
	}
	return s + "s"
}

func singularize(s string) string {
	if strings.HasSuffix(s, "ies") {
		return s[:len(s)-3] + "y"
	}
	if strings.HasSuffix(s, "es") {
		return s[:len(s)-2]
	}
	if strings.HasSuffix(s, "s") {
		return s[:len(s)-1]
	}
	return s
}

func wordWrap(s string, width int) string {
	if width <= 0 {
		return s
	}

	var result []string
	lines := strings.Split(s, "\n")

	for _, line := range lines {
		if len(line) <= width {
			result = append(result, line)
			continue
		}

		words := strings.Fields(line)
		currentLine := ""

		for _, word := range words {
			if currentLine == "" {
				currentLine = word
			} else if len(currentLine)+1+len(word) <= width {
				currentLine += " " + word
			} else {
				result = append(result, currentLine)
				currentLine = word
			}
		}

		if currentLine != "" {
			result = append(result, currentLine)
		}
	}

	return strings.Join(result, "\n")
}

func indent(s string, n int, char ...string) string {
	indentStr := strings.Repeat(" ", n)
	if len(char) > 0 {
		indentStr = strings.Repeat(char[0], n)
	}

	lines := strings.Split(s, "\n")
	for i := range lines {
		if lines[i] != "" {
			lines[i] = indentStr + lines[i]
		}
	}
	return strings.Join(lines, "\n")
}

func dedent(s string) string {
	lines := strings.Split(s, "\n")
	if len(lines) == 0 {
		return s
	}

	minIndent := -1
	for _, line := range lines {
		if line == "" {
			continue
		}
		indent := 0
		for _, r := range line {
			if r == ' ' || r == '\t' {
				indent++
			} else {
				break
			}
		}
		if minIndent == -1 || indent < minIndent {
			minIndent = indent
		}
	}

	if minIndent <= 0 {
		return s
	}

	for i := range lines {
		if len(lines[i]) >= minIndent {
			lines[i] = lines[i][minIndent:]
		}
	}

	return strings.Join(lines, "\n")
}

func quote(s string) string {
	return fmt.Sprintf("%q", s)
}

func unquote(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}

func stripTags(s string) template.HTML {
	return template.HTML(stripTagsRe.ReplaceAllString(s, ""))
}

func nl2br(s string) template.HTML {
	return template.HTML(strings.ReplaceAll(template.HTMLEscapeString(s), "\n", "<br>"))
}

func br2nl(s string) string {
	s = strings.ReplaceAll(s, "<br>", "\n")
	s = strings.ReplaceAll(s, "<br/>", "\n")
	s = strings.ReplaceAll(s, "<br />", "\n")
	return s
}
