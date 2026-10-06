package command_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/v2/command"
)

func TestDecisionTrainHandsTheTrainerToTheSeam(t *testing.T) {
	byName := allByName(t)
	got := ""
	env := &command.Env{DecisionTrain: func(ctx context.Context, trainer string) (string, error) {
		got = trainer
		return "decision: enqueued the laya run (j42)", nil
	}}
	out, err := byName["decision"].Run(context.Background(), "train laya", env)
	if err != nil || got != "laya" || !strings.Contains(out, "enqueued") {
		t.Fatalf("train = (%q, %v), trainer %q", out, err, got)
	}
}

func TestDecisionTrainRefuses(t *testing.T) {
	byName := allByName(t)
	if _, err := byName["decision"].Run(context.Background(), "train laya", &command.Env{}); err == nil || !strings.Contains(err.Error(), "no training seam") {
		t.Fatalf("no seam refuses loudly, got %v", err)
	}
	seam := &command.Env{DecisionTrain: func(ctx context.Context, trainer string) (string, error) {
		return "enqueued " + trainer, nil
	}}
	for _, args := range []string{"", "train", "run laya", "train laya extra"} {
		if _, err := byName["decision"].Run(context.Background(), args, seam); err == nil {
			t.Fatalf("decision %q must refuse", args)
		}
	}
}
