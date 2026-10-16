package room

import (
	"net"
	"strings"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/evt/broadcast"
)

const (
	doorTestPeriod   = 50 * time.Millisecond
	doorTestWindow   = 4
	doorTestPatience = 20 * time.Second
)

func doorAwait(t *testing.T, claim string, met func() bool) {
	t.Helper()
	deadline := time.Now().Add(doorTestPatience)
	for time.Now().Before(deadline) {
		if met() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal(claim)
}

func doorPorts(t *testing.T, n int) []string {
	t.Helper()
	addrs := make([]string, n)
	for i := range addrs {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		addrs[i] = ln.Addr().String()
		ln.Close()
	}
	return addrs
}

func doorMember(t *testing.T, home string) Member {
	t.Helper()
	m, err := New(home, JobStatusSince, func(int64) int { return 1 }, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func doorConfig(machine, addr string, peers []string) broadcast.MeshConfig {
	return broadcast.MeshConfig{
		Machine: machine,
		Addr:    addr,
		Peers:   peers,
		Clock:   broadcast.NewTickClock(doorTestPeriod, doorTestWindow, PriorityFleet),
		Window:  doorTestWindow,
	}
}

func TestTheDoorAnswersTheMintsItSawUnderKeysItDoesNotCarry(t *testing.T) {
	ma := doorMember(t, t.TempDir())
	mb := doorMember(t, t.TempDir())
	defer ma.Close()
	defer mb.Close()
	key := KeyOf("alias-on-one-side")
	addrs := doorPorts(t, 2)
	da, err := NewDoor(ma, doorConfig("a", addrs[0], []string{addrs[1]}), []int64{key}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer da.Close()
	db, err := NewDoor(mb, doorConfig("b", addrs[1], []string{addrs[0]}), nil, []int64{KeyOf("home-row")})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	claimed := make(chan broadcast.Mint, 1)
	ma.Claim(key, PriorityTurn, 0, func(m broadcast.Mint, ok bool) { claimed <- m })
	mint := <-claimed

	doorAwait(t, "the door must answer the mints it saw under keys it does not carry", func() bool {
		return db.Foreign(mint)
	})
	doorAwait(t, "the answer must leave past the horizon's window", func() bool {
		return !db.Foreign(mint)
	})
	ma.Release(mint, PriorityTurn)
}

func TestARepostReleasedBeforeItsAdmissionStillEnds(t *testing.T) {
	home := t.TempDir()
	ma := doorMember(t, home)
	defer ma.Close()
	mb, err := New(home, JobStatusSince, func(int64) int { return 1 }, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer mb.Close()
	mc := doorMember(t, t.TempDir())
	defer mc.Close()
	key := KeyOf("the-model")
	addrs := doorPorts(t, 2)
	db, err := NewDoor(ma, doorConfig("a", addrs[0], []string{addrs[1]}), nil, []int64{key})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	dc, err := NewDoor(mc, doorConfig("c", addrs[1], []string{addrs[0]}), []int64{key}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer dc.Close()

	held := make(chan broadcast.Mint, 1)
	mb.Claim(key, PriorityTurn, 0, func(m broadcast.Mint, ok bool) { held <- m })
	his := <-held

	claimed := make(chan broadcast.Mint, 1)
	mc.Claim(key, PriorityTurn, 0, func(m broadcast.Mint, ok bool) { claimed <- m })
	mint := <-claimed

	doorAwait(t, "the home door to carry the repost unadmitted behind the seat holder", func() bool {
		h, machine, _ := db.Holding(key, mint)
		return !h && machine != ""
	})
	db.Release(mint)
	mb.Release(his, PriorityTurn)
	doorAwait(t, "the repost's later admission to release the home room's claim", func() bool {
		return len(ma.Holders(key)) == 0
	})
}

func TestTheDoorMirrorsAHoldAcrossMachines(t *testing.T) {
	ma := doorMember(t, t.TempDir())
	mb := doorMember(t, t.TempDir())
	defer ma.Close()
	defer mb.Close()
	key := KeyOf("remote-model")
	addrs := doorPorts(t, 2)
	da, err := NewDoor(ma, doorConfig("a", addrs[0], []string{addrs[1]}), []int64{key}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer da.Close()
	db, err := NewDoor(mb, doorConfig("b", addrs[1], []string{addrs[0]}), nil, []int64{key})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	claimed := make(chan broadcast.Mint, 1)
	ma.Claim(key, PriorityTurn, 0, func(m broadcast.Mint, ok bool) { claimed <- m })
	mint := <-claimed

	doorAwait(t, "the hold must mirror across the mesh and the home door must repost it at the remote claim's priority", func() bool {
		for _, op := range mb.Holders(key) {
			if op.Priority() == PriorityTurn {
				return true
			}
		}
		return false
	})

	doorAwait(t, "the home door's machines must name the peer once its act crosses", func() bool {
		for _, machine := range db.Machines() {
			if machine == "a" {
				return true
			}
		}
		return false
	})

	ma.Release(mint, PriorityTurn)
	doorAwait(t, "the hold's end must cross the mesh and the home door's repost must end", func() bool {
		return len(mb.Holders(key)) == 0
	})
}

func TestTheSilentDoorSHoldsAreReleased(t *testing.T) {
	ma := doorMember(t, t.TempDir())
	mb := doorMember(t, t.TempDir())
	defer ma.Close()
	defer mb.Close()
	key := KeyOf("remote-model")
	addrs := doorPorts(t, 2)
	da, err := NewDoor(ma, doorConfig("a", addrs[0], []string{addrs[1]}), []int64{key}, nil)
	if err != nil {
		t.Fatal(err)
	}
	db, err := NewDoor(mb, doorConfig("b", addrs[1], []string{addrs[0]}), nil, []int64{key})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	claimed := make(chan broadcast.Mint, 1)
	ma.Claim(key, PriorityTurn, 0, func(m broadcast.Mint, ok bool) { claimed <- m })
	<-claimed

	doorAwait(t, "the hold must mirror before the silence", func() bool {
		return len(mb.Holders(key)) == 1
	})

	da.Close()
	doorAwait(t, "a door that misses its rounds is gone and its holds are released as acts", func() bool {
		return len(mb.Holders(key)) == 0
	})
}

func TestALoneDoorSClockRuns(t *testing.T) {
	m := doorMember(t, t.TempDir())
	defer m.Close()
	d, err := NewDoor(m, doorConfig("a", doorPorts(t, 1)[0], nil), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	doorAwait(t, "the machine's clock must run from the door's start (one tick a period, nothing between ticks)", func() bool {
		return d.Horizon() > 0
	})
}

func TestTheFoldSHoldKeepsAClaimUnrefused(t *testing.T) {
	ma := doorMember(t, t.TempDir())
	mb := doorMember(t, t.TempDir())
	defer ma.Close()
	defer mb.Close()
	key := KeyOf("remote-model")
	addrs := doorPorts(t, 2)
	da, err := NewDoor(ma, doorConfig("a", addrs[0], []string{addrs[1]}), []int64{key}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer da.Close()
	db, err := NewDoor(mb, doorConfig("b", addrs[1], []string{addrs[0]}), nil, []int64{key})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	claimed := make(chan broadcast.Mint, 1)
	ma.Claim(key, PriorityTurn, 0, func(m broadcast.Mint, ok bool) { claimed <- m })
	mint := <-claimed

	doorAwait(t, "the hold must mirror before the refusal is asked", func() bool {
		return len(mb.Holders(key)) == 1
	})
	since := db.Horizon()
	if db.Refused(key, mint, since) {
		t.Fatal("the fold holds the claim: the door must not call it refused")
	}
}

func TestTheDoorProvesAClaimRefusedPastTheHorizon(t *testing.T) {
	ma := doorMember(t, t.TempDir())
	mb := doorMember(t, t.TempDir())
	defer ma.Close()
	defer mb.Close()
	key := KeyOf("remote-model")
	addrs := doorPorts(t, 2)
	da, err := NewDoor(ma, doorConfig("a", addrs[0], []string{addrs[1]}), []int64{key}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer da.Close()
	db, err := NewDoor(mb, broadcast.MeshConfig{
		Machine: "b",
		Addr:    addrs[1],
		Peers:   []string{addrs[0]},
		Clock:   broadcast.NewTickClock(doorTestPeriod/2, doorTestWindow, PriorityFleet),
		Window:  doorTestWindow,
	}, nil, []int64{key})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	claimed := make(chan broadcast.Mint, 1)
	ma.Claim(key, PriorityTurn, 0, func(m broadcast.Mint, ok bool) { claimed <- m })
	mint := <-claimed

	doorAwait(t, "the home door's clock to run", func() bool {
		return db.Horizon() > 0
	})
	since := db.Horizon()
	doorAwait(t, "the fold's absence past the horizon proves the claim refused (the machine's clock is wrong)", func() bool {
		return db.Refused(key, mint, since)
	})
}

func TestTheRoomSWireCarriesNoMeshSignal(t *testing.T) {
	enc := Codec()
	_, err := enc.Encode(broadcast.NewMessage(1, true, broadcast.Act{Machine: "a"}))
	if err == nil || !strings.Contains(err.Error(), "does not cross a transport") {
		t.Fatalf("the mesh's act must not cross the unix room's wire (the instances never join the mesh, so the wire cannot change): %v", err)
	}
	_, err = enc.Encode(broadcast.NewMessage(1, true, broadcast.Tick{}))
	if err == nil || !strings.Contains(err.Error(), "does not cross a transport") {
		t.Fatalf("the mesh's tick must not cross the unix room's wire: %v", err)
	}
}

func TestAJoinerNeverLearnsHoldsOlderThanTheWindow(t *testing.T) {
	ma := doorMember(t, t.TempDir())
	mb := doorMember(t, t.TempDir())
	defer ma.Close()
	defer mb.Close()
	key := KeyOf("remote-model")
	addrs := doorPorts(t, 2)
	da, err := NewDoor(ma, doorConfig("a", addrs[0], []string{addrs[1]}), []int64{key}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer da.Close()
	db, err := NewDoor(mb, doorConfig("b", addrs[1], []string{addrs[0]}), nil, []int64{key})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	claimed := make(chan broadcast.Mint, 1)
	ma.Claim(key, PriorityTurn, 0, func(m broadcast.Mint, ok bool) { claimed <- m })
	mint := <-claimed

	doorAwait(t, "the act must not be acted on before the horizon (the repost waits it)", func() bool {
		return len(mb.Holders(key)) == 0
	})
	doorAwait(t, "the repost must land past the horizon", func() bool {
		return len(mb.Holders(key)) == 1
	})

	ma.Release(mint, PriorityTurn)
	doorAwait(t, "the hold's end must cross the mesh", func() bool {
		return len(mb.Holders(key)) == 0
	})
}

func TestPartitionHealingLeavesNoTwoHolders(t *testing.T) {
	ma := doorMember(t, t.TempDir())
	mb := doorMember(t, t.TempDir())
	defer ma.Close()
	defer mb.Close()
	key := KeyOf("remote-model")
	addrs := doorPorts(t, 2)
	da, err := NewDoor(ma, doorConfig("a", addrs[0], []string{addrs[1]}), []int64{key}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer da.Close()
	db, err := NewDoor(mb, doorConfig("b", addrs[1], []string{addrs[0]}), nil, []int64{key})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	claimed := make(chan broadcast.Mint, 1)
	ma.Claim(key, PriorityTurn, 0, func(m broadcast.Mint, ok bool) { claimed <- m })
	<-claimed

	doorAwait(t, "the mirror to hold the key", func() bool {
		return len(mb.Holders(key)) == 1
	})

	ma.Close()
	da.Close()
	doorAwait(t, "the silent door's holds to be released as acts and the partition to heal with one holder", func() bool {
		return len(mb.Holders(key)) == 0 && len(db.Machines()) == 1
	})
}

func TestServeDownMeansOutOfTheMesh(t *testing.T) {
	ma := doorMember(t, t.TempDir())
	mb := doorMember(t, t.TempDir())
	defer ma.Close()
	defer mb.Close()
	key := KeyOf("remote-model")
	addrs := doorPorts(t, 2)
	da, err := NewDoor(ma, doorConfig("a", addrs[0], []string{addrs[1]}), []int64{key}, nil)
	if err != nil {
		t.Fatal(err)
	}
	db, err := NewDoor(mb, doorConfig("b", addrs[1], []string{addrs[0]}), nil, []int64{key})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	claimed := make(chan broadcast.Mint, 1)
	ma.Claim(key, PriorityTurn, 0, func(m broadcast.Mint, ok bool) { claimed <- m })
	<-claimed
	doorAwait(t, "the hold to mirror", func() bool {
		return len(mb.Holders(key)) == 1
	})
	second, err := NewDoor(mb, doorConfig("a", addrs[0], nil), nil, []int64{key})
	if err == nil {
		second.Close()
		t.Fatal("no other instance must bind the door address")
	}
	da.Close()
	doorAwait(t, "serve down is out of the mesh: the machine leaves and the holds leave as acts", func() bool {
		machines := db.Machines()
		gone := true
		for _, m := range machines {
			if m == "a" {
				gone = false
			}
		}
		return gone && len(mb.Holders(key)) == 0
	})
}

func TestADoorGoneEndsItsCalls(t *testing.T) {
	m := doorMember(t, t.TempDir())
	defer m.Close()
	d, err := NewDoor(m, doorConfig("a", doorPorts(t, 1)[0], nil), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	d.Close()
	deadline := time.Now().Add(250 * time.Millisecond)
	for time.Now().Before(deadline) {
		done := make(chan struct{})
		go func() {
			d.Holding(KeyOf("row"), broadcast.Mint{})
			d.Foreign(broadcast.Mint{})
			d.Refused(KeyOf("row"), broadcast.Mint{}, 0)
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("a call on a closed door must return")
		}
	}
}
