package hyperchi

import (
	"errors"
	"path/filepath"

	"github.com/regiellis/hyperchi/hyperchi/config"
)

// ResourceController serves the conventional routes of an HTML resource. Each
// method renders its own response, usually with c.View.
type ResourceController interface {
	Index(c *Context) error  // GET    /pattern
	New(c *Context) error    // GET    /pattern/new
	Create(c *Context) error // POST   /pattern
	Show(c *Context) error   // GET    /pattern/{id}
	Edit(c *Context) error   // GET    /pattern/{id}/edit
	Update(c *Context) error // PUT and PATCH /pattern/{id}
	Delete(c *Context) error // DELETE /pattern/{id}
}

// Resource registers the seven ResourceController routes under pattern.
// Optional middleware applies to all of them.
func (g *HyperChi) Resource(pattern string, rc ResourceController, mw ...Middleware) {
	g.Get(pattern, rc.Index, mw...)
	g.Get(pattern+"/new", rc.New, mw...)
	g.Post(pattern, rc.Create, mw...)
	g.Get(pattern+"/{id}", rc.Show, mw...)
	g.Get(pattern+"/{id}/edit", rc.Edit, mw...)
	g.Put(pattern+"/{id}", rc.Update, mw...)
	g.Patch(pattern+"/{id}", rc.Update, mw...)
	g.Delete(pattern+"/{id}", rc.Delete, mw...)
}

// RESTController serves the routes of a JSON API resource.
type RESTController interface {
	Index(c *Context) error  // GET    /pattern
	Create(c *Context) error // POST   /pattern
	Show(c *Context) error   // GET    /pattern/{id}
	Update(c *Context) error // PUT and PATCH /pattern/{id}
	Delete(c *Context) error // DELETE /pattern/{id}
}

// RESTful registers the RESTController routes under pattern. Their errors are
// always JSON (see JSONErrors).
func (g *HyperChi) RESTful(pattern string, rc RESTController, mw ...Middleware) {
	mw = append([]Middleware{JSONErrors}, mw...)
	g.Get(pattern, rc.Index, mw...)
	g.Post(pattern, rc.Create, mw...)
	g.Get(pattern+"/{id}", rc.Show, mw...)
	g.Put(pattern+"/{id}", rc.Update, mw...)
	g.Patch(pattern+"/{id}", rc.Update, mw...)
	g.Delete(pattern+"/{id}", rc.Delete, mw...)
}

// Health registers a JSON liveness endpoint at pattern that reports the
// framework version. CreateHealthEndpoints adds readiness checks.
func (g *HyperChi) Health(pattern string) {
	g.Get(pattern, func(c *Context) error {
		return c.JSON(H{"status": "healthy", "service": "hyperchi", "version": config.FrameworkVersion()})
	})
}

// Status registers a JSON endpoint at pattern with the framework version, a
// feature list, and cache statistics.
func (g *HyperChi) Status(pattern string) {
	g.Get(pattern, g.statusHandler)
}

func (g *HyperChi) statusHandler(c *Context) error {
	return c.JSON(H{
		"status":     "online",
		"framework":  "HyperChi",
		"version":    config.FrameworkVersion(),
		"features":   []string{"routing", "validation", "content", "events", "cache"},
		"cache":      g.Cache.Stats(),
		"middleware": "enabled",
	})
}

// Metrics registers an endpoint with cache statistics and the number of
// content collections, schemas, and event names.
func (g *HyperChi) Metrics(pattern string) {
	g.Get(pattern, g.metricsHandler)
}

func (g *HyperChi) metricsHandler(c *Context) error {
	return c.JSON(H{
		"cache":   g.Cache.Stats(),
		"content": g.Content.collectionCount(),
		"schemas": g.Schemas.Len(),
		"events":  len(g.Events.EventStats()),
	})
}

// Debug adds development middleware and the /_debug/status, metrics, cache,
// and events endpoints. The endpoints answer 404 whenever the app is not in
// development (see Production), checked on each request, so a Debug call
// left in production code exposes nothing.
func (g *HyperChi) Debug() *HyperChi {
	g.Use(g.Middleware.Development())
	g.Use(g.Middleware.NoCache())
	g.Get("/_debug/status", g.developmentOnly(g.statusHandler))
	g.Get("/_debug/metrics", g.developmentOnly(g.metricsHandler))
	g.Get("/_debug/cache", g.developmentOnly(func(c *Context) error { return c.JSON(g.Cache.Stats()) }))
	g.Get("/_debug/events", g.developmentOnly(func(c *Context) error {
		stats := g.Events.EventStats()
		return c.JSON(H{"subscribers": len(stats), "events": stats})
	}))
	return g
}

// developmentOnly answers 404 unless the app is in development.
func (g *HyperChi) developmentOnly(h Handler) Handler {
	return func(c *Context) error {
		if !g.isDevelopment() {
			return NotFound("")
		}
		return h(c)
	}
}

