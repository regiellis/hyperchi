// Command showcase serves HyperChi's landing page and documentation. Every
// section runs on the framework feature it describes, so a broken feature shows
// on screen.
package main

import (
	"errors"
	"fmt"
	"html"
	"html/template"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/regiellis/hyperchi/hyperchi"
	"github.com/regiellis/hyperchi/hyperchi/config"
	"github.com/regiellis/hyperchi/hyperchi/helpers"
	"github.com/regiellis/hyperchi/hyperchi/patterns"
	"github.com/regiellis/hyperchi/hyperchi/security"
)

// reqCount counts requests to this process (countMiddleware increments it). The
// live metrics strip reports it, so every number shown is real.
var (
	reqCount  atomic.Int64
	startTime = time.Now()
)

// releaseVersion is stamped into the image for deployment verification.
var releaseVersion = "development"

func main() {
	host := os.Getenv("HOST")
	if host == "" {
		host = "127.0.0.1"
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "9999"
	}
	policy, err := newSitePolicyWithPreview(host, port, os.Getenv("PUBLIC_ORIGIN"), os.Getenv("PREVIEW_ORIGIN"))
	if err != nil {
		log.Fatal(err)
	}
	// This site always runs with production behavior, even under Air. Some
	// framework subsystems read ENVIRONMENT or GO_ENV instead of Config, so set
	// both as well as config.Production.
	if err := os.Setenv("ENVIRONMENT", "production"); err != nil {
		log.Fatal(err)
	}
	if err := os.Setenv("GO_ENV", "production"); err != nil {
		log.Fatal(err)
	}
	cfg := config.Production()
	cfg.Database.Name = ":memory:"
	app := hyperchi.NewWithConfig(cfg)
	sessions, err := waitlistSessions(app, os.Getenv("PUBLIC_ORIGIN") != "")
	if err != nil {
		log.Fatal(err)
	}
	// Intentionally do not trust forwarding headers or enable permissive CORS.
	app.Use(policy.middleware(app), app.Middleware.Recovery(), app.Middleware.HTMX(), countMiddleware, sessions)
	app.Extensions().GetWebSocketHub().SetInsecureSkipVerify(false)
	app.Extensions().GetWebSocketHub().SetReceiveOnly(true)
	app.Extensions().EnableAllExtensions()
	helpers.Must(app.LoadTemplates("templates/*.tmpl"))
	helpers.Must(setupRoutes(app, os.Getenv("PUBLIC_ORIGIN")))
	helpers.Must(setupIntegrations(app))
	go broadcastMetrics(app)
	addr := net.JoinHostPort(host, port)
	app.Helpers.Logger.Info("HyperChi showcase listening on %s", addr)
	helpers.Must(app.Serve(addr))
}

func countMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqCount.Add(1)
		next.ServeHTTP(w, r)
	})
}

