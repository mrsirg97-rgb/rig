package tui

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/v2/core"
)

type phaseScreen struct {
	s       *scriptedSession
	v       *vt
	size    *mutable
	painted int
}

func newPhaseScreen(t *testing.T, th Theme, width, height int) *phaseScreen {
	t.Helper()
	sz := newMutable(width, height)
	s := newScriptedSession(t, th, WithWidth(width), WithSize(sz.get),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }))
	if got := s.prompt(promptMark(th), "go\n"); got != "go" {
		t.Fatalf("prompt = %q", got)
	}
	s.fe.Notify(core.TurnEnd{Reason: core.TurnOver})
	p := &phaseScreen{s: s, v: newVTScreen(width, height), size: sz}
	p.feed(t)
	return p
}

func (p *phaseScreen) feed(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		chunks := p.s.out.writeChunks()
		if len(chunks) > p.painted {
			for ; p.painted < len(chunks); p.painted++ {
				p.v.feed([]byte(chunks[p.painted]))
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("a frame never landed:\n%q", p.v.rows)
		}
		time.Sleep(time.Millisecond)
	}
	if p.v.err != "" {
		t.Fatalf("harness: %s", p.v.err)
	}
}

func (p *phaseScreen) drain(t *testing.T) {
	t.Helper()
	time.Sleep(50 * time.Millisecond)
	chunks := p.s.out.writeChunks()
	for ; p.painted < len(chunks); p.painted++ {
		p.v.feed([]byte(chunks[p.painted]))
	}
	if p.v.err != "" {
		t.Fatalf("harness: %s", p.v.err)
	}
}

func (p *phaseScreen) awaitOut(t *testing.T, want string) {
	t.Helper()
	waitUntil(t, "the stream to carry "+want, func() bool {
		return strings.Contains(p.s.out.String(), want)
	})
}

func (p *phaseScreen) rows() []string {
	out := make([]string, len(p.v.rows))
	for i, r := range p.v.rows {
		out[i] = paintFree(r)
	}
	return out
}

func (p *phaseScreen) say(d string) {
	p.s.fe.Notify(core.Phase{Name: "reviewing", Text: d})
	p.s.tick()
}

func countRowsContaining(rows []string, sub string) int {
	n := 0
	for _, r := range rows {
		if strings.Contains(r, sub) {
			n++
		}
	}
	return n
}

func indexOfRowContaining(rows []string, sub string) int {
	for i, r := range rows {
		if strings.Contains(r, sub) {
			return i
		}
	}
	return -1
}

