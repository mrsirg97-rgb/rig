package main

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

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
	a, err := machineroom.New(home, ka.places, ka, nil)
	if err != nil {
		t.Fatal(err)
	}
	kb := newChildren(home, models.Table{}, nil, func() { b.Close() })
	b, err = machineroom.New(home, kb.places, kb, nil)
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
