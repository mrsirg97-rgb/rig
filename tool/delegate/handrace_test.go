package delegate_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/evt"
	"github.com/mrsirg97-rgb/evt/broadcast"
	"github.com/mrsirg97-rgb/rig/v3"
	"github.com/mrsirg97-rgb/rig/v3/core"
	sched "github.com/mrsirg97-rgb/rig/v3/store/scheduler"
)

type parkedSubmission struct {
	releases atomic.Int64
	stops    atomic.Int64
	settled  chan struct{}
}

func newParkedSubmission() *parkedSubmission {
	s := &parkedSubmission{settled: make(chan struct{})}
	close(s.settled)
	return s
}

func (s *parkedSubmission) Started() bool { return false }

func (s *parkedSubmission) Settled() <-chan struct{} { return s.settled }

func (s *parkedSubmission) Stop() { s.stops.Add(1) }

func (s *parkedSubmission) Release() { s.releases.Add(1) }

type parkedCap struct {
	mu   sync.Mutex
	sub  *parkedSubmission
	work evt.Closure
}

func (c *parkedCap) Size() int     { return 2 }
func (c *parkedCap) InFlight() int { return 0 }
func (c *parkedCap) Queued() int   { return 0 }

func (c *parkedCap) Submit(ctx context.Context, work evt.Closure, priority int) evt.Submission {
	c.mu.Lock()
	c.work = work
	c.mu.Unlock()
	return c.sub
}

func (c *parkedCap) release() {
	c.mu.Lock()
	work := c.work
	c.mu.Unlock()
	go work.Resolve(context.Background())
}

type parkedMember struct {
	id    int64
	mu    sync.Mutex
	gates []chan struct{}
	seen  chan struct{}
	last  atomic.Value
}

func newParkedMember() *parkedMember {
	return &parkedMember{id: rig.MemberDelegate, seen: make(chan struct{}, 32)}
}

func (m *parkedMember) Id() int64    { return m.id }
func (m *parkedMember) Room() string { return "fleet" }
func (m *parkedMember) Leave() bool  { return true }

func (m *parkedMember) Forward(context.Context, func(target int64, ack error), ...broadcast.Message) {
}

func (m *parkedMember) Subscribe(context.Context, func(err error, messages ...broadcast.Message)) {
}

func (m *parkedMember) Publish(ctx context.Context, cb func(error), messages ...broadcast.Message) {
	rel := make(chan struct{})
	m.mu.Lock()
	m.gates = append(m.gates, rel)
	m.mu.Unlock()
	if len(messages) > 0 {
		m.last.Store(messages[len(messages)-1])
	}
	m.seen <- struct{}{}
	<-rel
	cb(nil)
}

func (m *parkedMember) parkedCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.gates)
}

func (m *parkedMember) let(i int) {
	m.mu.Lock()
	ch := m.gates[i]
	m.mu.Unlock()
	close(ch)
}

func (m *parkedMember) lastSignal() evt.Signal {
	if v, ok := m.last.Load().(broadcast.Message); ok {
		return v.Signal()
	}
	return nil
}

type parkedRoom struct {
	member *parkedMember
}

func (r *parkedRoom) Id() string                                                   { return "fleet" }
func (r *parkedRoom) Add(int64) broadcast.Member                                   { return r.member }
func (r *parkedRoom) Mint() broadcast.Member                                       { return r.member }
func (r *parkedRoom) Remove(int64) bool                                            { return true }
func (r *parkedRoom) Members() []int64                                             { return nil }
func (r *parkedRoom) Broadcast(context.Context, int64, ...broadcast.Message) error { return nil }

type racedSubmission struct {
	started chan struct{}
	settled chan struct{}
}

func newRacedSubmission() *racedSubmission {
	return &racedSubmission{started: make(chan struct{}), settled: make(chan struct{})}
}

func (s *racedSubmission) Started() bool {
	<-s.started
	return true
}

func (s *racedSubmission) Settled() <-chan struct{} { return s.settled }

func (s *racedSubmission) Stop() {}

func (s *racedSubmission) Release() {}

type racedCap struct {
	mu        sync.Mutex
	work      evt.Closure
	sub       *racedSubmission
	submitted chan struct{}
}

