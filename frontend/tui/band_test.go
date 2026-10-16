package tui

import (
	"context"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/evt"
	"github.com/mrsirg97-rgb/rig/v3/core"
	machineroom "github.com/mrsirg97-rgb/rig/v3/room"
)

func bandFixture() StatusIn {
	return StatusIn{
		Model: "ox-alpha", Effort: "max", Role: "default", Approve: "auto",
		Window: 524288, Up: 275000, Down: 506, CacheRead: 274999,
	}
}

func bandNow() time.Time {
	return time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
}

func bandSession(t *testing.T, width int) *scriptedSession {
	t.Helper()
	now := bandNow()
	s := newScriptedSession(t, oledTheme(t), WithWidth(width),
		WithStatus(func(context.Context) StatusIn { return bandFixture() }),
		WithNow(func() time.Time { return now }))
	if got := s.prompt(promptMark(oledTheme(t)), "go\n"); got != "go" {
		t.Fatalf("prompt = %q", got)
	}
	return s
}

func bandScreen(t *testing.T, s *scriptedSession, width int) []string {
	t.Helper()
	v := newVT(width)
	v.feed(s.out.Bytes())
	if v.err != "" {
		t.Fatalf("harness: %s\nstream:\n%s", v.err, s.out.String())
	}
	return v.rows
}

func bandFooter(t *testing.T, rows []string, n int) []string {
	t.Helper()
	if len(rows) < n {
		t.Fatalf("the screen holds %d rows, want at least %d:\n%q", len(rows), n, rows)
	}
	return rows[len(rows)-n:]
}

func bandSettle(t *testing.T, s *scriptedSession) {
	breathe(t, s, emberBreathStops)
}

func bandAwaitPaint(t *testing.T, s *scriptedSession, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if strings.Contains(s.out.String(), want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the stream never painted %q\nstream:\n%s", want, s.out.String())
		}
		time.Sleep(time.Millisecond)
	}
}

func bandAwait(t *testing.T, s *scriptedSession, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		rows := bandFooter(t, bandScreen(t, s, 52), 5)
		if rows[4] == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the row never reached %q, holds %q\nstream:\n%s", want, rows[4], s.out.String())
		}
		time.Sleep(time.Millisecond)
	}
}

const bandDelegateCall = "edit frontend/tui/band.go and the spec"

func bandWorker(running bool, tool string, at time.Time) core.SwarmWorker {
	w := core.SwarmWorker{ID: 1, Role: delegateRole, State: "queued", Heartbeat: bandNow()}
	if running {
		w.State = "running"
	}
	w.Tool, w.ToolAt = tool, at
	return w
}

