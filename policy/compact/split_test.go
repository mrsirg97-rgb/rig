package compact_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/models"
	compact "github.com/mrsirg97-rgb/rig/policy/compact"
)

func TestKeepRecentCutsAtPairBoundary(t *testing.T) {
	t.Run("budget inside a multi-call batch slides to the batch's assistant", func(t *testing.T) {

		row := models.Model{Role: models.RoleInteractive, ID: "local", Window: 900, MaxTokens: 500, Reserve: 150, KeepRecent: 120}
		s := core.NewSession()
		s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("x", 2000)})
		s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("p", 200)})
		s.Append(core.Message{
			Role: core.RoleAssistant, Content: strings.Repeat("a", 100),
			ToolCalls: []core.ToolCall{{ID: "c1", Name: "bash", Args: []byte(`{}`)}},
		})
		s.Append(core.Message{Role: core.RoleTool, ToolID: "c1", Content: strings.Repeat("r", 200)})
		s.Append(core.Message{
			Role: core.RoleAssistant, Content: strings.Repeat("b", 100),
			ToolCalls: []core.ToolCall{{ID: "c2", Name: "bash", Args: []byte(`{}`)}, {ID: "c3", Name: "bash", Args: []byte(`{}`)}},
		})
		s.Append(core.Message{Role: core.RoleTool, ToolID: "c2", Content: strings.Repeat("r", 200)})
		s.Append(core.Message{Role: core.RoleTool, ToolID: "c3", Content: strings.Repeat("r", 200)})

		prov := &scriptedProvider{turns: []scriptedTurn{{events: []core.Event{core.TextDelta{Text: "S"}, core.Done{}}}}}
		fe := &captureFrontend{}
		pol, err := compact.New(prov, fe, s, "S", row)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		out, err := pol.Assemble(context.Background(), s)
		if err != nil {
			t.Fatalf("Assemble: %v", err)
		}

		if len(out) != 5 {
			t.Fatalf("assembled = %d messages, want system + summary + the batch (3)", len(out))
		}
		if out[2].Role != core.RoleAssistant || len(out[2].ToolCalls) != 2 {
			t.Fatalf("the tail must start at the batch's assistant: %+v", out[2])
		}

		evs := stripCue(fe.snapshot())
		if len(evs) != 1 {
			t.Fatalf("frontend events = %v", evs)
		}
		c, ok := evs[0].(core.Compacted)
		if !ok {
			t.Fatalf("event 0 = %T", evs[0])
		}
		if c.Kept != 128 {
			t.Fatalf("Kept = %d, want the batch's estimate (28 + 50 + 50)", c.Kept)
		}
		if c.Dropped != 627 {
			t.Fatalf("Dropped = %d, want the older prefix's estimate (500 + 50 + 27 + 50)", c.Dropped)
		}

		reqs := prov.reqs()
		if len(reqs) != 1 {
			t.Fatalf("summary calls = %d, want 1", len(reqs))
		}
		msgs := reqs[0].Messages
		if len(msgs) != 2 {
			t.Fatalf("the summary request = %d messages, want the 3 shape (two)", len(msgs))
		}
		tag := strings.Index(msgs[1].Content, "</transcript>")
		if tag < 0 {
			t.Fatal("the quoted block must be closed")
		}
		body := strings.TrimSuffix(msgs[1].Content[:tag], "\n")
		if last := strings.Split(body, "\n")[len(strings.Split(body, "\n"))-1]; last != "tool: "+strings.Repeat("r", 200) {
			t.Fatalf("the older prefix must end at c1's result; the block's last line is %q...", last[:40])
		}
		if strings.Contains(body, strings.Repeat("b", 100)) {
			t.Fatal("the quoted block must not carry the batch (its assistant line is in the kept tail)")
		}
	})

	t.Run("the single-call pair is atomic", func(t *testing.T) {

		prompt, err := os.ReadFile("summary_prompt.txt")
		if err != nil {
			t.Fatalf("read the prompt file: %v", err)
		}
		row := models.Model{Role: models.RoleInteractive, ID: "local", Window: (len(prompt)+3)/4 + 550 + 71, MaxTokens: 500, Reserve: 100, KeepRecent: 120}
		s := core.NewSession()
		s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("x", 2000)})
		s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("p", 200)})
		s.Append(core.Message{
			Role: core.RoleAssistant, Content: strings.Repeat("a", 300),
			ToolCalls: []core.ToolCall{{ID: "c1", Name: "bash", Args: []byte(`{}`)}},
		})
		s.Append(core.Message{Role: core.RoleTool, ToolID: "c1", Content: strings.Repeat("r", 400)})

		prov := &scriptedProvider{turns: []scriptedTurn{{events: []core.Event{core.TextDelta{Text: "S"}, core.Done{}}}}}
		pol, err := compact.New(prov, &captureFrontend{}, s, "S", row)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		out, err := pol.Assemble(context.Background(), s)
		if err != nil {
			t.Fatalf("Assemble: %v", err)
		}
		if len(out) != 4 {
			t.Fatalf("assembled = %d messages, want system + summary + the pair (2)", len(out))
		}
		if out[2].Role != core.RoleAssistant || len(out[2].ToolCalls) != 1 {
			t.Fatalf("the tail must start at the pair's assistant: %+v", out[2])
		}
	})

	t.Run("a single oversized last message skips the compact", func(t *testing.T) {
		row := models.Model{Role: models.RoleInteractive, ID: "local", Window: 1000, MaxTokens: 500, Reserve: 100, KeepRecent: 100}
		s := core.NewSession()
		s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("p", 8000)})

		prov := &scriptedProvider{}
		pol, err := compact.New(prov, &captureFrontend{}, s, "S", row)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		out, err := pol.Assemble(context.Background(), s)
		if err != nil {
			t.Fatalf("Assemble: %v", err)
		}
		if prov.calls() != 0 {
			t.Fatalf("nothing to drop: no summary call (calls = %d)", prov.calls())
		}
		if len(out) != 2 || out[1].Content != strings.Repeat("p", 8000) {
			t.Fatalf("the passthrough must be returned: %d messages", len(out))
		}
	})
}
