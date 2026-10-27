package main

import (
	"strings"
	"testing"
)

// seatedPair opens a table for Alice and seats Bob, then connects both pages.
func seatedPair(t *testing.T, opts Options) (alice, bob *player, aliceWS, bobWS *socket, code string) {
	t.Helper()
	srv, _ := testServer(t, opts)
	ctx := timeout(t)
	alice = newPlayer(t, srv, "Alice")
	bob = newPlayer(t, srv, "Bob")
	code = alice.createRoom(2000)
	bob.join(code)

	aliceWS = alice.connect(ctx, "/ws/r/"+code)
	aliceWS.until("#table", `value="start"`) // two seats: Alice may start
	bobWS = bob.connect(ctx, "/ws/r/"+code)
	bobWS.until("#table", `value="start"`)
	return alice, bob, aliceWS, bobWS, code
}

func TestTwoPlayersTakeTurns(t *testing.T) {
	roll := newScript(
		[]int{1, 2, 3, 4, 6, 6}, // Alice: one scoring die
		[]int{2, 3, 4, 6, 6, 2}, // Bob: nothing scores
	)
	_, _, aliceWS, bobWS, _ := seatedPair(t, Options{Roller: roll.roll})

	aliceWS.send(map[string]any{"action": "start"})
	aliceWS.until("#table", "Your turn")
	bobWS.until("#table", "Alice is rolling")

	// Out of turn: refused, and only Bob hears about it.
	bobWS.send(map[string]any{"action": "roll"})
	bobWS.until("#flash", "It is not your turn.")

	aliceWS.send(map[string]any{"action": "roll"})
	_, seen := aliceWS.until("#board", "Alice rolled 1 2 3 4 6 6.")
	for _, f := range seen {
		if strings.Contains(f.Content, "not your turn") {
			t.Fatalf("Alice received Bob's error: %v", f)
		}
	}
	// The roll is broadcast: Bob's board shows it too.
	bobWS.until("#board", "Alice rolled 1 2 3 4 6 6.")

	// A preview answers the sender only and changes nothing.
	aliceWS.send(map[string]any{"action": "preview", "keep": []int{0}})
	aliceWS.until("#preview", `<strong class="num">100</strong>`)
	aliceWS.send(map[string]any{"action": "preview", "keep": []string{"1"}})
	aliceWS.until("#preview", "Every die you set aside has to score.")

	aliceWS.send(map[string]any{"action": "bank", "keep": []string{"0"}})
	aliceWS.until("#board", "Alice banked 100 and has 100.")
	bobWS.until("#table", "Your turn")

	bobWS.send(map[string]any{"action": "roll"})
	bobWS.until("#board", "Bob rolled 2 3 4 6 6 2: no score. Bust.")
	aliceWS.until("#table", "Your turn")

	// A move that works clears Bob's old error: the push keeps an empty
	// content field, which the ws extension swaps in.
	clear := bobWS.next("#flash")
	if content, ok := clear["content"]; !ok || content != "" {
		t.Fatalf("flash clear = %v, want an empty content field", clear)
	}
}

func TestDisconnectedPlayerKeepsSeatAndRejoins(t *testing.T) {
	roll := newScript([]int{5, 5, 5, 2, 3, 4})
	tm := &timers{}
	_, bob, aliceWS, bobWS, code := seatedPair(t, Options{Roller: roll.roll, AfterFunc: tm.after})
	aliceWS.send(map[string]any{"action": "start"})
	bobWS.until("#table", "Alice is rolling")

	bobWS.close()
	if f, _ := aliceWS.until("#board", "Bob lost connection."); !strings.Contains(f.Content, "reconnecting") {
		t.Fatal("roster does not show Bob as reconnecting")
	}

	// Bob comes back within the grace window: same seat, same game.
	again := bob.connect(timeout(t), "/ws/r/"+code)
	again.until("#table", "Alice is rolling")
	aliceWS.until("#board", "Bob is back.")

	aliceWS.send(map[string]any{"action": "roll"})
	again.until("#board", "Alice rolled 5 5 5 2 3 4.")
}

func TestSeatIsReleasedWhenGraceRunsOut(t *testing.T) {
	tm := &timers{}
	_, _, aliceWS, bobWS, _ := seatedPair(t, Options{Roller: newScript().roll, AfterFunc: tm.after})
	aliceWS.send(map[string]any{"action": "start"})
	bobWS.until("#table", "Alice is rolling")

	bobWS.close()
	aliceWS.until("#board", "Bob lost connection.")
	tm.fireLast(t)

	aliceWS.until("#board", "Bob did not come back and left the table.")
	aliceWS.until("#table", "You win.")
}

