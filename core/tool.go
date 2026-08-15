package core

import (
	"context"
	"encoding/json"
)

// Tool executes one named capability. Schemas are authored by hand next
// to the tool; reflection-derived schemas drift from intent.
type Tool interface {
	Name() string
	Schema() json.RawMessage
	Exec(ctx context.Context, args json.RawMessage) (string, error)
}

// ToolSpec is what a Request carries per tool: name plus schema, nothing
// executable.
type ToolSpec struct {
	Name   string
	Schema json.RawMessage
}