// setupRoutes registers every page and demo endpoint. publicOrigin is the
// validated PUBLIC_ORIGIN used for absolute documentation links, or "".
func setupRoutes(app *hyperchi.HyperChi, publicOrigin string) error {
	// Site-wide template values are set once instead of in every handler.
	app.SetGlobal("version", "v0.2.0")

	// The embedded static files. Pages link them with {{asset "/static/..."}},
	// which adds a content version (?v=) that StaticFS answers with a one-year
	// immutable Cache-Control, so a changed file gets a new URL.
	static, err := fs.Sub(publicAssets, "static")
	if err != nil {
		return err
	}
	if err := app.StaticFS("/static/*", static, hyperchi.StaticOptions{}); err != nil {
		return err
	}

	app.OnError(siteError)

	healthz := func(c *hyperchi.Context) error {
		return c.SetHeader("Cache-Control", "no-store").Text("ok\n")
	}
	app.Get("/healthz", healthz)
	app.Head("/healthz", healthz)

	app.Get("/", func(c *hyperchi.Context) error {
		return c.View("index.tmpl", hyperchi.H{
			"title":   "HyperChi | Stay close to the web.",
			"current": "home",
		})
	})

	// Documentation is rendered by the same HyperChi application as the homepage,
	// from the Markdown files in docs/ (see docs.go).
	if err := setupDocs(app, publicOrigin); err != nil {
		return err
	}

	// One endpoint, two shapes: ?format=json gets JSON, and anything else (htmx
	// or not) gets the HTML fragment. The page calls it live.
	app.Get("/demo/echo", func(c *hyperchi.Context) error {
		served := time.Now().Format("15:04:05")
		if c.Query("format") == "json" {
			return c.JSON(hyperchi.H{
				"htmx":   c.IsHTMX(),
				"method": c.Method(),
				"path":   c.Path(),
				"served": served,
			})
		}
		kind := "direct request"
		if c.IsHTMX() {
			kind = "HTMX request → fragment, target " + c.HX().Target()
		}
		// Application-owned markup; every request value is escaped before it is inserted.
		return c.HTML(template.HTML(fmt.Sprintf(
			`<div class="well mono"><div>negotiated: <b>%s</b></div>`+
				`<div class="muted" style="margin-top:6px">method %s · %s · served %s</div></div>`,
			html.EscapeString(kind), html.EscapeString(c.Method()), html.EscapeString(c.Path()), served,
		)))
	})

	// A visitor message is broadcast to the "activity" WebSocket room, and the
	// htmx ws extension appends it to every open feed. The message is escaped
	// before broadcast.
	app.Post("/activity/send", func(c *hyperchi.Context) error {
		in, err := hyperchi.Bind[activityMessage](c)
		if err != nil {
			return err // a malformed form is a 400
		}
		msg := strings.TrimSpace(in.Message)
		if msg == "" {
			return c.NoContent()
		}
		if runes := []rune(msg); len(runes) > 140 {
			msg = string(runes[:140])
		}
		line := fmt.Sprintf(
			`<div class="feed-item"><span class="tag live mono">ws</span>`+
				`<div><div class="who">visitor</div><div>%s</div>`+
				`<div class="when">%s</div></div></div>`,
			html.EscapeString(msg), time.Now().Format("15:04:05"),
		)
		// BroadcastSwap puts the HTML in the `content` field the htmx 4 ws extension
		// swaps; a bare Data message is delivered but never appears in the feed.
		if hub := app.Extensions().GetWebSocketHub(); hub != nil {
			hub.BroadcastSwap("activity", "#activity-log", "beforeend", line)
		}
		return c.NoContent()
	})

	// Uploads go through the security validator directly rather than upload
	// middleware. The file is validated and reported, never stored: this is a
	// public site whose container has a read-only root and no data volume, and
	// keeping anonymous visitors' files would turn the demo into free file
	// hosting. An app that keeps uploads stores them with res.SaveAll (or
	// security.SaveUpload), which names each file itself; see the docs.
	uploadCfg := security.DefaultUploadConfig()
	uploadCfg.MaxFileSize = 2 * 1024 * 1024 // 2 MB
	uploadCfg.MaxFiles = 1
	uploadCfg.AllowedExtensions = []string{".png", ".jpg", ".jpeg", ".webp", ".txt", ".pdf"}
	// DefaultUploadConfig sets RequireAuth, which rejects an upload with no
	// verified user in context. This public demo turns it off; keep it on in a
	// real app.
	uploadCfg.RequireAuth = false
	app.Post("/upload", func(c *hyperchi.Context) error {
		res := security.NewUploadValidator(uploadCfg).ValidateUpload(c.Request())
		if res == nil || !res.Valid {
			var b strings.Builder
			b.WriteString(`<div class="well" style="border-color:var(--warn)"><b class="mono" style="color:var(--warn)">rejected</b><ul style="margin:6px 0 0;padding-left:18px">`)
			if res != nil {
				for _, e := range res.Errors {
					fmt.Fprintf(&b, `<li>%s</li>`, html.EscapeString(e))
				}
			}
			b.WriteString(`</ul></div>`)
			return c.Status(http.StatusUnprocessableEntity).HTML(template.HTML(b.String()))
		}
		var b strings.Builder
		b.WriteString(`<div class="well"><b class="mono" style="color:var(--ok)">validated</b><div style="margin-top:6px">`)
		for _, f := range res.Files {
			fmt.Fprintf(&b, `<div class="mono">%s · %d bytes · %s</div>`,
				html.EscapeString(f.SafeFilename), f.Size, html.EscapeString(f.ContentType))
		}
		b.WriteString(`</div><p class="muted" style="margin-top:6px;font-size:0.85rem">Validated only; this demo does not store the file.</p></div>`)
		return c.HTML(template.HTML(b.String()))
	})

	setupPatterns(app)
	setupWaitlist(app)
	return nil
}

