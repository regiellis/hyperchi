package main

import (
	"net/http"
	"strings"

	"github.com/regiellis/hyperchi/hyperchi"
	"github.com/regiellis/hyperchi/hyperchi/database"
)

// The newsletter is the site's one write path: a validated form, protected
// against cross-origin posts and floods, stored in SQLite with a unique email.
// No email is sent; the confirmation says so.

const (
	signupPath = "/newsletter"
	thanksPath = "/newsletter/thanks"
)

// subscribersTable is the migration, run on every boot (CREATE TABLE IF NOT EXISTS).
var subscribersTable = database.Table("subscribers").
	ID().
	String("email", 254).NotNull().Unique().
	String("list", 16).NotNull().
	CreatedAt().
	Build()

// Signup is the posted form. Bind decodes it by form tags and checks the
// validate tags; a failure is a 422 with messages per field.
type Signup struct {
	Email string `form:"email" validate:"required,email,max=254"`
	List  string `form:"list" validate:"required,oneof=studio launch"`
	// Website is a honeypot: hidden from people, filled in by some bots.
	Website string `form:"website"`
}

// SignupForm is what the form template renders: the visitor's input and
// one message per field.
type SignupForm struct {
	Email  string
	List   string
	Errors map[string]string
}

// fieldMessages turns Bind's field errors into sentences for this form.
var fieldMessages = map[string]string{
	"email": "Enter an email address like name@example.com.",
	"list":  "Choose which emails you want.",
}

const duplicateMessage = "That address is already on the list. Nothing else to do."

type newsletter struct {
	app  *hyperchi.HyperChi
	site *Site
	opts Options
}

func newNewsletter(app *hyperchi.HyperChi, site *Site, opts Options) *newsletter {
	return &newsletter{app: app, site: site, opts: opts}
}

// Routes registers POST /newsletter behind ProtectForm: a same-origin check
// (net/http's CrossOriginProtection), a per-client token bucket keyed by
// hyperchi.ClientIP, a URL-encoded body capped at 2 KiB, then the handler.
func (n *newsletter) Routes() error {
	limiter, err := hyperchi.NewRateLimiterE(&hyperchi.RateLimitConfig{
		Burst:          n.opts.SignupBurst,
		RefillInterval: n.opts.RefillEvery,
		KeyGenerator:   hyperchi.ClientIP,
		MaxKeys:        10_000,
	})
	if err != nil {
		return err
	}
	// The app's ProtectForm sends 403, 413, 415, and 429 through OnError like
	// every other error.
	protect, err := n.app.ProtectForm(hyperchi.FormProtectionOptions{
		MaxBodyBytes: 2 << 10,
		RateLimiter:  limiter,
	})
	if err != nil {
		return err
	}
	n.app.Post(signupPath, n.subscribe, protect)
	return nil
}

func (n *newsletter) subscribe(c *hyperchi.Context) error {
	in, err := hyperchi.Bind[Signup](c)
	if err != nil {
		fields := hyperchi.FieldErrors(err)
		if fields == nil {
			return err // an unreadable body: 400 through the error pipeline
		}
		form := SignupForm{Email: in.Email, List: in.List, Errors: map[string]string{}}
		for name := range fields {
			form.Errors[name] = fieldMessages[name]
		}
		return n.invalid(c, form)
	}
	if strings.TrimSpace(in.Website) != "" {
		return n.accepted(c, "") // a bot: answer as if it worked, store nothing
	}
	email := strings.ToLower(in.Email)
	form := SignupForm{Email: in.Email, List: in.List}
	duplicate := func() error {
		form.Errors = map[string]string{"email": duplicateMessage}
		return n.invalid(c, form)
	}
	taken, err := database.IsTakenContext(c.Ctx(), n.app.DB, "subscribers", "email", email)
	if err != nil {
		return err
	}
	if taken {
		return duplicate()
	}
	// The unique index settles a race between two signups for one address.
	res, err := n.app.DB.ExecContext(c.Ctx(),
		"INSERT INTO subscribers (email, list) VALUES (?, ?) ON CONFLICT (email) DO NOTHING", email, in.List)
	if err != nil {
		return err
	}
	added, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if added == 0 {
		return duplicate()
	}
	return n.accepted(c, email)
}

// invalid answers 422 with the form re-rendered: as a fragment for htmx,
// which swaps it in place, or inside the newsletter page for a plain post.
func (n *newsletter) invalid(c *hyperchi.Context, form SignupForm) error {
	c.Status(http.StatusUnprocessableEntity)
	if c.IsHTMX() {
		return c.Fragment("newsletter-form", hyperchi.H{"form": form})
	}
	m := noIndex(n.site.Meta("Newsletter", "Join the Driftlamp list.", signupPath, ""))
	return c.Page("newsletter", hyperchi.H{"meta": m, "form": form})
}

// accepted confirms a signup: in place for htmx, by redirect otherwise
// (post, redirect, get), so a reload does not post again.
func (n *newsletter) accepted(c *hyperchi.Context, email string) error {
	if c.IsHTMX() {
		return c.Fragment("newsletter-done", hyperchi.H{"email": email})
	}
	return c.Redirect(thanksPath)
}
