package command_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/command"
	"github.com/mrsirg97-rgb/rig/core"
)

var (
	t1 = time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	t2 = time.Date(2026, 7, 9, 9, 14, 11, 0, time.UTC)
	t3 = time.Date(2026, 7, 8, 16, 2, 47, 0, time.UTC)
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
	want := "01j3c4x9ab12  started 2026-07-09T12:00:00Z  exit open   turns 3  *\n" +
		"01j3c2f7cd01  started 2026-07-09T09:14:11Z  exit ok     turns 12\n" +
		"01j3b19eaa55  started 2026-07-08T16:02:47Z  exit fault  turns 1\n"
	if out != want {
		t.Fatalf("the list lines must be exact:\ngot:\n%s\nwant:\n%s", out, want)
	}
}

func TestSessionsSubHints(t *testing.T) {
	byName := allByName(t)
	subber, ok := byName["sessions"].(interface{ Sub() []command.Sub })
	if !ok {
		t.Fatal("sessions must carry Sub hints (the TUI's menu door)")
	}
	subs := subber.Sub()
	want := []string{"list", "show", "resume"}
	if len(subs) != len(want) {
		t.Fatalf("Sub() = %d hints, want %d", len(subs), len(want))
	}
	for i, s := range subs {
		if s.Name != want[i] {
			t.Fatalf("Sub() %d = %q, want %q", i, s.Name, want[i])
		}
		if s.Desc == "" {
			t.Fatalf("Sub() %d (%s) must carry a one-liner", i, s.Name)
		}
	}
}

func TestSessionsListVerb(t *testing.T) {
	byName := allByName(t)
	env := &command.Env{
		SessionList: func(ctx context.Context) ([]command.SessionRow, error) { return listRows, nil },
	}
	out, err := byName["sessions"].Run(context.Background(), "list", env)
	if err != nil {
		t.Fatal(err)
	}
	want := "01j3c4x9ab12  started 2026-07-09T12:00:00Z  exit open   turns 3  *\n" +
		"01j3c2f7cd01  started 2026-07-09T09:14:11Z  exit ok     turns 12\n" +
		"01j3b19eaa55  started 2026-07-08T16:02:47Z  exit fault  turns 1\n"
	if out != want {
		t.Fatalf("the list verb must match the bare list:\ngot:\n%s\nwant:\n%s", out, want)
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
	want := "[1] user: fix the flaky test\n" +
		"[2] assistant: let me look\n" +
		"    thinking: the guard test is the flaky one…\n" +
		"    call c7 bash go test ./middleware/\n" +
		"[3] tool (c7): ok  middleware/guard 0.4s\n" +
		"[4] assistant: fixed the race in the budget map\n" +
		"[5] user: [compaction] the older transcript, summarized\n" +
		"line two of the summary\n"
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
	if err == nil || err.Error() != "sessions: usage: sessions [list|show|resume <id>]" {
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

func TestSessionsResumeCurrentId(t *testing.T) {
	byName := allByName(t)
	touched := false
	env := &command.Env{
		Session: func() *core.Session {
			return &core.Session{ID: "s1"}
		},
		SessionResume: func(ctx context.Context, id string) error {
			touched = true
			return nil
		},
	}
	_, err := byName["sessions"].Run(context.Background(), "resume s1", env)
	if err == nil || err.Error() != "sessions: already the current session: s1" {
		t.Fatalf("the current id must refuse, got %v", err)
	}
	if touched {
		t.Fatal("the resume must not run on the current id")
	}
}

func TestSessionsResumeUnknownIdBeforeTouch(t *testing.T) {
	byName := allByName(t)
	env := &command.Env{
		Session: func() *core.Session {
			return &core.Session{ID: "s1"}
		},
		SessionResume: func(ctx context.Context, id string) error {
			return errors.New("sessions: no such session: nope")
		},
	}
	_, err := byName["sessions"].Run(context.Background(), "resume nope", env)
	if err == nil || err.Error() != "sessions: no such session: nope" {
		t.Fatalf("an unknown id must be loud, got %v", err)
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
