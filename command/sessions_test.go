package command_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/v2/command"
	"github.com/mrsirg97-rgb/rig/v2/core"
)

var (
	t1 = time.Now().Add(-2 * time.Hour)
	t2 = time.Now().Add(-5 * time.Hour)
	t3 = time.Now().Add(-26 * time.Hour)
)

var listRows = []command.SessionRow{
	{ID: "01j3c4x9ab12", Started: t1, Exit: "open", Turns: 3, Current: true},
	{ID: "01j3c2f7cd01", Started: t2, Exit: "ok", Turns: 12},
	{ID: "01j3b19eaa55", Started: t3, Exit: "fault", Turns: 1},
}

func TestSessionsList(t *testing.T) {
	byName := allByName(t)
	env := &command.Env{
		SessionList: func(ctx context.Context) ([]command.SessionRow, error) { return listRows, nil },
	}
	out, err := byName["sessions"].Run(context.Background(), "", env)
	if err != nil {
		t.Fatal(err)
	}
	want := "3 sessions · current 01j3c4x9ab12\n" +
		"  01j3c4x9ab12 [~] 3 turns · 0 tokens · started 2h ago · exit open\n" +
		"  01j3c2f7cd01 [x] 12 turns · 0 tokens · started 5h ago · exit ok\n" +
		"  01j3b19eaa55 [!] 1 turn · 0 tokens · started 1d ago · exit fault"
	if out != want {
		t.Fatalf("the list lines must be exact:\ngot:\n%s\nwant:\n%s", out, want)
	}
}

func TestSessionsListFitsTheScreenAndNamesTheRest(t *testing.T) {
	byName := allByName(t)
	rows := make([]command.SessionRow, 12)
	for i := range rows {
		rows[i] = command.SessionRow{ID: fmt.Sprintf("s%02d", i), Started: t1, Exit: "ok", Turns: 1}
	}
	env := &command.Env{
		SessionList: func(ctx context.Context) ([]command.SessionRow, error) { return rows, nil },
		Lines:       func() int { return 6 },
	}
	out, err := byName["sessions"].Run(context.Background(), "", env)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(out, "\n")
	if len(lines) != 6 || lines[0] != "12 sessions" || lines[5] != "· 8 more · sessions list all" {
		t.Fatalf("six screen lines leave four rows under the head and the footer names the rest:\n%s", out)
	}
	for _, args := range []string{"list all", "list 12"} {
		out, err = byName["sessions"].Run(context.Background(), args, env)
		if err != nil || strings.Count(out, "\n") != 12 || strings.Contains(out, "more") {
			t.Fatalf("%q must list every row with no footer, got (%d lines, %v)", args, strings.Count(out, "\n")+1, err)
		}
	}
	out, err = byName["sessions"].Run(context.Background(), "list 3", env)
	if err != nil || !strings.HasSuffix(out, "· 9 more · sessions list all") || strings.Count(out, "\n") != 4 {
		t.Fatalf("list 3 shows three rows and names the nine hidden, got (%q, %v)", out, err)
	}
	if _, err = byName["sessions"].Run(context.Background(), "list x", env); err == nil ||
		err.Error() != `sessions: list: "x" is not a count (all, or a positive number)` {
		t.Fatalf("a bad count must refuse by name, got %v", err)
	}
	env.Lines = nil
	out, _ = byName["sessions"].Run(context.Background(), "", env)
	if strings.Contains(out, "more") {
		t.Fatalf("no screen (the piped frontends) means no cap:\n%s", out)
	}
}

func TestSessionsListNone(t *testing.T) {
	byName := allByName(t)
	env := &command.Env{
		SessionList: func(ctx context.Context) ([]command.SessionRow, error) { return nil, nil },
	}
	out, err := byName["sessions"].Run(context.Background(), "", env)
	if err != nil || out != "sessions: none" {
		t.Fatalf("an empty store must print the named line, got (%q, %v)", out, err)
	}
}

func TestSessionsSummaryVerbCallsTheTool(t *testing.T) {
	byName := allByName(t)
	var sawArgs string
	fake := fakeExecFunc(func(ctx context.Context, args json.RawMessage) (string, error) {
		sawArgs = string(args)
		return "vitals: 2 sessions, 2 turns\nmodels: local 0.16.1\nfaults: 0\ncache ratio: 46% (cache_read 140 / prompt 300)", nil
	})
	env := &command.Env{Tools: map[string]core.Tool{"sessions": fake}}
	out, err := byName["sessions"].Run(context.Background(), "summary", env)
	if err != nil {
		t.Fatal(err)
	}
	if sawArgs != `{"action":"summary"}` {
		t.Fatalf("the verb must call the tool with the summary action, got %s", sawArgs)
	}
	if !strings.Contains(out, "cache ratio") {
		t.Fatalf("the reply must pass through verbatim, got %q", out)
	}
}

