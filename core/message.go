package core

import "encoding/json"

// Role labels the author of a Message.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message is the kernel's wire shape. Providers adapt to and from it;
// nothing provider-specific crosses into the kernel.
type Message struct {
	Role      Role
	Content   string
	Reasoning string     // assistant turns only; empty when the model did not think (SPEC_HARDENING decision 2)
	ToolCalls []ToolCall // assistant turns only
	ToolID    string     // tool result turns only
}

// ToolCall is an assistant's request to run a named tool. Args stay raw;
// each tool parses its own slice.
type ToolCall struct {
	ID   string
	Name string
	Args json.RawMessage
}
