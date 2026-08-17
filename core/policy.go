package core

import "context"

// ContextPolicy assembles the messages one model turn sees: the prompt and
// the transcript, and compaction. v1 ships the passthrough; deliverable 8
// (SPEC_COMPACT) lands the first non-passthrough policy: at its trigger it
// rewrites the session transcript (the one named mutation this seam
// carries; the passthrough stays pure).
type ContextPolicy interface {
	Assemble(ctx context.Context, s *Session) ([]Message, error)
}
