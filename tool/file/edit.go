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

const editHunkCap = 32

type editHunk struct {
	Old string `json:"old"`
	New string `json:"new"`
}

type editTool struct{}

func Edit() core.Tool { return &editTool{} }

func (editTool) Name() string { return "edit" }

func (editTool) Description() string {
	return "Updates the content of an existing file, replacing exact text. Guidelines: put enough of the file in each old to match exactly once; several changes to one file go in one call, applied in order, all or none. On a file you have not read this session, a hunk that does not match once comes back as the file's text instead of a refusal, and the next call edits it. On a file you have read, the call refuses and names why: a hunk matched never or more than once as the earlier hunks leave it, or the file changed since your read. Reply: one line per hunk, then the path and total bytes replaced."
}

func (editTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path":  {"type": "string", "description": "the file to edit"},
			"edits": {
				"type": "array",
				"description": "the changes, applied in order; a single change is a list of one",
				"items": {
					"type": "object",
					"properties": {
						"old": {"type": "string", "description": "the exact text to replace; must occur exactly once as the earlier hunks leave it"},
						"new": {"type": "string", "description": "the replacement text"}
					},
					"required": ["old", "new"]
				}
			}
		},
		"required": ["path", "edits"]
	}`)
}

type editArgs struct {
	Path  string     `json:"path"`
	Edits []editHunk `json:"edits"`
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
	if len(a.Edits) > editHunkCap {
		return "", fmt.Errorf("edit: %d hunks is over the %d-hunk bound; split the call", len(a.Edits), editHunkCap)
	}
	total := 0
	for i, h := range a.Edits {
		total += len(h.Old) + len(h.New)
		if h.Old == "" {
			return "", fmt.Errorf("edit: hunk %d of %d: zero-width old string", i+1, len(a.Edits))
		}
	}
	if total >= readCap {
		return "", fmt.Errorf("edit: the hunks total %d bytes, the read ceiling is %d; split the call", total, readCap)
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

	updated, miss := applyHunks(string(fileData), a.Edits)
	if miss != nil {
		if fresh {
			return unreadObservation(ctx, a.Path)
		}
		if miss.count == 0 {
			return "", fmt.Errorf("edit: hunk %d of %d: old matched 0 times (absent from the file as the earlier hunks leave it); nothing landed", miss.index+1, len(a.Edits))
		}
		return "", fmt.Errorf("edit: hunk %d of %d: old matched %d times, want exactly 1; nothing landed", miss.index+1, len(a.Edits), miss.count)
	}

	if now, err := os.ReadFile(a.Path); err != nil {
		return "", fmt.Errorf("edit: %w", err)
	} else if nowSum := sha256.Sum256(now); nowSum != sum {
		return "", fmt.Errorf("edit: %s changed on disk mid-call: the validated bytes are sha256 %s, the bytes on disk now are sha256 %s; nothing landed", a.Path, digestShort(sum), digestShort(nowSum))
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
		fmt.Fprintf(&b, "hunk %d: replaced %d byte(s)\n", i+1, len(h.Old))
		replaced += len(h.Old)
	}
	fmt.Fprintf(&b, "edited %s: replaced %d byte(s)", a.Path, replaced)
	return b.String(), nil
}

type hunkMiss struct {
	index int
	count int
}

func applyHunks(content string, hunks []editHunk) (string, *hunkMiss) {
	updated := content
	for i, h := range hunks {
		count := strings.Count(updated, h.Old)
		if count != 1 {
			return content, &hunkMiss{index: i, count: count}
		}
		updated = strings.Replace(updated, h.Old, h.New, 1)
	}
	return updated, nil
}

func digestShort(sum [32]byte) string {
	return hex.EncodeToString(sum[:])[:12]
}

func unreadObservation(ctx context.Context, path string) (string, error) {
	content, total, sum, err := readWindow(path, 0, -1)
	if err != nil {
		return "", fmt.Errorf("edit: %w", err)
	}
	recordDigest(ctx, path, sum)
	s, _ := core.SessionFrom(ctx)
	rememberContent(s, path, content)
	if len(content) > readCap {
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
