package command_test

import (
	"context"
	"testing"

	"github.com/mrsirg97-rgb/rig/command"
)

func TestNewUsageRefusal(t *testing.T) {
	byName := allByName(t)
	_, err := byName["new"].Run(context.Background(), "extra", &command.Env{})
	if err == nil || err.Error() != "new: usage: new" {
		t.Fatalf("new extra must refuse with the usage line, got %v", err)
	}
}

// TestNewRefusesLiveTurn (SPEC_COMMANDS, named): a dispatcher reporting
// LiveTurn true refuses; the swap never happens.
func TestNewRefusesLiveTurn(t *testing.T) {
	byName := allByName(t)
	fs := &fakeSteer{live: true}
	swapped := false
	env := &command.Env{
		Steer: fs,
		NewSession: func(ctx context.Context) (string, error) {
			swapped = true
			return "s2", nil
		},
	}
	_, err := byName["new"].Run(context.Background(), "", env)
	if err == nil || err.Error() != "new: a turn is live; steer or interrupt first" {
		t.Fatalf("a live turn must refuse, got %v", err)
	}
	if swapped {
		t.Fatal("the swap must not happen on a refused new")
	}
}

// TestNewSuccessLineAndSlotClear (SPEC_COMMANDS 4): the fresh id on the
// line, and the queued steer dropped — a steer queued for the old
// session is not delivered into the new one.
func TestNewSuccessLineAndSlotClear(t *testing.T) {
	byName := allByName(t)
	fs := &fakeSteer{slot: "queued steer", hasSlot: true}
	env := &command.Env{
		Steer: fs,
		NewSession: func(ctx context.Context) (string, error) {
			return "s2-fresh", nil
		},
	}
	out, err := byName["new"].Run(context.Background(), "", env)
	if err != nil || out != "new: session s2-fresh" {
		t.Fatalf("the success line = (%q, %v), want the fresh id on the line", out, err)
	}
	if fs.slotHeld() {
		t.Fatal("the queued steer must be dropped: a new session does not inherit it")
	}
	if fs.cleared != 1 {
		t.Fatalf("the slot must be cleared exactly once, got %d", fs.cleared)
	}
}

// TestNewRefusedCloseKeepsTheCurrent (SPEC_COMMANDS 4): a refused close
// (store fault) is loud and the swap does not happen — the current
// session continues.
func TestNewRefusedCloseKeepsTheCurrent(t *testing.T) {
	byName := allByName(t)
	env := &command.Env{
		NewSession: func(ctx context.Context) (string, error) {
			return "", errStoreFault
		},
	}
	out, err := byName["new"].Run(context.Background(), "", env)
	if err == nil || err.Error() != errStoreFault.Error() || out != "" {
		t.Fatalf("a refused close must be loud, got (%q, %v)", out, err)
	}
}
