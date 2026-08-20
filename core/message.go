package core

import "encoding/json"

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type Message struct {
	Role          Role
	Content       string
	Reasoning     string
	ToolCalls     []ToolCall
	ToolID        string
	ContextTokens int
}

type ToolCall struct {
	ID   string
	Name string
	Args json.RawMessage
}
