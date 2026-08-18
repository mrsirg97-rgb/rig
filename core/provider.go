package core

import (
	"context"
	"time"
)

// Event is the streaming vocabulary of a model turn. Go has no sum types;
// the sealed marker keeps the loop's switch exhaustive by convention.
// Fault terminates the stream. Providers close the channel after Done or
// Fault; a closed channel without either is a provider bug.
type Event interface{ event() }

var (
	_ Event = TextDelta{}
	_ Event = ReasoningDelta{}
	_ Event = ToolCallEvent{}
	_ Event = Done{}
	_ Event = Fault{}
	_ Event = ToolStart{}
	_ Event = ToolResult{}
	_ Event = TurnEnd{}
	_ Event = TestEvent{}
	_ Event = Compacted{}
)

// TextDelta is an incremental fragment of assistant text.
type TextDelta struct{ Text string }

func (TextDelta) event() {}

// ReasoningDelta is an incremental fragment of the model's thinking
// (reasoning_content). Provider-stream event, in stream order; the loop
// accumulates it onto the assistant message (SPEC_HARDENING L5).
type ReasoningDelta struct{ Text string }

func (ReasoningDelta) event() {}

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
	CacheRead  int // tokens served from the server-side prompt cache (a subset of Prompt on this wire)
	CacheWrite int // zero until the transport reports one
}

// ToolStart announces that the loop is executing one tool call. Loop event:
// the provider announces intent (ToolCallEvent); the loop announces
// execution. Emitted before each exec (SPEC_HARDENING L4).
type ToolStart struct{ Call ToolCall }

func (ToolStart) event() {}

// ToolResult reports the execution of one tool call: the guarded result,
// exactly what the loop appends to the session (the bracket wraps the
// whole middleware chain). Err non-nil marks the fed-back failure; the
// Frontend and the recorder can tell outcomes apart without parsing the
// string. Duration is measured by the loop.
type ToolResult struct {
	ID       string
	Content  string
	Err      error // nil = success; the guarded result, named
	Duration time.Duration
}

func (ToolResult) event() {}

// TurnReason is how a turn ended. The loop emits it at every turn exit
// (SPEC_HARDENING L7).
type TurnReason string

const (
	TurnOver      TurnReason = "over"      // the turn completed
	TurnFault     TurnReason = "fault"     // a Fault or transport error crossed it
	TurnInterrupt TurnReason = "interrupt" // the turn context died (steering)
)

// TurnEnd closes every turn inside the run, after the turn's last other
// event. A run-context cancel ends the run, not a turn, and does not emit
// it. The recorder's rule (SPEC_HARDENING decision 4): an unlanded partial
// at any TurnEnd is a partial and is discarded.
type TurnEnd struct{ Reason TurnReason }

func (TurnEnd) event() {}

// TestEvent exists so the compat rule has a subject (SPEC_HARDENING
// decision 8): an event the loop forwards but does not accumulate, and a
// Frontend must ignore.
type TestEvent struct{ Name string }

func (TestEvent) event() {}

// Compacted announces a policy-side compaction (SPEC_COMPACT 5): the older
// transcript was rewritten into the summary it carries. Emitted at
// Assemble time (the trigger path) or on-stream between the swallowed
// fault and the retry's first event (the overflow decorator); the loop
// forwards it in its existing default. Summary is the summary row's
// content, as the transcript carries it (the marker is in it); Dropped
// and Kept are calibrated estimates in the trigger's units, so the
// operator reads them against the window; Usage is the server's own count
// for the summary call.
type Compacted struct {
	Summary string
	Dropped int
	Kept    int
	Usage   Usage
}

func (Compacted) event() {}

// Compacting announces the summary call is starting (SPEC_COMPACT 5,
// amended): at deep context its prefill can run minutes, and the silent
// gap reads as a hang. The policy emits it once per compaction, just
// before the summary call, on both doors (the trigger path and the
// forced verb); the loop forwards it in its existing default; a
// Frontend renders a phase or ignores it (the compat rule).
type Compacting struct{}

func (Compacting) event() {}

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

	// MaxTokens is the response budget the root clamps per row (SPEC_COMPACT
	// 8's request-side reserve); 0 = the provider's default (additive — a
	// provider that does not know it ignores it).
	MaxTokens int

	// ReasoningEffort is the reasoning budget the caller asks for; empty = the
	// provider's default (additive — a provider that does not know it ignores
	// it). The compact summary call sets "medium" (SPEC_COMPACT 3): it is the
	// one call whose thinking nobody reads.
	ReasoningEffort string
}
