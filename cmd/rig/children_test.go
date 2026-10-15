package main

import (
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/evt/broadcast"
	"github.com/mrsirg97-rgb/rig/v3/models"
	machineroom "github.com/mrsirg97-rgb/rig/v3/room"
)

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port
}

func TestAnActEffectRunsOnceOnItsOwner(t *testing.T) {
	home := t.TempDir()
	port := freePort(t)
	marker := filepath.Join(home, "starts")
	ka := newChildren(home, models.Table{}, nil, nil)
	kb := newChildren(home, models.Table{}, nil, nil)
	var a, b machineroom.Member
	a, err := machineroom.New(home, ka.places, ka.effects, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err = machineroom.New(home, kb.places, kb.effects, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close(); b.Close(); ka.close(); kb.close() })
	waitLive(t, a, b)
	a.Act(machineroom.StartChild{
		Model:   "fake",
		Port:    port,
		Command: "echo start >> " + marker + "; sleep 1; python3 -m http.server ${PORT}",
	}, machineroom.KeyOf("fake"), machineroom.PriorityChild, func(bool) {})
	await(t, "the child's port to answer", 10*time.Second, func() bool { return machineroom.Answers(port) })
	starts, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("the child never started: %v", err)
	}
	if n := strings.Count(string(starts), "start"); n != 1 {
		t.Fatalf("the act's effect must run once, on the act's owner: %d starts", n)
	}
	ka.stop("fake", 2)
}

func TestTheRoomEngineStaysFreeWhileAChildStarts(t *testing.T) {
	home := t.TempDir()
	port := freePort(t)
	ka := newChildren(home, models.Table{}, nil, nil)
	kb := newChildren(home, models.Table{}, nil, nil)
	var a, b machineroom.Member
	a, err := machineroom.New(home, ka.places, ka.effects, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err = machineroom.New(home, kb.places, kb.effects, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close(); b.Close(); ka.close(); kb.close() })
	waitLive(t, a, b)
	a.Act(machineroom.StartChild{
		Model:   "fake",
		Port:    port,
		Command: "sleep 3; python3 -m http.server ${PORT}",
	}, machineroom.KeyOf("fake"), machineroom.PriorityChild, func(bool) {})
	row := `{"id":"fake2","window":8192,"maxTokens":2048,"reserve":1024,"keepRecent":2048,"role":"worker","command":"python3 -m http.server 9","port":` + strconv.Itoa(port+1) + `,"places":1}`
	a.Act(machineroom.TableChange{Row: json.RawMessage(row)}, machineroom.TableKey(), machineroom.PriorityChild, func(bool) {})
	await(t, "the room to fold a table change while the child's start still waits the port", 1500*time.Millisecond, func() bool {
		ok := false
		b.Ask(func() { _, ok = kb.rows.Get("fake2") })
		return ok
	})
	await(t, "the child's port to answer", 10*time.Second, func() bool { return machineroom.Answers(port) })
	a.Act(machineroom.StopChild{Model: "fake", Grace: 2}, machineroom.KeyOf("fake"), machineroom.PriorityChild, func(bool) {})
	await(t, "the child's port to stop answering", 15*time.Second, func() bool { return !machineroom.Answers(port) })
}

func waitLive(t *testing.T, members ...machineroom.Member) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		ready := true
		for _, m := range members {
			if len(m.Live()) != len(members)-1 {
				ready = false
			}
		}
		if ready {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("the members never saw each other")
}

func await(t *testing.T, what string, within time.Duration, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("%s never happened within %s", what, within)
}

func loadRows(t *testing.T, path string) models.Table {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var rows []models.Model
	if err := json.Unmarshal(data, &rows); err != nil {
		t.Fatal(err)
	}
	table, err := models.New(rows...)
	if err != nil {
		t.Fatal(err)
	}
	return table
}

