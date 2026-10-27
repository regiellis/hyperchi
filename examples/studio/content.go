package main

import (
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"maps"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/regiellis/hyperchi/hyperchi"
	"github.com/regiellis/hyperchi/hyperchi/content"
	"github.com/regiellis/hyperchi/modules/markdown"
)

// The content model. Every file under content/ is Markdown with flat front
// matter, loaded once at startup by app.Content.LoadFS: each directory is a
// collection with a schema (required fields, types, dates, known keys), and
// modules/markdown renders the bodies. The library then builds typed models
// and checks what spans files (a post's game, one flagship). A missing field,
// an unknown key, a bad date, or a bad slug is an error, so a broken edit
// fails the tests and the boot instead of a visitor's request.

const dateLayout = "2006-01-02"

var slugPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// Link is a labeled external URL, such as a store page.
type Link struct {
	Label string
	URL   string
}

// Image is a picture under static/ with its alternative text.
type Image struct {
	Src string // URL path, such as "/static/img/games/drystone/key-art.svg"
	Alt string
}

// Fact is one row of a press-kit fact sheet.
type Fact struct {
	Label string
	Value string
}

// Game is one title from content/games.
type Game struct {
	Slug       string
	Path       string
	Title      string
	Tagline    string
	Summary    string
	Genre      string
	Status     string // shown to visitors: "Early Access, spring 2027"
	StatusKind string // released, early-access, or in-development
	Order      int
	Flagship   bool
	Platforms  []string
	Stores     []Link
	Cover      Image
	Screens    []Image
	Facts      []Fact
	Body       template.HTML
}

// Post is one devlog entry from content/devlog.
type Post struct {
	Slug        string
	Path        string
	Title       string
	Author      string
	Summary     string
	Date        time.Time
	Tags        []string
	Game        *Game // the game the post is about, if any
	Body        template.HTML
	TOC         []markdown.Heading // level 2 and 3 headings, in order
	ReadMinutes int
	Newer       *Post
	Older       *Post
}

// Page is a standalone page from content/pages (about, press, privacy).
type Page struct {
	Slug        string
	Title       string
	Description string
	Facts       []Fact
	Body        template.HTML
}

// Role is an open position from content/careers.
type Role struct {
	Slug     string
	Path     string
	Title    string
	Location string
	Kind     string // "Contract, 6 months"
	Summary  string
	Posted   time.Time
	Body     template.HTML
}

// TagCount is a devlog tag and how many posts carry it.
type TagCount struct {
	Name  string
	Count int
}

// Library is all site content, loaded once.
type Library struct {
	Games []*Game // by Order
	Posts []*Post // newest first
	Tags  []TagCount
	Pages map[string]*Page
	Roles []*Role // newest first
	games map[string]*Game
	posts map[string]*Post
	byTag map[string][]*Post
}

// requiredPages are the pages the routes render; each must exist.
var requiredPages = []string{"about", "press", "privacy"}

// The content collections, in load order: a post may name its game.
var collections = []string{"games", "devlog", "pages", "careers"}

// LoadLibrary loads every Markdown file under content/ in fsys into cm (the
// app's content manager) and builds the typed library from it.
func LoadLibrary(cm *hyperchi.ContentManager, fsys fs.FS) (*Library, error) {
	md := markdown.New()
	render := func(body string) (template.HTML, error) {
		doc, err := md.Parse([]byte(body))
		if err != nil {
			return "", err
		}
		return doc.HTML, nil
	}
	opts := []hyperchi.ContentOption{hyperchi.WithMarkdown(render)}
	for _, c := range contentSchemas() {
		opts = append(opts, hyperchi.WithContentCollection(c))
	}
	// A file that fails its schema is left out and reported here; any
	// report fails the load.
	if err := cm.LoadFS(fsys, "content", opts...); err != nil {
		return nil, err
	}

	lib := &Library{
		Pages: map[string]*Page{},
		games: map[string]*Game{},
		posts: map[string]*Post{},
		byTag: map[string][]*Post{},
	}
	add := map[string]func(*hyperchi.ContentItem) error{
		"games":   lib.addGame,
		"devlog":  func(item *hyperchi.ContentItem) error { return lib.addPost(md, item) },
		"pages":   lib.addPage,
		"careers": lib.addRole,
	}
	var errs []error
	for _, name := range collections {
		items, err := cm.Query(name).All()
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if len(items) == 0 {
			errs = append(errs, fmt.Errorf("content/%s: no Markdown files", name))
		}
		for _, item := range items {
			if !slugPattern.MatchString(item.Slug) {
				errs = append(errs, fmt.Errorf("%s: file name must be a lowercase slug", item.Path))
				continue
			}
			if err := add[name](item); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", item.Path, err))
			}
		}
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return lib, lib.finish()
}

