// Package helpers holds general-purpose utilities for HyperChi applications:
// .env loading, file and directory access, string and collection helpers, a
// JSON HTTP client, a leveled logger, a request profiler, error helpers, and
// generators for Air and deployment files.
//
//	port := GetEnvOr("PORT", "8080")
//	debug := GetEnvBoolOr("DEBUG", false)
//
//	content, err := ReadFile("settings.txt")
//	fallback := ReadFileOr("settings.txt", "default content")
//
//	slug := Slugify("My Blog Post") // "my-blog-post"
package helpers

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Env reads configuration from .env files, the process environment, and
// values set in code. It is safe for concurrent use.
//
// Lookup order: values set in code with Set, then the process environment,
// then values loaded from .env files. A deployment's real environment
// therefore always beats a .env file left in the working directory, the usual
// dotenv rule. EnvConfig.OverrideSystemEnv reverses the last two.
type Env struct {
	vars     map[string]string // from .env files and Set
	explicit map[string]bool   // keys set in code with Set; these beat the process environment
	override bool              // .env values beat the process environment (OverrideSystemEnv)
	loaded   []string          // files already loaded, in order
	mutex    sync.RWMutex
}

// EnvConfig configures Env.LoadConfig.
type EnvConfig struct {
	Files             []string          // .env files to load in order; empty loads the LoadDefault files
	OverrideSystemEnv bool              // .env values beat the process environment and are written into it
	Required          []string          // keys that must be set, or LoadConfig returns an error
	Defaults          map[string]string // values Set for keys no source has
}

// NewEnv returns an empty Env. Nothing is loaded until a Load method runs.
func NewEnv() *Env {
	return &Env{
		vars:     make(map[string]string),
		explicit: make(map[string]bool),
		loaded:   make([]string, 0),
	}
}

// LoadDefault loads .env, .env.local, and .env.development from the working
// directory, in that order, skipping any that do not exist. A later file
// wins for a key both define.
func (e *Env) LoadDefault() error {
	files := []string{".env", ".env.local", ".env.development"}

	for _, file := range files {
		if FileExists(file) {
			if err := e.LoadFile(file); err != nil {
				return fmt.Errorf("failed to load %s: %v", file, err)
			}
		}
	}

	return nil
}

// LoadFile reads KEY=value lines from filename. Blank lines and lines
// starting with # are skipped, an "export " prefix is allowed, and quoting
// and $VAR expansion follow the usual dotenv rules. Loading the same file
// name twice is a no-op. A malformed line returns an error naming the line.
func (e *Env) LoadFile(filename string) error {
	e.mutex.Lock()
	defer e.mutex.Unlock()

	for _, loaded := range e.loaded {
		if loaded == filename {
			return nil
		}
	}

	file, err := os.Open(filename)
	if err != nil {
		return err
	}
	// The handle is read-only, so a close error cannot lose data. Log it and
	// move on.
	defer func() {
		if cerr := file.Close(); cerr != nil {
			log.Printf("hyperchi: failed to close env file %s: %v", filename, cerr)
		}
	}()

	scanner := bufio.NewScanner(file)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())

		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		key, value, err := e.parseLine(line)
		if err != nil {
			return fmt.Errorf("error parsing %s line %d: %v", filename, lineNum, err)
		}

		if key != "" {
			e.vars[key] = value
		}
	}

	if err := scanner.Err(); err != nil {
		return err
	}

	e.loaded = append(e.loaded, filename)
	return nil
}

// LoadFiles calls LoadFile for each name in order and stops at the first
// error.
func (e *Env) LoadFiles(filenames ...string) error {
	for _, filename := range filenames {
		if err := e.LoadFile(filename); err != nil {
			return err
		}
	}
	return nil
}

// LoadConfig loads config.Files (or the LoadDefault files), applies
// config.Defaults, and returns an error listing any config.Required key that
// is still unset. With OverrideSystemEnv it also writes every .env value into
// the process environment.
func (e *Env) LoadConfig(config EnvConfig) error {
	e.mutex.Lock()
	e.override = config.OverrideSystemEnv
	e.mutex.Unlock()

	if len(config.Files) > 0 {
		if err := e.LoadFiles(config.Files...); err != nil {
			return err
		}
	} else {
		if err := e.LoadDefault(); err != nil {
			return err
		}
	}

	for key, value := range config.Defaults {
		if !e.Has(key) {
			e.Set(key, value)
		}
	}

	missing := make([]string, 0)
	for _, key := range config.Required {
		if !e.Has(key) && os.Getenv(key) == "" {
			missing = append(missing, key)
		}
	}

	if len(missing) > 0 {
		return fmt.Errorf("missing required environment variables: %v", missing)
	}

	if config.OverrideSystemEnv {
		for key, value := range e.vars {
			if err := os.Setenv(key, value); err != nil {
				return fmt.Errorf("failed to set system environment variable %s: %w", key, err)
			}
		}
	}

	return nil
}

