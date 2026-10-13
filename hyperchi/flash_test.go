package hyperchi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

// browser carries cookies between requests the way a browser does: a
// Set-Cookie with Max-Age below zero deletes the cookie.
type browser struct {
	t       *testing.T
	app     *HyperChi
	cookies map[string]*http.Cookie
}

func newBrowser(t *testing.T, app *HyperChi) *browser {
	return &browser{t: t, app: app, cookies: map[string]*http.Cookie{}}
}

func (b *browser) do(req *http.Request) *httptest.ResponseRecorder {
	b.t.Helper()
	for _, ck := range b.cookies {
		req.AddCookie(&http.Cookie{Name: ck.Name, Value: ck.Value})
	}
	rec := httptest.NewRecorder()
	b.app.ServeHTTP(rec, req)
	for _, ck := range rec.Result().Cookies() {
		if ck.MaxAge < 0 {
			delete(b.cookies, ck.Name)
		} else {
			b.cookies[ck.Name] = ck
		}
	}
	return rec
}

func (b *browser) get(path string) string {
	b.t.Helper()
	rec := b.do(httptest.NewRequest(http.MethodGet, path, nil))
	if rec.Code != http.StatusOK {
		b.t.Fatalf("GET %s = %d: %s", path, rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

// flashApp serves a page that shows .flashes and routes that add them.
func flashApp(t *testing.T) *HyperChi {
	t.Helper()
	app := keyApp(t, "development", testKeyA)
	fsys := fstest.MapFS{
		"templates/show.tmpl": {Data: []byte(`{{define "show"}}{{if .flashes}}{{range .flashes}}[{{.Kind}}:{{.Message}}]{{end}}{{else}}none{{end}}{{end}}`)},
	}
	if err := app.LoadTemplatesFS(fsys, "templates/*.tmpl"); err != nil {
		t.Fatal(err)
	}
	return app
}

func addFlashRoutes(app *HyperChi) {
	app.Get("/", func(c *Context) error { return c.Layout("none").Page("show", nil) })
	app.Get("/fragment", func(c *Context) error { return c.Fragment("show", H{}) })
	app.Post("/save", func(c *Context) error {
		if err := c.Flash("success", "Saved <b>it</b>."); err != nil {
			return err
		}
		return c.Redirect("/")
	})
}

func TestFlashAcrossRedirect(t *testing.T) {
	app := flashApp(t)
	addFlashRoutes(app)
	b := newBrowser(t, app)

	rec := b.do(httptest.NewRequest(http.MethodPost, "/save", nil))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST = %d", rec.Code)
	}
	if ck := b.cookies[flashCookieName]; ck == nil || !ck.HttpOnly || ck.SameSite != http.SameSiteLaxMode || ck.Path != "/" {
		t.Fatalf("flash cookie = %+v", ck)
	}
	if got := b.get("/"); got != "[success:Saved &lt;b&gt;it&lt;/b&gt;.]" {
		t.Fatalf("page after redirect = %q", got)
	}
	if got := b.get("/"); got != "none" {
		t.Fatalf("flash shown twice: %q", got)
	}
	if _, ok := b.cookies[flashCookieName]; ok {
		t.Fatal("flash cookie not cleared after it was read")
	}
}

func TestFlashAcrossHTMXRedirect(t *testing.T) {
	app := flashApp(t)
	addFlashRoutes(app)
	b := newBrowser(t, app)

	req := httptest.NewRequest(http.MethodPost, "/save", nil)
	req.Header.Set("HX-Request", "true")
	rec := b.do(req)
	if rec.Header().Get("HX-Redirect") != "/" {
		t.Fatalf("HX-Redirect = %q", rec.Header().Get("HX-Redirect"))
	}
	frag := httptest.NewRequest(http.MethodGet, "/fragment", nil)
	frag.Header.Set("HX-Request", "true")
	if got := b.do(frag).Body.String(); got != "[success:Saved &lt;b&gt;it&lt;/b&gt;.]" {
		t.Fatalf("fragment = %q", got)
	}
}

func TestFlashesReadOnce(t *testing.T) {
	app := flashApp(t)
	addFlashRoutes(app)
	var first, second []Flash
	app.Get("/read", func(c *Context) error {
		first = c.Flashes()
		second = c.Flashes()
		return c.Text("ok")
	})
	b := newBrowser(t, app)
	b.do(httptest.NewRequest(http.MethodPost, "/save", nil))
	b.get("/read")
	if len(first) != 1 || first[0].Kind != "success" || len(second) != 0 {
		t.Fatalf("first = %v, second = %v", first, second)
	}
	if got := b.get("/"); got != "none" {
		t.Fatalf("flash survived Flashes(): %q", got)
	}
}

// A flash added during a request is for the next one, and unread flashes
// from earlier requests are kept when a new one is added.
func TestFlashIsForTheNextRequest(t *testing.T) {
	app := flashApp(t)
	addFlashRoutes(app)
	app.Post("/again", func(c *Context) error {
		if err := c.Flash("info", "second"); err != nil {
			return err
		}
		return c.Layout("none").Page("show", nil)
	})
	b := newBrowser(t, app)
	b.do(httptest.NewRequest(http.MethodPost, "/save", nil))

	rec := b.do(httptest.NewRequest(http.MethodPost, "/again", nil))
	if got := rec.Body.String(); got != "[success:Saved &lt;b&gt;it&lt;/b&gt;.]" {
		t.Fatalf("render showed %q, want only the earlier flash", got)
	}
	if n := strings.Count(strings.Join(rec.Header().Values("Set-Cookie"), "\n"), flashCookieName+"="); n != 1 {
		t.Fatalf("%d flash Set-Cookie lines, want 1", n)
	}
	if got := b.get("/"); got != "[info:second]" {
		t.Fatalf("next page = %q", got)
	}
}

func TestFlashNoFlashesNoCookie(t *testing.T) {
	app := flashApp(t)
	addFlashRoutes(app)
	rec := newBrowser(t, app).do(httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Body.String() != "none" || len(rec.Header().Values("Set-Cookie")) != 0 {
		t.Fatalf("body %q, Set-Cookie %q", rec.Body.String(), rec.Header().Values("Set-Cookie"))
	}
}

func TestFlashTamperedCookieIgnoredAndCleared(t *testing.T) {
	app := flashApp(t)
	addFlashRoutes(app)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: flashCookieName, Value: "forged"})
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Body.String() != "none" {
		t.Fatalf("forged flash rendered: %q", rec.Body.String())
	}
	cleared := false
	for _, ck := range rec.Result().Cookies() {
		cleared = cleared || (ck.Name == flashCookieName && ck.MaxAge < 0)
	}
	if !cleared {
		t.Fatal("forged flash cookie not cleared")
	}
}

func TestFlashExpires(t *testing.T) {
	app := flashApp(t)
	addFlashRoutes(app)
	clock := newFakeClock(time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC))
	app.clock = clock
	b := newBrowser(t, app)
	b.do(httptest.NewRequest(http.MethodPost, "/save", nil))
	clock.Advance(flashMaxAge + time.Minute)
	if got := b.get("/"); got != "none" {
		t.Fatalf("stale flash shown: %q", got)
	}
}