// contentSchemas declares each collection's front matter: the required
// fields and their types, and the keys a file may carry at all, so a typo
// fails as an unknown key. The README lists the same schema.
func contentSchemas() []content.CollectionConfig {
	text := func(b *content.SchemaBuilder, names ...string) *content.SchemaBuilder {
		for _, name := range names {
			b = b.Field(name, content.FieldTypeString).Required().MinLength(1).Build()
		}
		return b
	}
	games := text(content.NewSchema("games"),
		"title", "tagline", "summary", "genre", "status", "status_kind", "platforms", "cover", "screen_1").
		Field("order", content.FieldTypeInt).Required().Build().
		Field("flagship", content.FieldTypeBool).Default(false).Build().
		Validate(knownKeys([]string{"title", "tagline", "summary", "genre", "status", "status_kind", "platforms", "cover", "order", "flagship"},
			"screen_", "store_", "fact_"))
	devlog := text(content.NewSchema("devlog"), "title", "author", "tags", "summary").
		Field("date", content.FieldTypeTime).Required().Build().
		Field("game", content.FieldTypeString).Build().
		Validate(knownKeys([]string{"title", "author", "tags", "summary", "date", "game"}))
	pages := text(content.NewSchema("pages"), "title", "description").
		Validate(knownKeys([]string{"title", "description"}, "fact_"))
	careers := text(content.NewSchema("careers"), "title", "location", "kind", "summary").
		Field("posted", content.FieldTypeTime).Required().Build().
		Validate(knownKeys([]string{"title", "location", "kind", "summary", "posted"}))
	return []content.CollectionConfig{
		{Name: "games", Schema: games.Build()},
		{Name: "devlog", Schema: devlog.Build()},
		{Name: "pages", Schema: pages.Build()},
		{Name: "careers", Schema: careers.Build()},
	}
}

// knownKeys rejects a front matter key that is neither listed nor starts
// with one of the prefixes.
func knownKeys(keys []string, prefixes ...string) content.ValidationFunc {
	return func(c *content.Content) error {
		var errs []error
		for _, k := range slices.Sorted(maps.Keys(c.Data)) {
			if slices.Contains(keys, k) || slices.ContainsFunc(prefixes, func(p string) bool { return strings.HasPrefix(k, p) }) {
				continue
			}
			errs = append(errs, fmt.Errorf("unknown front matter key %q", k))
		}
		return errors.Join(errs...)
	}
}

// Game returns the game with the slug, or nil.
func (l *Library) Game(slug string) *Game { return l.games[slug] }

// Post returns the post with the slug, or nil.
func (l *Library) Post(slug string) *Post { return l.posts[slug] }

// Flagship returns the game marked flagship.
func (l *Library) Flagship() *Game {
	for _, g := range l.Games {
		if g.Flagship {
			return g
		}
	}
	return nil
}

// PostsTagged returns the posts with the tag, newest first; "" means all.
func (l *Library) PostsTagged(tag string) ([]*Post, bool) {
	if tag == "" {
		return l.Posts, true
	}
	posts, ok := l.byTag[tag]
	return posts, ok
}

// PostsAbout returns the posts that name the game, newest first.
func (l *Library) PostsAbout(g *Game) []*Post {
	var out []*Post
	for _, p := range l.Posts {
		if p.Game == g {
			out = append(out, p)
		}
	}
	return out
}