func TestSessionsSummaryVerbRefusals(t *testing.T) {
	byName := allByName(t)
	if _, err := byName["sessions"].Run(context.Background(), "summary extra", &command.Env{
		Tools: map[string]core.Tool{"sessions": fakeExecFunc(func(context.Context, json.RawMessage) (string, error) { return "x", nil })},
	}); err == nil || err.Error() != "sessions: summary takes no args (sessions summary)" {
		t.Fatalf("summary with extra args must refuse the shape, got %v", err)
	}
	if _, err := byName["sessions"].Run(context.Background(), "summary", &command.Env{}); err == nil ||
		err.Error() != "sessions: no tools seam (the root did not wire one)" {
		t.Fatalf("summary without the tools seam must refuse, got %v", err)
	}
	if _, err := byName["sessions"].Run(context.Background(), "summary", &command.Env{
		Tools: map[string]core.Tool{},
	}); err == nil || err.Error() != "sessions: no sessions tool (the root did not put it in Env.Tools)" {
		t.Fatalf("summary without the sessions tool must refuse, got %v", err)
	}
}

func TestSessionsShow(t *testing.T) {
	byName := allByName(t)
	s := &core.Session{ID: "s1", Messages: []core.Message{
		{Role: core.RoleUser, Content: "fix the flaky test"},
		{Role: core.RoleAssistant, Content: "let me look", Reasoning: "the guard test is the flaky one…",
			ToolCalls: []core.ToolCall{{ID: "c7", Name: "bash", Args: raw(`go test ./middleware/`)}}},
		{Role: core.RoleTool, ToolID: "c7", Content: "ok  middleware/guard 0.4s"},
		{Role: core.RoleAssistant, Content: "fixed the race in the budget map"},
		{Role: core.RoleUser, Content: "[compaction] the older transcript, summarized\nline two of the summary"},
	}}
	env := &command.Env{
		SessionShow: func(ctx context.Context, id string) (string, error) {
			if id != "s1" {
				return "", errors.New("sessions: no such session: " + id)
			}
			return command.RenderShow(s), nil
		},
	}
	out, err := byName["sessions"].Run(context.Background(), "show s1", env)
	if err != nil {
		t.Fatal(err)
	}
	want := "session s1 · 5 messages\n" +
		"  1 user: fix the flaky test\n" +
		"  2 assistant: let me look\n" +
		"    thinking: the guard test is the flaky one…\n" +
		"    call c7 bash go test ./middleware/\n" +
		"  3 tool c7: ok  middleware/guard 0.4s\n" +
		"  4 assistant: fixed the race in the budget map\n" +
		"  5 user: [compaction] the older transcript, summarized\n" +
		"    line two of the summary"
	if out != want {
		t.Fatalf("the show render must be exact:\ngot:\n%s\nwant:\n%s", out, want)
	}
}

func TestSessionsShowRefusals(t *testing.T) {
	byName := allByName(t)
	env := &command.Env{
		SessionShow: func(ctx context.Context, id string) (string, error) {
			return "", errors.New("sessions: no such session: " + id)
		},
	}
	if _, err := byName["sessions"].Run(context.Background(), "show", env); err == nil ||
		err.Error() != "sessions: show needs an id (sessions show <id>)" {
		t.Fatalf("show without an id must refuse with the shape, got %v", err)
	}
	if _, err := byName["sessions"].Run(context.Background(), "show nope", env); err == nil ||
		err.Error() != "sessions: no such session: nope" {
		t.Fatalf("an unknown id must be loud, got %v", err)
	}
	if _, err := byName["sessions"].Run(context.Background(), "show a b", env); err == nil ||
		err.Error() != "sessions: show takes one id" {
		t.Fatalf("two ids must refuse, got %v", err)
	}
}

func TestSessionsUsage(t *testing.T) {
	byName := allByName(t)
	_, err := byName["sessions"].Run(context.Background(), "frob", &command.Env{})
	if err == nil || err.Error() != "sessions: usage: sessions [list [all|<n>]|summary|show|resume <id>]" {
		t.Fatalf("a foreign sub-verb must be the usage line, got %v", err)
	}
}

func TestSessionsResumeLiveTurn(t *testing.T) {
	byName := allByName(t)
	fs := &fakeSteer{live: true}
	touched := false
	env := &command.Env{
		Steer: fs,
		Session: func() *core.Session {
			return &core.Session{ID: "s1"}
		},
		SessionResume: func(ctx context.Context, id string) error {
			touched = true
			return nil
		},
	}
	_, err := byName["sessions"].Run(context.Background(), "resume s2", env)
	if err == nil || err.Error() != "sessions: a turn is live; steer or interrupt first" {
		t.Fatalf("a live turn must refuse, got %v", err)
	}
	if touched {
		t.Fatal("the resume must not run on a live turn")
	}
}

func TestSessionsResumeLine(t *testing.T) {
	byName := allByName(t)
	fs := &fakeSteer{slot: "queued", hasSlot: true}
	s2 := &core.Session{ID: "s2", Messages: []core.Message{
		{Role: core.RoleUser, Content: "one"},
		{Role: core.RoleAssistant, Content: "two"},
	}}
	var cur *core.Session = &core.Session{ID: "s1"}
	env := &command.Env{
		Steer:   fs,
		Session: func() *core.Session { return cur },
		SessionResume: func(ctx context.Context, id string) error {
			cur = s2
			return nil
		},
	}
	out, err := byName["sessions"].Run(context.Background(), "resume s2", env)
	if err != nil || out != "sessions: resumed s2 (2 messages)" {
		t.Fatalf("the resume line = (%q, %v), want the id and the message count", out, err)
	}
	if fs.slotHeld() {
		t.Fatal("the queued steer must be dropped on the swap")
	}
}

func raw(s string) []byte { return []byte(s) }
