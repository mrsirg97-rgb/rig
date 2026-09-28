package file

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"unicode/utf8"

	"github.com/mrsirg97-rgb/rig/v2/core"
	difftool "github.com/mrsirg97-rgb/rig/v2/tool/diff"
)

const readCap = 1 << 20

const readChunk = 64 * 1024

type readTool struct{}

func Read() core.Tool { return &readTool{} }

func (readTool) Name() string { return "read" }

func (readTool) Description() string {
	return "read (with offset/limit for a range), not cat or sed, for any file you may edit. Guidelines: the edit is drift-checked against what you read and a bash read leaves no observation; diff: true appends the file's git diff against HEAD, or 'no changes' when clean. Reply: the text; a range past the end refuses by name."
}

func (readTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path":   {"type": "string", "description": "the file to read"},
			"offset": {"type": "integer", "description": "the 0-based line to start at (default 0); past the end refuses"},
			"limit":  {"type": "integer", "description": "the number of lines to read (default the rest of the file); negative refuses"},
			"diff":   {"type": "boolean", "description": "append the file's git diff against HEAD, or 'no changes' when clean (a non-git cwd refuses)"}
		},
		"required": ["path"]
	}`)
}

type readArgs struct {
	Path   string `json:"path"`
	Offset *int   `json:"offset"`
	Limit  *int   `json:"limit"`
	Diff   bool   `json:"diff"`
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
	content, total, sum, err := readWindow(a.Path, offset, limit)
	if err != nil {
		return "", fmt.Errorf("read: %w", err)
	}
	if offset >= total {
		return "", fmt.Errorf("read: offset %d is past the end (%d lines)", offset, total)
	}
	stale := false
	if recorded, seen := stateOf(ctx, a.Path); seen {
		if recorded.Hash != hex.EncodeToString(sum[:]) || recorded.Mtime != mtimeOf(a.Path) {
			stale = true
		}
	}
	recordDigest(ctx, a.Path, sum)
	s, _ := core.SessionFrom(ctx)
	rememberContent(s, a.Path, content)
	if len(content) > readCap {
		cut := readCap
		for !utf8.RuneStart(content[cut]) {
			cut--
		}
		content = content[:cut] + "\n[output truncated]"
	}
	if stale {
		content = "[changed since your observation] " + a.Path + " — re-read before acting on it\n" + content
	}
	if a.Diff {
		d, err := difftool.Files(ctx, "HEAD", []string{a.Path})
		if err != nil {
			return "", fmt.Errorf("read: diff: %w", err)
		}
		content = content + "\n\n" + d
	}
	return content, nil
}

func readWindow(path string, offset, limit int) (string, int, [32]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, [32]byte{}, err
	}
	defer f.Close()
	h := sha256.New()
	lr := lineReader{r: bufio.NewReaderSize(io.TeeReader(f, h), readChunk), cap: readCap + 1}
	var window []byte
	appendWindow := func(b []byte) {
		if room := readCap + 1 - len(window); len(b) >= room {
			window = append(window, b[:room]...)
		} else {
			window = append(window, b...)
		}
	}
	first := true
	seen := 0
	for {
		line, ok := lr.next()
		if !ok {
			break
		}
		seen++
		if seen <= offset {
			continue
		}
		if limit >= 0 && seen > offset+limit {
			continue
		}
		if !first {
			appendWindow([]byte{'\n'})
		}
		first = false
		appendWindow(line)
	}
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return string(window), seen, sum, nil
}

type lineReader struct {
	r       *bufio.Reader
	cap     int
	buf     []byte
	done    bool
	endedNl bool
	sawAny  bool
}

func (lr *lineReader) next() ([]byte, bool) {
	if lr.done {
		return nil, false
	}
	lr.buf = lr.buf[:0]
	buf := lr.buf
	for {
		frag, err := lr.r.ReadSlice('\n')
		n := len(frag)
		delim := n > 0 && frag[n-1] == '\n'
		if delim {
			frag = frag[:n-1]
		}
		if n > 0 {
			if room := lr.cap - len(buf); room > 0 {
				if len(frag) >= room {
					buf = append(buf, frag[:room]...)
				} else {
					buf = append(buf, frag...)
				}
			}
		}
		if err == nil {
			lr.buf = buf
			lr.endedNl = true
			lr.sawAny = true
			return buf, true
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		lr.done = true
		if n > 0 {
			lr.buf = buf
			return buf, true
		}
		if lr.endedNl || !lr.sawAny {
			lr.buf = buf
			return buf, true
		}
		return nil, false
	}
}
