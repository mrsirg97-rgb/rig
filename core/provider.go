package core

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

type Event interface{ event() }

type Snapshot interface {
	Event
	Snapshot()
}

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
	_ Event = Notice{}
	_ Event = Verdict{}
	_ Event = Phase{}
	_ Event = WorkerDone{}
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
	Model    string
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

// BoundedCall is what a tool start published over a fleet transport names: the
// tool and the one short argument the sender let across. A published call
// carries that argument in Call.Args as a JSON string, because a call's body
// never crosses — and this is the reader that knows the difference.
func (e ToolStart) BoundedCall() string {
	var arg string
	if len(e.Call.Args) > 0 {
		json.Unmarshal(e.Call.Args, &arg)
	}
	if arg == "" {
		return e.Call.Name
	}
	return e.Call.Name + " " + arg
}

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
	Model   string
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
	Tool      string    // the worker's last call, bounded; empty until it makes one
	ToolAt    time.Time // when that call arrived; the age the band shows is this against the paint
}

type SwarmStatus struct {
	Workers []SwarmWorker
	Pending int
	Review  int
}

func (SwarmStatus) event() {}

func (SwarmStatus) Snapshot() {}

type Level int

const (
	LevelInfo Level = iota
	LevelSuccess
	LevelError
)

func (l Level) String() string {
	switch l {
	case LevelSuccess:
		return "success"
	case LevelError:
		return "error"
	}
	return "info"
}

type Notice struct {
	Source string
	Text   string
	Level  Level
}

func (Notice) event() {}

type Verdict struct {
	Row    int64
	Accept bool
	Reason string
}

func (Verdict) event() {}

type Phase struct {
	Name string
	Text string
	Done bool
	Ok   bool
	Note string
}

func (Phase) event() {}

type Provider interface {
	Stream(ctx context.Context, req Request) (<-chan Event, error)
}

type Request struct {
	Messages        []Message
	Tools           []ToolSpec
	MaxTokens       int
	ReasoningEffort string
}

// WorkerDone is a delegated worker's return (2.14.0, SPEC_DELEGATE 8). It
// carries no Snapshot, so a frontend keeps every worker that comes back: the
// inbox appends them and hands them to the next turn in the order they
// arrived. Content is the same text the synchronous tool result always was —
// the worker's capped stdout and its trailer line.
type WorkerDone struct {
	N        int
	Task     string
	Content  string
	Exit     int
	Duration time.Duration
	Session  string
	Log      string
}

func (WorkerDone) event() {}

// Head is the line a return opens with — which worker, how it ended, how long
// it ran, where its session lives — and the line every frontend shows as the
// return lands.
func (d WorkerDone) Head() string {
	return "delegate #" + strconv.Itoa(d.N) + " returned · exit " + strconv.Itoa(d.Exit) +
		" · " + d.Duration.Round(time.Second).String() + " · session " + d.Session
}

// WorkerBlock is the text a turn of returns is made of: each worker's head over
// its content, in the order they arrived. One shape, because it is the text the
// model reads, in whichever frontend it was sitting.
func WorkerBlock(returns []WorkerDone) string {
	blocks := make([]string, 0, len(returns))
	for _, d := range returns {
		blocks = append(blocks, d.Head()+"\n"+d.Content)
	}
	return strings.Join(blocks, "\n\n")
}
