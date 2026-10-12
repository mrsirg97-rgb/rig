package room

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/evt/broadcast"
)

func newRoom(t *testing.T, home string, places int) Member {
	t.Helper()
	m, err := New(home, func(int64) int { return places }, nil, nil)
	if err != nil {
		t.Fatalf("join: %v", err)
	}
	t.Cleanup(m.Close)
	return m
}

func TestAMemberAloneClaimsAndHolds(t *testing.T) {
	home := t.TempDir()
	m := newRoom(t, home, 1)
	key := KeyOf("local")
	done := make(chan bool, 1)
	m.Claim(key, PriorityTurn, KeyOf("session-1"), func(mint broadcast.Mint, admitted bool) {
		done <- admitted
	})
	select {
	case admitted := <-done:
		if !admitted {
			t.Fatal("the alone room's claim must be admitted")
		}
		if len(m.Holders(key)) != 1 {
			t.Fatalf("holders = %d, want 1", len(m.Holders(key)))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the claim never completed")
	}
}

func TestTwoMembersAgreeOnWhoHolds(t *testing.T) {
	home := t.TempDir()
	first := newRoom(t, home, 2)
	second := newRoom(t, home, 2)
	waitFor(t, "the members to see each other", func() bool {
		return len(first.Live()) == 1 && len(second.Live()) == 1
	})
	key := KeyOf("local")
	var admitted [2]bool
	var seen [2]bool
	seenC := make(chan int, 2)
	for i, m := range []Member{first, second} {
		go func(i int, m Member) {
			m.Claim(key, PriorityFleet, KeyOf("session-a"), func(mint broadcast.Mint, ok bool) {
				admitted[i] = ok
				seen[i] = true
				seenC <- i
			})
		}(i, m)
	}
	count := 0
	deadline := time.After(10 * time.Second)
	for count < 2 {
		select {
		case <-seenC:
			count++
		case <-deadline:
			t.Fatalf("the claims never settled: seen %v admitted %v", seen, admitted)
		}
	}
	if !admitted[0] || !admitted[1] {
		t.Fatalf("both claims must be admitted: %v", admitted)
	}
	for _, m := range []Member{first, second} {
		if len(m.Holders(key)) != 2 {
			t.Fatalf("holders = %d, want 2", len(m.Holders(key)))
		}
	}
}

func TestPriorityOutranksArrival(t *testing.T) {
	home := t.TempDir()
	first := newRoom(t, home, 1)
	second := newRoom(t, home, 1)
	key := KeyOf("local")
	low := make(chan broadcast.Mint, 1)
	high := make(chan bool, 1)
	first.Claim(key, PriorityFleet, KeyOf("session-a"), func(mint broadcast.Mint, ok bool) { low <- mint })
	mint := <-low
	second.Claim(key, PriorityTurn, KeyOf("session-b"), func(mint broadcast.Mint, ok bool) { high <- ok })
	first.Release(mint, PriorityFleet)
	select {
	case ok := <-high:
		if !ok {
			t.Fatal("the higher claim must be admitted after the release")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the higher claim never completed")
	}
}

func TestAMemberThatDiesReleasesForIt(t *testing.T) {
	home := t.TempDir()
	holder := newRoom(t, home, 1)
	watcher := newRoom(t, home, 1)
	key := KeyOf("local")
	holder.Claim(key, PriorityFleet, KeyOf("session-a"), func(broadcast.Mint, bool) {})
	waitFor(t, "the hold to settle", func() bool { return len(watcher.Holders(key)) == 1 })
	holder.Close()
	waitFor(t, "the release from the dead holder", func() bool { return len(watcher.Holders(key)) == 0 })
}

func TestTheSocketNameRoundTrips(t *testing.T) {
	id := broadcast.MemberOf([]byte("the boot id 1234 99"))
	if id <= 0 {
		t.Fatalf("id = %d", id)
	}
	name := broadcast.EncodeID(id)
	if name == "" {
		t.Fatalf("the socket name's encoding produced nothing for %d", id)
	}
	if back, err := broadcast.DecodeID(name); err != nil || back != id {
		t.Fatalf("the socket name's encoding must round-trip: %d -> %s -> %d (%v)", id, name, back, err)
	}
	if len(filepath.Join("room", name+"-12345.sock")) > 96 {
		t.Fatalf("the socket name is too long: %s", name)
	}
}

func waitFor(t *testing.T, what string, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("%s never happened", what)
}
