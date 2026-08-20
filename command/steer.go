package command

import (
	"context"
	"errors"
)

type steerCmd struct{}

func (steerCmd) Name() string { return "steer" }

func (steerCmd) Description() string {
	return "queue a steering line (latest wins), or interrupt a live turn"
}

func (steerCmd) Run(ctx context.Context, args string, env any) (string, error) {
	e, err := EnvOf(env)
	if err != nil {
		return "", err
	}
	if e.Steer == nil {
		return "", errors.New("steer: no steering seam (the frontend does not support steering)")
	}
	if args == "" {
		if e.Steer.Interrupt() {
			return "steer: interrupted", nil
		}
		return "steer: no live turn", nil
	}
	if e.Steer.Steer(args) {
		return "steer: queued " + args + " · turn interrupted", nil
	}
	return "steer: queued " + args, nil
}
