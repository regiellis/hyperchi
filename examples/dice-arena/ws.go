package main

import (
	"errors"
	"fmt"
	"log"
	"slices"
	"sort"
	"strings"

	"github.com/regiellis/hyperchi/hyperchi"
)

// Action is one message from a room page. The htmx ws extension sends the
// form's fields as JSON: {"action": "roll", "keep": ["0", "3"], "headers": {...}}.
type Action struct {
	Action string `form:"action" validate:"required,oneof=sit leave start rematch roll bank preview"`
	Keep   []int  `form:"keep" validate:"max=6"`
}

// Validate rejects die indexes that cannot exist; the game checks the rest.
func (a *Action) Validate() error {
	for _, i := range a.Keep {
		if i < 0 || i >= NumDice {
			return hyperchi.Unprocessable("Please correct the highlighted fields.",
				map[string][]string{"keep": {fmt.Sprintf("must be a die from 0 to %d", NumDice-1)}})
		}
	}
	return nil
}

// lobbySocket pushes the open-tables list to the lobby page. It only sends.
func (srv *server) lobbySocket(s *hyperchi.Socket) error {
	s.Join(lobbyChannel)
	// The page rendered the list on load; send it again in case a table
	// opened between that render and this connection.
	if err := s.Swap("#open-rooms", "innerHTML", "room-list", hyperchi.H{"rooms": srv.arena.Listing()}); err != nil {
		return err
	}
	return s.Wait()
}

// roomSocket is one page connected to a table, seated or watching. It reads
// moves until the page goes away; every result is pushed by the room.
func (srv *server) roomSocket(s *hyperchi.Socket) error {
	code := normalizeCode(s.Context().Param("code"))
	// The hub identified the connection from the signed player cookie
	// (SetIdentifyUser in newApp). The name rides along from the same request.
	who := Identity{ID: s.UserID(), Name: identityFrom(s.Context().Ctx()).Name}

	room, ok := srv.arena.Room(code)
	if !ok {
		return closedTable(s)
	}
	s.Join(roomChannel(code))
	if err := room.Attach(s, who); err != nil {
		if errors.Is(err, errRoomGone) {
			return closedTable(s)
		}
		return err
	}
	defer func() {
		if err := room.Detach(s, who); err != nil {
			log.Printf("dice-arena: room %s: detach: %v", code, err)
		}
	}()

	for {
		msg, err := s.Receive()
		if err != nil {
			return err
		}
		in, err := hyperchi.BindMessage[Action](msg)
		if err != nil {
			var bad *hyperchi.HTTPError
			if errors.As(err, &bad) {
				if err := flash(s, describeBindError(bad)); err != nil {
					return err
				}
				continue
			}
			return err
		}

		if in.Action == "preview" {
			if err := preview(s, room, who.ID, in.Keep); err != nil {
				return err
			}
			continue
		}

		err = room.Act(who, in)
		var rule RuleError
		switch {
		case errors.As(err, &rule):
			// A move the rules refuse goes back to the sender only.
			if err := flash(s, rule.Error()); err != nil {
				return err
			}
		case errors.Is(err, errRoomGone):
			return closedTable(s)
		case err != nil:
			return err
		default:
			if err := flash(s, ""); err != nil {
				return err
			}
		}
	}
}

// preview answers a change of selection with what it would score, to the
// sender only. Nothing changes on the table.
func preview(s *hyperchi.Socket, room *Room, id string, keep []int) error {
	data := hyperchi.H{"picked": len(keep)}
	if len(keep) > 0 {
		pts, err := room.Preview(id, keep)
		var rule RuleError
		switch {
		case errors.As(err, &rule):
			data["problem"] = rule.Error()
		case err != nil:
			return err
		default:
			data["points"] = points(pts)
		}
	}
	return s.Swap("#preview", "innerHTML", "preview", data)
}

// flash shows (or, with "", clears) a message for this page only.
func flash(s *hyperchi.Socket, message string) error {
	return s.Swap("#flash", "innerHTML", "flash", hyperchi.H{"message": message})
}

// closedTable tells the page its table is gone and keeps the socket open
// until the page leaves. Closing it from here would fire htmx:ws:close, and
// the page would show its "Connection lost. Reconnecting" banner under a
// table that is never coming back.
func closedTable(s *hyperchi.Socket) error {
	if err := s.Swap("#table", "innerHTML", "table-gone", nil); err != nil {
		return err
	}
	return s.Wait()
}

// describeBindError turns a rejected message into one line for the player.
func describeBindError(err *hyperchi.HTTPError) string {
	if len(err.Fields) == 0 {
		return "The game did not understand that message: " + strings.TrimSuffix(err.Message, ".") + "."
	}
	names := make([]string, 0, len(err.Fields))
	for name := range err.Fields {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, name+" "+strings.Join(slices.Compact(err.Fields[name]), ", "))
	}
	return "The game did not understand that message: " + strings.Join(parts, "; ") + "."
}

func normalizeCode(code string) string {
	return strings.ToUpper(strings.TrimSpace(code))
}
