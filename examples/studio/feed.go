package main

import (
	"encoding/xml"
	"time"
)

// The Atom feed and the sitemap are built once at startup from the same
// library the pages use, then served with c.Blob and their own content types.

type atomFeed struct {
	XMLName  xml.Name    `xml:"http://www.w3.org/2005/Atom feed"`
	Title    string      `xml:"title"`
	Subtitle string      `xml:"subtitle"`
	ID       string      `xml:"id"`
	Updated  string      `xml:"updated"`
	Links    []atomLink  `xml:"link"`
	Author   atomPerson  `xml:"author"`
	Entries  []atomEntry `xml:"entry"`
}

type atomLink struct {
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr,omitempty"`
	Type string `xml:"type,attr,omitempty"`
}

type atomPerson struct {
	Name string `xml:"name"`
}

type atomText struct {
	Type string `xml:"type,attr"`
	Body string `xml:",chardata"`
}

type atomCategory struct {
	Term string `xml:"term,attr"`
}

type atomEntry struct {
	Title      string         `xml:"title"`
	ID         string         `xml:"id"`
	Updated    string         `xml:"updated"`
	Published  string         `xml:"published"`
	Links      []atomLink     `xml:"link"`
	Author     atomPerson     `xml:"author"`
	Summary    string         `xml:"summary"`
	Content    atomText       `xml:"content"`
	Categories []atomCategory `xml:"category"`
}

// feedTime gives a post's publication day as an Atom timestamp (midnight UTC).
func feedTime(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// AtomFeed renders the devlog as an Atom 1.0 document. Atom needs absolute
// IRIs for ids and links, which is why the site needs a public origin.
func (s *Site) AtomFeed() ([]byte, error) {
	posts := s.Lib.Posts
	feed := atomFeed{
		Title:    s.Name + " devlog",
		Subtitle: "Notes on design, art, sound, and running a small studio.",
		ID:       s.Origin + "/devlog",
		Updated:  feedTime(posts[0].Date),
		Links: []atomLink{
			{Href: s.Origin + "/devlog/feed.xml", Rel: "self", Type: "application/atom+xml"},
			{Href: s.Origin + "/devlog", Rel: "alternate", Type: "text/html"},
		},
		Author: atomPerson{Name: s.Name},
	}
	for _, p := range posts {
		e := atomEntry{
			Title:     p.Title,
			ID:        s.Origin + p.Path,
			Updated:   feedTime(p.Date),
			Published: feedTime(p.Date),
			Links:     []atomLink{{Href: s.Origin + p.Path, Rel: "alternate", Type: "text/html"}},
			Author:    atomPerson{Name: p.Author},
			Summary:   p.Summary,
			Content:   atomText{Type: "html", Body: string(p.Body)}, // escaped by encoding/xml
		}
		for _, t := range p.Tags {
			e.Categories = append(e.Categories, atomCategory{Term: t})
		}
		feed.Entries = append(feed.Entries, e)
	}
	body, err := xml.MarshalIndent(feed, "", "  ")
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), append(body, '\n')...), nil
}

type urlSet struct {
	XMLName xml.Name     `xml:"http://www.sitemaps.org/schemas/sitemap/0.9 urlset"`
	URLs    []sitemapURL `xml:"url"`
}

type sitemapURL struct {
	Loc     string `xml:"loc"`
	LastMod string `xml:"lastmod,omitempty"`
}

// Sitemap lists the given paths with absolute URLs; posts carry their date.
func (s *Site) Sitemap(paths []string) ([]byte, error) {
	set := urlSet{}
	for _, p := range paths {
		u := sitemapURL{Loc: s.Origin + p}
		for _, post := range s.Lib.Posts {
			if post.Path == p {
				u.LastMod = post.Date.Format(dateLayout)
			}
		}
		set.URLs = append(set.URLs, u)
	}
	body, err := xml.MarshalIndent(set, "", "  ")
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), append(body, '\n')...), nil
}
