package publishing

import (
	"encoding/xml"
	"time"
)

type rssItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	GUID        string `xml:"guid"`
	Description string `xml:"description,omitempty"`
	Published   string `xml:"pubDate,omitempty"`
}

// RSS generates RSS 2.0. Items use Published for pubDate, falling back to
// Updated when Published is absent. Author is required by the shared feed model
// but is emitted only by Atom because RSS author requires an email address.
func (f Feed) RSS() (Document, error) {
	if err := f.validate(); err != nil {
		return Document{}, err
	}
	channel := struct {
		Language    string    `xml:"language,omitempty"`
		Title       string    `xml:"title"`
		Link        string    `xml:"link"`
		Description string    `xml:"description"`
		Updated     string    `xml:"lastBuildDate"`
		Items       []rssItem `xml:"item"`
	}{Language: f.Language, Title: f.Title, Link: f.URL, Description: f.Description, Updated: date(f.Updated, time.RFC1123Z)}
	for _, e := range f.Entries {
		published := e.Published
		if published.IsZero() {
			published = e.Updated
		}
		channel.Items = append(channel.Items, rssItem{e.Title, e.URL, e.URL, e.Summary, date(published, time.RFC1123Z)})
	}
	root := struct {
		XMLName xml.Name `xml:"rss"`
		Version string   `xml:"version,attr"`
		Channel any      `xml:"channel"`
	}{Version: "2.0", Channel: channel}
	return document(root, "application/rss+xml")
}

type atomLink struct {
	Rel  string `xml:"rel,attr"`
	Href string `xml:"href,attr"`
}
type atomAuthor struct {
	Name string `xml:"name"`
}
type atomEntry struct {
	Title     string   `xml:"title"`
	ID        string   `xml:"id"`
	Link      atomLink `xml:"link"`
	Updated   string   `xml:"updated"`
	Published string   `xml:"published,omitempty"`
	Summary   string   `xml:"summary"`
}

// Atom generates Atom 1.0. Entries inherit the feed's named author and link
// to their canonical page; summaries are XML-escaped plain text.
func (f Feed) Atom() (Document, error) {
	if err := f.validate(); err != nil {
		return Document{}, err
	}
	root := struct {
		XMLName   xml.Name    `xml:"feed"`
		Language  string      `xml:"xml:lang,attr,omitempty"`
		Namespace string      `xml:"xmlns,attr"`
		Title     string      `xml:"title"`
		Subtitle  string      `xml:"subtitle"`
		ID        string      `xml:"id"`
		Updated   string      `xml:"updated"`
		Author    atomAuthor  `xml:"author"`
		Links     []atomLink  `xml:"link"`
		Entries   []atomEntry `xml:"entry"`
	}{Language: f.Language, Namespace: "http://www.w3.org/2005/Atom", Title: f.Title, Subtitle: f.Description, ID: f.FeedURL, Updated: date(f.Updated, time.RFC3339), Author: atomAuthor{f.Author}, Links: []atomLink{{"alternate", f.URL}, {"self", f.FeedURL}}}
	for _, e := range f.Entries {
		root.Entries = append(root.Entries, atomEntry{e.Title, e.URL, atomLink{"alternate", e.URL}, date(e.Updated, time.RFC3339), date(e.Published, time.RFC3339), e.Summary})
	}
	return document(root, "application/atom+xml")
}
