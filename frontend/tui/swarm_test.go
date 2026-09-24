package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/core"
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
	th, err := ResolveTheme("p1", []byte(`{"base":"p1","glyphs":"ascii"}`), true)
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

func TestSwarmNoticeCommitsOneLine(t *testing.T) {
	th := oledTheme(t)
	s := newScriptedSession(t, th, WithWidth(60),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
	)
	if got := s.prompt(promptMark(th), "go\n"); got != "go" {
		t.Fatalf("prompt = %q", got)
	}
	s.fe.Notify(core.SwarmNotice{Text: "swarm: t1 failed — the worker died twice"})
	s.await("swarm: t1 failed — the worker died twice")
	rows := screenLines(t, s, 60)
	count := 0
	for _, r := range rows {
		if strings.Contains(paintFree(r), "swarm: t1 failed — the worker died twice") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("the notice painted %d times, want one line:\n%q", count, rows)
	}
}
