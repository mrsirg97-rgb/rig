// Package file carries the read, write, and edit tools. Edit is exact-match
// string replacement with loud, specific failure messages; provenance from
// the threaded session makes edit-after-external-change fail loudly instead
// of clobbering.
package file

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mrsirg97-rgb/rig/core"
)

// readCap bounds a single read's contribution to the transcript.
const readCap = 1 << 20

func strictDecode(data json.RawMessage, out any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	return dec.Decode(out)
}

func stateOf(ctx context.Context, path string) (core.FileState, bool) {
	s, ok := core.SessionFrom(ctx)
	if !ok {
		return core.FileState{}, false
	}
	st, ok := s.Files[path]
	return st, ok
}

// normalizePath canonicalizes at the boundary so a.go and ./a.go are the
// same key: without it the drift check can be silently bypassed by path
// spelling.
func normalizePath(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return path
}

func recordState(ctx context.Context, path string, data []byte) {
	s, ok := core.SessionFrom(ctx)
	if !ok {
		return // standalone exec: no session to maintain
	}
	st, err := os.Stat(path)
	if err != nil {
		return
	}
	sum := sha256.Sum256(data)
	s.Files[path] = core.FileState{
		Hash:  hex.EncodeToString(sum[:]),
		Mtime: st.ModTime().UnixNano(),
	}
}

// --- read -----------------------------------------------------------------

type readTool struct{}

func Read() core.Tool { return &readTool{} }

func (readTool) Name() string { return "read" }

func (readTool) Description() string {
	return "read a file; remembers its disk state for drift-checked edits"
}

func (readTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {"type": "string", "description": "the file to read"}
		},
		"required": ["path"]
	}`)
}

type readArgs struct {
	Path string `json:"path"`
}

func (readTool) Exec(ctx context.Context, data json.RawMessage) (string, error) {
	var a readArgs
	if err := strictDecode(data, &a); err != nil {
		return "", fmt.Errorf("read: args: %w", err)
	}
	a.Path = normalizePath(a.Path)
	fileData, err := os.ReadFile(a.Path)
	if err != nil {
		return "", fmt.Errorf("read: %w", err)
	}
	recordState(ctx, a.Path, fileData)
	content := string(fileData)
	if len(content) > readCap {
		content = content[:readCap] + "\n[output truncated]"
	}
	return content, nil
}

// --- write ----------------------------------------------------------------

type writeTool struct{}

func Write() core.Tool { return &writeTool{} }

func (writeTool) Name() string { return "write" }

func (writeTool) Description() string {
	return "create or overwrite a file"
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
	return fmt.Sprintf("wrote %d bytes to %s", len(a.Content), a.Path), nil
}

// --- edit -----------------------------------------------------------------

type editTool struct{}

func Edit() core.Tool { return &editTool{} }

func (editTool) Name() string { return "edit" }

func (editTool) Description() string {
	return "replace exactly one occurrence of an old string; refuses drift and ambiguity"
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

	fileData, err := os.ReadFile(a.Path)
	if err != nil {
		return "", fmt.Errorf("edit: %w", err)
	}

	// drift check: the file must still be what was last observed.
	if recorded, seen := stateOf(ctx, a.Path); seen {
		sum := sha256.Sum256(fileData)
		if recorded.Hash != hex.EncodeToString(sum[:]) || recorded.Mtime != mtimeOf(a.Path) {
			return "", fmt.Errorf("edit: file drifted since last read (external change?): %s", a.Path)
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
	return fmt.Sprintf("edited %s: replaced %d byte(s)", a.Path, len(a.Old)), nil
}

func mtimeOf(path string) int64 {
	st, err := os.Stat(path)
	if err != nil {
		return -1
	}
	return st.ModTime().UnixNano()
}
