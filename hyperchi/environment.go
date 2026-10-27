package hyperchi

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/regiellis/hyperchi/hyperchi/config"
)

// StartAssetWatcher starts watching the asset source directory for hot
// reload. It fails when the app has no asset watcher.
func (g *HyperChi) StartAssetWatcher() error {
	if g.Assets != nil {
		return g.Assets.Start()
	}
	return fmt.Errorf("asset watcher not initialized")
}

// StopAssetWatcher stops the asset watcher, logging a failure. Serve calls it
// on shutdown.
func (g *HyperChi) StopAssetWatcher() {
	if g.Assets != nil {
		if err := g.Assets.Stop(); err != nil {
			log.Printf("hyperchi: failed to stop asset watcher: %v", err)
		}
	}
}

// isDevMode reads development mode straight from ENVIRONMENT or GO_ENV: unset,
// "development", or "dev". The app itself decides from its configuration
// (isDevelopment).
func isDevMode() bool {
	env := os.Getenv("ENVIRONMENT")
	if env == "" {
		env = os.Getenv("GO_ENV")
	}
	return env == "" || env == "development" || env == "dev"
}

// Production switches the app to production: environment "production", no
// debug or hot reload, generic error pages, the /_debug endpoints (see Debug)
// answering 404, and the Security and Recovery middleware (installed with
// chi's Use, so call it before registering routes).
func (g *HyperChi) Production() *HyperChi {
	g.Configure().Env("production")
	g.Config.Development.Debug = false
	g.Config.Assets.HotReload = false
	g.ErrorPageHandler.isDev = false
	g.ErrorPageHandler.tracker.isDev = false
	g.Use(g.Middleware.Security())
	g.Use(g.Middleware.Recovery())

	log.Println("hyperchi: production mode: /_debug endpoints answer 404")

	return g
}

// Configure returns a chainable editor for the app's configuration. Use it
// during setup, before serving.
func (g *HyperChi) Configure() *AppConfig {
	return &AppConfig{app: g}
}

// AppConfig edits an app's configuration; see Configure.
type AppConfig struct {
	app *HyperChi
}

// Port sets the server port on the application's configuration.
func (c *AppConfig) Port(port int) *AppConfig {
	c.app.Config.Server.Port = port
	return c
}

// Env sets the environment ("development" or "production") and updates the
// error pages and the CORS default to match.
func (c *AppConfig) Env(env string) *AppConfig {
	c.app.Config.Server.Environment = env
	c.app.ErrorPageHandler.isDev = c.app.Config.IsDevelopment()
	c.app.ErrorPageHandler.tracker.isDev = c.app.Config.IsDevelopment()
	c.app.Middleware.corsOrigins = c.app.Config.Security.CORSOrigins
	if c.app.Middleware.corsOrigins == "" && c.app.Config.IsDevelopment() {
		c.app.Middleware.corsOrigins = "*"
	}
	return c
}

func (g *HyperChi) isDevelopment() bool { return g.Config != nil && g.Config.IsDevelopment() }

// LoadEnv loads environment variables from the named files, or from .env,
// .env.local, and .env.development (those that exist) when none are named.
func (g *HyperChi) LoadEnv(filenames ...string) error {
	if len(filenames) == 0 {
		return g.Helpers.Env.LoadDefault()
	}
	return g.Helpers.Env.LoadFiles(filenames...)
}

// IsDevelopment reports whether the app's configuration is in development
// mode. NewFromEnv takes it from ENVIRONMENT or GO_ENV, unset meaning
// development; New uses config.Default, which is development.
func (g *HyperChi) IsDevelopment() bool { return g.isDevelopment() }

// IsProduction reports the opposite of IsDevelopment.
func (g *HyperChi) IsProduction() bool { return !g.isDevelopment() }

// ValidateConfig checks the PORT, LOG_LEVEL, and environment (ENVIRONMENT,
// else GO_ENV) variables and returns an error for each invalid one. The
// environment follows config.ParseEnvironment, the rule config.FromEnv and
// Config.Validate apply. It does not inspect app.Config; app.Config.Validate
// does that.
func (g *HyperChi) ValidateConfig() []error {
	var errors []error

	if portStr := os.Getenv("PORT"); portStr != "" {
		if port, err := strconv.Atoi(portStr); err != nil || port < 1 || port > 65535 {
			errors = append(errors, fmt.Errorf("invalid PORT value: %s (must be 1-65535)", portStr))
		}
	}

	if logLevel := os.Getenv("LOG_LEVEL"); logLevel != "" {
		validLevels := []string{"debug", "info", "warn", "error", "fatal"}
		valid := false
		for _, level := range validLevels {
			if strings.ToLower(logLevel) == level {
				valid = true
				break
			}
		}
		if !valid {
			errors = append(errors, fmt.Errorf("invalid LOG_LEVEL: %s (must be one of: %v)", logLevel, validLevels))
		}
	}

	if env := config.EnvironmentFromEnv(); env != "" {
		if _, err := config.ParseEnvironment(env); err != nil {
			errors = append(errors, fmt.Errorf("ENVIRONMENT or GO_ENV: %w", err))
		}
	}

	return errors
}
