package publishing

import (
	"bytes"
	"encoding/xml"
	"strings"
	"testing"
	"time"
)

func TestLocalizedSitemap(t *testing.T) {
	t.Parallel()
	alternates := []Alternate{{Language: "fr", URL: "https://example.fr/news?a=1&b=2"},
		{Language: "en", URL: "https://example.com/news"}, {Language: "x-default", URL: "https://example.com/"}}
	doc, err := Sitemap([]SitemapEntry{{URL: "https://example.com/news", Alternates: alternates}})
	if err != nil {
		t.Fatal(err)
	}
	var root struct {
		Pages []struct {
			Links []struct {
				Language string `xml:"hreflang,attr"`
				Href     string `xml:"href,attr"`
			} `xml:"http://www.w3.org/1999/xhtml link"`
		} `xml:"url"`
	}
	if err := xml.Unmarshal(doc.Bytes(), &root); err != nil {
		t.Fatal(err)
	}
	if len(root.Pages) != 1 || len(root.Pages[0].Links) != 3 {
		t.Fatalf("invalid alternate namespace: %s", doc.Bytes())
	}
	if root.Pages[0].Links[1].Href != alternates[0].URL {
		t.Fatal("URL escaping failed")
	}
	if alternates[0].Language != "fr" {
		t.Fatal("caller input mutated")
	}
	again, err := Sitemap([]SitemapEntry{{URL: "https://example.com/news", Alternates: []Alternate{alternates[2], alternates[1], alternates[0]}}})
	if err != nil || !bytes.Equal(doc.Bytes(), again.Bytes()) {
		t.Fatal("unstable output")
	}
	for _, tt := range []struct {
		name string
		alts []Alternate
	}{
		{"duplicate", []Alternate{{Language: "fr", URL: "https://example.com/fr"}, {Language: "FR", URL: "https://example.com/fr"}}},
		{"language", []Alternate{{Language: "en_US", URL: "https://example.com/"}}},
		{"external scheme", []Alternate{{Language: "en", URL: "javascript:alert(1)"}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Sitemap([]SitemapEntry{{URL: "https://example.com/", Alternates: tt.alts}}); err == nil {
				t.Fatal("accepted invalid alternates")
			}
		})
	}
}

func TestFeedLanguage(t *testing.T) {
	t.Parallel()
	feed := Feed{Title: "News", Description: "Updates", URL: "https://example.com/fr", FeedURL: "https://example.com/fr/feed",
		Author: "Team", Updated: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Language: "fr"}
	rss, err := feed.RSS()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rss.Bytes()), "<language>fr</language>") {
		t.Fatal("RSS language missing")
	}
	atom, err := feed.Atom()
	if err != nil {
		t.Fatal(err)
	}
	var root struct {
		Language string `xml:"http://www.w3.org/XML/1998/namespace lang,attr"`
	}
	if err := xml.Unmarshal(atom.Bytes(), &root); err != nil || root.Language != "fr" {
		t.Fatalf("Atom language: %s %v", atom.Bytes(), err)
	}
	feed.Language = "fr\" broken"
	if _, err := feed.RSS(); err == nil {
		t.Fatal("invalid RSS language accepted")
	}
	if _, err := feed.Atom(); err == nil {
		t.Fatal("invalid Atom language accepted")
	}
}
