package tui

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/evt"
	"github.com/mrsirg97-rgb/rig/v3/core"
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

func TestTheStartItemsRouteByRoleAndKeepTheirOrder(t *testing.T) {
	th := oledTheme(t)
	s := newScriptedSession(t, th, WithWidth(90), WithNow(func() time.Time { return bandNow() }))
	s.fe.Notify(core.SwarmStatus{
		Workers: []core.SwarmWorker{{ID: 2, Role: "worker", State: "running", Heartbeat: bandNow()}},
		Pending: 3,
	})
	s.fe.Notify(core.SwarmStatus{Workers: []core.SwarmWorker{
		{ID: 1, Role: "delegate", State: "running", Heartbeat: bandNow()},
	}})
	s.fe.mu.Lock()
	first, second, queued := s.fe.bandHold.text, s.fe.bandQueue[0].text, len(s.fe.bandQueue)
	s.fe.mu.Unlock()
	if first != "swarm · 1 worker" || second != "delegating · 1 worker" {
		t.Fatalf("the starts name their publisher and count: %q then %q", first, second)
	}
	if queued != 1 {
		t.Fatalf("the swarm's start holds the row and the delegate's waits: %d queued", queued)
	}
	s.fe.Notify(core.SwarmStatus{Workers: []core.SwarmWorker{
		{ID: 2, Role: "worker", State: "exited", Done: 1},
	}})
	s.fe.mu.Lock()
	second = s.fe.bandQueue[0].text
	s.fe.mu.Unlock()
	if second != "delegating · 1 worker" {
		t.Fatalf("the swarm's drained status leaves the delegate's item: %q", second)
	}
}

func TestTheRuleNeverLeavesTheFooter(t *testing.T) {
	th, err := ResolveTheme("", []byte(`{"base":"p1","glyphs":"ascii"}`), true)
	if err != nil {
		t.Fatal(err)
	}
	s := newScriptedSession(t, th, WithWidth(60),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }))
	if got := s.prompt(promptMark(th), "go\n"); got != "go" {
		t.Fatalf("prompt = %q", got)
	}
	s.await("....")
	joined := paintFree(strings.Join(screenLines(t, s, 60), "\n"))
	if !strings.Contains(joined, "....\n—") {
		t.Fatalf("the idle footer carries the rule and the dash:\n%s", joined)
	}
}

func TestAnEmptySnapshotQueuesNothing(t *testing.T) {
	s := newScriptedSession(t, oledTheme(t), WithWidth(90), WithNow(func() time.Time { return bandNow() }))
	s.fe.Notify(core.SwarmStatus{})
	exited := core.SwarmStatus{Workers: []core.SwarmWorker{
		{ID: 1, Role: "worker", Done: 5, State: "exited"},
	}}
	s.fe.Notify(exited)
	s.fe.mu.Lock()
	queued, hold := len(s.fe.bandQueue), s.fe.bandHold.text
	s.fe.mu.Unlock()
	if queued != 0 || hold != "" {
		t.Fatalf("nothing runs, nothing queues: %d items, hold %q", queued, hold)
	}
}

