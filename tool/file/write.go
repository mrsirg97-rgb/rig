package file

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/tool"
)

type Write interface {
	tool.Definition
	Exec(ctx context.Context, args json.RawMessage) (string, error)

	Write(ctx context.Context, path, content string) (string, error)
}

type writeTool struct{ tool.Definition }

func NewWrite() Write { return &writeTool{tool.Def("write")} }

type writeArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func (t writeTool) Exec(ctx context.Context, data json.RawMessage) (string, error) {
	var a writeArgs
	if err := strictDecode(data, &a); err != nil {
		return "", fmt.Errorf("write: args: %w", err)
	}
	return t.Write(ctx, a.Path, a.Content)
}

func (writeTool) Write(ctx context.Context, path, content string) (string, error) {
	path = normalizePath(path)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("write: %w", err)
	}
	recordState(ctx, path, []byte(content))
	s, _ := core.SessionFrom(ctx)
	rememberContent(s, path, content)
	return fmt.Sprintf("wrote %d bytes to %s", len(content), path), nil
}
