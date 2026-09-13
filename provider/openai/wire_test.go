package openai

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/mrsirg97-rgb/rig/core"
)

func TestWireMarshalingIsDeterministic(t *testing.T) {
	msgs := []core.Message{
		{Role: core.RoleSystem, Content: "be terse"},
		{Role: core.RoleUser, Content: "go"},
		{Role: core.RoleAssistant, Content: "text", Reasoning: "thinking",
			ToolCalls: []core.ToolCall{{ID: "c1", Name: "bash", Args: json.RawMessage(`{"x":1}`)}}},
		{Role: core.RoleTool, ToolID: "c1", Content: "out"},
	}
	specs := []core.ToolSpec{{Name: "bash", Description: "runs commands", Schema: json.RawMessage(`{"type":"object"}`)}}
	req := func() []byte {
		b, err := json.Marshal(wireRequest{
			Model:    "m",
			Messages: wireMessages(msgs),
			Tools:    wireTools(specs),
			Stream:   true,
		})
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	if a, b := req(), req(); !bytes.Equal(a, b) {
		t.Fatalf("the same request must marshal to identical bytes (the prefix cache is byte-keyed):\n a: %s\n b: %s", a, b)
	}
}

func TestWireMessagesAreAppendOnly(t *testing.T) {
	turn1 := []core.Message{
		{Role: core.RoleSystem, Content: "be terse"},
		{Role: core.RoleUser, Content: "go"},
	}
	turn2 := append(append([]core.Message{}, turn1...),
		core.Message{Role: core.RoleAssistant, Content: "text",
			ToolCalls: []core.ToolCall{{ID: "c1", Name: "bash", Args: json.RawMessage(`{}`)}}},
		core.Message{Role: core.RoleTool, ToolID: "c1", Content: "out"},
	)
	b1, err := json.Marshal(wireMessages(turn1))
	if err != nil {
		t.Fatal(err)
	}
	b2, err := json.Marshal(wireMessages(turn2))
	if err != nil {
		t.Fatal(err)
	}
	prefix := append(append([]byte{}, b1[:len(b1)-1]...), ',')
	if !bytes.HasPrefix(b2, prefix) {
		t.Fatalf("a later turn's messages must be the earlier turn's plus the appended tail (a rewrite or reorder kills the prefix cache):\n t1: %s\n t2: %s", b1, b2)
	}
}