// parseLine parses a single line from a .env file. LoadFile calls it while
// holding e.mutex, so variable references resolve through lookupLocked.
//
// Single-quoted values are literal. Double-quoted values decode \n, \t, \r,
// \", \\ and \$, then expand references. Unquoted values expand references.
// ${NAME} expands anywhere; $NAME expands at the start of a whitespace
// separated word. Whitespace inside a value is preserved, and substituted
// text is never expanded again.
//
// A # preceded by whitespace starts a comment: KEY=value # note sets "value".
// A # with no whitespace before it is part of the value (pa#ss), and a # inside
// quotes is always literal. A quoted value may be followed by a comment.
func (e *Env) parseLine(line string) (string, string, error) {
	line = strings.TrimPrefix(line, "export ")

	parts := strings.SplitN(line, "=", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid line format: %s", line)
	}

	key := strings.TrimSpace(parts[0])
	raw := parts[1]
	value := strings.TrimSpace(raw)

	if key == "" {
		return "", "", fmt.Errorf("empty key")
	}

	if len(value) >= 2 {
		switch value[0] {
		case '\'':
			if end := strings.IndexByte(value[1:], '\'') + 1; end > 0 && isEnvComment(value[end+1:]) {
				return key, value[1:end], nil
			}
			if value[len(value)-1] == '\'' {
				return key, value[1 : len(value)-1], nil
			}
		case '"':
			if end := closingDoubleQuote(value); end > 0 && isEnvComment(value[end+1:]) {
				return key, e.expandValue(value[1:end], true), nil
			}
			if value[len(value)-1] == '"' {
				return key, e.expandValue(value[1:len(value)-1], true), nil
			}
		}
	}
	return key, e.expandValue(strings.TrimSpace(stripEnvComment(raw)), false), nil
}

// isEnvComment reports whether rest, the text after a closing quote, is empty
// or a comment.
func isEnvComment(rest string) bool {
	rest = strings.TrimSpace(rest)
	return rest == "" || rest[0] == '#'
}

// closingDoubleQuote returns the index of the quote that closes the
// double-quoted value starting at s[0], skipping backslash escapes, or -1.
func closingDoubleQuote(s string) int {
	for i := 1; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '"':
			return i
		}
	}
	return -1
}

// stripEnvComment cuts an unquoted value at the first # that follows a space
// or tab.
func stripEnvComment(raw string) string {
	for i := 1; i < len(raw); i++ {
		if raw[i] == '#' && (raw[i-1] == ' ' || raw[i-1] == '\t') {
			return raw[:i]
		}
	}
	return raw
}

// expandValue expands variable references in s in a single pass. When
// quoted is true it also decodes double-quoted escape sequences. The caller
// holds e.mutex.
func (e *Env) expandValue(s string, quoted bool) string {
	var b strings.Builder
	b.Grow(len(s))
	wordStart := true
	for i := 0; i < len(s); {
		c := s[i]
		if quoted && c == '\\' && i+1 < len(s) {
			switch next := s[i+1]; next {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'r':
				b.WriteByte('\r')
			case '"', '\\', '$':
				b.WriteByte(next)
			default:
				b.WriteByte('\\')
				b.WriteByte(next)
			}
			i += 2
			wordStart = false
			continue
		}
		if c == '$' && strings.HasPrefix(s[i:], "${") {
			if end := strings.IndexByte(s[i+2:], '}'); end >= 0 {
				value, _ := e.lookupLocked(s[i+2 : i+2+end])
				b.WriteString(value)
				i += end + 3
				wordStart = false
				continue
			}
		}
		if c == '$' && wordStart {
			if n := envNameLen(s[i+1:]); n > 0 {
				value, _ := e.lookupLocked(s[i+1 : i+1+n])
				b.WriteString(value)
				i += n + 1
				wordStart = false
				continue
			}
		}
		b.WriteByte(c)
		wordStart = c == ' ' || c == '\t'
		i++
	}
	return b.String()
}

// envNameLen returns the length of the variable name at the start of s:
// a letter or underscore followed by letters, digits, or underscores.
func envNameLen(s string) int {
	for i := 0; i < len(s); i++ {
		c := s[i]
		isLetter := c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
		if isLetter || (i > 0 && c >= '0' && c <= '9') {
			continue
		}
		return i
	}
	return len(s)
}

