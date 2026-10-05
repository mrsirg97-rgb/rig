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
	"github.com/mrsirg97-rgb/rig/v2/tool"
	difftool "github.com/mrsirg97-rgb/rig/v2/tool/diff"
)

const driftCap = 20

type editTool struct{ tool.Definition }

func Edit() core.Tool { return &editTool{tool.Def("edit")} }

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
		return "", errors.New("edit: old is empty; give the text to replace")
	}
	if total := len(a.Old) + len(a.New); total >= ReadCap {
		return "", fmt.Errorf("edit: old plus new is %d bytes, the read ceiling is %d; split the change across calls", total, ReadCap)
	}

	fileData, err := os.ReadFile(a.Path)
	if err != nil {
		return "", fmt.Errorf("edit: %w", err)
	}
	content := string(fileData)
	sum := sha256.Sum256(fileData)

	_, threaded := core.SessionFrom(ctx)
	recorded, seen := stateOf(ctx, a.Path)
	fresh := threaded && !seen
	if seen {
		if recorded.Hash != hex.EncodeToString(sum[:]) || recorded.Mtime != mtimeOf(a.Path) {
			return "", fmt.Errorf("%s", driftRefusal(ctx, a.Path, content))
		}
	}

	if count := strings.Count(content, a.Old); count != 1 {
		if fresh {
			return unreadObservation(ctx, a.Path)
		}
		if count == 0 {
			return "", errors.New("edit: old matched 0 times; nothing landed")
		}
		return "", fmt.Errorf("edit: old matched %d times, want exactly 1; nothing landed", count)
	}
	updated := strings.Replace(content, a.Old, a.New, 1)

	if err := os.WriteFile(a.Path, []byte(updated), 0o644); err != nil {
		return "", fmt.Errorf("edit: %w", err)
	}
	recordState(ctx, a.Path, []byte(updated))
	s, _ := core.SessionFrom(ctx)
	rememberContent(s, a.Path, updated)
	return fmt.Sprintf("edited %s: replaced %d byte(s)", a.Path, len(a.Old)), nil
}

func unreadObservation(ctx context.Context, path string) (string, error) {
	content, total, sum, err := readWindow(path, 0, -1)
	if err != nil {
		return "", fmt.Errorf("edit: %w", err)
	}
	recordDigest(ctx, path, sum)
	s, _ := core.SessionFrom(ctx)
	rememberContent(s, path, content)
	if len(content) > ReadCap {
		content = capReply(content, total, 0)
	}
	return content + "\n[edit: " + path + " was not read this session; its text is above, now edit it]", nil
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
