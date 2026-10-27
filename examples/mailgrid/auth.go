package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/regiellis/hyperchi/hyperchi"
	"github.com/regiellis/hyperchi/hyperchi/database"
	"github.com/regiellis/hyperchi/hyperchi/security"
)

type ctxKey int

const userKey ctxKey = iota

const minPassword = 12

// currentUser is the signed-in user, or nil.
func currentUser(c *hyperchi.Context) *User {
	u, _ := c.Ctx().Value(userKey).(*User)
	return u
}

// loadUser puts the signed-in user, if any, on the request context. Identity
// comes from the server-side session only.
func (s *Server) loadUser(next http.Handler) http.Handler {
	return s.app.ToHTTP(func(c *hyperchi.Context) error {
		r := c.Request()
		session := c.Session()
		if session == nil {
			next.ServeHTTP(c.Response(), r)
			return nil
		}
		uid, err := strconv.ParseInt(session.GetString("uid"), 10, 64)
		if err != nil {
			next.ServeHTTP(c.Response(), r)
			return nil
		}
		u, err := one[User](s.db, `SELECT * FROM users WHERE id = ?`, uid)
		if errors.Is(err, errNotFound) {
			// The account is gone: forget it.
			session.Delete("uid")
			next.ServeHTTP(c.Response(), r)
			return nil
		}
		if err != nil {
			return err
		}
		ctx := context.WithValue(r.Context(), userKey, &u)
		ctx = security.WithUserID(ctx, strconv.FormatInt(u.ID, 10))
		next.ServeHTTP(c.Response(), r.WithContext(ctx))
		return nil
	})
}

// requireUser sends visitors to the login page, or to setup when there is
// no account yet. An htmx request is redirected with HX-Redirect.
func (s *Server) requireUser(next http.Handler) http.Handler {
	return s.app.ToHTTP(func(c *hyperchi.Context) error {
		if currentUser(c) != nil {
			next.ServeHTTP(c.Response(), c.Request())
			return nil
		}
		count, err := s.db.Count("users", "")
		if err != nil {
			return err
		}
		if count == 0 {
			return c.Redirect("/setup")
		}
		target := "/login"
		if c.Method() == http.MethodGet && !partial(c) {
			target += "?next=" + url.QueryEscape(c.Request().URL.RequestURI())
		}
		return c.Redirect(target)
	})
}

// LoginForm is the sign-in form.
type LoginForm struct {
	Email    string `form:"email" validate:"required,email"`
	Password string `form:"password" validate:"required"`
	Next     string `form:"next"`
}

func (s *Server) loginPage(c *hyperchi.Context) error {
	if currentUser(c) != nil {
		return c.Redirect("/lists")
	}
	count, err := s.db.Count("users", "")
	if err != nil {
		return err
	}
	if count == 0 {
		return c.Redirect("/setup")
	}
	return s.page(c, "Sign in", "", loginView(LoginForm{Next: c.Query("next")}, nil, c.CSRFToken()))
}

// dummyHash makes a failed lookup cost as much as a wrong password, so the
// response time does not reveal which emails have accounts. It is computed
// on first use because bcrypt at HyperChi's cost takes about a second.
var dummyHash = sync.OnceValues(func() (string, error) {
	return security.HashPassword("mailgrid-no-such-account")
})

func (s *Server) login(c *hyperchi.Context) error {
	in, err := hyperchi.Bind[LoginForm](c)
	if err != nil {
		if fields := hyperchi.FieldErrors(err); fields != nil {
			return s.rejected(c, "Sign in", "", loginForm(in, fields, c.CSRFToken()))
		}
		return err
	}
	email := strings.ToLower(strings.TrimSpace(in.Email))
	u, err := one[User](s.db, `SELECT * FROM users WHERE email = ?`, email)
	if err != nil && !errors.Is(err, errNotFound) {
		return err
	}
	hash := u.PasswordHash
	if hash == "" {
		if hash, err = dummyHash(); err != nil {
			return err
		}
	}
	if !security.CheckPasswordHash(in.Password, hash) || u.ID == 0 {
		in.Password = ""
		return s.rejected(c, "Sign in", "", loginForm(in, map[string][]string{
			"password": {"That email and password do not match an account."},
		}, c.CSRFToken()))
	}
	session := c.Session()
	if session == nil {
		return errors.New("login: no session on the request")
	}
	// A new session ID at sign-in defeats session fixation.
	if err := session.Regenerate(); err != nil {
		return err
	}
	session.Set("uid", strconv.FormatInt(u.ID, 10))
	if err := audit(c.Ctx(), s.db, &u, "signed in", ""); err != nil {
		return err
	}
	return c.Redirect(safeNext(in.Next))
}

