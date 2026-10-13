// Package publishing generates immutable sitemap, RSS 2.0, and Atom 1.0
// documents from explicitly selected public content. It never discovers routes
// or reads the request Host header to construct canonical URLs.
package publishing

import (
	"bytes"
	"crypto/sha256"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/regiellis/hyperchi/hyperchi/i18n"
)

// Document is a generated XML document. Its zero value is not usable.
// Generate a new document and replace its handler when content changes.
type Document struct {
	body      []byte
	mediaType string
}

// Bytes returns an independent copy suitable for writing to a static build.
func (d Document) Bytes() []byte { return bytes.Clone(d.body) }

// Handler serves an immutable snapshot with GET, HEAD, ETag validation, and
// Cache-Control: no-cache. It is safe for concurrent use.
func (d Document) Handler() http.Handler {
	tag := fmt.Sprintf(`"%x"`, sha256.Sum256(d.body))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(d.body) == 0 {
			http.Error(w, "document not generated", http.StatusInternalServerError)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", d.mediaType+"; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("ETag", tag)
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(d.body))
	})
}

// Alternate identifies a translated page, including the page itself. Language
// may be x-default. Callers supply reciprocal sets for each translated page.
type Alternate struct {
	Language string
	URL      string
}

type sitemapAlternate struct {
	Rel      string `xml:"rel,attr"`
	Language string `xml:"hreflang,attr"`
	URL      string `xml:"href,attr"`
}

// SitemapEntry identifies a public canonical page. Zero LastModified omits
// lastmod; use the page's modification date, not the build time. Alternates
// lists all translated versions, including this page, when available.
type SitemapEntry struct {
	URL          string
	LastModified time.Time
	Alternates   []Alternate
}

type sitemapURL struct {
	Location     string             `xml:"loc"`
	LastModified string             `xml:"lastmod,omitempty"`
	Alternates   []sitemapAlternate `xml:"xhtml:link"`
}

// Sitemap generates a single sitemap, sorted by URL without mutating entries.
// URLs must be unique, absolute HTTP(S), under 2048 bytes, and share an origin.
// The protocol limits of 50,000 entries and 50 MiB are enforced. Index files
// and automatic splitting are not provided.
func Sitemap(entries []SitemapEntry) (Document, error) {
	if len(entries) == 0 || len(entries) > 50_000 {
		return Document{}, fmt.Errorf("sitemap requires 1 to 50000 entries")
	}
	sorted := slices.Clone(entries)
	slices.SortFunc(sorted, func(a, b SitemapEntry) int { return strings.Compare(a.URL, b.URL) })
	root := struct {
		XMLName   xml.Name     `xml:"urlset"`
		Namespace string       `xml:"xmlns,attr"`
		XHTML     string       `xml:"xmlns:xhtml,attr,omitempty"`
		URLs      []sitemapURL `xml:"url"`
	}{Namespace: "http://www.sitemaps.org/schemas/sitemap/0.9"}
	origin := ""
	previous := ""
	for _, entry := range sorted {
		u, err := publicURL(entry.URL)
		if err != nil {
			return Document{}, err
		}
		current := strings.ToLower(u.Scheme + "://" + u.Host)
		if origin != "" && origin != current {
			return Document{}, fmt.Errorf("sitemap URLs must share an origin")
		}
		if len(entry.URL) >= 2048 || entry.URL == previous {
			return Document{}, fmt.Errorf("duplicate or oversized sitemap URL %q", entry.URL)
		}
		if err := validDate(entry.LastModified); err != nil {
			return Document{}, err
		}
		origin, previous = current, entry.URL
		alternates := slices.Clone(entry.Alternates)
		slices.SortFunc(alternates, func(a, b Alternate) int { return strings.Compare(a.Language, b.Language) })
		page := sitemapURL{Location: entry.URL, LastModified: date(entry.LastModified, time.RFC3339)}
		seen := map[string]bool{}
		for _, alternate := range alternates {
			locale := strings.ToLower(alternate.Language)
			if locale != "x-default" {
				if _, err := i18n.NormalizeLocale(locale); err != nil {
					return Document{}, err
				}
			}
			if seen[locale] {
				return Document{}, fmt.Errorf("duplicate alternate language %q", locale)
			}
			seen[locale] = true
			if _, err := publicURL(alternate.URL); err != nil {
				return Document{}, err
			}
			if len(alternate.URL) >= 2048 {
				return Document{}, fmt.Errorf("oversized alternate URL")
			}
			root.XHTML = "http://www.w3.org/1999/xhtml"
			page.Alternates = append(page.Alternates, sitemapAlternate{Rel: "alternate", Language: locale, URL: alternate.URL})
		}
		root.URLs = append(root.URLs, page)
	}
	d, err := document(root, "application/xml")
	if err == nil && len(d.body) > 50<<20 {
		return Document{}, fmt.Errorf("sitemap exceeds 50 MiB")
	}
	return d, err
}

