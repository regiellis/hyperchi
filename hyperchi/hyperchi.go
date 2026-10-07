package hyperchi

import (
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/regiellis/hyperchi/hyperchi/assets"
	"github.com/regiellis/hyperchi/hyperchi/config"
	"github.com/regiellis/hyperchi/hyperchi/database"
	"github.com/regiellis/hyperchi/hyperchi/extensions/sse"
	"github.com/regiellis/hyperchi/hyperchi/extensions/websocket"
	"github.com/regiellis/hyperchi/hyperchi/filters"
	"github.com/regiellis/hyperchi/hyperchi/helpers"
	"github.com/regiellis/hyperchi/hyperchi/integrations"
)

// HelpersGroup holds the app-owned helper services, reached as app.Helpers.
type HelpersGroup struct {
	ErrorHandler *helpers.ErrorHandler
	Logger       *helpers.Logger           // leveled logger
	HTTP         *helpers.HTTPClient       // outbound HTTP client
	Profiler     *helpers.Profiler         // request timing (StartProfiler)
	Env          *helpers.Env              // environment and .env files
	Air          *helpers.AirHelper        // Air live-reload setup
	Deploy       *helpers.DeploymentHelper // Dockerfile, compose, and systemd generation
}

// hcCtxKey is a private context-key type so values set by the framework
// cannot collide with other packages' string keys (staticcheck SA1029).
type hcCtxKey int

const (
	validatedDataCtxKey hcCtxKey = iota
	hxValuesCtxKey
	csrfTokenCtxKey
	requestIDCtxKey
	jsonErrorsCtxKey
)

type applicationClose struct {
	once sync.Once
	err  error
}

// HyperChi is the application: router, middleware, templates, and the services
// (database, KV stores, sessions, cache, events, content) that handlers reach.
type HyperChi struct {
	integrations        *integrations.Manager
	closeState          *applicationClose
	router              chi.Router
	templates           *template.Template
	funcMap             template.FuncMap
	Config              *config.Config
	Middleware          *MiddlewareGroup
	Content             *ContentManager
	Schemas             *SchemaRegistry // named schemas for c.ValidateSchema
	Events              *EventBus
	Cache               *CacheManager
	DB                  *database.DB
	Helpers             *HelpersGroup
	Assets              *assets.Watcher
	Data                map[string]any        // JSON from data/, loaded by LoadTemplatesWithLayout
	ErrorPageHandler    *ErrorPageHandler     // error pages and panic recovery
	hotReloadRouteSetup bool                  // the hot-reload route is registered
	templatesLoaded     bool                  // tracks if templates were explicitly loaded
	extensions          *ExtensionIntegration // HTMX extensions integration
	globals             map[string]any        // template values set with SetGlobal
	started             time.Time             // construction time, for health uptime
	onError             ErrorFunc             // replaces DefaultError when set (OnError)
	clock               schedulerClock        // scheduler time source; nil means the real clock

	// deferredMiddleware is global middleware wrapped around the router at
	// Serve time (see UseGlobal), because chi's Use panics once a route exists.
	deferredMiddleware []func(http.Handler) http.Handler

	kvRegistry *KVRegistry     // named KV store backends
	kvSessions *KVSessionStore // session store, once configured
	kvCache    *KVCache        // KV cache, once configured

	keys *keyring // application secret keys for signed cookies and flashes

	staticAssets *assetRegistry // static mounts, for the asset template function and AssetURL

	// routePrefix is the path this app's router is mounted under: "" for the
	// app, "/admin" inside Group("/admin", ...). chi sub-routers match on the
	// remaining path but leave r.URL.Path whole, so static mounts strip it.
	routePrefix string
}

// New returns an app built from config.Default(). It panics when the app
// cannot be initialized; see NewWithConfigE.
func New() *HyperChi {
	return NewWithConfig(config.Default())
}

// NewFromEnv returns an app built from config.FromEnv(), which reads the
// environment variables. It panics like New.
func NewFromEnv() *HyperChi {
	return NewWithConfig(config.FromEnv())
}

// NewProduction returns an app built from config.ProductionFromEnv(): the
// environment variables apply, as in NewFromEnv, and the app runs in
// production mode whatever ENVIRONMENT says. It panics like New.
func NewProduction() *HyperChi {
	return NewWithConfig(config.ProductionFromEnv())
}

// NewWithConfigE builds an app from a copy of cfg, so later changes to cfg do
// not reach the running app. It returns an error for an invalid configuration
// or a failed initialization (such as the database), where NewWithConfig
// panics. Prefer it where the host process must not stop.
func NewWithConfigE(cfg *config.Config) (*HyperChi, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("hyperchi: invalid configuration: %w", err)
	}
	owned := *cfg
	owned.Security.TrustedProxies = append([]string(nil), cfg.Security.TrustedProxies...)
	owned.Security.SecretKeyPrevious = append([]string(nil), cfg.Security.SecretKeyPrevious...)
	cfg = &owned
	return newWithConfig(cfg)
}

// NewWithConfig builds an app from cfg and panics on an invalid configuration
// or a failed initialization. Use NewWithConfigE to handle the error.
func NewWithConfig(cfg *config.Config) *HyperChi {
	app, err := NewWithConfigE(cfg)
	if err != nil {
		panic(err)
	}
	return app
}

