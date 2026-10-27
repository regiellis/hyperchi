package hyperchi

import (
	"embed"
	"fmt"
	"html/template"
	"log"
	"net/http"

	"github.com/regiellis/hyperchi/hyperchi/extensions"
	"github.com/regiellis/hyperchi/hyperchi/extensions/response_targets"
	"github.com/regiellis/hyperchi/hyperchi/extensions/sse"
	"github.com/regiellis/hyperchi/hyperchi/extensions/websocket"
	"github.com/regiellis/hyperchi/hyperchi/patterns"
	"github.com/regiellis/hyperchi/hyperchi/security"
)

// extensionScripts embeds the browser-side htmx 4 extension scripts served at
// /hyperchi/js/*, so they load whatever the working directory and whether or not
// the source tree exists at runtime (a deployed binary has none). The source of
// truth is frontend/assets/scripts/. Run `task sync-runtime-assets` to refresh
// these copies after editing an extension.
//
//go:embed extensions/scripts/hx-sse.js extensions/scripts/hx-ws.js
var extensionScripts embed.FS

// ExtensionIntegration connects the htmx extensions (SSE, WebSocket, response
// targets) and the pattern manager to an app. Reach it with app.Extensions().
type ExtensionIntegration struct {
	app            *HyperChi
	registry       *extensions.Registry
	sseExtension   *sse.SSEExtension
	wsExtension    *websocket.WebSocketExtension
	rtExtension    *response_targets.ResponseTargetsExtension
	patternManager *patterns.PatternManager
}

// loadScriptAsset returns the embedded extension script name. A name with no
// embedded script is a programming error (the //go:embed list is fixed), so it
// panics rather than serving a broken stub.
func loadScriptAsset(name string) []byte {
	data, err := extensionScripts.ReadFile("extensions/scripts/" + name)
	if err != nil {
		panic(fmt.Sprintf("hyperchi: embedded extension script %q not found: %v", name, err))
	}
	return data
}

// NewExtensionIntegration creates the extension integration for app and
// registers the built-in SSE, WebSocket, and Response Targets extensions. A
// registration failure is returned rather than logged, so a caller can tell that
// real-time support is not available.
func NewExtensionIntegration(app *HyperChi) (*ExtensionIntegration, error) {
	return newExtensionIntegration(app, extensions.NewRegistry())
}

// newExtensionIntegration registers the core extensions into registry. Tests
// pass a registry that already holds an extension to exercise the failure path.
func newExtensionIntegration(app *HyperChi, registry *extensions.Registry) (*ExtensionIntegration, error) {
	ei := &ExtensionIntegration{
		app:          app,
		registry:     registry,
		sseExtension: sse.NewSSEExtension(),
		wsExtension:  websocket.NewWebSocketExtension(),
		rtExtension:  response_targets.NewResponseTargetsExtension(),
	}
	if err := ei.registry.Register(ei.sseExtension); err != nil {
		return nil, fmt.Errorf("hyperchi: register SSE extension: %w", err)
	}
	if err := ei.registry.Register(ei.wsExtension); err != nil {
		return nil, fmt.Errorf("hyperchi: register WebSocket extension: %w", err)
	}
	if err := ei.registry.Register(ei.rtExtension); err != nil {
		return nil, fmt.Errorf("hyperchi: register Response Targets extension: %w", err)
	}
	ei.patternManager = patterns.NewPatternManager(patternHost{ei.app}, ei.sseExtension.GetHub())
	return ei, nil
}

// EnableSSE serves the SSE hub at endpoint:
//
//	app.Extensions().EnableSSE("/events")
func (ei *ExtensionIntegration) EnableSSE(endpoint string) {
	ei.app.router.HandleFunc(endpoint, ei.sseExtension.GetHub().HTTPHandler())
	log.Printf("hyperchi: SSE enabled at endpoint %s", endpoint)
}

