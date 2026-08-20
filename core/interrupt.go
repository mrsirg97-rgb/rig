package core

import "context"

type interruptKey struct{}

func WithInterrupt(ctx context.Context, cancel context.CancelFunc) context.Context {
	return context.WithValue(ctx, interruptKey{}, cancel)
}

func InterruptFrom(ctx context.Context) (context.CancelFunc, bool) {
	cancel, ok := ctx.Value(interruptKey{}).(context.CancelFunc)
	return cancel, ok
}
