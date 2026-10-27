package main

import (
	"context"
	"crypto/rand"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/regiellis/hyperchi/hyperchi"
)

// Identity is who is asking: a player ID the server minted, and the display
// name they picked (empty until they pick one).
type Identity struct {
	ID   string
	Name string
}

type identityKey struct{}

const (
	// playerCookie holds the player ID and name as one value, signed with the
	// app's SECRET_KEY by c.SetSignedCookie. Signing them together means a
	// name cannot be moved to another player, and only the server can mint a
	// valid ID, so a client cannot claim someone else's seat.
	playerCookie = "dice_player"
	cookieMaxAge = 30 * 24 * time.Hour
)

// setIdentity signs who into the player cookie. The framework sends it
// HttpOnly, SameSite=Lax (which keeps it on links into a room from another
// site and off cross-site POSTs), and Secure in production.
func setIdentity(c *hyperchi.Context, who Identity) error {
	return c.SetSignedCookie(&http.Cookie{
		Name:   playerCookie,
		Value:  who.ID + ":" + who.Name, // IDs are base32, so the first colon splits them
		MaxAge: int(cookieMaxAge.Seconds()),
	})
}

// readIdentity checks the player cookie's signature and age and splits it.
// The error wraps hyperchi.ErrCookieMissing, ErrCookieInvalid, or
// ErrCookieExpired for a visitor who needs a new ID.
func readIdentity(c *hyperchi.Context) (Identity, error) {
	value, err := c.SignedCookie(playerCookie, cookieMaxAge)
	if err != nil {
		return Identity{}, err
	}
	id, name, _ := strings.Cut(value, ":")
	if id == "" {
		return Identity{}, hyperchi.ErrCookieInvalid
	}
	return Identity{ID: id, Name: name}, nil
}

// identityMiddleware gives every request an Identity. A visitor without a
// valid player cookie gets a new random ID. It runs before the WebSocket
// handshake too, so the hub's SetIdentifyUser can read the ID from the
// request context. Signing fails only without a secret key in production,
// which newApp refuses at startup.
func identityMiddleware(app *hyperchi.HyperChi) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c := app.NewContext(w, r)
			who, err := readIdentity(c)
			if err != nil {
				if errors.Is(err, hyperchi.ErrNoSecretKey) {
					app.RespondStatus(w, r, http.StatusInternalServerError, err)
					return
				}
				who = Identity{ID: rand.Text()}
				if err := setIdentity(c, who); err != nil {
					app.RespondStatus(w, r, http.StatusInternalServerError, err)
					return
				}
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), identityKey{}, who)))
		})
	}
}

// identityFrom reads the Identity the middleware attached.
func identityFrom(ctx context.Context) Identity {
	who, ok := ctx.Value(identityKey{}).(Identity)
	if !ok {
		return Identity{}
	}
	return who
}
