package tui_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/frontend/tui"
)

const schedListReply = "/home/ng/Projects/rig:\n" +
	"j2 weekly-report active · next 2026-04-19T00:00:00Z · ok exit 0 2026-04-18T22:00:00Z\n" +
	"  cron 0 0 * * 1 · qwen3.8-workers · /home/ng/Projects/rig\n" +
	"  drift: no crontab line\n"

const schedEmptyReply = "scheduler: no jobs (global.sqlite)\n"

const schedRunsReply = "j2 · 3 runs (oldest first):\n" +
	"2026-04-16T00:00:01Z  ok  exit 0 12000ms /home/ng/.config/rig/scheduler/runs/j2-1.log\n" +
	"2026-04-17T00:00:02Z  fail  exit 1 300ms\n" +
	"2026-04-18T00:00:03Z  skip  busy: skip (worker holds the gpu)\n"

func TestTodoBlockBothDoorsByteEqualMinusOpening(t *testing.T) {
	th, err := tui.ResolveTheme("oled", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, reply := range []string{newTodoFixture(t).queue(), newTodoFixture(t).completeEcho()} {
		tool := tui.RenderTodoBlock(th,
			th.Paint("ember", "●")+" "+th.Paint("ember", "todo")+th.Paint("dim", " · ")+th.Paint("text", "start t3"),
			reply)
		cmd := tui.RenderTodoBlock(th,
			th.Paint("dim", "/todo")+th.Paint("dim", " · ")+th.Paint("text", "start t3"),
			reply)
		body := func(s string) string {
			rest, _, ok := strings.Cut(s, "\n")
			if !ok {
				t.Fatal("no opening line")
			}
			_ = rest
			return s[len(rest)+1:]
		}
		if body(tool) != body(cmd) {
			t.Fatalf("the two doors differ below the opening line:\n[tool]\n%s\n[cmd]\n%s", body(tool), body(cmd))
		}
		if tool == cmd {
			t.Fatal("the opening lines must differ (the door is the difference)")
		}
	}
}

func TestTodoBlockExactBytes(t *testing.T) {
	th, err := tui.ResolveTheme("oled", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	got := tui.RenderTodoBlock(th, "OPEN", newTodoFixture(t).queue())

	if !strings.Contains(got, th.Paint("dim", "[rig] ")+th.Paint("ember", "▰▰")+th.Paint("dim", "▱▱▱")+th.Paint("dim", " 3 open · 2 of 2 finished shown · next t5")) {
		t.Fatalf("the scoped progress head is missing or wrong:\n%s", got)
	}
	if !strings.Contains(got, th.Paint("success", "●")+" "+th.Paint("dim", "t1")+" "+th.Paint("text", "wire the models table")) {
		t.Fatalf("the done row is missing or wrong:\n%s", got)
	}
	if !strings.Contains(got, th.Paint("accent", "◐")+" "+th.Paint("dim", "t3")+" "+th.Paint("text", "steer verb")) {
		t.Fatalf("the in-progress row is missing or wrong:\n%s", got)
	}
	if !strings.Contains(got, th.Paint("dim", "○")+" "+th.Paint("dim", "t4")+" "+th.Paint("text", "policy test")+th.Paint("dim", " · requires t3")) {
		t.Fatalf("the linked row keeps its requires, dim:\n%s", got)
	}
	if !strings.Contains(got, th.Paint("dim", "    · 2 notes")) {
		t.Fatalf("the note count stays visible, dim:\n%s", got)
	}
}

func TestTodoBlockClaimAndStaleFooter(t *testing.T) {
	th, err := tui.ResolveTheme("oled", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	got := tui.RenderTodoBlock(th, "OPEN", newTodoFixture(t).claim())
	if !strings.Contains(got, th.Paint("dim", " · claimed by 01a011f6")) {
		t.Fatalf("a foreign claim stays visible, dim:\n%s", got)
	}
	got = tui.RenderTodoBlock(th, "OPEN", newTodoFixture(t).stale())
	re := regexp.MustCompile(`  · [0-9]+ unresolved since [0-9]{4}-[0-9]{2}-[0-9]{2} \(recovered from log\)`)
	if !re.MatchString(tui.RemoveColor(got)) {
		t.Fatalf("the stale footer (the todo's own) commits dim:\n%s", got)
	}
}

func TestTodoBlockReview(t *testing.T) {
	th, err := tui.ResolveTheme("oled", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	got := tui.RenderTodoBlock(th, "OPEN", newTodoFixture(t).review())
	if !strings.Contains(got, th.Paint("dim", "[rig] ")+th.Paint("dim", "▱")+th.Paint("dim", " 1 open")) {
		t.Fatalf("the review queue's head is missing or wrong:\n%s", got)
	}
	if !strings.Contains(got, th.Paint("warn", "⧗")+" "+th.Paint("dim", "t1")+" "+th.Paint("text", "ready")+th.Paint("dim", " · claimed for review by sessB")) {
		t.Fatalf("the review row keeps its glyph and the review claim, dim:\n%s", got)
	}
}

func TestTodoBlockPresentRendersHeadAndHint(t *testing.T) {
	th, err := tui.ResolveTheme("oled", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	got := tui.RenderTodoBlock(th, "OPEN", newTodoFixture(t).present())
	if !strings.Contains(got, th.Paint("dim", "[rig] ")+th.Paint("ember", "▰▰▰▰▰▰▰")+th.Paint("dim", "▱")+th.Paint("dim", " 2 open · 10 of 13 finished shown · next t4")) {
		t.Fatalf("the present head is missing or wrong:\n%s", got)
	}
	if !strings.Contains(got, th.Paint("error", "✕")+" "+th.Paint("dim", "t5")+" "+th.Paint("text", "later")+th.Paint("dim", " · requires t1")) {
		t.Fatalf("a failed row is open work, marked, and stays in the block:\n%s", got)
	}
	if !strings.Contains(got, th.Paint("success", "●")+" "+th.Paint("dim", "t3")+" "+th.Paint("text", "dep3")+th.Paint("dim", " · requires t2")) {
		t.Fatalf("the nearest related hop leads the finished rows:\n%s", got)
	}
	if !strings.Contains(got, th.Paint("success", "●")+" "+th.Paint("dim", "t1")+" "+th.Paint("text", "dep1")) {
		t.Fatalf("the farthest related hop closes the chain:\n%s", got)
	}
	if !strings.Contains(got, th.Paint("success", "●")+" "+th.Paint("dim", "t15")+" "+th.Paint("text", "r10")) {
		t.Fatalf("the recent finished rows follow the chain:\n%s", got)
	}
	if !strings.Contains(got, th.Paint("dim", "  · 3 more finished · todo list finished 13")) {
		t.Fatalf("the hint names what is hidden and the door:\n%s", got)
	}
}

func TestTodoBlockFinishedListPinsTheStore(t *testing.T) {
	th, err := tui.ResolveTheme("oled", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	got := tui.RenderTodoBlock(th, "OPEN", newTodoFixture(t).finishedList())
	if !strings.Contains(got, th.Paint("dim", "[rig] ")+th.Paint("ember", "▰▰▰")+th.Paint("dim", "▱")+th.Paint("dim", " 1 open · 2 of 3 finished shown · next t4")) {
		t.Fatalf("the finished list head is missing or wrong:\n%s", got)
	}
	if !strings.Contains(got, th.Paint("success", "●")+" "+th.Paint("dim", "t3")+" "+th.Paint("text", "c")) {
		t.Fatalf("the newest finished leads:\n%s", got)
	}
	if !strings.Contains(got, th.Paint("dim", "  · 1 more finished · todo list finished 3")) {
		t.Fatalf("the finished list names its hidden rows:\n%s", got)
	}
}

func TestTodoBlockBareQueueOneDimLine(t *testing.T) {
	th, err := tui.ResolveTheme("oled", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := tui.RenderTodoBlock(th, "OPEN", newTodoFixture(t).bareQueue()); got != th.Paint("dim", "queue: rig (bound)") {
		t.Fatalf("a bare queue report prints as one dim line, got:\n%s", got)
	}
}

func TestTodoBlockUnrecognizedKeepsTheOpeningAndRendersDim(t *testing.T) {
	th, err := tui.ResolveTheme("oled", nil, true)
	if err != nil {
		t.Fatal(err)
	}

	raw := "all good, queue is healthy"
	want := "OPEN\n" + th.Paint(tui.SlotDim, raw)
	if got := tui.RenderTodoBlock(th, "OPEN", raw); got != want {
		t.Fatalf("an unparseable reply must keep the opening and go dim, got:\n%s", got)
	}

	raw = "2/2 done\n  t1 ?? text"
	want = "OPEN\n" + th.Paint(tui.SlotDim, "2/2 done") + "\n" + th.Paint(tui.SlotDim, "  t1 ?? text")
	if got := tui.RenderTodoBlock(th, "OPEN", raw); got != want {
		t.Fatalf("a malformed task row must keep the opening and go dim, got:\n%s", got)
	}
}

func TestTodoBlockEchoRendersNoteAndRowThroughTheQueuePainter(t *testing.T) {
	th, err := tui.ResolveTheme("oled", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	got := tui.RenderTodoBlock(th, "OPEN", newTodoFixture(t).completeEcho())
	if !strings.HasPrefix(got, "OPEN\n") {
		t.Fatalf("the echo must keep the opening, got:\n%s", got)
	}
	if !strings.Contains(got, th.Paint(tui.SlotDim, "\u2192 't1' auto-started and completed")) {
		t.Fatalf("the echo's note renders dim, got:\n%s", got)
	}
	if !strings.Contains(got, th.Paint(tui.SlotSuccess, "\u25cf")+" "+th.Paint(tui.SlotDim, "t1")+" "+th.Paint(tui.SlotText, "wire the models table")) {
		t.Fatalf("the echo's row must ride the queue's task-line painter, got:\n%s", got)
	}
	if !strings.Contains(got, th.Paint(tui.SlotDim, "[rig] ")+th.Paint(tui.SlotEmber, "\u25b0")+th.Paint(tui.SlotDim, " 0 open · 1 of 1 finished shown")) {
		t.Fatalf("the echo's summary renders as the head, got:\n%s", got)
	}
}

func TestTodoBlockNoteEchoRendersNoteAndRow(t *testing.T) {
	th, err := tui.ResolveTheme("oled", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	got := tui.RenderTodoBlock(th, "OPEN", newTodoFixture(t).noteEcho())
	if !strings.Contains(got, th.Paint(tui.SlotDim, "\u2192 note added to 't1'")) {
		t.Fatalf("the note echo's note renders dim, got:\n%s", got)
	}
	if !strings.Contains(got, th.Paint(tui.SlotDim, "    \u00b7 1 note")) {
		t.Fatalf("the note echo's count rides the row, got:\n%s", got)
	}
}

func TestSchedulerBlockBothDoorsByteEqualMinusOpening(t *testing.T) {
	th, err := tui.ResolveTheme("oled", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, reply := range []string{schedListReply, schedRunsReply} {
		tool := tui.RenderSchedulerBlock(th,
			th.Paint("ember", "●")+" "+th.Paint("ember", "scheduler")+th.Paint("dim", " · ")+th.Paint("text", "list"),
			reply)
		cmd := tui.RenderSchedulerBlock(th,
			th.Paint("dim", "/scheduler")+th.Paint("dim", " · ")+th.Paint("text", "list"),
			reply)
		body := func(s string) string {
			rest, _, ok := strings.Cut(s, "\n")
			if !ok {
				t.Fatal("no opening line")
			}
			return s[len(rest)+1:]
		}
		if body(tool) != body(cmd) {
			t.Fatalf("the scheduler doors differ below the opening line:\n[tool]\n%s\n[cmd]\n%s", body(tool), body(cmd))
		}
	}
}

func TestSchedulerListBlockExact(t *testing.T) {
	th, err := tui.ResolveTheme("oled", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	got := tui.RenderSchedulerBlock(th, "OPEN", schedListReply)
	if !strings.Contains(got, th.Paint("accent", "●")+" "+th.SGR("text")+"j2 weekly-report active") {
		t.Fatalf("an active job carries the accent dot:\n%s", got)
	}
	if !strings.Contains(got, th.Paint("dim", "  drift: no crontab line")+"") && !strings.Contains(got, "drift: no crontab line") {
		t.Fatalf("the drift is named:\n%s", got)
	}
	if !strings.Contains(got, th.Paint("dim", "  /home/ng/Projects/rig:")) {
		t.Fatalf("the directory section header stays dim:\n%s", got)
	}

	paused := "/home/ng:\nj1 nightly paused · at passed\n  cron once · at 2026-04-19T00:00:00Z · qwen3.8-workers · /home/ng\n"
	got = tui.RenderSchedulerBlock(th, "OPEN", paused)
	if !strings.Contains(got, th.Paint("dim", "○")+" "+th.SGR("text")+"j1 nightly paused") {
		t.Fatalf("a paused job carries the dim open circle:\n%s", got)
	}
}

func TestSchedulerEmptyListNamesTheStore(t *testing.T) {
	th, err := tui.ResolveTheme("oled", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	got := tui.RenderSchedulerBlock(th, "OPEN", schedEmptyReply)
	if !strings.Contains(got, th.Paint("dim", "  scheduler: no jobs")) {
		t.Fatalf("the empty store renders one dim line:\n%s", got)
	}
}

func TestSchedulerRunsBlock(t *testing.T) {
	th, err := tui.ResolveTheme("oled", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	got := tui.RenderSchedulerBlock(th, "OPEN", schedRunsReply)
	for _, line := range []string{
		"j2 · 3 runs (oldest first):",
		"2026-04-16T00:00:01Z  ok  exit 0 12000ms",
		"2026-04-17T00:00:02Z  fail  exit 1 300ms",
	} {
		if !strings.Contains(got, line) {
			t.Fatalf("the run line %q is missing:\n%s", line, got)
		}
	}
}
