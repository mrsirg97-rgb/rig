package evt

import "context"

type Closure interface {
	Resolve(ctx context.Context)
}

type Func func(ctx context.Context)

func (f Func) Resolve(ctx context.Context) { f(ctx) }