func TestThePhoneMockupsRenderByteForByte(t *testing.T) {
	th := oledTheme(t)
	s := bandSession(t, 52)
	s.fe.Notify(core.Compacted{Summary: "[compaction] earlier turns", Kept: 92000})

	rule := strings.Repeat(th.Glyph(GlyphDot), 4)
	footer := func(row string) []string {
		return []string{
			"ox-alpha · 92k/524k",
			"max · default · auto",
			"up 275k down 506 · cache r 275k 99%",
			rule,
			row,
		}
	}
	wantFooter := func(want []string) {
		t.Helper()
		got := bandFooter(t, bandScreen(t, s, 52), len(want))
		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Fatalf("the footer drifted:\n%q\nwant:\n%q", got, want)
		}
	}

	wantFooter(footer("—"))
	if !strings.Contains(s.out.String(), th.Paint(SlotDim, "—")) {
		t.Fatalf("the idle row is not the dim dash:\n%s", s.out.String())
	}

	s.fe.Notify(core.SwarmStatus{Workers: []core.SwarmWorker{bandWorker(true, "", time.Time{})}})
	bandAwait(t, s, "delegating · 1 worker")
	wantFooter(footer("delegating · 1 worker"))
	breathe(t, s, 1)
	bandAwaitPaint(t, s, th.BreathPaint(SlotText, 1, "delegating · 1 worker"))
	bandSettle(t, s)
	bandAwaitPaint(t, s, th.Paint(SlotDim, "delegating · 1 worker · 0s"))
	wantFooter(footer("delegating · 1 worker · 0s"))

	s.fe.Notify(core.SwarmStatus{Workers: []core.SwarmWorker{
		bandWorker(true, bandDelegateCall, bandNow().Add(-2*time.Second)),
	}})
	bandAwait(t, s, "#1 edit frontend/tui/band.g…")
	wantFooter(footer("#1 edit frontend/tui/band.g…"))
	bandSettle(t, s)
	bandAwait(t, s, "delegating · 1 worker · 0s")

	s.fe.Notify(core.SwarmStatus{})
	bandAwait(t, s, "#1 edit frontend/tui/band.g…")
	wantFooter(footer("#1 edit frontend/tui/band.g…"))
	bandAwaitPaint(t, s, th.Paint(SlotDim, "#1 edit frontend/tui/band.g…"))

	s.fe.Notify(machineroom.JobStatus{Job: "j8", State: machineroom.JobRunning, Workers: 1, Started: bandNow()})
	bandAwait(t, s, "scheduled job · 1 worker")
	wantFooter(footer("scheduled job · 1 worker"))
	bandSettle(t, s)
	bandAwait(t, s, "job j8 · 1 worker · 0s")

	s.fe.Notify(machineroom.JobStatus{Job: "j8", State: machineroom.JobRunning, Workers: 1, Started: bandNow(),
		Call: bandDelegateCall, CallAt: bandNow().Add(-2 * time.Second)})
	bandAwait(t, s, "j8 #1 edit frontend/tui/band.g…")
	wantFooter(footer("j8 #1 edit frontend/tui/band.g…"))
	bandSettle(t, s)
	bandAwait(t, s, "job j8 · 1 worker · 0s")

	s.fe.Notify(machineroom.JobStatus{Job: "j8", State: machineroom.JobDone,
		Started: bandNow().Add(-8 * time.Minute), Ended: bandNow()})
	bandAwait(t, s, "job j8 done · 8m")
	wantFooter(footer("job j8 done · 8m"))
	bandSettle(t, s)
	bandAwaitPaint(t, s, th.Paint(SlotDim, "job j8 done · 8m"))
	wantFooter(footer("job j8 done · 8m"))
}

func TestEightCallsInOneBreathLeaveOneQueuedAndTheLatestShows(t *testing.T) {
	th := oledTheme(t)
	s := bandSession(t, 52)
	s.fe.Notify(core.SwarmStatus{Workers: []core.SwarmWorker{bandWorker(true, "", time.Time{})}})
	for i := 0; i < 8; i++ {
		s.fe.Notify(core.SwarmStatus{Workers: []core.SwarmWorker{
			bandWorker(true, "read f"+strconv.Itoa(i), bandNow().Add(-time.Duration(9-i)*time.Second)),
		}})
	}
	s.fe.Notify(machineroom.JobStatus{Job: "j8", State: machineroom.JobDone,
		Started: bandNow().Add(-8 * time.Minute), Ended: bandNow()})
	for i := 0; i < 3; i++ {
		bandSettle(t, s)
	}
	bandAwaitPaint(t, s, th.BreathPaint(SlotWarn, 1, "#1 read f7"))
	for i := 1; i < 7; i++ {
		call := "#1 read f" + strconv.Itoa(i)
		if strings.Contains(s.out.String(), th.BreathPaint(SlotWarn, 1, call)) {
			t.Fatalf("the replaced call %d breathed anyway:\n%s", i, s.out.String())
		}
	}
	bandAwaitPaint(t, s, th.BreathPaint(SlotSuccess, 11, "job j8 done · 8m"))
	call := th.BreathPaint(SlotWarn, 11, "#1 read f7")
	done := th.BreathPaint(SlotSuccess, 11, "job j8 done · 8m")
	if strings.LastIndex(s.out.String(), call) > strings.LastIndex(s.out.String(), done) {
		t.Fatalf("the done event must follow the latest call, not lead it")
	}
	bandAwait(t, s, "delegating · 1 worker · 0s")
}

