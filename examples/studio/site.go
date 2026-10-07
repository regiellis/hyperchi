package main

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/regiellis/hyperchi/hyperchi"
)

// Site joins the content library, the static files, and the public origin,
// and knows how to describe each page to search engines and link previews.
type Site struct {
	Name    string
	Tagline string
	Origin  string
	Lib     *Library
	// assetURL is app.AssetURL: the versioned URL of a file under /static/.
	assetURL func(string) (string, error)
}

// NewSite checks that every image the content names exists before any page
// renders, so a missing screenshot fails the boot and the tests. assetURL is
// app.AssetURL, called after the static files are mounted.
func NewSite(lib *Library, assetURL func(string) (string, error), origin string) (*Site, error) {
	s := &Site{
		Name:     "Driftlamp",
		Tagline:  "A three-person game studio making quiet, careful games.",
		Origin:   origin,
		Lib:      lib,
		assetURL: assetURL,
	}
	var errs []error
	need := func(owner, src string) {
		if _, err := assetURL(src); err != nil {
			errs = append(errs, fmt.Errorf("%s: image %q is not under static/: %w", owner, src, err))
		}
	}
	need("site", defaultOGImage)
	for _, g := range lib.Games {
		need(g.Path, g.Cover.Src)
		need(g.Path, ogImageFor(g))
		for _, shot := range g.Screens {
			need(g.Path, shot.Src)
		}
	}
	return s, errors.Join(errs...)
}

const defaultOGImage = "/static/img/og/studio.png"

func ogImageFor(g *Game) string { return "/static/img/og/" + g.Slug + ".png" }

// NavItem is one entry of the primary navigation.
type NavItem struct {
	Label   string
	Path    string
	Section string
}

// Globals are the values every template sees through app.SetGlobal.
type Globals struct {
	Name    string
	Tagline string
	Nav     []NavItem
	Year    int
	FeedURL string
}

// Globals returns the site-wide template values.
func (s *Site) Globals() Globals {
	return Globals{
		Name:    s.Name,
		Tagline: s.Tagline,
		Year:    time.Now().Year(),
		FeedURL: "/devlog/feed.xml",
		Nav: []NavItem{
			{"Games", "/games", "games"},
			{"Devlog", "/devlog", "devlog"},
			{"Press", "/press", "press"},
			{"About", "/about", "about"},
			{"Careers", "/careers", "careers"},
		},
	}
}

// Meta describes one page for the layout's head: title, description,
// canonical URL, and Open Graph tags, all absolute where they must be.
type Meta struct {
	Title       string
	Description string
	Path        string
	Canonical   string
	Image       string
	ImageAlt    string
	Type        string // "website" or "article"
	Section     string // the nav item to mark as current
	Published   time.Time
	NoIndex     bool
}

// Meta builds the head data for a page at path. An empty title is the home page.
func (s *Site) Meta(title, description, path, section string) Meta {
	full := s.Name + ": " + s.Tagline
	if title != "" {
		full = title + " | " + s.Name
	}
	return Meta{
		Title:       full,
		Description: description,
		Path:        path,
		Canonical:   s.Origin + path,
		Image:       s.absoluteAsset(defaultOGImage),
		ImageAlt:    "The Driftlamp lantern over a dark harbor",
		Type:        "website",
		Section:     section,
	}
}

func (s *Site) absoluteAsset(name string) string {
	u, err := s.assetURL(name)
	if err != nil { // NewSite checked every image the site names
		return ""
	}
	return s.Origin + u
}

// prerendered is one page whose content is fixed at startup.
type prerendered struct {
	path, tmpl string
	data       hyperchi.H
}

