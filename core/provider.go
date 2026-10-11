package core

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/mrsirg97-rgb/evt/event"
)

var (
	_ event.Event = TextDelta{}
	_ event.Event = ReasoningDelta{}
	_ event.Event = ToolCallEvent{}
	_ event.Event = Done{}
	_ event.Event = EmptyTurn{}
	_ event.Event = Fault{}
	_ event.Event = ToolStart{}
	_ event.Event = ToolResult{}
	_ event.Event = TurnEnd{}
	_ event.Event = TestEvent{}
	_ event.Event = Compacted{}
	_ event.Event = SwarmStatus{}
	_ event.Event = Verdict{}
	_ event.Event = Phase{}
	_ event.Event = WorkerDone{}
)

type TextDelta struct {
	Text string

	event.Marker
}

type ReasoningDelta struct {
	Text    string
	Details json.RawMessage

	event.Marker
}

type ToolCallEvent struct {
	Call ToolCall

	event.Marker
}

type Done struct {
	StopReason string
	Usage      Usage
	Model      string

	event.Marker
}

type EmptyTurn struct {
	Resample int
	Limit    int
	Usage    Usage
	Model    string

	event.Marker
}

type Fault struct {
	Err error

	event.Marker
}

type Usage struct {
	Prompt     int
	Completion int
	CacheRead  int
	CacheWrite int
	Cost       float64
}

type ToolStart struct {
	Call ToolCall

	event.Marker
}

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

	event.Marker
}

type TurnReason string

const (
	TurnOver      TurnReason = "over"
	TurnFault     TurnReason = "fault"
	TurnInterrupt TurnReason = "interrupt"
)

type TurnEnd struct {
	Reason TurnReason

	event.Marker
}

type TestEvent struct {
	Name string

	event.Marker
}

type Compacted struct {
	Summary string
	Dropped int
	Kept    int
	Usage   Usage
	Model   string

	event.Marker
}

type Compacting struct {
	event.Marker
}

type SwarmWorker struct {
	ID        int
	Role      string
	Task      string
	Heartbeat time.Time
	Done      int
	Failed    int
	State     string
	Tool      string
	ToolAt    time.Time
}

type SwarmStatus struct {
	Workers []SwarmWorker
	Pending int
	Review  int

	event.Marker
}

func (SwarmStatus) Snapshot() {}

type Verdict struct {
	Row    int64
	Accept bool
	Reason string

	event.Marker
}

type Phase struct {
	Name string
	Text string
	Done bool
	Ok   bool
	Note string

	event.Marker
}

type Provider interface {
	Stream(ctx context.Context, req Request) (<-chan event.Event, error)
}

type Request struct {
	Messages        []Message
	Tools           []ToolSpec
	MaxTokens       int
	ReasoningEffort string
}

const (
	KindCommand = "command"
	KindKernel  = "kernel"
	KindFetch   = "fetch"
)

type WorkerDone struct {
	N        int
	Task     string
	Content  string
	Exit     int
	Duration time.Duration
	Session  string
	Log      string
	Kind     string
	RunID    string

	event.Marker
}

func (d WorkerDone) Head() string {
	if d.Kind == "" {
		return "delegate #" + strconv.Itoa(d.N) + " returned · exit " + strconv.Itoa(d.Exit) +
			" · " + d.Duration.Round(time.Second).String() + " · session " + d.Session
	}
	return "delegate #" + strconv.Itoa(d.N) + " returned · exit " + strconv.Itoa(d.Exit) +
		" · " + d.Duration.Round(time.Second).String() + " · run " + d.RunID
}

func WorkerBlock(returns []WorkerDone) string {
	blocks := make([]string, 0, len(returns))
	for _, d := range returns {
		blocks = append(blocks, d.Head()+"\n"+d.Content)
	}
	return strings.Join(blocks, "\n\n")
}
