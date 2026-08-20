package command

import (
	"context"
	"errors"
)

type newCmd struct{}

func (newCmd) Name() string { return "new" }

func (newCmd) Description() string {
	return "close the current session ok and start a fresh one (same process)"
}

func (newCmd) Run(ctx context.Context, args string, env any) (string, error) {
	if args != "" {
		return "", errors.New("new: usage: new")
	}
	e, err := EnvOf(env)
	if err != nil {
		return "", err
	}
	if liveTurn(e) {
		return "", errors.New("new: a turn is live; steer or interrupt first")
	}
	if e.NewSession == nil {
		return "", errors.New("new: no new-session seam (the root did not wire one)")
	}
	id, err := e.NewSession(ctx)
	if err != nil {
		return "", err
	}
	if e.Steer != nil {
		e.Steer.ClearSlot()
	}
	return "new: session " + id, nil
}
