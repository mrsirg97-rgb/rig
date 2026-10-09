package tui

import (
	"context"
	"fmt"
	"strconv"
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
	s.await(th.Paint(SlotText, " delegate #2 returned"))
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

func (s *scriptedSession) inputWhile(line string) string {
	s.t.Helper()
	go s.si.feed(line)
	return s.inputAt(s.ctx)
}

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

func TestAnIdleEscWithNoWorkersClearsThePrompt(t *testing.T) {
	th, _ := ResolveTheme("oled", nil, true)
	stops := 0
	s := newScriptedSession(t, th, WithWidth(90), WithIdleInterrupt(func() { stops++ }))

	s.fe.onKey(keyEsc, 0)
	s.fe.onKey(keyEsc, 0)
	if stops != 0 {
		t.Fatalf("with no workers on the band, esc clears the prompt and stops nothing: %d stops", stops)
	}

	s.fe.ed.setText("text in the way")
	s.fe.onKey(keyEsc, 0)
	if stops != 0 {
		t.Fatal("esc with something typed clears the line; it does not stop the workers")
	}
	s.inputWhile("open a turn\n")
	s.fe.onKey(keyEsc, 0)
	if stops != 0 {
		t.Fatal("with a turn live, esc interrupts the turn; the workers are not what is running")
	}
}

func TestAReturnCommitsAsABlockAndThePromptNamesIt(t *testing.T) {
	th, _ := ResolveTheme("oled", nil, true)
	s := newScriptedSession(t, th, WithWidth(90), WithSize(sizeFixture(90, 30)),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
	)
	if got := s.prompt(promptMark(th), "go\n"); got != "go" {
		t.Fatalf("prompt = %q", got)
	}
	s.fe.Notify(core.TurnEnd{Reason: core.TurnOver})
	s.fe.Notify(core.WorkerDone{
		N: 9, Task: "sweep the parsers", Content: "core/ matches\nthe listed locations",
		Exit: 0, Duration: 4860 * time.Second, Session: "1a11cf58abcdef",
	})
	line := s.inputAt(context.Background())
	if !strings.HasPrefix(line, "delegate #9 returned · exit 0 · 1h21m0s · session 1a11cf58abcdef") {
		t.Fatalf("the model still reads the block: %q", line)
	}
	plain := stripANSI(s.out.String())
	if !strings.Contains(plain, "delegate #9 · sweep the parsers") {
		t.Fatalf("the block's head: %s", plain)
	}
	if !strings.Contains(plain, "core/ matches") || !strings.Contains(plain, "the listed locations") {
		t.Fatalf("the block's preview: %s", plain)
	}
	if !strings.Contains(plain, "delegate ✓ 4860.0s · session 1a11cf58") {
		t.Fatalf("the block's close: %s", plain)
	}
	if !strings.Contains(plain, "❯ delegate #9 returned") {
		t.Fatalf("the prompt line: %s", plain)
	}
	if strings.Contains(plain, "⏎") {
		t.Fatalf("the return painted the prompt row: %s", plain)
	}
}

func TestAFailedReturnClosesWithTheFailGlyphAndTheExit(t *testing.T) {
	th, _ := ResolveTheme("oled", nil, true)
	s := newScriptedSession(t, th, WithWidth(90), WithSize(sizeFixture(90, 30)),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
	)
	if got := s.prompt(promptMark(th), "go\n"); got != "go" {
		t.Fatalf("prompt = %q", got)
	}
	s.fe.Notify(core.TurnEnd{Reason: core.TurnOver})
	s.fe.Notify(core.WorkerDone{N: 2, Task: "t2", Content: "died", Exit: -1, Duration: 100 * time.Millisecond, Session: "s2"})
	s.inputAt(context.Background())
	plain := stripANSI(s.out.String())
	if !strings.Contains(plain, "delegate ✕ 0.1s · exit -1 · session s2") {
		t.Fatalf("the failed close: %s", plain)
	}
	if strings.Contains(plain, "✓") {
		t.Fatalf("the failed worker closed with the success glyph: %s", plain)
	}
}

func TestABatchOfThreePaintsThreeBlocksAndOnePromptLine(t *testing.T) {
	th, _ := ResolveTheme("oled", nil, true)
	s := newScriptedSession(t, th, WithWidth(90), WithSize(sizeFixture(90, 40)),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
	)
	if got := s.prompt(promptMark(th), "go\n"); got != "go" {
		t.Fatalf("prompt = %q", got)
	}
	s.fe.Notify(core.TurnEnd{Reason: core.TurnOver})
	s.fe.Notify(core.WorkerDone{N: 9, Task: "t9", Content: "nine", Exit: 0, Duration: time.Second, Session: "s9"})
	s.fe.Notify(core.WorkerDone{N: 11, Task: "t11", Content: "eleven", Exit: 0, Duration: time.Second, Session: "s11"})
	s.fe.Notify(core.WorkerDone{N: 12, Task: "t12", Content: "twelve", Exit: 0, Duration: time.Second, Session: "s12"})
	line := s.inputAt(context.Background())
	if i9, i11, i12 := strings.Index(line, "delegate #9"), strings.Index(line, "delegate #11"), strings.Index(line, "delegate #12"); i9 > i11 || i11 > i12 {
		t.Fatalf("the block keeps arrival order: %q", line)
	}
	plain := stripANSI(s.out.String())
	for _, want := range []string{"delegate #9 · t9", "delegate #11 · t11", "delegate #12 · t12", "❯ delegate #9, #11, #12 returned"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("the batch did not paint %q:\n%s", want, plain)
		}
	}
}

