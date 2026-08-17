package command_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/command"
	"github.com/mrsirg97-rgb/rig/core"
)

// TestCompactUsageRefusal (SPEC_COMMANDS, named): args are refused,
// naming the shape.
func TestCompactUsageRefusal(t *testing.T) {
	byName := allByName(t)
	out, err := byName["compact"].Run(context.Background(), "extra", &command.Env{})
	if err == nil || out != "" || err.Error() != "compact: usage: compact" {
		t.Fatalf("compact extra = (%q, %v), want the usage refusal verbatim", out, err)
	}
}

// TestCompactRefusesLiveTurn (SPEC_COMMANDS, named): a dispatcher
// reporting LiveTurn true refuses; the transcript is untouched (the fake
// Compact is never called), no event, no row.
func TestCompactRefusesLiveTurn(t *testing.T) {
	byName := allByName(t)
	fs := &fakeSteer{live: true}
	called := false
	s := &core.Session{ID: "s1", Messages: []core.Message{{Role: core.RoleUser, Content: "x"}}}
	env := &command.Env{
		Steer:   fs,
		Session: func() *core.Session { return s },
		Compact: func(ctx context.Context) (core.Compacted, bool, error) {
			called = true
			return core.Compacted{}, true, nil
		},
	}
	out, err := byName["compact"].Run(context.Background(), "", env)
	if err == nil || err.Error() != "compact: a turn is live; steer or interrupt first" {
		t.Fatalf("a live turn must refuse, got (%q, %v)", out, err)
	}
	if called {
		t.Fatal("the compact action must not run on a live turn (the transcript is untouched)")
	}
	if len(s.Messages) != 1 {
		t.Fatal("the transcript must be untouched by the refusal")
	}
}

// TestCompactNothingToDrop (SPEC_COMMANDS, named): the action reports
// nothing to drop — the named line, no event (the caller delivered none).
func TestCompactNothingToDrop(t *testing.T) {
	byName := allByName(t)
	env := &command.Env{
		Compact: func(ctx context.Context) (core.Compacted, bool, error) {
			return core.Compacted{}, false, nil
		},
	}
	out, err := byName["compact"].Run(context.Background(), "", env)
	if err != nil || out != "compact: nothing to drop" {
		t.Fatalf("nothing to drop = (%q, %v), want the named line", out, err)
	}
}

// TestCompactRefusedIsVerbatim (SPEC_COMMANDS, named): the action's
// error is the command's output, verbatim — the command owns its voice.
func TestCompactRefusedIsVerbatim(t *testing.T) {
	byName := allByName(t)
	voice := "compact: local: the summary input alone does not fit the window: window 65536, estimate 71000"
	env := &command.Env{
		Compact: func(ctx context.Context) (core.Compacted, bool, error) {
			return core.Compacted{}, false, errors.New(voice)
		},
	}
	out, err := byName["compact"].Run(context.Background(), "", env)
	if err == nil || !strings.HasSuffix(err.Error(), voice) {
		t.Fatalf("the refusal must pass through verbatim, got (%q, %v)", out, err)
	}
}

// TestCompactDeliveredEventIsTheOutput (SPEC_COMMANDS 3): on a compact
// the command prints no second line — the event's delivery (the ⧉ line,
// rendered by the frontend) is the output, so Run returns no reply text.
func TestCompactDeliveredEventIsTheOutput(t *testing.T) {
	byName := allByName(t)
	env := &command.Env{
		Compact: func(ctx context.Context) (core.Compacted, bool, error) {
			return core.Compacted{Summary: "[compaction] s", Dropped: 12000, Kept: 16000}, true, nil
		},
	}
	out, err := byName["compact"].Run(context.Background(), "", env)
	if err != nil || out != "" {
		t.Fatalf("a compacted run prints the event line only (the closure delivered it), got (%q, %v)", out, err)
	}
}