// Get returns the value for key: a value set in code, else the process
// environment, else a .env value (see Env for OverrideSystemEnv). It returns
// "" when no source has key.
func (e *Env) Get(key string) string {
	value, _ := e.lookup(key)
	return value
}

// lookup applies Env's precedence and reports whether any source had key.
func (e *Env) lookup(key string) (string, bool) {
	e.mutex.RLock()
	defer e.mutex.RUnlock()
	return e.lookupLocked(key)
}

// lookupLocked is lookup for callers that already hold e.mutex.
func (e *Env) lookupLocked(key string) (string, bool) {
	if e.explicit[key] {
		return e.vars[key], true
	}
	if !e.override {
		if value, ok := os.LookupEnv(key); ok {
			return value, true
		}
	}
	if value, ok := e.vars[key]; ok {
		return value, true
	}
	return os.LookupEnv(key)
}

// GetOr returns the value for key, or defaultValue when it is unset or empty.
func (e *Env) GetOr(key, defaultValue string) string {
	value := e.Get(key)
	if value == "" {
		return defaultValue
	}
	return value
}

// GetInt parses the value for key as an int. It returns an error when the
// value is unset, empty, or not an integer.
func (e *Env) GetInt(key string) (int, error) {
	value := e.Get(key)
	if value == "" {
		return 0, fmt.Errorf("environment variable %s not found", key)
	}
	return strconv.Atoi(value)
}

// GetIntOr returns the value for key as an int, or defaultValue when it is
// unset, empty, or not an integer.
func (e *Env) GetIntOr(key string, defaultValue int) int {
	value, err := e.GetInt(key)
	if err != nil {
		return defaultValue
	}
	return value
}

// GetBool reports whether the value for key is "true", "1", "yes", or "on",
// ignoring case. Any other value, or none, is false.
func (e *Env) GetBool(key string) bool {
	value := strings.ToLower(e.Get(key))
	return value == "true" || value == "1" || value == "yes" || value == "on"
}

// GetBoolOr returns defaultValue when key is unset or empty, and GetBool
// otherwise.
func (e *Env) GetBoolOr(key string, defaultValue bool) bool {
	value := e.Get(key)
	if value == "" {
		return defaultValue
	}
	return e.GetBool(key)
}

// GetFloat parses the value for key as a float64. It returns an error when
// the value is unset, empty, or not a number.
func (e *Env) GetFloat(key string) (float64, error) {
	value := e.Get(key)
	if value == "" {
		return 0, fmt.Errorf("environment variable %s not found", key)
	}
	return strconv.ParseFloat(value, 64)
}

// GetFloatOr returns the value for key as a float64, or defaultValue when it
// is unset, empty, or not a number.
func (e *Env) GetFloatOr(key string, defaultValue float64) float64 {
	value, err := e.GetFloat(key)
	if err != nil {
		return defaultValue
	}
	return value
}

// GetSlice splits the value for key on commas and trims each part. It
// returns an empty slice when the value is unset or empty.
func (e *Env) GetSlice(key string) []string {
	value := e.Get(key)
	if value == "" {
		return []string{}
	}

	parts := strings.Split(value, ",")
	result := make([]string, len(parts))
	for i, part := range parts {
		result[i] = strings.TrimSpace(part)
	}
	return result
}

// Set sets a value in code (in memory only). It wins over both the process
// environment and .env files.
func (e *Env) Set(key, value string) {
	e.mutex.Lock()
	defer e.mutex.Unlock()
	e.vars[key] = value
	e.explicit[key] = true
}

// SetSystem calls Set and also sets key in the process environment.
func (e *Env) SetSystem(key, value string) error {
	e.Set(key, value)
	return os.Setenv(key, value)
}

// Has reports whether any source has key, even with an empty value.
func (e *Env) Has(key string) bool {
	_, ok := e.lookup(key)
	return ok
}

// All returns every variable from every source, merged with the same
// precedence as Get.
func (e *Env) All() map[string]string {
	e.mutex.RLock()
	defer e.mutex.RUnlock()

	result := make(map[string]string)

	for _, env := range os.Environ() {
		parts := strings.SplitN(env, "=", 2)
		if len(parts) == 2 {
			result[parts[0]] = parts[1]
		}
	}

	// Layer .env and Set values with the same precedence as Get.
	for key, value := range e.vars {
		if _, inSystem := result[key]; inSystem && !e.override && !e.explicit[key] {
			continue
		}
		result[key] = value
	}

	return result
}

// Keys returns the keys of All in no particular order.
func (e *Env) Keys() []string {
	all := e.All()
	keys := make([]string, 0, len(all))
	for key := range all {
		keys = append(keys, key)
	}
	return keys
}

