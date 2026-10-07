package command

import (
	"context"
	"errors"
	"strings"
)

type decisionCmd struct{}

func (decisionCmd) Name() string { return "decision" }

func (decisionCmd) Description() string {
	return "the decision model: train it from rig's own settled rows"
}

func (decisionCmd) Sub() []Sub {
	return []Sub{
		{Name: "train", Desc: "enqueue a training run off the turn: train <trainer>; the nightly is the operator's cron line"},
	}
}

const decisionUsage = "decision: usage: decision train <trainer>"

func (decisionCmd) Run(ctx context.Context, args string, env any) (string, error) {
	fields := strings.Fields(args)
	if len(fields) != 2 || fields[0] != "train" {
		return "", errors.New(decisionUsage)
	}
	e, err := EnvOf(env)
	if err != nil {
		return "", err
	}
	if e.DecisionTrain == nil {
		return "", errors.New("decision: no training seam (the root did not wire one)")
	}
	return e.DecisionTrain(ctx, fields[1])
}