func TestADelegatesAndAJobsItemsInterleaveInArrivalOrder(t *testing.T) {
	th := oledTheme(t)
	s := bandSession(t, 52)
	call := bandDelegateCall
	breaths := []string{
		th.BreathPaint(SlotText, 11, "delegating · 1 worker"),
		th.BreathPaint(SlotText, 11, "scheduled job · 1 worker"),
		th.BreathPaint(SlotWarn, 11, "#1 "+bandCallCut(call)),
		th.BreathPaint(SlotWarn, 11, "j8 #1 "+bandCallCut(call)),
		th.BreathPaint(SlotSuccess, 11, "job j8 done · 8m"),
	}
	s.fe.Notify(core.SwarmStatus{Workers: []core.SwarmWorker{bandWorker(true, "", time.Time{})}})
	s.fe.Notify(machineroom.JobStatus{Job: "j8", State: machineroom.JobRunning, Workers: 1, Started: bandNow()})
	s.fe.Notify(core.SwarmStatus{Workers: []core.SwarmWorker{bandWorker(true, call, bandNow().Add(-2*time.Second))}})
	s.fe.Notify(machineroom.JobStatus{Job: "j8", State: machineroom.JobRunning, Workers: 1,
		Call: call, CallAt: bandNow().Add(-2 * time.Second)})
	s.fe.Notify(machineroom.JobStatus{Job: "j8", State: machineroom.JobDone,
		Started: bandNow().Add(-8 * time.Minute), Ended: bandNow()})
	for i := 0; i < 5; i++ {
		bandSettle(t, s)
	}
	bandAwaitPaint(t, s, th.BreathPaint(SlotSuccess, 11, "job j8 done · 8m"))
	stream := s.out.String()
	at := -1
	for i, want := range breaths {
		next := strings.Index(stream[at+1:], want)
		if next < 0 {
			t.Fatalf("the item %d never breathed its last frame:\n%s", i, stream)
		}
		at = at + 1 + next
	}
	bandAwait(t, s, "delegating · 1 worker · 0s")
}

func th_(t *testing.T) Theme {
	t.Helper()
	return oledTheme(t)
}

func TestANoticeBreathesWhileTheTurnIsLive(t *testing.T) {
	s := bandSession(t, 52)
	in := make(chan string, 1)
	go func() { in <- func() string { l, _ := s.input(); return l }() }()
	s.fe.Notify(core.TextDelta{Text: "working\n"})
	s.fe.Notify(evt.Notice{Source: "todo", Text: "added t1"})
	s.await("todo: added t1")
	if got := bandFooter(t, bandScreen(t, s, 52), 5)[4]; got != "todo: added t1" {
		t.Fatalf("the notice must breathe mid-turn, the row holds %q\nstream:\n%s", got, s.out.String())
	}
	s.fe.Notify(core.TextDelta{Text: "still working\n"})
	breathe(t, s, 1)
	s.await("still working")
	activity := ""
	for _, r := range bandScreen(t, s, 52) {
		if isIndicator(strings.TrimPrefix(r, " ")) {
			activity = strings.TrimPrefix(r, " ")
		}
	}
	if activity != "thinking" {
		t.Fatalf("the indicator must keep the turn, got %q", activity)
	}
}

func TestBandCallCutHoldsRunesAndWidths(t *testing.T) {
	wide := strings.Repeat("你", 30)
	if got := bandCallCut(wide); got != strings.Repeat("你", 12)+"…" || WidthOf(got) != bandCallCols {
		t.Fatalf("the wide cut splits a rune or drifts: %q (%d)", got, WidthOf(got))
	}
	emoji := strings.Repeat("👍", 20)
	if got := bandCallCut(emoji); got != strings.Repeat("👍", 12)+"…" || WidthOf(got) != bandCallCols {
		t.Fatalf("the emoji cut splits a rune or drifts: %q (%d)", got, WidthOf(got))
	}
	exact := strings.Repeat("a", bandCallCols)
	if got := bandCallCut(exact); got != exact {
		t.Fatalf("an exact fit must not cut: %q", got)
	}
	seed := uint64(3)
	next := func(n int) int {
		seed = seed*6364136223846793005 + 1442695040888963407
		return int((seed >> 33) % uint64(n))
	}
	alphabet := []string{"a", "b", "你", "👍", "…", " "}
	for i := 0; i < 500; i++ {
		var raw strings.Builder
		for j := 0; j < next(60); j++ {
			raw.WriteString(alphabet[next(len(alphabet))])
		}
		in := raw.String()
		got := bandCallCut(in)
		if WidthOf(got) > bandCallCols {
			t.Fatalf("the cut overflowed: %q is %d wide", got, WidthOf(got))
		}
		trimmed := got
		if strings.HasSuffix(trimmed, "…") {
			trimmed = trimmed[:len(trimmed)-len("…")]
		}
		if !strings.HasPrefix(in, trimmed) {
			t.Fatalf("the cut is not a rune prefix: %q of %q", got, in)
		}
		if got != in {
			for _, r := range in[len(trimmed):] {
				if WidthOf(trimmed)+runeWidth(r) <= bandCallCols-1 {
					t.Fatalf("the cut is not maximal: %q of %q could hold %q", got, in, string(r))
				}
				break
			}
		}
	}
}

