package delegate_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/v2/broadcast"
	"github.com/mrsirg97-rgb/rig/v2/core"
	sched "github.com/mrsirg97-rgb/rig/v2/store/scheduler"
)

// waitForReturn reads the returns a frontend would fold, once n of them have
// landed; the order they land in is the order the frontends are tested on.
func waitForReturn(t *testing.T, fe *recordFrontend, n int) []core.WorkerDone {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for len(fe.returns()) < n {
		if time.Now().After(deadline) {
			t.Fatalf("%d worker returns never landed: %v", n, fe.returns())
		}
		time.Sleep(2 * time.Millisecond)
	}
	return fe.returns()
}

func TestTheHandBackArrivesBeforeTheWorkerExits(t *testing.T) {
	h := newHarness(t, "/ws/sess")
	release := make(chan struct{})
	spawn := &fakeSpawn{result: sched.SpawnResult{Exit: 0, Stdout: "the late answer"}, block: release}
	tool, fe := h.asyncTool(t, fakeFetch(""), spawn.spawn)

	turnCtx, cancelTurn := context.WithCancel(context.Background())
	out, err := tool.Exec(turnCtx, runArgs("the sweep that outlives its turn"))
	if err != nil {
		t.Fatalf("handing off is not a failure: %v", err)
	}
	if !strings.HasPrefix(out, "delegate: worker #1 started · session ") || !strings.Contains(out, " · log runs/") {
		t.Fatalf("the hand-back is one line naming the worker, its session and its log:\n%q", out)
	}
	if strings.Contains(out, "the late answer") {
		t.Fatalf("the answer cannot be in the hand-back: %q", out)
	}
	if lines := strings.Count(strings.TrimSpace(out), "\n"); lines != 0 {
		t.Fatalf("the hand-back is one line, got %d:\n%q", lines+1, out)
	}

	waitUntil(t, "the worker spawn", func() bool { return spawn.count() == 1 })
	workerCtx := spawn.ctxOf(0)
	cancelTurn()
	select {
	case <-workerCtx.Done():
		t.Fatal("the worker outlives the turn that started it; the turn's context must not rule it")
	case <-time.After(100 * time.Millisecond):
	}

	close(release)
	done := waitForReturn(t, fe, 1)
	if done[0].N != 1 {
		t.Fatalf("the return names the worker the hand-back named: %+v", done[0])
	}
	if !strings.Contains(done[0].Content, "the late answer") || !strings.Contains(done[0].Content, "delegate: exit 0 · ") {
		t.Fatalf("the return is the text the synchronous result always was:\n%q", done[0].Content)
	}
	if done[0].Session == "" || done[0].Log == "" || done[0].Task != "the sweep that outlives its turn" {
		t.Fatalf("the return carries the session, the log and the task: %+v", done[0])
	}
}

func TestEveryHandBackNumbersItsWorkerAndTwoRunAtOnce(t *testing.T) {
	h := newHarness(t, "/ws/sess")
	release := make(chan struct{})
	spawn := &fakeSpawn{result: sched.SpawnResult{Exit: 0, Stdout: "done"}, block: release}
	tool, fe := h.asyncTool(t, fakeFetch(""), spawn.spawn)

	first, err := tool.Exec(context.Background(), runArgs("sweep one"))
	if err != nil {
		t.Fatalf("hand off one: %v", err)
	}
	second, err := tool.Exec(context.Background(), runArgs("sweep two"))
	if err != nil {
		t.Fatalf("a second hand-back while the first runs is the point: %v", err)
	}
	if !strings.Contains(first, "worker #1 ") || !strings.Contains(second, "worker #2 ") {
		t.Fatalf("each worker is numbered for the session: %q / %q", first, second)
	}
	waitUntil(t, "both workers", func() bool { return spawn.count() == 2 })
	close(release)
	done := waitForReturn(t, fe, 2)
	byN := map[int]core.WorkerDone{}
	for _, d := range done {
		byN[d.N] = d
	}
	if byN[1].Task != "sweep one" || byN[2].Task != "sweep two" {
		t.Fatalf("each return keeps the task and the number its hand-back named: %+v", done)
	}
}

