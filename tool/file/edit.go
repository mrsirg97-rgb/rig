package file

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/mrsirg97-rgb/rig/v2/core"
	difftool "github.com/mrsirg97-rgb/rig/v2/tool/diff"
)

const driftCap = 20

type editTool struct{}

func Edit() core.Tool { return &editTool{} }

func (editTool) Name() string { return "edit" }

func (editTool) Description() string {
	return "Replaces one occurrence of old with new in a file. Guidelines: read the file first, then put enough of it in old to match exactly once. The call refuses, naming why, when you did not read the file this session, when old matches never or more than once, or when the file changed since your read. Reply: the path and the bytes replaced."
}

func (editTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {"type": "string", "description": "the file to edit"},
			"old":  {"type": "string", "description": "the exact text to replace; must occur exactly once"},
			"new":  {"type": "string", "description": "the replacement text"}
		},
		"required": ["path", "old", "new"]
	}`)
}

type editArgs struct {
	Path string `json:"path"`
	Old  string `json:"old"`
	New  string `json:"new"`
}

func (editTool) Exec(ctx context.Context, data json.RawMessage) (string, error) {
	var a editArgs
	if err := strictDecode(data, &a); err != nil {
		return "", fmt.Errorf("edit: args: %w", err)
	}
	a.Path = normalizePath(a.Path)
	if a.Old == "" {
		return "", errors.New("edit: zero-width old string")
	}
	if _, threaded := core.SessionFrom(ctx); threaded {
		if _, seen := stateOf(ctx, a.Path); !seen {
			return "", fmt.Errorf("edit: %s was never read this session: read it first", a.Path)
		}
	}

	fileData, err := os.ReadFile(a.Path)
	if err != nil {
		return "", fmt.Errorf("edit: %w", err)
	}

	if recorded, seen := stateOf(ctx, a.Path); seen {
		sum := sha256.Sum256(fileData)
		if recorded.Hash != hex.EncodeToString(sum[:]) || recorded.Mtime != mtimeOf(a.Path) {
			return "", fmt.Errorf("%s", driftRefusal(ctx, a.Path, string(fileData)))
		}
	}

	count := strings.Count(string(fileData), a.Old)
	switch {
	case count == 0:
		return "", fmt.Errorf("edit: old string not found in %s", a.Path)
	case count > 1:
		return "", fmt.Errorf("edit: old string occurs %d times in %s, want exactly 1", count, a.Path)
	}

	updated := strings.Replace(string(fileData), a.Old, a.New, 1)
	if err := os.WriteFile(a.Path, []byte(updated), 0o644); err != nil {
		return "", fmt.Errorf("edit: %w", err)
	}
	recordState(ctx, a.Path, []byte(updated))
	s, _ := core.SessionFrom(ctx)
	rememberContent(s, a.Path, string(updated))
	return fmt.Sprintf("edited %s: replaced %d byte(s)", a.Path, len(a.Old)), nil
}

func mtimeOf(path string) int64 {
	st, err := os.Stat(path)
	if err != nil {
		return -1
	}
	return st.ModTime().UnixNano()
}

func driftRefusal(ctx context.Context, path, onDisk string) string {
	const header = "edit: the file changed since the read:"
	s, _ := core.SessionFrom(ctx)
	remembered, ok := forgottenContent(s, path)
	if !ok || remembered == onDisk {
		return header
	}
	d := difftool.Diff(remembered, onDisk, "as read", "on disk")
	lines := strings.Split(d, "\n")
	if len(lines) > driftCap {
		lines = append(lines[:driftCap], "… "+strconv.Itoa(len(lines)-driftCap)+" more lines")
	}
	return header + "\n" + strings.Join(lines, "\n")
}
