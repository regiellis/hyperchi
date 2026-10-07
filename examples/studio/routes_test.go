package main

import (
	"encoding/json"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/regiellis/hyperchi/hyperchi"
	"github.com/regiellis/hyperchi/hyperchi/config"
)

const testOrigin = "https://driftlamp.example"

// newTestApp builds the production configuration of the site over a
// database in t.TempDir.
func newTestApp(t *testing.T, burst int) *hyperchi.HyperChi {
	t.Helper()
	cfg := config.Production()
	cfg.Database.Name = filepath.Join(t.TempDir(), "studio.db")
	app, err := NewApp(cfg, Options{Origin: testOrigin, SignupBurst: burst, RefillEvery: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := app.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})
	return app
}

type response struct {
	code   int
	header http.Header
	body   string
}

func do(t *testing.T, h http.Handler, req *http.Request) response {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	body, err := io.ReadAll(rec.Result().Body)
	if err != nil {
		t.Fatal(err)
	}
	return response{rec.Code, rec.Result().Header, string(body)}
}

func get(t *testing.T, h http.Handler, target string, headers ...string) response {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	return do(t, h, req)
}

func mustContain(t *testing.T, label, body string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(body, w) {
			t.Errorf("%s: missing %q", label, w)
		}
	}
}

func TestPages(t *testing.T) {
	app := newTestApp(t, 5)
	cases := []struct {
		path  string
		wants []string
	}{
		{"/", []string{"<title>Driftlamp: A three-person game studio", "The Lamplit Crossing", "Wishlist on Steam", `<link rel="canonical" href="https://driftlamp.example/">`, `property="og:image" content="https://driftlamp.example/static/img/og/lamplit-crossing.png?v=`, "From the devlog", `action="/newsletter"`}},
		{"/games", []string{"<title>Games | Driftlamp</title>", "Pocket Orrery", "Drystone"}},
		{"/games/lamplit-crossing", []string{"<title>The Lamplit Crossing | Driftlamp</title>", "Early Access, spring 2027", "At a glance", "Godot 4", "Screenshots", "https://store.example/steam/the-lamplit-crossing", `aria-current="page">Games`}},
		{"/games/pocket-orrery", []string{"Buy on Nintendo eShop", "USD 14.99"}},
		{"/press", []string{"Studio facts", "press@driftlamp.example", `id="drystone"`, "Key art, SVG"}},
		{"/about", []string{"Mara Ilves", "fictional"}},
		{"/careers", []string{"Narrative writer, contract", "/careers/accessibility-tester"}},
		{"/careers/narrative-writer", []string{"How to apply", `datetime="2026-09-16"`}},
		{"/privacy", []string{"local storage"}},
		{"/newsletter", []string{"Hear when the ferry sails", `name="email"`}},
		{"/robots.txt", []string{"Sitemap: https://driftlamp.example/sitemap.xml"}},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			res := get(t, app, tc.path)
			if res.code != http.StatusOK {
				t.Fatalf("status %d", res.code)
			}
			mustContain(t, tc.path, res.body, tc.wants...)
		})
	}
}

func TestSecurityHeadersInProduction(t *testing.T) {
	res := get(t, newTestApp(t, 5), "/")
	csp := res.header.Get("Content-Security-Policy")
	if !strings.Contains(csp, "script-src 'self' 'unsafe-eval'") || strings.Contains(csp, "unsafe-inline") {
		t.Errorf("CSP = %q", csp)
	}
	if res.header.Get("X-Frame-Options") != "DENY" || res.header.Get("Strict-Transport-Security") == "" {
		t.Errorf("headers = %v", res.header)
	}
}