// setupWaitlist registers the three-step waitlist wizard (the MultiStepForm
// pattern, validated on the server step by step) and an AutoSave endpoint.
// Nothing a visitor enters is stored past the wizard: its answers live in the
// visitor's session until the last step, and Complete only counts the run.
//
// The wizard keeps answers in the session, so its route needs the session
// middleware from waitlistSessions. The last step's POST answers with
// HX-Redirect to /waitlist/done, a full navigation, so the completion page is
// a whole page rather than a fragment.
func setupWaitlist(app *hyperchi.HyperChi) {
	pm := app.Extensions().GetPatternManager()
	if pm == nil {
		app.Helpers.Logger.Error("pattern manager is nil; waitlist section disabled")
		return
	}

	// The step templates are inline strings, so they are registered with the
	// pattern manager. It also finds templates loaded with app.LoadTemplates.
	pm.RegisterTemplate("wizard-step-1",
		template.Must(template.New("wizard-step-1").Parse(wizardStep1Tmpl)))
	pm.RegisterTemplate("wizard-step-2",
		template.Must(template.New("wizard-step-2").Parse(wizardStep2Tmpl)))
	pm.RegisterTemplate("wizard-step-3",
		template.Must(template.New("wizard-step-3").Parse(wizardStep3Tmpl)))

	pm.MultiStepForm(waitlistPath, patterns.MultiStepFormConfig{
		SuccessURL: "/waitlist/done",
		Steps: []patterns.FormStep{
			{
				Name: "identity", Title: "Where do we reach you?", Template: "wizard-step-1",
				Fields: []string{"email"}, Validate: validateWaitlistEmail,
			},
			{
				Name: "context", Title: "What are you building with?", Template: "wizard-step-2",
				Fields: []string{"role", "stack"}, Validate: validateWaitlistRole,
			},
			{Name: "confirm", Title: "Confirm and join", Template: "wizard-step-3"},
		},
		// Complete gets every accepted answer. This public demo keeps none of
		// them: it counts the finished run for the completion page.
		Complete: func(_ *http.Request, _ url.Values) error {
			waitlistCompletions.Add(1)
			return nil
		},
	})

	// AutoSave demonstrates a server round trip. The callback only counts requests;
	// it does not persist email addresses, roles, or drafts.
	pm.AutoSave("/waitlist/save", patterns.AutoSaveConfig{
		DraftKey: "waitlist",
		SaveFunc: func(data map[string]any) error {
			waitlistSaves.Add(1)
			return nil
		},
	})

	// The completion page, reached by the full navigation that ends the wizard.
	app.Get("/waitlist/done", func(c *hyperchi.Context) error {
		return c.View("done.tmpl", hyperchi.H{
			"title":     "Form complete | HyperChi",
			"current":   "home",
			"saves":     waitlistSaves.Load(),
			"completed": waitlistCompletions.Load(),
		})
	})
}

// waitlistPath is the wizard's route, and the only path that runs with a
// session.
const waitlistPath = "/waitlist"

