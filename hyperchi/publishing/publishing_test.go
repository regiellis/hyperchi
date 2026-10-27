package publishing_test

import (
	"bytes"
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/regiellis/hyperchi/hyperchi/publishing"
)

func sampleFeed() publishing.Feed {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	return publishing.Feed{Title: "News & updates", Description: "<Plain text>", URL: "https://example.com/", FeedURL: "https://example.com/feed.xml", Author: "Team", Updated: now, Entries: []publishing.Entry{{Title: "One & two", URL: "https://example.com/posts/one?a=1&b=2", Summary: "<script>alert(1)</script>", Updated: now, Published: now.Add(-time.Hour)}}}
}

func TestSitemap(t *testing.T) {
	t.Parallel()
	entries := []publishing.SitemapEntry{{URL: "https://example.com/z?x=1&y=2"}, {URL: "https://example.com/a", LastModified: sampleFeed().Updated}}
	d, err := publishing.Sitemap(entries)
	if err != nil {
		t.Fatal(err)
	}
	var root struct {
		XMLName xml.Name
		URLs    []struct {
			Loc     string `xml:"loc"`
			LastMod string `xml:"lastmod"`
		} `xml:"url"`
	}
	if err := xml.Unmarshal(d.Bytes(), &root); err != nil {
		t.Fatal(err)
	}
	if root.XMLName.Space != "http://www.sitemaps.org/schemas/sitemap/0.9" || len(root.URLs) != 2 || root.URLs[0].Loc != entries[1].URL || root.URLs[1].Loc != entries[0].URL || root.URLs[1].LastMod != "" {
		t.Fatalf("sitemap: %+v", root)
	}
	if entries[0].URL != "https://example.com/z?x=1&y=2" {
		t.Fatal("mutated input")
	}
	copy := d.Bytes()
	copy[0] = 'X'
	if bytes.Equal(copy, d.Bytes()) {
		t.Fatal("Bytes exposes internal storage")
	}
	second, err := publishing.Sitemap(entries)
	if err != nil || !bytes.Equal(d.Bytes(), second.Bytes()) {
		t.Fatal("nondeterministic output", err)
	}
}

func TestSitemapValidation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		entries []publishing.SitemapEntry
	}{
		{"empty", nil},
		{"relative", []publishing.SitemapEntry{{URL: "/a"}}},
		{"fragment", []publishing.SitemapEntry{{URL: "https://example.com/a#secret"}}},
		{"credentials", []publishing.SitemapEntry{{URL: "https://user:pass@example.com/"}}},
		{"duplicate", []publishing.SitemapEntry{{URL: "https://example.com/a"}, {URL: "https://example.com/a"}}},
		{"mixed origin", []publishing.SitemapEntry{{URL: "https://example.com/a"}, {URL: "https://other.com/a"}}},
		{"oversized URL", []publishing.SitemapEntry{{URL: "https://example.com/" + strings.Repeat("x", 2048)}}},
		{"too many", make([]publishing.SitemapEntry, 50001)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := publishing.Sitemap(tc.entries); err == nil {
				t.Fatal("accepted invalid sitemap")
			}
		})
	}
}