// TestDevlogTagFilter checks the filter both ways c.View answers: the
// template alone for htmx (swapped into #main) and the whole page otherwise.
func TestDevlogTagFilter(t *testing.T) {
	app := newTestApp(t, 5)
	full := get(t, app, "/devlog?tag=audio")
	if full.code != http.StatusOK {
		t.Fatalf("status %d", full.code)
	}
	mustContain(t, "full page", full.body, "<html", "<title>Devlog posts tagged audio | Driftlamp</title>",
		`<link rel="canonical" href="https://driftlamp.example/devlog?tag=audio">`,
		"Recording the harbor", `aria-current="page">audio`)
	if strings.Contains(full.body, "Saving a voyage") {
		t.Error("an untagged post leaked into the filtered list")
	}
	frag := get(t, app, "/devlog?tag=audio", "HX-Request", "true", "HX-Target", "main")
	if frag.code != http.StatusOK || strings.Contains(frag.body, "<html") || strings.Contains(frag.body, "<header class=\"site-header\"") {
		t.Fatalf("htmx answer is not a fragment (status %d)", frag.code)
	}
	mustContain(t, "fragment", frag.body, `data-page-title="Devlog posts tagged audio | Driftlamp"`, "Recording the harbor",
		`hx-get="/devlog?tag=design" hx-target="#main" hx-push-url="true"`)
	boosted := get(t, app, "/devlog?tag=audio", "HX-Request", "true", "HX-Boosted", "true")
	mustContain(t, "boosted", boosted.body, "<html")
}

func TestDevlogPageTwo(t *testing.T) {
	app := newTestApp(t, 5)
	full := get(t, app, "/devlog?page=2")
	frag := get(t, app, "/devlog?page=2", "HX-Request", "true")
	for name, res := range map[string]response{"full": full, "fragment": frag} {
		if res.code != http.StatusOK {
			t.Fatalf("%s: status %d", name, res.code)
		}
		mustContain(t, name, res.body, "page 2 of 3", `aria-current="page"><span class="sr-only">Page </span>2`,
			`href="/devlog"`, `href="/devlog?page=3"`)
		if got := strings.Count(res.body, `class="post-item"`); got != postsPerPage {
			t.Errorf("%s: %d posts, want %d", name, got, postsPerPage)
		}
	}
	mustContain(t, "full", full.body, "<html", `<link rel="canonical" href="https://driftlamp.example/devlog?page=2">`)
	if strings.Contains(frag.body, "<html") {
		t.Error("htmx page 2 rendered the layout")
	}
	for _, target := range []string{"/devlog?page=4", "/devlog?page=0", "/devlog?tag=nope"} {
		if res := get(t, app, target); res.code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", target, res.code)
		}
	}
}

func TestPostPage(t *testing.T) {
	res := get(t, newTestApp(t, 5), "/devlog/saving-a-voyage")
	if res.code != http.StatusOK {
		t.Fatalf("status %d", res.code)
	}
	mustContain(t, "post", res.body,
		"<title>Saving a voyage in 2 KB | Driftlamp</title>",
		`<meta property="og:type" content="article">`,
		`<meta property="article:published_time" content="2026-06-24">`,
		`<h2 id="store-decisions-not-state">Store decisions, not state <a class="heading-anchor" href="#store-decisions-not-state"`,
		`<li class="toc-level-2"><a href="#versioning">Versioning</a></li>`,
		"Older post", "Eleven islands, one map", "Newer post", "What two thousand demo players taught us",
		"<pre><code", `href="/games/lamplit-crossing"`)
}