// LoadedFiles returns a copy of the names of the files loaded so far, in
// load order.
func (e *Env) LoadedFiles() []string {
	e.mutex.RLock()
	defer e.mutex.RUnlock()

	result := make([]string, len(e.loaded))
	copy(result, e.loaded)
	return result
}

// Dump returns a copy of the values loaded from .env files and set with Set.
// It excludes the process environment.
func (e *Env) Dump() map[string]string {
	e.mutex.RLock()
	defer e.mutex.RUnlock()

	result := make(map[string]string)
	for key, value := range e.vars {
		result[key] = value
	}
	return result
}

// Save writes the current .env variables to filename, sorted by key. Values
// that need it are double-quoted with escapes, so LoadFile reads back exactly
// what Dump returns. The file is written with mode 0600 because .env files
// hold secrets.
func (e *Env) Save(filename string) error {
	e.mutex.RLock()
	vars := make(map[string]string, len(e.vars))
	for key, value := range e.vars {
		vars[key] = value
	}
	e.mutex.RUnlock()

	keys := make([]string, 0, len(vars))
	for key := range vars {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var content strings.Builder
	for _, key := range keys {
		content.WriteString(key)
		content.WriteByte('=')
		content.WriteString(quoteEnvValue(vars[key]))
		content.WriteByte('\n')
	}

	return writeSecretFile(filename, content.String())
}

// envValueEscaper encodes a value for a double-quoted .env entry. It is
// single-pass, so an escaped backslash is never re-read as an escape.
var envValueEscaper = strings.NewReplacer(
	`\`, `\\`, `"`, `\"`, "$", `\$`, "\n", `\n`, "\r", `\r`, "\t", `\t`,
)

// quoteEnvValue returns value as it should appear after KEY= in a .env file.
func quoteEnvValue(value string) string {
	if !strings.ContainsAny(value, " \t\n\r\"'\\$") {
		return value
	}
	return `"` + envValueEscaper.Replace(value) + `"`
}

// Clear forgets every .env and Set value and the list of loaded files, so
// files can be loaded again. The process environment is untouched.
func (e *Env) Clear() {
	e.mutex.Lock()
	defer e.mutex.Unlock()

	e.vars = make(map[string]string)
	e.explicit = make(map[string]bool)
	e.loaded = make([]string, 0)
}

// Validate returns an error listing every key in required that no source
// has.
func (e *Env) Validate(required []string) error {
	missing := make([]string, 0)

	for _, key := range required {
		if !e.Has(key) {
			missing = append(missing, key)
		}
	}

	if len(missing) > 0 {
		return fmt.Errorf("missing required environment variables: %v", missing)
	}

	return nil
}

// defaultEnv backs the package-level env functions.
var defaultEnv = NewEnv()

// LoadEnv loads the named .env files into the package-level Env, or the
// LoadDefault files when no names are given.
func LoadEnv(filenames ...string) error {
	if len(filenames) == 0 {
		return defaultEnv.LoadDefault()
	}
	return defaultEnv.LoadFiles(filenames...)
}

// GetEnv returns the package-level Env's value for key (see Env.Get).
func GetEnv(key string) string {
	return defaultEnv.Get(key)
}

// GetEnvOr returns the value for key, or defaultValue when it is unset or
// empty.
func GetEnvOr(key, defaultValue string) string {
	return defaultEnv.GetOr(key, defaultValue)
}

// GetEnvInt parses the value for key as an int. It returns an error when the
// value is unset, empty, or not an integer.
func GetEnvInt(key string) (int, error) {
	return defaultEnv.GetInt(key)
}

// GetEnvIntOr returns the value for key as an int, or defaultValue when it is
// unset, empty, or not an integer.
func GetEnvIntOr(key string, defaultValue int) int {
	return defaultEnv.GetIntOr(key, defaultValue)
}

// GetEnvBool reports whether the value for key is "true", "1", "yes", or
// "on", ignoring case.
func GetEnvBool(key string) bool {
	return defaultEnv.GetBool(key)
}

// GetEnvBoolOr returns defaultValue when key is unset or empty, and
// GetEnvBool otherwise.
func GetEnvBoolOr(key string, defaultValue bool) bool {
	return defaultEnv.GetBoolOr(key, defaultValue)
}

// SetEnv sets key on the package-level Env and in the process environment.
func SetEnv(key, value string) error {
	return defaultEnv.SetSystem(key, value)
}

// HasEnv reports whether any source has key, even with an empty value.
func HasEnv(key string) bool {
	return defaultEnv.Has(key)
}

// RequireEnv returns an error listing every key that no source has.
func RequireEnv(keys ...string) error {
	return defaultEnv.Validate(keys)
}