func TestFeeds(t *testing.T) {
	t.Parallel()
	f := sampleFeed()
	rss, err := f.RSS()
	if err != nil {
		t.Fatal(err)
	}
	var r struct {
		XMLName xml.Name
		Version string `xml:"version,attr"`
		Channel struct {
			Title string
			Link  string
			Items []struct {
				GUID        string `xml:"guid"`
				Description string `xml:"description"`
				Published   string `xml:"pubDate"`
			} `xml:"item"`
		} `xml:"channel"`
	}
	if err := xml.Unmarshal(rss.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	if r.XMLName.Local != "rss" || r.Version != "2.0" || len(r.Channel.Items) != 1 || r.Channel.Items[0].GUID != f.Entries[0].URL || r.Channel.Items[0].Description != f.Entries[0].Summary {
		t.Fatalf("RSS: %+v", r)
	}
	if _, err := time.Parse(time.RFC1123Z, r.Channel.Items[0].Published); err != nil {
		t.Fatal(err)
	}
	atom, err := f.Atom()
	if err != nil {
		t.Fatal(err)
	}
	var a struct {
		XMLName xml.Name
		Title   string `xml:"title"`
		Author  struct {
			Name string `xml:"name"`
		} `xml:"author"`
		Links []struct {
			Rel  string `xml:"rel,attr"`
			Href string `xml:"href,attr"`
		} `xml:"link"`
		Entries []struct {
			ID      string `xml:"id"`
			Summary string `xml:"summary"`
			Updated string `xml:"updated"`
		} `xml:"entry"`
	}
	if err := xml.Unmarshal(atom.Bytes(), &a); err != nil {
		t.Fatal(err)
	}
	if a.XMLName.Space != "http://www.w3.org/2005/Atom" || a.Title != f.Title || a.Author.Name != f.Author || len(a.Entries) != 1 || a.Entries[0].Summary != f.Entries[0].Summary || a.Entries[0].ID != f.Entries[0].URL {
		t.Fatalf("Atom: %+v", a)
	}
	if len(a.Links) != 2 || a.Links[1].Rel != "self" || a.Links[1].Href != f.FeedURL {
		t.Fatal("missing self link")
	}
	if _, err := time.Parse(time.RFC3339, a.Entries[0].Updated); err != nil {
		t.Fatal(err)
	}
	for _, d := range []publishing.Document{rss, atom} {
		if bytes.Contains(d.Bytes(), []byte("<script>")) {
			t.Fatal("unescaped markup")
		}
	}
}

func TestFeedValidation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		edit func(*publishing.Feed)
	}{
		{"missing author", func(f *publishing.Feed) { f.Author = "" }},
		{"missing title", func(f *publishing.Feed) { f.Title = " " }},
		{"invalid XML", func(f *publishing.Feed) { f.Description = "bad\x00text" }},
		{"invalid UTF8", func(f *publishing.Feed) { f.Title = string([]byte{255}) }},
		{"missing updated", func(f *publishing.Feed) { f.Updated = time.Time{} }},
		{"duplicate entry", func(f *publishing.Feed) { f.Entries = append(f.Entries, f.Entries[0]) }},
		{"invalid URL", func(f *publishing.Feed) { f.Entries[0].URL = "javascript:alert(1)" }},
		{"future entry", func(f *publishing.Feed) { f.Entries[0].Updated = f.Updated.Add(time.Hour) }},
		{"invalid date", func(f *publishing.Feed) { f.Updated = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := sampleFeed()
			tc.edit(&f)
			if _, err := f.RSS(); err == nil {
				t.Error("RSS accepted invalid feed")
			}
			if _, err := f.Atom(); err == nil {
				t.Error("Atom accepted invalid feed")
			}
		})
	}
}

func TestDocumentHandler(t *testing.T) {
	t.Parallel()
	d, err := sampleFeed().Atom()
	if err != nil {
		t.Fatal(err)
	}
	handler := d.Handler()
	get := httptest.NewRecorder()
	handler.ServeHTTP(get, httptest.NewRequest("GET", "/feed.xml", nil))
	if get.Code != 200 || !bytes.Equal(get.Body.Bytes(), d.Bytes()) || get.Header().Get("Content-Type") != "application/atom+xml; charset=utf-8" {
		t.Fatalf("GET: %+v", get)
	}
	for _, tc := range []struct {
		name, method, tag string
		status            int
	}{
		{"HEAD", "HEAD", "", 200}, {"conditional GET", "GET", get.Header().Get("ETag"), 304}, {"conditional HEAD", "HEAD", get.Header().Get("ETag"), 304}, {"POST", "POST", "", 405},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, "/feed.xml", nil)
			r.Header.Set("If-None-Match", tc.tag)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("status %d", w.Code)
			}
			if tc.status != http.StatusMethodNotAllowed && w.Body.Len() != 0 {
				t.Fatal("unexpected body")
			}
		})
	}
}
