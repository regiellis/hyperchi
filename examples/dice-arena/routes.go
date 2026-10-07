package main

import (
	"embed"
	"errors"
	"io/fs"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/regiellis/hyperchi/hyperchi"
)

//go:embed static
var staticFiles embed.FS

// server holds what the handlers share.
type server struct {
	arena *Arena
	dev   bool
}

func (srv *server) routes(app *hyperchi.HyperChi) error {
	// Forms that create state are rate limited per client address.
	limit := app.Middleware.RateLimit(30, time.Minute)

	app.Get("/", srv.lobby)
	app.Post("/name", srv.setName, limit)
	app.Post("/rooms", srv.createRoom, limit)
	app.Post("/join", srv.joinRoom, limit)
	app.Get("/r/{code}", srv.roomPage)
	app.WebSocket("/ws/lobby", srv.lobbySocket)
	app.WebSocket("/ws/r/{code}", srv.roomSocket)
	app.Get("/healthz", func(c *hyperchi.Context) error {
		return c.SetHeader("Cache-Control", "no-store").Text("ok\n")
	})

	// The binary carries its own assets. StaticFS refuses directories and
	// dotfiles, answers misses through the error pipeline, and gives each
	// embedded file a content-hash ETag. Development always revalidates.
	static, err := fs.Sub(staticFiles, "static")
	if err != nil {
		return err
	}
	return app.StaticFS("/static/*", static, hyperchi.StaticOptions{
		CacheControl: "public, max-age=3600",
		Development:  srv.dev,
	})
}

// NameForm picks a display name.
type NameForm struct {
	Name string `form:"name" validate:"required,max=40"`
}

// Validate trims the name and allows letters, digits, spaces, and - _ . '
func (f *NameForm) Validate() error {
	f.Name = strings.Join(strings.Fields(f.Name), " ")
	n := len([]rune(f.Name))
	if n < 2 || n > 20 {
		return nameError("must be 2 to 20 characters")
	}
	for _, r := range f.Name {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune(" -_.'", r) {
			return nameError("can use letters, numbers, spaces, and - _ . '")
		}
	}
	return nil
}

func nameError(msg string) error {
	return hyperchi.Unprocessable("Please correct the highlighted fields.", map[string][]string{"name": {msg}})
}

// CreateForm opens a table.
type CreateForm struct {
	Target int `form:"target" validate:"required,oneof=2000 5000 10000"`
}

// JoinForm joins a table by code.
type JoinForm struct {
	Code string `form:"code" validate:"required,max=12"`
}

// Validate normalizes the code and checks its shape.
func (f *JoinForm) Validate() error {
	f.Code = normalizeCode(f.Code)
	if len(f.Code) != codeLength || strings.Trim(f.Code, codeAlphabet) != "" {
		return hyperchi.Unprocessable("Please correct the highlighted fields.",
			map[string][]string{"code": {"is five letters and numbers, like K7QFD"}})
	}
	return nil
}

// lobbyData is everything the lobby templates and the layout read. input
// holds the values to put back into the forms.
func (srv *server) lobbyData(c *hyperchi.Context, who Identity, input hyperchi.H, errs map[string][]string) hyperchi.H {
	form := hyperchi.H{"name": who.Name, "code": ""}
	for k, v := range input {
		form[k] = v
	}
	if errs == nil {
		errs = map[string][]string{}
	}
	return hyperchi.H{
		"title":         "Dice Arena",
		"me":            who,
		"rooms":         srv.arena.Listing(),
		"targets":       targetChoices,
		"defaultTarget": defaultTarget,
		"errors":        errs,
		"input":         form,
		"csrf":          c.CSRFToken(), // the layout hands it to htmx
	}
}

func (srv *server) lobby(c *hyperchi.Context) error {
	return c.View("lobby", srv.lobbyData(c, identityFrom(c.Ctx()), hyperchi.H{"code": c.Query("code")}, nil))
}

// lobbyFormError re-renders the lobby forms with field errors, as a 422 that
// htmx swaps into #lobby-main. An error without fields goes to the error page.
func (srv *server) lobbyFormError(c *hyperchi.Context, who Identity, err error, input hyperchi.H) error {
	fields := hyperchi.FieldErrors(err)
	if fields == nil {
		return err
	}
	return c.Status(http.StatusUnprocessableEntity).Fragment("lobby-main", srv.lobbyData(c, who, input, fields))
}

func (srv *server) setName(c *hyperchi.Context) error {
	who := identityFrom(c.Ctx())
	in, err := hyperchi.Bind[NameForm](c)
	if err != nil {
		return srv.lobbyFormError(c, who, err, hyperchi.H{"name": c.FormValue("name"), "editing": true})
	}
	who.Name = in.Name
	if err := setIdentity(c, who); err != nil {
		return err
	}
	return c.HX().Trigger("name-saved").Fragment("lobby-main", srv.lobbyData(c, who, nil, nil))
}

func (srv *server) createRoom(c *hyperchi.Context) error {
	who := identityFrom(c.Ctx())
	in, err := hyperchi.Bind[CreateForm](c)
	if err != nil {
		return srv.lobbyFormError(c, who, err, nil)
	}
	room, err := srv.arena.Create(who, in.Target)
	var rule RuleError
	switch {
	case errors.As(err, &rule):
		return srv.lobbyFormError(c, who, hyperchi.Unprocessable(rule.Error(), map[string][]string{"name": {"comes first: pick one to open a table"}}), nil)
	case errors.Is(err, errArenaFull):
		return hyperchi.Errorf(http.StatusServiceUnavailable, "Every table is taken right now. Try again in a minute.")
	case err != nil:
		return err
	}
	return c.Redirect("/r/" + room.Code())
}

func (srv *server) joinRoom(c *hyperchi.Context) error {
	who := identityFrom(c.Ctx())
	in, err := hyperchi.Bind[JoinForm](c)
	if err != nil {
		return srv.lobbyFormError(c, who, err, hyperchi.H{"code": c.FormValue("code")})
	}
	room, ok := srv.arena.Room(in.Code)
	if !ok {
		return srv.lobbyFormError(c, who, hyperchi.Unprocessable("No such table.",
			map[string][]string{"code": {"does not match an open table"}}), hyperchi.H{"code": in.Code})
	}
	// Take a seat when there is one; otherwise (a game running, a full table,
	// or no name yet) the visitor watches and can sit from the room page.
	err = room.Sit(who)
	var rule RuleError
	if err != nil && !errors.As(err, &rule) && !errors.Is(err, errRoomGone) {
		return err
	}
	return c.Redirect("/r/" + room.Code())
}

func (srv *server) roomPage(c *hyperchi.Context) error {
	code := normalizeCode(c.Param("code"))
	room, ok := srv.arena.Room(code)
	if !ok {
		return hyperchi.NotFound("No open table has the code " + code + ". It may have emptied out.")
	}
	who := identityFrom(c.Ctx())
	return c.View("room", hyperchi.H{
		"title": "Table " + code + " · Dice Arena",
		"code":  code,
		"me":    who,
		"b":     room.Board(),
		"t":     room.Table(who),
		"csrf":  c.CSRFToken(),
	})
}
