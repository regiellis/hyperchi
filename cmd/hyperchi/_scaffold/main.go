package main

import (
	"log"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/regiellis/hyperchi/hyperchi"
	"github.com/regiellis/hyperchi/hyperchi/config"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	// ENVIRONMENT=production switches off development behavior (relaxed
	// WebSocket origins, open CORS, detailed errors); set it in every deployment.
	cfg := config.FromEnv()
	cfg.Database.Disabled = true // set to false, and DB_PATH, when the app needs SQLite
	app, err := hyperchi.NewWithConfigE(cfg)
	if err != nil {
		return err
	}
	defer func() {
		if err := app.Close(); err != nil {
			log.Printf("close: %v", err)
		}
	}()
	app.AutoMiddleware()
	if err := app.LoadTemplates(filepath.Join("templates", "**", "*.tmpl")); err != nil {
		return err
	}

	// Values every template sees, set once instead of in every handler.
	app.SetGlobal("development", cfg.IsDevelopment())
	app.SetGlobal("vite_dev_server", envOr("VITE_DEV_SERVER", "http://localhost:3000"))

	// Static assets: the Vite build and vendored runtime scripts.
	if err := os.MkdirAll("dist", 0o755); err != nil {
		return err
	}
	for pattern, dir := range map[string]string{"/dist/*": "dist", "/static/*": "static"} {
		if err := app.StaticWithOptions(pattern, dir, hyperchi.StaticOptions{Development: cfg.IsDevelopment()}); err != nil {
			return err
		}
	}

	// Pages. View renders the full page for a normal or boosted navigation and
	// only the template for an htmx partial request.
	app.Get("/", func(c *hyperchi.Context) error {
		return c.View("index", hyperchi.H{"title": "HyperChi", "current": "home"})
	})
	app.Get("/docs/{slug}", docsPage)

	// An htmx fragment endpoint, demonstrated on the /docs/htmx page.
	app.Get("/api/time", func(c *hyperchi.Context) error {
		return c.Fragment("time", hyperchi.H{"now": time.Now().Format("Mon, 02 Jan 2006 15:04:05 MST")})
	})

	// The Go server is the address to open. `task dev` also runs Vite on
	// :3000, which only serves assets.
	log.Print("HyperChi: open the Go server address below, not Vite at :3000")
	return app.Serve(net.JoinHostPort(envOr("HOST", "0.0.0.0"), envOr("PORT", "8080")))
}

// docs maps each /docs/{slug} page to its title. A slug not listed here is a
// 404, so a request can never name an arbitrary template.
var docs = map[string]string{
	"directives":       "Directives",
	"htmx":             "HTMX",
	"view-transitions": "View Transitions",
	"deploy":           "Deploy",
}

func docsPage(c *hyperchi.Context) error {
	slug := c.Param("slug")
	title, ok := docs[slug]
	if !ok {
		return hyperchi.NotFound("No such page.")
	}
	return c.View("docs-"+slug, hyperchi.H{"title": title + " | HyperChi", "current": slug})
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
