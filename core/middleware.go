package core

import "context"

type ToolExec func(ctx context.Context, call ToolCall) (string, error)

type ToolMiddleware interface {
	Wrap(next ToolExec) ToolExec
}

type ToolMiddlewareFunc func(next ToolExec) ToolExec

func (f ToolMiddlewareFunc) Wrap(next ToolExec) ToolExec { return f(next) }

type TurnObserver interface {
	TurnStart(ctx context.Context, s *Session)
}

type GuidelineContributor interface {
	Guidelines() string
}