func TestAJoinerReadsTheWorld(t *testing.T) {
	home := t.TempDir()
	port := freePort(t)
	marker := filepath.Join(home, "starts")
	rowOf := func(id string, p int, places int) string {
		return `{"id":"` + id + `","window":8192,"maxTokens":2048,"reserve":1024,"keepRecent":2048,"role":"worker","command":"python3 -m http.server ${PORT}","port":` + strconv.Itoa(p) + `,"places":` + strconv.Itoa(places) + `,"stopGrace":2}`
	}
	table := func(rows ...string) []byte { return []byte("[" + strings.Join(rows, ",") + "]") }
	if err := os.WriteFile(filepath.Join(home, "models.json"), table(rowOf("fake", port, 1), rowOf("other", port+7, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	ka := newChildren(home, loadRows(t, filepath.Join(home, "models.json")), nil, nil)
	var a, b machineroom.Member
	a, err := machineroom.New(home, ka.places, ka.effects, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close(); ka.close() })
	if err := os.WriteFile(filepath.Join(home, "models.json"), table(rowOf("fake", port, 1), rowOf("other", port+7, 2)), 0o644); err != nil {
		t.Fatal(err)
	}
	a.Act(machineroom.TableChange{Row: json.RawMessage(rowOf("other", port+7, 2))}, machineroom.TableKey(), machineroom.PriorityChild, func(bool) {})
	await(t, "the first member's table to take the change", 5*time.Second, func() bool {
		taken := false
		a.Ask(func() {
			m, _ := ka.rows.Get("other")
			taken = m.Places == 2
		})
		return taken
	})
	a.Act(machineroom.StartChild{
		Model:   "fake",
		Port:    port,
		Command: "echo start >> " + marker + "; python3 -m http.server ${PORT}",
	}, machineroom.KeyOf("fake"), machineroom.PriorityChild, func(bool) {})
	await(t, "the child's port to answer", 10*time.Second, func() bool { return machineroom.Answers(port) })
	await(t, "the pid file to be the world a later stop reads", 5*time.Second, func() bool {
		_, err := os.Stat(filepath.Join(home, "logs", "fake.pid"))
		return err == nil
	})
	kb := newChildren(home, loadRows(t, filepath.Join(home, "models.json")), nil, nil)
	b, err = machineroom.New(home, kb.places, kb.effects, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close(); kb.close() })
	waitLive(t, a, b)
	places := 0
	a.Ask(func() { places = ka.places(machineroom.KeyOf("fake")) })
	if places != 1 {
		t.Fatalf("the row's places must be the key's places: %d", places)
	}
	var mine, theirs models.Model
	a.Ask(func() { mine, _ = ka.rows.Get("other") })
	b.Ask(func() { theirs, _ = kb.rows.Get("other") })
	if mine.Places != theirs.Places || mine.Port != theirs.Port || mine.Command != theirs.Command || mine.StopGrace != theirs.StopGrace {
		t.Fatalf("the joiner's view must agree with the first member's: %v against %v", mine, theirs)
	}
	if !kb.start("fake", port, "echo start >> "+marker+"; python3 -m http.server ${PORT}", nil, "") {
		t.Fatal("the joiner's start must find the model resident")
	}
	starts, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(starts), "start"); n != 1 {
		t.Fatalf("a joiner must start nothing for a resident model: %d starts", n)
	}
	if _, err := os.Stat(filepath.Join(home, "logs", "fake.pid")); err != nil {
		t.Fatalf("the pid file must be the world a later stop reads: %v", err)
	}
	a.Act(machineroom.StopChild{Model: "fake", Grace: 2}, machineroom.KeyOf("fake"), machineroom.PriorityChild, func(bool) {})
	await(t, "the child's port to stop answering", 15*time.Second, func() bool { return !machineroom.Answers(port) })
}

