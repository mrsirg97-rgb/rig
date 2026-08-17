package command_test

import (
	"context"
	"testing"

	"github.com/mrsirg97-rgb/rig/command"
)

// TestSteerNoSeam (SPEC_COMMANDS, named): a dispatcher that did not
// fill Env.Steer refuses loud — the compat shape: a Frontend without
// steering simply never gets the command to work.
func TestSteerNoSeam(t *testing.T) {
	byName := allByName(t)
	_, err := byName["steer"].Run(context.Background(), "fix it", &command.Env{})
	if err == nil || err.Error() != "steer: no steering seam (the frontend does not support steering)" {
		t.Fatalf("a missing seam must refuse loud, got %v", err)
	}
}

// TestSteerQueuesAndReports (SPEC_COMMANDS 7): the text is queued
// (latest wins, the seam's contract) and the interrupt's landing is
// reported on the line.
func TestSteerQueuesAndReports(t *testing.T) {
	byName := allByName(t)

	fs := &fakeSteer{}
	env := &command.Env{Steer: fs}
	out, err := byName["steer"].Run(context.Background(), "fix it", env)
	if err != nil || out != "steer: queued fix it" {
		t.Fatalf("a quiet steer = (%q, %v)", out, err)
	}
	if len(fs.steered) != 1 || fs.steered[0] != "fix it" {
		t.Fatalf("the text must be queued verbatim, got %v", fs.steered)
	}

	fs = &fakeSteer{live: true}
	env = &command.Env{Steer: fs}
	out, err = byName["steer"].Run(context.Background(), "fix  it", env)
	if err != nil || out != "steer: queued fix  it · turn interrupted" {
		t.Fatalf("a live-turn steer = (%q, %v)", out, err)
	}
	if len(fs.steered) != 1 || fs.steered[0] != "fix  it" {
		t.Fatalf("the text must be queued verbatim (the interior spaces kept), got %v", fs.steered)
	}
}

// TestSteerEmptyInterrupts (SPEC_COMMANDS 7): empty steer interrupts
// only — no text is queued — and reports the landing.
func TestSteerEmptyInterrupts(t *testing.T) {
	byName := allByName(t)

	fs := &fakeSteer{live: true, slot: "earlier", hasSlot: true}
	env := &command.Env{Steer: fs}
	out, err := byName["steer"].Run(context.Background(), "", env)
	if err != nil || out != "steer: interrupted" {
		t.Fatalf("an empty steer on a live turn = (%q, %v)", out, err)
	}
	if !fs.slotHeld() || fs.slot != "earlier" {
		t.Fatalf("an empty steer must queue no text and keep the earlier slot, got %q", fs.slot)
	}
	if fs.interrupts != 1 {
		t.Fatalf("an empty steer must interrupt exactly once, got %d", fs.interrupts)
	}

	fs = &fakeSteer{}
	env = &command.Env{Steer: fs}
	out, err = byName["steer"].Run(context.Background(), "", env)
	if err != nil || out != "steer: no live turn" {
		t.Fatalf("an empty steer at a clean boundary = (%q, %v)", out, err)
	}
}
