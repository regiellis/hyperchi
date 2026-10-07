// Command streamer is a podcast catalog built on HyperChi: htmx fragment
// endpoints for details and modals, and HyperChi directives for local state.
// Run it with go run main.go or air.
package main

import (
	"log"

	"github.com/regiellis/hyperchi/hyperchi"
	"github.com/regiellis/hyperchi/hyperchi/config"
	"github.com/regiellis/hyperchi/hyperchi/helpers"
)

// Podcast is one episode.
type Podcast struct {
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
	// development behavior (detailed error pages, open CORS). The podcast data
	// is static, so the app opens no SQLite database.
	cfg := config.FromEnv()
	cfg.Database.Disabled = true
	app, err := hyperchi.NewWithConfigE(cfg)
	if err != nil {
		log.Fatal(err)
	}

	app.AutoMiddleware()

	helpers.SetupDevelopmentLogging()

	helpers.Must(app.LoadEnv())

	helpers.Must(app.LoadTemplates("templates/*.tmpl"))

	setupRoutes(app)

	port := app.Helpers.Env.GetOr("PORT", "8080")
	app.Helpers.Logger.Info("HyperChi Streamer starting on port %s", port)
	app.Helpers.Logger.Info("HTMX enhanced interactions enabled")
	app.Helpers.Logger.Info("HyperChi declarative directives enabled")
	app.Helpers.Logger.Info("No database - static content for simplicity")
	if err := app.Serve("0.0.0.0:" + port); err != nil {
		app.Helpers.Logger.Fatal("server stopped: %v", err)
	}
}

// setupRoutes registers the page, the htmx fragment endpoints, search, static
// files, and the health routes.
func setupRoutes(app *hyperchi.HyperChi) {
	podcastData := getPodcastData()
	spotifyURL := app.Helpers.Env.Get("PLATFORM_SPOTIFY_URL")

	// Main page - shows podcast list, always as a full page
	app.Get("/", func(c *hyperchi.Context) error {
		return c.Page("index", hyperchi.H{
			"title":       "Streamer",
			"description": "Swiss Design Podcast Platform - Powered by HyperChi + HTMX + HyperChi directives",
			"podcasts":    podcastData,
			"spotify":     spotifyURL,
		})
	})

	// findPodcast resolves the {id} route parameter to a podcast. A
	// non-numeric ID is treated as unknown, so it answers 404 like a missing one.
	findPodcast := func(c *hyperchi.Context) (*Podcast, error) {
		id := c.Param("id")
		if helpers.S(id).IsEmpty() {
			return nil, hyperchi.BadRequest("Missing podcast ID")
		}

		podcastID, err := helpers.S(id).ToInt()
		if err != nil {
			return nil, hyperchi.NotFound("Podcast not found")
		}
		for i := range podcastData {
			if podcastData[i].ID == podcastID {
				return &podcastData[i], nil
			}
		}
		return nil, hyperchi.NotFound("Podcast not found")
	}

	// HTMX endpoint for expanding podcast details
	app.Get("/podcast/{id}/details", func(c *hyperchi.Context) error {
		podcast, err := findPodcast(c)
		if err != nil {
			return err
		}
		return c.Fragment("podcast-details", hyperchi.H{"podcast": podcast})
	})

	// HTMX endpoint for podcast modal content
	app.Get("/podcast/{id}/modal", func(c *hyperchi.Context) error {
		podcast, err := findPodcast(c)
		if err != nil {
			return err
		}
		return c.Fragment("podcast-modal", hyperchi.H{"podcast": podcast})
	})

	// JSON search over title, category, and description.
	app.Get("/api/search", func(c *hyperchi.Context) error {
		query := c.Query("q")

		results := []Podcast{}
		if query != "" {
			searchTerm := helpers.S(query).Lower().Trim().String()

			for _, podcast := range podcastData {
				if helpers.S(podcast.Title).Lower().Contains(searchTerm) ||
					helpers.S(podcast.Category).Lower().Contains(searchTerm) ||
					helpers.S(podcast.Description).Lower().Contains(searchTerm) {
					results = append(results, podcast)
				}
			}
		}

		return c.JSON(hyperchi.H{
			"query":   query,
			"results": results,
			"count":   len(results),
		})
	})

	app.Static("/static/*", "static")

	health := hyperchi.H{
		"status":  "ok",
		"service": "HyperChi Streamer",
	}

	app.Get("/health", func(c *hyperchi.Context) error {
		return c.JSON(health)
	})

	// HEAD /health: the JSON response sends its headers and skips the body
	// because the request is HEAD.
	app.Head("/health", func(c *hyperchi.Context) error {
		return c.SetHeader("X-Service", "HyperChi Streamer").JSON(health)
	})

	// OPTIONS /health: the allowed methods as headers, with an empty 200.
	app.Options("/health", func(c *hyperchi.Context) error {
		return c.SetHeader("Allow", "GET, HEAD, OPTIONS").SetHeader("X-Service", "HyperChi Streamer").Empty()
	})

	// An OPTIONS route that answers with a JSON body.
	app.Options("/test-options", func(c *hyperchi.Context) error {
		return c.SetHeader("Allow", "GET, POST, PUT, DELETE, PATCH, HEAD, OPTIONS").
			SetHeader("X-Custom-Options", "true").
			JSON(hyperchi.H{
				"methods": []string{"GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS"},
				"message": "OPTIONS method working correctly",
			})
	})
}