func TestTwoInstancesRaceToStartAModelAndOneChildRuns(t *testing.T) {
	home := t.TempDir()
	port := freePort(t)
	ka := newChildren(home, models.Table{}, nil, nil)
	kb := newChildren(home, models.Table{}, nil, nil)
	var a, b machineroom.Member
	a, err := machineroom.New(home, ka.places, ka.effects, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err = machineroom.New(home, kb.places, kb.effects, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close(); b.Close(); ka.close(); kb.close() })
	waitLive(t, a, b)
	act := machineroom.StartChild{Model: "fake", Port: port, Command: "python3 -m http.server ${PORT}"}
	a.Act(act, machineroom.KeyOf("fake"), machineroom.PriorityChild, func(bool) {})
	b.Act(act, machineroom.KeyOf("fake"), machineroom.PriorityChild, func(bool) {})
	await(t, "the child's port to answer", 15*time.Second, func() bool { return machineroom.Answers(port) })
	await(t, "the pid file to name the child that holds the port", 10*time.Second, func() bool {
		held := ka.held("fake")
		return held != 0 && alive(held)
	})
	ka.stop("fake", 2)
	deadline := time.Now().Add(10 * time.Second)
	for machineroom.Answers(port) && time.Now().Before(deadline) {
		time.Sleep(25 * time.Millisecond)
	}
	if machineroom.Answers(port) {
		world, _ := os.ReadFile(filepath.Join(home, "logs", "fake.pid"))
		cline := func(pid int) string {
			if pid <= 0 {
				return "-"
			}
			b, _ := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline")
			return strconv.Quote(string(b))
		}
		t.Fatalf("the child to be stopped never happened within 10s: pid file %q, a's own %d (%s), b's own %d (%s)",
			string(world), ka.owned["fake"], cline(ka.owned["fake"]), kb.owned["fake"], cline(kb.owned["fake"]))
	}
}

func TestASwapWaitsForTheHoldsAndThenSwaps(t *testing.T) {
	home := t.TempDir()
	oldPort, newPort := freePort(t), freePort(t)
	ka := newChildren(home, models.Table{}, nil, nil)
	kb := newChildren(home, models.Table{}, nil, nil)
	var a, b machineroom.Member
	a, err := machineroom.New(home, ka.places, ka.effects, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err = machineroom.New(home, kb.places, kb.effects, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close(); b.Close(); ka.close(); kb.close() })
	waitLive(t, a, b)
	if !ka.start("old", oldPort, "python3 -m http.server ${PORT}", nil, "") {
		t.Fatal("the outgoing child's port never answered")
	}
	key := machineroom.KeyOf("fake")
	held := make(chan broadcast.Mint, 1)
	a.Claim(key, machineroom.PriorityFleet, machineroom.KeyOf("session"), func(mint broadcast.Mint, ok bool) { held <- mint })
	var claim broadcast.Mint
	select {
	case claim = <-held:
	case <-time.After(10 * time.Second):
		t.Fatal("the claim must hold the model's key")
	}
	pinned := make(chan struct{})
	a.Act(machineroom.SwapChild{
		Model: "new", Out: "old", Port: newPort,
		Command: "python3 -m http.server ${PORT}", Grace: 1,
	}, key, machineroom.PriorityChild, func(bool) { close(pinned) })
	select {
	case <-pinned:
		t.Fatal("the swap must wait behind the hold")
	case <-time.After(500 * time.Millisecond):
	}
	if !machineroom.Answers(oldPort) || machineroom.Answers(newPort) {
		t.Fatal("nothing may stop or start while the hold stands")
	}
	a.Release(claim, machineroom.PriorityFleet)
	await(t, "the new child to answer", 15*time.Second, func() bool { return machineroom.Answers(newPort) })
	await(t, "the outgoing child to be stopped", 10*time.Second, func() bool { return !machineroom.Answers(oldPort) })
	ka.stop("new", 2)
}

