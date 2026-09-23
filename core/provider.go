package core

import (
	"context"
	"encoding/json"
	"time"
)

type Event interface{ event() }

var (
	_ Event = TextDelta{}
	_ Event = ReasoningDelta{}
	_ Event = ToolCallEvent{}
	_ Event = Done{}
	_ Event = EmptyTurn{}
	_ Event = Fault{}
	_ Event = ToolStart{}
	_ Event = ToolResult{}
	_ Event = TurnEnd{}
	_ Event = TestEvent{}
	_ Event = Compacted{}
	_ Event = SwarmStatus{}
	_ Event = SwarmNotice{}
)

type TextDelta struct{ Text string }

func (TextDelta) event() {}

type ReasoningDelta struct {
	Text    string
	Details json.RawMessage
}

func (ReasoningDelta) event() {}

type ToolCallEvent struct{ Call ToolCall }

func (ToolCallEvent) event() {}

type Done struct {
	StopReason string
	Usage      Usage
	Model      string
}

func (Done) event() {}

type EmptyTurn struct {
	Resample int
	Limit    int
	Usage    Usage
}

func (EmptyTurn) event() {}

type Fault struct{ Err error }

func (Fault) event() {}

type Usage struct {
	Prompt     int
	Completion int
	CacheRead  int
	CacheWrite int
	Cost       float64
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

type SwarmWorker struct {
	ID        int
	Role      string
	Task      string
	Heartbeat time.Time
	Done      int
	Failed    int
	State     string
}

type SwarmStatus struct {
	Workers []SwarmWorker
	Pending int
	Review  int
}

func (SwarmStatus) event() {}

type SwarmNotice struct{ Text string }

func (SwarmNotice) event() {}

type Provider interface {
	Stream(ctx context.Context, req Request) (<-chan Event, error)
}

type Request struct {
	Messages        []Message
	Tools           []ToolSpec
	MaxTokens       int
	ReasoningEffort string
}
