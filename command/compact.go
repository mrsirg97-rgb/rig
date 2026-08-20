package command

import (
	"context"
	"errors"
)

type compactCmd struct{}

func (compactCmd) Name() string { return "compact" }

func (compactCmd) Description() string {
	return "force a compaction of the current transcript (the Compacted line, or 'nothing to drop')"
}

func (compactCmd) Run(ctx context.Context, args string, env any) (string, error) {
	if args != "" {
		return "", errors.New("compact: usage: compact")
	}
	e, err := EnvOf(env)
	if err != nil {
		return "", err
	}
	if liveTurn(e) {
		return "", errors.New("compact: a turn is live; steer or interrupt first")
	}
	if e.Compact == nil {
		return "", errors.New("compact: no compact seam (the root did not wire one)")
	}
	_, compacted, err := e.Compact(ctx)
	if err != nil {
		return "", err
	}
	if !compacted {
		return "compact: nothing to drop", nil
	}
	return "", nil
}
