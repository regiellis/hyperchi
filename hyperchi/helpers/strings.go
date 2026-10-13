package helpers

import (
	"crypto/rand"
	"math/big"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Str is a string with chainable methods: S(" Hi ").Trim().Lower().
//
// Every length, offset, and width Str takes or returns counts runes (Unicode
// code points), not bytes: Len, Index, LastIndex, Slice, Left, Right, Center,
// PadLeft, and PadRight agree with each other, so s.Left(s.Index(":")) never
// splits a multi-byte character. Convert with []rune or utf8 before using a
// Str offset to index the underlying string directly.
type Str string

// S converts s to a Str.
func S(s string) Str {
	return Str(s)
}

// String returns s as a plain string.
func (s Str) String() string {
	return string(s)
}

// Len returns the number of runes in the string.
func (s Str) Len() int {
	return utf8.RuneCountInString(string(s))
}

// IsEmpty reports whether s is "".
func (s Str) IsEmpty() bool {
	return len(string(s)) == 0
}

// IsNotEmpty reports whether s is not "".
func (s Str) IsNotEmpty() bool {
	return len(string(s)) > 0
}

// IsBlank reports whether s is empty or only whitespace.
func (s Str) IsBlank() bool {
	return strings.TrimSpace(string(s)) == ""
}

// IsNotBlank reports whether s has a non-whitespace character.
func (s Str) IsNotBlank() bool {
	return !s.IsBlank()
}

// Trim removes leading and trailing whitespace.
func (s Str) Trim() Str {
	return Str(strings.TrimSpace(string(s)))
}

// TrimLeft removes leading whitespace.
func (s Str) TrimLeft() Str {
	return Str(strings.TrimLeftFunc(string(s), unicode.IsSpace))
}

// TrimRight removes trailing whitespace.
func (s Str) TrimRight() Str {
	return Str(strings.TrimRightFunc(string(s), unicode.IsSpace))
}

// TrimPrefix removes prefix from the start of s if it is there.
func (s Str) TrimPrefix(prefix string) Str {
	return Str(strings.TrimPrefix(string(s), prefix))
}

// TrimSuffix removes suffix from the end of s if it is there.
func (s Str) TrimSuffix(suffix string) Str {
	return Str(strings.TrimSuffix(string(s), suffix))
}

// Lower returns s in lowercase.
func (s Str) Lower() Str {
	return Str(strings.ToLower(string(s)))
}

// Upper returns s in uppercase.
func (s Str) Upper() Str {
	return Str(strings.ToUpper(string(s)))
}

// Title uppercases the first letter of each word. A word starts after any
// rune that is not a letter or digit; other letters are left as they are.
func (s Str) Title() Str {
	var b strings.Builder
	b.Grow(len(s))
	prevIsSeparator := true
	for _, r := range string(s) {
		if prevIsSeparator && unicode.IsLetter(r) {
			b.WriteRune(unicode.ToTitle(r))
		} else {
			b.WriteRune(r)
		}
		prevIsSeparator = !unicode.IsLetter(r) && !unicode.IsNumber(r)
	}
	return Str(b.String())
}

// Capitalize uppercases the first rune and leaves the rest unchanged.
func (s Str) Capitalize() Str {
	str := string(s)
	if len(str) == 0 {
		return s
	}
	runes := []rune(str)
	runes[0] = unicode.ToUpper(runes[0])
	return Str(string(runes))
}

// Contains reports whether substr is in s.
func (s Str) Contains(substr string) bool {
	return strings.Contains(string(s), substr)
}

// ContainsAny reports whether any rune in chars is in s.
func (s Str) ContainsAny(chars string) bool {
	return strings.ContainsAny(string(s), chars)
}

// StartsWith reports whether s begins with prefix.
func (s Str) StartsWith(prefix string) bool {
	return strings.HasPrefix(string(s), prefix)
}

// EndsWith reports whether s ends with suffix.
func (s Str) EndsWith(suffix string) bool {
	return strings.HasSuffix(string(s), suffix)
}

// Count returns the number of non-overlapping occurrences of substr.
func (s Str) Count(substr string) int {
	return strings.Count(string(s), substr)
}

// Index returns the rune offset of the first occurrence of substr, or -1.
func (s Str) Index(substr string) int {
	return runeOffset(string(s), strings.Index(string(s), substr))
}

// LastIndex returns the rune offset of the last occurrence of substr, or -1.
func (s Str) LastIndex(substr string) int {
	return runeOffset(string(s), strings.LastIndex(string(s), substr))
}

// runeOffset converts a byte offset into s to a rune offset, keeping -1.
func runeOffset(s string, byteOffset int) int {
	if byteOffset < 0 {
		return byteOffset
	}
	return utf8.RuneCountInString(s[:byteOffset])
}

// Replace replaces every occurrence of old with new.
func (s Str) Replace(old, new string) Str {
	return Str(strings.ReplaceAll(string(s), old, new))
}

// ReplaceN replaces the first n occurrences of old with new. A negative n
// replaces all of them.
func (s Str) ReplaceN(old, new string, n int) Str {
	return Str(strings.Replace(string(s), old, new, n))
}

// Split splits s around each sep (see strings.Split).
func (s Str) Split(sep string) []string {
	return strings.Split(string(s), sep)
}

// SplitN splits s around sep into at most n parts (see strings.SplitN).
func (s Str) SplitN(sep string, n int) []string {
	return strings.SplitN(string(s), sep, n)
}

// SplitLines splits s at each "\n". A "\r" before the newline is kept.
func (s Str) SplitLines() []string {
	return strings.Split(string(s), "\n")
}

// SplitWhitespace splits s around runs of whitespace (see strings.Fields).
func (s Str) SplitWhitespace() []string {
	return strings.Fields(string(s))
}

// Join joins parts with s as the separator.
func (s Str) Join(parts []string) Str {
	return Str(strings.Join(parts, string(s)))
}

// Repeat repeats the string n times. A non-positive n returns "".
func (s Str) Repeat(n int) Str {
	if n <= 0 {
		return ""
	}
	return Str(strings.Repeat(string(s), n))
}

// Reverse reverses s rune by rune.
func (s Str) Reverse() Str {
	runes := []rune(string(s))
	for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
		runes[i], runes[j] = runes[j], runes[i]
	}
	return Str(string(runes))
}

