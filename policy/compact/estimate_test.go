package compact_test

import (
	"testing"

	"github.com/mrsirg97-rgb/rig/core"
	compact "github.com/mrsirg97-rgb/rig/policy/compact"
)

// TestEstimateIsBytesOverFour (decision 4's stdlib contract): for each
// message, the bytes of Content plus Reasoning plus each tool call's name
// and args, divided by 4, rounded up — per message, so the ceiling does
// not undercount many small messages.
func TestEstimateIsBytesOverFour(t *testing.T) {
	cases := []struct {
		name string
		msgs []core.Message
		want int
	}{
		{"empty", nil, 0},
		{"content only", []core.Message{{Content: "0123456789"}}, 3}, // ceil(10/4)
		{"content plus reasoning", []core.Message{{Content: "0123456789", Reasoning: "0123456789"}}, 5},
		{"content plus a call's name and args", []core.Message{{
			Content:   "0123456789",
			Reasoning: "0123456789",
			ToolCalls: []core.ToolCall{{Name: "bash", Args: []byte(`{"a":12}`)}},
		}}, 8}, // (10 + 10 + 4 + 7)/4
		{"two small messages round per message", []core.Message{{Content: "012"}, {Content: "012"}}, 2}, // 1 + 1, not ceil(6/4)
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := compact.Estimate(c.msgs); got != c.want {
				t.Fatalf("Estimate = %d, want %d", got, c.want)
			}
		})
	}
}
