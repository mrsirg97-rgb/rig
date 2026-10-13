package room

import (
	"net"
	"strings"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/evt/broadcast"
)

const (
	doorTestIterations = int64(40000)
	doorTestPatience   = 20 * time.Second
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
	m, err := New(home, func(int64) int { return 1 }, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func doorConfig(room, machine, addr string, peers []string) broadcast.MeshConfig {
	return broadcast.MeshConfig{
		Room:       room,
		Machine:    machine,
		Addr:       addr,
		Peers:      peers,
		Prime:      broadcast.RFC3526Prime1536(),
		Iterations: doorTestIterations,
		Window:     4,
	}
}

func TestTheDoorMirrorsAHoldAcrossMachines(t *testing.T) {
	ma := doorMember(t, t.TempDir())
	mb := doorMember(t, t.TempDir())
	defer ma.Close()
	defer mb.Close()
	key := KeyOf("remote-model")
	addrs := doorPorts(t, 2)
	da, err := NewDoor(ma, doorConfig("door-room", "a", addrs[0], []string{addrs[1]}), []int64{key}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer da.Close()
	db, err := NewDoor(mb, doorConfig("door-room", "b", addrs[1], []string{addrs[0]}), nil, []int64{key})
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
	da, err := NewDoor(ma, doorConfig("door-room", "a", addrs[0], []string{addrs[1]}), []int64{key}, nil)
	if err != nil {
		t.Fatal(err)
	}
	db, err := NewDoor(mb, doorConfig("door-room", "b", addrs[1], []string{addrs[0]}), nil, []int64{key})
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

func TestALoneDoorPacesTheChain(t *testing.T) {
	m := doorMember(t, t.TempDir())
	defer m.Close()
	d, err := NewDoor(m, doorConfig("door-room", "a", doorPorts(t, 1)[0], nil), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	doorAwait(t, "the chain must run from the door's start (the tag's clock has no gate)", func() bool {
		return d.Horizon() > 0
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
	da, err := NewDoor(ma, doorConfig("door-room", "a", addrs[0], []string{addrs[1]}), []int64{key}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer da.Close()
	db, err := NewDoor(mb, doorConfig("door-room", "b", addrs[1], []string{addrs[0]}), nil, []int64{key})
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
	da, err := NewDoor(ma, doorConfig("door-room", "a", addrs[0], []string{addrs[1]}), []int64{key}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer da.Close()
	db, err := NewDoor(mb, doorConfig("door-room", "b", addrs[1], []string{addrs[0]}), nil, []int64{key})
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
	da, err := NewDoor(ma, doorConfig("door-room", "a", addrs[0], []string{addrs[1]}), []int64{key}, nil)
	if err != nil {
		t.Fatal(err)
	}
	db, err := NewDoor(mb, doorConfig("door-room", "b", addrs[1], []string{addrs[0]}), nil, []int64{key})
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
	second, err := NewDoor(mb, doorConfig("door-room", "a", addrs[0], nil), nil, []int64{key})
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
