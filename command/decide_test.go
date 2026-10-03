package command_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/v2/command"
)

func TestDecideBareCountsPending(t *testing.T) {
	byName := allByName(t)
	env := &command.Env{PendingReviews: func(ctx context.Context) (int, error) { return 3, nil }}
	out, err := byName["decide"].Run(context.Background(), "", env)
	if err != nil || !strings.Contains(out, "3") || !strings.Contains(out, "review") {
		t.Fatalf("bare = (%q, %v), want the count and the door", out, err)
	}
}

func TestDecideBareWithNoReviewerRefuses(t *testing.T) {
	byName := allByName(t)
	_, err := byName["decide"].Run(context.Background(), "", &command.Env{})
	if err == nil || !strings.Contains(err.Error(), "no reviewer") {
		t.Fatalf("no seam refuses by name: %v", err)
	}
}

func TestDecideReviewCallsTheSeam(t *testing.T) {
	byName := allByName(t)
	fired := 0
	env := &command.Env{Review: func(ctx context.Context) (string, error) {
		fired++
		return "the review fire is running", nil
	}}
	out, err := byName["decide"].Run(context.Background(), "review", env)
	if err != nil || fired != 1 || out != "the review fire is running" {
		t.Fatalf("review = (%q, %v), %d fires", out, err, fired)
	}
}

func TestDecideReviewWithNoReviewerRefuses(t *testing.T) {
	byName := allByName(t)
	_, err := byName["decide"].Run(context.Background(), "review", &command.Env{})
	if err == nil || !strings.Contains(err.Error(), "no reviewer") {
		t.Fatalf("no seam refuses by name: %v", err)
	}
}

func TestDecideRefusesAnUnknownSub(t *testing.T) {
	byName := allByName(t)
	_, err := byName["decide"].Run(context.Background(), "now", &command.Env{})
	if err == nil || !strings.Contains(err.Error(), "usage") {
		t.Fatalf("an unknown sub refuses with the usage: %v", err)
	}
}
