package loop_test

import (
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/evt"
	"github.com/mrsirg97-rgb/rig/v3"
	"github.com/mrsirg97-rgb/rig/v3/core"
	"github.com/mrsirg97-rgb/rig/v3/loop"
)

type gatedTool struct{ gate chan struct{} }

func (t *gatedTool) Name() string            { return "gated" }
func (t *gatedTool) Description() string     { return "blocks until the gate opens" }
func (t *gatedTool) Schema() json.RawMessage { return json.RawMessage(`{}`) }
func (t *gatedTool) Exec(ctx context.Context, _ json.RawMessage) (string, error) {
	<-t.gate
	return "ok", nil
}

type waveFront struct {
	inputs chan string
	starts chan string
}

func (f *waveFront) Input(ctx context.Context) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case v, ok := <-f.inputs:
		if !ok {
			return "", io.EOF
		}
		return v, nil
	}
}

func (f *waveFront) Notify(ev evt.Signal) {
	if e, ok := ev.(core.ToolStart); ok {
		f.starts <- e.Call.ID
	}
}

func TestWaveStartsArriveWhileTheWaveRuns(t *testing.T) {
	gate := make(chan struct{})
	tool := &gatedTool{gate: gate}
	p := &scriptedProvider{turns: []scriptedTurn{
		{events: []evt.Signal{
			callEv(core.ToolCall{ID: "a", Name: "gated"}),
			callEv(core.ToolCall{ID: "b", Name: "gated"}),
			callEv(core.ToolCall{ID: "c", Name: "gated"}),
			doneEv(),
		}},
		{events: []evt.Signal{textEv("done"), doneEv()}},
	}}
	f := &waveFront{inputs: make(chan string, 4), starts: make(chan string, 3)}
	k := rig.New(
		rig.WithProvider(p),
		rig.WithFrontend(f),
		rig.WithPolicy(&transcriptPolicy{}),
		rig.WithTools(tool),
		rig.WithPriority(func(core.ToolCall) int { return rig.PriorityRead }),
	)
	k.Session = core.NewSession()

	f.inputs <- "go"
	close(f.inputs)
	done := make(chan error, 1)
	go func() { done <- loop.Run(context.Background(), k) }()

	var got []string
	deadline := time.After(2 * time.Second)
	for len(got) < 3 {
		select {
		case id := <-f.starts:
			got = append(got, id)
		case <-deadline:
			t.Fatalf("the wave's starts must all arrive while the wave runs (the tools hold the gate), got %v", got)
		}
	}
	close(gate)
	if err := <-done; err != nil {
		t.Fatalf("run: %v", err)
	}
}
