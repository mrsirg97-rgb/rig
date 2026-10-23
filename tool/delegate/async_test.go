package delegate_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	"github.com/mrsirg97-rgb/rig/v3/core"
	sched "github.com/mrsirg97-rgb/rig/v3/store/scheduler"
)

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
	wreg, werr := sched.Wire()
	if werr != nil {
		panic(werr)
	}
	pipe := broadcast.NewPipeTransport(id, w, broadcast.NewJSONEncoder(wreg))
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

func (c *callSpawn) send(ev evt.Signal) error {
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
	var settled atomic.Bool
	spawn := &callSpawn{started: make(chan struct{}), until: settled.Load}
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
	t.Cleanup(func() {
		settled.Store(true)
		waitForReturn(t, fe, 1)
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
	if !strings.Contains(done[0].Content, "permission denied") {
		t.Fatalf("the reason a worker never ran rides its return, not the floor:\n%q", done[0].Content)
	}
}

func TestAWorkerThatReturnsOnItsOwnReleasesItsContext(t *testing.T) {
	h := newHarness(t, "/ws/sess")
	release := make(chan struct{})
	spawn := &fakeSpawn{result: sched.SpawnResult{Exit: 0, Stdout: "done"}, block: release}
	tool, fe := h.asyncTool(t, fakeFetch(""), spawn.spawn)
	if _, err := tool.Exec(context.Background(), runArgs("the sweep that ends")); err != nil {
		t.Fatalf("hand off: %v", err)
	}
	waitUntil(t, "the worker spawn", func() bool { return spawn.count() == 1 })
	close(release)
	waitForReturn(t, fe, 1)
	select {
	case <-spawn.ctxOf(0).Done():
	case <-time.After(5 * time.Second):
		t.Fatal("a worker that returned on its own left its context registered on the session")
	}
}

func TestWaitingADelegationThatNeverWasRefuses(t *testing.T) {
	if _, err := (sched.Delegation{}).Wait(); err == nil {
		t.Fatal("a zero delegation has no worker to wait for, and says so")
	}
}

func TestTheCapQueuesTheRestAndStartsEachAsOneReturns(t *testing.T) {
	h := newHarness(t, "/ws/sess")
	h.cap = evt.NewCap(h.engine, 2)
	release := make(chan struct{})
	spawn := &fakeSpawn{result: sched.SpawnResult{Exit: 0, Stdout: "done"}, block: release}
	tool, fe := h.asyncTool(t, fakeFetch(""), spawn.spawn)

	var lines []string
	for _, task := range []string{"one", "two", "three", "four"} {
		out, err := tool.Exec(context.Background(), runArgs(task))
		if err != nil {
			t.Fatalf("a call over the cap queues, it does not fail: %v", err)
		}
		lines = append(lines, out)
	}
	if !strings.Contains(lines[1], "worker #2 started") {
		t.Fatalf("the second fits under the cap: %q", lines[1])
	}
	for _, out := range lines[2:] {
		if !strings.Contains(out, " queued · 2 workers run at once (settings maxWorkers)") {
			t.Fatalf("a call over the cap says it queued and names the setting: %q", out)
		}
	}
	waitUntil(t, "the first two", func() bool { return spawn.count() == 2 })
	time.Sleep(50 * time.Millisecond)
	if n := spawn.count(); n != 2 {
		t.Fatalf("the cap holds: %d spawned while two run", n)
	}
	close(release)
	done := waitForReturn(t, fe, 4)
	if spawn.count() != 4 {
		t.Fatalf("every queued worker ran: %d", spawn.count())
	}
	for _, d := range done {
		if d.Exit != 0 {
			t.Fatalf("a queued worker returns like any other: %+v", d)
		}
	}
}

func TestAQueuedWorkerStoppedBeforeItStartsReturnsAndNeverSpawns(t *testing.T) {
	h := newHarness(t, "/ws/sess")
	h.cap = evt.NewCap(h.engine, 1)
	release := make(chan struct{})
	spawn := &fakeSpawn{result: sched.SpawnResult{Exit: 0}, block: release}
	tool, fe := h.asyncTool(t, fakeFetch(""), spawn.spawn)
	if _, err := tool.Exec(context.Background(), runArgs("runs")); err != nil {
		t.Fatal(err)
	}
	out, err := tool.Exec(context.Background(), runArgs("waits"))
	if err != nil || !strings.Contains(out, "worker #2 queued") {
		t.Fatalf("the second queues: %q %v", out, err)
	}
	waitUntil(t, "the first", func() bool { return spawn.count() == 1 })
	tool.StopAll()
	close(release)
	done := waitForReturn(t, fe, 2)
	if spawn.count() != 1 {
		t.Fatalf("a stopped queued worker never spawns: %d", spawn.count())
	}
	var stopped bool
	for _, d := range done {
		if d.N == 2 && d.Exit == -1 && strings.Contains(d.Content, "worker #2 stopped while queued") {
			stopped = true
		}
	}
	if !stopped {
		t.Fatalf("the queued worker returns its stop: %+v", done)
	}
	waitUntil(t, "the cap empty of both places and queues", func() bool {
		return h.cap.InFlight() == 0 && h.cap.Queued() == 0
	})
}

func TestAQueuedWorkerOutlivesTheTurnThatQueuedIt(t *testing.T) {
	h := newHarness(t, "/ws/sess")
	h.cap = evt.NewCap(h.engine, 1)
	release := make(chan struct{})
	spawn := &fakeSpawn{result: sched.SpawnResult{Exit: 0, Stdout: "the queued answer"}, block: release}
	tool, fe := h.asyncTool(t, fakeFetch(""), spawn.spawn)

	if _, err := tool.Exec(context.Background(), runArgs("runs first")); err != nil {
		t.Fatal(err)
	}
	turnCtx, endTurn := context.WithCancel(context.Background())
	out, err := tool.Exec(turnCtx, runArgs("queued for a slot"))
	if err != nil || !strings.Contains(out, "worker #2 queued") {
		t.Fatalf("the second call queues: %q %v", out, err)
	}
	waitUntil(t, "the first worker's spawn", func() bool { return spawn.count() == 1 })

	endTurn()
	close(release)

	done := waitForReturn(t, fe, 2)
	if n := spawn.count(); n != 2 {
		t.Fatalf("the queued worker starts when a slot frees: %d spawned", n)
	}
	for _, d := range done {
		if d.Exit != 0 || !strings.Contains(d.Content, "the queued answer") {
			t.Fatalf("the queued worker returns like any other: %+v", d)
		}
	}
	waitUntil(t, "the cap empty of places and queues", func() bool {
		return h.cap.InFlight() == 0 && h.cap.Queued() == 0
	})
}

func TestACommandWorkerHandsBackItsRun(t *testing.T) {
	h := newHarness(t, "/ws/sess")
	spawn := &fakeSpawn{result: sched.SpawnResult{Exit: 0, Stdout: "hi\n"}}
	tool, fe := h.asyncTool(t, fakeFetch(""), spawn.spawn)

	out, err := tool.Exec(context.Background(), marshalArgs(map[string]any{
		"command":   "echo hi",
		"timeoutMs": 60000,
	}))
	if err != nil {
		t.Fatalf("hand-back: %v", err)
	}
	if !strings.HasPrefix(out, "delegate: worker #1 started · run j1 · ") || !strings.Contains(out, " · log runs/") {
		t.Fatalf("a command worker's hand-back names its run:\n%q", out)
	}
	waitUntil(t, "the command spawn", func() bool { return spawn.count() == 1 })
	if got := spawn.calls[0].Argv; len(got) != 3 || got[0] != "bash" || got[1] != "-c" || got[2] != "echo hi" {
		t.Fatalf("the command worker is bash -c: %v", got)
	}
	if deadline, ok := spawn.calls[0].Ctx.Deadline(); !ok {
		t.Fatalf("the bound must ride the spawn context: %v", deadline)
	}
	if _, _, ok := sched.FleetFrom(spawn.calls[0].Ctx); ok {
		t.Fatal("a command worker's shell cannot speak the fleet pipe, so it carries none")
	}
	for _, e := range spawn.calls[0].Env {
		if strings.HasPrefix(e, sched.FleetEnv+"=") {
			t.Fatalf("a command worker's env carries no fleet id: %q", e)
		}
	}
	done := waitForReturn(t, fe, 1)
	if done[0].Kind != core.KindCommand || done[0].RunID == "" {
		t.Fatalf("the return names its kind and its run: %+v", done[0])
	}
	if done[0].Session != "" {
		t.Fatalf("a command worker has no session: %q", done[0].Session)
	}
	if !strings.Contains(done[0].Content, "hi\n") || !strings.Contains(done[0].Content, "delegate: exit 0 · ") || !strings.Contains(done[0].Content, "· run "+done[0].RunID+" · ") {
		t.Fatalf("the return carries the output and the run trailer:\n%q", done[0].Content)
	}
	if done[0].Task != "echo hi" {
		t.Fatalf("the return's task is the command's first line: %q", done[0].Task)
	}
}

func TestACommandWorkerHandsBackOnceItsProcessExits(t *testing.T) {
	h := newHarness(t, "/ws/sess")
	tool, fe := h.asyncTool(t, fakeFetch(""), sched.RealSpawn)
	pidPath := filepath.Join(t.TempDir(), "child.pid")
	command := fmt.Sprintf("sleep 15 >/dev/null 2>&1 & echo $! > %s; exit 0", pidPath)
	out, err := tool.Exec(context.Background(), marshalArgs(map[string]any{"command": command}))
	if err != nil {
		t.Fatalf("hand-back: %v", err)
	}
	if !strings.HasPrefix(out, "delegate: worker #1 started · run ") {
		t.Fatalf("the hand-back is the started line: %q", out)
	}
	var holder int
	waitUntil(t, "the fd-3 holder the shell forked", func() bool {
		b, err := os.ReadFile(pidPath)
		if err != nil {
			return false
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
		if err != nil {
			return false
		}
		holder = pid
		return syscall.Kill(pid, 0) == nil
	})
	t.Cleanup(func() { _ = syscall.Kill(holder, syscall.SIGKILL) })
	waitUntil(t, "the run's log with its exit", func() bool {
		matches, _ := filepath.Glob(filepath.Join(h.home, "runs", "*", "*.log"))
		if len(matches) != 1 {
			return false
		}
		b, err := os.ReadFile(matches[0])
		return err == nil && strings.Contains(string(b), "exit=0")
	})
	done := waitForReturn(t, fe, 1)
	if len(done) != 1 {
		t.Fatalf("the hand-back is one return, got %d: %+v", len(done), done)
	}
	if done[0].Kind != core.KindCommand || done[0].Exit != 0 || done[0].RunID == "" {
		t.Fatalf("the return is the command worker's: %+v", done[0])
	}
	waitUntil(t, "the band clear", func() bool {
		ss := fe.statuses()
		return len(ss) > 0 && len(ss[len(ss)-1].Workers) == 0
	})
	if syscall.Kill(holder, 0) != nil {
		t.Fatal("the return lands while the fork of the worker's shell still lives")
	}
}

type heldPipeSpawn struct {
	mu      sync.Mutex
	pipe    broadcast.Transport
	started chan struct{}
}

func (c *heldPipeSpawn) spawn(ctx context.Context, argv []string, cwd string, env []string, observe func([]byte)) (sched.SpawnResult, error) {
	id, w, ok := sched.FleetFrom(ctx)
	if !ok {
		panic("a model worker's spawn context carries the fleet pipe")
	}
	dup, err := syscall.Dup(int(w.Fd()))
	if err != nil {
		panic(err)
	}
	wreg, werr := sched.Wire()
	if werr != nil {
		panic(werr)
	}
	pipe := broadcast.NewPipeTransport(id, os.NewFile(uintptr(dup), "fleet"), broadcast.NewJSONEncoder(wreg))
	c.mu.Lock()
	c.pipe = pipe
	c.mu.Unlock()
	close(c.started)
	return sched.SpawnResult{Exit: 0, Stdout: "done"}, nil
}

func (c *heldPipeSpawn) send(ev evt.Signal) error {
	c.mu.Lock()
	pipe := c.pipe
	c.mu.Unlock()
	if pipe == nil {
		return errors.New("the worker has not started")
	}
	sent := make(chan error, 1)
	pipe.Send(context.Background(), func(err error) { sent <- err }, broadcast.NewMessage(pipe.Id(), true, ev))
	return <-sent
}

func (c *heldPipeSpawn) closePipe() {
	c.mu.Lock()
	pipe := c.pipe
	c.mu.Unlock()
	if pipe != nil {
		pipe.Close()
	}
}

func TestAWorkerLastFrameArrivesBeforeWaitReturns(t *testing.T) {
	h := newHarness(t, "/ws/sess")
	room, fe := newFleetRoom(t)
	spawn := &heldPipeSpawn{started: make(chan struct{})}
	tool := h.newToolRoom(t, context.Background(), false, room, fakeFetch(""), spawn.spawn)
	if _, err := tool.Exec(context.Background(), runArgs("the reviewer's sweep")); err != nil {
		t.Fatalf("hand off: %v", err)
	}
	<-spawn.started
	landed := false
	deadline := time.Now().Add(500 * time.Millisecond)
	for !landed && time.Now().Before(deadline) {
		landed = len(fe.returns()) > 0
		if !landed {
			time.Sleep(2 * time.Millisecond)
		}
	}
	if err := spawn.send(core.Verdict{Accept: true, Reason: "the rows read clean"}); err != nil {
		t.Fatalf("the worker's last frame: %v", err)
	}
	waitUntil(t, "the verdict delivered", func() bool {
		for _, ev := range fe.signals() {
			if _, ok := ev.(core.Verdict); ok {
				return true
			}
		}
		return false
	})
	spawn.closePipe()
	done := waitForReturn(t, fe, 1)
	if len(done) != 1 {
		t.Fatalf("one worker hands back once: %+v", done)
	}
	verdictAt, returnAt := -1, -1
	for i, ev := range fe.signals() {
		switch ev.(type) {
		case core.Verdict:
			verdictAt = i
		case core.WorkerDone:
			if returnAt == -1 {
				returnAt = i
			}
		}
	}
	if verdictAt == -1 || returnAt == -1 || verdictAt > returnAt {
		t.Fatalf("the worker's last frame must arrive before Wait returns: verdict at %d, first return at %d", verdictAt, returnAt)
	}
}

func TestACommandWorkerTakesNoClockWhenUnbounded(t *testing.T) {
	h := newHarness(t, "/ws/sess")
	spawn := &fakeSpawn{result: sched.SpawnResult{Exit: 0, Stdout: "hi\n"}}
	tool, _ := h.asyncTool(t, fakeFetch(""), spawn.spawn)
	if _, err := tool.Exec(context.Background(), marshalArgs(map[string]any{"command": "echo hi"})); err != nil {
		t.Fatalf("hand-back: %v", err)
	}
	waitUntil(t, "the command spawn", func() bool { return spawn.count() == 1 })
	if deadline, ok := spawn.calls[0].Ctx.Deadline(); ok {
		t.Fatalf("no bound, no clock: %v", deadline)
	}
}

func TestDelegateCommandRefusals(t *testing.T) {
	cases := []struct {
		name string
		args map[string]any
		want string
	}{
		{"both", map[string]any{"task": "t", "command": "c"}, "task or command"},
		{"neither", map[string]any{}, "task is required"},
		{"model on a command worker", map[string]any{"command": "c", "model": "brain"}, "no model"},
		{"bound on a model worker", map[string]any{"task": "t", "timeoutMs": 60000}, "no clock"},
		{"zero bound", map[string]any{"command": "c", "timeoutMs": 0}, "at least 1000"},
		{"tiny bound", map[string]any{"command": "c", "timeoutMs": 500}, "at least 1000"},
		{"over the ceiling", map[string]any{"command": "c", "timeoutMs": 86_400_001}, "ceiling"},
		{"negative bound", map[string]any{"command": "c", "timeoutMs": -5}, "at least 1000"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t, "/ws/sess")
			spawn := &fakeSpawn{result: sched.SpawnResult{Exit: 0}}
			tool, _ := h.asyncTool(t, fakeFetch(""), spawn.spawn)
			if _, err := tool.Exec(context.Background(), marshalArgs(c.args)); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("%s: want %q, got %v", c.name, c.want, err)
			}
			if spawn.count() != 0 {
				t.Fatalf("a refused call never spawns: %d", spawn.count())
			}
		})
	}
}