// waitlistSessions sets up the in-memory session store the wizard keeps its
// answers in, and returns session middleware limited to the wizard's route.
// The rest of the site never touches a session, so it sets no session cookie
// and stores no session record. Sessions expire after 30 minutes; the memory
// store drops them on its own. https says whether the site has an HTTPS
// origin: without one (loopback or the Tailscale preview) a Secure cookie
// never comes back, and the wizard would forget every answer.
func waitlistSessions(app *hyperchi.HyperChi, https bool) (func(http.Handler) http.Handler, error) {
	if err := app.UseKVStoreMemoryAdvanced("sessions"); err != nil {
		return nil, err
	}
	if err := app.UseSessions(hyperchi.KVSessionConfig{Backend: "sessions", MaxAge: 30 * time.Minute, InsecureCookies: !https}); err != nil {
		return nil, err
	}
	sessions := app.SessionMiddleware()
	return func(next http.Handler) http.Handler {
		withSession := sessions(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == waitlistPath {
				withSession.ServeHTTP(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}, nil
}

// activityMessage is the activity room form. Bind reads it and answers 400 for a
// body that is not a readable form.
type activityMessage struct {
	Message string `form:"message"`
}

// waitlistSaves counts AutoSave round trips so the draft indicator is real, and
// waitlistCompletions counts finished wizards for the completion page.
var (
	waitlistSaves       atomic.Int64
	waitlistCompletions atomic.Int64
)

// validateWaitlistEmail is the server-side gate for step 1. A rejected step
// comes back as 422 with the message under its field.
func validateWaitlistEmail(values url.Values) map[string][]string {
	email := strings.TrimSpace(values.Get("email"))
	if !strings.Contains(email, "@") || !strings.Contains(email, ".") {
		return map[string][]string{"email": {"Enter a valid work email so we can reach you."}}
	}
	return nil
}

// validateWaitlistRole is the gate for step 2: a role from the list is
// required, and the optional stack boxes must be ones the form offers.
func validateWaitlistRole(values url.Values) map[string][]string {
	errs := map[string][]string{}
	if !slices.Contains(waitlistRoles, values.Get("role")) {
		errs["role"] = []string{"Pick the role that fits best."}
	}
	for _, s := range values["stack"] {
		if !slices.Contains(waitlistStacks, s) {
			errs["stack"] = []string{"Choose from the listed options."}
		}
	}
	return errs
}

var (
	waitlistRoles  = []string{"backend", "fullstack", "lead", "founder"}
	waitlistStacks = []string{"htmx", "sqlite", "templ"}
)

// The wizard steps target #wizard directly: MultiStepForm's POST returns the
// next step on success, and the current step on a validation failure with
// .errors (messages per field) and .values (the submitted answers). Going back
// to a step shows its stored answers through .values too.
const wizardStep1Tmpl = `<form hx-post="/waitlist" hx-target="#wizard" hx-swap="innerHTML" class="stack">
  <div class="wiz-head">
    <span class="mono muted">step {{.step}} / {{.total_steps}}</span>
    <div class="wiz-bar"><span style="width:{{.progress}}%"></span></div>
  </div>
  <h3>{{.step_info.Title}}</h3>
  <input type="hidden" name="step" value="{{.step}}">
  <label class="field"><span>work email</span>
    <input name="email" type="email" required placeholder="you@company.com" autocomplete="email" value="{{.values.Get "email"}}"
           hx-post="/waitlist/save" hx-trigger="input changed delay:600ms" hx-swap="none"></label>
  {{range index .errors "email"}}<div class="wiz-err" aria-live="polite">{{.}}</div>{{end}}
  <div class="wiz-actions"><button class="btn btn-primary" type="submit">continue</button>
    <span id="draft-status" class="mono muted"></span></div>
</form>`

const wizardStep2Tmpl = `<form hx-post="/waitlist" hx-target="#wizard" hx-swap="innerHTML" class="stack">
  <div class="wiz-head">
    <span class="mono muted">step {{.step}} / {{.total_steps}}</span>
    <div class="wiz-bar"><span style="width:{{.progress}}%"></span></div>
  </div>
  <h3>{{.step_info.Title}}</h3>
  <input type="hidden" name="step" value="{{.step}}">
  {{$role := .values.Get "role"}}{{$stack := index .values "stack"}}
  <label class="field"><span>your role</span>
    <select name="role" hx-post="/waitlist/save" hx-trigger="change" hx-swap="none">
      <option value="" {{if not $role}}selected{{end}}>choose one</option>
      <option value="backend" {{if eq $role "backend"}}selected{{end}}>Backend engineer</option>
      <option value="fullstack" {{if eq $role "fullstack"}}selected{{end}}>Full-stack engineer</option>
      <option value="lead" {{if eq $role "lead"}}selected{{end}}>Tech lead / architect</option>
      <option value="founder" {{if eq $role "founder"}}selected{{end}}>Founder</option>
    </select></label>
  {{range index .errors "role"}}<div class="wiz-err" aria-live="polite">{{.}}</div>{{end}}
  <fieldset class="field"><span>stack (optional)</span>
    <div class="checks">
      <label><input type="checkbox" name="stack" value="htmx"{{range $stack}}{{if eq . "htmx"}} checked{{end}}{{end}}> htmx</label>
      <label><input type="checkbox" name="stack" value="sqlite"{{range $stack}}{{if eq . "sqlite"}} checked{{end}}{{end}}> SQLite</label>
      <label><input type="checkbox" name="stack" value="templ"{{range $stack}}{{if eq . "templ"}} checked{{end}}{{end}}> templates</label>
    </div></fieldset>
  {{range index .errors "stack"}}<div class="wiz-err" aria-live="polite">{{.}}</div>{{end}}
  <div class="wiz-actions"><button class="btn btn-ghost" type="button" hx-get="/waitlist?step=1" hx-target="#wizard" hx-swap="innerHTML">back</button>
    <button class="btn btn-primary" type="submit">continue</button>
    <span id="draft-status" class="mono muted"></span></div>
</form>`

const wizardStep3Tmpl = `<form hx-post="/waitlist" hx-target="#wizard" hx-swap="innerHTML" class="stack">
  <div class="wiz-head">
    <span class="mono muted">step {{.step}} / {{.total_steps}}</span>
    <div class="wiz-bar"><span style="width:{{.progress}}%"></span></div>
  </div>
  <h3>{{.step_info.Title}}</h3>
  <input type="hidden" name="step" value="{{.step}}">
  <p class="muted">Joining as <b>{{.values.Get "email"}}</b>, {{.values.Get "role"}}. Each step was
     validated on the server as you went. Submitting completes the multi-step form and the
     pattern redirects you to a confirmation page.</p>
  <div class="wiz-actions"><button class="btn btn-ghost" type="button" hx-get="/waitlist?step=2" hx-target="#wizard" hx-swap="innerHTML">back</button>
    <button class="btn btn-accent" type="submit">join the waitlist</button>
    <span id="draft-status" class="mono muted"></span></div>
</form>`

// setupPatterns registers the LiveSearch and InfiniteScroll patterns over the
// in-memory changelog.
func setupPatterns(app *hyperchi.HyperChi) {
	pm := app.Extensions().GetPatternManager()
	if pm == nil {
		app.Helpers.Logger.Error("pattern manager is nil; patterns section disabled")
		return
	}

	// Inline templates (one with its own FuncMap) are registered with the
	// pattern manager. It also finds templates loaded with app.LoadTemplates.
	pm.RegisterTemplate("changelog-search",
		template.Must(template.New("changelog-search").Parse(searchTmpl)))
	pm.RegisterTemplate("changelog-page",
		template.Must(template.New("changelog-page").
			Funcs(template.FuncMap{"add": func(a, b int) int { return a + b }}).
			Parse(pageTmpl)))

	pm.LiveSearch("/patterns/search", patterns.LiveSearchConfig{
		SearchFunc:  searchChangelog,
		Template:    "changelog-search",
		MinQueryLen: 2,
		MaxResults:  8,
	})

	pm.InfiniteScroll("/patterns/changelog", patterns.InfiniteScrollConfig{
		DataFunc: changelogPage,
		Template: "changelog-page",
		PageSize: changelogPageSize,
		// Without HasMoreFunc the pattern guesses from whether a page came back
		// full, which asks for one empty page when the changelog divides evenly.
		HasMoreFunc: func(page int) bool {
			return page >= 1 && page < (len(changelog)+changelogPageSize-1)/changelogPageSize
		},
	})
}

const changelogPageSize = 6

// Entry is one changelog line, backing both the live search and the infinite scroll.
type Entry struct {
	Tag   string
	Title string
	Body  string
	Date  string
}

var changelog = []Entry{
	{"fix", "Graceful shutdown unblocks Serve", "Serve returns cleanly on SIGINT and SIGTERM and runs cleanup exactly once.", "2026-06-14"},
	{"fix", "SSE hub self-deadlock removed", "Broadcasting from inside a subscriber callback no longer blocks the hub loop.", "2026-06-13"},
	{"fix", "WebSocket origin policy honored", "Same-origin is enforced by default and relaxed only in development.", "2026-06-12"},
	{"feat", "Server-side connection identity", "SetIdentifyUser fixes identity on the server; client user_id is never trusted.", "2026-06-11"},
	{"fix", "SQLite DSN pragmas applied", "WAL, foreign keys, and busy timeout now take effect on the mattn driver.", "2026-06-10"},
	{"feat", "hc-* directive engine", "Reactive hc-data, hc-on, hc-model, hc-for survive htmx swaps via a mutation observer.", "2026-05-30"},
	{"feat", "100+ template filters", "date, timeAgo, currency, bytes, humanize, slug, chunk, and the full HTMX helper set.", "2026-05-22"},
	{"feat", "SSE and WebSocket hubs", "Channel broadcasting for SSE and room broadcasting for WebSocket, htmx-aware.", "2026-05-18"},
	{"feat", "Response header builder", "Fluent Retarget, Reswap, Trigger, Redirect over the HX-* response headers.", "2026-05-12"},
	{"feat", "Upload validation middleware", "Size, extension, and content-type checks with a safe-filename result.", "2026-05-08"},
	{"feat", "Six advanced patterns", "LiveFeed, ProgressBar, MultiStepForm, InfiniteScroll, LiveSearch, AutoSave.", "2026-05-01"},
	{"feat", "AnimeJS integration", "Declarative data-anime plus HX-Trigger animations on swapped content.", "2026-04-24"},
	{"feat", "Content collections", "Filesystem-backed content with query, filter, and pagination.", "2026-04-18"},
	{"docs", "Fragments and layouts guide", "How full-page, fragment, and JSON negotiation resolve on a request.", "2026-04-10"},
	{"feat", "Session and KV stores", "Pluggable memory, bbolt, and cache backends behind one interface.", "2026-04-02"},
	{"fix", "CSRF token rotation", "Tokens rotate per session and validate on unsafe methods.", "2026-03-26"},
	{"feat", "View transitions", "Cross-route view transitions for htmx-driven navigation.", "2026-03-19"},
	{"feat", "Health endpoints", "Readiness and liveness with extension registry info.", "2026-03-11"},
	{"docs", "Context system spec", "How request data flows into the template render pipeline.", "2026-03-04"},
	{"feat", "hyperchi CLI scaffolding", "new, dev, build, serve, and deploy subcommands.", "2026-02-25"},
}

// searchChangelog backs LiveSearch: case-insensitive match on tag, title, and body.
func searchChangelog(query string, limit int) (any, error) {
	q := strings.ToLower(query)
	out := make([]Entry, 0, limit)
	for _, e := range changelog {
		hay := strings.ToLower(e.Tag + " " + e.Title + " " + e.Body)
		if strings.Contains(hay, q) {
			out = append(out, e)
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

// changelogPage backs InfiniteScroll: a slice of the changelog for the given page.
func changelogPage(page, pageSize int) (any, error) {
	if page < 1 || pageSize < 1 || len(changelog) == 0 || page > (len(changelog)-1)/pageSize+1 {
		return []Entry{}, nil
	}
	start := (page - 1) * pageSize
	end := len(changelog)
	if pageSize < len(changelog)-start {
		end = start + pageSize
	}
	return changelog[start:end], nil
}

const searchTmpl = `{{if .results}}{{range .results}}<div class="result"><span class="tag mono">{{.Tag}}</span> <b>{{.Title}}</b><div class="muted">{{.Body}}</div></div>{{end}}{{else}}<div class="result muted">no matches for "{{.query}}"</div>{{end}}`

const pageTmpl = `{{range .data}}<div class="result"><span class="tag mono">{{.Tag}}</span> <b>{{.Title}}</b> <span class="when mono">{{.Date}}</span><div class="muted">{{.Body}}</div></div>{{end}}{{if .has_more}}<div hx-get="/patterns/changelog?page={{add .page 1}}" hx-trigger="revealed" hx-swap="afterend"><div class="result muted mono">loading more…</div></div>{{end}}`

// broadcastMetrics sends the live metrics strip over SSE once a second:
// requests in the last second, total requests, and uptime, all from reqCount.
func broadcastMetrics(app *hyperchi.HyperChi) {
	hub := app.Extensions().GetSSEHub()
	if hub == nil {
		app.Helpers.Logger.Error("SSE hub is nil; metrics stream disabled")
		return
	}
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	last := reqCount.Load()
	for range ticker.C {
		now := reqCount.Load()
		perSec := now - last
		last = now
		up := time.Since(startTime).Truncate(time.Second)

		// htmx 4 swaps only unnamed SSE messages, which BroadcastSwap sends. A
		// named event (BroadcastHTML) is dispatched as a DOM event and never
		// swapped, so the strip would stay on "connecting".
		hub.BroadcastSwap("metrics", fmt.Sprintf(
			`<div class="metric"><div class="val mono">%d</div><div class="lab">requests / sec</div></div>`+
				`<div class="metric"><div class="val mono">%d</div><div class="lab">requests total</div></div>`+
				`<div class="metric"><div class="val mono">%s</div><div class="lab">uptime</div></div>`+
				`<div class="metric"><div class="val mono">live</div><div class="lab"><span class="live-dot on"></span> via SSE</div></div>`,
			perSec, now, up.String(),
		))
	}
}

// siteError renders the errors a browser sees, router misses and site policy
// refusals included, as a page in the site's design. htmx and JSON clients
// keep the framework default (an error fragment or JSON), so an htmx target
// never receives a whole page.
func siteError(c *hyperchi.Context, err error) {
	accept := c.Header("Accept")
	if c.IsHTMX() || (strings.Contains(accept, "application/json") && !strings.Contains(accept, "text/html")) {
		c.App().DefaultError(c, err)
		return
	}
	code, message := http.StatusInternalServerError, "Something went wrong on our side. Try again in a moment."
	var he *hyperchi.HTTPError
	if errors.As(err, &he) {
		code = he.Code
		if code < http.StatusInternalServerError {
			message = he.Message
		}
	}
	if code >= http.StatusInternalServerError {
		log.Printf("showcase: %s %s: %v", c.Method(), c.Path(), err)
	}
	heading := http.StatusText(code)
	if code == http.StatusNotFound {
		heading, message = "Nothing lives here.", "There is no page at this address. The docs and the playground are a click away."
	}
	if rerr := c.Status(code).View("error-page.tmpl", hyperchi.H{
		"title":   heading + " | HyperChi",
		"status":  code,
		"heading": heading,
		"message": message,
	}); rerr != nil {
		log.Printf("showcase: error page: %v", rerr)
		c.App().DefaultError(c, err)
	}
}
