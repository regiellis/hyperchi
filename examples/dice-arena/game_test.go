package main

import (
	"errors"
	"slices"
	"testing"
)

func TestScore(t *testing.T) {
	tests := []struct {
		name string
		dice []int
		want int
		ok   bool
	}{
		{"single one", []int{1}, 100, true},
		{"single five", []int{5}, 50, true},
		{"ones and fives", []int{1, 5, 1}, 250, true},
		{"three ones", []int{1, 1, 1}, 1000, true},
		{"three twos", []int{2, 2, 2}, 200, true},
		{"three sixes", []int{6, 6, 6}, 600, true},
		{"four fours double", []int{4, 4, 4, 4}, 800, true},
		{"five threes quadruple", []int{3, 3, 3, 3, 3}, 1200, true},
		{"six ones", []int{1, 1, 1, 1, 1, 1}, 8000, true},
		{"triple plus single", []int{2, 2, 2, 5}, 250, true},
		{"straight", []int{3, 1, 6, 2, 5, 4}, 1500, true},
		{"three pairs", []int{2, 2, 3, 3, 6, 6}, 750, true},
		{"four and a pair is not three pairs", []int{2, 2, 2, 2, 3, 3}, 0, false},
		{"lone two", []int{2}, 0, false},
		{"one with a stray", []int{1, 3}, 0, false},
		{"pair of fours", []int{4, 4}, 0, false},
		{"empty", nil, 0, false},
		{"not a die", []int{7}, 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := Score(tc.dice)
			if got != tc.want || ok != tc.ok {
				t.Fatalf("Score(%v) = %d, %v; want %d, %v", tc.dice, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestHasScore(t *testing.T) {
	tests := []struct {
		dice []int
		want bool
	}{
		{[]int{2, 3, 4, 6, 6, 2}, false},
		{[]int{2, 3, 4, 6}, false},
		{[]int{2, 3, 4, 6, 5}, true},
		{[]int{1}, true},
		{[]int{4, 4, 4, 2}, true},
		{[]int{2, 2, 3, 3, 4, 4}, true},
		{[]int{2, 2, 3, 3, 4}, false},
	}
	for _, tc := range tests {
		if got := HasScore(tc.dice); got != tc.want {
			t.Errorf("HasScore(%v) = %v, want %v", tc.dice, got, tc.want)
		}
	}
}

// play starts a game in the given order with scripted dice.
func play(order []string, target int, rolls ...[]int) *Game {
	return NewGame(order, target, newScript(rolls...).roll)
}

// onTable returns the dice not set aside, in seat order.
func onTable(g *Game) []int {
	var out []int
	for _, d := range g.Dice {
		if !d.Held {
			out = append(out, d.Value)
		}
	}
	return out
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestRollKeepAndBust(t *testing.T) {
	g := play([]string{"ann", "ben"}, 5000,
		[]int{1, 5, 2, 3, 4, 6}, // six dice
		[]int{1, 1, 1, 2},       // after setting aside the 1 and the 5
		[]int{6},                // after setting aside three 1s: a bust
	)

	if err := g.Roll("ben", nil); !errors.Is(err, ErrNotYourTurn) {
		t.Fatalf("out of turn: %v", err)
	}
	if err := g.Bank("ann", []int{0}); !errors.Is(err, ErrRollFirst) {
		t.Fatalf("bank before rolling: %v", err)
	}
	if err := g.Roll("ann", []int{0}); !errors.Is(err, ErrRollFirst) {
		t.Fatalf("keep before rolling: %v", err)
	}
	must(t, g.Roll("ann", nil))
	if g.Last.Kind != EventRolled || g.RollsLeft() != MaxRolls-1 || !slices.Equal(onTable(g), []int{1, 5, 2, 3, 4, 6}) {
		t.Fatalf("after first roll: %+v, table %v", g.Last, onTable(g))
	}

	refused := []struct {
		keep []int
		want error
	}{
		{nil, ErrPickDice},
		{[]int{2}, ErrNoScore},
		{[]int{0, 2}, ErrNoScore},
		{[]int{0, 0}, ErrBadDie},
		{[]int{9}, ErrBadDie},
	}
	for _, tc := range refused {
		if err := g.Roll("ann", tc.keep); !errors.Is(err, tc.want) {
			t.Fatalf("keep %v: %v, want %v", tc.keep, err, tc.want)
		}
	}

	if pts, err := g.Preview("ann", []int{0, 1}); err != nil || pts != 150 {
		t.Fatalf("preview = %d, %v", pts, err)
	}
	if g.TurnPts != 0 || g.Dice[0].Held {
		t.Fatal("preview changed the game")
	}

	must(t, g.Roll("ann", []int{0, 1}))
	if g.TurnPts != 150 || !g.Dice[0].Held || !g.Dice[1].Held || !slices.Equal(onTable(g), []int{1, 1, 1, 2}) {
		t.Fatalf("after setting aside 1 and 5: pts %d, dice %+v", g.TurnPts, g.Dice)
	}
	if _, err := g.Preview("ann", []int{0}); !errors.Is(err, ErrBadDie) {
		t.Fatalf("a die set aside earlier was choosable again: %v", err)
	}

	must(t, g.Roll("ann", []int{2, 3, 4}))
	if g.Last.Kind != EventBust || g.Last.Points != 1150 || !slices.Equal(g.Last.Values, []int{6}) {
		t.Fatalf("bust event: %+v", g.Last)
	}
	if g.Scores["ann"] != 0 || g.Current() != "ben" || g.Rolled || g.TurnPts != 0 || g.Rolls != 0 {
		t.Fatalf("after bust: scores %v current %s rolled %v", g.Scores, g.Current(), g.Rolled)
	}
}

func TestHotDiceRollsAllSixAgain(t *testing.T) {
	g := play([]string{"ann", "ben"}, 5000, []int{1, 5, 1, 5, 1, 5}, []int{2, 2, 2, 3, 4, 6})
	must(t, g.Roll("ann", nil))
	must(t, g.Roll("ann", []int{0, 1, 2, 3, 4, 5}))
	if g.TurnPts != 1500 {
		t.Fatalf("turn points %d, want 1500", g.TurnPts)
	}
	if !slices.Equal(onTable(g), []int{2, 2, 2, 3, 4, 6}) {
		t.Fatalf("hot dice did not bring all six back: %+v", g.Dice)
	}
}

func TestLastRollMustBank(t *testing.T) {
	g := play([]string{"ann", "ben"}, 5000,
		[]int{1, 2, 3, 4, 6, 6},
		[]int{1, 2, 3, 4, 6},
		[]int{1, 2, 3, 4},
		[]int{1, 2, 3},
	)
	must(t, g.Roll("ann", nil))
	for i := 0; i < MaxRolls-1; i++ {
		must(t, g.Roll("ann", []int{i}))
	}
	if g.RollsLeft() != 0 {
		t.Fatalf("rolls left %d", g.RollsLeft())
	}
	if err := g.Roll("ann", []int{3}); !errors.Is(err, ErrNoRollsLeft) {
		t.Fatalf("fifth roll: %v", err)
	}
	must(t, g.Bank("ann", []int{3}))
	if g.Scores["ann"] != 400 || g.Current() != "ben" || g.Last.Kind != EventBanked || g.Last.Total != 400 {
		t.Fatalf("after bank: %v, %+v", g.Scores, g.Last)
	}
}

func TestReachingTheTargetWins(t *testing.T) {
	g := play([]string{"ann", "ben"}, 1000, []int{1, 1, 1, 2, 3, 4})
	must(t, g.Roll("ann", nil))
	must(t, g.Bank("ann", []int{0, 1, 2}))
	if !g.Over || g.Winner != "ann" || g.Last.Kind != EventWon || g.Current() != "" {
		t.Fatalf("not won: over %v winner %q last %+v", g.Over, g.Winner, g.Last)
	}
	if err := g.Roll("ben", nil); !errors.Is(err, ErrGameOver) {
		t.Fatalf("roll after the end: %v", err)
	}
}

func TestRemovePlayers(t *testing.T) {
	g := play([]string{"ann", "ben", "cy", "dee"}, 5000, []int{1, 2, 3, 4, 6, 6}, []int{5, 2, 3, 4, 6, 6})
	must(t, g.Roll("ann", nil))
	must(t, g.Bank("ann", []int{0}))
	must(t, g.Roll("ben", nil)) // ben is mid-turn

	g.Remove("nobody")
	g.Remove("ann") // before the current player
	if g.Current() != "ben" || !g.Rolled {
		t.Fatalf("removing an earlier seat moved the turn: %s", g.Current())
	}
	g.Remove("ben") // the current player, mid-turn
	if g.Current() != "cy" || g.Rolled || g.TurnPts != 0 || g.Over {
		t.Fatalf("turn did not pass cleanly: %q rolled %v over %v", g.Current(), g.Rolled, g.Over)
	}
	g.Remove("dee")
	if !g.Over || g.Winner != "cy" || g.Last.Kind != EventForfeit {
		t.Fatalf("last player standing did not win: %+v", g.Last)
	}
}

func TestRemoveLastOfTheOrderWrapsTheTurn(t *testing.T) {
	g := play([]string{"ann", "ben", "cy"}, 5000, []int{1, 2, 3, 4, 6, 6}, []int{1, 2, 3, 4, 6, 6})
	must(t, g.Roll("ann", nil))
	must(t, g.Bank("ann", []int{0}))
	must(t, g.Roll("ben", nil))
	must(t, g.Bank("ben", []int{0}))
	g.Remove("cy")
	if g.Current() != "ann" || g.Over {
		t.Fatalf("turn after removing the last seat: %q over %v", g.Current(), g.Over)
	}
}

func TestCryptoRoller(t *testing.T) {
	values, err := CryptoRoller(6000)
	if err != nil {
		t.Fatal(err)
	}
	var counts [7]int
	for _, v := range values {
		if v < 1 || v > 6 {
			t.Fatalf("rolled %d", v)
		}
		counts[v]++
	}
	for face := 1; face <= 6; face++ {
		if counts[face] < 800 { // expected 1000; 800 is far outside chance
			t.Fatalf("face %d came up %d times in 6000 rolls", face, counts[face])
		}
	}
}
