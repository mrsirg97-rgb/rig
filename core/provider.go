package core

import (
	"context"
	"time"
)

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

type TextDelta struct{ Text string }

func (TextDelta) event() {}

type ReasoningDelta struct{ Text string }

func (ReasoningDelta) event() {}

type ToolCallEvent struct{ Call ToolCall }

func (ToolCallEvent) event() {}

type Done struct {
	StopReason string
	Usage      Usage
}

func (Done) event() {}

type Fault struct{ Err error }

func (Fault) event() {}

type Usage struct {
	Prompt     int
	Completion int
	CacheRead  int
	CacheWrite int
}

type ToolStart struct{ Call ToolCall }

func (ToolStart) event() {}

type ToolResult struct {
	ID       string
	Content  string
	Err      error
	Duration time.Duration
}

func (ToolResult) event() {}

type TurnReason string

const (
	TurnOver      TurnReason = "over"
	TurnFault     TurnReason = "fault"
	TurnInterrupt TurnReason = "interrupt"
)

type TurnEnd struct{ Reason TurnReason }

func (TurnEnd) event() {}

type TestEvent struct{ Name string }

func (TestEvent) event() {}

type Compacted struct {
	Summary string
	Dropped int
	Kept    int
	Usage   Usage
}

func (Compacted) event() {}

type Compacting struct{}

func (Compacting) event() {}

type Provider interface {
	Stream(ctx context.Context, req Request) (<-chan Event, error)
}

type Request struct {
	Messages        []Message
	Tools           []ToolSpec
	MaxTokens       int
	ReasoningEffort string
}
