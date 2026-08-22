package compact_test

import (
	"testing"

	"github.com/mrsirg97-rgb/rig/core"
	compact "github.com/mrsirg97-rgb/rig/policy/compact"
)

func TestEstimateIsBytesOverFour(t *testing.T) {
	cases := []struct {
		name string
		msgs []core.Message
		want int
	}{
		{"empty", nil, 0},
		{"content only", []core.Message{{Content: "0123456789"}}, 3},
		{"the last assistant's reasoning counts", []core.Message{{Role: core.RoleAssistant, Content: "0123456789", Reasoning: "0123456789"}}, 5},
		{"content plus a call's name and args", []core.Message{{
			Role:      core.RoleAssistant,
			Content:   "0123456789",
			Reasoning: "0123456789",
			ToolCalls: []core.ToolCall{{Name: "bash", Args: []byte(`{"a":12}`)}},
		}}, 8},
		{"history reasoning does not count", []core.Message{
			{Role: core.RoleAssistant, Content: "0123456789", Reasoning: "0123456789012345678901234567890123456789"},
			{Role: core.RoleTool, Content: "0123"},
			{Role: core.RoleAssistant, Content: "0123456789", Reasoning: "0123456789"},
		}, 3 + 1 + 5},
		{"reasoning on a non-assistant row never counts", []core.Message{{Role: core.RoleUser, Content: "0123456789", Reasoning: "0123456789"}}, 3},
		{"two small messages round per message", []core.Message{{Content: "012"}, {Content: "012"}}, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := compact.Estimate(c.msgs); got != c.want {
				t.Fatalf("Estimate = %d, want %d", got, c.want)
			}
		})
	}
}