func (c *racedCap) Size() int     { return 1 }
func (c *racedCap) InFlight() int { return 1 }
func (c *racedCap) Queued() int   { return 0 }

func (c *racedCap) Submit(ctx context.Context, work evt.Closure, priority int) evt.Submission {
	c.mu.Lock()
	c.work = work
	c.mu.Unlock()
	close(c.submitted)
	return c.sub
}

func (c *racedCap) start() {
	c.mu.Lock()
	work := c.work
	c.mu.Unlock()
	go func() {
		work.Resolve(context.Background())
		close(c.sub.settled)
	}()
}

func TestAQueuedWorkerTheCapStartedRunsOnce(t *testing.T) {
	h := newHarness(t, "/ws/sess")
	sub := newRacedSubmission()
	rcap := &racedCap{sub: sub, submitted: make(chan struct{})}
	h.cap = rcap
	release := make(chan struct{})
	spawn := &fakeSpawn{result: sched.SpawnResult{Exit: 0, Stdout: "done"}, block: release}
	tool, fe := h.asyncTool(t, fakeFetch(""), spawn.spawn)

	type outcome struct {
		out string
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- outcome{err: fmt.Errorf("exec panicked after %d spawns: %v", spawn.count(), r)}
			}
		}()
		out, err := tool.Exec(context.Background(), runArgs("the queued race"))
		done <- outcome{out, err}
	}()

	<-rcap.submitted
	rcap.start()
	waitUntil(t, "the cap's runner at the spawn", func() bool { return spawn.count() == 1 })
	close(sub.started)
	close(release)
	got := <-done
	if got.err != nil {
		t.Fatalf("the queued worker's start is one run, not two: %v", got.err)
	}
	if !strings.Contains(got.out, "worker #1 started") {
		t.Fatalf("the hand-back is the started line: %q", got.out)
	}
	if n := spawn.count(); n != 1 {
		t.Fatalf("one worker number is one spawn, got %d", n)
	}
	returns := waitForReturn(t, fe, 1)
	if len(returns) != 1 {
		t.Fatalf("one run hands back once, got %d: %+v", len(returns), returns)
	}
	if returns[0].N != 1 || returns[0].Exit != 0 || !strings.Contains(returns[0].Content, "done") {
		t.Fatalf("the return is the run's: %+v", returns[0])
	}
	waitUntil(t, "the band clear", func() bool {
		ss := fe.statuses()
		return len(ss) > 0 && len(ss[len(ss)-1].Workers) == 0
	})
}

func TestAQueuedWorkerThatStartsBeforeTheCallerBookkeepsStillReleasesItsPlace(t *testing.T) {
	h := newHarness(t, "/ws/sess")
	sub := newParkedSubmission()
	cap := &parkedCap{sub: sub}
	h.cap = cap
	member := newParkedMember()
	room := &parkedRoom{member: member}
	spawn := &fakeSpawn{result: sched.SpawnResult{Exit: 0, Stdout: "done"}}
	tool := h.newToolRoom(t, context.Background(), false, room, fakeFetch(""), spawn.spawn)

	type outcome struct {
		out string
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		out, err := tool.Exec(context.Background(), runArgs("the queued race"))
		done <- outcome{out, err}
	}()

	<-member.seen
	cap.release()
	<-member.seen
	member.let(1)
	<-member.seen
	member.let(2)
	<-member.seen
	member.let(3)
	deadline := time.Now().Add(5 * time.Second)
	for sub.releases.Load() != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := sub.releases.Load(); got != 1 {
		t.Fatalf("the claimed submission must release its place exactly once, got %d", got)
	}
	if doneMsg, ok := member.lastSignal().(core.WorkerDone); !ok || doneMsg.N != 1 || doneMsg.Exit != 0 || !strings.Contains(doneMsg.Content, "done") {
		t.Fatalf("the settled worker must still return: %+v", member.lastSignal())
	}
	member.let(0)
	got := <-done
	if got.err != nil {
		t.Fatalf("a queued hand-off is not a failure: %v", got.err)
	}
	if !strings.Contains(got.out, "worker #1 queued") {
		t.Fatalf("the hand-back is the queued line: %q", got.out)
	}
}
