package main

import (
	"crypto/rand"
	"fmt"
	"slices"
)

// Hot Dice, a variant of Farkle. The rules live here as pure functions and a
// Game type with no I/O, so they can be tested with scripted dice.
//
//   - Six dice. On your turn, roll all six.
//   - After every roll, set aside at least one scoring die. Then roll the rest
//     again, or bank: add the points set aside this turn to your score.
//   - A roll with no scoring dice is a bust: the turn's points are lost.
//   - Set aside all six and you have hot dice: the next roll uses all six again.
//   - A turn has at most MaxRolls rolls. After the last one, bank.
//   - The first player to reach the target score wins.
const (
	NumDice  = 6
	MaxRolls = 4
)

// Roller returns n dice values from 1 to 6. Games use CryptoRoller; tests
// inject a scripted one.
type Roller func(n int) ([]int, error)

// CryptoRoller rolls with crypto/rand, rejecting bytes that would bias the
// result toward low faces.
func CryptoRoller(n int) ([]int, error) {
	out := make([]int, n)
	var b [1]byte
	for i := range out {
		for {
			if _, err := rand.Read(b[:]); err != nil {
				return nil, fmt.Errorf("roll dice: %w", err)
			}
			if b[0] < 252 { // 252 is the largest multiple of 6 below 256
				out[i] = int(b[0]%6) + 1
				break
			}
		}
	}
	return out, nil
}

// Score returns the points for dice set aside together, and whether every die
// in the set counts toward them. A set with a die that does not score is not
// a valid choice.
//
// Scoring: a single 1 is 100 and a single 5 is 50. Three of a kind is 100
// times the face (three 1s are 1,000), and each extra matching die doubles it.
// With all six dice, a straight (1 to 6) is 1,500 and three pairs are 750.
func Score(dice []int) (int, bool) {
	if len(dice) == 0 {
		return 0, false
	}
	var counts [7]int
	for _, d := range dice {
		if d < 1 || d > 6 {
			return 0, false
		}
		counts[d]++
	}
	if len(dice) == NumDice {
		straight, pairs := true, 0
		for face := 1; face <= 6; face++ {
			if counts[face] != 1 {
				straight = false
			}
			if counts[face] == 2 {
				pairs++
			}
		}
		if straight {
			return 1500, true
		}
		if pairs == 3 {
			return 750, true
		}
	}
	total := 0
	for face := 1; face <= 6; face++ {
		n := counts[face]
		switch {
		case n >= 3:
			base := face * 100
			if face == 1 {
				base = 1000
			}
			total += base << (n - 3)
		case face == 1:
			total += 100 * n
		case face == 5:
			total += 50 * n
		case n > 0:
			return 0, false
		}
	}
	return total, true
}

// HasScore reports whether a roll offers anything to set aside. A roll
// without it is a bust.
func HasScore(dice []int) bool {
	var counts [7]int
	for _, d := range dice {
		if d >= 1 && d <= 6 {
			counts[d]++
		}
	}
	if counts[1] > 0 || counts[5] > 0 {
		return true
	}
	pairs := 0
	for face := 2; face <= 6; face++ {
		if counts[face] >= 3 {
			return true
		}
		if counts[face] == 2 {
			pairs++
		}
	}
	return len(dice) == NumDice && pairs == 3
}

// RuleError is a move the rules do not allow. Its text is shown to the player
// who tried it.
type RuleError string

// Error returns the message shown to the player.
func (e RuleError) Error() string { return string(e) }

// The rule errors, worded for the player.
const (
	ErrGameOver    RuleError = "The game is over."
	ErrNotYourTurn RuleError = "It is not your turn."
	ErrRollFirst   RuleError = "Roll the dice first."
	ErrPickDice    RuleError = "Set aside at least one scoring die."
	ErrNoScore     RuleError = "Every die you set aside has to score."
	ErrBadDie      RuleError = "That die is not on the table."
	ErrNoRollsLeft RuleError = "That was your last roll. Bank your points."
)

// Die is one of the six dice. Held dice were set aside earlier this turn.
type Die struct {
	Value int
	Held  bool
}

// EventKind names what the last move did.
type EventKind string

// The kinds of move an Event reports.
const (
	EventRolled  EventKind = "rolled"
	EventBust    EventKind = "bust"
	EventBanked  EventKind = "banked"
	EventWon     EventKind = "won"
	EventForfeit EventKind = "forfeit"
)

// Event describes the last move, for the log and screen-reader announcements.
type Event struct {
	Player string
	Kind   EventKind
	Values []int // the dice rolled, for rolled and bust
	Points int   // turn points for rolled, lost points for bust, banked points otherwise
	Total  int   // the player's score after banking
}

// Game is one game of Hot Dice between players identified by ID. It is not
// safe for concurrent use; Room serializes access.
type Game struct {
	Target  int
	Order   []string // turn order
	Scores  map[string]int
	Dice    [NumDice]Die
	Rolls   int  // rolls used this turn
	TurnPts int  // points set aside this turn
	Rolled  bool // a roll is on the table, waiting for dice to be set aside
	Over    bool
	Winner  string
	Last    Event

	turn int
	roll Roller
}

// NewGame starts a game. order is the turn order; the first player rolls first.
func NewGame(order []string, target int, roll Roller) *Game {
	g := &Game{
		Target: target,
		Order:  slices.Clone(order),
		Scores: make(map[string]int, len(order)),
		roll:   roll,
	}
	for _, id := range order {
		g.Scores[id] = 0
	}
	return g
}