func TestTheFooterHoldsItsHeightThroughABand(t *testing.T) {
	th := oledTheme(t)
	s := newScriptedSession(t, th, WithWidth(60), WithSize(sizeFixture(60, 20)),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
	)
	if got := s.prompt(promptMark(th), "go\n"); got != "go" {
		t.Fatalf("prompt = %q", got)
	}
	base := screenAt(t, s, 60, 20)

	s.fe.Notify(swarmBandStatus())
	s.await("swarm · 2 workers")
	rows := screenAt(t, s, 60, 20)
	if len(rows) != len(base) {
		t.Fatalf("a band's start must not grow the footer: %d rows, base %d:\n%q", len(rows), len(base), rows)
	}

	s.fe.Notify(core.SwarmStatus{
		Workers: []core.SwarmWorker{
			{ID: 2, Role: "worker", Task: "t389", Heartbeat: time.Now().Add(-12 * time.Second), Done: 2, Failed: 1, State: "running", Tool: "read x", ToolAt: time.Now()},
			{ID: 3, Role: "reviewer", Task: "t387", Heartbeat: time.Now(), State: "running"},
		},
		Pending: 5,
		Review:  2,
	})
	breathe(t, s, emberBreathStops)
	s.await("#2 read x · 0s")
	rows = screenAt(t, s, 60, 20)
	if len(rows) != len(base) {
		t.Fatalf("the call item must not grow the footer: %d rows, base %d", len(rows), len(base))
	}

	s.fe.Notify(core.SwarmStatus{
		Workers: []core.SwarmWorker{
			{ID: 2, Role: "worker", State: "exited", Done: 2, Failed: 1},
			{ID: 3, Role: "reviewer", State: "exited"},
		},
	})
	deadline := time.Now().Add(3 * time.Second)
	for {
		rows = screenAt(t, s, 60, 20)
		if len(rows) == len(base) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the footer left its base height: %d rows, base %d:\n%q", len(rows), len(base), rows)
		}
		time.Sleep(2 * time.Millisecond)
	}
	if got := paintFree(strings.Join(rows, "\n")); strings.Contains(got, "w2") && !strings.Contains(got, "read x") {
		t.Fatalf("the row lost its held item:\n%s", got)
	}
}

func TestTheSwarmAndTheDelegateStartsBreatheInOrder(t *testing.T) {
	th := oledTheme(t)
	s := newScriptedSession(t, th, WithWidth(60), WithSize(sizeFixture(60, 20)),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
	)
	if got := s.prompt(promptMark(th), "go\n"); got != "go" {
		t.Fatalf("prompt = %q", got)
	}

	s.fe.Notify(core.SwarmStatus{
		Workers: []core.SwarmWorker{
			{ID: 2, Role: "worker", Task: "t388", Heartbeat: time.Now(), State: "running"},
		},
		Pending: 3,
	})
	s.fe.Notify(core.SwarmStatus{Workers: []core.SwarmWorker{
		{ID: 1, Role: "delegate", Task: "t1", Heartbeat: time.Now(), State: "running"},
	}})
	s.await("swarm · 1 worker")
	breathe(t, s, emberBreathStops)
	s.await("delegating · 1 worker")

	s.fe.Notify(core.SwarmStatus{Workers: []core.SwarmWorker{
		{ID: 2, Role: "worker", State: "exited", Done: 1},
	}})
	time.Sleep(100 * time.Millisecond)
	if got := paintFree(strings.Join(screenAt(t, s, 60, 20), "\n")); !strings.Contains(got, "delegating · 1 worker") && !strings.Contains(got, "swarm · 1 worker") {
		t.Fatalf("the row lost both starts:\n%s", got)
	}
}