func (l *Library) finish() error {
	if len(l.Games) == 0 || len(l.Posts) == 0 {
		return errors.New("content: need at least one game and one post")
	}
	flagships := 0
	for _, g := range l.Games {
		if g.Flagship {
			flagships++
		}
	}
	if flagships != 1 {
		return fmt.Errorf("content/games: exactly one game must be flagship, found %d", flagships)
	}
	for _, slug := range requiredPages {
		if l.Pages[slug] == nil {
			return fmt.Errorf("content/pages: %s.md is required", slug)
		}
	}
	sort.SliceStable(l.Games, func(i, j int) bool { return l.Games[i].Order < l.Games[j].Order })
	// Newest first; the slug breaks a tie so the order never depends on the file system.
	byDate := func(a, b *Post) int {
		if c := b.Date.Compare(a.Date); c != 0 {
			return c
		}
		return strings.Compare(a.Slug, b.Slug)
	}
	slices.SortFunc(l.Posts, byDate)
	for i, p := range l.Posts {
		if i > 0 {
			p.Newer = l.Posts[i-1]
		}
		if i+1 < len(l.Posts) {
			p.Older = l.Posts[i+1]
		}
		for _, t := range p.Tags {
			l.byTag[t] = append(l.byTag[t], p)
		}
	}
	for name, posts := range l.byTag {
		l.Tags = append(l.Tags, TagCount{Name: name, Count: len(posts)})
	}
	sort.Slice(l.Tags, func(i, j int) bool { return l.Tags[i].Name < l.Tags[j].Name })
	sort.SliceStable(l.Roles, func(i, j int) bool { return l.Roles[i].Posted.After(l.Roles[j].Posted) })
	return nil
}

// field returns a front matter value as text. The schema has checked the
// required ones; facts may be numbers, which print as written.
func field(item *hyperchi.ContentItem, key string) string {
	v, ok := item.Metadata[key]
	if !ok {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(v))
}

