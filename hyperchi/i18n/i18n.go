// Package i18n provides explicit locale resolution, localized paths, and plain
// text catalogs. Construct a Bundle at startup and share it across requests.
package i18n

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Config defines supported locales. Default must occur in Locales. Catalogs
// contain plain text keyed by message ID. Negotiate enables Accept-Language
// on paths without a supported locale prefix. All generated paths are prefixed.
type Config struct {
	Default   string
	Locales   []string
	Catalogs  map[string]map[string]string
	Negotiate bool
}

// Bundle is immutable and safe for concurrent use. Use New to construct it.
type Bundle struct {
	fallback  string
	locales   []string
	catalogs  map[string]map[string]string
	negotiate bool
}

var localePattern = regexp.MustCompile(`^[a-zA-Z]{2,8}(-[a-zA-Z0-9]{1,8})*$`)
var qualityPattern = regexp.MustCompile(`^[01](\.[0-9]{0,3})?$`)

// NormalizeLocale validates basic language-tag syntax and returns lower case.
// It does not validate tags against the IANA registry or resolve aliases.
func NormalizeLocale(locale string) (string, error) {
	if len(locale) > 128 || !localePattern.MatchString(locale) {
		return "", fmt.Errorf("i18n: invalid locale %q", locale)
	}
	return strings.ToLower(locale), nil
}

// New validates configuration and copies all caller-owned maps and slices.
func New(cfg Config) (*Bundle, error) {
	fallback, err := NormalizeLocale(cfg.Default)
	if err != nil {
		return nil, err
	}
	b := &Bundle{fallback: fallback, catalogs: map[string]map[string]string{}, negotiate: cfg.Negotiate}
	for _, raw := range cfg.Locales {
		locale, err := NormalizeLocale(raw)
		if err != nil {
			return nil, err
		}
		if slices.Contains(b.locales, locale) {
			return nil, fmt.Errorf("i18n: duplicate locale %q", locale)
		}
		b.locales = append(b.locales, locale)
	}
	if !slices.Contains(b.locales, fallback) {
		return nil, fmt.Errorf("i18n: default must be a supported locale")
	}
	for raw, messages := range cfg.Catalogs {
		locale, err := NormalizeLocale(raw)
		if err != nil {
			return nil, err
		}
		if !slices.Contains(b.locales, locale) {
			return nil, fmt.Errorf("i18n: unsupported catalog %q", raw)
		}
		if _, exists := b.catalogs[locale]; exists {
			return nil, fmt.Errorf("i18n: duplicate catalog %q", raw)
		}
		b.catalogs[locale] = maps.Clone(messages)
	}
	return b, nil
}

// Locales returns a copy in configured preference order.
func (b *Bundle) Locales() []string { return slices.Clone(b.locales) }

// Match tries an exact locale and successively shorter parent tags, then Default.
// It never guesses a sibling region. Invalid tags also return Default.
func (b *Bundle) Match(locale string) string {
	locale, err := NormalizeLocale(locale)
	if err == nil {
		for locale != "" {
			if slices.Contains(b.locales, locale) {
				return locale
			}
			locale = parent(locale)
		}
	}
	return b.fallback
}

func parent(locale string) string {
	i := strings.LastIndexByte(locale, '-')
	if i < 0 {
		return ""
	}
	locale = locale[:i]
	// Remove a dangling extension singleton along with the last subtag.
	if i = strings.LastIndexByte(locale, '-'); i >= 0 && len(locale)-i == 2 {
		locale = locale[:i]
	}
	return locale
}

// Text looks up a message in the requested locale, supported parent locales,
// and Default, in that order. A missing message returns its key. Empty messages
// are intentional translations. Values remain ordinary strings for HTML escaping.
func (b *Bundle) Text(locale, key string) string {
	locale = b.Match(locale)
	for candidate := locale; candidate != ""; candidate = parent(candidate) {
		if value, ok := b.catalogs[candidate][key]; ok {
			return value
		}
	}
	if value, ok := b.catalogs[b.fallback][key]; ok {
		return value
	}
	return key
}

func (b *Bundle) prefix(path string) string {
	first, _, _ := strings.Cut(strings.TrimPrefix(path, "/"), "/")
	first = strings.ToLower(first)
	if slices.Contains(b.locales, first) {
		return first
	}
	return ""
}