// Pages lists every pre-rendered page in sitemap order.
func (s *Site) Pages() []prerendered {
	lib := s.Lib
	flagship := lib.Flagship()
	home := s.Meta("", "Driftlamp makes "+flagship.Title+" and other small games about keeping things running. Read the devlog, get the press kit, or join the list.", "/", "home")
	home.Image, home.ImageAlt = s.absoluteAsset(ogImageFor(flagship)), flagship.Cover.Alt
	press := s.pageData("press", "/press", "press")
	press["games"] = lib.Games
	pages := []prerendered{
		{"/", "home", hyperchi.H{"meta": home, "flagship": flagship, "games": lib.Games, "posts": lib.Posts[:min(3, len(lib.Posts))], "form": SignupForm{List: "studio"}}},
		{"/games", "games", hyperchi.H{"meta": s.Meta("Games", "Every Driftlamp game, with platforms, release status, and where to buy.", "/games", "games"), "games": lib.Games}},
	}
	for _, g := range lib.Games {
		m := s.Meta(g.Title, g.Summary, g.Path, "games")
		m.Image, m.ImageAlt = s.absoluteAsset(ogImageFor(g)), g.Cover.Alt
		pages = append(pages, prerendered{g.Path, "game", hyperchi.H{"meta": m, "game": g, "posts": lib.PostsAbout(g)}})
	}
	for _, p := range lib.Posts {
		m := s.Meta(p.Title, p.Summary, p.Path, "devlog")
		m.Type, m.Published = "article", p.Date
		if p.Game != nil {
			m.Image, m.ImageAlt = s.absoluteAsset(ogImageFor(p.Game)), p.Game.Cover.Alt
		}
		pages = append(pages, prerendered{p.Path, "post", hyperchi.H{"meta": m, "post": p}})
	}
	pages = append(pages,
		prerendered{"/press", "press", press},
		prerendered{"/about", "page", s.pageData("about", "/about", "about")},
		prerendered{"/careers", "careers", hyperchi.H{"meta": s.Meta("Careers", "Open roles at Driftlamp. We hire a few people at a time, remotely, on clear contracts.", "/careers", "careers"), "roles": lib.Roles}},
	)
	for _, r := range lib.Roles {
		pages = append(pages, prerendered{r.Path, "role", hyperchi.H{"meta": s.Meta(r.Title, r.Summary, r.Path, "careers"), "role": r}})
	}
	return append(pages,
		prerendered{"/newsletter", "newsletter", hyperchi.H{"meta": s.Meta("Newsletter", "Join the Driftlamp list for devlog posts and launch news.", "/newsletter", ""), "form": SignupForm{List: "studio"}}},
		prerendered{"/privacy", "page", s.pageData("privacy", "/privacy", "")},
		prerendered{thanksPath, "newsletter-thanks", hyperchi.H{"meta": noIndex(s.Meta("You are on the list", "Newsletter confirmation.", thanksPath, ""))}},
	)
}

// Routes registers every page. Pages whose content is fixed at startup are
// pre-rendered once (PreRenderedPage); the devlog index reads its query.
func (s *Site) Routes(app *hyperchi.HyperChi, dev bool) error {
	pages := s.Pages()
	var indexed []string
	for _, p := range pages {
		if err := app.PreRenderedPage(p.path, p.tmpl, p.data, hyperchi.PreRenderOptions{Layout: "base"}); err != nil {
			return fmt.Errorf("pre-render %s: %w", p.path, err)
		}
		if m, ok := p.data["meta"].(Meta); ok && !m.NoIndex {
			indexed = append(indexed, p.path)
		}
	}
	indexed = append(indexed, "/devlog")

	app.Get("/devlog", s.devlogIndex)
	feed, err := s.AtomFeed()
	if err != nil {
		return err
	}
	app.Get("/devlog/feed.xml", func(c *hyperchi.Context) error {
		return c.Blob("application/atom+xml; charset=utf-8", feed)
	})
	sitemap, err := s.Sitemap(indexed)
	if err != nil {
		return err
	}
	app.Get("/sitemap.xml", func(c *hyperchi.Context) error {
		return c.Blob("application/xml; charset=utf-8", sitemap)
	})
	robots := "User-agent: *\nAllow: /\nSitemap: " + s.Origin + "/sitemap.xml\n"
	app.Get("/robots.txt", func(c *hyperchi.Context) error { return c.Text(robots) })
	app.Get("/healthz", func(c *hyperchi.Context) error {
		return c.SetHeader("Cache-Control", "no-store").Text("ok\n")
	})
	// One branded 404, rendered once. Every miss reaches it through OnError:
	// unknown routes, unknown slugs, missing static files.
	notFound, err := app.PreRender("error", hyperchi.H{
		"meta":    noIndex(s.Meta("Page not found", "There is no page at this address.", "/404", "")),
		"full":    true,
		"status":  http.StatusNotFound,
		"message": "There is no page at this address. It may have moved when we reorganized the site.",
	}, hyperchi.PreRenderOptions{Layout: "base", Status: http.StatusNotFound})
	if err != nil {
		return fmt.Errorf("pre-render 404: %w", err)
	}
	app.OnError(s.onError(notFound, dev))
	return nil
}

func noIndex(m Meta) Meta {
	m.NoIndex = true
	return m
}

func (s *Site) pageData(slug, path, section string) hyperchi.H {
	p := s.Lib.Pages[slug]
	return hyperchi.H{"meta": s.Meta(p.Title, p.Description, path, section), "page": p}
}

