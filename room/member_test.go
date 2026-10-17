package room

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/evt"
	"github.com/mrsirg97-rgb/evt/broadcast"
)

func newRoom(t *testing.T, home string, places int) Member {
	t.Helper()
	m, err := New(home, JobStatusSince, func(int64) int { return places }, nil, nil)
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
	waitFor(t, "both members to agree on the two holders", func() bool {
		return len(first.Holders(key)) == 2 && len(second.Holders(key)) == 2
	})
}

func TestTwoMembersStartedTogetherEndInOneRoom(t *testing.T) {
	home := t.TempDir()
	type outcome struct {
		m   Member
		err error
	}
	out := make(chan outcome, 2)
	for i := 0; i < 2; i++ {
		go func() {
			m, err := New(home, JobStatusSince, func(int64) int { return 1 }, nil, nil)
			out <- outcome{m, err}
		}()
	}
	var joined [2]Member
	for i := 0; i < 2; i++ {
		select {
		case o := <-out:
			if o.err != nil {
				t.Fatalf("join: %v", o.err)
			}
			m := o.m
			t.Cleanup(m.Close)
			joined[i] = m
		case <-time.After(10 * time.Second):
			t.Fatal("the two starts never returned")
		}
	}
	waitFor(t, "the two members started together to end in one room", func() bool {
		return len(joined[0].Live()) == 1 && len(joined[1].Live()) == 1
	})
	if joined[0].Live()[0] != joined[1].Id() || joined[1].Live()[0] != joined[0].Id() {
		t.Fatalf("each member must see the other: %v against %v", joined[0].Live(), joined[1].Live())
	}
}