// Current is the player whose turn it is, or "" when the game is over.
func (g *Game) Current() string {
	if g.Over || len(g.Order) == 0 {
		return ""
	}
	return g.Order[g.turn]
}

// RollsLeft is how many rolls remain in the current turn.
func (g *Game) RollsLeft() int { return MaxRolls - g.Rolls }

// Roll sets aside the dice at the keep indexes (required once a roll is on the
// table) and rolls the rest. All six dice set aside means hot dice: all six
// roll again.
func (g *Game) Roll(player string, keep []int) error {
	if err := g.checkTurn(player); err != nil {
		return err
	}
	pts := 0
	if g.Rolled {
		if g.Rolls >= MaxRolls {
			return ErrNoRollsLeft
		}
		var err error
		if pts, err = g.scoreKeep(keep); err != nil {
			return err
		}
	} else if len(keep) > 0 {
		return ErrRollFirst
	}

	free := g.freeCount() - len(keep)
	if free == 0 {
		free = NumDice
	}
	values, err := g.roll(free)
	if err != nil {
		return err
	}
	if len(values) != free {
		return fmt.Errorf("roller returned %d dice, want %d", len(values), free)
	}

	g.setAside(keep, pts)
	if g.freeCount() == 0 {
		for i := range g.Dice {
			g.Dice[i] = Die{}
		}
	}
	next := 0
	for i := range g.Dice {
		if !g.Dice[i].Held {
			g.Dice[i].Value = values[next]
			next++
		}
	}
	g.Rolls++
	g.Rolled = true

	if !HasScore(values) {
		g.Last = Event{Player: player, Kind: EventBust, Values: values, Points: g.TurnPts}
		g.advance()
		return nil
	}
	g.Last = Event{Player: player, Kind: EventRolled, Values: values, Points: g.TurnPts}
	return nil
}

// Bank sets aside the dice at the keep indexes and adds the turn's points to
// the player's score. Reaching the target wins the game.
func (g *Game) Bank(player string, keep []int) error {
	if err := g.checkTurn(player); err != nil {
		return err
	}
	if !g.Rolled {
		return ErrRollFirst
	}
	pts, err := g.scoreKeep(keep)
	if err != nil {
		return err
	}
	g.setAside(keep, pts)
	g.Scores[player] += g.TurnPts
	total := g.Scores[player]
	g.Last = Event{Player: player, Kind: EventBanked, Points: g.TurnPts, Total: total}
	if total >= g.Target {
		g.Over = true
		g.Winner = player
		g.Last.Kind = EventWon
		g.Rolled = false
		return nil
	}
	g.advance()
	return nil
}

// Preview scores a possible selection without changing the game.
func (g *Game) Preview(player string, keep []int) (int, error) {
	if err := g.checkTurn(player); err != nil {
		return 0, err
	}
	if !g.Rolled {
		return 0, ErrRollFirst
	}
	return g.scoreKeep(keep)
}

// Remove takes a player out of the game. If they were rolling, the turn passes
// on. A game left with one player ends with that player as the winner.
func (g *Game) Remove(player string) {
	i := slices.Index(g.Order, player)
	if i < 0 {
		return
	}
	g.Order = slices.Delete(g.Order, i, i+1)
	delete(g.Scores, player)
	if g.Over {
		if g.turn >= len(g.Order) {
			g.turn = 0
		}
		return
	}
	switch {
	case i < g.turn:
		g.turn--
	case i == g.turn:
		g.resetTurn()
		if g.turn >= len(g.Order) {
			g.turn = 0
		}
	}
	switch len(g.Order) {
	case 0:
		g.Over = true
	case 1:
		g.Over = true
		g.Winner = g.Order[0]
		g.Last = Event{Player: g.Winner, Kind: EventForfeit, Total: g.Scores[g.Winner]}
	}
}

func (g *Game) checkTurn(player string) error {
	if g.Over {
		return ErrGameOver
	}
	if player == "" || player != g.Current() {
		return ErrNotYourTurn
	}
	return nil
}

// scoreKeep validates a selection of dice still on the table and scores it.
func (g *Game) scoreKeep(keep []int) (int, error) {
	if len(keep) == 0 {
		return 0, ErrPickDice
	}
	values := make([]int, 0, len(keep))
	seen := map[int]bool{}
	for _, i := range keep {
		if i < 0 || i >= NumDice || seen[i] || g.Dice[i].Held || g.Dice[i].Value == 0 {
			return 0, ErrBadDie
		}
		seen[i] = true
		values = append(values, g.Dice[i].Value)
	}
	pts, ok := Score(values)
	if !ok {
		return 0, ErrNoScore
	}
	return pts, nil
}

func (g *Game) setAside(keep []int, pts int) {
	for _, i := range keep {
		g.Dice[i].Held = true
	}
	g.TurnPts += pts
}

func (g *Game) freeCount() int {
	n := 0
	for _, d := range g.Dice {
		if !d.Held {
			n++
		}
	}
	return n
}

func (g *Game) resetTurn() {
	g.Dice = [NumDice]Die{}
	g.Rolls = 0
	g.TurnPts = 0
	g.Rolled = false
}

func (g *Game) advance() {
	g.resetTurn()
	g.turn = (g.turn + 1) % len(g.Order)
}