func (s *scriptedSession) screenUntil(t *testing.T, w, h int, within time.Duration, want func(string) bool, fail string) string {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		joined := paintFree(strings.Join(screenAt(t, s, w, h), "\n"))
		if want(joined) {
			return joined
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s:\n%s", fail, joined)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestTheRowSurvivesAResizeWithoutTornRows(t *testing.T) {
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
	s.await("swarm · 2 workers")
	time.Sleep(20 * time.Millisecond)

	v := newVTScreen(50, 14)
	chunks := s.out.writeChunks()
	painted := 0
	for ; painted < len(chunks); painted++ {
		v.feed([]byte(chunks[painted]))
	}
	checkViewportInvariants(t, "band pre-resize", v, "swarm · 2 workers")

	size.set(36, 10)
	v.width = 36
	winch <- struct{}{}
	time.Sleep(30 * time.Millisecond)
	chunks = s.out.writeChunks()
	for ; painted < len(chunks); painted++ {
		v.feed([]byte(chunks[painted]))
	}
	checkViewportInvariants(t, "band resize", v, "swarm · 2 workers")
	joined := paintFree(strings.Join(v.rows, "\n"))
	if !strings.Contains(joined, "····") || !strings.Contains(joined, "swarm · 2 workers") {
		t.Fatalf("the row or its rule vanished on the resize:\n%s", joined)
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
	if !strings.Contains(joined, "····") || !strings.Contains(joined, "swarm · 2 workers") {
		t.Fatalf("the freeze harness lost the row:\n%s", joined)
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

func TestANoticeBreathesOnTheRowThenHoldsDimmed(t *testing.T) {
	th := oledTheme(t)
	s := idleSession(t, th)
	s.fe.Notify(evt.Notice{Source: "decision", Text: "review: fire: refused", Level: evt.LevelError})
	awaitScreen(t, s, "decision: review: fire: refused", true)
	if n := screenHas(t, s, "decision: review: fire: refused"); n != 1 {
		t.Fatalf("the notice paints one row while it breathes, got %d", n)
	}
	breathe(t, s, emberBreathStops)
	awaitScreen(t, s, "decision: review: fire: refused", true)
	deadline := time.Now().Add(3 * time.Second)
	for {
		s.fe.mu.Lock()
		live, running, hold := s.fe.bandLive, s.fe.tickStop != nil, s.fe.bandHold.text
		s.fe.mu.Unlock()
		if !live && !running && hold == "decision: review: fire: refused" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("after the breath the row holds and the ticker stops: live=%v ticker=%v hold=%q", live, running, hold)
		}
		time.Sleep(time.Millisecond)
	}
	if !strings.Contains(s.out.String(), th.Paint(SlotDim, "decision: review: fire: refused")) {
		t.Fatalf("the hold is not dim:\n%s", s.out.String())
	}
}

func TestNoticesShowInOrderAndADuplicateCollapses(t *testing.T) {
	th := oledTheme(t)
	s := idleSession(t, th)
	first := evt.Notice{Source: "swarm", Text: "w1 died — t1 restarted", Level: evt.LevelError}
	second := evt.Notice{Source: "swarm", Text: "/swarm exited — 2 workers stopped", Level: evt.LevelSuccess}
	s.fe.Notify(first)
	s.fe.Notify(first)
	s.fe.Notify(second)
	s.fe.mu.Lock()
	queued, hold := len(s.fe.bandQueue), s.fe.bandHold.text
	s.fe.mu.Unlock()
	if queued != 1 || hold != "swarm: w1 died — t1 restarted" {
		t.Fatalf("a duplicate collapses against the breathing hold: queued %d, hold %q", queued, hold)
	}
	awaitScreen(t, s, "swarm: w1 died — t1 restarted", true)
	if screenHas(t, s, "/swarm exited") != 0 {
		t.Fatal("the second notice waits for the first breath")
	}
	breathe(t, s, emberBreathStops)
	awaitScreen(t, s, "swarm: /swarm exited — 2 workers stopped", true)
	breathe(t, s, emberBreathStops)
	awaitScreen(t, s, "swarm: /swarm exited — 2 workers stopped", true)
	if n := screenHas(t, s, "w1 died"); n != 0 {
		t.Fatalf("nothing commits to the transcript, found %d rows", n)
	}
}

func TestANoticeBreathesInItsLevelsSlot(t *testing.T) {
	th := oledTheme(t)
	s := idleSession(t, th)
	text := "swarm: /swarm exited — 1 worker stopped"
	s.fe.Notify(evt.Notice{Source: "swarm", Text: "/swarm exited — 1 worker stopped", Level: evt.LevelSuccess})
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
	breathe(t, s, 1)
	awaitScreen(t, s, "folding the older turns into one", true)
	s.fe.Notify(core.Compacted{Summary: "s", Dropped: 1200, Kept: 400, Usage: core.Usage{Prompt: 2000, Completion: 1000}})
	awaitScreen(t, s, "compact: -1.2k kept 400 · up 2.0k down 1.0k", true)
	if n := screenHas(t, s, "folding the older turns"); n != 0 {
		t.Fatalf("the summarizing preview left %d rows in the record, want 0:\n%s", n,
			strings.Join(screenLines(t, s, 60), "\n"))
	}
}