func TestMalformedMessagesGetAnErrorFragment(t *testing.T) {
	_, _, aliceWS, bobWS, _ := seatedPair(t, Options{})

	cases := []struct {
		name string
		send any
		want string
	}{
		{"not an object", "roll please", "not a JSON object"},
		{"unknown action", map[string]any{"action": "fly"}, "action must be one of"},
		{"missing action", map[string]any{"keep": []int{1}}, "action is required"},
		{"die is not a number", map[string]any{"action": "roll", "keep": []string{"x"}}, "keep"},
		{"die off the table", map[string]any{"action": "roll", "keep": []int{9}}, "must be a die from 0 to 5"},
		{"roll before the game", map[string]any{"action": "roll"}, "No game is running."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			aliceWS.t = t
			if s, ok := tc.send.(string); ok {
				if err := aliceWS.conn.Write(aliceWS.ctx, 1, []byte(s)); err != nil {
					t.Fatal(err)
				}
			} else {
				aliceWS.send(tc.send)
			}
			aliceWS.until("#flash", tc.want)
		})
	}

	// None of it reached Bob: his next push is the start of the game.
	aliceWS.send(map[string]any{"action": "start"})
	_, seen := bobWS.until("#table", "Alice is rolling")
	for _, f := range seen {
		if f.Target == "#flash" {
			t.Fatalf("Bob received an error meant for Alice: %v", f)
		}
	}
}

func TestSpectatorWatchesButCannotPlay(t *testing.T) {
	roll := newScript([]int{1, 1, 1, 1, 1, 1})
	srv, _ := testServer(t, Options{Roller: roll.roll})
	ctx := timeout(t)
	alice := newPlayer(t, srv, "Alice")
	bob := newPlayer(t, srv, "Bob")
	carol := newPlayer(t, srv, "Carol")
	code := alice.createRoom(2000)
	bob.join(code)

	aliceWS := alice.connect(ctx, "/ws/r/"+code)
	aliceWS.until("#table", `value="start"`)
	aliceWS.send(map[string]any{"action": "start"})
	aliceWS.until("#table", "Your turn")

	// Carol joins mid-game: no seat, so she watches.
	carol.join(code)
	carolWS := carol.connect(ctx, "/ws/r/"+code)
	carolWS.until("#table", "Alice is rolling")
	aliceWS.until("#board", "1 watching")

	carolWS.send(map[string]any{"action": "sit"})
	carolWS.until("#flash", "A game is in progress.")
	carolWS.send(map[string]any{"action": "roll"})
	carolWS.until("#flash", "It is not your turn.")

	aliceWS.send(map[string]any{"action": "roll"})
	carolWS.until("#board", "Alice rolled 1 1 1 1 1 1.")
}

func TestLobbyListsTablesLive(t *testing.T) {
	srv, _ := testServer(t, Options{})
	ctx := timeout(t)
	dana := newPlayer(t, srv, "Dana")
	lobby := dana.connect(ctx, "/ws/lobby")
	lobby.until("#open-rooms", "No open tables yet.")

	eve := newPlayer(t, srv, "Eve")
	code := eve.createRoom(5000)
	row, _ := lobby.until("#open-rooms", code)
	if !strings.Contains(row.Content, "Eve") || !strings.Contains(row.Content, "to 5,000") || !strings.Contains(row.Content, "waiting") {
		t.Fatalf("lobby row: %s", row.Content)
	}

	// The row reads the room's named status type through eq.
	dana.join(code)
	table := eve.connect(ctx, "/ws/r/"+code)
	table.until("#table", `value="start"`)
	table.send(map[string]any{"action": "start"})
	lobby.until("#open-rooms", "in play")
}

func TestEmptyRoomCloses(t *testing.T) {
	tm := &timers{}
	srv, arena := testServer(t, Options{AfterFunc: tm.after})
	ctx := timeout(t)
	fay := newPlayer(t, srv, "Fay")
	code := fay.createRoom(2000)
	page := fay.connect(ctx, "/ws/r/"+code)
	page.until("#table", "Waiting for players")

	page.send(map[string]any{"action": "leave"})
	page.until("#board", "Fay left the table.")
	page.close()

	lobby := fay.connect(ctx, "/ws/lobby")
	lobby.until("#open-rooms", "No open tables yet.")
	if _, ok := arena.Room(code); ok {
		t.Fatal("empty room is still open")
	}
	late := fay.connect(ctx, "/ws/r/"+code)
	late.until("#table", "This table has closed.")
}
