package main

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

// recorder is a fake page: it records what the room pushed to it.
type recorder struct {
	mu    sync.Mutex
	swaps map[string]int
}

func (r *recorder) Swap(target, _, _ string, _ any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.swaps == nil {
		r.swaps = map[string]int{}
	}
	r.swaps[target]++
	return nil
}

func (r *recorder) count(target string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.swaps[target]
}

// cycle is a Roller safe for concurrent use that always offers a 1, so turns
// keep going.
type cycle struct {
	mu sync.Mutex
	n  int
}

func (c *cycle) roll(n int) ([]int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]int, n)
	for i := range out {
		c.n++
		out[i] = c.n%6 + 1
	}
	out[0] = 1
	return out, nil
}

func TestRoomStateIsRaceFree(t *testing.T) {
	tm := &timers{}
	_, arena := testServer(t, Options{Roller: (&cycle{}).roll, AfterFunc: tm.after})
	players := []Identity{{"p1", "One"}, {"p2", "Two"}, {"p3", "Three"}, {"p4", "Four"}}
	room, err := arena.Create(players[0], 10000)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range players[1:] {
		if err := room.Sit(p); err != nil {
			t.Fatal(err)
		}
	}
	pages := make([]*recorder, len(players))
	for i, p := range players {
		pages[i] = &recorder{}
		if err := room.Attach(pages[i], p); err != nil {
			t.Fatal(err)
		}
	}
	if err := room.Act(players[0], Action{Action: "start"}); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 64)
	// Every player hammers the room at once. Only the current player's moves
	// succeed; the rest must come back as rule errors, never corrupt state.
	for _, p := range players {
		wg.Go(func() {
			for i := range 200 {
				var err error
				switch i % 4 {
				case 0:
					err = room.Act(p, Action{Action: "roll"})
				case 1:
					err = room.Act(p, Action{Action: "roll", Keep: []int{0}})
				case 2:
					_, err = room.Preview(p.ID, []int{0})
				case 3:
					err = room.Act(p, Action{Action: "bank", Keep: []int{0}})
				}
				var rule RuleError
				if err != nil && !errors.As(err, &rule) {
					errs <- err
					return
				}
			}
		})
	}
	// Spectators come and go, and readers take snapshots, meanwhile.
	for w := range 4 {
		wg.Go(func() {
			who := Identity{ID: fmt.Sprintf("watcher-%d", w)}
			for range 100 {
				page := &recorder{}
				if err := room.Attach(page, who); err != nil {
					errs <- err
					return
				}
				if room.Board().Code != room.Code() || room.Table(who).Seated || len(arena.Listing()) != 1 {
					errs <- errors.New("inconsistent snapshot")
					return
				}
				if err := room.Detach(page, who); err != nil {
					errs <- err
					return
				}
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	b := room.Board()
	if len(b.Seats) != 4 || b.Spectators != 0 {
		t.Fatalf("board after the storm: %+v", b)
	}
	for i, page := range pages {
		if page.count("#table") < 2 {
			t.Fatalf("player %d's page got %d table pushes", i+1, page.count("#table"))
		}
	}
}

func TestRoomSeatsAndCleanup(t *testing.T) {
	tm := &timers{}
	_, arena := testServer(t, Options{AfterFunc: tm.after})
	host := Identity{"h", "Host"}
	room, err := arena.Create(host, 2000)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := arena.Create(Identity{ID: "anon"}, 2000); err == nil {
		t.Fatal("a player without a name opened a table")
	}

	for i := 1; i < MaxSeats; i++ {
		if err := room.Sit(Identity{ID: fmt.Sprint(i), Name: fmt.Sprint("P", i)}); err != nil {
			t.Fatal(err)
		}
	}
	var rule RuleError
	if err := room.Sit(Identity{"late", "Late"}); !errors.As(err, &rule) {
		t.Fatalf("fifth seat: %v", err)
	}
	if got := arena.Listing(); len(got) != 1 || got[0].Seated != MaxSeats || got[0].CanSit {
		t.Fatalf("listing: %+v", got)
	}

	// Nobody ever connects: each seat's grace timer releases it, and the
	// room closes when the last one goes.
	tm.mu.Lock()
	pending := append([]func(){}, tm.funcs...)
	tm.mu.Unlock()
	for _, expire := range pending {
		expire()
	}
	if _, ok := arena.Room(room.Code()); ok {
		t.Fatal("room with no seats and no pages is still open")
	}
	if len(arena.Listing()) != 0 {
		t.Fatal("closed room is still listed")
	}
	if err := room.Sit(host); !errors.Is(err, errRoomGone) {
		t.Fatalf("sit in a closed room: %v", err)
	}
}

func TestStaleGraceTimerDoesNothing(t *testing.T) {
	tm := &timers{}
	_, arena := testServer(t, Options{AfterFunc: tm.after})
	host := Identity{"h", "Host"}
	room, err := arena.Create(host, 2000)
	if err != nil {
		t.Fatal(err)
	}
	page := &recorder{}
	if err := room.Attach(page, host); err != nil {
		t.Fatal(err)
	}
	// The timer from before the page connected fires late: the seat stays.
	tm.fireLast(t)
	if b := room.Board(); len(b.Seats) != 1 || b.Seats[0].Away {
		t.Fatalf("stale timer released a connected seat: %+v", b.Seats)
	}
}