func TestAtomFeed(t *testing.T) {
	res := get(t, newTestApp(t, 5), "/devlog/feed.xml")
	if res.code != http.StatusOK || res.header.Get("Content-Type") != "application/atom+xml; charset=utf-8" {
		t.Fatalf("status %d, type %q", res.code, res.header.Get("Content-Type"))
	}
	var feed struct {
		XMLName xml.Name `xml:"http://www.w3.org/2005/Atom feed"`
		ID      string   `xml:"id"`
		Updated string   `xml:"updated"`
		Entries []struct {
			ID      string `xml:"id"`
			Title   string `xml:"title"`
			Updated string `xml:"updated"`
			Link    struct {
				Href string `xml:"href,attr"`
			} `xml:"link"`
			Content struct {
				Type string `xml:"type,attr"`
				Body string `xml:",chardata"`
			} `xml:"content"`
		} `xml:"entry"`
	}
	if err := xml.Unmarshal([]byte(res.body), &feed); err != nil {
		t.Fatalf("feed is not valid XML: %v", err)
	}
	if feed.ID != testOrigin+"/devlog" || len(feed.Entries) != 9 {
		t.Fatalf("id %q, %d entries", feed.ID, len(feed.Entries))
	}
	first := feed.Entries[0]
	if first.Title != "The Early Access date, and what is in it" || first.Updated != "2026-09-30T00:00:00Z" || feed.Updated != first.Updated {
		t.Errorf("newest entry = %+v, feed updated %s", first, feed.Updated)
	}
	for _, e := range feed.Entries {
		if _, err := time.Parse(time.RFC3339, e.Updated); err != nil || !strings.HasPrefix(e.ID, testOrigin+"/devlog/") || e.Link.Href != e.ID {
			t.Errorf("entry %q: id %q, link %q, updated %q", e.Title, e.ID, e.Link.Href, e.Updated)
		}
		if e.Content.Type != "html" || !strings.Contains(e.Content.Body, "<h2 id=") {
			t.Errorf("entry %q: content not escaped HTML", e.Title)
		}
	}
}

func TestSitemap(t *testing.T) {
	res := get(t, newTestApp(t, 5), "/sitemap.xml")
	if res.code != http.StatusOK || !strings.HasPrefix(res.header.Get("Content-Type"), "application/xml") {
		t.Fatalf("status %d, type %q", res.code, res.header.Get("Content-Type"))
	}
	var set struct {
		URLs []struct {
			Loc     string `xml:"loc"`
			LastMod string `xml:"lastmod"`
		} `xml:"url"`
	}
	if err := xml.Unmarshal([]byte(res.body), &set); err != nil {
		t.Fatal(err)
	}
	locs := map[string]string{}
	for _, u := range set.URLs {
		locs[u.Loc] = u.LastMod
	}
	for _, p := range []string{"/", "/games/drystone", "/devlog", "/press", "/careers/narrative-writer", "/privacy"} {
		if _, ok := locs[testOrigin+p]; !ok {
			t.Errorf("sitemap lacks %s", p)
		}
	}
	if locs[testOrigin+"/devlog/no-fuel-gauge"] != "2025-11-12" {
		t.Errorf("post lastmod = %q", locs[testOrigin+"/devlog/no-fuel-gauge"])
	}
	if _, ok := locs[testOrigin+thanksPath]; ok {
		t.Error("the noindex confirmation page is in the sitemap")
	}
}

// TestNotFound sends every kind of miss through the error pipeline.
func TestNotFound(t *testing.T) {
	app := newTestApp(t, 5)
	for _, target := range []string{"/nope", "/games/nope", "/devlog/nope", "/static/nope.css"} {
		res := get(t, app, target)
		if res.code != http.StatusNotFound {
			t.Fatalf("%s: status %d", target, res.code)
		}
		mustContain(t, target, res.body, "<html", "This page drifted off", `<meta name="robots" content="noindex">`, "Back to the home page")
		if res.header.Get("Cache-Control") != "no-store" || res.header.Get("X-Robots-Tag") != "noindex" {
			t.Errorf("%s: headers %v", target, res.header)
		}
	}
	frag := get(t, app, "/nope", "HX-Request", "true")
	if frag.code != http.StatusNotFound || strings.Contains(frag.body, "<html") {
		t.Errorf("htmx 404 should be a fragment: %d %q", frag.code, frag.body)
	}
	api := get(t, app, "/nope", "Accept", "application/json")
	var body map[string]any
	if err := json.Unmarshal([]byte(api.body), &body); err != nil || api.code != http.StatusNotFound {
		t.Errorf("JSON 404: %d %q (%v)", api.code, api.body, err)
	}
	method := do(t, app, httptest.NewRequest(http.MethodPost, "/about", nil))
	if method.code != http.StatusMethodNotAllowed || !strings.Contains(method.body, "That request did not work") {
		t.Errorf("405 page: %d", method.code)
	}
}

