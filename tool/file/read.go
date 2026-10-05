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
	"strings"
	"unicode/utf8"

	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/tool"
	difftool "github.com/mrsirg97-rgb/rig/v2/tool/diff"
)

const ReadCap = 1 << 20

const readChunk = 64 * 1024

type Read interface {
	tool.Definition
	Exec(ctx context.Context, args json.RawMessage) (string, error)

	Read(ctx context.Context, path string, offset, limit *int, diff bool) (string, error)
}

type readTool struct{ tool.Definition }

func NewRead() Read { return &readTool{tool.Def("read")} }

type readArgs struct {
	Path   string `json:"path"`
	Offset *int   `json:"offset"`
	Limit  *int   `json:"limit"`
	Diff   bool   `json:"diff"`
}

func (t readTool) Exec(ctx context.Context, data json.RawMessage) (string, error) {
	var a readArgs
	if err := strictDecode(data, &a); err != nil {
		return "", fmt.Errorf("read: args: %w", err)
	}
	return t.Read(ctx, a.Path, a.Offset, a.Limit, a.Diff)
}

func (readTool) Read(ctx context.Context, path string, offset, limit *int, diff bool) (string, error) {
	path = normalizePath(path)
	var start int
	if offset != nil {
		if *offset < 0 {
			return "", fmt.Errorf("read: offset %d is negative", *offset)
		}
		start = *offset
	}
	window := -1
	if limit != nil {
		if *limit < 0 {
			return "", fmt.Errorf("read: limit %d is negative", *limit)
		}
		window = *limit
	}
	content, total, sum, err := readWindow(path, start, window)
	if err != nil {
		return "", fmt.Errorf("read: %w", err)
	}
	if start >= total {
		return "", fmt.Errorf("read: offset %d is past the end (%d lines)", start, total)
	}
	stale := false
	if recorded, seen := stateOf(ctx, path); seen {
		if recorded.Hash != hex.EncodeToString(sum[:]) || recorded.Mtime != mtimeOf(path) {
			stale = true
		}
	}
	recordDigest(ctx, path, sum)
	s, _ := core.SessionFrom(ctx)
	rememberContent(s, path, content)
	if len(content) > ReadCap {
		content = capReply(content, total, start)
	}
	if stale {
		content = "[changed since your observation] " + path + " — re-read before acting on it\n" + content
	}
	if diff {
		d, err := difftool.Files(ctx, "HEAD", []string{path})
		if err != nil {
			return "", fmt.Errorf("read: diff: %w", err)
		}
		content = content + "\n\n" + d
	}
	return content, nil
}

func capReply(content string, total, offset int) string {
	cut := strings.LastIndexByte(content[:ReadCap], '\n')
	if cut < 0 {
		cut = ReadCap
		for !utf8.RuneStart(content[cut]) {
			cut--
		}
		return content[:cut] + fmt.Sprintf("\n[output truncated: line %d is longer than the 1 MiB cap; slice it with bash]", offset+1)
	}
	cut++
	lines := strings.Count(content[:cut], "\n")
	return content[:cut] + fmt.Sprintf("\n[output truncated: %d of %d lines; continue at offset %d]", lines, total, offset+lines)
}

func readWindow(path string, offset, limit int) (string, int, [32]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, [32]byte{}, err
	}
	defer f.Close()
	h := sha256.New()
	lr := lineReader{r: bufio.NewReaderSize(io.TeeReader(f, h), readChunk), cap: ReadCap + 1}
	var window []byte
	appendWindow := func(b []byte) {
		if room := ReadCap + 1 - len(window); len(b) >= room {
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
