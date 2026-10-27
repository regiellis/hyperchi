package main

import (
	"crypto/rand"
	"errors"
	"fmt"
	"log"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/regiellis/hyperchi/hyperchi"
)

// Locking: every Room has its own mutex, and every change to a room and the
// pushes that announce it happen under that mutex, so each client sees one
// room's updates in order. The Arena mutex guards the room index and the lobby
// listing. A goroutine holding a room's mutex may take the Arena's, never the
// other way round: Arena methods release their mutex before they touch a room.

// MaxSeats is the most players one table seats.
const (
	MaxSeats      = 4
	logLength     = 8
	codeLength    = 5
	codeAlphabet  = "ABCDEFGHJKMNPQRSTUVWXYZ23456789" // no I, L, O, 0, or 1
	lobbyChannel  = "lobby"
	defaultGrace  = 45 * time.Second
	defaultRooms  = 500
	defaultTarget = 5000
)

var (
	errRoomGone   = errors.New("room closed")
	errArenaFull  = errors.New("arena full")
	targetChoices = []TargetOption{{2000, "2,000"}, {5000, "5,000"}, {10000, "10,000"}}
)

// TargetOption is one choice of winning score when opening a table.
type TargetOption struct {
	Value int
	Label string
}

// viewer is a connected page. *hyperchi.Socket is the real one; tests use a
// recorder.
type viewer interface {
	Swap(target, swap, name string, data any) error
}

// roomStatus is where a table is in its life: waiting for players, playing a
// game, or finished and ready for a rematch.
type roomStatus string

const (
	statusWaiting  roomStatus = "waiting"
	statusPlaying  roomStatus = "playing"
	statusFinished roomStatus = "finished"
)

// Options tune an Arena. Zero values take the defaults.
type Options struct {
	Grace     time.Duration // how long a disconnected player keeps their seat
	Roller    Roller
	AfterFunc func(d time.Duration, f func()) (stop func() bool) // timer hook for tests
	MaxRooms  int
}

// Arena owns every room and the lobby listing.
type Arena struct {
	app  *hyperchi.HyperChi
	opts Options

	mu      sync.Mutex
	rooms   map[string]*Room
	listing map[string]RoomSummary
	created int // a counter, so the lobby lists rooms in creation order
}

// NewArena returns an empty arena that pushes updates through app.
func NewArena(app *hyperchi.HyperChi, opts Options) *Arena {
	if opts.Grace <= 0 {
		opts.Grace = defaultGrace
	}
	if opts.Roller == nil {
		opts.Roller = CryptoRoller
	}
	if opts.AfterFunc == nil {
		opts.AfterFunc = func(d time.Duration, f func()) func() bool { return time.AfterFunc(d, f).Stop }
	}
	if opts.MaxRooms <= 0 {
		opts.MaxRooms = defaultRooms
	}
	return &Arena{app: app, opts: opts, rooms: map[string]*Room{}, listing: map[string]RoomSummary{}}
}

// RoomSummary is one row of the lobby listing.
type RoomSummary struct {
	Code   string
	Target string
	Status roomStatus
	Seated int
	Names  string
	CanSit bool
	order  int
}

// Create opens a room with owner in the first seat.
func (a *Arena) Create(owner Identity, target int) (*Room, error) {
	if owner.Name == "" {
		return nil, RuleError("Pick a name before you open a table.")
	}
	a.mu.Lock()
	if len(a.rooms) >= a.opts.MaxRooms {
		a.mu.Unlock()
		return nil, errArenaFull
	}
	code, err := a.newCodeLocked()
	if err != nil {
		a.mu.Unlock()
		return nil, err
	}
	a.created++
	r := &Room{arena: a, code: code, target: target, order: a.created, status: statusWaiting, viewers: map[viewer]Identity{}}
	// Seat the owner before the room is reachable, so nobody can see it empty.
	if err := r.sitLocked(owner); err != nil {
		a.mu.Unlock()
		return nil, err
	}
	a.rooms[code] = r
	a.mu.Unlock()

	r.mu.Lock()
	defer r.mu.Unlock()
	return r, r.publishLocked(false)
}

func (a *Arena) newCodeLocked() (string, error) {
	for range 20 {
		code, err := randomCode()
		if err != nil {
			return "", err
		}
		if _, taken := a.rooms[code]; !taken {
			return code, nil
		}
	}
	return "", errors.New("no free room code")
}

