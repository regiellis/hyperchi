package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/regiellis/hyperchi/hyperchi"
	"github.com/regiellis/hyperchi/hyperchi/config"
	"github.com/regiellis/hyperchi/hyperchi/database"
	"github.com/regiellis/hyperchi/hyperchi/integrations"
	gc "github.com/regiellis/hyperchi/modules/gomponents"
	g "maragu.dev/gomponents"
	h "maragu.dev/gomponents/html"
)

//go:embed static
var staticFS embed.FS

const (
	renderer          = "gomponents"
	recipientsPerPage = 25
	outboxPerPage     = 25
	auditPerPage      = 50
)

// Options configure a Server. main fills them from the environment; tests
// fill them directly.
type Options struct {
	Config *config.Config
	// SessionPath is the bbolt file that keeps sessions across restarts.
	// Empty keeps sessions in memory.
	SessionPath string
	// Pace is a pause between recipients, so a person can watch a send-out
	// and cancel it. Zero renders as fast as the database allows.
	Pace time.Duration
	// ProgressEvery limits how often a running job publishes progress.
	// Zero publishes after every recipient.
	ProgressEvery time.Duration
	// MaxUpload caps a CSV upload in bytes.
	MaxUpload int64
	// From is the From header written into exported .eml files.
	From string
	// SeedDemo adds a demo list and letter on first run.
	SeedDemo bool
	// Admin creates this account at startup when no user exists yet.
	AdminEmail, AdminPassword, AdminName string
	// Shutdown ends running jobs and open event streams when it is done.
	Shutdown context.Context
}

// Server is the mailgrid application: a HyperChi app plus the send-out jobs.
type Server struct {
	app    *hyperchi.HyperChi
	db     *database.DB
	jobs   *Jobs
	stream *progressStream
	opts   Options
}

// NewServer builds the app, runs migrations, seeds what the options ask
// for, and registers every route. Call app.Serve (or start the integrations
// in a test) to begin.
func NewServer(opts Options) (*Server, error) {
	if opts.Config == nil {
		return nil, errors.New("mailgrid: no configuration")
	}
	if opts.Shutdown == nil {
		opts.Shutdown = context.Background()
	}
	if opts.MaxUpload <= 0 {
		opts.MaxUpload = 2 << 20
	}
	if opts.From == "" {
		opts.From = "Mailgrid <outbox@mailgrid.invalid>"
	}
	app, err := hyperchi.NewWithConfigE(opts.Config)
	if err != nil {
		return nil, err
	}
	s := &Server{app: app, db: app.DB, opts: opts}
	if err := s.setup(); err != nil {
		return nil, errors.Join(err, app.Close())
	}
	return s, nil
}

func (s *Server) setup() error {
	if s.db == nil {
		return errors.New("mailgrid needs a database; unset DB_DISABLED")
	}
	if err := s.app.Migrate(migrations()...); err != nil {
		return err
	}
	if err := s.storeSessions(); err != nil {
		return err
	}
	if err := ensureAdmin(s.db, s.opts.AdminEmail, s.opts.AdminPassword, s.opts.AdminName); err != nil {
		return err
	}
	if s.opts.SeedDemo {
		if err := seedDemo(s.db); err != nil {
			return err
		}
	}
	if err := s.app.Integrations().Register(renderer, gc.New()); err != nil {
		return err
	}
	hub := s.app.Extensions().GetSSEHub()
	s.stream = &progressStream{hub: hub, db: s.db}
	s.jobs = newJobs(s.opts.Shutdown, s.db, s.opts.Pace, s.opts.ProgressEvery, s.stream.Publish)
	// The jobs' lifecycle belongs to the app: interrupted runs are marked at
	// start, and Close waits for running jobs before the database closes.
	if err := s.app.Integrations().Register("sendout-jobs", integrations.Hooks{
		StartFunc: func(ctx context.Context) error {
			return s.jobs.markInterrupted(ctx)
		},
		CloseFunc: s.jobs.Wait,
	}); err != nil {
		return err
	}
	s.app.OnError(s.renderError)
	return s.routes()
}

func (s *Server) storeSessions() error {
	if s.opts.SessionPath != "" {
		if err := s.app.UseKVStoreBBolt("sessions", s.opts.SessionPath, "sessions"); err != nil {
			return err
		}
	} else if err := s.app.UseKVStoreMemory("sessions"); err != nil {
		return err
	}
	return s.app.UseSessions(hyperchi.KVSessionConfig{
		Backend:         "sessions",
		CookieName:      "mailgrid_session",
		MaxAge:          12 * time.Hour,
		InsecureCookies: s.app.Config.IsDevelopment(),
	})
}

