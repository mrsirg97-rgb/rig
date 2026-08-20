package compact

import (
	"fmt"
	"strings"

	"github.com/mrsirg97-rgb/rig/core"
)

const SummarySystem = "You write summaries of agent transcripts."

func RenderTranscript(older []core.Message) string {
	var b strings.Builder
	for _, m := range older {
		switch m.Role {
		case core.RoleUser:
			fmt.Fprintf(&b, "user: %s\n", m.Content)
		case core.RoleAssistant:
			if m.Content != "" {
				fmt.Fprintf(&b, "assistant: %s\n", m.Content)
			}
			for _, c := range m.ToolCalls {
				fmt.Fprintf(&b, "assistant: [calls %s] %s\n", c.Name, c.Args)
			}
		case core.RoleTool:
			fmt.Fprintf(&b, "tool: %s\n", m.Content)
		}
	}
	return b.String()
}

func SummaryInput(older []core.Message) []core.Message {
	return []core.Message{
		{Role: core.RoleSystem, Content: SummarySystem},
		{Role: core.RoleUser, Content: "<transcript>\n" + RenderTranscript(older) + "</transcript>\n\n" + summaryPrompt},
	}
}
