package command

import (
	"context"
	"errors"
)

// compact forces the compaction action (decision 3): the policy's
// Compact seam over the same internal action the trigger path runs —
// split, summarize, rewrite, reflect — with the trigger bypassed on
// purpose: the trigger is the model's window math, the verb is the
// user's judgment.
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
		// the action rewrites Session.Messages; a mid-turn rewrite races
		// the loop's own read of the transcript (decision 3's named case).
		return "", errors.New("compact: a turn is live; steer or interrupt first")
	}
	if e.Compact == nil {
		return "", errors.New("compact: no compact seam (the root did not wire one)")
	}
	_, compacted, err := e.Compact(ctx)
	if err != nil {
		return "", err // the action's error, verbatim — the command owns its voice
	}
	if !compacted {
		return "compact: nothing to drop", nil
	}
	// compacted: the Compacted line, the existing CLI rendering, exactly
	// once — the root's closure delivers the event to the recorder (the
	// ⧉ line is the output); the command prints no second line.
	return "", nil
}