// list splits a comma-separated value.
func list(item *hyperchi.ContentItem, key string) []string {
	var out []string
	for _, v := range strings.Split(field(item, key), ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// date returns a time field the schema has parsed.
func date(item *hyperchi.ContentItem, key string) time.Time {
	t, _ := item.Metadata[key].(time.Time) // the schema typed it; zero otherwise
	return t
}

// prefixed returns the keys starting with prefix, sorted.
func prefixed(item *hyperchi.ContentItem, prefix string) []string {
	var keys []string
	for k := range item.Metadata {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

// image parses "img/path/under/static.svg | Alternative text" into an image
// at /static/img/....
func image(key, raw string) (Image, error) {
	src, alt, ok := strings.Cut(raw, "|")
	src, alt = strings.TrimSpace(src), strings.TrimSpace(alt)
	if !ok || src == "" || alt == "" {
		return Image{}, fmt.Errorf("%s: want \"img/path.svg | alternative text\"", key)
	}
	return Image{Src: "/static/" + strings.TrimPrefix(src, "/"), Alt: alt}, nil
}

// facts reads fact_* keys in a stable, press-friendly order.
func facts(item *hyperchi.ContentItem) []Fact {
	keys := prefixed(item, "fact_")
	rank := func(k string) int {
		if i := slices.Index(factOrder, strings.TrimPrefix(k, "fact_")); i >= 0 {
			return i
		}
		return len(factOrder)
	}
	sort.SliceStable(keys, func(i, j int) bool { return rank(keys[i]) < rank(keys[j]) })
	out := make([]Fact, 0, len(keys))
	for _, k := range keys {
		label := strings.ReplaceAll(strings.TrimPrefix(k, "fact_"), "_", " ")
		out = append(out, Fact{Label: strings.ToUpper(label[:1]) + label[1:], Value: field(item, k)})
	}
	return out
}

var factOrder = []string{"developer", "founded", "based", "team", "release", "platforms", "price", "players", "languages", "engine", "contact"}

// storeOrder lists the stores a game may link, in the order buttons appear:
// the first is the primary call to action.
var storeOrder = []struct{ key, label string }{
	{"store_steam", "Steam"},
	{"store_eshop", "Nintendo eShop"},
	{"store_gog", "GOG"},
	{"store_itch", "itch.io"},
}

var statusKinds = []string{"released", "early-access", "in-development"}

func (l *Library) addGame(item *hyperchi.ContentItem) error {
	g := &Game{
		Slug:       item.Slug,
		Path:       "/games/" + item.Slug,
		Title:      field(item, "title"),
		Tagline:    field(item, "tagline"),
		Summary:    field(item, "summary"),
		Genre:      field(item, "genre"),
		Status:     field(item, "status"),
		StatusKind: field(item, "status_kind"),
		Platforms:  list(item, "platforms"),
		Flagship:   item.Metadata["flagship"] == true,
		Facts:      facts(item),
		Body:       item.HTML,
	}
	g.Order, _ = item.Metadata["order"].(int) // the schema requires an int
	var errs []error
	if !slices.Contains(statusKinds, g.StatusKind) {
		errs = append(errs, fmt.Errorf("status_kind must be one of %v", statusKinds))
	}
	cover, err := image("cover", field(item, "cover"))
	errs = append(errs, err)
	g.Cover = cover
	for _, k := range prefixed(item, "screen_") {
		shot, err := image(k, field(item, k))
		errs = append(errs, err)
		g.Screens = append(g.Screens, shot)
	}
	stores := prefixed(item, "store_")
	for _, store := range storeOrder {
		if i := slices.Index(stores, store.key); i >= 0 {
			g.Stores = append(g.Stores, Link{Label: store.label, URL: field(item, store.key)})
			stores = slices.Delete(stores, i, i+1)
		}
	}
	for _, k := range stores {
		errs = append(errs, fmt.Errorf("unknown store %q", k))
	}
	if err := errors.Join(errs...); err != nil {
		return err
	}
	l.Games = append(l.Games, g)
	l.games[g.Slug] = g
	return nil
}

// addPost builds a post. The body is parsed again for its headings (the
// table of contents) and plain text (the reading time): WithMarkdown hands
// the loader HTML only.
func (l *Library) addPost(md *markdown.Renderer, item *hyperchi.ContentItem) error {
	doc, err := md.Parse([]byte(item.Content))
	if err != nil {
		return err
	}
	p := &Post{
		Slug:    item.Slug,
		Path:    "/devlog/" + item.Slug,
		Title:   field(item, "title"),
		Author:  field(item, "author"),
		Summary: field(item, "summary"),
		Date:    date(item, "date"),
		Tags:    list(item, "tags"),
		Body:    withHeadingAnchors(item.HTML),
	}
	var errs []error
	for _, t := range p.Tags {
		if !slugPattern.MatchString(t) {
			errs = append(errs, fmt.Errorf("tag %q must be a lowercase slug", t))
		}
	}
	if game := field(item, "game"); game != "" {
		if p.Game = l.games[game]; p.Game == nil {
			errs = append(errs, fmt.Errorf("game %q does not exist", game))
		}
	}
	for _, h := range doc.Headings {
		if h.Level == 2 || h.Level == 3 {
			p.TOC = append(p.TOC, h)
		}
	}
	words := len(strings.Fields(doc.Text()))
	p.ReadMinutes = max(1, (words+199)/200)
	if err := errors.Join(errs...); err != nil {
		return err
	}
	l.Posts = append(l.Posts, p)
	l.posts[p.Slug] = p
	return nil
}

func (l *Library) addPage(item *hyperchi.ContentItem) error {
	l.Pages[item.Slug] = &Page{
		Slug:        item.Slug,
		Title:       field(item, "title"),
		Description: field(item, "description"),
		Facts:       facts(item),
		Body:        withHeadingAnchors(item.HTML),
	}
	return nil
}

func (l *Library) addRole(item *hyperchi.ContentItem) error {
	l.Roles = append(l.Roles, &Role{
		Slug:     item.Slug,
		Path:     "/careers/" + item.Slug,
		Title:    field(item, "title"),
		Location: field(item, "location"),
		Kind:     field(item, "kind"),
		Summary:  field(item, "summary"),
		Posted:   date(item, "posted"),
		Body:     item.HTML,
	})
	return nil
}

// headingTag matches the h2 and h3 elements goldmark renders with an id.
var headingTag = regexp.MustCompile(`(?s)<h([23]) id="([a-z0-9-]+)">(.*?)</h[23]>`)

// withHeadingAnchors gives each h2 and h3 a link to itself. The ids and inner
// HTML come from goldmark's safe-mode output, and the id pattern admits only
// slug characters, so nothing unescaped is introduced.
func withHeadingAnchors(body template.HTML) template.HTML {
	out := headingTag.ReplaceAllString(string(body),
		`<h$1 id="$2">$3 <a class="heading-anchor" href="#$2" aria-label="Link to this section">#</a></h$1>`)
	return template.HTML(out) // goldmark safe-mode output plus fixed markup
}
