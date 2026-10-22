package command_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/v3/command"
	"github.com/mrsirg97-rgb/rig/v3/core"
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

func TestDecisionSubsCarryTheTrainerNames(t *testing.T) {
	env := &command.Env{Trainers: func() []string { return []string{"laya", "risk"} }}
	cmds := command.All()
	command.DecisionHints(cmds, env)
	subber, ok := decisionIn(cmds).(command.Subber)
	if !ok {
		t.Fatal("decision is a Subber")
	}
	subs := subber.Sub()
	if len(subs) != 2 || subs[0].Name != "train laya" || subs[1].Name != "train risk" {
		t.Fatalf("the zone's trainers are the subs: %+v", subs)
	}
	if subs[0].Desc == "" {
		t.Fatal("the row's description is the point")
	}
}

func decisionIn(cmds []core.Command) core.Command {
	for _, c := range cmds {
		if c.Name() == "decision" {
			return c
		}
	}
	return nil
}

func TestDecisionSubsFallBackToTheUsageRow(t *testing.T) {
	byName := allByName(t)
	command.DecisionHints(command.All(), &command.Env{Trainers: func() []string { return nil }})
	subs := byName["decision"].(command.Subber).Sub()
	if len(subs) != 1 || subs[0].Name != "train" || subs[0].Desc != "enqueue a training run off the turn: train <trainer>; the nightly is the operator's cron line" {
		t.Fatalf("no seam falls back to the usage row: %+v", subs)
	}
}