func TestStaticAssets(t *testing.T) {
	app := newTestApp(t, 5)
	home := get(t, app, "/")
	m := regexp.MustCompile(`href="(/static/css/site\.css\?v=[0-9a-f]{12})"`).FindStringSubmatch(home.body)
	if m == nil {
		t.Fatal("home page does not link a fingerprinted stylesheet")
	}
	css := get(t, app, m[1])
	if css.code != http.StatusOK || css.header.Get("Cache-Control") != "public, max-age=31536000, immutable" || !strings.HasPrefix(css.header.Get("Content-Type"), "text/css") {
		t.Fatalf("css: %d %v", css.code, css.header)
	}
	again := get(t, app, "/static/css/site.css", "If-None-Match", css.header.Get("ETag"))
	if again.code != http.StatusNotModified || again.body != "" {
		t.Errorf("conditional GET: %d", again.code)
	}
	if res := get(t, app, "/static/js/htmx.min.js"); res.header.Get("Cache-Control") != "public, max-age=3600" {
		t.Errorf("unversioned asset cache = %q", res.header.Get("Cache-Control"))
	}
	if res := get(t, app, "/static/css/site.css?v=000000000000"); res.header.Get("Cache-Control") != "public, max-age=3600" {
		t.Errorf("stale fingerprint cache = %q", res.header.Get("Cache-Control"))
	}
	font := get(t, app, "/static/fonts/fraunces-latin-wght-normal.woff2")
	if font.header.Get("Content-Type") != "font/woff2" || font.header.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("font headers: %v", font.header)
	}
	for _, target := range []string{"/static/css", "/static/../go.mod"} {
		if res := get(t, app, target); res.code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", target, res.code)
		}
	}
}

// TestRunsFromAnyDirectory builds the site with the working directory
// somewhere else: templates, content, and static files are all embedded.
func TestRunsFromAnyDirectory(t *testing.T) {
	t.Chdir(t.TempDir())
	cfg := config.Default()
	cfg.Database.Name = filepath.Join(t.TempDir(), "studio.db")
	app, err := NewApp(cfg, Options{Origin: testOrigin, SignupBurst: 5, RefillEvery: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := app.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})
	devlog := get(t, app, "/devlog")
	if devlog.code != http.StatusOK || !strings.Contains(devlog.body, "<html") {
		t.Fatalf("devlog: %d", devlog.code)
	}
	css := get(t, app, "/static/css/site.css")
	if css.code != http.StatusOK || css.header.Get("Cache-Control") != "no-cache" {
		t.Fatalf("development css: %d %q", css.code, css.header.Get("Cache-Control"))
	}
}

func TestPublicOrigin(t *testing.T) {
	dev, prod := config.Default(), config.Production()
	if got, err := publicOrigin("", dev); err != nil || got != "http://localhost:8080" {
		t.Errorf("dev default = %q, %v", got, err)
	}
	if _, err := publicOrigin("", prod); err == nil {
		t.Error("production without PUBLIC_ORIGIN must fail")
	}
	if _, err := publicOrigin("http://driftlamp.example", prod); err == nil {
		t.Error("production must require https")
	}
	if got, err := publicOrigin("https://driftlamp.example/", prod); err != nil || got != testOrigin {
		t.Errorf("trailing slash: %q, %v", got, err)
	}
	if _, err := publicOrigin("https://driftlamp.example/blog", prod); err == nil {
		t.Error("an origin with a path must fail")
	}
}

// TestNoEmDashes holds every indexed page to the project voice.
func TestNoEmDashes(t *testing.T) {
	app := newTestApp(t, 5)
	sitemap := get(t, app, "/sitemap.xml").body
	locs := regexp.MustCompile(`<loc>([^<]+)</loc>`).FindAllStringSubmatch(sitemap, -1)
	for _, m := range append(locs, []string{"", testOrigin + "/nope"}) {
		u, err := url.Parse(m[1])
		if err != nil {
			t.Fatal(err)
		}
		if body := get(t, app, u.RequestURI()).body; strings.ContainsRune(body, '\u2014') {
			t.Errorf("%s contains an em-dash", u.Path)
		}
	}
}
