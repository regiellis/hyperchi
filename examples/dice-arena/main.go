// Dice Arena: a realtime multiplayer dice game built on HyperChi. A lobby lists
// open tables live; at a table, two to four players take turns at Hot Dice
// while anyone else watches. Every move travels over one WebSocket per page,
// and the server renders each update as HTML for htmx to swap.
package main

import (
	"embed"
	"errors"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/regiellis/hyperchi/hyperchi"
	"github.com/regiellis/hyperchi/hyperchi/config"
)

// The templates are compiled into the binary with the static files (routes.go),
// so the app runs from any working directory.
//
//go:embed templates
var templateFiles embed.FS

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	cfg := config.FromEnv() // ENVIRONMENT=production switches every production default on
	if cfg.IsDevelopment() {
		cfg.Server.PortFallback = true // :8080 taken? take the next free port
	}
	app, _, err := newApp(cfg, appOptions{
		OriginPatterns: splitList(os.Getenv("ALLOWED_ORIGINS")),
		Logging:        true,
	})
	if err != nil {
		return err
	}
	// Serve logs every URL the server answers on (localhost and each LAN
	// address for a 0.0.0.0 bind) and drains connections on SIGINT/SIGTERM.
	return app.Serve(cfg.Server.Address())
}

// appOptions are the settings main reads from the environment, and tests set.
type appOptions struct {
	OriginPatterns []string // extra WebSocket origins allowed in production
	Logging        bool
	Arena          Options
}

// newApp builds the whole application: middleware, WebSocket policy,
// templates, and routes. main and the tests share it.
func newApp(cfg *config.Config, opts appOptions) (*hyperchi.HyperChi, *Arena, error) {
	// No database or asset watcher: game state lives in
	// memory and the templates and static files are embedded.
	cfg.Database.Disabled = true
	cfg.Assets.HotReload = false
	// The player cookie is signed with SECRET_KEY (cfg.Security.SecretKey).
	// Development signs with a random key, so identities reset on restart;
	// production refuses to start without one rather than fail every request.
	if cfg.IsProduction() && cfg.Security.SecretKey == "" {
		return nil, nil, errors.New("dice-arena: SECRET_KEY is required in production to sign player cookies (generate one with: openssl rand -base64 32)")
	}
	app, err := hyperchi.NewWithConfigE(cfg)
	if err != nil {
		return nil, nil, err
	}

	mw := []func(http.Handler) http.Handler{app.Middleware.Recovery()}
	if opts.Logging {
		mw = append(mw, app.Middleware.Logger())
	}
	// CSRF backs up the SameSite=Lax player cookie: the layout hands its token
	// to htmx as an X-CSRF-Token header for every form post.
	mw = append(mw, app.Middleware.HTMX(), app.Middleware.Security(), identityMiddleware(app), app.Middleware.CSRF())
	app.Use(mw...)

	hub := app.Extensions().GetWebSocketHub()
	// A connection's identity comes from the signed cookie the middleware
	// checked, never from anything the client sends over the socket.
	hub.SetIdentifyUser(func(r *http.Request) string { return identityFrom(r.Context()).ID })
	// Development relaxes the origin check so a phone on the LAN can play. In
	// production the hub accepts same-origin handshakes only; a site served
	// under another host name lists it in ALLOWED_ORIGINS.
	if len(opts.OriginPatterns) > 0 {
		hub.SetOriginPatterns(opts.OriginPatterns)
	}
	// Serves the htmx 4 ws extension at /hyperchi/js/hx-ws.js. The app does
	// not call EnableWebSocket: that generic endpoint lets clients join any
	// room and relay messages to it, and every message here goes through the
	// game's own socket routes.
	app.Extensions().EnableExtensionAssets()

	app.SetGlobal("siteName", "Dice Arena")
	app.SetGlobal("maxRolls", MaxRolls)
	app.SetGlobal("maxSeats", MaxSeats)
	app.SetGlobal("development", cfg.IsDevelopment())
	if err := app.LoadTemplatesFS(templateFiles, "templates/**/*.tmpl"); err != nil {
		return nil, nil, err
	}

	arena := NewArena(app, opts.Arena)
	srv := &server{arena: arena, dev: cfg.IsDevelopment()}
	if err := srv.routes(app); err != nil {
		return nil, nil, err
	}
	return app, arena, nil
}

func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}