// Slice returns the runes from start up to end. A negative index counts from
// the end, and both indices are clamped to the string's bounds.
func (s Str) Slice(start, end int) Str {
	runes := []rune(string(s))
	length := len(runes)

	if start < 0 {
		start += length
	}
	if end < 0 {
		end += length
	}
	start = max(start, 0)
	end = min(end, length)
	if start >= end {
		return ""
	}
	return Str(runes[start:end])
}

// Left returns the leftmost n runes. A non-positive n returns "".
func (s Str) Left(n int) Str {
	if n <= 0 {
		return ""
	}
	runes := []rune(string(s))
	if n >= len(runes) {
		return s
	}
	return Str(runes[:n])
}

// Right returns the rightmost n runes. A non-positive n returns "".
func (s Str) Right(n int) Str {
	if n <= 0 {
		return ""
	}
	runes := []rune(string(s))
	if n >= len(runes) {
		return s
	}
	return Str(runes[len(runes)-n:])
}

// Center centers the string in a field width runes wide. The fill is the
// first rune of fill, a space by default; an empty fill also means a space.
// A string already at least width runes long is returned unchanged.
func (s Str) Center(width int, fill ...string) Str {
	padding := width - s.Len()
	if padding <= 0 {
		return s
	}
	fillRune := padRune(fill)
	left := padding / 2
	return Str(strings.Repeat(fillRune, left) + string(s) + strings.Repeat(fillRune, padding-left))
}

// PadLeft pads the string on the left to width runes, filling with the first
// rune of fill (a space by default).
func (s Str) PadLeft(width int, fill ...string) Str {
	padding := width - s.Len()
	if padding <= 0 {
		return s
	}
	return Str(strings.Repeat(padRune(fill), padding) + string(s))
}