// Room finds an open room by code.
func (a *Arena) Room(code string) (*Room, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	r, ok := a.rooms[code]
	return r, ok
}

// Listing returns the lobby rows, oldest room first.
func (a *Arena) Listing() []RoomSummary {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.listingLocked()
}

func (a *Arena) listingLocked() []RoomSummary {
	out := make([]RoomSummary, 0, len(a.listing))
	for _, s := range a.listing {
		out = append(out, s)
	}
	slices.SortFunc(out, func(x, y RoomSummary) int { return x.order - y.order })
	return out
}

// updateListing records a room's lobby row (nil removes it) and pushes the
// listing to every lobby page. Callers hold the room's mutex.
func (a *Arena) updateListing(code string, s *RoomSummary) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if s == nil {
		delete(a.rooms, code)
		delete(a.listing, code)
	} else {
		a.listing[code] = *s
	}
	return a.app.Room(lobbyChannel).Swap("#open-rooms", "innerHTML", "room-list", hyperchi.H{"rooms": a.listingLocked()})
}

// Room is one table: seats, viewers, and the game in progress.
type Room struct {
	arena  *Arena
	code   string
	target int
	order  int

	mu      sync.Mutex
	seats   []*seat
	viewers map[viewer]Identity
	game    *Game
	status  roomStatus
	starter int      // seat index that rolls first; rotates on rematch
	log     []string // newest last
	news    string   // what changed since the last publish, for announcements
	closed  bool
}

type seat struct {
	id, name string
	sockets  int  // open pages for this player
	away     bool // lost every page; the seat is held until the grace timer fires
	seen     bool // has had a page open, so a reconnect is "back"
	gen      int  // bumped per disconnect so a stale timer does nothing
	stop     func() bool
}

// Code is the room's share code.
func (r *Room) Code() string { return r.code }

func roomChannel(code string) string { return "room:" + code }

func (r *Room) seatOf(id string) *seat {
	for _, s := range r.seats {
		if s.id == id {
			return s
		}
	}
	return nil
}

func (r *Room) name(id string) string {
	if s := r.seatOf(id); s != nil {
		return s.name
	}
	return "A player"
}

// note adds a line to the room log and to the next announcement.
func (r *Room) note(format string, args ...any) {
	line := fmt.Sprintf(format, args...)
	r.log = append(r.log, line)
	if len(r.log) > logLength {
		r.log = r.log[len(r.log)-logLength:]
	}
	if r.news == "" {
		r.news = line
	} else {
		r.news += " " + line
	}
}

// Sit gives who a seat if the table has room and no game is running.
func (r *Room) Sit(who Identity) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return errRoomGone
	}
	if err := r.sitLocked(who); err != nil {
		return err
	}
	return r.publishLocked(false)
}

func (r *Room) sitLocked(who Identity) error {
	switch {
	case who.ID == "":
		return RuleError("Your browser did not send a player ID. Reload the page.")
	case who.Name == "":
		return RuleError("Pick a name in the lobby before you take a seat.")
	case r.seatOf(who.ID) != nil:
		return nil
	case r.status == statusPlaying:
		return RuleError("A game is in progress. Watch, and take a seat when it ends.")
	case len(r.seats) >= MaxSeats:
		return RuleError("The table is full.")
	}
	s := &seat{id: who.ID, name: who.Name}
	for _, v := range r.viewers {
		if v.ID == who.ID {
			s.sockets++
		}
	}
	s.seen = s.sockets > 0
	if s.sockets == 0 {
		// Seated from the lobby before the room page connects: hold the seat
		// for the grace window like a disconnect.
		r.holdLocked(s)
	}
	r.seats = append(r.seats, s)
	r.note("%s took a seat.", who.Name)
	return nil
}

// holdLocked starts the grace timer for a seat with no open page.
func (r *Room) holdLocked(s *seat) {
	s.away = true
	s.gen++
	gen, id := s.gen, s.id
	s.stop = r.arena.opts.AfterFunc(r.arena.opts.Grace, func() {
		if err := r.expire(id, gen); err != nil {
			log.Printf("dice-arena: room %s: release seat: %v", r.code, err)
		}
	})
}

func (s *seat) release() {
	if s.stop != nil {
		s.stop()
		s.stop = nil
	}
}