func TestAFailedWorkerReturnsItsExitRatherThanAnError(t *testing.T) {
	h := newHarness(t, "/ws/sess")
	spawn := &fakeSpawn{result: sched.SpawnResult{Exit: 7, Stdout: "half an answer"}}
	tool, fe := h.asyncTool(t, fakeFetch(""), spawn.spawn)
	if _, err := tool.Exec(context.Background(), runArgs("fall over")); err != nil {
		t.Fatalf("the hand-back cannot fail for what the worker does later: %v", err)
	}
	done := waitForReturn(t, fe, 1)
	if done[0].Exit != 7 {
		t.Fatalf("the exit is the return's: %+v", done[0])
	}
	if !strings.Contains(done[0].Content, "delegate: exit 7 · ") {
		t.Fatalf("the trailer names the exit:\n%q", done[0].Content)
	}
}

func TestTheIdleInterruptStopsEveryRunningWorker(t *testing.T) {
	h := newHarness(t, "/ws/sess")
	release := make(chan struct{})
	spawn := &fakeSpawn{result: sched.SpawnResult{Exit: 0}, block: release}
	spawn.ctxDies(true)
	tool, fe := h.asyncTool(t, fakeFetch(""), spawn.spawn)
	if _, err := tool.Exec(context.Background(), runArgs("hold on")); err != nil {
		t.Fatalf("hand off the first: %v", err)
	}
	if _, err := tool.Exec(context.Background(), runArgs("hold on too")); err != nil {
		t.Fatalf("hand off the second: %v", err)
	}
	waitUntil(t, "both workers", func() bool { return spawn.count() == 2 })

	tool.StopAll()

	for _, i := range []int{0, 1} {
		select {
		case <-spawn.ctxOf(i).Done():
		case <-time.After(5 * time.Second):
			t.Fatalf("worker %d must die with the idle interrupt", i)
		}
	}
	done := waitForReturn(t, fe, 2)
	for _, d := range done {
		if d.Exit == 0 {
			t.Fatalf("an interrupted worker returns a failure, not a success: %+v", d)
		}
	}
	// With the batch gone the stop set is empty, and the gesture costs
	// nothing: a worker that already returned is not stopped twice.
	tool.StopAll()
}

func TestTheEndOfTheSessionContextRulesTheWorker(t *testing.T) {
	h := newHarness(t, "/ws/sess")
	release := make(chan struct{})
	spawn := &fakeSpawn{result: sched.SpawnResult{Exit: 0}, block: release}
	spawn.ctxDies(true)
	sessionCtx, endSession := context.WithCancel(context.Background())
	room, fe := newFleetRoom(t)
	tool := h.newToolRoom(t, sessionCtx, false, room, fakeFetch(""), spawn.spawn)
	if _, err := tool.Exec(context.Background(), runArgs("outlive the turn, not the session")); err != nil {
		t.Fatalf("hand off: %v", err)
	}
	waitUntil(t, "the worker", func() bool { return spawn.count() == 1 })
	endSession()
	select {
	case <-spawn.ctxOf(0).Done():
	case <-time.After(5 * time.Second):
		t.Fatal("a worker lives until it exits or the session ends")
	}
	waitForReturn(t, fe, 1)
}

type callSpawn struct {
	mu      sync.Mutex
	pipe    broadcast.Transport
	started chan struct{}
	until   func() bool
}