// ContentRouteConfig configures ContentRoutes.
type ContentRouteConfig struct {
	// Templates, defaulting to "<collection>/index" and "<collection>/item".
	// They receive "collection" and "items", or "item".
	IndexTemplate string
	ItemTemplate  string

	// Query defaults for the index.
	DefaultFilters map[string]any
	SortBy         string
	SortOrder      string // "asc" or "desc"
	Limit          int

	// Optional handlers that replace the default responses.
	IndexHandler    func(c *Context, items []*ContentItem) error
	ItemHandler     func(c *Context, item *ContentItem) error
	NotFoundHandler func(c *Context, slug string) error

	// EnableCRUD adds JSON create, update, and delete routes. The optional
	// handlers replace the default JSON decoding.
	EnableCRUD    bool
	CreateHandler func(c *Context) (*ContentItem, error)
	UpdateHandler func(c *Context, item *ContentItem) (*ContentItem, error)
	DeleteHandler func(c *Context, item *ContentItem) (bool, error)
}

// ContentRoutes serves a content collection: an index at prefix and one item
// at prefix/{slug}, rendered with View so htmx requests get fragments.
func (g *HyperChi) ContentRoutes(prefix, collection string, cfg ContentRouteConfig) {
	indexTemplate := cfg.IndexTemplate
	if indexTemplate == "" {
		indexTemplate = collection + "/index"
	}
	itemTemplate := cfg.ItemTemplate
	if itemTemplate == "" {
		itemTemplate = collection + "/item"
	}

	g.Get(prefix, func(c *Context) error {
		query := g.Content.Query(collection)
		for field, value := range cfg.DefaultFilters {
			query = query.Where(field, value)
		}
		if cfg.SortBy != "" {
			query = query.SortBy(cfg.SortBy, cfg.SortOrder)
		}
		if cfg.Limit > 0 {
			query = query.Limit(cfg.Limit)
		}
		items, err := query.All()
		if err != nil {
			return err
		}
		if cfg.IndexHandler != nil {
			return cfg.IndexHandler(c, items)
		}
		return c.View(indexTemplate, H{"collection": collection, "items": items})
	})

	g.Get(prefix+"/{slug}", func(c *Context) error {
		slug := c.Param("slug")
		item, err := g.Content.Get(collection, slug)
		if err != nil {
			if cfg.NotFoundHandler != nil {
				return cfg.NotFoundHandler(c, slug)
			}
			return NotFound("Content not found.").Wrap(err)
		}
		if cfg.ItemHandler != nil {
			return cfg.ItemHandler(c, item)
		}
		return c.View(itemTemplate, H{"item": item})
	})

	if cfg.EnableCRUD {
		g.contentCRUDRoutes(prefix, collection, cfg)
	}
}

func (g *HyperChi) contentCRUDRoutes(prefix, collection string, cfg ContentRouteConfig) {
	// These routes speak JSON in both directions, so their errors do too.
	g.Post(prefix, func(c *Context) error {
		var item *ContentItem
		if cfg.CreateHandler != nil {
			created, err := cfg.CreateHandler(c)
			if err != nil {
				return err
			}
			item = created
		} else {
			var data map[string]any
			if err := c.decodeJSONMap(&data); err != nil {
				return err
			}
			slug, _ := data["slug"].(string)
			if slug == "" {
				return Unprocessable("Please correct the highlighted fields.", map[string][]string{"slug": {"is required"}})
			}
			item = &ContentItem{Slug: slug, Path: filepath.Join(collection, slug+".md"), Metadata: data}
		}
		if err := g.Content.Create(collection, item); err != nil {
			var he *HTTPError
			if errors.As(err, &he) {
				return he
			}
			return Conflict(err.Error()).Wrap(err)
		}
		return c.Status(201).JSON(H{"success": true, "item": item})
	}, JSONErrors)

	update := func(c *Context) error {
		slug := c.Param("slug")
		item, err := g.Content.Get(collection, slug)
		if err != nil {
			return NotFound("Content not found.").Wrap(err)
		}
		if cfg.UpdateHandler != nil {
			updated, err := cfg.UpdateHandler(c, item)
			if err != nil {
				return err
			}
			return c.JSON(H{"success": true, "item": updated})
		}
		var updates map[string]any
		if err := c.decodeJSONMap(&updates); err != nil {
			return err
		}
		if err := g.Content.Update(collection, slug, updates); err != nil {
			return err
		}
		return c.JSON(H{"success": true, "item": item})
	}
	g.Put(prefix+"/{slug}", update, JSONErrors)
	g.Patch(prefix+"/{slug}", update, JSONErrors)

	g.Delete(prefix+"/{slug}", func(c *Context) error {
		slug := c.Param("slug")
		item, err := g.Content.Get(collection, slug)
		if err != nil {
			return NotFound("Content not found.").Wrap(err)
		}
		if cfg.DeleteHandler != nil {
			ok, err := cfg.DeleteHandler(c, item)
			if err != nil {
				return err
			}
			if !ok {
				return c.JSON(H{"success": false})
			}
		}
		if err := g.Content.Delete(collection, slug); err != nil {
			return err
		}
		return c.JSON(H{"success": true})
	}, JSONErrors)
}
