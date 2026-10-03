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

const editChunkCap = 32

type editChunk struct {
	Old string `json:"old"`
	New string `json:"new"`
}

type editTool struct{ tool.Definition }

func Edit() core.Tool { return &editTool{tool.Def("edit")} }

type editArgs struct {
	Path  string      `json:"path"`
	Edits []editChunk `json:"edits"`
}

func (editTool) Exec(ctx context.Context, data json.RawMessage) (string, error) {
	var a editArgs
	if err := strictDecode(data, &a); err != nil {
		return "", fmt.Errorf("edit: args: %w", err)
	}
	a.Path = normalizePath(a.Path)
	if len(a.Edits) == 0 {
		return "", errors.New("edit: the edits list is empty")
	}
	if len(a.Edits) > editChunkCap {
		return "", fmt.Errorf("edit: %d chunks is over the %d-chunk bound; split the call", len(a.Edits), editChunkCap)
	}
	total := 0
	for i, h := range a.Edits {
		total += len(h.Old) + len(h.New)
		if h.Old == "" {
			return "", fmt.Errorf("edit: chunk %d of %d: zero-width old string", i+1, len(a.Edits))
		}
	}
	if total >= ReadCap {
		return "", fmt.Errorf("edit: the chunks total %d bytes, the read ceiling is %d; split the call", total, ReadCap)
	}

	fileData, err := os.ReadFile(a.Path)
	if err != nil {
		return "", fmt.Errorf("edit: %w", err)
	}
	sum := sha256.Sum256(fileData)

	_, threaded := core.SessionFrom(ctx)
	recorded, seen := stateOf(ctx, a.Path)
	fresh := threaded && !seen
	if seen {
		if recorded.Hash != hex.EncodeToString(sum[:]) || recorded.Mtime != mtimeOf(a.Path) {
			return "", fmt.Errorf("%s", driftRefusal(ctx, a.Path, string(fileData)))
		}
	}

	updated, miss := applyChunks(string(fileData), a.Edits)
	if miss != nil {
		if fresh {
			return unreadObservation(ctx, a.Path)
		}
		if miss.count == 0 {
			return "", fmt.Errorf("edit: chunk %d of %d: old matched 0 times (absent from the file as the earlier chunks leave it); nothing landed", miss.index+1, len(a.Edits))
		}
		return "", fmt.Errorf("edit: chunk %d of %d: old matched %d times, want exactly 1; nothing landed", miss.index+1, len(a.Edits), miss.count)
	}

	if err := os.WriteFile(a.Path, []byte(updated), 0o644); err != nil {
		return "", fmt.Errorf("edit: %w", err)
	}
	recordState(ctx, a.Path, []byte(updated))
	s, _ := core.SessionFrom(ctx)
	rememberContent(s, a.Path, updated)

	var b strings.Builder
	replaced := 0
	for i, h := range a.Edits {
		fmt.Fprintf(&b, "chunk %d: replaced %d byte(s)\n", i+1, len(h.Old))
		replaced += len(h.Old)
	}
	fmt.Fprintf(&b, "edited %s: replaced %d byte(s)", a.Path, replaced)
	return b.String(), nil
}

type chunkMiss struct {
	index int
	count int
}

func applyChunks(content string, chunks []editChunk) (string, *chunkMiss) {
	updated := content
	for i, h := range chunks {
		count := strings.Count(updated, h.Old)
		if count != 1 {
			return content, &chunkMiss{index: i, count: count}
		}
		updated = strings.Replace(updated, h.Old, h.New, 1)
	}
	return updated, nil
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
