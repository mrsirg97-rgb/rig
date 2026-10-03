package file

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/tool"
)

type writeTool struct{ tool.Definition }

func Write() core.Tool { return &writeTool{tool.Def("write")} }

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
	touchIndex(a.Path)
	return fmt.Sprintf("wrote %d bytes to %s", len(a.Content), a.Path), nil
}
