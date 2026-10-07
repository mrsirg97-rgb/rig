package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/v2/core"
)

func delegateSnapshot(calls ...string) core.SwarmStatus {
	st := core.SwarmStatus{}
	now := time.Now()
	for i, call := range calls {
		w := core.SwarmWorker{
			ID: i + 1, Role: "delegate", Task: fmt.Sprintf("task %d", i+1), State: "running",
			Heartbeat: now.Add(-time.Duration(i) * time.Minute),
		}
		if call != "" {
			w.Tool = call
			w.ToolAt = now
		}
		st.Workers = append(st.Workers, w)
	}
	return st
}

func TestTheDelegateBandIsTwoRowsForAnyCount(t *testing.T) {
	th, err := ResolveTheme("oled", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for _, workers := range []int{1, 3} {
		calls := make([]string, workers)
		band := RenderDelegateBand(th, delegateSnapshot(calls...), now.Add(-72*time.Second), now)
		rows := strings.Split(band, "\n")
		if len(rows) != 3 {
			t.Fatalf("%d workers: the band is the rule and two rows, got %d:\n%s", workers, len(rows), band)
		}
		head := stripANSI(rows[1])
		if !strings.Contains(head, "delegating") || !strings.Contains(head, fmt.Sprintf("%d %s", workers, wordFor(workers))) {
			t.Fatalf("%d workers: the head row counts them: %q", workers, head)
		}
		if !strings.Contains(head, "1m") {
			t.Fatalf("the head row carries the elapsed of the batch: %q", head)
		}
		if strings.TrimSpace(stripANSI(rows[2])) != "—" {
			t.Fatalf("no worker has called yet, so the call row is a dash: %q", rows[2])
		}
	}
}

func TestTheCallRowIsTheMostRecentCallAcrossWorkers(t *testing.T) {
	th, _ := ResolveTheme("oled", nil, true)
	now := time.Now()
	st := core.SwarmStatus{Workers: []core.SwarmWorker{
		{ID: 1, Role: "delegate", Task: "a", State: "running", Heartbeat: now, Tool: "read core/provider.go", ToolAt: now.Add(-30 * time.Second)},
		{ID: 2, Role: "delegate", Task: "b", State: "running", Heartbeat: now, Tool: "edit tool/file/edit.go", ToolAt: now.Add(-12 * time.Second)},
		{ID: 3, Role: "delegate", Task: "c", State: "running", Heartbeat: now},
	}}
	rows := strings.Split(RenderDelegateBand(th, st, now.Add(-time.Hour), now), "\n")
	got := stripANSI(rows[2])
	if !strings.Contains(got, "#2") || !strings.Contains(got, "edit tool/file/edit.go") || !strings.Contains(got, "12s") {
		t.Fatalf("the row is the freshest call, its worker and its age: %q", got)
	}
	if strings.Contains(got, "read core") {
		t.Fatalf("the older call does not belong to the row: %q", got)
	}
}

func TestTheSwarmBandKeepsItsRoleRows(t *testing.T) {
	th, _ := ResolveTheme("oled", nil, true)
	now := time.Now()
	st := core.SwarmStatus{Pending: 7, Review: 2, Workers: []core.SwarmWorker{
		{ID: 2, Role: "worker", Task: "t388", State: "running", Heartbeat: now.Add(-12 * time.Second), Done: 3, Failed: 1},
		{ID: 3, Role: "reviewer", Task: "t386", State: "running", Heartbeat: now.Add(-4 * time.Minute)},
	}}
	if IsDelegateBand(st) {
		t.Fatal("a swarm's rows are not a delegate's")
	}
	band := RenderSwarmBand(th, st)
	if !strings.Contains(band, "workers") || !strings.Contains(band, "reviewer") {
		t.Fatalf("the swarm band keeps its role rows:\n%s", band)
	}
}

func TestABandOnlyBreathesWhileWorkersRun(t *testing.T) {
	if !bandRunning(delegateSnapshot("read x")) {
		t.Fatal("a delegate batch running between turns still breathes the status")
	}
	done := delegateSnapshot("")
	done.Workers[0].State = "done"
	if bandRunning(done) {
		t.Fatal("nothing runs, nothing breathes")
	}
	if bandRunning(core.SwarmStatus{Workers: []core.SwarmWorker{{ID: 1, Role: "worker", State: "running"}}}) {
		t.Fatal("a swarm's band has always been painted by its events")
	}
}

func TestTheInboxDrainsAloneWhenNoTurnIsLive(t *testing.T) {
	th, _ := ResolveTheme("oled", nil, true)
	s := newScriptedSession(t, th, WithWidth(90))
	s.fe.Notify(core.WorkerDone{
		N: 2, Task: "sweep the floor", Exit: 0, Duration: 252 * time.Second, Session: "abc", Log: "runs/j1/x.log",
		Content: "the answer\ndelegate: exit 0 · 42ms · session abc · log runs/j1/x.log",
	})
	line, err := s.input()
	if err != nil {
		t.Fatalf("input: %v", err)
	}
	if !strings.HasPrefix(line, "delegate #2 returned · exit 0 · 4m12s · session abc\n") {
		t.Fatalf("the block opens with its head line: %q", line)
	}
	if !strings.Contains(line, "the answer") {
		t.Fatalf("the block carries the worker's content: %q", line)
	}
	s.await("delegate #2 returned · exit 0 · 4m12s · session abc")
	if _, ok := s.fe.drainInbox(context.Background()); ok {
		t.Fatal("the inbox drained with the turn")
	}
}

func TestTheDelegateBandShowsDuringAndBetweenTurns(t *testing.T) {
	th, _ := ResolveTheme("oled", nil, true)
	ticks := make(chan time.Time, 64)
	go func() {
		t := time.NewTicker(20 * time.Millisecond)
		defer t.Stop()
		for now := range t.C {
			select {
			case ticks <- now:
			default:
			}
		}
	}()
	s := newScriptedSession(t, th, WithWidth(90), WithTicks(ticks), WithStatus(func(ctx context.Context) StatusIn {
		return StatusIn{Model: "huihui3.8", Effort: "xhigh", Window: 262144, Up: 214000, Down: 18200, CacheRead: 187000}
	}))

	if line := s.inputWhile("go\n"); line != "go" {
		t.Fatalf("input: %q", line)
	}
	st := delegateSnapshot("")
	s.fe.Notify(st)
	s.await("delegating")

	s.fe.Notify(core.SwarmStatus{Workers: []core.SwarmWorker{{
		ID: 1, Role: "delegate", Task: "task 1", State: "running", Heartbeat: time.Now(),
		Tool: "edit tool/file/edit.go", ToolAt: time.Now(),
	}}})
	s.await("edit tool/file/edit.go")

	s.fe.Notify(core.TurnEnd{Reason: core.TurnOver})

	// Between turns nothing else repaints, so the band has to drive the frame
	// ticker itself or the row freezes where the operator is looking.
	before := len(s.out.Bytes())
	deadline := time.Now().Add(2 * time.Second)
	for len(s.out.Bytes()) <= before && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if len(s.out.Bytes()) <= before {
		t.Fatal("between turns the band keeps breathing")
	}

	stale := delegateSnapshot("")
	stale.Workers[0].State = "done"
	s.fe.Notify(stale)
	time.Sleep(150 * time.Millisecond)
	s.tail = len(s.out.Bytes())
	time.Sleep(150 * time.Millisecond)
	if plain := stripANSI(s.since()); strings.Contains(plain, "delegating") {
		t.Fatalf("the band left when the batch did:\n%s", plain)
	}
}

func TestTwoReturnsDuringATurnArriveAsOneBlockInOrder(t *testing.T) {
	th, _ := ResolveTheme("oled", nil, true)
	s := newScriptedSession(t, th, WithWidth(90))

	if line := s.inputWhile("open the turn\n"); line != "open the turn" {
		t.Fatalf("the operator's text opens the turn: %q", line)
	}

	s.fe.Notify(core.WorkerDone{N: 1, Content: "one", Exit: 0, Duration: time.Second, Session: "s1"})
	s.fe.Notify(core.WorkerDone{N: 2, Content: "two", Exit: 0, Duration: time.Second, Session: "s2"})
	s.fe.Notify(core.TurnEnd{Reason: core.TurnOver})
	s.fe.steer("the next thing")

	line := s.inputAt(context.Background())
	if !strings.HasPrefix(line, "delegate #1 returned") {
		t.Fatalf("the block comes first: %q", line)
	}
	if strings.Index(line, "delegate #1") > strings.Index(line, "delegate #2") {
		t.Fatalf("the block keeps arrival order: %q", line)
	}
	if strings.Contains(line, "the next thing") {
		t.Fatalf("the operator's text is its own turn, after the block: %q", line)
	}
	if line := s.inputAt(context.Background()); line != "the next thing" {
		t.Fatalf("the operator's text follows the block: %q", line)
	}
}

// inputWhile feeds a line to a reader that has not started yet: the TUI's reader
// belongs to Input, so the keystroke has to follow the call, not precede it.
func (s *scriptedSession) inputWhile(line string) string {
	s.t.Helper()
	go s.si.feed(line)
	return s.mustInput()
}

// inputAt runs Input on a context of the caller's: a steer cancels the turn it
// interrupts, and a test must not inherit that.
func (s *scriptedSession) inputAt(ctx context.Context) string {
	s.t.Helper()
	type res struct {
		line string
		err  error
	}
	ch := make(chan res, 1)
	go func() {
		line, err := s.fe.Input(ctx)
		ch <- res{line, err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			s.t.Fatalf("input: %v", r.err)
		}
		return r.line
	case <-time.After(3 * time.Second):
		s.t.Fatalf("input never came back; stream:\n%s", s.out.String())
		return ""
	}
}

func (s *scriptedSession) mustInput() string {
	s.t.Helper()
	type res struct {
		line string
		err  error
	}
	ch := make(chan res, 1)
	go func() {
		line, err := s.fe.Input(s.ctx)
		ch <- res{line, err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			s.t.Fatalf("input: %v", r.err)
		}
		return r.line
	case <-time.After(3 * time.Second):
		s.t.Fatalf("input never came back; stream:\n%s", s.out.String())
		return ""
	}
}

// tail marks the length of the stream at a moment; since is what has been
// painted since — the stream is append-only, so "left the screen" only means
// anything about the frames after the mark.
func (s *scriptedSession) since() string {
	all := s.out.String()
	if s.tail > len(all) {
		s.tail = len(all)
	}
	return all[s.tail:]
}

func stripANSI(s string) string {
	var out strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		out.WriteByte(s[i])
	}
	return out.String()
}

func wordFor(n int) string {
	if n == 1 {
		return "worker"
	}
	return "workers"
}
func TestEscOnAnEmptyPromptStopsTheWorkers(t *testing.T) {
	th, _ := ResolveTheme("oled", nil, true)
	stops := 0
	s := newScriptedSession(t, th, WithWidth(90), WithIdleInterrupt(func() { stops++ }))

	s.fe.onKey(keyEsc, 0)
	if stops != 1 {
		t.Fatalf("the bare esc on an empty prompt is the idle interrupt: %d stops", stops)
	}

	s.fe.ed.setText("text in the way")
	s.fe.onKey(keyEsc, 0)
	if stops != 1 {
		t.Fatal("esc with something typed clears the line; it does not stop the workers")
	}
	s.inputWhile("open a turn\n")
	s.fe.onKey(keyEsc, 0)
	if stops != 1 {
		t.Fatal("with a turn live, esc interrupts the turn; the workers are not what is running")
	}
}
