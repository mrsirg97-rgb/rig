package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mrsirg97-rgb/evt"
	"github.com/mrsirg97-rgb/evt/broadcast"
)

var (
	_ evt.Signal = TextDelta{}
	_ evt.Signal = ReasoningDelta{}
	_ evt.Signal = ToolCallEvent{}
	_ evt.Signal = Done{}
	_ evt.Signal = EmptyTurn{}
	_ evt.Signal = Fault{}
	_ evt.Signal = ToolStart{}
	_ evt.Signal = ToolResult{}
	_ evt.Signal = TurnEnd{}
	_ evt.Signal = TestEvent{}
	_ evt.Signal = Compacted{}
	_ evt.Signal = SwarmStatus{}
	_ evt.Signal = Verdict{}
	_ evt.Signal = Phase{}
	_ evt.Signal = WorkerDone{}
)

type TextDelta struct {
	Text string

	evt.Mark
}

type ReasoningDelta struct {
	Text    string
	Details json.RawMessage

	evt.Mark
}

type ToolCallEvent struct {
	Call ToolCall

	evt.Mark
}

type Done struct {
	StopReason string
	Usage      Usage
	Model      string

	evt.Mark
}

type EmptyTurn struct {
	Resample int
	Limit    int
	Usage    Usage
	Model    string

	evt.Mark
}

type Fault struct {
	Err error

	evt.Mark
}

func FormatTokens(n int) string {
	switch {
	case n < 1000:
		return strconv.Itoa(n)
	case n < 10000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	case n < 1000000:
		return fmt.Sprintf("%dk", (n+500)/1000)
	default:
		return fmt.Sprintf("%.1fM", float64(n)/1000000)
	}
}

type Usage struct {
	Prompt     int
	Completion int
	CacheRead  int
	CacheWrite int
	Cost       float64

	PromptPerSecond float64
	DecodePerSecond float64
}

type ToolStart struct {
	Call ToolCall

	evt.Mark
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

	evt.Mark
}

type TurnReason string

const (
	TurnOver      TurnReason = "over"
	TurnFault     TurnReason = "fault"
	TurnInterrupt TurnReason = "interrupt"
)

type TurnEnd struct {
	Reason TurnReason

	evt.Mark
}

type TestEvent struct {
	Name string

	evt.Mark
}

type Compacted struct {
	Summary string
	Dropped int
	Kept    int
	Usage   Usage
	Model   string

	evt.Mark
}

type Compacting struct {
	evt.Mark
}

type SwarmWorker struct {
	ID        int
	Role      string
	Model     string
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

	evt.Mark
}

func (SwarmStatus) Snapshot() {}

type Verdict struct {
	Row    int64
	Accept bool
	Reason string

	evt.Mark
}

type Phase struct {
	Name string
	Text string
	Done bool
	Ok   bool
	Note string

	evt.Mark
}

type Provider interface {
	Stream(ctx context.Context, req Request) (<-chan evt.Signal, error)
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

const MintHeader = "X-Rig-Mint"

type mintKey struct{}

func MintContext(ctx context.Context, mint broadcast.Mint) context.Context {
	encoded, err := json.Marshal(mint)
	if err != nil {
		return ctx
	}
	return context.WithValue(ctx, mintKey{}, string(encoded))
}

func MintFrom(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(mintKey{}).(string)
	return v, ok
}

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

	evt.Mark
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
