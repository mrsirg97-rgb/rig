package cli

import (
	"fmt"
	"github.com/mrsirg97-rgb/rig/v2/core"
	"io"
	"strconv"
)

func (c *cli) Notify(ev core.Event) {
	switch e := ev.(type) {
	case core.TextDelta:
		io.WriteString(c.out, e.Text)
	case core.ReasoningDelta:

		io.WriteString(c.out, e.Text)
	case core.ToolStart:
		c.current = e.Call.Name
		fmt.Fprintf(c.out, "\n● %s\n", e.Call.Name)
	case core.ToolResult:
		outcome := "✓"
		if e.Err != nil {
			outcome = "✕"
		}
		fmt.Fprintf(c.out, "%s %s %s\n", c.current, outcome, e.Duration)
	case core.Done:
		io.WriteString(c.out, "\n")

		c.prompt += e.Usage.Prompt
		c.completion += e.Usage.Completion
		c.cacheRead += e.Usage.CacheRead
	case core.EmptyTurn:
		fmt.Fprintf(c.out, "\nempty turn, resampling (%d/%d)\n", e.Resample, e.Limit)
		c.prompt += e.Usage.Prompt
		c.completion += e.Usage.Completion
		c.cacheRead += e.Usage.CacheRead
	case core.Compacting:

		io.WriteString(c.out, "\u29c9 compacting\u2026\n")
	case core.Compacted:

		fmt.Fprintf(c.out, "⧉ compact: -%s kept %s · summary ↑%s ↓%s\n",
			formatTokens(e.Dropped), formatTokens(e.Kept),
			formatTokens(e.Usage.Prompt), formatTokens(e.Usage.Completion))
	case core.Fault:
		fmt.Fprintf(c.out, "\n[fault] %v\n", e.Err)
	case core.TurnEnd:

		hit := 0
		if c.prompt > 0 {
			hit = int(int64(c.cacheRead) * 100 / int64(c.prompt))
		}
		fmt.Fprintf(c.out, "↑%s ↓%s · cache %s %d%%\n",
			formatTokens(c.prompt), formatTokens(c.completion), formatTokens(c.cacheRead), hit)
		c.prompt, c.completion, c.cacheRead = 0, 0, 0
	}
}

func formatTokens(n int) string {
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
