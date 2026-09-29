package file

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/mrsirg97-rgb/rig/v2/core"
)

type writeTool struct{}

func Write() core.Tool { return &writeTool{} }

func (writeTool) Name() string { return "write" }

func (writeTool) Description() string {
	return "create or overwrite a file with its full content. Guidelines: new files and whole rewrites; for a change inside an existing file use edit. Reply: the path and the bytes written. A plugin you write lands in plugins/pending/ until the operator approves it."
}

func (writeTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path":    {"type": "string", "description": "the file to write"},
			"content": {"type": "string", "description": "the full new content"}
		},
		"required": ["path", "content"]
	}`)
}

type writeArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func (writeTool) Exec(ctx context.Context, data json.RawMessage) (string, error) {
	var a writeArgs
	if err := strictDecode(data, &a); err != nil {
		return "", fmt.Errorf("write: args: %w", err)
	}
	a.Path = normalizePath(a.Path)
	if err := os.WriteFile(a.Path, []byte(a.Content), 0o644); err != nil {
		return "", fmt.Errorf("write: %w", err)
	}
	recordState(ctx, a.Path, []byte(a.Content))
	s, _ := core.SessionFrom(ctx)
	rememberContent(s, a.Path, a.Content)
	return fmt.Sprintf("wrote %d bytes to %s", len(a.Content), a.Path), nil
}
