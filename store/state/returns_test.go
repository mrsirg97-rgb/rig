package state_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/store"
	"github.com/mrsirg97-rgb/rig/v2/store/state"
)

type inboxed struct {
	scripted
	blocks []string
}

func (f *inboxed) Returns() (string, bool) {
	if len(f.blocks) == 0 {
		return "", false
	}
	block := f.blocks[0]
	f.blocks = f.blocks[1:]
	return block, true
}

func TestRecorderLandsAReturnHandedOverInsideATurn(t *testing.T) {
	db, _, _, err := store.Open(filepath.Join(t.TempDir(), "sessions.sqlite"), state.Statements(), state.SchemaVersion)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	fe := &inboxed{scripted: scripted{inputs: []string{"go"}}, blocks: []string{"delegate #1 returned · exit 0 · 1s · run j1\nBUILD-OK"}}
	rec := state.NewRecorder(fe, db, "/tmp/wt", "model-x", "2.18.2", "rec-returns", core.NewSession())
	ctx := context.Background()
	if _, err := rec.Input(ctx); err != nil {
		t.Fatalf("input: %v", err)
	}
	rec.Notify(core.ToolCallEvent{Call: core.ToolCall{ID: "c1", Name: "bash", Args: json.RawMessage(`{"command":"go build"}`)}})
	rec.Notify(core.Done{StopReason: "tool_calls"})
	rec.Notify(core.ToolResult{ID: "c1", Content: "delegate: worker #1 started"})
	block, ok := rec.Returns()
	if !ok || block != "delegate #1 returned · exit 0 · 1s · run j1\nBUILD-OK" {
		t.Fatalf("the recorder passes the block through: %q %v", block, ok)
	}
	if _, ok := rec.Returns(); ok {
		t.Fatal("an empty inbox records nothing")
	}
	rec.Notify(core.TextDelta{Text: "it built"})
	rec.Notify(core.Done{StopReason: "stop"})

	sess, err := state.Resume(ctx, db, "rec-returns")
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	want := []core.Role{core.RoleUser, core.RoleAssistant, core.RoleTool, core.RoleUser, core.RoleAssistant}
	if len(sess.Messages) != len(want) {
		t.Fatalf("the resumed transcript has the return in its place: %+v", sess.Messages)
	}
	for i, role := range want {
		if sess.Messages[i].Role != role {
			t.Fatalf("message %d is %s, want %s: %+v", i, sess.Messages[i].Role, role, sess.Messages)
		}
	}
	if sess.Messages[3].Content != block {
		t.Fatalf("the return is stored as it was told: %q", sess.Messages[3].Content)
	}
}

func TestRecorderOverAFrontendWithNoInboxHandsNothingOver(t *testing.T) {
	rec, _ := openRecorder(t, "rec-no-inbox", []string{"go"})
	if _, ok := rec.Returns(); ok {
		t.Fatal("no inbox beneath, nothing to hand over")
	}
}
