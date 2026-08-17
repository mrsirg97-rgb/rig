package compact

import (
	"fmt"
	"strings"

	"github.com/mrsirg97-rgb/rig/core"
)

// SummarySystem is the summary call's short role (decision 3).
const SummarySystem = "You write summaries of agent transcripts."

// RenderTranscript renders the older prefix as quoted data (decision 3):
// one line per message, role prefixed — the user and assistant content as
// is, each tool call as the assistant's [calls name args] line, each
// result as its tool line. The prefix is data inside the block, not live
// messages: a "reply with only X" or a bare tool call cannot be mistaken
// for the next instruction.
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

// SummaryInput is the summary call's message list (decision 3): the short
// system role, and one user message carrying the older prefix rendered as
// quoted transcript data, followed by the summary_prompt.txt instruction.
// The model summarizes the block; it does not continue the conversation
// it quotes.
func SummaryInput(older []core.Message) []core.Message {
	return []core.Message{
		{Role: core.RoleSystem, Content: SummarySystem},
		{Role: core.RoleUser, Content: "<transcript>\n" + RenderTranscript(older) + "</transcript>\n\n" + summaryPrompt},
	}
}