func (c *callSpawn) spawn(ctx context.Context, argv []string, cwd string, env []string, observe func([]byte)) (sched.SpawnResult, error) {
	id, w, ok := sched.FleetFrom(ctx)
	if !ok {
		panic("a delegate worker's spawn context carries the fleet pipe")
	}
	pipe := broadcast.NewPipeTransport(id, w, broadcast.NewJSONEncoder())
	c.mu.Lock()
	c.pipe = pipe
	c.mu.Unlock()
	if c.started != nil {
		close(c.started)
	}
	deadline := time.Now().Add(10 * time.Second)
	for c.until != nil && !c.until() && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	return sched.SpawnResult{Exit: 0, Stdout: "done"}, nil
}

func (c *callSpawn) send(ev core.Event) error {
	c.mu.Lock()
	pipe := c.pipe
	c.mu.Unlock()
	if pipe == nil {
		return errors.New("the worker has not started")
	}
	done := make(chan error, 1)
	pipe.Send(context.Background(), func(err error) { done <- err }, broadcast.NewMessage(pipe.Id(), true, ev))
	return <-done
}

func TestTheSnapshotCarriesEachWorkersLastCall(t *testing.T) {
	h := newHarness(t, "/ws/sess")
	room, fe := newFleetRoom(t)
	spawn := &callSpawn{started: make(chan struct{})}
	tool := h.newToolRoom(t, context.Background(), false, room, fakeFetch(""), spawn.spawn)
	if _, err := tool.Exec(context.Background(), runArgs("read the file")); err != nil {
		t.Fatalf("hand off: %v", err)
	}
	<-spawn.started
	waitUntil(t, "the running row", func() bool {
		st := fe.statuses()
		return len(st) > 0 && len(st[len(st)-1].Workers) == 1
	})

	before := fe.statuses()
	if got := before[len(before)-1].Workers[0].Tool; got != "" {
		t.Fatalf("a worker that has not called has no call to show, got %q", got)
	}

	if err := spawn.send(core.ToolStart{Call: core.ToolCall{Name: "read", Args: json.RawMessage(`"core/provider.go"`)}}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	waitUntil(t, "the call row", func() bool {
		st := fe.statuses()
		w := st[len(st)-1].Workers
		return len(w) == 1 && w[0].Tool == "read core/provider.go" && !w[0].ToolAt.IsZero()
	})
	if err := spawn.send(core.ToolStart{Call: core.ToolCall{Name: "bash", Args: json.RawMessage(`"go test ./..."`)}}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	waitUntil(t, "the second call", func() bool {
		st := fe.statuses()
		w := st[len(st)-1].Workers
		return len(w) == 1 && w[0].Tool == "bash go test ./..."
	})
}

func TestASpawnThatFaultsIsLoudAndWaitedAndQuietlyReturnedWhenHandedOff(t *testing.T) {
	h := newHarness(t, "/ws/sess")
	fault := errors.New("exec: self: permission denied")
	spawn := &fakeSpawn{err: fault}
	tool := h.newTool(t, fakeFetch(""), spawn.spawn)
	if _, err := tool.Exec(context.Background(), runArgs("the doomed sweep")); !errors.Is(err, fault) {
		t.Fatalf("the awaited shape returns the runner's error, not a story about it: %v", err)
	}

	async, fe := h.asyncTool(t, fakeFetch(""), spawn.spawn)
	out, err := async.Exec(context.Background(), runArgs("the doomed hand-off"))
	if err != nil {
		t.Fatalf("the fault arrives after the hand-back, so the hand-back stands: %v", err)
	}
	if !strings.Contains(out, "worker #1 started") {
		t.Fatalf("the hand-back is what the turn gets: %q", out)
	}
	done := waitForReturn(t, fe, 1)
	if done[0].Exit == 0 {
		t.Fatalf("a worker that never ran is not a success: %+v", done[0])
	}
}

func TestWaitingADelegationThatNeverWasRefuses(t *testing.T) {
	if _, err := (sched.Delegation{}).Wait(); err == nil {
		t.Fatal("a zero delegation has no worker to wait for, and says so")
	}
}