// onError renders every error a browser sees in the site's design. htmx and
// JSON clients keep the framework's default (an error fragment or JSON).
func (s *Site) onError(notFound hyperchi.Handler, dev bool) hyperchi.ErrorFunc {
	return func(c *hyperchi.Context, err error) {
		code, message := http.StatusInternalServerError, "Something went wrong on our side. Please try again in a moment."
		var he *hyperchi.HTTPError
		if errors.As(err, &he) {
			code = he.Code
			if code < http.StatusInternalServerError {
				message = he.Message
			}
		}
		accept := c.Header("Accept")
		apiClient := strings.Contains(accept, "application/json") && !strings.Contains(accept, "text/html")
		if c.IsHTMX() || apiClient || (dev && code >= http.StatusInternalServerError) {
			if c.IsHTMX() && c.Path() == signupPath {
				// Keep the visitor's form: show the problem in its status line.
				c.HX().Retarget("#newsletter-status").Reswap("innerHTML")
			}
			c.App().DefaultError(c, err) // development 5xx: the full cause
			return
		}
		if code == http.StatusNotFound {
			if perr := notFound(c); perr != nil {
				log.Printf("studio: 404 page: %v", perr)
			}
			return
		}
		if code >= http.StatusInternalServerError {
			log.Printf("studio: %s %s: %v", c.Method(), c.Path(), err)
		}
		title := http.StatusText(code)
		data := hyperchi.H{
			"meta":    noIndex(s.Meta(title, title, c.Path(), "")),
			"full":    true,
			"status":  code,
			"message": message,
		}
		if perr := c.Status(code).Page("error", data); perr != nil {
			log.Printf("studio: error page: %v", perr)
			c.App().DefaultError(c, err)
		}
	}
}

const postsPerPage = 4

// DevlogView is one page of the devlog index, optionally filtered by tag.
type DevlogView struct {
	Tag      string
	Tags     []TagCount
	Total    int
	Posts    []*Post
	Page     int
	Pages    int
	NewerURL string
	OlderURL string
	PageURLs []PageLink
	AllURL   string
}

// PageLink is one numbered link of the pager.
type PageLink struct {
	Number  int
	URL     string
	Current bool
}

// devlogURL is the canonical address of a devlog index page.
func devlogURL(tag string, page int) string {
	q := url.Values{}
	if tag != "" {
		q.Set("tag", tag)
	}
	if page > 1 {
		q.Set("page", strconv.Itoa(page))
	}
	if len(q) == 0 {
		return "/devlog"
	}
	return "/devlog?" + q.Encode()
}

// Devlog returns one page of posts. An unknown tag or a page past the end
// is a 404, not an empty list: those URLs should not exist.
func (s *Site) Devlog(tag string, page int) (DevlogView, error) {
	posts, ok := s.Lib.PostsTagged(tag)
	if !ok {
		return DevlogView{}, hyperchi.NotFound("No devlog posts carry that tag.")
	}
	pages := (len(posts) + postsPerPage - 1) / postsPerPage
	if page < 1 || page > pages {
		return DevlogView{}, hyperchi.NotFound("That page of the devlog does not exist.")
	}
	v := DevlogView{
		Tag: tag, Tags: s.Lib.Tags, Total: len(posts), Page: page, Pages: pages,
		Posts:  posts[(page-1)*postsPerPage : min(page*postsPerPage, len(posts))],
		AllURL: devlogURL("", 1),
	}
	if page > 1 {
		v.NewerURL = devlogURL(tag, page-1)
	}
	if page < pages {
		v.OlderURL = devlogURL(tag, page+1)
	}
	for n := 1; n <= pages; n++ {
		v.PageURLs = append(v.PageURLs, PageLink{Number: n, URL: devlogURL(tag, n), Current: n == page})
	}
	return v, nil
}

// devlogIndex serves /devlog. The tag filters and the pager ask with htmx
// for #main's contents and push the URL; c.View answers that request with
// the template alone and a normal visit with the full page.
func (s *Site) devlogIndex(c *hyperchi.Context) error {
	tag := c.Query("tag")
	page := c.QueryInt("page", 1)
	view, err := s.Devlog(tag, page)
	if err != nil {
		return err
	}
	title, desc := "Devlog", "Notes from Driftlamp on design, art, sound, and running a small studio."
	if tag != "" {
		title = "Devlog posts tagged " + tag
		desc = fmt.Sprintf("%d Driftlamp devlog posts tagged %s.", view.Total, tag)
	}
	if page > 1 {
		title += fmt.Sprintf(", page %d", page)
	}
	return c.View("devlog", hyperchi.H{
		"meta":   s.Meta(title, desc, devlogURL(tag, page), "devlog"),
		"devlog": view,
	})
}
