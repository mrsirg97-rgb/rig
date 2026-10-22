package command

import (
	"context"
	"errors"
	"strings"

	"github.com/mrsirg97-rgb/rig/v3/core"
)

type decisionCmd struct {
	subs func() []Sub
}

func (decisionCmd) Name() string { return "decision" }

func (decisionCmd) Description() string {
	return "the decision model: train it from rig's own settled rows"
}

func (m decisionCmd) Sub() []Sub {
	if m.subs == nil {
		return decisionSubs(nil)
	}
	return m.subs()
}

func decisionSubs(names []string) []Sub {
	if len(names) == 0 {
		return []Sub{
			{Name: "train", Desc: "enqueue a training run off the turn: train <trainer>; the nightly is the operator's cron line"},
		}
	}
	out := make([]Sub, 0, len(names))
	for _, n := range names {
		out = append(out, Sub{Name: "train " + n, Desc: "enqueue a training run off the turn"})
	}
	return out
}

func DecisionHints(cmds []core.Command, e *Env) {
	if e == nil || e.Trainers == nil {
		return
	}
	for _, c := range cmds {
		m, ok := c.(*decisionCmd)
		if !ok {
			continue
		}
		m.subs = func() []Sub { return decisionSubs(e.Trainers()) }
	}
}

const decisionUsage = "decision: usage: decision train <trainer>"

func (decisionCmd) Run(ctx context.Context, args string, env any) (string, error) {
	fields := strings.Fields(args)
	if len(fields) != 2 || fields[0] != "train" {
		return "", errors.New(decisionUsage)
	}
	e, err := envOf(env)
	if err != nil {
		return "", err
	}
	if e.DecisionTrain == nil {
		return "", errors.New("decision: no training seam (the root did not wire one)")
	}
	return e.DecisionTrain(ctx, fields[1])
}