// Attach registers a connected page. A returning player gets their seat back.
func (r *Room) Attach(v viewer, who Identity) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return errRoomGone
	}
	r.viewers[v] = who
	s := r.seatOf(who.ID)
	if s == nil {
		if err := r.publishBoardLocked(); err != nil {
			return err
		}
		return r.sendTableLocked(v, who, false)
	}
	s.sockets++
	if s.sockets > 1 {
		return r.sendTableLocked(v, who, false)
	}
	s.release()
	s.away = false
	if s.seen {
		r.note("%s is back.", s.name)
	}
	s.seen = true
	return r.publishLocked(false)
}

// Detach forgets a page. A player who closed their last page keeps the seat
// for the grace window; an empty room closes.
func (r *Room) Detach(v viewer, who Identity) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.viewers[v]; !ok || r.closed {
		return nil
	}
	delete(r.viewers, v)
	var err error
	if s := r.seatOf(who.ID); s != nil {
		s.sockets--
		if s.sockets == 0 {
			r.holdLocked(s)
			r.note("%s lost connection. Holding the seat for %d seconds.", s.name, int(r.arena.opts.Grace.Seconds()))
			err = r.publishLocked(false)
		}
	} else {
		err = r.publishBoardLocked()
	}
	return errors.Join(err, r.closeIfEmptyLocked())
}

// expire releases a seat whose player did not come back in time.
func (r *Room) expire(id string, gen int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	s := r.seatOf(id)
	if s == nil || s.gen != gen || s.sockets > 0 {
		return nil
	}
	r.removeSeatLocked(s, "%s did not come back and left the table.")
	return errors.Join(r.publishLocked(false), r.closeIfEmptyLocked())
}

func (r *Room) removeSeatLocked(s *seat, format string) {
	s.release()
	r.seats = slices.DeleteFunc(r.seats, func(x *seat) bool { return x == s })
	r.note(format, s.name)
	if r.status == statusPlaying && r.game != nil {
		r.game.Remove(s.id)
		if r.game.Over {
			r.status = statusFinished
			if r.game.Winner != "" {
				r.note("%s wins: everyone else left.", r.name(r.game.Winner))
			}
		}
	}
}

func (r *Room) closeIfEmptyLocked() error {
	if len(r.seats) > 0 || len(r.viewers) > 0 {
		return nil
	}
	r.closed = true
	return r.arena.updateListing(r.code, nil)
}

// Act applies a player's move. A RuleError is the player's mistake, for them
// alone to see; any other error is the server's.
func (r *Room) Act(who Identity, in Action) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return errRoomGone
	}
	rolled := false
	switch in.Action {
	case "sit":
		if err := r.sitLocked(who); err != nil {
			return err
		}
	case "leave":
		s := r.seatOf(who.ID)
		if s == nil {
			return RuleError("You are not seated.")
		}
		r.removeSeatLocked(s, "%s left the table.")
	case "start", "rematch":
		if err := r.startLocked(who.ID, in.Action == "rematch"); err != nil {
			return err
		}
	case "roll", "bank":
		if r.status != statusPlaying {
			return RuleError("No game is running.")
		}
		var err error
		if in.Action == "roll" {
			err = r.game.Roll(who.ID, in.Keep)
		} else {
			err = r.game.Bank(who.ID, in.Keep)
		}
		if err != nil {
			return err
		}
		rolled = r.game.Last.Kind == EventRolled || r.game.Last.Kind == EventBust
		r.describeLocked(r.game.Last)
		if r.game.Over {
			r.status = statusFinished
		}
	default:
		return RuleError("That move is not part of this game.")
	}
	return r.publishLocked(rolled)
}

func (r *Room) startLocked(id string, rematch bool) error {
	if r.seatOf(id) == nil {
		return RuleError("Take a seat to start a game.")
	}
	switch {
	case rematch && r.status != statusFinished:
		return RuleError("There is no finished game to replay.")
	case !rematch && r.status != statusWaiting:
		return RuleError("The game has already started.")
	case len(r.seats) < 2:
		return RuleError("You need at least two players.")
	}
	if rematch {
		r.starter++
	}
	first := r.starter % len(r.seats)
	order := make([]string, 0, len(r.seats))
	for i := range r.seats {
		order = append(order, r.seats[(first+i)%len(r.seats)].id)
	}
	r.game = NewGame(order, r.target, r.arena.opts.Roller)
	r.status = statusPlaying
	r.note("New game to %s. %s rolls first.", points(r.target), r.name(order[0]))
	return nil
}

