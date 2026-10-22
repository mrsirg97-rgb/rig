package room

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/evt"
	"github.com/mrsirg97-rgb/evt/broadcast"
)

func registry3_1() (broadcast.Registry, error) {
	reg, err := broadcast.Protocol()
	if err != nil {
		return nil, err
	}
	if reg, err = jsonDeclare[StartChild](reg, "room/start-child"); err != nil {
		return nil, err
	}
	if reg, err = jsonDeclare[StopChild](reg, "room/stop-child"); err != nil {
		return nil, err
	}
	if reg, err = jsonDeclare[SwapChild](reg, "room/swap-child"); err != nil {
		return nil, err
	}
	if reg, err = jsonDeclare[TableChange](reg, "room/table-change"); err != nil {
		return nil, err
	}
	return jsonDeclare[Update](reg, "room/update")
}

func codec3_1() broadcast.Encoder {
	reg, err := registry3_1()
	if err != nil {
		panic("room: " + err.Error())
	}
	return broadcast.NewJSONEncoder(reg)
}

type seenEffects struct {
	seen *[]evt.Signal
}

func (s seenEffects) Next(op broadcast.Operation) error {
	*s.seen = append(*s.seen, op.Signal())
	return nil
}

func roomAwait(t *testing.T, what string, f func() bool) {
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

func TestAnOldRegistryDropsTheLinkOnTheUndeclaredAct(t *testing.T) {
	home := shortTemp(t)
	var aTold, bTold []evt.Notice
	var aSeen, bSeen []evt.Signal
	a, err := New(home, JobStatusSince, func(int64) int { return 1 },
		func(int64) broadcast.Effects { return seenEffects{&aSeen} }, func(n evt.Notice) { aTold = append(aTold, n) })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	codec := codecOf
	codecOf = codec3_1
	b, err := New(home, "", func(int64) int { return 1 },
		func(int64) broadcast.Effects { return seenEffects{&bSeen} }, func(n evt.Notice) { bTold = append(bTold, n) })
	codecOf = codec
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)
	roomAwait(t, "the members see each other", func() bool {
		return len(a.Live()) == 1 && len(b.Live()) == 1
	})

	a.Act(JobStatus{Job: "j1", State: JobRunning}, KeyOf("job/j1"), PriorityFleet, func(bool) {})
	roomAwait(t, "the old member drops the link", func() bool { return len(b.Live()) == 0 })
	roomAwait(t, "the old member names the leave", func() bool {
		for _, n := range bTold {
			if n.Source == "room" && n.Text == "member left" {
				return true
			}
		}
		return false
	})
	if len(bSeen) != 0 {
		t.Fatalf("the act never reached the old member's effects: %v", bSeen)
	}
}

func TestAdmitsGatesOnTheDeclarations(t *testing.T) {
	home := shortTemp(t)
	a := newRoom(t, home, 1)
	b := newRoom(t, home, 1)
	roomAwait(t, "the members see each other", func() bool { return len(a.Live()) == 1 && len(b.Live()) == 1 })
	if !a.Admits("3.2.0") {
		t.Fatal("a room of 3.2.0 members admits the 3.2.0 act")
	}
	if a.Admits("4.0.0") {
		t.Fatal("a 4.0 act is beyond the room's declarations")
	}
	if a.Admits("") {
		t.Fatal("an empty version refuses")
	}
	old, err := New(home, "3.1.2", func(int64) int { return 1 }, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	roomAwait(t, "the old member joins", func() bool { return len(a.Live()) == 2 })
	if a.Admits("3.2.0") {
		t.Fatal("the room holds back the act while a 3.1.2 member is live")
	}
	old.Close()
	roomAwait(t, "the old member is gone", func() bool { return len(a.Live()) == 1 })
	if !a.Admits("3.2.0") {
		t.Fatal("the room admits again once the old member left")
	}

	mute, err := New(home, "", func(int64) int { return 1 }, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	roomAwait(t, "the mute member joins", func() bool { return len(a.Live()) == 2 })
	if a.Admits("3.2.0") {
		t.Fatal("a member with no declared version refuses fail-closed")
	}
	mute.Close()
	roomAwait(t, "the mute member is gone", func() bool { return len(a.Live()) == 1 })
	if !a.Admits("3.2.0") {
		t.Fatal("the room admits again once the mute member left")
	}

	peer := filepath.Join(home, "room", strconv.FormatInt(a.Live()[0], 10)+sidecarExt)
	if err := os.WriteFile(peer, []byte("not a version"), 0o644); err != nil {
		t.Fatal(err)
	}
	if a.Admits("3.2.0") {
		t.Fatal("a garbage sidecar refuses fail-closed")
	}
}

func TestAClosedMemberAdmitsNothing(t *testing.T) {
	m := newRoom(t, shortTemp(t), 1)
	m.Close()
	if m.Admits(JobStatusSince) {
		t.Fatal("a closed member admits everything: the live walk over nothing returned true")
	}
}

func TestTheSidecarNamesTheJoinAndIsCleanedUp(t *testing.T) {
	home := shortTemp(t)
	m := newRoom(t, home, 1)
	name := filepath.Join(home, "room", strconv.FormatInt(m.Id(), 10)+sidecarExt)
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("the sidecar is written at the join: %v", err)
	}
	if string(b) != JobStatusSince {
		t.Fatalf("the sidecar declares the member's version: %q", b)
	}
	m.Close()
	if _, err := os.Stat(name); !os.IsNotExist(err) {
		t.Fatalf("the sidecar is removed at the close: %v", err)
	}
}

func TestVersionAtLeast(t *testing.T) {
	for _, c := range []struct {
		have, want string
		ok         bool
	}{{"3.2.0", "3.2.0", true}, {"3.2.1", "3.2.0", true}, {"3.2.0", "3.2.1", false},
		{"3.1.2", "3.2.0", false}, {"3.10.0", "3.9.0", true}, {"4", "3.2.0", true},
		{"", "3.2.0", false}, {"garbage", "3.2.0", false}, {"3.2.0", "garbage", false},
		{"3.2.0-rc1", "3.2.0", false}} {
		if versionAtLeast(c.have, c.want) != c.ok {
			t.Fatalf("versionAtLeast(%q, %q) = %v", c.have, c.want, !c.ok)
		}
	}
}
