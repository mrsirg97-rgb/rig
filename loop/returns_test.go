package loop_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mrsirg97-rgb/evt"
	"github.com/mrsirg97-rgb/rig/v3"
	"github.com/mrsirg97-rgb/rig/v3/core"
	"github.com/mrsirg97-rgb/rig/v3/loop"
)

type inboxFrontend struct {
	recorderFrontend
	blocks []string
	asked  int
}

func (f *inboxFrontend) Returns() (string, bool) {
	f.asked++
	if len(f.blocks) == 0 {
		return "", false
	}
	block := f.blocks[0]
	f.blocks = f.blocks[1:]
	return block, true
}

func TestAReturnIsHandedOverAtTheStepBoundaryInsideTheTurn(t *testing.T) {
	bash := &scriptedTool{name: "bash", result: "started"}
	p := &scriptedProvider{turns: []scriptedTurn{
		{events: []evt.Signal{
			callEv(core.ToolCall{ID: "c1", Name: "bash", Args: json.RawMessage(`{}`)}),
			doneEv(),
		}},
		{events: []evt.Signal{
			callEv(core.ToolCall{ID: "c2", Name: "bash", Args: json.RawMessage(`{}`)}),
			doneEv(),
		}},
		{events: []evt.Signal{textEv("read it"), doneEv()}},
	}}
	f := &inboxFrontend{recorderFrontend: recorderFrontend{inputs: make(chan string, 8)}, blocks: []string{"delegate #1 returned · exit 0"}}
	session := core.NewSession()
	k := rig.New(rig.WithProvider(p), rig.WithFrontend(f), rig.WithPolicy(&transcriptPolicy{}), rig.WithTools(bash))
	k.Session = session

	f.inputs <- "go"
	close(f.inputs)
	if err := loop.Run(context.Background(), k); err != nil {
		t.Fatalf("run: %v", err)
	}
	wantTranscript(t, session,
		core.Message{Role: core.RoleUser, Content: "go"},
		core.Message{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "c1", Name: "bash"}}},
		core.Message{Role: core.RoleTool, ToolID: "c1", Content: "started"},
		core.Message{Role: core.RoleUser, Content: "delegate #1 returned · exit 0"},
		core.Message{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "c2", Name: "bash"}}},
		core.Message{Role: core.RoleTool, ToolID: "c2", Content: "started"},
		core.Message{Role: core.RoleAssistant, Content: "read it"},
	)
	if f.asked != 2 {
		t.Fatalf("the inbox is asked once per step boundary: %d", f.asked)
	}
}

func TestAFrontendWithNoInboxRunsAsBefore(t *testing.T) {
	bash := &scriptedTool{name: "bash", result: "42"}
	p := &scriptedProvider{turns: []scriptedTurn{
		{events: []evt.Signal{callEv(core.ToolCall{ID: "c1", Name: "bash", Args: json.RawMessage(`{}`)}), doneEv()}},
		{events: []evt.Signal{textEv("done"), doneEv()}},
	}}
	f := &recorderFrontend{inputs: make(chan string, 8)}
	session := core.NewSession()
	k := rig.New(rig.WithProvider(p), rig.WithFrontend(f), rig.WithPolicy(&transcriptPolicy{}), rig.WithTools(bash))
	k.Session = session
	f.inputs <- "go"
	close(f.inputs)
	if err := loop.Run(context.Background(), k); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(session.Messages) != 4 {
		t.Fatalf("no inbox, no extra message: %d", len(session.Messages))
	}
}

func TestAnInterruptedTurnLeavesTheReturnsForTheNextInput(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	bash := &scriptedTool{name: "bash", result: "x", cancel: cancel}
	p := &scriptedProvider{turns: []scriptedTurn{
		{events: []evt.Signal{callEv(core.ToolCall{ID: "c1", Name: "bash", Args: json.RawMessage(`{}`)}), doneEv()}},
		{holdCtx: true},
	}}
	f := &inboxFrontend{recorderFrontend: recorderFrontend{inputs: make(chan string, 8)}, blocks: []string{"delegate #1 returned"}}
	k := rig.New(rig.WithProvider(p), rig.WithFrontend(f), rig.WithPolicy(&transcriptPolicy{}), rig.WithTools(bash))
	k.Session = core.NewSession()
	f.inputs <- "go"
	_ = loop.Run(ctx, k)
	if f.asked != 0 || len(f.blocks) != 1 {
		t.Fatalf("a cancelled turn does not take the returns: asked %d, left %d", f.asked, len(f.blocks))
	}
}
