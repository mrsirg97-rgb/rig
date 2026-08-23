package tui_test

import (
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/frontend/tui"
)

const todoReply = "→ t3 started\n" +
	"2/5 done · next: t4\n" +
	"  t1 [x] wire the models table\n" +
	"  t2 [x] the switch seam\n" +
	"  t3 [~] steer verb\n" +
	"  t4 [ ] policy test · waits on t3\n" +
	"  t5 [ ] rem check\n"

const todoReplyWithClaim = "→ t3 started\n" +
	"1/3 done · next: t2\n" +
	"  t1 [x] wire the models table\n" +
	"  t2 [~] the switch seam\n" +
	"  t3 [ ] rem check · claimed by 01a011f6\n"

const todoReplyStale = "3/3 done · next: t4\n" +
	"  t1 [x] one\n" +
	"  t2 [x] two\n" +
	"  t3 [x] three\n" +
	"· 2 unresolved since 2026-04-18 (recovered from log)\n"

const schedListReply = "/home/ng/Projects/rig:\n" +
	"j2 weekly-report active · next 2026-04-19T00:00:00Z · ok exit 0 2026-04-18T22:00:00Z\n" +
	"  cron 0 0 * * 1 · qwen3.8-workers · /home/ng/Projects/rig\n" +
	"  drift: no crontab line\n"

const schedEmptyReply = "scheduler: no jobs (global.sqlite)\n"

const schedRunsReply = "j2 · 3 runs (oldest first):\n" +
	"2026-04-16T00:00:01Z  ok  exit 0 12000ms /home/ng/.config/rig/scheduler/runs/j2-1.log\n" +
	"2026-04-17T00:00:02Z  fail  exit 1 300ms\n" +
	"2026-04-18T00:00:03Z  skip  busy: skip (worker holds the gpu)\n"

func toolDoor(th tui.Theme, opening string, reply string) string {
	return tui.RenderTodoBlock(th, opening, reply)
}

func TestTodoBlockBothDoorsByteEqualMinusOpening(t *testing.T) {
	th, err := tui.ResolveTheme("oled", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	tool := tui.RenderTodoBlock(th,
		th.Paint("accent", "●")+" "+th.Paint("accent", "todo")+th.Paint("dim", " · ")+th.Paint("text", "start t3"),
		todoReply)
	cmd := tui.RenderTodoBlock(th,
		th.Paint("dim", "/todo")+th.Paint("dim", " · ")+th.Paint("text", "start t3"),
		todoReply)
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

func TestTodoBlockExactBytes(t *testing.T) {
	th, err := tui.ResolveTheme("oled", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	got := tui.RenderTodoBlock(th, "OPEN", todoReply)

	if !strings.Contains(got, th.Paint("accent", "▰▰▰")+th.Paint("dim", "▱▱")+th.Paint("dim", " 2/5 · next t4")) {
		t.Fatalf("the progress head is missing or wrong:\n%s", got)
	}
	if !strings.Contains(got, th.Paint("success", "●")+" "+th.Paint("dim", "t1")+" "+th.Paint("text", "wire the models table")) {
		t.Fatalf("the done row is missing or wrong:\n%s", got)
	}
	if !strings.Contains(got, th.Paint("accent", "◐")+" "+th.Paint("dim", "t3")+" "+th.Paint("text", "steer verb")) {
		t.Fatalf("the in-progress row is missing or wrong:\n%s", got)
	}
	if !strings.Contains(got, th.Paint("dim", "○")+" "+th.Paint("dim", "t4")+" "+th.Paint("text", "policy test")+th.Paint("dim", " · waits on t3")) {
		t.Fatalf("the blocked row keeps its waits-on, dim:\n%s", got)
	}
}

func TestTodoBlockClaimAndStaleFooter(t *testing.T) {
	th, err := tui.ResolveTheme("oled", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	got := tui.RenderTodoBlock(th, "OPEN", todoReplyWithClaim)
	if !strings.Contains(got, th.Paint("dim", " · claimed by 01a011f6")) {
		t.Fatalf("a foreign claim stays visible, dim:\n%s", got)
	}
	got = tui.RenderTodoBlock(th, "OPEN", todoReplyStale)
	if !strings.Contains(got, th.Paint("dim", "  · 2 unresolved since 2026-04-18 (recovered from log)")) {
		t.Fatalf("the stale footer (the todo's own) commits dim:\n%s", got)
	}
}

func TestTodoBlockParseFailureDegradesToRaw(t *testing.T) {
	th, err := tui.ResolveTheme("oled", nil, true)
	if err != nil {
		t.Fatal(err)
	}

	raw := "all good, queue is healthy"
	if got := tui.RenderTodoBlock(th, "OPEN", raw); got != raw {
		t.Fatalf("an unparseable reply commits raw, got:\n%s", got)
	}

	raw = "2/2 done\n  t1 ?? text"
	if got := tui.RenderTodoBlock(th, "OPEN", raw); got != raw {
		t.Fatalf("a malformed task row degrades to raw, got:\n%s", got)
	}
}

func TestSchedulerBlockBothDoorsByteEqualMinusOpening(t *testing.T) {
	th, err := tui.ResolveTheme("oled", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, reply := range []string{schedListReply, schedRunsReply} {
		tool := tui.RenderSchedulerBlock(th,
			th.Paint("accent", "●")+" "+th.Paint("accent", "scheduler")+th.Paint("dim", " · ")+th.Paint("text", "list"),
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
