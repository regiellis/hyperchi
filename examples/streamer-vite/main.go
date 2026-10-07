// Command streamer-vite is the Streamer podcast catalog with Vite serving its
// JS and CSS and a HyperChi KV store holding the podcasts (bbolt, or memory
// with KV_BACKEND=memory).
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/regiellis/hyperchi/hyperchi"
	"github.com/regiellis/hyperchi/hyperchi/config"
	"github.com/regiellis/hyperchi/hyperchi/helpers"
)

// PodcastResponse is one episode, stored in the KV store as JSON.
type PodcastResponse struct {
	ID          int                 `json:"id"`
	Date        string              `json:"date"`
	Title       string              `json:"title"`
	Category    string              `json:"category"`
	Duration    string              `json:"duration"`
	ImagePath   string              `json:"image_path"`
	Description string              `json:"description"`
	Topics      []string            `json:"topics,omitempty"`
	Sponsors    []map[string]string `json:"sponsors,omitempty"`
	Links       []map[string]string `json:"links,omitempty"`
	ListenOn    []map[string]string `json:"listen_on,omitempty"`
	Transcript  []map[string]string `json:"transcript,omitempty"`
}

func main() {
	// Configuration from the environment: ENVIRONMENT=production switches off
	// development behavior (detailed error pages, open CORS). Podcasts live in a
	// KV store, so the app opens no SQLite database.
	cfg := config.FromEnv()
	cfg.Database.Disabled = true
	app, err := hyperchi.NewWithConfigE(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	// Load .env values for the settings read below (PORT, HOST, KV_BACKEND, ...)
	helpers.Must(app.LoadEnv())

	if app.IsProduction() {
		helpers.SetupProductionLogging()
	} else {
		helpers.SetupDevelopmentLogging()
	}

	app.Helpers.Logger.Info("🚀 Starting %s", app.Helpers.Env.GetOr("APP_NAME", "HyperChi Streamer + Vite"))

	if err := setupKVStore(app); err != nil {
		app.Helpers.Logger.Error("KV store setup failed: %v", err)
		os.Exit(1)
	}

	// ENABLE_AIR=true writes .air.toml and dev.sh when the project has no Air
	// configuration yet. This example ships both, so InitAir reports that and
	// the dev script is left alone.
	if app.Helpers.Env.GetBool("ENABLE_AIR") {
		if err := app.Helpers.Air.InitAir(); err != nil {
			app.Helpers.Logger.Warn("Air setup skipped: %v", err)
		} else if err := app.Helpers.Air.CreateDevScript(); err != nil {
			app.Helpers.Logger.Warn("Air dev script not written: %v", err)
		} else {
			app.Helpers.Logger.Info("✨ Air live reload configuration created")
		}
	}

	helpers.Must(app.LoadTemplatesWithLayout())

	app.Use(app.Middleware.Recovery())
	app.Use(app.Middleware.Logger())
	app.Use(app.Middleware.HTMX())
	app.Use(app.Middleware.CORS())

	autoVite := setupGlobals(app)
	if err := setupRoutes(app, autoVite); err != nil {
		app.Helpers.Logger.Error("Route setup failed: %v", err)
		os.Exit(1)
	}

	port := app.Helpers.Env.GetOr("PORT", "8080")
	host := app.Helpers.Env.GetOr("HOST", "0.0.0.0") // Listen on all interfaces so LAN and SSH clients can reach it
	addr := host + ":" + port

	app.Helpers.Logger.Info("🎧 HyperChi Streamer + Vite starting on %s", addr)
	app.Helpers.Logger.Info("📡 HTMX enhanced interactions enabled")
	app.Helpers.Logger.Info("🎨 Swiss design aesthetics maintained")
	app.Helpers.Logger.Info("💾 KV store-powered podcast platform")
	app.Helpers.Logger.Info("⚡ Vite frontend tooling integration")

	if err := app.Serve(addr); err != nil {
		app.Helpers.Logger.Error("Server stopped: %v", err)
		os.Exit(1)
	}
}

// setupKVStore registers the "podcasts" KV store (bbolt at ./data/podcasts.db
// unless KV_BACKEND=memory) and seeds it when it is empty or cannot be counted.
func setupKVStore(app *hyperchi.HyperChi) error {
	app.Helpers.Logger.Info("🗃️  Setting up KV store...")

	backend := app.Helpers.Env.GetOr("KV_BACKEND", "bbolt")

	if backend == "memory" {
		app.Helpers.Logger.Info("📦 Using in-memory KV store (development mode)")
		if err := app.UseKVStoreMemory("podcasts"); err != nil {
			return err
		}
	} else {
		app.Helpers.Logger.Info("📦 Using BBolt KV store (persistent storage)")
		// bbolt creates the file but not its directory, and git does not keep an empty data/.
		if err := os.MkdirAll("data", 0o750); err != nil {
			return fmt.Errorf("create data directory: %w", err)
		}
		if err := app.UseKVStoreBBolt("podcasts", "./data/podcasts.db", "podcasts"); err != nil {
			return err
		}
	}

	podcastStore := NewPodcastStore(app)

	count, err := podcastStore.Count()
	app.Helpers.Logger.Info("🔍 KV store count check: count=%d, err=%v", count, err)

	if err != nil || count == 0 {
		app.Helpers.Logger.Info("📥 Starting KV store seeding...")
		podcasts, err := getSeedData()
		if err != nil {
			return err
		}
		if err := podcastStore.Seed(podcasts); err != nil {
			return err
		}
		app.Helpers.Logger.Info("✅ KV store ready with %d podcasts (seeded)", len(podcasts))
		return nil
	}

	app.Helpers.Logger.Info("✅ KV store ready with %d podcasts", count)
	return nil
}

// setupGlobals sets the template values every page needs once, at startup,
// so handlers never repeat them. VITE_DEV_SERVER picks where the layout loads
// JS and CSS from:
//
//	empty    = pre-built /dist/ assets (no Vite needed)
//	"auto"   = derived per request from the browser's host, port 3000
//	full URL = used as-is (e.g., http://192.168.1.5:3000)
//
// In development, open the Go server on :8080; Vite on :3000 serves only JS
// and CSS, with HMR. setupGlobals returns true in "auto" mode, where
// vite_dev_server depends on the request and cannot be a global.
func setupGlobals(app *hyperchi.HyperChi) bool {
	viteDevServer := app.Helpers.Env.GetOr("VITE_DEV_SERVER", "")
	app.SetGlobal("development", viteDevServer != "")

	if viteDevServer == "auto" {
		return true
	}
	app.SetGlobal("vite_dev_server", viteDevServer)
	return false
}

// setupRoutes registers the page, the htmx fragments, the JSON endpoints,
// static files, and the health routes. It fails when the built dist/ assets
// are missing and the pages would load them: run npm run build first, or set
// VITE_DEV_SERVER so Vite serves them.
func setupRoutes(app *hyperchi.HyperChi, autoVite bool) error {
	podcastStore := NewPodcastStore(app)

	// Register custom animation preset for podcast cards
	hyperchi.RegisterPreset("podcastCardEnter", func(targets string) *hyperchi.AnimationConfig {
		return hyperchi.NewAnimation(targets).
			FadeIn().
			Set("translateY", []int{30, 0}).
			SetDuration(600).
			SetEasing("easeOutCubic")
	})

	// Main page - shows podcast list
	app.Get("/", homeHandler(app, podcastStore, autoVite))

	// HTMX endpoints for dynamic content
	app.Get("/podcast/{id}/details", podcastDetailsHandler(app, podcastStore))
	app.Get("/podcast/{id}/modal", podcastModalHandler(app, podcastStore))

	// JSON endpoints
	app.Get("/api/search", searchHandler(app, podcastStore))
	app.Get("/admin/stats", statsHandler(app, podcastStore))

	// Static files. The layout links them with {{asset}}, so they get content
	// versions and long-lived caching. With Vite serving the JS and CSS in
	// development, a missing dist/ only warrants a warning.
	app.Static("/static/*", "static")
	err := app.StaticWithOptions("/dist/*", "dist", hyperchi.StaticOptions{Development: !app.IsProduction()})
	if err != nil {
		if app.Helpers.Env.GetOr("VITE_DEV_SERVER", "") == "" {
			return fmt.Errorf("dist/ is missing and VITE_DEV_SERVER is unset; run npm run build or start Vite: %w", err)
		}
		app.Helpers.Logger.Warn("No dist/ directory; pages load their assets from Vite: %v", err)
	}

	// /health, /health/ready, and /health/live
	app.CreateHealthEndpoints()
	return nil
}

// viteServerForHost builds the Vite dev-server URL from the host the browser
// used, so the page works from remote machines hitting the dev server.
func viteServerForHost(app *hyperchi.HyperChi, host string) string {
	if colonIndex := helpers.S(host).Index(":"); colonIndex >= 0 {
		host = helpers.S(host).Left(colonIndex).String()
	}
	return "http://" + host + ":3000"
}

// homeHandler renders the podcast list, newest first.
func homeHandler(app *hyperchi.HyperChi, store *PodcastStore, autoVite bool) hyperchi.Handler {
	return func(c *hyperchi.Context) error {
		podcasts, err := store.List()
		if err != nil {
			return hyperchi.Errorf(500, "Failed to load podcasts: %w", err)
		}

		app.Helpers.Logger.Info("📊 Loaded %d podcasts for template rendering", len(podcasts))

		data := hyperchi.H{
			"title":       "Streamer + Vite",
			"description": "Swiss Design Podcast Platform - Powered by HyperChi + Vite",
			"podcasts":    podcasts,
			"total_count": len(podcasts),
		}
		if autoVite {
			data["vite_dev_server"] = viteServerForHost(app, c.Request().Host)
		}

		return c.View("index", data)
	}
}

// podcastDetailsHandler renders the details fragment for one podcast.
func podcastDetailsHandler(app *hyperchi.HyperChi, store *PodcastStore) hyperchi.Handler {
	return func(c *hyperchi.Context) error {
		id := c.Param("id")
		if helpers.S(id).IsEmpty() {
			return hyperchi.BadRequest("Missing podcast ID")
		}

		podcast, err := store.Get(id)
		if err != nil {
			app.Helpers.Logger.Error("Failed to fetch podcast %s: %v", id, err)
			return hyperchi.NotFound("Podcast not found").Wrap(err)
		}

		detailsAnim := hyperchi.AnimationPresets.ComponentSlideIn("#podcast-details-" + id)

		return c.Fragment("podcast-details", helpers.NewJSON().
			Set("podcast", podcast).
			Set("detailsAnimation", detailsAnim.ToHTMLAttr()))
	}
}

// podcastModalHandler renders the modal fragment for one podcast.
func podcastModalHandler(app *hyperchi.HyperChi, store *PodcastStore) hyperchi.Handler {
	return func(c *hyperchi.Context) error {
		id := c.Param("id")
		if helpers.S(id).IsEmpty() {
			return hyperchi.BadRequest("Missing podcast ID")
		}

		podcast, err := store.Get(id)
		if err != nil {
			app.Helpers.Logger.Error("Failed to fetch podcast %s: %v", id, err)
			return hyperchi.NotFound("Podcast not found").Wrap(err)
		}

		return c.Fragment("podcast-modal", helpers.NewJSON().
			Set("podcast", podcast).
			Set("modal_id", "modal-"+id).
			Set("modalAnimation", hyperchi.AnimeData("pageSlideInFromTop")))
	}
}

// searchHandler answers a search as JSON.
func searchHandler(app *hyperchi.HyperChi, store *PodcastStore) hyperchi.Handler {
	return func(c *hyperchi.Context) error {
		query := c.Query("q")

		// An empty query returns every podcast.
		podcasts, err := store.Search(query)
		if err != nil {
			return hyperchi.Errorf(500, "Search failed: %w", fmt.Errorf("search for %q: %w", query, err))
		}

		return c.JSON(helpers.NewJSON().
			Set("query", query).
			Set("results", podcasts).
			Set("count", len(podcasts)).
			Set("search_term", helpers.S(query).Trim().Lower().String()))
	}
}

// statsHandler answers podcast counts by category as JSON.
func statsHandler(app *hyperchi.HyperChi, store *PodcastStore) hyperchi.Handler {
	return func(c *hyperchi.Context) error {
		stats, err := store.Stats()
		if err != nil {
			return hyperchi.Errorf(500, "Failed to generate statistics: %w", err)
		}

		return c.JSON(helpers.NewJSON().
			Set("title", "Streamer + Vite Platform Statistics").
			Set("podcast_count", (*stats)["total"]).
			Set("categories", (*stats)["by_category"]).
			Set("environment", helpers.NewJSON().
				Set("app_name", app.Helpers.Env.GetOr("APP_NAME", "HyperChi Streamer + Vite")).
				Set("version", app.Helpers.Env.GetOr("APP_VERSION", "2.0.0")).
				Set("framework", "HyperChi v2 with KV Store + Vite")).
			Set("features", helpers.NewSlice(
				"KV store-powered content",
				"HTMX interactions",
				"Vite frontend tooling",
				"Swiss design aesthetics",
				"Performance optimization")))
	}
}

// getSeedData returns the built-in catalog, or an error naming the first
// entry that does not decode.
func getSeedData() ([]PodcastResponse, error) {
	// The list fields are written as JSON strings. decodeSeed parses them.
	podcastsRaw := []map[string]any{
		{
			"date":        "2025-01-01",
			"title":       "Armin Hofmann — Mastering Visual Balance",
			"category":    "Typography",
			"duration":    "01:50h",
			"image_path":  "/static/images/1.png",
			"description": "Explore the life and teachings of Armin Hofmann, the pioneer of Swiss typography and his approach to visual balance and clarity.",
			"topics":      `["The impact of Armin Hofmann on Swiss typography.", "Understanding visual balance in minimalistic design.", "How Hofmann's teachings remain relevant in modern design.", "Typography as the backbone of communication design."]`,
			"sponsors":    `[{"name": "Grid Masters", "description": "Your go-to resource for advanced design grids and templates."}, {"name": "SwissType Foundry", "description": "Discover premium typefaces inspired by Swiss design."}]`,
			"links":       `[{"label": "Learn more about Armin Hofmann", "url": "#"}, {"label": "Typography Principles", "url": "#"}]`,
			"listen_on":   `[{"name": "Spotify", "url": "#"}, {"name": "Apple Podcast", "url": "#"}, {"name": "Overcast", "url": "#"}]`,
			"transcript":  `[{"speaker": "Host", "text": "Welcome to this episode where we discuss Armin Hofmann's influence on modern design."}, {"speaker": "Guest", "text": "Hofmann's principles of balance and structure are timeless."}]`,
		},
		{
			"date":        "2025-01-08",
			"title":       "Josef Müller-Brockmann — The Grid as a Design Tool",
			"category":    "Grids",
			"duration":    "02:15h",
			"image_path":  "/static/images/2.png",
			"description": "A deep dive into Josef Müller-Brockmann's philosophy of the grid system and its transformative impact on design.",
			"topics":      `["The evolution of the grid system in Swiss design.", "Why the grid is a tool for clarity and creativity.", "Josef Müller-Brockmann's most iconic works."]`,
			"sponsors":    `[{"name": "The Grid Co.", "description": "Modern tools for grid-based design workflows."}, {"name": "TypoSwiss", "description": "Explore typefaces built with the Swiss grid in mind."}]`,
			"links":       `[{"label": "Josef Müller-Brockmann's legacy", "url": "#"}, {"label": "The Grid as a System", "url": "#"}]`,
			"listen_on":   `[{"name": "Spotify", "url": "#"}, {"name": "Apple Podcast", "url": "#"}, {"name": "Overcast", "url": "#"}]`,
			"transcript":  `[{"speaker": "Host", "text": "Today, we explore how the grid became a design revolution..."}]`,
		},
		{
			"date":        "2025-01-15",
			"title":       "The Swiss Design Movement — Simplicity and Functionality",
			"category":    "History",
			"duration":    "02:00h",
			"image_path":  "/static/images/3.png",
			"description": "Unpacking the Swiss design movement's emphasis on simplicity, functionality, and clarity.",
			"topics":      `["What defines Swiss design aesthetics?", "The role of neutrality and functionality in design.", "Swiss design beyond Switzerland: Its global influence."]`,
			"sponsors":    `[{"name": "Neutral Spaces", "description": "Creating designs that speak without excess."}]`,
			"links":       `[{"label": "Swiss design history", "url": "#"}, {"label": "Neutral design philosophy", "url": "#"}]`,
			"listen_on":   `[{"name": "Spotify", "url": "#"}, {"name": "Apple Podcast", "url": "#"}, {"name": "Overcast", "url": "#"}]`,
			"transcript":  `[{"speaker": "Host", "text": "Swiss design is a testament to the beauty of simplicity..."}]`,
		},
		{
			"date":        "2025-01-22",
			"title":       "Emil Ruder — Precision in Typography",
			"category":    "Typography",
			"duration":    "01:45h",
			"image_path":  "/static/images/4.png",
			"description": "Discover Emil Ruder's unique contributions to Swiss typography and his philosophy on precision and order.",
			"topics":      `["Ruder's influence on modern typography.", "Why precision matters in communication.", "Breaking rules: When it's acceptable in typography."]`,
			"sponsors":    `[{"name": "TypeCraft", "description": "Perfect tools for crafting precise typographic designs."}]`,
			"links":       `[{"label": "Read more about Emil Ruder", "url": "#"}]`,
			"listen_on":   `[{"name": "Spotify", "url": "#"}, {"name": "Apple Podcast", "url": "#"}, {"name": "Overcast", "url": "#"}]`,
			"transcript":  `[{"speaker": "Host", "text": "Emil Ruder was a genius of clarity and precision in design..."}]`,
		},
		{
			"date":        "2025-01-29",
			"title":       "Swiss Design in Digital Spaces",
			"category":    "Digital Design",
			"duration":    "01:30h",
			"image_path":  "/static/images/5.png",
			"description": "How Swiss design principles have transitioned into the digital era while maintaining their core values.",
			"topics":      `["The challenges of adapting Swiss design to digital media.", "Maintaining clarity and simplicity in a digital-first world."]`,
			"sponsors":    `[{"name": "Digital Grids", "description": "Helping you design for the digital age with precision."}]`,
			"links":       `[{"label": "Swiss design for digital media", "url": "#"}]`,
			"listen_on":   `[{"name": "Spotify", "url": "#"}, {"name": "Apple Podcast", "url": "#"}, {"name": "Overcast", "url": "#"}]`,
			"transcript":  `[{"speaker": "Host", "text": "Digital spaces have opened new avenues for Swiss design..."}]`,
		},
		{
			"date":        "2025-02-05",
			"title":       "Max Bill — The Art of Clarity",
			"category":    "Art and Design",
			"duration":    "02:00h",
			"image_path":  "/static/images/6.png",
			"description": "Exploring Max Bill's contributions to art and design, focusing on his relentless pursuit of clarity.",
			"topics":      `["Max Bill's multidisciplinary approach to design.", "Art as an extension of clarity and logic.", "Why clarity remains at the heart of effective design."]`,
			"sponsors":    `[{"name": "Clarity Collective", "description": "Simplify your designs with tools inspired by Max Bill."}]`,
			"links":       `[{"label": "Max Bill's portfolio", "url": "#"}]`,
			"listen_on":   `[{"name": "Spotify", "url": "#"}, {"name": "Apple Podcast", "url": "#"}, {"name": "Overcast", "url": "#"}]`,
			"transcript":  `[{"speaker": "Host", "text": "Max Bill taught us that art and design can coexist beautifully..."}]`,
		},
		{
			"date":        "2025-02-12",
			"title":       "The Role of White Space in Swiss Design",
			"category":    "Minimalism",
			"duration":    "01:20h",
			"image_path":  "/static/images/7.png",
			"description": "An exploration of how Swiss designers use white space to emphasize clarity and simplicity in their work.",
			"topics":      `["What is white space, and why does it matter?", "How Swiss design masters incorporated white space into their layouts.", "Balancing white space with content density."]`,
			"sponsors":    `[{"name": "Whitespace Studio", "description": "Tools for mastering the art of minimalism in design."}]`,
			"links":       `[{"label": "Learn more about white space in design", "url": "#"}]`,
			"listen_on":   `[{"name": "Spotify", "url": "#"}, {"name": "Apple Podcast", "url": "#"}, {"name": "Overcast", "url": "#"}]`,
			"transcript":  `[{"speaker": "Host", "text": "White space is often misunderstood but vital in design..."}]`,
		},
		{
			"date":        "2025-02-19",
			"title":       "Grid-Based Typography: Practical Techniques",
			"category":    "Typography",
			"duration":    "01:40h",
			"image_path":  "/static/images/8.png",
			"description": "Learn how to use grid systems to create visually stunning and readable typography.",
			"topics":      `["Combining typography and grids for better communication.", "Best practices for setting typographic hierarchy.", "Examples of effective grid-based typographic designs."]`,
			"sponsors":    `[{"name": "FontGrid Pro", "description": "Grids tailored for typographic design."}]`,
			"links":       `[{"label": "Typography and grid systems", "url": "#"}]`,
			"listen_on":   `[{"name": "Spotify", "url": "#"}, {"name": "Apple Podcast", "url": "#"}, {"name": "Overcast", "url": "#"}]`,
			"transcript":  `[{"speaker": "Host", "text": "Grids are an essential tool in achieving typographic clarity..."}]`,
		},
		{
			"date":        "2025-02-26",
			"title":       "Swiss Design in Branding",
			"category":    "Branding",
			"duration":    "01:50h",
			"image_path":  "/static/images/9.png",
			"description": "Discover how Swiss design principles can create impactful and timeless brands.",
			"topics":      `["Why Swiss design is the gold standard in branding.", "Case studies of brands inspired by Swiss design.", "How to apply Swiss design concepts to your brand identity."]`,
			"sponsors":    `[{"name": "BrandSwiss", "description": "Elevate your brand with Swiss-inspired design tools."}]`,
			"links":       `[{"label": "Swiss design and branding", "url": "#"}]`,
			"listen_on":   `[{"name": "Spotify", "url": "#"}, {"name": "Apple Podcast", "url": "#"}, {"name": "Overcast", "url": "#"}]`,
			"transcript":  `[{"speaker": "Host", "text": "Branding is one of the key areas where Swiss design thrives..."}]`,
		},
		{
			"date":        "2025-03-11",
			"title":       "Swiss Poster Design — Bold and Beautiful",
			"category":    "Graphic Design",
			"duration":    "01:35h",
			"image_path":  "/static/images/10.png",
			"description": "Explore the striking visual style of Swiss posters and how they combine bold typography with clean layouts.",
			"topics":      `["Key features of Swiss poster design.", "How to create a visually impactful poster using Swiss principles.", "Famous poster designs that defined the era."]`,
			"sponsors":    `[{"name": "PosterCraft", "description": "Tools for creating striking posters inspired by Swiss design."}]`,
			"links":       `[{"label": "Swiss poster design history", "url": "#"}]`,
			"listen_on":   `[{"name": "Spotify", "url": "#"}, {"name": "Apple Podcast", "url": "#"}, {"name": "Overcast", "url": "#"}]`,
			"transcript":  `[{"speaker": "Host", "text": "Swiss poster design is the epitome of clarity and boldness..."}]`,
		},
		{
			"date":        "2025-03-18",
			"title":       "Adrian Frutiger — Typography in Motion",
			"category":    "Typography",
			"duration":    "02:10h",
			"image_path":  "/static/images/11.png",
			"description": "Discover Adrian Frutiger's contributions to modern typography, including the creation of Univers and Frutiger typefaces.",
			"topics":      `["The genius of Adrian Frutiger.", "How Univers changed the typographic landscape.", "Frutiger's approach to legibility and functionality.", "Typography for wayfinding and signage systems."]`,
			"sponsors":    `[{"name": "TypeFounders", "description": "Premium typefaces for modern design projects."}, {"name": "SignSystems Pro", "description": "Professional wayfinding and signage solutions."}]`,
			"links":       `[{"label": "Adrian Frutiger biography", "url": "#"}, {"label": "Univers typeface history", "url": "#"}]`,
			"listen_on":   `[{"name": "Spotify", "url": "#"}, {"name": "Apple Podcast", "url": "#"}, {"name": "Overcast", "url": "#"}]`,
			"transcript":  `[{"speaker": "Host", "text": "Adrian Frutiger revolutionized how we think about typography in motion..."}, {"speaker": "Guest", "text": "His work on airport signage at Charles de Gaulle was groundbreaking for its clarity and functionality."}]`,
		},
		{
			"date":        "2025-03-25",
			"title":       "Color Theory in Swiss Design",
			"category":    "Color Theory",
			"duration":    "01:25h",
			"image_path":  "/static/images/12.png",
			"description": "Understanding how Swiss designers approach color: restraint, purpose, and psychological impact in minimal palettes.",
			"topics":      `["The philosophy of minimal color palettes.", "How Swiss designers choose and apply color.", "Color psychology in neutral design.", "When to break the rules: Strategic use of bold colors."]`,
			"sponsors":    `[{"name": "ColorSync Studio", "description": "Professional color management for designers."}, {"name": "Neutral Palette", "description": "Curated color systems for minimal design."}]`,
			"links":       `[{"label": "Swiss design color principles", "url": "#"}, {"label": "Color psychology guide", "url": "#"}]`,
			"listen_on":   `[{"name": "Spotify", "url": "#"}, {"name": "Apple Podcast", "url": "#"}, {"name": "Overcast", "url": "#"}]`,
			"transcript":  `[{"speaker": "Host", "text": "Color in Swiss design isn't about restriction—it's about intention and impact..."}, {"speaker": "Expert", "text": "When you limit your palette, every color choice becomes more meaningful and powerful."}]`,
		},
		{
			"date":        "2025-04-01",
			"title":       "The Bauhaus Influence on Swiss Design",
			"category":    "History",
			"duration":    "02:05h",
			"image_path":  "/static/images/13.png",
			"description": "Exploring the historical connections between the Bauhaus movement and the development of Swiss design principles.",
			"topics":      `["How Bauhaus principles influenced Swiss design.", "Key figures who bridged both movements.", "Form follows function: A shared philosophy.", "The migration of ideas from Germany to Switzerland."]`,
			"sponsors":    `[{"name": "Design History Archive", "description": "Preserving the legacy of design movements."}, {"name": "Bauhaus Revival", "description": "Modern interpretations of classical design principles."}]`,
			"links":       `[{"label": "Bauhaus and Swiss design connections", "url": "#"}, {"label": "Design movement timeline", "url": "#"}]`,
			"listen_on":   `[{"name": "Spotify", "url": "#"}, {"name": "Apple Podcast", "url": "#"}, {"name": "Overcast", "url": "#"}]`,
			"transcript":  `[{"speaker": "Host", "text": "The Bauhaus didn't just influence architecture—it fundamentally shaped how Swiss designers approach visual problems..."}, {"speaker": "Historian", "text": "Many Swiss designers studied under former Bauhaus masters, creating a direct lineage of ideas."}]`,
		},
		{
			"date":        "2025-04-08",
			"title":       "Swiss Typography in Corporate Identity",
			"category":    "Branding",
			"duration":    "01:55h",
			"image_path":  "/static/images/14.png",
			"description": "How Swiss typography principles became the foundation for modern corporate identity systems worldwide.",
			"topics":      `["The rise of systematic corporate design.", "Why Swiss typography works for global brands.", "Case studies: IBM, Lufthansa, and other iconic identities.", "Building timeless brand systems."]`,
			"sponsors":    `[{"name": "Corporate Identity Systems", "description": "Professional brand development using Swiss principles."}, {"name": "SystemsThinking Design", "description": "Comprehensive brand architecture solutions."}]`,
			"links":       `[{"label": "Swiss corporate design examples", "url": "#"}, {"label": "Brand system guidelines", "url": "#"}]`,
			"listen_on":   `[{"name": "Spotify", "url": "#"}, {"name": "Apple Podcast", "url": "#"}, {"name": "Overcast", "url": "#"}]`,
			"transcript":  `[{"speaker": "Host", "text": "Swiss design principles gave birth to the modern corporate identity as we know it..."}, {"speaker": "Brand Expert", "text": "Companies realized that systematic, clear communication was essential for global recognition."}]`,
		},
		{
			"date":        "2025-04-15",
			"title":       "Photography and Swiss Layout",
			"category":    "Photography",
			"duration":    "01:40h",
			"image_path":  "/static/images/15.png",
			"description": "The integration of photography within Swiss design layouts: composition, hierarchy, and visual storytelling.",
			"topics":      `["How Swiss designers approach photographic composition.", "The relationship between type and image.", "Grid systems for photo layouts.", "Documentary photography in Swiss design context."]`,
			"sponsors":    `[{"name": "GridPhoto Studio", "description": "Photography tools designed for systematic layouts."}, {"name": "Swiss Visual Co.", "description": "Professional photography with Swiss design sensibilities."}]`,
			"links":       `[{"label": "Photography in Swiss design", "url": "#"}, {"label": "Layout composition techniques", "url": "#"}]`,
			"listen_on":   `[{"name": "Spotify", "url": "#"}, {"name": "Apple Podcast", "url": "#"}, {"name": "Overcast", "url": "#"}]`,
			"transcript":  `[{"speaker": "Host", "text": "Photography in Swiss design isn't just illustration—it's an integral part of the communication system..."}, {"speaker": "Photographer", "text": "The grid doesn't limit photographic creativity; it enhances the relationship between image and message."}]`,
		},
		{
			"date":        "2025-04-22",
			"title":       "Swiss Design Education Methods",
			"category":    "Education",
			"duration":    "02:00h",
			"image_path":  "/static/images/16.png",
			"description": "Understanding how Swiss design schools taught and continue to teach systematic design thinking and visual problem-solving.",
			"topics":      `["The Basel School of Design methodology.", "Teaching systematic thinking in design.", "Exercise-based learning in Swiss design education.", "How to apply Swiss educational principles today."]`,
			"sponsors":    `[{"name": "Design Education Platform", "description": "Online courses based on Swiss design methodology."}, {"name": "SystemsLearning Co.", "description": "Educational tools for design thinking."}]`,
			"links":       `[{"label": "Swiss design education history", "url": "#"}, {"label": "Design thinking exercises", "url": "#"}]`,
			"listen_on":   `[{"name": "Spotify", "url": "#"}, {"name": "Apple Podcast", "url": "#"}, {"name": "Overcast", "url": "#"}]`,
			"transcript":  `[{"speaker": "Host", "text": "Swiss design education emphasized process over style, method over aesthetics..."}, {"speaker": "Educator", "text": "Students learned to think systematically about visual problems before touching pen to paper."}]`,
		},
		{
			"date":        "2025-04-29",
			"title":       "Minimalism vs. Swiss Design",
			"category":    "Philosophy",
			"duration":    "01:30h",
			"image_path":  "/static/images/17.png",
			"description": "Distinguishing between minimalism as an aesthetic choice and Swiss design as a systematic approach to communication.",
			"topics":      `["What makes Swiss design different from minimalism?", "The functional purpose behind Swiss 'simplicity'.", "When minimalism goes wrong vs. Swiss design principles.", "Understanding systematic vs. aesthetic approaches."]`,
			"sponsors":    `[{"name": "Purpose-Driven Design", "description": "Tools that prioritize function over aesthetics."}, {"name": "Systematic Studio", "description": "Design systems that solve real communication problems."}]`,
			"links":       `[{"label": "Swiss design vs. minimalism comparison", "url": "#"}, {"label": "Functional design principles", "url": "#"}]`,
			"listen_on":   `[{"name": "Spotify", "url": "#"}, {"name": "Apple Podcast", "url": "#"}, {"name": "Overcast", "url": "#"}]`,
			"transcript":  `[{"speaker": "Host", "text": "Swiss design is often confused with minimalism, but they serve very different purposes..."}, {"speaker": "Design Critic", "text": "Minimalism can be about aesthetics; Swiss design is always about clear communication."}]`,
		},
		{
			"date":        "2025-05-06",
			"title":       "Digital Swiss Design: Web and UI",
			"category":    "Digital Design",
			"duration":    "01:45h",
			"image_path":  "/static/images/18.png",
			"description": "Adapting Swiss design principles for modern digital interfaces, responsive design, and user experience.",
			"topics":      `["Translating grid systems to responsive web design.", "Swiss typography principles for digital screens.", "User interface design with Swiss clarity.", "Challenges of interactive media vs. static design."]`,
			"sponsors":    `[{"name": "Digital Grid Systems", "description": "Responsive grid frameworks inspired by Swiss design."}, {"name": "SwissUI Kit", "description": "Interface components built on Swiss design principles."}]`,
			"links":       `[{"label": "Swiss design for digital interfaces", "url": "#"}, {"label": "Responsive grid systems", "url": "#"}]`,
			"listen_on":   `[{"name": "Spotify", "url": "#"}, {"name": "Apple Podcast", "url": "#"}, {"name": "Overcast", "url": "#"}]`,
			"transcript":  `[{"speaker": "Host", "text": "Digital media presents new challenges for Swiss design principles, but the core remains relevant..."}, {"speaker": "UX Designer", "text": "The grid system translates beautifully to responsive design when you understand the underlying logic."}]`,
		},
		{
			"date":        "2025-05-13",
			"title":       "Swiss Book Design and Publication",
			"category":    "Editorial Design",
			"duration":    "02:20h",
			"image_path":  "/static/images/19.png",
			"description": "The art of Swiss editorial design: creating systematic approaches to books, magazines, and publications.",
			"topics":      `["Systematic approaches to book design.", "Typography hierarchy in long-form content.", "Grid systems for editorial layouts.", "The role of white space in readable design."]`,
			"sponsors":    `[{"name": "Editorial Systems Co.", "description": "Professional tools for systematic publication design."}, {"name": "BookGrid Studio", "description": "Grid-based templates for publishers and authors."}]`,
			"links":       `[{"label": "Swiss editorial design examples", "url": "#"}, {"label": "Publication design systems", "url": "#"}]`,
			"listen_on":   `[{"name": "Spotify", "url": "#"}, {"name": "Apple Podcast", "url": "#"}, {"name": "Overcast", "url": "#"}]`,
			"transcript":  `[{"speaker": "Host", "text": "Swiss book design prioritizes the reader's experience through systematic typography and layout..."}, {"speaker": "Editorial Designer", "text": "A well-designed book becomes invisible—the reader focuses on content, not the design."}]`,
		},
		{
			"date":        "2025-05-20",
			"title":       "Environmental Design and Wayfinding",
			"category":    "Environmental Design",
			"duration":    "01:50h",
			"image_path":  "/static/images/20.png",
			"description": "Swiss design principles applied to environmental graphics, signage systems, and architectural integration.",
			"topics":      `["Wayfinding systems based on Swiss design.", "Integration of graphics with architecture.", "Legibility in environmental contexts.", "Case studies: Airports, hospitals, and public spaces."]`,
			"sponsors":    `[{"name": "Wayfinding Solutions", "description": "Professional environmental graphic design services."}, {"name": "Architectural Graphics Co.", "description": "Signage systems that integrate with built environments."}]`,
			"links":       `[{"label": "Environmental Swiss design", "url": "#"}, {"label": "Wayfinding design principles", "url": "#"}]`,
			"listen_on":   `[{"name": "Spotify", "url": "#"}, {"name": "Apple Podcast", "url": "#"}, {"name": "Overcast", "url": "#"}]`,
			"transcript":  `[{"speaker": "Host", "text": "Swiss design principles are perfect for wayfinding—clarity and functionality are literally life-saving..."}, {"speaker": "Environmental Designer", "text": "Good wayfinding design is invisible until you need it, then it's absolutely essential."}]`,
		},
		{
			"date":        "2025-05-27",
			"title":       "The Economics of Swiss Design",
			"category":    "Business",
			"duration":    "01:35h",
			"image_path":  "/static/images/21.png",
			"description": "How Swiss design principles create economic value through efficiency, longevity, and systematic approaches.",
			"topics":      `["Why Swiss design is cost-effective long-term.", "Systematic design reduces production costs.", "Brand longevity through timeless design.", "The ROI of good design systems."]`,
			"sponsors":    `[{"name": "Design ROI Analytics", "description": "Measuring the business impact of design decisions."}, {"name": "Efficient Design Systems", "description": "Cost-effective design solutions for businesses."}]`,
			"links":       `[{"label": "Business value of Swiss design", "url": "#"}, {"label": "Design system economics", "url": "#"}]`,
			"listen_on":   `[{"name": "Spotify", "url": "#"}, {"name": "Apple Podcast", "url": "#"}, {"name": "Overcast", "url": "#"}]`,
			"transcript":  `[{"speaker": "Host", "text": "Good design isn't expensive—it's an investment that pays dividends over time..."}, {"speaker": "Business Analyst", "text": "Swiss design principles create efficiency that translates directly to cost savings."}]`,
		},
		{
			"date":        "2025-06-03",
			"title":       "Swiss Design Tools and Technology",
			"category":    "Technology",
			"duration":    "01:40h",
			"image_path":  "/static/images/22.png",
			"description": "Modern tools and technologies that support Swiss design principles in contemporary practice.",
			"topics":      `["Digital tools for systematic design.", "How technology enhances Swiss design principles.", "Automation and design systems.", "The future of systematic design tools."]`,
			"sponsors":    `[{"name": "SystemsDesign Software", "description": "Professional tools for systematic design workflows."}, {"name": "Grid Technology", "description": "Advanced grid systems for digital design."}]`,
			"links":       `[{"label": "Swiss design tools and software", "url": "#"}, {"label": "Systematic design workflows", "url": "#"}]`,
			"listen_on":   `[{"name": "Spotify", "url": "#"}, {"name": "Apple Podcast", "url": "#"}, {"name": "Overcast", "url": "#"}]`,
			"transcript":  `[{"speaker": "Host", "text": "Technology should support systematic thinking, not replace it..."}, {"speaker": "Design Technologist", "text": "The best design tools embody Swiss principles: they're systematic, logical, and help you think clearly."}]`,
		},
		{
			"date":        "2025-06-10",
			"title":       "Global Impact of Swiss Design",
			"category":    "Global Influence",
			"duration":    "02:15h",
			"image_path":  "/static/images/23.png",
			"description": "How Swiss design principles spread worldwide and influenced design cultures across different countries and contexts.",
			"topics":      `["Swiss design's influence on global design education.", "Cultural adaptations of Swiss principles.", "International designers influenced by Swiss methodology.", "Why Swiss design translates across cultures."]`,
			"sponsors":    `[{"name": "Global Design Network", "description": "Connecting designers worldwide through systematic principles."}, {"name": "International Design Archive", "description": "Preserving global design heritage and influence."}]`,
			"links":       `[{"label": "Swiss design global influence", "url": "#"}, {"label": "International design movements", "url": "#"}]`,
			"listen_on":   `[{"name": "Spotify", "url": "#"}, {"name": "Apple Podcast", "url": "#"}, {"name": "Overcast", "url": "#"}]`,
			"transcript":  `[{"speaker": "Host", "text": "Swiss design became a universal language of clear communication..."}, {"speaker": "Design Historian", "text": "The principles transcend cultural boundaries because they address fundamental human needs for clarity and understanding."}]`,
		},
		{
			"date":        "2025-06-17",
			"title":       "Teaching Swiss Design Principles",
			"category":    "Education",
			"duration":    "01:55h",
			"image_path":  "/static/images/24.png",
			"description": "Practical approaches for educators and mentors to teach Swiss design methodology and systematic thinking.",
			"topics":      `["Curriculum design for Swiss design education.", "Exercises that develop systematic thinking.", "Balancing creativity with systematic approaches.", "Assessment methods for design education."]`,
			"sponsors":    `[{"name": "Design Education Resources", "description": "Curriculum and materials for design educators."}, {"name": "Teaching Systems Co.", "description": "Educational frameworks for systematic design thinking."}]`,
			"links":       `[{"label": "Swiss design teaching methods", "url": "#"}, {"label": "Design education curriculum", "url": "#"}]`,
			"listen_on":   `[{"name": "Spotify", "url": "#"}, {"name": "Apple Podcast", "url": "#"}, {"name": "Overcast", "url": "#"}]`,
			"transcript":  `[{"speaker": "Host", "text": "Teaching Swiss design is about teaching thinking, not just aesthetics..."}, {"speaker": "Design Educator", "text": "Students need to understand the why behind every design decision, not just the how."}]`,
		},
		{
			"date":        "2025-06-24",
			"title":       "Swiss Design in the Age of AI",
			"category":    "Future Technology",
			"duration":    "02:00h",
			"image_path":  "/static/images/25.png",
			"description": "Exploring how Swiss design principles remain relevant and valuable in the era of artificial intelligence and automated design.",
			"topics":      `["How AI tools can support systematic design thinking.", "The irreplaceable value of human design judgment.", "Swiss principles for training AI design systems.", "Future challenges and opportunities."]`,
			"sponsors":    `[{"name": "AI Design Systems", "description": "Intelligent tools that embody systematic design principles."}, {"name": "Future Design Lab", "description": "Research into the intersection of AI and design methodology."}]`,
			"links":       `[{"label": "AI and Swiss design principles", "url": "#"}, {"label": "Future of systematic design", "url": "#"}]`,
			"listen_on":   `[{"name": "Spotify", "url": "#"}, {"name": "Apple Podcast", "url": "#"}, {"name": "Overcast", "url": "#"}]`,
			"transcript":  `[{"speaker": "Host", "text": "AI can generate designs, but systematic thinking ensures they communicate effectively..."}, {"speaker": "AI Researcher", "text": "Swiss design principles provide the logical framework that AI needs to create meaningful, not just beautiful, designs."}]`,
		},
		{
			"date":        "2025-07-01",
			"title":       "The Future of Swiss Design",
			"category":    "Future Thinking",
			"duration":    "02:30h",
			"image_path":  "/static/images/26.png",
			"description": "Speculating on the evolution of Swiss design principles and their role in addressing future communication challenges.",
			"topics":      `["Emerging challenges that Swiss design can address.", "Evolution vs. preservation of core principles.", "New media and communication technologies.", "The next generation of Swiss design thinkers."]`,
			"sponsors":    `[{"name": "Future Systems Design", "description": "Pioneering systematic approaches for emerging design challenges."}, {"name": "Next Generation Studio", "description": "Training the future leaders of systematic design."}]`,
			"links":       `[{"label": "Future of Swiss design", "url": "#"}, {"label": "Emerging design challenges", "url": "#"}]`,
			"listen_on":   `[{"name": "Spotify", "url": "#"}, {"name": "Apple Podcast", "url": "#"}, {"name": "Overcast", "url": "#"}]`,
			"transcript":  `[{"speaker": "Host", "text": "Swiss design principles aren't just historical—they're tools for solving future problems..."}, {"speaker": "Design Futurist", "text": "As information becomes more complex, the need for systematic, clear communication becomes even more critical."}]`,
		},
		{
			"date":        "2025-07-08",
			"title":       "Legacy and Continuation: 75 Years of Swiss Design",
			"category":    "Retrospective",
			"duration":    "03:00h",
			"image_path":  "/static/images/27.png",
			"description": "A comprehensive retrospective celebrating 75 years of Swiss design, its lasting impact, and continuing relevance in contemporary practice.",
			"topics":      `["Tracing 75 years of Swiss design evolution.", "Key figures and their lasting contributions.", "How Swiss design shaped modern visual culture.", "Ensuring the principles survive for future generations.", "Personal stories from designers influenced by Swiss methodology."]`,
			"sponsors":    `[{"name": "Swiss Design Foundation", "description": "Preserving and promoting Swiss design heritage worldwide."}, {"name": "Design Legacy Project", "description": "Documenting the stories and impact of design movements."}, {"name": "Systematic Design Alliance", "description": "A global network of designers committed to systematic approaches."}]`,
			"links":       `[{"label": "Swiss design 75th anniversary exhibition", "url": "#"}, {"label": "Complete Swiss design timeline", "url": "#"}, {"label": "Oral history project", "url": "#"}]`,
			"listen_on":   `[{"name": "Spotify", "url": "#"}, {"name": "Apple Podcast", "url": "#"}, {"name": "Overcast", "url": "#"}]`,
			"transcript":  `[{"speaker": "Host", "text": "Today we celebrate not just 75 years of Swiss design, but 75 years of clear thinking made visible..."}, {"speaker": "Design Historian", "text": "The impact cannot be measured just in posters and logos—it's in every clear sign, every readable interface, every moment when design serves human understanding."}, {"speaker": "Contemporary Designer", "text": "Swiss design taught us that design isn't about self-expression—it's about human connection through clarity."}, {"speaker": "Design Student", "text": "Learning Swiss principles changed how I see everything. It's not a style—it's a way of thinking that makes you a better communicator."}, {"speaker": "Host", "text": "As we look toward the future, Swiss design principles remain our compass for creating communication that truly serves humanity."}]`,
		},
	}

	return decodeSeed(podcastsRaw)
}

// decodeSeed turns raw catalog entries into podcasts, numbering them from 1.
// A missing text field or a list field that is not valid JSON is an error
// naming the entry, so a typo in the catalog stops startup instead of
// seeding a podcast with silently empty lists.
func decodeSeed(raw []map[string]any) ([]PodcastResponse, error) {
	podcasts := make([]PodcastResponse, 0, len(raw))
	for i, entry := range raw {
		podcast := PodcastResponse{ID: i + 1}
		for field, dest := range map[string]*string{
			"date":        &podcast.Date,
			"title":       &podcast.Title,
			"category":    &podcast.Category,
			"duration":    &podcast.Duration,
			"image_path":  &podcast.ImagePath,
			"description": &podcast.Description,
		} {
			value, ok := entry[field].(string)
			if !ok {
				return nil, fmt.Errorf("seed podcast %d: %q is missing or not text", i+1, field)
			}
			*dest = value
		}
		for field, dest := range map[string]any{
			"topics":     &podcast.Topics,
			"sponsors":   &podcast.Sponsors,
			"links":      &podcast.Links,
			"listen_on":  &podcast.ListenOn,
			"transcript": &podcast.Transcript,
		} {
			text, ok := entry[field].(string)
			if !ok || text == "" {
				continue // list fields are optional
			}
			if err := json.Unmarshal([]byte(text), dest); err != nil {
				return nil, fmt.Errorf("seed podcast %d (%v): %s: %w", i+1, entry["title"], field, err)
			}
		}
		podcasts = append(podcasts, podcast)
	}
	return podcasts, nil
}