func TestACommandWorkerDiesAtItsBoundAndNamesIt(t *testing.T) {
	h := newHarness(t, "/ws/sess")
	release := make(chan struct{})
	spawn := &fakeSpawn{result: sched.SpawnResult{Exit: 1, Stdout: "partial", TimedOut: true}, block: release}
	tool, fe := h.asyncTool(t, fakeFetch(""), spawn.spawn)
	if _, err := tool.Exec(context.Background(), marshalArgs(map[string]any{"command": "echo partial; sleep 5", "timeoutMs": 1000})); err != nil {
		t.Fatalf("hand-back: %v", err)
	}
	waitUntil(t, "the command spawn", func() bool { return spawn.count() == 1 })
	close(release)
	done := waitForReturn(t, fe, 1)
	if !strings.Contains(done[0].Content, "killed at timeoutMs=1000") {
		t.Fatalf("the return must name the bound: %q", done[0].Content)
	}
	if !strings.Contains(done[0].Content, "partial") {
		t.Fatalf("partial output must ride the return: %q", done[0].Content)
	}
	if done[0].Exit != 1 {
		t.Fatalf("the killed worker's exit rides the return: %+v", done[0])
	}
}

func marshalArgs(m map[string]any) json.RawMessage {
	b, _ := json.Marshal(m)
	return b
}
