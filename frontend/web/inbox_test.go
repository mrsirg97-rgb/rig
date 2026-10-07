package web

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/testenv"
)

func TestAReturnedWorkerShowsInTheFeedAndFoldsIntoTheNextTurn(t *testing.T) {
	srv, tok := newChatServer(t)
	ts := testenv.Server(t, srv.Handler())

	srv.Notify(core.WorkerDone{N: 2, Task: "sweep the floor", Content: "the answer", Exit: 0, Duration: 252 * time.Second, Session: "abc", Log: "runs/j1/x.log"})

	got := frames(t, ts.URL, tok, "0", 2, 3*time.Second)
	if k := kinds(got); k != "hello,worker_done" {
		t.Fatalf("kinds %s", k)
	}
	if got[1]["head"] != "delegate #2 returned · exit 0 · 4m12s · session abc" {
		t.Fatalf("the feed line is the head of the block: %v", got[1])
	}
	if got[1]["task"] != "sweep the floor" || got[1]["log"] != "runs/j1/x.log" {
		t.Fatalf("the feed names the work and where it is kept: %v", got[1])
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	line, err := srv.Input(ctx)
	if err != nil {
		t.Fatalf("input: %v", err)
	}
	if !strings.HasPrefix(line, "delegate #2 returned · exit 0 · 4m12s · session abc\nthe answer") {
		t.Fatalf("the fold is the same text the other frontends fold: %q", line)
	}
}

func TestTheWebWakeComesForAReturnWhileItWaits(t *testing.T) {
	srv, _ := newChatServer(t)
	done := make(chan string, 1)
	go func() {
		line, err := srv.Input(context.Background())
		if err != nil {
			t.Errorf("input: %v", err)
		}
		done <- line
	}()
	srv.Notify(core.WorkerDone{N: 5, Content: "five", Exit: 0, Duration: time.Second, Session: "s5"})
	select {
	case line := <-done:
		if !strings.HasPrefix(line, "delegate #5 returned") {
			t.Fatalf("a return while waiting is itself an input: %q", line)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Input never woke for the return")
	}
}

func TestTheStopButtonStopsTheWorkersWhenNoTurnIsLive(t *testing.T) {
	var mu sync.Mutex
	stopped := 0
	home := seedHome(t)
	srv, err := New(Options{Home: home, CWD: testCWD, Models: modelsTable(t), Crontab: &fakeCrontab{}, Root: home,
		StopWorkers: func() { mu.Lock(); stopped++; mu.Unlock() }})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	if !srv.chat.Interrupt() {
		t.Fatal("the idle interrupt did something: it stopped the workers")
	}
	mu.Lock()
	defer mu.Unlock()
	if stopped != 1 {
		t.Fatalf("the workers were stopped %d times", stopped)
	}
}

func TestTheIdleInterruptIsNotOfferedWithoutADelegate(t *testing.T) {
	srv, _ := newChatServer(t)
	if srv.chat.Interrupt() {
		t.Fatal("with no turn and no delegate, the stop button has nothing to stop")
	}
}
