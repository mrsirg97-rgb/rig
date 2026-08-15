package core

import "context"

// Event is the streaming vocabulary of a model turn. Go has no sum types;
// the sealed marker keeps the loop's switch exhaustive by convention.
// Fault terminates the stream. Providers close the channel after Done or
// Fault; a closed channel without either is a provider bug.
type Event interface{ event() }

var (
	_ Event = TextDelta{}
	_ Event = ToolCallEvent{}
	_ Event = Done{}
	_ Event = Fault{}
)

// TextDelta is an incremental fragment of assistant text.
type TextDelta struct{ Text string }

func (TextDelta) event() {}

// ToolCallEvent carries one complete tool call, accumulated by the adapter.
type ToolCallEvent struct{ Call ToolCall }

func (ToolCallEvent) event() {}

// Done ends the stream. Usage is zero when the transport reports none.
type Done struct {
	StopReason string
	Usage      Usage
}

func (Done) event() {}

// Fault ends the stream with an error. The loop surfaces it and preserves
// the session up to the last complete message.
type Fault struct{ Err error }

func (Fault) event() {}

// Usage reports token accounting for a completed turn.
type Usage struct {
	Prompt     int
	Completion int
}

// Provider streams one model turn. One method: cancellation is ctx; per-
// model tool-call wire formats are the adapter's problem, not the loop's.
type Provider interface {
	Stream(ctx context.Context, req Request) (<-chan Event, error)
}

// Request is one streamed completion over assembled messages. Tools carry
// name and schema only; execution stays in the kernel.
type Request struct {
	Messages []Message
	Tools    []ToolSpec
}
