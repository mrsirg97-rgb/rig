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
	"strconv"
	"strings"
	"sync"

	"github.com/mrsirg97-rgb/rig/core"
	difftool "github.com/mrsirg97-rgb/rig/tool/diff"
)

const readCap = 1 << 20

const driftCap = 20

var lastRead = struct {
	sync.Mutex
	m map[*core.Session]map[string]string
}{m: map[*core.Session]map[string]string{}}

func rememberContent(s *core.Session, path, content string) {
	if s == nil {
		return
	}
	lastRead.Lock()
	defer lastRead.Unlock()
	m, ok := lastRead.m[s]
	if !ok {
		m = map[string]string{}
		lastRead.m[s] = m
	}
	m[path] = content
}

func forgottenContent(s *core.Session, path string) (string, bool) {
	if s == nil {
		return "", false
	}
	lastRead.Lock()
	defer lastRead.Unlock()
	m, ok := lastRead.m[s]
	if !ok {
		return "", false
	}
	c, ok := m[path]
	return c, ok
}

func strictDecode(data json.RawMessage, out any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	return dec.Decode(out)
}

var filesMu sync.Mutex

func stateOf(ctx context.Context, path string) (core.FileState, bool) {
	s, ok := core.SessionFrom(ctx)
	if !ok {
		return core.FileState{}, false
	}
	filesMu.Lock()
	defer filesMu.Unlock()
	st, ok := s.Files[path]
	return st, ok
}

func normalizePath(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return path
}

func recordState(ctx context.Context, path string, data []byte) {
	s, ok := core.SessionFrom(ctx)
	if !ok {
		return
	}
	st, err := os.Stat(path)
	if err != nil {
		return
	}
	sum := sha256.Sum256(data)
	filesMu.Lock()
	defer filesMu.Unlock()
	s.Files[path] = core.FileState{
		Hash:  hex.EncodeToString(sum[:]),
		Mtime: st.ModTime().UnixNano(),
	}
}

type readTool struct{}

func Read() core.Tool { return &readTool{} }

func (readTool) Name() string { return "read" }

func (readTool) Description() string {
	return "read a file, or a line range of it (offset/limit). Guidelines: read before you edit — an edit is drift-checked against what you last read; a large file -> a narrower range. Reply: the text; a range past the end refuses by name."
}

func (readTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path":   {"type": "string", "description": "the file to read"},
			"offset": {"type": "integer", "description": "the 0-based line to start at (default 0); past the end refuses"},
			"limit":  {"type": "integer", "description": "the number of lines to read (default the rest of the file); negative refuses"}
		},
		"required": ["path"]
	}`)
}

type readArgs struct {
	Path   string `json:"path"`
	Offset *int   `json:"offset"`
	Limit  *int   `json:"limit"`
}

func (readTool) Exec(ctx context.Context, data json.RawMessage) (string, error) {
	var a readArgs
	if err := strictDecode(data, &a); err != nil {
		return "", fmt.Errorf("read: args: %w", err)
	}
	a.Path = normalizePath(a.Path)
	offset := 0
	if a.Offset != nil {
		if *a.Offset < 0 {
			return "", fmt.Errorf("read: offset %d is negative", *a.Offset)
		}
		offset = *a.Offset
	}
	limit := -1
	if a.Limit != nil {
		if *a.Limit < 0 {
			return "", fmt.Errorf("read: limit %d is negative", *a.Limit)
		}
		limit = *a.Limit
	}
	fileData, err := os.ReadFile(a.Path)
	if err != nil {
		return "", fmt.Errorf("read: %w", err)
	}
	stale := false
	if recorded, seen := stateOf(ctx, a.Path); seen {
		sum := sha256.Sum256(fileData)
		if recorded.Hash != hex.EncodeToString(sum[:]) || recorded.Mtime != mtimeOf(a.Path) {
			stale = true
		}
	}
	recordState(ctx, a.Path, fileData)
	s, _ := core.SessionFrom(ctx)
	rememberContent(s, a.Path, string(fileData))
	lines := strings.Split(string(fileData), "\n")
	if offset >= len(lines) {
		return "", fmt.Errorf("read: offset %d is past the end (%d lines)", offset, len(lines))
	}
	end := len(lines)
	if limit >= 0 {
		end = offset + limit
		if end > len(lines) {
			end = len(lines)
		}
	}
	content := strings.Join(lines[offset:end], "\n")
	if len(content) > readCap {
		content = content[:readCap] + "\n[output truncated]"
	}
	if stale {
		content = "[changed since your observation] " + a.Path + " — re-read before acting on it\n" + content
	}
	return content, nil
}

type writeTool struct{}

func Write() core.Tool { return &writeTool{} }

func (writeTool) Name() string { return "write" }

func (writeTool) Description() string {
	return "create or overwrite a file with the full content. Guidelines: new files and whole rewrites; a change inside an existing file -> edit. Reply: the path written; a plugin you author lands in plugins/pending/ for the operator to approve."
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

type editTool struct{}

func Edit() core.Tool { return &editTool{} }

func (editTool) Name() string { return "edit" }

func (editTool) Description() string {
	return "replace exactly one occurrence of old with new in a file. Guidelines: the precise change; read first and make old unique with context — a file you never read this session, an ambiguous or missing old, or one changed since your read, refuses by name. Reply: the path edited."
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
			return "", fmt.Errorf("edit: %s was never read this session: read (or write) it first", a.Path)
		}
	}

	fileData, err := os.ReadFile(a.Path)
	if err != nil {
		return "", fmt.Errorf("edit: %w", err)
	}

	if recorded, seen := stateOf(ctx, a.Path); seen {
		sum := sha256.Sum256(fileData)
		if recorded.Hash != hex.EncodeToString(sum[:]) || recorded.Mtime != mtimeOf(a.Path) {
			s, _ := core.SessionFrom(ctx)
			return "", fmt.Errorf("%s", driftRefusal(s, a.Path, string(fileData)))
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

func driftRefusal(s *core.Session, path, onDisk string) string {
	const header = "edit: the file changed since the read:"
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
