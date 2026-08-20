package command_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/command"
)

// TestApproveBareShows (SPEC_MODES 4, named): bare shows the active
// mode; the empty root state reads as auto.
func TestApproveBareShows(t *testing.T) {
	byName := allByName(t)
	env := &command.Env{Approve: func() string { return "" }}
	out, err := byName["approve"].Run(context.Background(), "", env)
	if err != nil || out != "approve: auto" {
		t.Fatalf("bare = (%q, %v), want the auto label", out, err)
	}
	env.Approve = func() string { return "manual" }
	out, err = byName["approve"].Run(context.Background(), "", env)
	if err != nil || out != "approve: manual" {
		t.Fatalf("bare manual = (%q, %v)", out, err)
	}
}

// TestApproveSetCallsTheSeam (SPEC_MODES 4, named): the argument sets
// through the root's closure; the reply names the very-next-call
// effect (no prefix moves — no re-prefill talk).
func TestApproveSetCallsTheSeam(t *testing.T) {
	byName := allByName(t)
	set := ""
	env := &command.Env{
		Approve:    func() string { return "auto" },
		SetApprove: func(ctx context.Context, mode string) error { set = mode; return nil },
	}
	out, err := byName["approve"].Run(context.Background(), "manual", env)
	if err != nil || out != "approve: manual (the next tool call)" || set != "manual" {
		t.Fatalf("set = (%q, %v) with mode %q", out, err, set)
	}
}

// TestApproveRefusalPassesThrough (SPEC_MODES 4, named): the root's
// refusal — manual without an ask door — is the command's reply,
// verbatim.
func TestApproveRefusalPassesThrough(t *testing.T) {
	byName := allByName(t)
	env := &command.Env{
		Approve: func() string { return "auto" },
		SetApprove: func(ctx context.Context, mode string) error {
			return errors.New("approve: manual needs an ask door (this frontend cannot ask)")
		},
	}
	_, err := byName["approve"].Run(context.Background(), "manual", env)
	if err == nil || !strings.Contains(err.Error(), "ask door") {
		t.Fatalf("the door refusal must pass through: %v", err)
	}
}

// TestApproveUsage (SPEC_MODES 4, named): two words refuse with the
// usage; the Sub hints carry the two modes.
func TestApproveUsage(t *testing.T) {
	byName := allByName(t)
	env := &command.Env{Approve: func() string { return "" }}
	if _, err := byName["approve"].Run(context.Background(), "auto manual", env); err == nil ||
		err.Error() != "approve: usage: approve [auto|manual]" {
		t.Fatalf("usage voice: %v", err)
	}
	subber, ok := byName["approve"].(interface{ Sub() []command.Sub })
	if !ok {
		t.Fatal("approve must carry Sub hints")
	}
	subs := subber.Sub()
	if len(subs) != 2 || subs[0].Name != "auto" || subs[1].Name != "manual" {
		t.Fatalf("the hints are the two modes: %+v", subs)
	}
}