func newWithConfig(cfg *config.Config) (*HyperChi, error) {
	router := chi.NewRouter()

	// The SSE and WebSocket attribute helpers are registered here so templates
	// can use them before any extension is enabled.
	funcMap := filters.FuncMap()
	for _, helpers := range []template.FuncMap{sse.TemplateFuncs(), websocket.TemplateFuncs()} {
		for name, fn := range helpers {
			funcMap[name] = fn
		}
	}

	funcMap["json"] = func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			log.Printf("hyperchi: json template func: marshal %T: %v", v, err)
			return "{\"error\": \"json_marshal_failed\"}"
		}
		return string(b)
	}

	// A placeholder until the app exists; replaced below.
	funcMap["hotReloadScript"] = func() template.HTML {
		return ""
	}

	cacheManager := newCacheManager(&cfg.Cache)
	eventBus := newEventBus()

	var db *database.DB
	if !cfg.Database.Disabled {
		var err error
		db, err = database.NewDB(databaseConfigFrom(cfg))
		if err != nil {
			return nil, fmt.Errorf("hyperchi: initialize database: %w", err)
		}
	}

	app := &HyperChi{
		integrations:     integrations.New(),
		closeState:       &applicationClose{},
		router:           router,
		funcMap:          funcMap,
		Config:           cfg,
		Middleware:       newMiddlewareGroup(cfg),
		Cache:            cacheManager,
		Events:           eventBus,
		DB:               db,
		Helpers:          newHelpersGroup(),
		Data:             make(map[string]any),
		globals:          make(map[string]any),
		started:          time.Now(),
		ErrorPageHandler: NewErrorPageHandler(cfg.IsDevelopment()),
		kvRegistry:       NewKVRegistry(),
		keys:             newKeyring(cfg),
		staticAssets:     &assetRegistry{},
	}
	app.funcMap["asset"] = app.assetTemplateFunc

	// Create extensions now rather than on first use: Group copies the app, and
	// every copy must share one WebSocket hub so rooms and broadcasts reach
	// sockets registered in any group.
	extensionIntegration, err := NewExtensionIntegration(app)
	if err != nil {
		return nil, err
	}
	app.extensions = extensionIntegration

	app.Content = newContentManager(cacheManager, eventBus)
	app.Content.app = app // LoadDir watches in development and stops on Close
	app.Schemas = newSchemaRegistry()

	assetConfig := assetConfigFrom(cfg.Assets)
	assetPipeline := assets.NewWithConfig(assetConfig)
	app.Assets = assets.NewWatcher(assetConfig, assetPipeline)

	// The hot-reload route itself is registered with the first route
	// (ensureHotReloadRoute), after the app has had a chance to add middleware.
	app.funcMap["hotReloadScript"] = func() template.HTML {
		if app.Assets != nil && cfg.Assets.HotReload {
			return template.HTML(app.Assets.GetHotReloadScript())
		}
		return ""
	}

	// Router misses, static misses, and middleware denials render through the
	// same pipeline as a Handler's returned error (DefaultError or OnError).
	app.Middleware.errors = app.ErrorPageHandler
	app.Middleware.respond = app.respondStatus
	app.ErrorPageHandler.ResponseHandler = app.respondStatus // panics from Middleware.Recovery
	app.router.NotFound(func(w http.ResponseWriter, r *http.Request) { app.respondStatus(w, r, http.StatusNotFound, nil) })
	app.router.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		app.respondStatus(w, r, http.StatusMethodNotAllowed, nil)
	})
	return app, nil
}

// newHelpersGroup returns helpers rooted at the working directory.
func newHelpersGroup() *HelpersGroup {
	return &HelpersGroup{
		ErrorHandler: helpers.NewErrorHandler(),
		Logger:       helpers.NewLogger(),
		HTTP:         helpers.NewHTTPClient(),
		Profiler:     helpers.NewProfiler(),
		Env:          helpers.NewEnv(),
		Air:          helpers.NewAirHelper("."),
		Deploy:       helpers.NewDeploymentHelper(".", "hyperchi-app"),
	}
}

// StartProfiler enables app.Helpers.Profiler and installs its timing
// middleware with chi's Use, so call it before registering routes.
func (g *HyperChi) StartProfiler() *HyperChi {
	g.Helpers.Profiler.Enable()
	g.Use(g.Helpers.Profiler.Middleware())
	return g
}

// StopProfiler disables app.Helpers.Profiler. Its middleware stays installed
// and stops recording.
func (g *HyperChi) StopProfiler() *HyperChi {
	g.Helpers.Profiler.Disable()
	return g
}

// assetConfigFrom maps the app's asset settings onto the asset pipeline's.
func assetConfigFrom(a config.AssetsConfig) *assets.Config {
	return &assets.Config{
		SourceDir:    a.SourceDir,
		OutputDir:    a.OutputDir,
		HotReload:    a.HotReload,
		EnableMinify: a.Minify,
	}
}

// databaseConfigFrom maps the app's database settings onto the database
// package's.
func databaseConfigFrom(cfg *config.Config) *database.DBConfig {
	return &database.DBConfig{
		Path: cfg.Database.DSN(), Timeout: cfg.Database.Timeout,
		MaxOpenConns: cfg.Database.MaxConns, MaxIdleConns: cfg.Database.MaxConns / 2,
		Debug: cfg.Database.LogQueries,
		// Create the file on first use, not at startup: an app (or a test)
		// that never touches the database should not leave one behind.
		Lazy: true,
	}
}