// routes registers every route. Pages and forms run behind sessions, CSRF,
// and the signed-in user; static files and the htmx extension skip them.
func (s *Server) routes() error {
	app := s.app
	app.Use(app.Middleware.Recovery(), app.Middleware.Security(), app.Middleware.HTMX())
	if app.Config.IsDevelopment() {
		app.Use(app.Middleware.Logger())
	}
	// Serves the htmx 4 SSE extension at /hyperchi/js/hx-sse.js.
	app.Extensions().EnableExtensionAssets()

	// The embedded assets are not fingerprinted, so they are cached briefly
	// and revalidated with the content-hash ETag StaticFS sets.
	static, err := fs.Sub(staticFS, "static")
	if err != nil {
		return err
	}
	if err := app.StaticFS("/static/*", static, hyperchi.StaticOptions{CacheControl: "public, max-age=300"}); err != nil {
		return err
	}
	app.Get("/healthz", func(c *hyperchi.Context) error {
		return c.SetHeader("Cache-Control", "no-store").Text("ok\n")
	})

	web := []hyperchi.Middleware{app.SessionMiddleware(), app.Middleware.CSRF(), s.loadUser}
	signedIn := append(append([]hyperchi.Middleware{}, web...), s.requireUser)
	// The event stream needs the user but not a fresh CSRF token: rotating
	// the token on every reconnect would only churn the cookie.
	stream := []hyperchi.Middleware{app.SessionMiddleware(), s.loadUser, s.requireUser}

	app.Get("/login", s.loginPage, web...)
	app.Post("/login", s.login, web...)
	app.Get("/setup", s.setupPage, web...)
	app.Post("/setup", s.createFirstUser, web...)
	app.Post("/logout", s.logout, signedIn...)

	app.Get("/", func(c *hyperchi.Context) error { return c.Redirect("/lists") }, signedIn...)
	app.Get("/lists", s.listsPage, signedIn...)
	app.Post("/lists", s.createList, signedIn...)
	app.Get("/lists/{id}", s.listPage, signedIn...)
	app.Post("/lists/{id}/import", s.importCSV, signedIn...)
	app.Get("/recipients/{id}/edit", s.editRecipient, signedIn...)
	app.Post("/recipients/{id}", s.updateRecipient, signedIn...)
	app.Delete("/recipients/{id}", s.deleteRecipient, signedIn...)
	app.Get("/recipient-options", s.recipientOptions, signedIn...)

	app.Get("/letters", s.lettersPage, signedIn...)
	app.Get("/letters/new", s.newLetterPage, signedIn...)
	app.Post("/letters", s.createLetter, signedIn...)
	app.Post("/letters/preview", s.previewLetter, signedIn...)
	app.Get("/letters/{id}", s.letterPage, signedIn...)
	app.Post("/letters/{id}", s.updateLetter, signedIn...)

	app.Get("/sendouts", s.sendoutsPage, signedIn...)
	app.Get("/sendouts/new", s.newSendoutPage, signedIn...)
	app.Post("/sendouts", s.startSendout, signedIn...)
	app.Get("/sendouts/{id}", s.sendoutPage, signedIn...)
	app.Post("/sendouts/{id}/cancel", s.cancelSendout, signedIn...)
	app.Get("/sendouts/{id}/events", s.sendoutEvents, stream...)
	app.Get("/sendouts/{id}/outbox", s.outboxPage, signedIn...)
	app.Get("/sendouts/{id}/export.csv", s.exportCSV, signedIn...)
	app.Get("/sendouts/{id}/export.zip", s.exportZip, signedIn...)
	app.Get("/outbox/{id}", s.outboxItemPage, signedIn...)
	app.Get("/outbox/{id}/eml", s.outboxItemEML, signedIn...)

	app.Get("/team", s.teamPage, signedIn...)
	app.Post("/team", s.addTeammate, signedIn...)
	app.Get("/audit", s.auditPage, signedIn...)
	return nil
}

// render sends a gomponents node with the status set on c.
func render(c *hyperchi.Context, node g.Node) error {
	return c.RenderWith(renderer, node)
}

// partial reports an htmx request that swaps part of the page, as opposed to
// a navigation that needs the whole document.
func partial(c *hyperchi.Context) bool {
	return c.IsHTMX() && !c.HX().Boosted() && !hyperchi.IsHXHistoryRestoreRequest(c.Request())
}

// page renders a full document around content.
func (s *Server) page(c *hyperchi.Context, title, section string, content ...g.Node) error {
	return render(c, layout(pageMeta{
		Title:   title,
		Section: section,
		User:    currentUser(c),
		CSRF:    c.CSRFToken(),
	}, content...))
}

// rejected answers a form that failed validation with 422: the form alone
// for htmx, which swaps it in place, or the whole page around it for a
// browser that posted without JavaScript.
func (s *Server) rejected(c *hyperchi.Context, title, section string, form g.Node) error {
	c.Status(http.StatusUnprocessableEntity)
	if partial(c) {
		return render(c, form)
	}
	return s.page(c, title, section, h.Section(h.Class("panel"), form))
}

// renderError brands every error the app answers with: htmx swaps get an
// alert fragment, browsers a full page, and API clients the default JSON.
func (s *Server) renderError(c *hyperchi.Context, err error) {
	var he *hyperchi.HTTPError
	if !errors.As(err, &he) {
		he = hyperchi.NewHTTPError(http.StatusInternalServerError, "Something went wrong on our side. The error is in the server log.")
	}
	if he.Code >= 500 {
		// The cause stays in the log; the page shows only the message.
		log.Printf("mailgrid: %s %s: %v", c.Method(), c.Path(), err)
	}
	if strings.Contains(c.Header("Accept"), "application/json") {
		s.app.DefaultError(c, he)
		return
	}
	var out error
	if partial(c) {
		out = render(c.Status(he.Code), errorAlert(he))
	} else {
		out = s.page(c.Status(he.Code), fmt.Sprintf("%d %s", he.Code, http.StatusText(he.Code)), "", errorPage(he))
	}
	if out != nil {
		log.Printf("mailgrid: rendering error page for %s: %v", c.Path(), out)
		s.app.DefaultError(c, he)
	}
}

// id reads a numeric route parameter.
func id(c *hyperchi.Context) (int64, error) {
	n, err := c.ParamInt("id")
	return int64(n), err
}

// notFound turns errNotFound into a 404 and passes other errors through.
func notFound(err error, what string) error {
	if errors.Is(err, errNotFound) {
		return hyperchi.NotFound("No such " + what + ".")
	}
	return err
}
