package core

import "context"

type Command interface {
	Name() string
	Description() string
	Run(ctx context.Context, args string, env any) (string, error)
}