// safeNext keeps a post-login redirect on this site.
func safeNext(next string) string {
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.HasPrefix(next, `/\`) {
		return "/lists"
	}
	return next
}

func (s *Server) logout(c *hyperchi.Context) error {
	u := currentUser(c)
	if err := audit(c.Ctx(), s.db, u, "signed out", ""); err != nil {
		return err
	}
	if session := c.Session(); session != nil {
		if err := session.Destroy(); err != nil {
			return err
		}
	}
	return c.Redirect("/login")
}

// UserForm creates an account, on the setup page or the team page.
type UserForm struct {
	Email    string `form:"email" validate:"required,email,max=254"`
	Name     string `form:"name" validate:"required,max=120"`
	Password string `form:"password" validate:"required,min=12,max=72"`
}

func (s *Server) setupPage(c *hyperchi.Context) error {
	count, err := s.db.Count("users", "")
	if err != nil {
		return err
	}
	if count > 0 {
		return c.Redirect("/login")
	}
	return s.page(c, "Set up Mailgrid", "", setupView(UserForm{}, nil, c.CSRFToken()))
}

// createFirstUser runs only while there is no account, so the setup page
// cannot be used to add accounts later.
func (s *Server) createFirstUser(c *hyperchi.Context) error {
	count, err := s.db.Count("users", "")
	if err != nil {
		return err
	}
	if count > 0 {
		return hyperchi.Forbidden("Mailgrid is already set up. Ask a teammate to add you.")
	}
	in, fields, err := s.bindUser(c)
	if err != nil {
		return err
	}
	if fields != nil {
		return s.rejected(c, "Set up Mailgrid", "", userForm("/setup", in, fields, c.CSRFToken(), "Create account"))
	}
	u, err := createUser(s.db, in.Email, in.Password, in.Name)
	if err != nil {
		return err
	}
	session := c.Session()
	if session == nil {
		return errors.New("setup: no session on the request")
	}
	if err := session.Regenerate(); err != nil {
		return err
	}
	session.Set("uid", strconv.FormatInt(u.ID, 10))
	if err := audit(c.Ctx(), s.db, &u, "created the first account", u.Email); err != nil {
		return err
	}
	return c.Redirect("/lists")
}

// bindUser binds and checks a UserForm. Field problems come back as fields;
// err is for requests that could not be read.
func (s *Server) bindUser(c *hyperchi.Context) (UserForm, map[string][]string, error) {
	in, err := hyperchi.Bind[UserForm](c)
	in.Password = strings.TrimSpace(in.Password)
	if err != nil {
		fields := hyperchi.FieldErrors(err)
		if fields == nil {
			return in, nil, err
		}
		return in, fields, nil
	}
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	taken, err := database.IsTaken(s.db, "users", "email", in.Email)
	if err != nil {
		return in, nil, err
	}
	if taken {
		return in, map[string][]string{"email": {"An account with this email already exists."}}, nil
	}
	return in, nil, nil
}

// createUser hashes the password with bcrypt and stores the account.
func createUser(db *database.DB, email, password, name string) (User, error) {
	hash, err := security.HashPassword(password)
	if err != nil {
		return User{}, err
	}
	email = strings.ToLower(strings.TrimSpace(email))
	id, err := db.Create("users", map[string]any{
		"email": email, "name": strings.TrimSpace(name), "password_hash": hash, "created_at": time.Now().UTC(),
	})
	if err != nil {
		return User{}, err
	}
	return one[User](db, `SELECT * FROM users WHERE id = ?`, id)
}

// ensureAdmin creates the account from MAILGRID_ADMIN_* when there is no
// account yet. With accounts present it does nothing, so a password set later
// in the app is never overwritten by the environment.
func ensureAdmin(db *database.DB, email, password, name string) error {
	if email == "" && password == "" {
		return nil
	}
	if email == "" || password == "" {
		return errors.New("MAILGRID_ADMIN_EMAIL and MAILGRID_ADMIN_PASSWORD must be set together")
	}
	if len(password) < minPassword {
		return fmt.Errorf("MAILGRID_ADMIN_PASSWORD must be at least %d characters", minPassword)
	}
	if _, msg := normalizeEmail(strings.TrimSpace(email)); msg != "" {
		return fmt.Errorf("MAILGRID_ADMIN_EMAIL: %s", msg)
	}
	count, err := db.Count("users", "")
	if err != nil || count > 0 {
		return err
	}
	if name == "" {
		name = "Admin"
	}
	u, err := createUser(db, email, password, name)
	if err != nil {
		return err
	}
	return audit(context.Background(), db, &u, "created from MAILGRID_ADMIN_EMAIL", u.Email)
}

func (s *Server) teamPage(c *hyperchi.Context) error {
	users, err := database.ListStruct[User](s.db, `SELECT * FROM users ORDER BY name, email`)
	if err != nil {
		return err
	}
	return s.page(c, "Team", "team", teamView(users, UserForm{}, nil, c.CSRFToken()))
}

func (s *Server) addTeammate(c *hyperchi.Context) error {
	in, fields, err := s.bindUser(c)
	if err != nil {
		return err
	}
	if fields != nil {
		return s.rejected(c, "Team", "team", userForm("/team", in, fields, c.CSRFToken(), "Add teammate"))
	}
	u, err := createUser(s.db, in.Email, in.Password, in.Name)
	if err != nil {
		return err
	}
	if err := audit(c.Ctx(), s.db, currentUser(c), "added a teammate", u.Email); err != nil {
		return err
	}
	return c.Redirect("/team")
}