func TestStartChildWaitsForThePortThenStops(t *testing.T) {
	home := t.TempDir()
	port := freePort(t)
	kids := newChildren(home, models.Table{}, nil, nil)
	if !kids.start("fake", port, "python3 -m http.server ${PORT}", nil, "") {
		t.Fatal("the child's port never answered")
	}
	if !machineroom.Answers(port) {
		t.Fatal("the port must answer while the child runs")
	}
	if _, err := os.Stat(filepath.Join(home, "logs", "fake.log")); err != nil {
		t.Fatalf("the log must land at the path the note names: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "logs", "fake.pid")); err != nil {
		t.Fatalf("the pid must be written for another instance to stop it: %v", err)
	}
	kids.stop("fake", 2)
	time.Sleep(200 * time.Millisecond)
	if machineroom.Answers(port) {
		t.Fatal("the child must be stopped")
	}
}

func TestASecondInstanceStopsTheChild(t *testing.T) {
	home := t.TempDir()
	port := freePort(t)
	first := newChildren(home, models.Table{}, nil, nil)
	if !first.start("fake", port, "python3 -m http.server ${PORT}", nil, "") {
		t.Fatal("the child's port never answered")
	}
	second := newChildren(home, models.Table{}, nil, nil)
	second.stop("fake", 2)
	if machineroom.Answers(port) {
		t.Fatal("the second instance's stop must reach the child through the pid file")
	}
}

func TestTheUpdateDrainsTheRoom(t *testing.T) {
	home := t.TempDir()
	var a, b machineroom.Member
	ka := newChildren(home, models.Table{}, nil, nil)
	a, err := machineroom.New(home, ka.places, ka.effects, nil)
	if err != nil {
		t.Fatal(err)
	}
	kb := newChildren(home, models.Table{}, nil, func() { b.Close() })
	b, err = machineroom.New(home, kb.places, kb.effects, nil)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && (len(a.Live()) != 1 || len(b.Live()) != 1) {
		time.Sleep(25 * time.Millisecond)
	}
	if len(a.Live()) != 1 {
		t.Fatal("the members must see each other before the update")
	}
	a.Act(machineroom.Update{Version: "3.0.0"}, machineroom.TableKey(), machineroom.PriorityTurn, func(bool) {})
	deadline = time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && len(a.Live()) != 0 {
		time.Sleep(25 * time.Millisecond)
	}
	if len(a.Live()) != 0 {
		t.Fatalf("the room must drain: %d member(s) left", len(a.Live()))
	}
}

func TestAChildThatLivesWithoutAnsweringFailsTheStartWithinTheBound(t *testing.T) {
	home := t.TempDir()
	port := freePort(t)
	c := newChildren(home, models.Table{}, nil, nil)
	t.Cleanup(c.close)
	c.startCeil = 300 * time.Millisecond
	done := make(chan bool, 1)
	go func() { done <- c.start("slow", port, "sleep 30", nil, "") }()
	select {
	case ok := <-done:
		if ok {
			t.Fatal("a child that lives without answering its port must fail the start")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the start wait must end at its bound, not for as long as the child lives")
	}
	deadline := time.Now().Add(5 * time.Second)
	for c.held("slow") != 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if pid := c.held("slow"); pid != 0 {
		t.Fatalf("the failed start must not leave the child as the row's holder, pid file carries %d", pid)
	}
}

func dummyChild(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("sleep", "30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { cmd.Wait(); close(done) }()
	t.Cleanup(func() {
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-done
	})
	return cmd.Process.Pid
}

func TestAStopSignalsTheWorldsChildAndItsOwn(t *testing.T) {
	home := t.TempDir()
	c := newChildren(home, models.Table{}, nil, nil)
	t.Cleanup(c.close)
	mine := dummyChild(t)
	theirs := dummyChild(t)
	if err := os.MkdirAll(filepath.Join(home, "logs"), 0o755); err != nil {
		t.Fatal(err)
	}
	c.owned["fake"] = mine
	if !c.hold("fake", theirs) {
		t.Fatal("the world's pid file must carry its child")
	}
	c.stop("fake", 1)
	if alive(mine) || alive(theirs) {
		t.Fatalf("a stop must signal the instance's own child and the world's (mine %d alive %v, theirs %d alive %v)", mine, alive(mine), theirs, alive(theirs))
	}
}
