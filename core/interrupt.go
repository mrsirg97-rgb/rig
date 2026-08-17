package core

import "context"

// interruptKey carries a turn's cancel under a typed value key.
type interruptKey struct{}

// WithInterrupt threads a CancelFunc (a turn's) under a typed key. A
// Frontend cannot cancel a parent from a child reference, so the CancelFunc
// is the interrupt handle, not the context; it rides the ctx exactly as the
// session does (the WithSession pattern). The loop threads the turn's
// cancel onto the ctx it hands to Input (SPEC_HARDENING L1); a Frontend
// holds the handle it was last given and cancels it to steer.
func WithInterrupt(ctx context.Context, cancel context.CancelFunc) context.Context {
	return context.WithValue(ctx, interruptKey{}, cancel)
}

// InterruptFrom recovers the CancelFunc threaded by WithInterrupt. A ctx
// without one (or with a foreign value) reports false: a Frontend without
// the capability is unaffected.
func InterruptFrom(ctx context.Context) (context.CancelFunc, bool) {
	cancel, ok := ctx.Value(interruptKey{}).(context.CancelFunc)
	return cancel, ok
}