// Preview scores a selection for the player whose turn it is.
func (r *Room) Preview(id string, keep []int) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.status != statusPlaying {
		return 0, RuleError("No game is running.")
	}
	return r.game.Preview(id, keep)
}

func (r *Room) describeLocked(e Event) {
	who := r.name(e.Player)
	switch e.Kind {
	case EventRolled:
		if e.Points > 0 {
			r.note("%s rolled %s with %s set aside.", who, faces(e.Values), points(e.Points))
		} else {
			r.note("%s rolled %s.", who, faces(e.Values))
		}
	case EventBust:
		if e.Points > 0 {
			r.note("%s rolled %s: no score. Bust, %s lost.", who, faces(e.Values), points(e.Points))
		} else {
			r.note("%s rolled %s: no score. Bust.", who, faces(e.Values))
		}
	case EventBanked:
		r.note("%s banked %s and has %s.", who, points(e.Points), points(e.Total))
	case EventWon:
		r.note("%s banked %s and wins with %s.", who, points(e.Points), points(e.Total))
	}
}

// Publishing. Every method below runs with r.mu held.

// publishLocked pushes the shared board to the room channel, then each page
// its own table and announcement. rolled marks a fresh roll, which animates.
func (r *Room) publishLocked(rolled bool) error {
	if err := r.publishBoardLocked(); err != nil {
		return err
	}
	news := r.news
	r.news = ""
	for v, who := range r.viewers {
		if err := r.sendTableLocked(v, who, rolled); err != nil {
			return err
		}
		if news == "" {
			continue
		}
		text := news
		if r.status == statusPlaying && r.game.Current() == who.ID {
			text += " Your turn."
		}
		if err := sent(v.Swap("#announce", "innerHTML", "announce", hyperchi.H{"text": text})); err != nil {
			return err
		}
	}
	summary := r.summaryLocked()
	return r.arena.updateListing(r.code, &summary)
}

// publishBoardLocked broadcasts the roster, scores, and log to everyone in
// the room with one render.
func (r *Room) publishBoardLocked() error {
	return r.arena.app.Room(roomChannel(r.code)).Swap("#board", "innerHTML", "board", hyperchi.H{"b": r.boardLocked()})
}

func (r *Room) sendTableLocked(v viewer, who Identity, rolled bool) error {
	return sent(v.Swap("#table", "innerHTML", "table", hyperchi.H{"t": r.tableLocked(who, rolled)}))
}

// sent treats a page that closed mid-push as delivered: its socket handler
// is about to Detach it.
func sent(err error) error {
	if errors.Is(err, hyperchi.ErrSocketClosed) {
		return nil
	}
	return err
}

func (r *Room) summaryLocked() RoomSummary {
	s := RoomSummary{
		Code:   r.code,
		Target: points(r.target),
		Status: r.status,
		Seated: len(r.seats),
		CanSit: r.status != statusPlaying && len(r.seats) < MaxSeats,
		order:  r.order,
	}
	names := make([]string, 0, len(r.seats))
	for _, seat := range r.seats {
		names = append(names, seat.name)
	}
	s.Names = strings.Join(names, ", ")
	return s
}

// Views. Each is a fresh copy, safe to render after the mutex is released.

// BoardView is the part of the room everyone sees the same way.
type BoardView struct {
	Code       string
	Target     string
	Status     roomStatus
	Seats      []SeatView
	Free       int
	Spectators int
	Log        []string
	Winner     string
}

// SeatView is one row of the roster.
type SeatView struct {
	Name   string
	Score  string
	Turn   bool
	Away   bool
	Winner bool
}

// Board returns the shared view, for the first page render.
func (r *Room) Board() BoardView {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.boardLocked()
}

func (r *Room) boardLocked() BoardView {
	b := BoardView{
		Code:   r.code,
		Target: points(r.target),
		Status: r.status,
		Free:   MaxSeats - len(r.seats),
		Log:    slices.Clone(r.log),
	}
	current := ""
	if r.game != nil {
		current = r.game.Current()
		if r.game.Winner != "" {
			b.Winner = r.name(r.game.Winner)
		}
	}
	for _, s := range r.seats {
		sv := SeatView{Name: s.name, Score: "0", Away: s.away, Turn: s.id == current}
		if r.game != nil {
			sv.Score = points(r.game.Scores[s.id])
			sv.Winner = r.game.Winner == s.id
		}
		b.Seats = append(b.Seats, sv)
	}
	watchers := map[string]bool{}
	for _, who := range r.viewers {
		if r.seatOf(who.ID) == nil {
			watchers[who.ID] = true
		}
	}
	b.Spectators = len(watchers)
	return b
}

