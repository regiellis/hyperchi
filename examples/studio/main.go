// Driftlamp: the website of a small, fictional game studio, built with
// HyperChi. It is the reference for content-driven marketing sites: games,
// a devlog, a press kit, and careers from embedded Markdown, pre-rendered at
// startup, plus one live form (the newsletter) backed by SQLite.
package main

import (
	"embed"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/regiellis/hyperchi/hyperchi"
	"github.com/regiellis/hyperchi/hyperchi/config"
)

// Content, templates, and static files are compiled into the binary, so a
// deployment needs only the binary and a writable database path, and it runs
// from any working directory.
//
//go:embed content
var contentFS embed.FS

//go:embed templates
var templatesFS embed.FS

//go:embed static
var staticFS embed.FS

// Options are the settings that are not part of the framework's config.
type Options struct {
	// Origin is the public origin ("https://driftlamp.example") used for
	// canonical URLs, Open Graph tags, the Atom feed, and the sitemap.
	Origin string
	// SignupBurst is how many newsletter posts one client may make before
	// the limiter makes it wait; one token returns every RefillEvery.
	SignupBurst int
	RefillEvery time.Duration
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	// ENVIRONMENT=production (or GO_ENV) switches off development behavior:
	// detailed error pages, open CORS, port fallback, and asset revalidation.
	cfg := config.FromEnv()
	origin, err := publicOrigin(os.Getenv("PUBLIC_ORIGIN"), cfg)
	if err != nil {
		return err
	}
	if cfg.Database.Name == config.Default().Database.Name { // DB_PATH unset
		cfg.Database.Name = filepath.Join("data", "studio.db")
		if err := os.MkdirAll("data", 0o750); err != nil {
			return err
		}
	}
	app, err := NewApp(cfg, Options{Origin: origin, SignupBurst: 5, RefillEvery: time.Minute})
	if err != nil {
		return err
	}
	defer func() {
		if err := app.Close(); err != nil {
			log.Printf("close: %v", err)
		}
	}()
	log.Printf("studio: %s mode, canonical origin %s, database %s",
		cfg.Server.Environment, origin, cfg.Database.Name)
	return app.Serve(cfg.Server.Address()) // logs every address it answers on
}

// publicOrigin validates PUBLIC_ORIGIN. Development falls back to localhost on
// the configured port; production must name its real HTTPS origin, because
// canonical URLs and the feed would otherwise point at the wrong host.
func publicOrigin(raw string, cfg *config.Config) (string, error) {
	if raw == "" {
		if cfg.IsProduction() {
			return "", errors.New("PUBLIC_ORIGIN is required in production, e.g. https://driftlamp.example")
		}
		return "http://localhost:" + strconv.Itoa(cfg.Server.Port), nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("PUBLIC_ORIGIN must be an origin without a path, got %q", raw)
	}
	if u.Scheme != "https" && (cfg.IsProduction() || u.Scheme != "http") {
		return "", fmt.Errorf("PUBLIC_ORIGIN must use https in production, got %q", raw)
	}
	return u.Scheme + "://" + u.Host, nil
}

// NewApp builds the whole site. Tests call it with a temporary database.
func NewApp(cfg *config.Config, opts Options) (*hyperchi.HyperChi, error) {
	dev := cfg.IsDevelopment()
	cfg.Server.PortFallback = dev // production binds exactly the requested port
	cfg.Assets.HotReload = false  // Air restarts the process; no asset watcher
	// Pages are pre-rendered once at startup, so they cannot carry a
	// per-request CSRF token, and the signup form must work without script.
	// The newsletter route is guarded by ProtectForm's origin check instead.
	cfg.Security.CSRF = false
	if cfg.Security.CSPPolicy == "" {
		// The hc-* directives compile expressions with new Function, so
		// scripts need 'unsafe-eval'. Nothing else is loosened: no inline
		// scripts or styles, no third-party origins.
		cfg.Security.CSPPolicy = "default-src 'self'; script-src 'self' 'unsafe-eval'; style-src 'self'; " +
			"img-src 'self' data:; font-src 'self'; connect-src 'self'; object-src 'none'; " +
			"base-uri 'none'; frame-ancestors 'none'; form-action 'self'"
	}
	app, err := hyperchi.NewWithConfigE(cfg)
	if err != nil {
		return nil, err
	}
	if err := build(app, dev, opts); err != nil {
		return nil, errors.Join(err, app.Close())
	}
	return app, nil
}

// build installs the middleware, mounts the static files, loads the content,
// then registers templates and routes on app. Static files come before the
// content and pages: the asset template function and NewSite's image checks
// read the mount.
func build(app *hyperchi.HyperChi, dev bool, opts Options) error {
	app.AutoMiddleware() // request IDs, client IP from the TCP peer, recovery, logging, security headers
	if err := mountStatic(app, staticFS, "static", dev); err != nil {
		return err
	}
	lib, err := LoadLibrary(app.Content, contentFS)
	if err != nil {
		return err
	}
	site, err := NewSite(lib, app.AssetURL, strings.TrimSuffix(opts.Origin, "/"))
	if err != nil {
		return err
	}
	if err := app.LoadTemplatesFS(templatesFS, "templates/*.tmpl"); err != nil {
		return err
	}
	app.SetGlobal("site", site.Globals())
	if err := app.Migrate(subscribersTable); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	if err := site.Routes(app, dev); err != nil {
		return err
	}
	return newNewsletter(app, site, opts).Routes()
}