// Path adds or replaces a supported locale prefix on a root-relative URL.
// Query strings, fragments, trailing slashes, and escaped path bytes survive.
// External URLs, backslashes, encoded slashes, and dot segments are rejected.
// Unsupported locale arguments return errors rather than silently changing URLs.
func (b *Bundle) Path(locale, raw string) (string, error) {
	locale = strings.ToLower(locale)
	if !slices.Contains(b.locales, locale) {
		return "", fmt.Errorf("i18n: unsupported locale %q", locale)
	}
	u, err := url.Parse(raw)
	if err != nil || !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") ||
		u.IsAbs() || u.Host != "" || strings.ContainsAny(raw, "\\\r\n") {
		return "", fmt.Errorf("i18n: expected a root-relative URL")
	}
	escaped := u.EscapedPath()
	if strings.Contains(strings.ToLower(escaped), "%2f") || strings.Contains(u.Path, "\\") {
		return "", fmt.Errorf("i18n: encoded separators are unsupported")
	}
	for _, part := range strings.Split(u.Path, "/") {
		if part == "." || part == ".." {
			return "", fmt.Errorf("i18n: dot segments are unsupported")
		}
	}
	if b.prefix(u.Path) != "" {
		_, rest, found := strings.Cut(strings.TrimPrefix(escaped, "/"), "/")
		escaped = ""
		if found {
			escaped = "/" + rest
		}
	}
	escaped = "/" + locale + escaped
	u.Path, _ = url.PathUnescape(escaped)
	u.RawPath = escaped
	return u.String(), nil
}

type preference struct {
	locale  string
	quality float64
}

// Resolve chooses a supported path prefix, optional weighted Accept-Language,
// then Default. Malformed preferences and q=0 entries are ignored. Wildcards
// select Default; equal weights retain header order. Matching uses exact tags
// and their parents, not likely-subtag inference. Requests are never rewritten.
func (b *Bundle) Resolve(r *http.Request) string {
	if locale := b.prefix(r.URL.Path); locale != "" {
		return locale
	}
	if !b.negotiate {
		return b.fallback
	}
	var preferences []preference
	for _, part := range strings.Split(r.Header.Get("Accept-Language"), ",") {
		pieces := strings.Split(strings.TrimSpace(part), ";")
		locale := strings.TrimSpace(pieces[0])
		quality := 1.0
		if len(pieces) > 2 {
			continue
		}
		if len(pieces) == 2 {
			param := strings.TrimSpace(pieces[1])
			if !strings.HasPrefix(param, "q=") {
				continue
			}
			if !qualityPattern.MatchString(strings.TrimPrefix(param, "q=")) {
				continue
			}
			var err error
			quality, err = strconv.ParseFloat(strings.TrimPrefix(param, "q="), 64)
			if err != nil || !(quality > 0 && quality <= 1) {
				continue
			}
		}
		if locale != "*" {
			var err error
			locale, err = NormalizeLocale(locale)
			if err != nil {
				continue
			}
		}
		preferences = append(preferences, preference{locale: locale, quality: quality})
	}
	slices.SortStableFunc(preferences, func(a, b preference) int {
		if a.quality > b.quality {
			return -1
		}
		if a.quality < b.quality {
			return 1
		}
		return 0
	})
	for _, p := range preferences {
		if p.locale == "*" {
			return b.fallback
		}
		for locale := p.locale; locale != ""; locale = parent(locale) {
			if slices.Contains(b.locales, locale) {
				return locale
			}
		}
	}
	return b.fallback
}

type localeKey struct{}

// Locale returns the locale installed by Middleware, or empty outside it.
func Locale(ctx context.Context) string { locale, _ := ctx.Value(localeKey{}).(string); return locale }

// Middleware sets the request locale and Content-Language. Negotiated URLs
// add Vary: Accept-Language. Register routes explicitly; no redirects, cookies,
// route rewriting, or automatic route discovery occur. Handlers must preserve
// Vary if they replace that header and may override Content-Language.
func (b *Bundle) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		locale := b.Resolve(r)
		w.Header().Set("Content-Language", locale)
		if b.negotiate && b.prefix(r.URL.Path) == "" {
			w.Header().Add("Vary", "Accept-Language")
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), localeKey{}, locale)))
	})
}