func TestTheRowNeverWrapsOnAPhone(t *testing.T) {
	s := bandSession(t, 52)
	long := "rig was updated: this instance left the room, restart it"
	s.fe.Notify(evt.Notice{Source: "room", Text: long})
	bandAwaitPaint(t, s, "room: rig was updated")
	bandSettle(t, s)
	rows := bandFooter(t, bandScreen(t, s, 52), 5)
	if WidthOf(rows[4]) > 52 || !strings.HasSuffix(rows[4], "…") {
		t.Fatalf("the row must hold one phone row, cut with …: %q", rows[4])
	}
	if rows[3] == "" || !strings.HasPrefix(rows[4], "room: rig was updated") {
		t.Fatalf("the footer shifted: %q", rows)
	}
}

func TestTheRestingRowCountsTheWorkInFlight(t *testing.T) {
	th := oledTheme(t)
	var elapsed atomic.Int64
	s := newScriptedSession(t, th, WithWidth(52),
		WithStatus(func(context.Context) StatusIn { return bandFixture() }),
		WithNow(func() time.Time { return bandNow().Add(time.Duration(elapsed.Load())) }))
	if got := s.prompt(promptMark(th), "go\n"); got != "go" {
		t.Fatalf("prompt = %q", got)
	}
	s.fe.Notify(core.TurnEnd{Reason: core.TurnOver})
	two := []core.SwarmWorker{bandWorker(true, "", time.Time{}), bandWorker(true, "", time.Time{})}
	two[1].ID = 2
	s.fe.Notify(core.SwarmStatus{Workers: two})
	bandSettle(t, s)
	bandAwait(t, s, "delegating · 2 workers · 0s")
	elapsed.Store(int64(7 * time.Second))
	breathe(t, s, 1)
	bandAwait(t, s, "delegating · 2 workers · 7s")
	s.fe.Notify(core.SwarmStatus{Workers: []core.SwarmWorker{bandWorker(true, "", time.Time{})}})
	breathe(t, s, 1)
	bandAwait(t, s, "delegating · 1 worker · 7s")
	elapsed.Store(int64(75 * time.Second))
	breathe(t, s, 1)
	bandAwait(t, s, "delegating · 1 worker · 1m")

	s.fe.Notify(machineroom.JobStatus{Job: "j8", State: machineroom.JobRunning, Workers: 1, Started: bandNow().Add(80 * time.Second)})
	bandSettle(t, s)
	elapsed.Store(int64(110 * time.Second))
	breathe(t, s, 1)
	bandAwait(t, s, "job j8 · 1 worker · 30s")

	s.fe.Notify(machineroom.JobStatus{Job: "j8", State: machineroom.JobDone,
		Started: bandNow().Add(80 * time.Second), Ended: bandNow().Add(110 * time.Second)})
	bandSettle(t, s)
	breathe(t, s, 1)
	bandAwait(t, s, "delegating · 1 worker · 1m")

	s.fe.Notify(core.SwarmStatus{})
	deadline := time.Now().Add(3 * time.Second)
	for {
		s.fe.mu.Lock()
		ticking := s.fe.tickStop != nil
		s.fe.mu.Unlock()
		if !ticking {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the ticker must rest once no work is in flight")
		}
		time.Sleep(time.Millisecond)
	}
	bandAwait(t, s, "job j8 done · 30s")
}