func TestABigReturnPaintsThePreviewBound(t *testing.T) {
	th, _ := ResolveTheme("oled", nil, true)
	s := newScriptedSession(t, th, WithWidth(90), WithSize(sizeFixture(90, 40)),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
	)
	if got := s.prompt(promptMark(th), "go\n"); got != "go" {
		t.Fatalf("prompt = %q", got)
	}
	s.fe.Notify(core.TurnEnd{Reason: core.TurnOver})
	var b strings.Builder
	for i := 0; i < 70000; i++ {
		if i == 30000 {
			b.WriteString("the middle row\n")
		}
		b.WriteString("row " + strconv.Itoa(i) + "\n")
	}
	s.fe.Notify(core.WorkerDone{N: 1, Task: "big", Content: b.String(), Exit: 0, Duration: time.Second, Session: "s1"})
	s.inputAt(context.Background())
	plain := stripANSI(s.out.String())
	if !strings.Contains(plain, "lines hidden") {
		t.Fatalf("the big return did not bound its preview:\n%s", plain)
	}
	if strings.Contains(plain, "the middle row") {
		t.Fatalf("the preview kept the middle: %s", plain[len(plain)-3000:])
	}
	if strings.Count(plain, "\nrow ") > 8 {
		t.Fatalf("the preview painted past its bound: %s", plain[len(plain)-3000:])
	}
}

func TestTheCallRowPaintsTheCallWarnAndTheAgeDim(t *testing.T) {
	th, _ := ResolveTheme("oled", nil, true)
	if th.Paint(SlotWarn, "x") == th.Paint(SlotDim, "x") {
		t.Fatal("the theme must tell warn from dim for this test to mean anything")
	}
	now := time.Now()
	st := core.SwarmStatus{Workers: []core.SwarmWorker{
		{ID: 8, Role: "delegate", Task: "a", State: "running", Heartbeat: now, Tool: "read specs/SPEC_HARDENING.md", ToolAt: now.Add(-12 * time.Second)},
	}}
	rows := strings.Split(RenderDelegateBand(th, st, now.Add(-time.Minute), now), "\n")
	want := th.Paint(SlotWarn, "#8 read specs/SPEC_HARDENING.md") + th.Paint(SlotDim, " · 12s")
	if rows[2] != want {
		t.Fatalf("the call is warn, the separator and age dim:\n got %q\nwant %q", rows[2], want)
	}
	if stripANSI(rows[2]) != "#8 read specs/SPEC_HARDENING.md · 12s" {
		t.Fatalf("the visible text does not move: %q", stripANSI(rows[2]))
	}
	idle := strings.Split(RenderDelegateBand(th, delegateSnapshot(""), now.Add(-time.Minute), now), "\n")
	if idle[2] != th.Paint(SlotDim, "—") {
		t.Fatalf("no call yet stays a dim dash: %q", idle[2])
	}
}
