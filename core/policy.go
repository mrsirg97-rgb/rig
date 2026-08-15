package core

import "context"

// ContextPolicy assembles the messages one model turn sees: the prompt and
// the transcript, and later, compaction. v1 ships the passthrough.
type ContextPolicy interface {
	Assemble(ctx context.Context, s *Session) ([]Message, error)
}