// PadRight pads the string on the right to width runes, filling with the
// first rune of fill (a space by default).
func (s Str) PadRight(width int, fill ...string) Str {
	padding := width - s.Len()
	if padding <= 0 {
		return s
	}
	return Str(string(s) + strings.Repeat(padRune(fill), padding))
}

// padRune returns the first rune of the optional fill argument as a string,
// or a space when none is given, so padding is always one rune per column.
func padRune(fill []string) string {
	if len(fill) == 0 || fill[0] == "" {
		return " "
	}
	r, _ := utf8.DecodeRuneInString(fill[0])
	return string(r)
}

// whitespaceRun matches the runs RemoveWhitespace and CollapseWhitespace
// replace. It is compiled once, not per call.
var whitespaceRun = regexp.MustCompile(`\s+`)

// RemoveWhitespace removes every whitespace rune.
func (s Str) RemoveWhitespace() Str {
	return Str(whitespaceRun.ReplaceAllString(string(s), ""))
}

// CollapseWhitespace trims s and replaces each run of whitespace with one
// space.
func (s Str) CollapseWhitespace() Str {
	return Str(whitespaceRun.ReplaceAllString(strings.TrimSpace(string(s)), " "))
}

// IsDigits reports whether s is non-empty and every rune is a Unicode digit.
func (s Str) IsDigits() bool {
	str := string(s)
	if len(str) == 0 {
		return false
	}
	for _, r := range str {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

// IsAlpha reports whether s is non-empty and every rune is a letter.
func (s Str) IsAlpha() bool {
	str := string(s)
	if len(str) == 0 {
		return false
	}
	for _, r := range str {
		if !unicode.IsLetter(r) {
			return false
		}
	}
	return true
}

// IsAlphaNumeric reports whether s is non-empty and every rune is a letter
// or digit.
func (s Str) IsAlphaNumeric() bool {
	str := string(s)
	if len(str) == 0 {
		return false
	}
	for _, r := range str {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

// ToInt parses the string as a base-10 int. Use ToIntOr for a fallback value
// instead of an error.
func (s Str) ToInt() (int, error) {
	return strconv.Atoi(string(s))
}

// ToIntOr parses the string as a base-10 int, returning defaultValue when it
// does not parse.
func (s Str) ToIntOr(defaultValue int) int {
	i, err := s.ToInt()
	if err != nil {
		return defaultValue
	}
	return i
}

// ToFloat parses the string as a float64. Use ToFloatOr for a fallback value
// instead of an error.
func (s Str) ToFloat() (float64, error) {
	return strconv.ParseFloat(string(s), 64)
}

// ToFloatOr parses the string as a float64, returning defaultValue when it
// does not parse.
func (s Str) ToFloatOr(defaultValue float64) float64 {
	f, err := s.ToFloat()
	if err != nil {
		return defaultValue
	}
	return f
}

// ToBool reports whether the trimmed s is "true", "1", "yes", or "on",
// ignoring case. Anything else is false.
func (s Str) ToBool() bool {
	str := strings.ToLower(strings.TrimSpace(string(s)))
	return str == "true" || str == "1" || str == "yes" || str == "on"
}

// Matches reports whether the string contains a match of the regular
// expression pattern. An invalid pattern returns an error rather than false.
// The pattern is compiled on every call; precompile with regexp.MustCompile
// on hot paths.
func (s Str) Matches(pattern string) (bool, error) {
	return regexp.MatchString(pattern, string(s))
}

// FindAll returns every match of pattern, or the compile error for an invalid
// pattern. The pattern is compiled on every call.
func (s Str) FindAll(pattern string) ([]string, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	return re.FindAllString(string(s), -1), nil
}

// RegexReplace replaces every match of pattern, or returns the compile error for
// an invalid pattern. The pattern is compiled on every call.
func (s Str) RegexReplace(pattern, replacement string) (Str, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return s, err
	}
	return Str(re.ReplaceAllString(string(s), replacement)), nil
}

// Join joins parts with sep.
func Join(sep string, parts ...string) string {
	return strings.Join(parts, sep)
}

// JoinLines joins lines with "\n".
func JoinLines(lines ...string) string {
	return strings.Join(lines, "\n")
}

// JoinSpaces joins parts with single spaces.
func JoinSpaces(parts ...string) string {
	return strings.Join(parts, " ")
}

// Truncate shortens s to at most maxLen runes, ending in suffix ("..." by
// default) when it cuts. Counting runes keeps multi-byte characters whole.
// A non-positive maxLen returns "".
func Truncate(s string, maxLen int, suffix ...string) string {
	if maxLen <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}

	ending := "..."
	if len(suffix) > 0 {
		ending = suffix[0]
	}
	endingLen := utf8.RuneCountInString(ending)

	if maxLen <= endingLen {
		return string(runes[:maxLen])
	}

	return string(runes[:maxLen-endingLen]) + ending
}

// Words splits s around runs of whitespace.
func Words(s string) []string {
	return strings.Fields(s)
}

// WordCount returns the number of whitespace-separated words in s.
func WordCount(s string) int {
	return len(strings.Fields(s))
}

// CamelCase converts s to camelCase: "hello world" and "hello_world" become
// "helloWorld". Words split at separators and at case changes, so acronyms
// keep their boundary ("userID" becomes "userId", "HTTPServer" becomes
// "httpServer"). Accented Latin letters are transliterated (see Slugify).
func CamelCase(s string) string {
	var b strings.Builder
	for i, word := range caseWords(s) {
		if i == 0 {
			b.WriteString(word)
			continue
		}
		b.WriteString(capitalizeASCII(word))
	}
	return b.String()
}

// PascalCase converts s to PascalCase, splitting words as CamelCase does.
func PascalCase(s string) string {
	var b strings.Builder
	for _, word := range caseWords(s) {
		b.WriteString(capitalizeASCII(word))
	}
	return b.String()
}

// SnakeCase converts s to snake_case, splitting words as CamelCase does:
// "userID" becomes "user_id" and "HTTPServer" becomes "http_server".
func SnakeCase(s string) string {
	return strings.Join(caseWords(s), "_")
}

// KebabCase converts s to kebab-case, splitting words as CamelCase does.
func KebabCase(s string) string {
	return strings.Join(caseWords(s), "-")
}

// Slugify creates a URL-friendly slug of lowercase ASCII letters, digits, and
// hyphens: "Crème Brûlée à la carte" becomes "creme-brulee-a-la-carte".
// Words split only at separators, never at case changes, so "MiXeD CaSe 42"
// becomes "mixed-case-42". Accented Latin letters are transliterated (é to e,
// ß to ss, ø to o, æ to ae). Letters with no ASCII transliteration, such as
// CJK or Cyrillic, are dropped.
func Slugify(s string) string {
	return KebabCase(strings.ToLower(s))
}

// capitalizeASCII uppercases the first byte of a lowercase ASCII word.
func capitalizeASCII(word string) string {
	if word == "" {
		return word
	}
	return strings.ToUpper(word[:1]) + word[1:]
}

// caseWords splits s into lowercase ASCII words for the case converters.
// Any rune that is not a letter or digit separates words. Within a run of
// letters and digits a word also ends before an uppercase letter that follows
// a lowercase letter or digit ("userID" is user|ID) and before the last
// uppercase letter of an acronym that leads into a lowercase letter
// ("HTTPServer" is HTTP|Server). Each word is then transliterated to ASCII.
func caseWords(s string) []string {
	runes := []rune(s)
	var words []string
	start := -1
	flush := func(end int) {
		if start >= 0 {
			if word := foldToASCII(runes[start:end]); word != "" {
				words = append(words, word)
			}
		}
		start = -1
	}
	for i, r := range runes {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			flush(i)
			continue
		}
		if start < 0 {
			start = i
			continue
		}
		if unicode.IsUpper(r) {
			prev := runes[i-1]
			lowerBefore := unicode.IsLower(prev) || unicode.IsDigit(prev)
			acronymEnd := unicode.IsUpper(prev) && i+1 < len(runes) && unicode.IsLower(runes[i+1])
			if lowerBefore || acronymEnd {
				flush(i)
				start = i
			}
		}
	}
	flush(len(runes))
	return words
}

// foldToASCII lowercases a word and transliterates it to ASCII letters and
// digits, dropping runes that have no transliteration.
func foldToASCII(word []rune) string {
	var b strings.Builder
	for _, r := range word {
		lower := unicode.ToLower(r)
		if lower < utf8.RuneSelf {
			if unicode.IsLetter(lower) || unicode.IsDigit(lower) {
				b.WriteRune(lower)
			}
			continue
		}
		b.WriteString(latinTransliteration[lower])
	}
	return b.String()
}

// latinTransliteration maps lowercase accented Latin letters (Latin-1
// Supplement, Latin Extended-A, and the common Romanian and pinyin forms) to
// their usual ASCII spelling. Uppercase letters fold to lowercase first.
var latinTransliteration = buildTransliteration(map[string]string{
	"àáâãäåāăąǎǻ": "a", "æǽ": "ae", "çćĉċč": "c", "ďđð": "d",
	"èéêëēĕėęě": "e", "ĝğġģ": "g", "ĥħ": "h", "ìíîïĩīĭįıǐ": "i",
	"ĳ": "ij", "ĵ": "j", "ķĸ": "k", "ĺļľŀł": "l", "ñńņňŉŋ": "n",
	"òóôõöøōŏőǒǿ": "o", "œ": "oe", "ŕŗř": "r", "śŝşšș": "s", "ß": "ss",
	"ţťŧț": "t", "þ": "th", "ùúûüũūŭůűųǔǖǘǚǜ": "u", "ŵ": "w",
	"ýÿŷ": "y", "źżž": "z", "ẞ": "ss",
})

// buildTransliteration expands each key's runes into a rune lookup table.
func buildTransliteration(groups map[string]string) map[rune]string {
	table := make(map[rune]string)
	for runes, ascii := range groups {
		for _, r := range runes {
			table[r] = ascii
		}
	}
	return table
}

// letters is the RandomString alphabet.
var letters = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789")

// RandomString returns length random characters from a-z, A-Z, and 0-9.
//
// Each character comes from crypto/rand, so the output is fit for tokens,
// identifiers, and nonces. Indices are drawn with rand.Int over the alphabet
// length, which is uniform; byte%len would bias toward the first characters.
//
// A non-positive length returns "". If the system random source fails,
// RandomString retries until it succeeds rather than emit a predictable
// character.
func RandomString(length int) string {
	if length <= 0 {
		return ""
	}

	b := make([]rune, length)
	max := big.NewInt(int64(len(letters)))
	for i := range b {
		n, err := rand.Int(rand.Reader, max)
		for err != nil {
			// crypto/rand failure is not expected; retry rather than produce
			// a biased or constant character.
			n, err = rand.Int(rand.Reader, max)
		}
		b[i] = letters[n.Int64()]
	}
	return string(b)
}

// Template replaces each {{key}} in template with vars[key]. Replacement is
// a single pass: a substituted value is never scanned for placeholders, so the
// result does not depend on map order and values cannot inject other vars.
func Template(template string, vars map[string]string) string {
	keys := make([]string, 0, len(vars))
	for key := range vars {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, 2*len(keys))
	for _, key := range keys {
		pairs = append(pairs, "{{"+key+"}}", vars[key])
	}
	return strings.NewReplacer(pairs...).Replace(template)
}

// Indent prefixes each non-blank line of s with indent.
func Indent(s string, indent string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) != "" {
			lines[i] = indent + line
		}
	}
	return strings.Join(lines, "\n")
}

// Dedent removes the leading whitespace that every non-blank line shares.
// Spaces and tabs each count as one column.
func Dedent(s string) string {
	lines := strings.Split(s, "\n")
	if len(lines) == 0 {
		return s
	}

	minIndent := -1
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
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

	for i, line := range lines {
		if strings.TrimSpace(line) != "" && len(line) >= minIndent {
			lines[i] = line[minIndent:]
		}
	}

	return strings.Join(lines, "\n")
}