func TestFlashCookieStaysUnder4KB(t *testing.T) {
	app := flashApp(t)
	addFlashRoutes(app)
	app.Post("/many", func(c *Context) error {
		for i := range 12 {
			msg := fmt.Sprintf("message-%02d-", i) + strings.Repeat("é", 600) // 1200+ bytes, cut to 1024
			if err := c.Flash(strings.Repeat("k", 40), msg); err != nil {
				return err
			}
		}
		return c.Redirect("/")
	})
	app.Post("/count", func(c *Context) error {
		for i := range 12 {
			if err := c.Flash("info", fmt.Sprintf("m%02d", i)); err != nil {
				return err
			}
		}
		return c.Redirect("/")
	})
	var seen []Flash
	app.Get("/read", func(c *Context) error {
		seen = c.Flashes()
		return c.Text("ok")
	})

	b := newBrowser(t, app)
	rec := b.do(httptest.NewRequest(http.MethodPost, "/many", nil))
	for _, line := range rec.Header().Values("Set-Cookie") {
		if len(line) > maxCookieBytes {
			t.Fatalf("Set-Cookie is %d bytes", len(line))
		}
	}
	b.get("/read")
	if len(seen) == 0 || len(seen) >= 12 {
		t.Fatalf("kept %d large flashes, want some but not all", len(seen))
	}
	last := seen[len(seen)-1]
	if !strings.HasPrefix(last.Message, "message-11-") || len(last.Message) > maxFlashMessageBytes || len(last.Kind) != maxFlashKindBytes {
		t.Fatalf("newest flash not kept intact: kind %d bytes, message %q...", len(last.Kind), last.Message[:12])
	}
	if !strings.HasSuffix(last.Message, "é") {
		t.Fatal("message cut inside a character")
	}

	b.do(httptest.NewRequest(http.MethodPost, "/count", nil))
	b.get("/read")
	if len(seen) != maxFlashes || seen[0].Message != "m02" || seen[maxFlashes-1].Message != "m11" {
		t.Fatalf("count bound kept %v", seen)
	}
}

func TestFlashThroughKVSession(t *testing.T) {
	app := flashApp(t)
	if err := app.UseKVStoreMemory("memory"); err != nil {
		t.Fatal(err)
	}
	if err := app.UseSessions(KVSessionConfig{Backend: "memory", InsecureCookies: true}); err != nil {
		t.Fatal(err)
	}
	app.Use(app.SessionMiddleware())
	addFlashRoutes(app)
	b := newBrowser(t, app)

	b.do(httptest.NewRequest(http.MethodPost, "/save", nil))
	if _, ok := b.cookies[flashCookieName]; ok {
		t.Fatal("flash went to a cookie although the request has a session")
	}
	if _, ok := b.cookies["session_id"]; !ok {
		t.Fatal("no session cookie")
	}
	if got := b.get("/"); got != "[success:Saved &lt;b&gt;it&lt;/b&gt;.]" {
		t.Fatalf("page after redirect = %q", got)
	}
	if got := b.get("/"); got != "none" {
		t.Fatalf("session flash shown twice: %q", got)
	}
}
