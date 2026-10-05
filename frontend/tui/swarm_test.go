package tui

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/v2/core"
)

func swarmBandStatus() core.SwarmStatus {
	now := time.Now()
	return core.SwarmStatus{
		Workers: []core.SwarmWorker{
			{ID: 1, Role: "worker", State: "exited"},
			{ID: 2, Role: "worker", Task: "t388", Heartbeat: now.Add(-12 * time.Second), Done: 3, Failed: 1, State: "running"},
			{ID: 3, Role: "reviewer", Task: "t386", Heartbeat: now.Add(-4 * time.Minute), Done: 1, State: "running"},
		},
		Pending: 7,
		Review:  2,
	}
}

func TestSwarmBandRows(t *testing.T) {
	th, err := ResolveTheme("oled", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	got := RenderSwarmBand(th, swarmBandStatus())
	sep := th.Paint("dim", " · ")
	want := th.Paint("dim", "····") + "\n" +
		th.Paint("dim", "workers") + th.Paint("text", " 2") + sep +
		th.Paint("dim", "+") + th.Paint("text", "7") + " " +
		th.Paint("success", th.Glyph(GlyphOK)) + th.Paint("text", "3") + " " +
		th.Paint("error", th.Glyph(GlyphFail)) + th.Paint("text", "1") + sep +
		th.Paint("dim", "w2 t388 12s") + "\n" +
		th.Paint("dim", "reviewer") + th.Paint("text", " 1") + sep +
		th.Paint("dim", th.Glyph(GlyphReview)) + th.Paint("text", "2") + " " +
		th.Paint("success", th.Glyph(GlyphOK)) + th.Paint("text", "1") + " " +
		th.Paint("error", th.Glyph(GlyphFail)) + th.Paint("text", "0") + sep +
		th.Paint("dim", "w3 t386 4m")
	if got != want {
		t.Fatalf("the band:\ngot  %q\nwant %q", got, want)
	}
}

func TestSwarmBandRulesAndGlyphsFollowTheTheme(t *testing.T) {
	th, err := ResolveTheme("", []byte(`{"base":"p1","glyphs":"ascii"}`), true)
	if err != nil {
		t.Fatal(err)
	}
	got := RenderSwarmBand(th, swarmBandStatus())
	rows := strings.Split(paintFree(got), "\n")
	if len(rows) != 3 {
		t.Fatalf("the ascii band paints %d rows, want rule + 2:\n%s", len(rows), got)
	}
	if rows[0] != "...." {
		t.Errorf("the ascii rule = %q, want four dots", rows[0])
	}
	row := rows[1]
	for _, want := range []string{".", "+7", "v3", "[x]1", "w2 t388 12s"} {
		if !strings.Contains(row, want) {
			t.Errorf("the ascii worker row %q misses %q", row, want)
		}
	}
	if !strings.Contains(rows[2], "~2") {
		t.Errorf("the ascii reviewer row %q misses the review glyph", rows[2])
	}
}

func TestSwarmBandZeroRowsAndNoRuleWhenNothingRuns(t *testing.T) {
	th, err := ResolveTheme("oled", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := RenderSwarmBand(th, core.SwarmStatus{}); got != "" {
		t.Fatalf("an empty snapshot paints %q", got)
	}
	exited := core.SwarmStatus{Workers: []core.SwarmWorker{
		{ID: 1, Role: "worker", Done: 5, State: "exited"},
	}}
	if got := RenderSwarmBand(th, exited); got != "" {
		t.Fatalf("an all-exited swarm paints %q", got)
	}
}

func TestSwarmBandDelegateShowsTheWorkerRowOnly(t *testing.T) {
	th, err := ResolveTheme("oled", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	st := core.SwarmStatus{Workers: []core.SwarmWorker{
		{ID: 1, Role: "worker", Task: "sweep the floor", Heartbeat: time.Now(), State: "running"},
	}}
	got := RenderSwarmBand(th, st)
	rows := strings.Split(got, "\n")
	if len(rows) != 2 {
		t.Fatalf("a delegate paints %d rows, want rule + one:\n%s", len(rows), got)
	}
	if !strings.Contains(paintFree(got), "workers 1 · +0 ✓0 ✕0 · w1 sweep the floor") {
		t.Fatalf("the delegate row:\n%s", got)
	}
}

func TestSwarmBandBusiestIsInFlightFirst(t *testing.T) {
	th, err := ResolveTheme("oled", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	st := core.SwarmStatus{Workers: []core.SwarmWorker{
		{ID: 1, Role: "worker", Done: 9, State: "running"},
		{ID: 2, Role: "worker", Task: "t388", Heartbeat: time.Now(), Done: 1, State: "running"},
	}}
	got := RenderSwarmBand(th, st)
	if !strings.Contains(paintFree(got), "w2 t388") {
		t.Fatalf("the in-flight worker must lead the tail:\n%s", got)
	}
}

func TestSwarmBandFooterGrowsUpdatesAndReturns(t *testing.T) {
	th := oledTheme(t)
	s := newScriptedSession(t, th, WithWidth(60), WithSize(sizeFixture(60, 20)),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
	)
	if got := s.prompt(promptMark(th), "go\n"); got != "go" {
		t.Fatalf("prompt = %q", got)
	}
	base := screenAt(t, s, 60, 20)

	s.fe.Notify(core.SwarmStatus{
		Workers: []core.SwarmWorker{
			{ID: 2, Role: "worker", Task: "t388", Heartbeat: time.Now(), Done: 1, State: "running"},
			{ID: 3, Role: "reviewer", Task: "t386", Heartbeat: time.Now(), State: "running"},
		},
		Pending: 3,
		Review:  1,
	})
	s.await(th.Paint("dim", "····"))
	rows := screenAt(t, s, 60, 20)
	if len(rows) != len(base)+3 {
		t.Fatalf("the footer grew to %d rows, want %d (base %d):\n%q", len(rows), len(base)+3, len(base), rows)
	}

	s.fe.Notify(core.SwarmStatus{
		Workers: []core.SwarmWorker{
			{ID: 2, Role: "worker", Task: "t389", Heartbeat: time.Now().Add(-12 * time.Second), Done: 2, Failed: 1, State: "running"},
			{ID: 3, Role: "reviewer", Task: "t387", Heartbeat: time.Now(), State: "running"},
		},
		Pending: 5,
		Review:  2,
	})
	s.await(th.Paint("text", "5"))
	rows = screenAt(t, s, 60, 20)
	if len(rows) != len(base)+3 {
		t.Fatalf("the update changed the footer height to %d, want %d", len(rows), len(base)+3)
	}
	joined := paintFree(strings.Join(rows, "\n"))
	if !strings.Contains(joined, "w2 t389 12s") || !strings.Contains(joined, "✓2") {
		t.Fatalf("the updated band did not paint:\n%s", joined)
	}

	s.fe.Notify(core.SwarmStatus{
		Workers: []core.SwarmWorker{
			{ID: 2, Role: "worker", State: "exited", Done: 2, Failed: 1},
			{ID: 3, Role: "reviewer", State: "exited"},
		},
		Pending: 0,
		Review:  0,
	})
	deadline := time.Now().Add(3 * time.Second)
	for {
		rows = screenAt(t, s, 60, 20)
		if len(rows) == len(base) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the footer did not return to %d rows, holds %d:\n%q", len(base), len(rows), rows)
		}
		time.Sleep(2 * time.Millisecond)
	}
	joined = paintFree(strings.Join(rows, "\n"))
	if strings.Contains(joined, "workers 2") || strings.Contains(joined, "····") {
		t.Fatalf("the band lingers after the exit:\n%s", joined)
	}
}

func TestSwarmBandResizeLeavesNoTornRows(t *testing.T) {
	th := oledTheme(t)
	size := newMutable(50, 14)
	winch := make(chan struct{}, 4)
	s := newScriptedSession(t, th, WithWidth(50), WithSize(size.get),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
		WithWinch(winch),
	)
	if got := s.prompt(promptMark(th), "go\n"); got != "go" {
		t.Fatalf("prompt = %q", got)
	}
	s.fe.Notify(core.SwarmStatus{
		Workers: []core.SwarmWorker{
			{ID: 2, Role: "worker", Task: "t388", Heartbeat: time.Now(), Done: 1, State: "running"},
			{ID: 3, Role: "reviewer", Task: "t386", Heartbeat: time.Now(), State: "running"},
		},
		Pending: 3,
		Review:  1,
	})
	s.await(th.Paint("dim", "····"))
	time.Sleep(20 * time.Millisecond)

	v := newVTScreen(50, 14)
	chunks := s.out.writeChunks()
	painted := 0
	for ; painted < len(chunks); painted++ {
		v.feed([]byte(chunks[painted]))
	}
	checkViewportInvariants(t, "band pre-resize", v, "workers 1")

	size.set(36, 10)
	v.width = 36
	winch <- struct{}{}
	time.Sleep(30 * time.Millisecond)
	chunks = s.out.writeChunks()
	for ; painted < len(chunks); painted++ {
		v.feed([]byte(chunks[painted]))
	}
	checkViewportInvariants(t, "band resize", v, "workers 1")
	joined := paintFree(strings.Join(v.rows, "\n"))
	if !strings.Contains(joined, "····") || !strings.Contains(joined, "+3") || !strings.Contains(joined, "⧗1") {
		t.Fatalf("the band or its rule vanished on the resize:\n%s", joined)
	}

	freeze := newVTStream(36)
	stream := s.out.Bytes()
	for off := 0; off < len(stream); off += 9 {
		end := off + 9
		if end > len(stream) {
			end = len(stream)
		}
		freeze.feed(stream[off:end])
		if freeze.err != "" {
			t.Fatalf("freeze harness: %s\nstream:\n%s", freeze.err, s.out.String())
		}
	}
	joined = paintFree(strings.Join(freeze.rows, "\n"))
	if !strings.Contains(joined, "workers 1") || !strings.Contains(joined, "+3") {
		t.Fatalf("the freeze harness lost the band rows:\n%s", joined)
	}
}

func idleSession(t *testing.T, th Theme) *scriptedSession {
	t.Helper()
	s := newScriptedSession(t, th, WithWidth(60),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
	)
	if got := s.prompt(promptMark(th), "go\n"); got != "go" {
		t.Fatalf("prompt = %q", got)
	}
	s.fe.Notify(core.TurnEnd{Reason: core.TurnOver})
	return s
}

func screenHas(t *testing.T, s *scriptedSession, text string) int {
	t.Helper()
	n := 0
	for _, r := range screenLines(t, s, 60) {
		if strings.Contains(paintFree(r), text) {
			n++
		}
	}
	return n
}

var breathClock atomic.Int64

func breathe(t *testing.T, s *scriptedSession, frames int) {
	t.Helper()
	for i := 0; i < frames; i++ {
		n := breathClock.Add(1)
		select {
		case s.ticks <- time.Unix(0, 0).Add(time.Duration(n) * animPeriod * 10):
		case <-time.After(2 * time.Second):
			t.Fatal("the frame ticker is not listening")
		}
	}
}

func awaitScreen(t *testing.T, s *scriptedSession, text string, present bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for (screenHas(t, s, text) > 0) != present {
		if time.Now().After(deadline) {
			t.Fatalf("screen never reached %q present=%v:\n%s", text, present, strings.Join(screenLines(t, s, 60), "\n"))
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestANoticeBreathesInTheIndicatorRowThenGoes(t *testing.T) {
	th := oledTheme(t)
	s := idleSession(t, th)
	s.fe.Notify(core.Notice{Source: "decision", Text: "review: fire: refused", Level: core.LevelError})
	awaitScreen(t, s, "decision: review: fire: refused", true)
	if n := screenHas(t, s, "decision: review: fire: refused"); n != 1 {
		t.Fatalf("the notice paints one row while it breathes, got %d", n)
	}
	breathe(t, s, emberBreathStops)
	awaitScreen(t, s, "decision: review: fire: refused", false)
	s.fe.mu.Lock()
	noticing, running := s.fe.noticing, s.fe.tickStop != nil
	s.fe.mu.Unlock()
	if noticing || running {
		t.Fatalf("after the breath the row is idle and the ticker stopped: noticing=%v ticker=%v", noticing, running)
	}
}

func TestANoticeWaitsWhileTheTurnIsLive(t *testing.T) {
	th := oledTheme(t)
	s := newScriptedSession(t, th, WithWidth(60),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
	)
	if got := s.prompt(promptMark(th), "go\n"); got != "go" {
		t.Fatalf("prompt = %q", got)
	}
	s.fe.Notify(core.Notice{Source: "graph", Text: "queue full, dropping a.go", Level: core.LevelError})
	breathe(t, s, 3)
	if n := screenHas(t, s, "graph: queue full"); n != 0 {
		t.Fatalf("a notice waits while the model works, got %d rows", n)
	}
	s.fe.Notify(core.TurnEnd{Reason: core.TurnOver})
	awaitScreen(t, s, "graph: queue full, dropping a.go", true)
	breathe(t, s, emberBreathStops)
	awaitScreen(t, s, "graph: queue full, dropping a.go", false)
}

func TestNoticesShowInOrderAndADuplicateCollapses(t *testing.T) {
	th := oledTheme(t)
	s := idleSession(t, th)
	first := core.Notice{Source: "swarm", Text: "w1 died — t1 restarted", Level: core.LevelError}
	second := core.Notice{Source: "swarm", Text: "/swarm exited — 2 workers stopped", Level: core.LevelSuccess}
	s.fe.Notify(first)
	s.fe.Notify(first)
	s.fe.Notify(second)
	s.fe.mu.Lock()
	queued := len(s.fe.notices)
	s.fe.mu.Unlock()
	if queued != 2 {
		t.Fatalf("a duplicate collapses: queued %d, want 2", queued)
	}
	awaitScreen(t, s, "swarm: w1 died — t1 restarted", true)
	if screenHas(t, s, "/swarm exited") != 0 {
		t.Fatal("the second notice waits for the first breath")
	}
	breathe(t, s, emberBreathStops)
	awaitScreen(t, s, "swarm: /swarm exited — 2 workers stopped", true)
	breathe(t, s, emberBreathStops)
	awaitScreen(t, s, "swarm: /swarm exited — 2 workers stopped", false)
	if n := screenHas(t, s, "w1 died"); n != 0 {
		t.Fatalf("nothing commits to the transcript, found %d rows", n)
	}
}

func TestANoticeBreathesInItsLevelsSlot(t *testing.T) {
	th := oledTheme(t)
	s := idleSession(t, th)
	text := "swarm: /swarm exited — 1 worker stopped"
	s.fe.Notify(core.Notice{Source: "swarm", Text: "/swarm exited — 1 worker stopped", Level: core.LevelSuccess})
	awaitScreen(t, s, text, true)
	stream := s.out.String()
	ok := false
	for i := 0; i < emberBreathStops; i++ {
		if strings.Contains(stream, th.BreathPaint(SlotSuccess, i, text)) {
			ok = true
		}
	}
	if !ok {
		t.Fatal("a success notice breathes on the success slot's curve")
	}
	for i := 0; i < emberBreathStops; i++ {
		if strings.Contains(stream, th.EmberPaint(i, text)) {
			t.Fatal("a notice never wears the ember")
		}
	}
}

func TestAPhaseTakesTheIdleRowStreamsItsThinkingAndChecksOut(t *testing.T) {
	th := oledTheme(t)
	s := idleSession(t, th)
	s.fe.Notify(core.Phase{Name: "reviewing"})
	awaitScreen(t, s, "reviewing · ", true)
	s.fe.Notify(core.Phase{Name: "reviewing", Text: "row 1 reads safe\n"})
	breathe(t, s, 1)
	awaitScreen(t, s, "row 1 reads safe", true)
	s.fe.Notify(core.Phase{Name: "reviewing", Done: true, Ok: true, Note: "3 rows settled"})
	awaitScreen(t, s, "reviewing · 3 rows settled", true)
	awaitScreen(t, s, "reviewing · 0s", false)
	s.fe.mu.Lock()
	aside, running := s.fe.aside, s.fe.tickStop != nil
	s.fe.mu.Unlock()
	if aside != "" || running {
		t.Fatalf("after the check the row is idle: aside=%q ticker=%v", aside, running)
	}
	if !strings.Contains(s.out.String(), th.Paint(SlotSuccess, th.Glyph(GlyphOK))+" "+th.Paint(SlotDim, "reviewing")) {
		t.Fatal("the end line is a green check beside the phase name")
	}
}

func TestAPhaseWaitsBehindALiveTurn(t *testing.T) {
	th := oledTheme(t)
	s := newScriptedSession(t, th, WithWidth(60),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
	)
	if got := s.prompt(promptMark(th), "go\n"); got != "go" {
		t.Fatalf("prompt = %q", got)
	}
	s.fe.Notify(core.Phase{Name: "reviewing"})
	s.fe.Notify(core.Phase{Name: "reviewing", Text: "quiet thought"})
	breathe(t, s, 2)
	if screenHas(t, s, "reviewing · ") != 0 || screenHas(t, s, "quiet thought") != 0 {
		t.Fatal("the turn's indicator owns the row; the phase and its thinking wait")
	}
	s.fe.Notify(core.TurnEnd{Reason: core.TurnOver})
	awaitScreen(t, s, "reviewing · ", true)
}

func TestCompactionIsTheSummarizingPhaseWithElapsedAndThinking(t *testing.T) {
	th := oledTheme(t)
	s := newScriptedSession(t, th, WithWidth(60),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
	)
	if got := s.prompt(promptMark(th), "go\n"); got != "go" {
		t.Fatalf("prompt = %q", got)
	}
	s.fe.Notify(core.Compacting{})
	s.fe.Notify(core.Phase{Name: "summarizing"})
	breathe(t, s, 1)
	awaitScreen(t, s, "summarizing · 0s", true)
	s.fe.Notify(core.Phase{Name: "summarizing", Text: "folding the older turns\n"})
	breathe(t, s, 1)
	awaitScreen(t, s, "folding the older turns", true)
	s.fe.Notify(core.Compacted{Summary: "s", Dropped: 1200, Kept: 400})
	awaitScreen(t, s, "summarizing · 0s", false)
	if screenHas(t, s, "compact: -1.2k kept 400") != 1 {
		t.Fatal("the compaction line is the summarizing phase's end")
	}
}

func TestTheCompactionLineLandsAfterTheSummarysLastThought(t *testing.T) {
	th := oledTheme(t)
	s := newScriptedSession(t, th, WithWidth(60),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
	)
	if got := s.prompt(promptMark(th), "go\n"); got != "go" {
		t.Fatalf("prompt = %q", got)
	}
	s.fe.Notify(core.Compacting{})
	s.fe.Notify(core.Phase{Name: "summarizing"})
	s.fe.Notify(core.Phase{Name: "summarizing", Text: "folding the older turns into one"})
	s.fe.Notify(core.Compacted{Summary: "s", Dropped: 1200, Kept: 400, Usage: core.Usage{Prompt: 2000, Completion: 1000}})
	awaitScreen(t, s, "compact: -1.2k kept 400 · up 2.0k down 1.0k", true)
	rows := screenLines(t, s, 60)
	thought, line := -1, -1
	for i, r := range rows {
		if strings.Contains(paintFree(r), "folding the older turns") {
			thought = i
		}
		if strings.Contains(paintFree(r), "compact: -1.2k") {
			line = i
		}
	}
	if thought < 0 || line < 0 || thought > line {
		t.Fatalf("the compaction line lands after the summary's last thought (thought row %d, line row %d):\n%s", thought, line, strings.Join(rows, "\n"))
	}
}
