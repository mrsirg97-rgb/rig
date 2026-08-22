package command_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/command"
)

func recentISO() string {
	return time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339)
}

func remListEnv(rows []command.RemRow) *command.Env {
	return &command.Env{
		RemList: func(ctx context.Context) ([]command.RemRow, error) { return rows, nil },
	}
}

func TestRemListProjectThenGlobal(t *testing.T) {
	byName := allByName(t)
	env := remListEnv([]command.RemRow{
		{ID: 1, Kind: "fact", CreatedAt: recentISO(), Strength: 0.5, Content: "project note one"},
		{ID: 2, Kind: "fact", CreatedAt: recentISO(), Strength: 0.5, Content: "project note two"},
		{ID: 3, Kind: "fact", CreatedAt: recentISO(), Strength: 0.5, Content: "global note", ScopeLabel: "global"},
	})
	out, err := byName["rem"].Run(context.Background(), "", env)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("the bare rem lists one line per live memory, got %d:\n%s", len(lines), out)
	}
	if !strings.Contains(lines[0], "m1 · fact ·") || !strings.Contains(lines[0], "· 0.50 · project note one") {
		t.Fatalf("line 1 must be id · kind · age · strength · first 80 chars:\n%s", lines[0])
	}
	if !strings.Contains(lines[2], "m3") || !strings.Contains(lines[2], "global note") {
		t.Fatalf("the global rows ride last (project then global):\n%s", lines[2])
	}
}

func TestRemListVerbAndEmpty(t *testing.T) {
	byName := allByName(t)
	out, err := byName["rem"].Run(context.Background(), "list", remListEnv([]command.RemRow{
		{ID: 1, Kind: "fact", CreatedAt: recentISO(), Strength: 0.5, Content: "note"},
	}))
	if err != nil || !strings.Contains(out, "m1") {
		t.Fatalf("rem list must be the bare read, got (%q, %v)", out, err)
	}
	out, err = byName["rem"].Run(context.Background(), "", remListEnv(nil))
	if err != nil || out != "rem: no memories" {
		t.Fatalf("an empty store must print the named line, got (%q, %v)", out, err)
	}
}

func TestRemShowAndForgetAndPin(t *testing.T) {
	byName := allByName(t)
	env := &command.Env{
		RemShow: func(ctx context.Context, id int64) (command.RemRow, error) {
			if id != 3 {
				return command.RemRow{}, errors.New("rem: no such memory: 3")
			}
			return command.RemRow{ID: 3, Kind: "fact", ScopeLabel: "rig", CreatedAt: recentISO(), Strength: 0.5, Importance: 0.5, Source: "s1", Content: "the full memory content"}, nil
		},
		RemForget: func(ctx context.Context, id int64) error {
			if id != 3 {
				return errors.New("rem: no such memory: 3")
			}
			return nil
		},
		RemPin: func(ctx context.Context, id int64) error {
			if id != 3 {
				return errors.New("rem: no such memory: 3")
			}
			return nil
		},
	}
	out, err := byName["rem"].Run(context.Background(), "show m3", env)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "the full memory content") || !strings.Contains(out, "m3") {
		t.Fatalf("show must render the full row:\n%s", out)
	}
	if !strings.Contains(out, "source: s1") || !strings.Contains(out, "importance 0.50") {
		t.Fatalf("show must carry the source and importance:\n%s", out)
	}
	out, err = byName["rem"].Run(context.Background(), "forget 3", env)
	if err != nil || out != "rem: forgot m3" {
		t.Fatalf("forget = (%q, %v), want the named line", out, err)
	}
	out, err = byName["rem"].Run(context.Background(), "pin 3", env)
	if err != nil || out != "rem: pinned m3" {
		t.Fatalf("pin = (%q, %v), want the named line", out, err)
	}
}

func TestRemRefusalsByName(t *testing.T) {
	byName := allByName(t)
	env := &command.Env{
		RemShow:   func(ctx context.Context, id int64) (command.RemRow, error) { return command.RemRow{}, errors.New("rem: no such memory: 99") },
		RemForget: func(ctx context.Context, id int64) error { return errors.New("rem: no such memory: 99") },
		RemPin:    func(ctx context.Context, id int64) error { return errors.New("rem: no such memory: 99") },
	}
	cases := []struct{ args, want string }{
		{"show", "rem: show needs an id (rem show <id>)"},
		{"show a b", "rem: show takes one id"},
		{"show 99", "rem: no such memory: 99"},
		{"forget", "rem: forget needs an id (rem forget <id>)"},
		{"pin", "rem: pin needs an id (rem pin <id>)"},
		{"forget 99", "rem: no such memory: 99"},
		{"pin 99", "rem: no such memory: 99"},
		{"show abc", "rem: the id must be a memory id (m<N> or <N>)"},
		{"frob", "rem: usage: rem [list|show|forget|pin <id>]"},
	}
	for _, c := range cases {
		_, err := byName["rem"].Run(context.Background(), c.args, env)
		if err == nil || err.Error() != c.want {
			t.Fatalf("%q: got %v, want %q", c.args, err, c.want)
		}
	}
}

func TestRemSubHints(t *testing.T) {
	byName := allByName(t)
	subber, ok := byName["rem"].(interface{ Sub() []command.Sub })
	if !ok {
		t.Fatal("rem must carry Sub hints (the TUI's menu door)")
	}
	subs := subber.Sub()
	want := []string{"list", "show", "forget", "pin"}
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