func waitUntil(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out awaiting %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestPhasePreviewIsARollingTailOfScreenRows(t *testing.T) {
	th := oledTheme(t)
	p := newPhaseScreen(t, th, 60, 24)
	p.s.fe.Notify(core.Phase{Name: "reviewing"})
	p.s.tick()
	p.feed(t)
	if indexOfRowContaining(p.rows(), "reviewing · ") < 0 {
		t.Fatalf("the phase opening did not take the indicator row:\n%q", p.rows())
	}

	for i := 1; i <= 40; i++ {
		p.say(fmt.Sprintf("line %02d\n", i))
		p.feed(t)
		rows := p.rows()
		if n := countRowsContaining(rows, "line "); n > phasePreviewRows {
			t.Fatalf("after %d streamed lines the screen carries %d reasoning rows, want at most %d:\n%q",
				i, n, phasePreviewRows, rows)
		}
	}

	p.awaitOut(t, "\u00b7 30 rows above \u00b7")
	p.drain(t)
	rows := p.rows()
	if indexOfRowContaining(rows, "· 30 rows above ·") < 0 {
		t.Fatalf("forty streamed one-row lines hide thirty rows:\n%q", rows)
	}
	header := indexOfRowContaining(rows, "rows above")
	indicator := indexOfRowContaining(rows, "reviewing · ")
	if indicator < 0 || !(indicator < header) {
		t.Fatalf("the preview heads its tail under the indicator row (indicator %d, header %d):\n%q",
			indicator, header, rows)
	}
	want := make([]string, phasePreviewRows)
	for i := range want {
		want[i] = fmt.Sprintf("line %02d", 31+i)
	}
	tail := rows[header+1:]
	if len(tail) < phasePreviewRows {
		t.Fatalf("the screen holds only %d rows below the header:\n%q", len(tail), rows)
	}
	for i := range want {
		if tail[i] != want[i] {
			t.Fatalf("the tail is not the last ten streamed lines:\ngot  %q\nwant %q", tail[:phasePreviewRows], want)
		}
	}
	if rest := tail[phasePreviewRows:]; countRowsContaining(rest, "line ") != 0 {
		t.Fatalf("more than %d rows of reasoning show:\n%q", phasePreviewRows, rows)
	}
}

func TestPhaseEndCommitsTheCheckLineAndNoneOfThePreview(t *testing.T) {
	th := oledTheme(t)
	p := newPhaseScreen(t, th, 60, 24)
	p.s.fe.Notify(core.Phase{Name: "reviewing"})
	p.s.tick()
	p.feed(t)
	for i := 1; i <= 40; i++ {
		p.say(fmt.Sprintf("line %02d\n", i))
	}
	p.awaitOut(t, "\u00b7 30 rows above \u00b7")
	p.drain(t)
	rows := p.rows()
	if n := countRowsContaining(rows, "line "); n != phasePreviewRows {
		t.Fatalf("mid-phase the preview shows %d rows, want %d:\n%q", n, phasePreviewRows, rows)
	}
	if indexOfRowContaining(rows, "· 30 rows above ·") < 0 {
		t.Fatalf("forty streamed lines hide thirty rows:\n%q", rows)
	}

	p.s.fe.Notify(core.Phase{Name: "reviewing", Done: true, Ok: true, Note: "40 rows settled"})
	p.s.tick()
	p.awaitOut(t, " 40 rows settled")
	p.feed(t)

	after := p.rows()
	if n := countRowsContaining(after, "line "); n != 0 {
		t.Fatalf("the preview left %d reasoning rows behind:\n%q", n, after)
	}
	if n := countRowsContaining(after, "rows above"); n != 0 {
		t.Fatalf("the preview left %d header rows behind:\n%q", n, after)
	}
	if indexOfRowContaining(after, "✓ reviewing · 40 rows settled") < 0 {
		t.Fatalf("the check line did not land:\n%q", after)
	}
	for _, r := range p.v.hist {
		if strings.Contains(paintFree(r), "line ") {
			t.Fatalf("the scrollback carries a reasoning row: %q", paintFree(r))
		}
	}
}

func TestTheCompactionLineLandsAfterTheCheckLine(t *testing.T) {
	th := oledTheme(t)
	p := newPhaseScreen(t, th, 60, 24)

	p.s.fe.Notify(core.Phase{Name: "reviewing"})
	p.s.tick()
	p.feed(t)
	p.say("row 1 reads safe\n")
	p.feed(t)
	if countRowsContaining(p.rows(), "row 1 reads safe") != 1 {
		t.Fatalf("the reviewing thought did not preview:\n%q", p.rows())
	}
	p.s.fe.Notify(core.Phase{Name: "reviewing", Done: true, Ok: true, Note: "1 row settled"})
	p.s.tick()
	p.feed(t)

	p.s.fe.Notify(core.Compacting{})
	p.s.fe.Notify(core.Phase{Name: "summarizing"})
	p.s.tick()
	p.feed(t)
	for _, d := range []string{"fold one\n", "fold two\n"} {
		p.s.fe.Notify(core.Phase{Name: "summarizing", Text: d})
		p.s.tick()
	}
	p.awaitOut(t, "fold two")
	p.drain(t)
	if countRowsContaining(p.rows(), "fold two") != 1 {
		t.Fatalf("the summarizing thought did not preview:\n%q", p.rows())
	}
	p.s.fe.Notify(core.Compacted{Summary: "s", Dropped: 1200, Kept: 400, Usage: core.Usage{Prompt: 2000, Completion: 1000}})
	p.s.tick()
	p.awaitOut(t, "compact: -1.2k kept 400")
	p.drain(t)

	after := p.rows()
	check := indexOfRowContaining(after, "✓ reviewing · 1 row settled")
	line := indexOfRowContaining(after, "compact: -1.2k kept 400")
	if check < 0 || line < 0 || check > line {
		t.Fatalf("the compaction line must land after the check line (check %d, line %d):\n%q", check, line, after)
	}
	if n := countRowsContaining(after, "fold "); n != 0 {
		t.Fatalf("the summarizing preview leaked into the record:\n%q", after)
	}
	for _, r := range p.v.hist {
		plain := paintFree(r)
		if strings.Contains(plain, "fold ") || strings.Contains(plain, "row 1 reads safe") {
			t.Fatalf("the scrollback carries a previewed reasoning row: %q", plain)
		}
	}
}

func TestReasoningToggleOffShowsTheRowAndNoPreview(t *testing.T) {
	th := oledTheme(t)
	p := newPhaseScreen(t, th, 60, 24)
	p.s.si.feed("\x14")
	waitUntil(t, "the reasoning toggle to flip off", func() bool {
		p.s.fe.mu.Lock()
		defer p.s.fe.mu.Unlock()
		return !p.s.fe.showReasoning
	})
	p.s.fe.Notify(core.Phase{Name: "reviewing"})
	p.s.tick()
	p.feed(t)
	for i := 1; i <= 12; i++ {
		p.say(fmt.Sprintf("line %02d\n", i))
	}
	p.drain(t)
	rows := p.rows()
	if indexOfRowContaining(rows, "reviewing · ") < 0 {
		t.Fatalf("the toggle off must not hide the indicator row:\n%q", rows)
	}
	if n := countRowsContaining(rows, "line "); n != 0 {
		t.Fatalf("the toggle off shows %d preview rows, want 0:\n%q", n, rows)
	}
	if n := countRowsContaining(rows, "rows above"); n != 0 {
		t.Fatalf("the toggle off shows %d header rows, want 0:\n%q", n, rows)
	}
}

func TestPhasePreviewSurvivesATerminalOneRowTall(t *testing.T) {
	th := oledTheme(t)
	p := newPhaseScreen(t, th, 40, 1)
	p.s.fe.Notify(core.Phase{Name: "reviewing"})
	p.s.tick()
	p.feed(t)
	for i := 1; i <= 15; i++ {
		p.say(fmt.Sprintf("line %02d of reasoning that is somewhat long\n", i))
	}
	p.feed(t)
	p.s.fe.Notify(core.Phase{Name: "reviewing", Done: true, Ok: true, Note: "15 rows settled"})
	p.s.tick()
	p.feed(t)
}

func TestPhasePreviewReMeasuresOnResize(t *testing.T) {
	th := oledTheme(t)
	p := newPhaseScreen(t, th, 60, 24)
	p.s.fe.Notify(core.Phase{Name: "reviewing"})
	p.s.tick()
	p.feed(t)
	lineOf := func(i int) string {
		return fmt.Sprintf("line %02d - %s", i, strings.Repeat("word ", 10))
	}
	for i := 1; i <= 40; i++ {
		p.say(lineOf(i) + "\n")
	}
	p.awaitOut(t, "\u00b7 30 rows above \u00b7")
	p.drain(t)
	if indexOfRowContaining(p.rows(), "· 30 rows above ·") < 0 {
		t.Fatalf("at width 60 forty one-row lines hide thirty rows:\n%q", p.rows())
	}

	p.size.set(20, 24)
	p.v = resizeVT(p.v, 20, 24)
	p.say(lineOf(41) + "\n")
	p.awaitOut(t, "\u00b7 53 rows above \u00b7")
	p.drain(t)

	rows := p.rows()
	header := indexOfRowContaining(rows, "rows above")
	if header < 0 {
		t.Fatalf("the resized preview lost its header:\n%q", rows)
	}
	visible := 0
	for _, r := range rows[header+1:] {
		if r == "" || strings.Contains(r, "❯") {
			break
		}
		if displayWidth(r) > 20 {
			t.Fatalf("a preview row overflows the resized width: %q", r)
		}
		visible++
	}
	if visible != phasePreviewRows {
		t.Fatalf("the resized preview shows %d rows, want %d:\n%q", visible, phasePreviewRows, rows)
	}
	if indexOfRowContaining(rows, "· 53 rows above ·") < 0 {
		t.Fatalf("the header must carry the thirty rows hidden at width 60, the three rows of the line dropped at width 20 and the twenty kept rows above the tail:\n%q", rows)
	}
}

func BenchmarkFramePaint100kPhaseParagraph(b *testing.B) {
	th, err := ResolveTheme("oled", nil, true)
	if err != nil {
		b.Fatal(err)
	}
	const width, height = 100, 30
	fe := New(newScriptInput(), io.Discard, th,
		WithWidth(width), WithSize(sizeFixture(width, height)),
		WithStatus(func(ctx context.Context) StatusIn { return statusFixture() }),
	).(*tui)
	defer fe.Close()

	fe.Notify(core.Phase{Name: "reviewing"})
	words := []string{"alpha", "beta", "gamma", "delta", "eps", "zeta"}
	for total := 0; total < 1250; total++ {
		line := fmt.Sprintf("%d ", total)
		for n := 0; n < 14; n++ {
			line += words[(total+n)%len(words)] + " "
		}
		fe.Notify(core.Phase{Name: "reviewing", Text: line + "\n"})
	}

	paint := func() {
		fe.mu.Lock()
		fe.paintLiveLocked()
		fe.mu.Unlock()
	}
	b.ResetTimer()
	start := time.Now()
	for i := 0; i < b.N; i++ {
		paint()
	}
	per := time.Since(start) / time.Duration(b.N)
	if per > time.Millisecond {
		b.Fatalf("a frame over a phase that thought 100k characters costs %s, want under 1ms", per)
	}
}