// EnableWebSocket serves the WebSocket hub at endpoint:
//
//	app.Extensions().EnableWebSocket("/ws")
//
// When the app's configuration is in development mode, origin verification is
// relaxed so a remote browser can connect, for example through a Vite dev
// server bound to 0.0.0.0 and reached over SSH or the LAN. In production the
// hub keeps its same-origin default. To allow other origins, call
// app.Extensions().GetWebSocketHub().SetOriginPatterns(...) before this; an
// explicit policy is kept in every environment.
//
// Rooms a page names in the URL (?rooms=lobby) are joined at the handshake;
// authorize that request in HTTP middleware. A client's join_room message is
// refused until the app decides who may join what with
// GetWebSocketHub().SetRoomAuthorizer, which then also checks ?rooms=.
func (ei *ExtensionIntegration) EnableWebSocket(endpoint string) {
	hub := ei.prepareWebSocketHub(endpoint)
	ei.app.router.HandleFunc(endpoint, hub.HTTPHandler())
	log.Printf("hyperchi: WebSocket enabled at endpoint %s", endpoint)
}

// prepareWebSocketHub applies the framework defaults shared by EnableWebSocket
// and app.WebSocket routes: a server-side identity from the authenticated
// session (never client input; an explicit SetIdentifyUser wins) and, outside
// production, a relaxed origin policy unless the app set its own.
func (ei *ExtensionIntegration) prepareWebSocketHub(endpoint string) *websocket.WebSocketHub {
	hub := ei.wsExtension.GetHub()
	hub.SetDefaultIdentifyUser(func(r *http.Request) string {
		return security.GetUserIDFromContext(r.Context())
	})
	if ei.app.isDevelopment() && hub.UseDevelopmentOriginPolicy() {
		log.Printf("hyperchi: WebSocket development mode: origin verification relaxed for %s; set OriginPatterns for production", endpoint)
	}
	return hub
}

// EnableAllExtensions installs the extension middleware, serves SSE at /sse
// and WebSocket at /ws, and serves the extension scripts (see
// EnableExtensionAssets). It may run before or after routes are registered.
func (ei *ExtensionIntegration) EnableAllExtensions() {
	// Deferred global middleware is applied at Serve time. chi's Use would panic
	// ("all middlewares must be defined before routes") once a route exists.
	ei.app.UseGlobal(ei.registry.Middleware())

	ei.EnableSSE("/sse")
	ei.EnableWebSocket("/ws")

	ei.EnableExtensionAssets()

	log.Printf("hyperchi: all HTMX extensions enabled")
}

// EnableExtensionAssets serves the htmx 4 SSE and WebSocket extension scripts
// at /hyperchi/js/hx-sse.js and /hyperchi/js/hx-ws.js, and a JSON list of
// registered extensions at /htmx/extensions.
func (ei *ExtensionIntegration) EnableExtensionAssets() {
	serveJS := func(content []byte) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(content)
		}
	}
	// Framework scripts live under /hyperchi/js/ to stay clear of an app's static routes.
	ei.app.router.HandleFunc("/hyperchi/js/hx-sse.js", serveJS(loadScriptAsset("hx-sse.js")))
	ei.app.router.HandleFunc("/hyperchi/js/hx-ws.js", serveJS(loadScriptAsset("hx-ws.js")))

	ei.app.Get("/htmx/extensions", func(c *Context) error {
		info := ei.registry.GetInfo()
		return c.JSON(H{"extensions": info, "total": len(info)})
	})
}

// patternHost gives the pattern manager raw route registration plus access to
// the app's loaded templates, without exposing a net/http route API on the app.
type patternHost struct{ app *HyperChi }

func (p patternHost) HandleFunc(pattern string, h http.HandlerFunc) {
	p.app.router.HandleFunc(pattern, h)
}

func (p patternHost) LookupTemplate(name string) *template.Template {
	return p.app.LookupTemplate(name)
}

// RespondStatus renders a pattern's refusal through the app's error pipeline
// (OnError or DefaultError), so patterns answer like every other denial.
func (p patternHost) RespondStatus(w http.ResponseWriter, r *http.Request, code int, err error) {
	p.app.respondStatus(w, r, code, err)
}

// patternHost keeps multi-step form answers in the request's KV session.
var _ patterns.WizardStoreHost = patternHost{}