// TableView is the dice and controls as one viewer sees them.
type TableView struct {
	Code        string
	Status      roomStatus
	Target      string
	Seated      bool
	Named       bool
	CanSit      bool
	CanStart    bool
	CanRematch  bool
	NeedPlayers int
	Seats       int

	MyTurn      bool
	Current     string
	CurrentAway bool
	Dice        []DieView // on the table, choosable
	Held        []DieView // set aside this turn
	Rolled      bool      // a roll waits for dice to be set aside
	CanRoll     bool
	CanBank     bool
	RollsLeft   int
	MaxRolls    int
	TurnPts     string
	Animate     bool
	Last        string

	Over   bool
	Winner string
	YouWon bool
}

// DieView is one die face.
type DieView struct {
	Index int
	Value int
	Cells [9]bool // a 3x3 pip grid
}

var pipCells = [7][]int{1: {4}, 2: {2, 6}, 3: {2, 4, 6}, 4: {0, 2, 6, 8}, 5: {0, 2, 4, 6, 8}, 6: {0, 2, 3, 5, 6, 8}}

func dieView(i, value int) DieView {
	d := DieView{Index: i, Value: value}
	if value >= 1 && value <= 6 {
		for _, c := range pipCells[value] {
			d.Cells[c] = true
		}
	}
	return d
}

// Table returns who's view of the table, for the first page render.
func (r *Room) Table(who Identity) TableView {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.tableLocked(who, false)
}

func (r *Room) tableLocked(who Identity, rolled bool) TableView {
	mine := r.seatOf(who.ID)
	t := TableView{
		Code:     r.code,
		Status:   r.status,
		Target:   points(r.target),
		Seated:   mine != nil,
		Named:    who.Name != "",
		Seats:    len(r.seats),
		MaxRolls: MaxRolls,
		Animate:  rolled,
	}
	if len(r.log) > 0 {
		t.Last = r.log[len(r.log)-1]
	}
	t.CanSit = mine == nil && r.status != statusPlaying && len(r.seats) < MaxSeats
	if mine != nil && len(r.seats) < 2 {
		t.NeedPlayers = 2 - len(r.seats)
	}
	t.CanStart = mine != nil && r.status == statusWaiting && len(r.seats) >= 2
	t.CanRematch = mine != nil && r.status == statusFinished && len(r.seats) >= 2
	g := r.game
	if g == nil || r.status == statusWaiting {
		return t
	}
	t.Over = g.Over
	if g.Winner != "" {
		t.Winner = r.name(g.Winner)
		t.YouWon = g.Winner == who.ID
	}
	if g.Over {
		return t
	}
	current := g.Current()
	t.Current = r.name(current)
	if s := r.seatOf(current); s != nil {
		t.CurrentAway = s.away
	}
	t.MyTurn = current == who.ID
	t.Rolled = g.Rolled
	t.RollsLeft = g.RollsLeft()
	t.TurnPts = points(g.TurnPts)
	t.CanRoll = t.MyTurn && (!g.Rolled || g.Rolls < MaxRolls)
	t.CanBank = t.MyTurn && g.Rolled
	for i, d := range g.Dice {
		if d.Held {
			t.Held = append(t.Held, dieView(i, d.Value))
		} else {
			t.Dice = append(t.Dice, dieView(i, d.Value))
		}
	}
	return t
}

// Formatting.

func points(n int) string {
	s := strconv.Itoa(n)
	if n < 0 {
		return "-" + points(-n)
	}
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

func faces(values []int) string {
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = strconv.Itoa(v)
	}
	return strings.Join(parts, " ")
}

// randomCode draws a room code from crypto/rand, rejecting bytes that would
// bias the result toward the start of the alphabet.
func randomCode() (string, error) {
	limit := byte(256 / len(codeAlphabet) * len(codeAlphabet))
	out := make([]byte, 0, codeLength)
	var b [1]byte
	for len(out) < codeLength {
		if _, err := rand.Read(b[:]); err != nil {
			return "", fmt.Errorf("room code: %w", err)
		}
		if b[0] < limit {
			out = append(out, codeAlphabet[int(b[0])%len(codeAlphabet)])
		}
	}
	return string(out), nil
}