// Feed is a public feed shared by RSS and Atom. URL is the website; FeedURL is
// the canonical URL of the generated feed (use a separate value per format).
// Title, Description, Author, and Updated are required. Text is plain text.
// Entry order is preserved; callers choose ordering and exclude drafts.
type Feed struct {
	// Language is optional and applies to the feed and its entries.
	Language    string
	Title       string
	Description string
	URL         string
	FeedURL     string
	Author      string
	Updated     time.Time
	Entries     []Entry
}

// Entry uses its canonical URL as its stable RSS GUID and Atom ID. Title and
// Updated are required. Published is optional; Summary is plain text, not HTML.
type Entry struct {
	Title     string
	URL       string
	Summary   string
	Updated   time.Time
	Published time.Time
}

func (f Feed) validate() error {
	if f.Language != "" {
		if _, err := i18n.NormalizeLocale(f.Language); err != nil {
			return err
		}
	}
	if strings.TrimSpace(f.Title) == "" || strings.TrimSpace(f.Description) == "" || strings.TrimSpace(f.Author) == "" || f.Updated.IsZero() {
		return fmt.Errorf("feed requires title, description, author, and updated")
	}
	for _, u := range []string{f.URL, f.FeedURL} {
		if _, err := publicURL(u); err != nil {
			return err
		}
	}
	for _, s := range []string{f.Title, f.Description, f.Author} {
		if err := validText(s); err != nil {
			return err
		}
	}
	if err := validDate(f.Updated); err != nil {
		return err
	}
	seen := make(map[string]bool, len(f.Entries))
	for i, e := range f.Entries {
		if strings.TrimSpace(e.Title) == "" || e.Updated.IsZero() {
			return fmt.Errorf("entry %d requires title and updated", i)
		}
		if _, err := publicURL(e.URL); err != nil {
			return err
		}
		if seen[e.URL] {
			return fmt.Errorf("duplicate entry URL %q", e.URL)
		}
		seen[e.URL] = true
		for _, s := range []string{e.Title, e.Summary} {
			if err := validText(s); err != nil {
				return err
			}
		}
		for _, t := range []time.Time{e.Updated, e.Published} {
			if err := validDate(t); err != nil {
				return err
			}
		}
		if e.Updated.After(f.Updated) {
			return fmt.Errorf("entry %d updated after feed", i)
		}
		if !e.Published.IsZero() && e.Published.After(e.Updated) {
			return fmt.Errorf("entry %d published after updated", i)
		}
	}
	return nil
}

func publicURL(s string) (*url.URL, error) {
	u, err := url.Parse(s)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Fragment != "" || strings.ContainsAny(s, "#\\") || strings.ContainsFunc(s, unicode.IsSpace) {
		return nil, fmt.Errorf("invalid public HTTP(S) URL %q", s)
	}
	if err := validText(s); err != nil {
		return nil, err
	}
	return u, nil
}

func validText(s string) error {
	if !utf8.ValidString(s) {
		return fmt.Errorf("text must be UTF-8")
	}
	for _, r := range s {
		if !isXMLChar(r) {
			return fmt.Errorf("text contains invalid XML character")
		}
	}
	return nil
}

// isXMLChar reports whether r is in the XML 1.0 Char production.
func isXMLChar(r rune) bool {
	return r == 9 || r == 10 || r == 13 || r >= 32 && r <= 0xD7FF || r >= 0xE000 && r <= 0xFFFD || r >= 0x10000 && r <= 0x10FFFF
}
func validDate(t time.Time) error {
	if !t.IsZero() && (t.UTC().Year() < 1 || t.UTC().Year() > 9999) {
		return fmt.Errorf("date year must be between 1 and 9999")
	}
	return nil
}
func date(t time.Time, layout string) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(layout)
}
func document(v any, media string) (Document, error) {
	b, err := xml.MarshalIndent(v, "", "  ")
	if err != nil {
		return Document{}, err
	}
	return Document{body: append([]byte(xml.Header), b...), mediaType: media}, nil
}