// WizardStore returns a store over the request's KV session, or
// patterns.ErrNoWizardStore when the route does not run behind
// SessionMiddleware.
func (p patternHost) WizardStore(r *http.Request) (patterns.WizardStore, error) {
	session := GetSessionFromContext(r)
	if session == nil {
		return nil, patterns.ErrNoWizardStore
	}
	return sessionWizardStore{session}, nil
}

// sessionWizardStore keeps a wizard's answers as a string session value, which
// survives the session's JSON round trip unchanged. SessionMiddleware saves the
// session with the response.
type sessionWizardStore struct{ session *KVSession }

func (s sessionWizardStore) Load(key string) ([]byte, error) {
	value := s.session.GetString(key)
	if value == "" {
		return nil, nil
	}
	return []byte(value), nil
}

func (s sessionWizardStore) Save(key string, data []byte) error {
	s.session.Set(key, string(data))
	return nil
}

func (s sessionWizardStore) Delete(key string) error {
	s.session.Delete(key)
	return nil
}

// GetSSEHub returns the SSE hub, or nil when the integration has no SSE
// extension:
//
//	hub := app.Extensions().GetSSEHub()
//	hub.Broadcast("updates", sse.SSEMessage{
//		Event: "data-update",
//		Data:  newData,
//	})
func (ei *ExtensionIntegration) GetSSEHub() *sse.SSEHub {
	if ei.sseExtension != nil {
		return ei.sseExtension.GetHub()
	}
	return nil
}

// GetWebSocketHub returns the WebSocket hub that EnableWebSocket and
// app.WebSocket routes share, or nil when the integration has no WebSocket
// extension. Route handlers usually broadcast with app.Room instead:
//
//	hub := app.Extensions().GetWebSocketHub()
//	hub.BroadcastToRoom("chat", websocket.WebSocketMessage{
//		Type: "message",
//		Data: message,
//	})
func (ei *ExtensionIntegration) GetWebSocketHub() *websocket.WebSocketHub {
	if ei.wsExtension != nil {
		return ei.wsExtension.GetHub()
	}
	return nil
}

// GetPatternManager returns the manager for the htmx UI patterns in the
// patterns package:
//
//	patterns := app.Extensions().GetPatternManager()
//	patterns.LiveFeed("/dashboard-feed", patterns.LiveFeedConfig{
//		Channel: "dashboard",
//		DataFunc: getDashboardData,
//		Template: "dashboard.html",
//	})
func (ei *ExtensionIntegration) GetPatternManager() *patterns.PatternManager {
	return ei.patternManager
}

// GetRegistry returns the extension registry.
func (ei *ExtensionIntegration) GetRegistry() *extensions.Registry {
	return ei.registry
}

// RegisterExtension adds a custom extension to the registry. It returns the
// registry's error, for example when the name is already taken:
//
//	customExt := &MyCustomExtension{}
//	app.Extensions().RegisterExtension(customExt)
func (ei *ExtensionIntegration) RegisterExtension(ext extensions.Extension) error {
	return ei.registry.Register(ext)
}

// GetExtensionScripts returns the script paths the named extensions (and
// their dependencies) need, so a page can load only what it uses:
//
//	scripts := app.Extensions().GetExtensionScripts([]string{"sse", "ws"})
func (ei *ExtensionIntegration) GetExtensionScripts(extensionNames []string) []string {
	return ei.registry.GetScripts(extensionNames)
}

// GetExtensionStyles returns the stylesheet paths the named extensions need.
func (ei *ExtensionIntegration) GetExtensionStyles(extensionNames []string) []string {
	return ei.registry.GetStyles(extensionNames)
}

// Extensions returns the app's HTMX extension integration, created with the
// app. An app built without New or NewWithConfig has none; Extensions then
// builds one and panics if the built-in extensions fail to register, since that
// is a framework defect rather than a runtime condition.
func (app *HyperChi) Extensions() *ExtensionIntegration {
	if app.extensions == nil {
		ei, err := NewExtensionIntegration(app)
		if err != nil {
			panic(err)
		}
		app.extensions = ei
	}
	return app.extensions
}