// getPodcastData returns the built-in catalog.
func getPodcastData() []Podcast {
	return []Podcast{
		{
			ID:          1,
			Date:        "2025-01-01",
			Title:       "Armin Hofmann — Mastering Visual Balance",
			Category:    "Typography",
			Duration:    "01:50h",
			ImagePath:   "/static/images/1.png",
			Description: "Explore the life and teachings of Armin Hofmann, the pioneer of Swiss typography and his approach to visual balance and clarity.",
			Topics: []string{
				"The impact of Armin Hofmann on Swiss typography.",
				"Understanding visual balance in minimalistic design.",
				"How Hofmann's teachings remain relevant in modern design.",
				"Typography as the backbone of communication design.",
			},
			Sponsors: []map[string]string{
				{"name": "Grid Masters", "description": "Your go-to resource for advanced design grids and templates."},
				{"name": "SwissType Foundry", "description": "Discover premium typefaces inspired by Swiss design."},
			},
			Links: []map[string]string{
				{"label": "Learn more about Armin Hofmann", "url": "#"},
				{"label": "Typography Principles", "url": "#"},
			},
			ListenOn: []map[string]string{
				{"name": "Spotify", "url": "#"},
				{"name": "Apple Podcast", "url": "#"},
				{"name": "Overcast", "url": "#"},
			},
			Transcript: []map[string]string{
				{"speaker": "Host", "text": "Welcome to this episode where we discuss Armin Hofmann's influence on modern design. Today, we'll explore how his teachings at the Basel School of Design shaped generations of designers and continue to influence contemporary visual communication. We're joined by design historian Dr. Sarah Chen."},
				{"speaker": "Guest", "text": "Hofmann's principles of balance and structure are timeless, and it's fascinating to see how they've evolved yet remained fundamentally relevant in our digital age."},
				{"speaker": "Host", "text": "Could you elaborate on what made his approach so revolutionary for its time?"},
				{"speaker": "Guest", "text": "Hofmann introduced a systematic approach to design that was radical in its simplicity. He emphasized that design isn't about decoration, but about finding the essential form of communication. His famous 'dot, line, and plane' exercises helped students understand the fundamental elements of visual composition."},
			},
		},
		{
			ID:          2,
			Date:        "2025-01-08",
			Title:       "Josef Müller-Brockmann — The Grid as a Design Tool",
			Category:    "Grids",
			Duration:    "02:15h",
			ImagePath:   "/static/images/2.png",
			Description: "A deep dive into Josef Müller-Brockmann's philosophy of the grid system and its transformative impact on design.",
			Topics: []string{
				"The evolution of the grid system in Swiss design.",
				"Why the grid is a tool for clarity and creativity.",
				"Josef Müller-Brockmann's most iconic works.",
			},
			Sponsors: []map[string]string{
				{"name": "The Grid Co.", "description": "Modern tools for grid-based design workflows."},
				{"name": "TypoSwiss", "description": "Explore typefaces built with the Swiss grid in mind."},
			},
			Links: []map[string]string{
				{"label": "Josef Müller-Brockmann's legacy", "url": "#"},
				{"label": "The Grid as a System", "url": "#"},
			},
			ListenOn: []map[string]string{
				{"name": "Spotify", "url": "#"},
				{"name": "Apple Podcast", "url": "#"},
				{"name": "Overcast", "url": "#"},
			},
			Transcript: []map[string]string{
				{"speaker": "Host", "text": "Today, we explore how the grid became a design revolution..."},
			},
		},
		{
			ID:          3,
			Date:        "2025-01-15",
			Title:       "The Swiss Design Movement — Simplicity and Functionality",
			Category:    "History",
			Duration:    "02:00h",
			ImagePath:   "/static/images/3.png",
			Description: "Unpacking the Swiss design movement's emphasis on simplicity, functionality, and clarity.",
			Topics: []string{
				"What defines Swiss design aesthetics?",
				"The role of neutrality and functionality in design.",
				"Swiss design beyond Switzerland: Its global influence.",
			},
			Sponsors: []map[string]string{
				{"name": "Neutral Spaces", "description": "Creating designs that speak without excess."},
			},
			Links: []map[string]string{
				{"label": "Swiss design history", "url": "#"},
				{"label": "Neutral design philosophy", "url": "#"},
			},
			ListenOn: []map[string]string{
				{"name": "Spotify", "url": "#"},
				{"name": "Apple Podcast", "url": "#"},
			},
			Transcript: []map[string]string{
				{"speaker": "Host", "text": "Swiss design is a testament to the beauty of simplicity..."},
			},
		},
		{
			ID:          4,
			Date:        "2025-01-22",
			Title:       "Emil Ruder — Precision in Typography",
			Category:    "Typography",
			Duration:    "01:45h",
			ImagePath:   "/static/images/4.png",
			Description: "Discover Emil Ruder's unique contributions to Swiss typography and his philosophy on precision and order.",
			Topics: []string{
				"Ruder's influence on modern typography.",
				"Why precision matters in communication.",
				"Breaking rules: When it's acceptable in typography.",
			},
			Sponsors: []map[string]string{
				{"name": "TypeCraft", "description": "Perfect tools for crafting precise typographic designs."},
			},
			Links: []map[string]string{
				{"label": "Read more about Emil Ruder", "url": "#"},
			},
			ListenOn: []map[string]string{
				{"name": "Spotify", "url": "#"},
				{"name": "Apple Podcast", "url": "#"},
			},
			Transcript: []map[string]string{
				{"speaker": "Host", "text": "Emil Ruder was a genius of clarity and precision in design..."},
			},
		},
		{
			ID:          5,
			Date:        "2025-01-29",
			Title:       "Swiss Design in Digital Spaces",
			Category:    "Digital Design",
			Duration:    "01:30h",
			ImagePath:   "/static/images/5.png",
			Description: "How Swiss design principles have transitioned into the digital era while maintaining their core values.",
			Topics: []string{
				"The challenges of adapting Swiss design to digital media.",
				"Maintaining clarity and simplicity in a digital-first world.",
			},
			Sponsors: []map[string]string{
				{"name": "Digital Grids", "description": "Helping you design for the digital age with precision."},
			},
			Links: []map[string]string{
				{"label": "Swiss design for digital media", "url": "#"},
			},
			ListenOn: []map[string]string{
				{"name": "Spotify", "url": "#"},
				{"name": "Apple Podcast", "url": "#"},
			},
			Transcript: []map[string]string{
				{"speaker": "Host", "text": "Digital spaces have opened new avenues for Swiss design..."},
			},
		},
		{
			ID:          6,
			Date:        "2025-02-05",
			Title:       "Max Bill — The Art of Clarity",
			Category:    "Art and Design",
			Duration:    "02:00h",
			ImagePath:   "/static/images/6.png",
			Description: "Exploring Max Bill's contributions to art and design, focusing on his relentless pursuit of clarity.",
			Topics: []string{
				"Max Bill's multidisciplinary approach to design.",
				"Art as an extension of clarity and logic.",
				"Why clarity remains at the heart of effective design.",
			},
			Sponsors: []map[string]string{
				{"name": "Clarity Collective", "description": "Simplify your designs with tools inspired by Max Bill."},
			},
			Links: []map[string]string{
				{"label": "Max Bill's portfolio", "url": "#"},
			},
			ListenOn: []map[string]string{
				{"name": "Spotify", "url": "#"},
				{"name": "Apple Podcast", "url": "#"},
			},
			Transcript: []map[string]string{
				{"speaker": "Host", "text": "Max Bill taught us that art and design can coexist beautifully..."},
			},
		},
		{
			ID:          7,
			Date:        "2025-02-12",
			Title:       "The Role of White Space in Swiss Design",
			Category:    "Minimalism",
			Duration:    "01:20h",
			ImagePath:   "/static/images/7.png",
			Description: "An exploration of how Swiss designers use white space to emphasize clarity and simplicity in their work.",
			Topics: []string{
				"What is white space, and why does it matter?",
				"How Swiss design masters incorporated white space into their layouts.",
				"Balancing white space with content density.",
			},
			Sponsors: []map[string]string{
				{"name": "Whitespace Studio", "description": "Tools for mastering the art of minimalism in design."},
			},
			Links: []map[string]string{
				{"label": "Learn more about white space in design", "url": "#"},
			},
			ListenOn: []map[string]string{
				{"name": "Spotify", "url": "#"},
				{"name": "Apple Podcast", "url": "#"},
			},
			Transcript: []map[string]string{
				{"speaker": "Host", "text": "White space is often misunderstood but vital in design..."},
			},
		},
		{
			ID:          8,
			Date:        "2025-02-19",
			Title:       "Grid-Based Typography: Practical Techniques",
			Category:    "Typography",
			Duration:    "01:40h",
			ImagePath:   "/static/images/8.png",
			Description: "Learn how to use grid systems to create visually stunning and readable typography.",
			Topics: []string{
				"Combining typography and grids for better communication.",
				"Best practices for setting typographic hierarchy.",
				"Examples of effective grid-based typographic designs.",
			},
			Sponsors: []map[string]string{
				{"name": "FontGrid Pro", "description": "Grids tailored for typographic design."},
			},
			Links: []map[string]string{
				{"label": "Typography and grid systems", "url": "#"},
			},
			ListenOn: []map[string]string{
				{"name": "Spotify", "url": "#"},
				{"name": "Apple Podcast", "url": "#"},
			},
			Transcript: []map[string]string{
				{"speaker": "Host", "text": "Grids are an essential tool in achieving typographic clarity..."},
			},
		},
		{
			ID:          9,
			Date:        "2025-02-26",
			Title:       "Swiss Design in Branding",
			Category:    "Branding",
			Duration:    "01:50h",
			ImagePath:   "/static/images/9.png",
			Description: "Discover how Swiss design principles can create impactful and timeless brands.",
			Topics: []string{
				"Why Swiss design is the gold standard in branding.",
				"Case studies of brands inspired by Swiss design.",
				"How to apply Swiss design concepts to your brand identity.",
			},
			Sponsors: []map[string]string{
				{"name": "BrandSwiss", "description": "Elevate your brand with Swiss-inspired design tools."},
			},
			Links: []map[string]string{
				{"label": "Swiss design and branding", "url": "#"},
			},
			ListenOn: []map[string]string{
				{"name": "Spotify", "url": "#"},
				{"name": "Apple Podcast", "url": "#"},
			},
			Transcript: []map[string]string{
				{"speaker": "Host", "text": "Branding is one of the key areas where Swiss design thrives..."},
			},
		},
		{
			ID:          10,
			Date:        "2025-03-11",
			Title:       "Swiss Poster Design — Bold and Beautiful",
			Category:    "Graphic Design",
			Duration:    "01:35h",
			ImagePath:   "/static/images/10.png",
			Description: "Explore the striking visual style of Swiss posters and how they combine bold typography with clean layouts.",
			Topics: []string{
				"Key features of Swiss poster design.",
				"How to create a visually impactful poster using Swiss principles.",
				"Famous poster designs that defined the era.",
			},
			Sponsors: []map[string]string{
				{"name": "PosterCraft", "description": "Tools for creating striking posters inspired by Swiss design."},
			},
			Links: []map[string]string{
				{"label": "Swiss poster design history", "url": "#"},
			},
			ListenOn: []map[string]string{
				{"name": "Spotify", "url": "#"},
				{"name": "Apple Podcast", "url": "#"},
			},
			Transcript: []map[string]string{
				{"speaker": "Host", "text": "Swiss poster design is the epitome of clarity and boldness..."},
			},
		},
		{
			ID:          11,
			Date:        "2025-03-18",
			Title:       "Adrian Frutiger — Type for the Future",
			Category:    "Typography",
			Duration:    "01:45h",
			ImagePath:   "/static/images/11.png",
			Description: "Learn about Adrian Frutiger's groundbreaking work in typography and how it shaped modern type design.",
			Topics: []string{
				"Adrian Frutiger's contributions to typeface design.",
				"How Univers revolutionized typography.",
				"The impact of Frutiger on digital and web typography.",
			},
			Sponsors: []map[string]string{
				{"name": "TypeFuture", "description": "Discover the tools inspired by Adrian Frutiger."},
			},
			Links: []map[string]string{
				{"label": "Adrian Frutiger's legacy", "url": "#"},
			},
			ListenOn: []map[string]string{
				{"name": "Spotify", "url": "#"},
				{"name": "Apple Podcast", "url": "#"},
			},
			Transcript: []map[string]string{
				{"speaker": "Host", "text": "Adrian Frutiger's name is synonymous with timeless typography..."},
			},
		},
		{
			ID:          12,
			Date:        "2025-03-25",
			Title:       "Swiss Design in Architecture",
			Category:    "Architecture",
			Duration:    "02:10h",
			ImagePath:   "/static/images/12.png",
			Description: "How Swiss design principles have influenced architectural forms and spaces.",
			Topics: []string{
				"The intersection of design and architecture in Switzerland.",
				"Why simplicity and functionality matter in architectural design.",
				"Case studies of Swiss-inspired buildings worldwide.",
			},
			Sponsors: []map[string]string{
				{"name": "SpaceSwiss", "description": "Designing architectural spaces with precision."},
			},
			Links: []map[string]string{
				{"label": "Swiss architectural design", "url": "#"},
			},
			ListenOn: []map[string]string{
				{"name": "Spotify", "url": "#"},
				{"name": "Apple Podcast", "url": "#"},
			},
			Transcript: []map[string]string{
				{"speaker": "Host", "text": "Swiss design principles extend far beyond graphic design..."},
			},
		},
		{
			ID:          13,
			Date:        "2025-04-01",
			Title:       "Photography and Swiss Minimalism",
			Category:    "Photography",
			Duration:    "01:25h",
			ImagePath:   "/static/images/13.png",
			Description: "How photographers incorporate Swiss minimalism to create striking visual narratives.",
			Topics: []string{
				"What is Swiss minimalism in photography?",
				"Techniques for incorporating minimalism into your photography.",
				"Examples of minimalist photography inspired by Swiss design.",
			},
			Sponsors: []map[string]string{
				{"name": "PhotoSimplicity", "description": "Minimalist photography tools and guides."},
			},
			Links: []map[string]string{
				{"label": "Minimalism in photography", "url": "#"},
			},
			ListenOn: []map[string]string{
				{"name": "Spotify", "url": "#"},
				{"name": "Apple Podcast", "url": "#"},
			},
			Transcript: []map[string]string{
				{"speaker": "Host", "text": "Swiss minimalism has become a cornerstone of visual storytelling..."},
			},
		},
		{
			ID:          14,
			Date:        "2025-04-08",
			Title:       "Swiss Web Design — Function Meets Form",
			Category:    "Web Design",
			Duration:    "02:05h",
			ImagePath:   "/static/images/14.png",
			Description: "Applying Swiss design principles to web interfaces for better usability and aesthetics.",
			Topics: []string{
				"How Swiss design principles influence web interfaces.",
				"Balancing usability and aesthetics in web design.",
				"The future of Swiss-inspired web design.",
			},
			Sponsors: []map[string]string{
				{"name": "WebSwiss", "description": "Building better websites with Swiss-inspired tools."},
			},
			Links: []map[string]string{
				{"label": "Swiss web design principles", "url": "#"},
			},
			ListenOn: []map[string]string{
				{"name": "Spotify", "url": "#"},
				{"name": "Apple Podcast", "url": "#"},
			},
			Transcript: []map[string]string{
				{"speaker": "Host", "text": "Web design and Swiss design are a perfect match..."},
			},
		},
		{
			ID:          15,
			Date:        "2025-04-15",
			Title:       "The Bauhaus and Swiss Design Connection",
			Category:    "Design History",
			Duration:    "01:40h",
			ImagePath:   "/static/images/15.png",
			Description: "How the Bauhaus influenced the development of Swiss design principles and modern minimalism.",
			Topics: []string{
				"Parallels between Bauhaus and Swiss design.",
				"The Bauhaus emphasis on function and its Swiss evolution.",
				"Why the Bauhaus legacy thrives in Swiss design.",
			},
			Sponsors: []map[string]string{
				{"name": "Design Origins", "description": "Tracing design history with tools inspired by legends."},
			},
			Links: []map[string]string{
				{"label": "Explore Bauhaus and Swiss design", "url": "#"},
			},
			ListenOn: []map[string]string{
				{"name": "Spotify", "url": "#"},
				{"name": "Apple Podcast", "url": "#"},
			},
			Transcript: []map[string]string{
				{"speaker": "Host", "text": "Swiss design owes much to the Bauhaus principles of functionality and simplicity..."},
			},
		},
		{
			ID:          16,
			Date:        "2025-04-22",
			Title:       "The Art of Neutrality in Swiss Design",
			Category:    "Philosophy",
			Duration:    "01:25h",
			ImagePath:   "/static/images/16.png",
			Description: "Neutrality as a cornerstone of Swiss design philosophy and its impact on global aesthetics.",
			Topics: []string{
				"What does neutrality mean in design?",
				"How neutrality enhances clarity and universality.",
				"Examples of neutrality in Swiss design works.",
			},
			Sponsors: []map[string]string{
				{"name": "NeutralForm", "description": "Creating designs that prioritize clarity above all."},
			},
			Links: []map[string]string{
				{"label": "Neutral design principles", "url": "#"},
			},
			ListenOn: []map[string]string{
				{"name": "Spotify", "url": "#"},
				{"name": "Apple Podcast", "url": "#"},
			},
			Transcript: []map[string]string{
				{"speaker": "Host", "text": "Neutrality is often seen as the foundation of Swiss design..."},
			},
		},
		{
			ID:          17,
			Date:        "2025-04-29",
			Title:       "Swiss Design in Editorial Layouts",
			Category:    "Editorial Design",
			Duration:    "01:50h",
			ImagePath:   "/static/images/17.png",
			Description: "How Swiss design has revolutionized the world of magazines, newspapers, and digital publications.",
			Topics: []string{
				"Using grids to create harmonious editorial layouts.",
				"Why readability is key in Swiss editorial design.",
				"The evolution of editorial design in the digital age.",
			},
			Sponsors: []map[string]string{
				{"name": "LayoutPro", "description": "Advanced tools for editorial design."},
			},
			Links: []map[string]string{
				{"label": "Learn about editorial grids", "url": "#"},
			},
			ListenOn: []map[string]string{
				{"name": "Spotify", "url": "#"},
				{"name": "Apple Podcast", "url": "#"},
			},
			Transcript: []map[string]string{
				{"speaker": "Host", "text": "Editorial design and Swiss principles go hand in hand..."},
			},
		},
		{
			ID:          18,
			Date:        "2025-05-06",
			Title:       "Swiss Packaging Design — Form and Function",
			Category:    "Packaging Design",
			Duration:    "01:30h",
			ImagePath:   "/static/images/18.png",
			Description: "How Swiss design principles influence sustainable and visually striking packaging.",
			Topics: []string{
				"The importance of simplicity in packaging.",
				"How Swiss design impacts eco-friendly packaging trends.",
				"Case studies of iconic Swiss packaging designs.",
			},
			Sponsors: []map[string]string{
				{"name": "PackSwiss", "description": "Sustainable packaging with Swiss aesthetics."},
			},
			Links: []map[string]string{
				{"label": "Swiss packaging trends", "url": "#"},
			},
			ListenOn: []map[string]string{
				{"name": "Spotify", "url": "#"},
				{"name": "Apple Podcast", "url": "#"},
			},
			Transcript: []map[string]string{
				{"speaker": "Host", "text": "Packaging design reflects the core of Swiss principles: functionality and beauty..."},
			},
		},
		{
			ID:          19,
			Date:        "2025-05-13",
			Title:       "Swiss Design in Motion Graphics",
			Category:    "Motion Design",
			Duration:    "01:45h",
			ImagePath:   "/static/images/19.png",
			Description: "Adapting Swiss design principles to the dynamic world of motion graphics.",
			Topics: []string{
				"Translating grid systems into motion.",
				"Why simplicity works best in animation.",
				"The evolution of Swiss design in digital media.",
			},
			Sponsors: []map[string]string{
				{"name": "MotionGrid", "description": "Tools for minimalist motion design."},
			},
			Links: []map[string]string{
				{"label": "Swiss motion graphics", "url": "#"},
			},
			ListenOn: []map[string]string{
				{"name": "Spotify", "url": "#"},
				{"name": "Apple Podcast", "url": "#"},
			},
			Transcript: []map[string]string{
				{"speaker": "Host", "text": "Swiss design principles extend seamlessly into motion graphics..."},
			},
		},
		{
			ID:          20,
			Date:        "2025-05-20",
			Title:       "The Role of Color in Swiss Design",
			Category:    "Color Theory",
			Duration:    "01:20h",
			ImagePath:   "/static/images/20.png",
			Description: "How Swiss designers use color intentionally to create harmony and focus in their work.",
			Topics: []string{
				"Minimalist approaches to color in Swiss design.",
				"Balancing neutrality with impactful accent colors.",
				"Color theory essentials for modern designers.",
			},
			Sponsors: []map[string]string{
				{"name": "ColorGrid", "description": "Color palettes inspired by Swiss minimalism."},
			},
			Links: []map[string]string{
				{"label": "Color theory and design", "url": "#"},
			},
			ListenOn: []map[string]string{
				{"name": "Spotify", "url": "#"},
				{"name": "Apple Podcast", "url": "#"},
			},
			Transcript: []map[string]string{
				{"speaker": "Host", "text": "Color plays a subtle yet powerful role in Swiss design..."},
			},
		},
		{
			ID:          21,
			Date:        "2025-05-27",
			Title:       "Swiss Design in Wayfinding Systems",
			Category:    "Environmental Design",
			Duration:    "02:00h",
			ImagePath:   "/static/images/21.png",
			Description: "Learn how Swiss design principles shape intuitive and effective wayfinding systems worldwide.",
			Topics: []string{
				"The importance of clarity in wayfinding.",
				"Examples of Swiss-inspired wayfinding systems.",
				"How to design for user-centric navigation.",
			},
			Sponsors: []map[string]string{
				{"name": "WayfindPro", "description": "Simplify navigation with Swiss design principles."},
			},
			Links: []map[string]string{
				{"label": "Explore wayfinding design", "url": "#"},
			},
			ListenOn: []map[string]string{
				{"name": "Spotify", "url": "#"},
				{"name": "Apple Podcast", "url": "#"},
			},
			Transcript: []map[string]string{
				{"speaker": "Host", "text": "Swiss design is at the forefront of wayfinding systems..."},
			},
		},
		{
			ID:          22,
			Date:        "2025-06-03",
			Title:       "Designing Accessible Interfaces with Swiss Principles",
			Category:    "Accessibility",
			Duration:    "01:35h",
			ImagePath:   "/static/images/22.png",
			Description: "How Swiss design principles align with accessibility standards to create inclusive digital experiences.",
			Topics: []string{
				"How simplicity aids accessibility.",
				"Creating layouts for diverse user needs.",
				"Swiss design as a model for universal usability.",
			},
			Sponsors: []map[string]string{
				{"name": "AccessDesign", "description": "Inclusive design made simple."},
			},
			Links: []map[string]string{
				{"label": "Accessible design guidelines", "url": "#"},
			},
			ListenOn: []map[string]string{
				{"name": "Spotify", "url": "#"},
				{"name": "Apple Podcast", "url": "#"},
			},
			Transcript: []map[string]string{
				{"speaker": "Host", "text": "Accessibility and Swiss design share a common goal: universality..."},
			},
		},
		{
			ID:          23,
			Date:        "2025-06-10",
			Title:       "The Helvetica Effect — Cultural Impact of a Typeface",
			Category:    "Typography",
			Duration:    "01:50h",
			ImagePath:   "/static/images/23.png",
			Description: "An exploration of Helvetica's influence on culture and design over decades.",
			Topics: []string{
				"The global adoption of Helvetica.",
				"Why Helvetica became synonymous with clarity.",
				"Cultural milestones influenced by Helvetica.",
			},
			Sponsors: []map[string]string{
				{"name": "TypeCulture", "description": "Celebrating iconic typefaces worldwide."},
			},
			Links: []map[string]string{
				{"label": "Explore Helvetica's history", "url": "#"},
			},
			ListenOn: []map[string]string{
				{"name": "Spotify", "url": "#"},
				{"name": "Apple Podcast", "url": "#"},
			},
			Transcript: []map[string]string{
				{"speaker": "Host", "text": "Helvetica transcends design—it's a cultural phenomenon..."},
			},
		},
		{
			ID:          24,
			Date:        "2025-06-17",
			Title:       "Swiss Aesthetics in Data Visualization",
			Category:    "Data Design",
			Duration:    "01:40h",
			ImagePath:   "/static/images/24.png",
			Description: "How Swiss design principles create effective and aesthetically pleasing data visualizations.",
			Topics: []string{
				"Using grids to structure data visualizations.",
				"The role of simplicity in presenting complex data.",
				"Why Swiss design principles improve comprehension.",
			},
			Sponsors: []map[string]string{
				{"name": "DataSwiss", "description": "Better data visualization tools for modern designers."},
			},
			Links: []map[string]string{
				{"label": "Swiss data design techniques", "url": "#"},
			},
			ListenOn: []map[string]string{
				{"name": "Spotify", "url": "#"},
				{"name": "Apple Podcast", "url": "#"},
			},
			Transcript: []map[string]string{
				{"speaker": "Host", "text": "Swiss design offers a unique approach to visualizing data effectively..."},
			},
		},
		{
			ID:          25,
			Date:        "2025-06-24",
			Title:       "The International Style — Swiss Design Goes Global",
			Category:    "Design History",
			Duration:    "02:05h",
			ImagePath:   "/static/images/25.png",
			Description: "A discussion on how the International Typographic Style brought Swiss design to the world stage.",
			Topics: []string{
				"What defines the International Typographic Style?",
				"Key designers who spread Swiss design globally.",
				"How the style evolved beyond Switzerland.",
			},
			Sponsors: []map[string]string{
				{"name": "DesignHistory", "description": "Uncover the past to inspire the future."},
			},
			Links: []map[string]string{
				{"label": "Learn about International Typographic Style", "url": "#"},
			},
			ListenOn: []map[string]string{
				{"name": "Spotify", "url": "#"},
				{"name": "Apple Podcast", "url": "#"},
			},
			Transcript: []map[string]string{
				{"speaker": "Host", "text": "The International Typographic Style is a testament to Swiss design's impact worldwide..."},
			},
		},
		{
			ID:          26,
			Date:        "2025-07-01",
			Title:       "Swiss Design for Startups — Building a Strong Visual Identity",
			Category:    "Business Design",
			Duration:    "01:30h",
			ImagePath:   "/static/images/26.png",
			Description: "How startups can use Swiss design principles to establish impactful and professional branding.",
			Topics: []string{
				"Applying Swiss design principles to brand building.",
				"How simplicity builds trust in startup branding.",
				"Case studies of startups using Swiss design.",
			},
			Sponsors: []map[string]string{
				{"name": "BrandStarter", "description": "Helping startups create timeless branding."},
			},
			Links: []map[string]string{
				{"label": "Swiss branding for startups", "url": "#"},
			},
			ListenOn: []map[string]string{
				{"name": "Spotify", "url": "#"},
				{"name": "Apple Podcast", "url": "#"},
			},
			Transcript: []map[string]string{
				{"speaker": "Host", "text": "Swiss design is a powerful tool for creating strong visual identities..."},
			},
		},
		{
			ID:          27,
			Date:        "2025-03-04",
			Title:       "The Evolution of Helvetica",
			Category:    "Typography",
			Duration:    "02:00h",
			ImagePath:   "/static/images/27.png",
			Description: "A journey through the history of Helvetica, the world's most famous typeface.",
			Topics: []string{
				"The origins of Helvetica and its initial design goals.",
				"How Helvetica has adapted to different mediums.",
				"Why Helvetica remains relevant in modern design.",
			},
			Sponsors: []map[string]string{
				{"name": "TypeHistory", "description": "Explore the legacy of iconic typefaces."},
			},
			Links: []map[string]string{
				{"label": "History of Helvetica", "url": "#"},
			},
			ListenOn: []map[string]string{
				{"name": "Spotify", "url": "#"},
				{"name": "Apple Podcast", "url": "#"},
			},
			Transcript: []map[string]string{
				{"speaker": "Host", "text": "Helvetica is more than just a typeface—it's a cultural icon..."},
			},
		},
	}
}
