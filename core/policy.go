package core

import "context"

type ContextPolicy interface {
	Assemble(ctx context.Context, s *Session) ([]Message, error)
}
