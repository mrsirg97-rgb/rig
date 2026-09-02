package evt_test

import (
	"context"
	"sort"
	"sync"
	"testing"

	"github.com/mrsirg97-rgb/rig/evt"
)

func TestMonotonicNeverRepeats(t *testing.T) {
	c := evt.Monotonic()
	const n = 100000
	prev := c.Step()
	for i := 1; i < n; i++ {
		id := c.Step()
		if id <= prev {
			t.Fatalf("step %d: id %d not above previous %d", i, id, prev)
		}
		prev = id
	}
}

func TestMonotonicUniqueAcrossProducers(t *testing.T) {
	c := evt.Monotonic()
	const producers, per = 8, 5000
	ids := make([][]uint64, producers)
	var wg sync.WaitGroup
	for p := 0; p < producers; p++ {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			ids[p] = make([]uint64, per)
			for i := 0; i < per; i++ {
				ids[p][i] = c.Step()
			}
		}(p)
	}
	wg.Wait()
	all := make([]uint64, 0, producers*per)
	for _, s := range ids {
		all = append(all, s...)
	}
	sort.Slice(all, func(i, j int) bool { return all[i] < all[j] })
	for i := 1; i < len(all); i++ {
		if all[i] == all[i-1] {
			t.Fatalf("duplicate id %d minted under concurrent steps", all[i])
		}
	}
}

func TestMonotonicPushesAreNeverDropped(t *testing.T) {
	e := evt.NewEngine(evt.WithClock(evt.Monotonic()))
	const n = 10000
	for i := 0; i < n; i++ {
		e.Add(evt.Func(func(context.Context) {}), 0)
	}
	if got := len(e.Pending()); got != n {
		t.Fatalf("pending %d, want %d: same-nanosecond ids collapsed pushes", got, n)
	}
}