func TestAPeerGoneAtTheDialIsSkipped(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "room")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	ghost := broadcast.MemberOf([]byte("a member that is gone at the dial"))
	name := fmt.Sprintf("%s-%d.sock", broadcast.EncodeID(ghost), os.Getpid())
	if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	m := newRoom(t, home, 1)
	if got := m.Live(); len(got) != 0 {
		t.Fatalf("the peer gone at the dial must be skipped, not the whole start: live %v", got)
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

func TestAKeyServesAsManyPlacesAsItsRowSays(t *testing.T) {
	home := t.TempDir()
	first := newRoom(t, home, 2)
	second := newRoom(t, home, 2)
	waitFor(t, "the members to see each other", func() bool {
		return len(first.Live()) == 1 && len(second.Live()) == 1
	})
	key := KeyOf("local")
	firstMint := make(chan broadcast.Mint, 1)
	secondMint := make(chan broadcast.Mint, 1)
	go func() {
		first.Claim(key, PriorityFleet, KeyOf("session-a"), func(mint broadcast.Mint, ok bool) { firstMint <- mint })
	}()
	go func() {
		second.Claim(key, PriorityFleet, KeyOf("session-a"), func(mint broadcast.Mint, ok bool) { secondMint <- mint })
	}()
	var held broadcast.Mint
	deadline := time.After(10 * time.Second)
	select {
	case held = <-firstMint:
	case <-deadline:
		t.Fatal("the first member's claim must take a place")
	}
	select {
	case <-secondMint:
	case <-deadline:
		t.Fatal("the second member's claim must take the other place")
	}
	waitFor(t, "both members to agree on the two holders", func() bool {
		return len(first.Holders(key)) == 2 && len(second.Holders(key)) == 2
	})
	waiting := make(chan bool, 1)
	first.Claim(key, PriorityFleet, KeyOf("session-b"), func(broadcast.Mint, bool) { waiting <- true })
	select {
	case <-waiting:
		t.Fatal("a claim beyond the places must wait for one")
	case <-time.After(500 * time.Millisecond):
	}
	if len(second.Waiting(key)) != 1 {
		t.Fatalf("the waiting claim must sit in the order every member agrees on: %d", len(second.Waiting(key)))
	}
	first.Release(held, PriorityFleet)
	select {
	case <-waiting:
	case <-time.After(10 * time.Second):
		t.Fatal("the waiting claim must enter when a place frees")
	}
}

func TestAHigherClaimEntersPastOneInFlight(t *testing.T) {
	home := t.TempDir()
	first := newRoom(t, home, 1)
	second := newRoom(t, home, 1)
	waitFor(t, "the members to see each other", func() bool {
		return len(first.Live()) == 1 && len(second.Live()) == 1
	})
	key := KeyOf("local")
	low := make(chan broadcast.Mint, 1)
	first.Claim(key, PriorityFleet, KeyOf("session-a"), func(mint broadcast.Mint, ok bool) { low <- mint })
	var held broadcast.Mint
	select {
	case held = <-low:
	case <-time.After(10 * time.Second):
		t.Fatal("the low claim must take the place")
	}
	entry := make(chan bool, 1)
	second.Claim(key, PriorityTurn, KeyOf("session-b"), func(mint broadcast.Mint, ok bool) { entry <- ok })
	select {
	case <-entry:
	case <-time.After(5 * time.Second):
		t.Fatal("the strictly higher claim must enter past the one in flight")
	}
	waitFor(t, "the entry to be a holder beyond the place on both members", func() bool {
		return len(first.Holders(key)) == 2 && len(second.Holders(key)) == 2
	})
	lower := make(chan bool, 1)
	first.Claim(key, PriorityOutside, KeyOf("session-c"), func(mint broadcast.Mint, ok bool) { lower <- ok })
	select {
	case <-lower:
		t.Fatal("a second claim at or below the holders must wait")
	case <-time.After(500 * time.Millisecond):
	}
	first.Release(held, PriorityFleet)
	select {
	case <-lower:
	case <-time.After(10 * time.Second):
		t.Fatal("the waiting claim must enter when the place frees")
	}
}

func TestATransientAcceptErrorKeepsTheMemberAdmitting(t *testing.T) {
	home := t.TempDir()
	socketReady := make(chan struct{})
	var socket string
	old := acceptFrom
	var calls int32
	acceptFrom = func(l net.Listener) (net.Conn, error) {
		<-socketReady
		if l.Addr().String() == socket && atomic.AddInt32(&calls, 1) == 1 {
			return nil, syscall.EMFILE
		}
		return old(l)
	}
	t.Cleanup(func() { acceptFrom = old })
	var mu sync.Mutex
	var said []evt.Notice
	m, err := New(home, JobStatusSince, func(int64) int { return 1 }, nil, func(n evt.Notice) {
		mu.Lock()
		said = append(said, n)
		mu.Unlock()
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	socket = filepath.Join(home, "room", fmt.Sprintf("%s-%d.sock", broadcast.EncodeID(m.Id()), os.Getpid()))
	close(socketReady)
	peer := newRoom(t, home, 1)
	waitFor(t, "the peer to be admitted past the transient accept error", func() bool {
		for _, id := range m.Live() {
			if id == peer.Id() {
				return true
			}
		}
		return false
	})
	waitFor(t, "the transient accept error to be said loud", func() bool {
		mu.Lock()
		defer mu.Unlock()
		for _, n := range said {
			if n.Level == evt.LevelError && strings.Contains(n.Text, "accept") {
				return true
			}
		}
		return false
	})
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

func holdCandidate(t *testing.T, home, seed string, i int) {
	t.Helper()
	id := broadcast.MemberOf([]byte(seed + " " + strconv.Itoa(i)))
	path := filepath.Join(home, "room", fmt.Sprintf("%s-%d.sock", broadcast.EncodeID(id), os.Getpid()))
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
}

func TestAJoinWalksPastItsOwnTakenIds(t *testing.T) {
	seed, err := identity()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "room"), 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < joinAttempts-1; i++ {
		holdCandidate(t, home, seed, i)
	}
	m, err := New(home, JobStatusSince, func(int64) int { return 1 }, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	want := broadcast.MemberOf([]byte(seed + " " + strconv.Itoa(joinAttempts-1)))
	if m.Id() != want {
		t.Fatalf("the join walks to the first free id: %d, want %d", m.Id(), want)
	}
}

func TestAJoinPastTheBoundRefusesLoud(t *testing.T) {
	seed, err := identity()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "room"), 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < joinAttempts; i++ {
		holdCandidate(t, home, seed, i)
	}
	if _, err := New(home, JobStatusSince, func(int64) int { return 1 }, nil, nil); err == nil {
		t.Fatal("a join past the bound must refuse loud")
	} else if !strings.Contains(err.Error(), strconv.Itoa(joinAttempts)) {
		t.Fatalf("the refusal names the bound: %v", err)
	}
}

func TestAStatReadFailureFailsTheIdentity(t *testing.T) {
	old := procStat
	procStat = filepath.Join(t.TempDir(), "absent")
	t.Cleanup(func() { procStat = old })
	if _, err := startTime(); err == nil {
		t.Fatal("an unreadable stat must fail the identity")
	}
}
