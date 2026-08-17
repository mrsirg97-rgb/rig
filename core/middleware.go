package core

import "context"

// ToolExec executes one tool call and reports its result. A non-nil error
// marks a fed-back failure: the result string is what the model sees, and
// the error is what bounds and accounts for the attempt.
type ToolExec func(ctx context.Context, call ToolCall) (string, error)

// ToolMiddleware wraps ToolExec, the http.Handler shape. Permissions,
// retry guards, timeouts, and logging are all this one seam, composed in
// listed order at the root: first-listed is innermost. Widened from a
// function type to an interface (SPEC_HARDENING decision 6): participants
// that wrap only adapt through ToolMiddlewareFunc; optional capabilities
// are checked by assertion at the loop and the root.
type ToolMiddleware interface {
	Wrap(next ToolExec) ToolExec
}

// ToolMiddlewareFunc adapts a plain function to the seam (the
// http.HandlerFunc shape), for participants that wrap only.
type ToolMiddlewareFunc func(next ToolExec) ToolExec

func (f ToolMiddlewareFunc) Wrap(next ToolExec) ToolExec { return f(next) }

// TurnObserver is an optional capability: a participant that resets
// per-turn state (the retry guard's budget) at the turn boundary. The loop
// fans out TurnStart at every turn start (SPEC_HARDENING L6).
type TurnObserver interface {
	TurnStart(ctx context.Context, s *Session)
}

// GuidelineContributor is an optional capability: a participant that adds
// system-prompt prose. The root collects Guidelines() into the system
// string the policy receives (SPEC_HARDENING decision 6).
type GuidelineContributor interface {
	Guidelines() string
}
