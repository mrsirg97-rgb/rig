package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/v3/core"
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

func TestTheStartItemCountsTheBatch(t *testing.T) {
	th, err := ResolveTheme("oled", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, workers := range []int{1, 3} {
		s := newScriptedSession(t, th, WithWidth(90), WithNow(func() time.Time { return bandNow() }))
		ws := make([]core.SwarmWorker, workers)
		for i := range ws {
			ws[i] = core.SwarmWorker{ID: i + 1, Role: "delegate", Task: "t", State: "running", Heartbeat: bandNow()}
		}
		s.fe.Notify(core.SwarmStatus{Workers: ws})
		s.fe.mu.Lock()
		text := s.fe.bandHold.text
		s.fe.mu.Unlock()
		if text != "delegating · "+strconv.Itoa(workers)+" "+wordFor(workers) {
			t.Fatalf("%d workers: the start item counts them: %q", workers, text)
		}
	}
}

func TestEachWorkersCallIsItsOwnItem(t *testing.T) {
	th, _ := ResolveTheme("oled", nil, true)
	s := newScriptedSession(t, th, WithWidth(90), WithNow(func() time.Time { return bandNow() }))
	now := bandNow()
	s.fe.Notify(core.SwarmStatus{Workers: []core.SwarmWorker{
		{ID: 1, Role: "delegate", State: "running", Heartbeat: now, Tool: "read core/provider.go", ToolAt: now.Add(-30 * time.Second)},
		{ID: 2, Role: "delegate", State: "running", Heartbeat: now, Tool: "edit tool/file/edit.go", ToolAt: now.Add(-12 * time.Second)},
	}})
	s.fe.mu.Lock()
	queued := len(s.fe.bandQueue)
	first := s.fe.bandQueue[0].text
	second := s.fe.bandQueue[1].text
	s.fe.mu.Unlock()
	if queued != 2 {
		t.Fatalf("two workers' calls queue as two items beside the held start, got %d", queued)
	}
	if first != "#1 read core/provider.go" {
		t.Fatalf("the first worker's item: %q", first)
	}
	if second != "#2 edit tool/file/edit.go" {
		t.Fatalf("the second worker's item: %q", second)
	}
	s.fe.Notify(core.SwarmStatus{Workers: []core.SwarmWorker{
		{ID: 1, Role: "delegate", State: "running", Heartbeat: now, Tool: "read core/other.go", ToolAt: now.Add(-5 * time.Second)},
		{ID: 2, Role: "delegate", State: "running", Heartbeat: now, Tool: "edit tool/file/edit.go", ToolAt: now.Add(-12 * time.Second)},
	}})
	s.fe.mu.Lock()
	queued = len(s.fe.bandQueue)
	first = s.fe.bandQueue[0].text
	s.fe.mu.Unlock()
	if queued != 2 {
		t.Fatalf("a newer call replaces its worker's item in place, got %d", queued)
	}
	if first != "#1 read core/other.go" {
		t.Fatalf("the replaced item: %q", first)
	}
}

func TestTheHeartbeatStreamDoesNotRebreatheACall(t *testing.T) {
	th, _ := ResolveTheme("oled", nil, true)
	s := newScriptedSession(t, th, WithWidth(90), WithNow(func() time.Time { return bandNow() }))
	now := bandNow()
	s.fe.Notify(core.SwarmStatus{Workers: []core.SwarmWorker{
		{ID: 1, Role: "delegate", State: "running", Heartbeat: now, Tool: "read core/provider.go", ToolAt: now.Add(-2 * time.Second)},
	}})
	s.fe.mu.Lock()
	queued := len(s.fe.bandQueue)
	s.fe.mu.Unlock()
	if queued != 1 {
		t.Fatalf("the first call queues: %d", queued)
	}
	s.fe.Notify(core.SwarmStatus{Workers: []core.SwarmWorker{
		{ID: 1, Role: "delegate", State: "running", Heartbeat: now.Add(time.Second), Tool: "read core/provider.go", ToolAt: now.Add(-2 * time.Second)},
	}})
	s.fe.mu.Lock()
	queued = len(s.fe.bandQueue)
	s.fe.mu.Unlock()
	if queued != 1 {
		t.Fatalf("the same call re-reported on a heartbeat must not requeue: %d", queued)
	}
}

func TestTheTickerRunsForBreathsNotBatches(t *testing.T) {
	th := oledTheme(t)
	s := newScriptedSession(t, th, WithWidth(90), WithNow(func() time.Time { return bandNow() }))
	if got := s.prompt(promptMark(th), "go\n"); got != "go" {
		t.Fatalf("prompt = %q", got)
	}
	s.fe.Notify(core.TurnEnd{Reason: core.TurnOver})
	now := bandNow()
	s.fe.Notify(core.SwarmStatus{Workers: []core.SwarmWorker{
		{ID: 1, Role: "delegate", State: "running", Heartbeat: now},
	}})
	s.fe.mu.Lock()
	live := s.fe.bandLive
	s.fe.mu.Unlock()
	if !live {
		t.Fatal("a work item breathes as soon as it arrives")
	}
	breathe(t, s, emberBreathStops)
	s.fe.Notify(core.SwarmStatus{Workers: []core.SwarmWorker{
		{ID: 1, Role: "delegate", State: "running", Heartbeat: now, Tool: "read x", ToolAt: now},
	}})
	breathe(t, s, emberBreathStops)
	s.fe.Notify(core.SwarmStatus{})
	time.Sleep(50 * time.Millisecond)
	s.fe.mu.Lock()
	live, running, hold := s.fe.bandLive, s.fe.tickStop != nil, s.fe.bandHold.text
	s.fe.mu.Unlock()
	if live || running {
		t.Fatalf("nothing breathes once the queue drains: live=%v ticker=%v", live, running)
	}
	if hold != "#1 read x" {
		t.Fatalf("the row holds the last item dimmed: %q", hold)
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

func TestTheDelegateItemsShowDuringAndBetweenTurns(t *testing.T) {
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

	time.Sleep(150 * time.Millisecond)
	if !strings.Contains(paintFree(strings.Join(screenLines(t, s, 90), "\n")), "edit tool/file/edit.go") {
		t.Fatalf("between turns the row holds the last item:\n%s", stripANSI(s.since()))
	}

	stale := delegateSnapshot("")
	stale.Workers[0].State = "done"
	s.fe.Notify(stale)
	time.Sleep(150 * time.Millisecond)
	if !strings.Contains(paintFree(strings.Join(screenLines(t, s, 90), "\n")), "edit tool/file/edit.go") {
		t.Fatalf("the batch's end holds the last item, it does not erase the row")
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

func TestTheCallRowPaintsTheCallWarn(t *testing.T) {
	th, _ := ResolveTheme("oled", nil, true)
	if th.Paint(SlotWarn, "x") == th.Paint(SlotDim, "x") {
		t.Fatal("the theme must tell warn from dim for this test to mean anything")
	}
	s := newScriptedSession(t, th, WithWidth(90), WithNow(func() time.Time { return bandNow() }))
	now := bandNow()
	s.fe.Notify(core.SwarmStatus{Workers: []core.SwarmWorker{
		{ID: 8, Role: "delegate", Task: "a", State: "running", Heartbeat: now, Tool: "read core/provider.go", ToolAt: now.Add(-12 * time.Second)},
	}})
	s.fe.mu.Lock()
	item := s.fe.bandQueue[0]
	s.fe.mu.Unlock()
	if item.text != "#8 read core/provider.go" {
		t.Fatalf("the call item carries the worker and the call: %q", item.text)
	}
	if th.Paint(item.slot, item.text) != th.Paint(SlotWarn, "#8 read core/provider.go") {
		t.Fatalf("the call breathes in the warn slot: %q", item.slot)
	}
}

func TestAReturnInsideATurnIsCommittedAndHandedOverOnce(t *testing.T) {
	th, _ := ResolveTheme("oled", nil, true)
	s := newScriptedSession(t, th, WithWidth(90))

	if line := s.inputWhile("open the turn\n"); line != "open the turn" {
		t.Fatalf("the operator's text opens the turn: %q", line)
	}
	if _, ok := s.fe.Returns(); ok {
		t.Fatal("an empty inbox hands nothing over")
	}
	s.fe.Notify(core.WorkerDone{N: 1, Task: "go build", Content: "BUILD-OK", Exit: 0, Duration: time.Second, Kind: core.KindCommand, RunID: "j1"})
	s.fe.Notify(core.WorkerDone{N: 2, Task: "go vet", Content: "VET-OK", Exit: 0, Duration: time.Second, Kind: core.KindCommand, RunID: "j2"})
	s.tail = len(s.out.Bytes())
	block, ok := s.fe.Returns()
	if !ok || !strings.HasPrefix(block, "delegate #1 returned") || !strings.Contains(block, "delegate #2 returned") {
		t.Fatalf("both returns ride one block in arrival order: %q", block)
	}
	plain := stripANSI(s.since())
	for _, want := range []string{"delegate #1", "BUILD-OK", "VET-OK", "delegate #1, #2 returned"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("the transcript shows what the model was told (%q):\n%s", want, plain)
		}
	}
	if _, ok := s.fe.Returns(); ok {
		t.Fatal("a return is handed over once")
	}
	s.fe.Notify(core.TurnEnd{Reason: core.TurnOver})
	if _, ok := s.fe.drainInbox(context.Background()); ok {
		t.Fatal("the boundary has nothing left to carry")
	}
}
