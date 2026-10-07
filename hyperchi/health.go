package hyperchi

import (
	"log"
	"net/http"
	"time"
)

// CreateHealthEndpoints adds /health (JSON status), /health/ready (database and
// templates ready), and /health/live (process alive).
func (g *HyperChi) CreateHealthEndpoints() *HyperChi {
	g.Get("/health", g.healthCheck)
	g.Get("/health/ready", g.readiness)
	g.Get("/health/live", func(c *Context) error { return c.Text("Alive") })
	return g
}

// healthCheck reports overall status, uptime, and per-service checks. Failure
// details are logged, never returned: the endpoint is public.
func (g *HyperChi) healthCheck(c *Context) error {
	checks := map[string]string{"database": "not configured"}
	status, code := "healthy", http.StatusOK
	if g.DB != nil {
		checks["database"] = "healthy"
		if err := g.DB.PingContext(c.Ctx()); err != nil {
			log.Printf("hyperchi: health: database: %v", err)
			checks["database"] = "unhealthy"
			status, code = "degraded", http.StatusServiceUnavailable
		}
	}
	checks["profiler"] = "disabled"
	if g.Helpers.Profiler.IsEnabled() {
		checks["profiler"] = "enabled"
	}
	env := "production"
	if g.isDevelopment() {
		env = "development"
	}
	return c.Status(code).JSON(H{
		"status":      status,
		"timestamp":   time.Now().Format(time.RFC3339),
		"environment": env,
		"uptime":      time.Since(g.started).Round(time.Second).String(),
		"checks":      checks,
	})
}

// readiness answers 200 once the database (if any) and templates are ready.
func (g *HyperChi) readiness(c *Context) error {
	if g.DB != nil {
		if err := g.DB.PingContext(c.Ctx()); err != nil {
			log.Printf("hyperchi: readiness: database: %v", err)
			return c.Status(http.StatusServiceUnavailable).Text("Database not ready")
		}
	}
	if g.templates == nil {
		return c.Status(http.StatusServiceUnavailable).Text("Templates not loaded")
	}
	return c.Text("Ready")
}
