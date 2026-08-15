// Package fs provides named filesystem tools: ls, find, grep. Named tools
// with small schemas are what a local model reaches for; `bash grep` is
// where it fumbles quoting. Stdlib only: WalkDir, regexp, path.Match.
package fs

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/mrsirg97-rgb/looper/core"
)

// Hard result caps. Truncation is named in the output, never silent;
// matches beyond a cap count, not accumulate, so memory stays bounded.
const (
	lsCap   = 1000 // entries
	findCap = 1000 // paths
	grepCap = 500  // match lines
	lineCap = 512  // result bytes per matched line
)

const binaryPeek = 8 << 10 // a NUL byte within the first 8 KiB marks binary

// lsArgs names one directory level.
type lsArgs struct {
	Path   string `json:"path"`
	Hidden bool   `json:"hidden"`
}

// findArgs names a glob under a root.
type findArgs struct {
	Pattern string `json:"pattern"`
	Root    string `json:"root"`
}

// grepArgs names a regexp under a root, optionally path-restricted.
type grepArgs struct {
	Pattern string `json:"pattern"`
	Root    string `json:"root"`
	Glob    string `json:"glob"`
}

// tool is the shared shape: name, description, hand-authored schema, exec.
type tool struct {
	name        string
	description string
	schema      json.RawMessage
	exec        func(ctx context.Context, args json.RawMessage) (string, error)
}

func (t *tool) Name() string { return t.name }

func (t *tool) Description() string { return t.description }

func (t *tool) Schema() json.RawMessage { return t.schema }

func (t *tool) Exec(ctx context.Context, args json.RawMessage) (string, error) {
	return t.exec(ctx, args)
}

// LS lists one directory level.
func LS() core.Tool {
	return &tool{
		name:        "ls",
		description: "list one level of a directory: kind, name, and size",
		schema:      json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"directory to list (default .)"},"hidden":{"type":"boolean","description":"include dot-entries"}}}`),
		exec:        lsExec,
	}
}

// Find searches by glob name under a root; ** spans directories.
func Find() core.Tool {
	return &tool{
		name:        "find",
		description: "find files by name or path glob under a root; bare patterns match the name, ** spans directories",
		schema:      json.RawMessage(`{"type":"object","properties":{"pattern":{"type":"string","description":"a name glob (matches the file name) or a path glob with / (** spans directories)"},"root":{"type":"string","description":"root to search (default .)"}},"required":["pattern"]}`),
		exec:        findExec,
	}
}

// Grep searches file lines under a root with a Go regexp.
func Grep() core.Tool {
	return &tool{
		name:        "grep",
		description: "search file lines under a root with a Go regexp; prints path:line: text",
		schema:      json.RawMessage(`{"type":"object","properties":{"pattern":{"type":"string","description":"Go regexp matched per line"},"root":{"type":"string","description":"root to search (default .)"},"glob":{"type":"string","description":"restrict matches to a path glob"}},"required":["pattern"]}`),
		exec:        grepExec,
	}
}

func lsExec(_ context.Context, data json.RawMessage) (string, error) {
	var a lsArgs
	if err := strictDecode(data, &a); err != nil {
		return "", err
	}
	dir, err := filepath.Abs(a.pathOr("."))
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("ls: %v", err)
	}
	var lines []string
	for _, e := range entries {
		if !a.Hidden && strings.HasPrefix(e.Name(), ".") {
			continue
		}
		line := kindOf(e) + " " + e.Name()
		if e.Type().IsRegular() {
			if info, err := e.Info(); err == nil {
				line += "\t" + strconv.FormatInt(info.Size(), 10)
			}
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return "(empty)", nil
	}
	if total := len(lines); total > lsCap {
		lines = append(lines[:lsCap], fmt.Sprintf("[truncated: %d of %d]", lsCap, total))
	}
	return strings.Join(lines, "\n"), nil
}

func findExec(ctx context.Context, data json.RawMessage) (string, error) {
	var a findArgs
	if err := strictDecode(data, &a); err != nil {
		return "", err
	}
	if a.Pattern == "" {
		return "", fmt.Errorf("find: pattern required")
	}
	root, err := filepath.Abs(a.pathOr("."))
	if err != nil {
		return "", err
	}
	var matched []string
	total, skipped := 0, 0
	skipped, err = walk(root, func(rel string, d fs.DirEntry) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if !d.IsDir() && findMatch(rel, a.Pattern) {
			total++
			if len(matched) < findCap {
				matched = append(matched, rel)
			}
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("find: %v", err)
	}
	out := matched
	if total == 0 {
		out = []string{"(no matches)"}
	}
	if total > findCap {
		out = append(out, fmt.Sprintf("[truncated: %d of %d]", findCap, total))
	}
	if skipped > 0 {
		out = append(out, fmt.Sprintf("[skipped: %d unreadable]", skipped))
	}
	return strings.Join(out, "\n"), nil
}

func grepExec(ctx context.Context, data json.RawMessage) (string, error) {
	var a grepArgs
	if err := strictDecode(data, &a); err != nil {
		return "", err
	}
	if a.Pattern == "" {
		return "", fmt.Errorf("grep: pattern required")
	}
	re, err := regexp.Compile(a.Pattern)
	if err != nil {
		return "", fmt.Errorf("grep: pattern: %v", err)
	}
	root, err := filepath.Abs(a.pathOr("."))
	if err != nil {
		return "", err
	}
	var lines []string
	total, skipped := 0, 0
	skipped, err = walk(root, func(rel string, d fs.DirEntry) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		if a.Glob != "" && !globMatch(rel, a.Glob) {
			return nil
		}
		f, err := os.Open(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			return nil // unreadable: skipped, walk continues
		}
		defer f.Close()
		peek := make([]byte, binaryPeek)
		n, err := io.ReadFull(io.LimitReader(f, binaryPeek), peek)
		if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
			return nil
		}
		if bytes.IndexByte(peek[:n], 0) >= 0 {
			return nil // binary: skipped
		}
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return err
		}
		rd := bufio.NewReader(f)
		for ln := 1; ; ln++ {
			line, rerr := rd.ReadBytes('\n')
			if len(line) > 0 {
				text := strings.TrimRight(string(line), "\r\n")
				if re.MatchString(text) {
					total++
					if len(lines) < grepCap {
						shown := text
						if len(shown) > lineCap {
							shown = shown[:lineCap] + " [line truncated]"
						}
						lines = append(lines, fmt.Sprintf("%s:%d: %s", rel, ln, shown))
					}
				}
			}
			if rerr != nil {
				if rerr != io.EOF {
					return fmt.Errorf("grep: %s: %v", rel, rerr)
				}
				break
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	out := lines
	if total == 0 {
		out = []string{"(no matches)"}
	}
	if total > grepCap {
		out = append(out, fmt.Sprintf("[truncated: %d of %d]", grepCap, total))
	}
	if skipped > 0 {
		out = append(out, fmt.Sprintf("[skipped: %d unreadable]", skipped))
	}
	return strings.Join(out, "\n"), nil
}

func (a lsArgs) pathOr(def string) string {
	if a.Path != "" {
		return a.Path
	}
	return def
}

func (a findArgs) pathOr(def string) string {
	if a.Root != "" {
		return a.Root
	}
	return def
}

func (a grepArgs) pathOr(def string) string {
	if a.Root != "" {
		return a.Root
	}
	return def
}

// walk visits every entry under root, root-relative and slash-separated,
// pruning .git subtrees. WalkDir does not follow symlinks.
// walk visits every entry under root, root-relative and slash-separated,
// pruning .git subtrees. Unreadable entries are skipped, counted, and named
// in the result as [skipped: N unreadable]; WalkDir does not follow symlinks.
func walk(root string, visit func(rel string, d fs.DirEntry) error) (skipped int, err error) {
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, werr error) error {
		if werr != nil {
			skipped++
			if d != nil && d.IsDir() {
				return fs.SkipDir // unreadable tree: counted, pruned
			}
			return nil // unreadable entry: counted, walk continues
		}
		if d.IsDir() && d.Name() == ".git" {
			return fs.SkipDir
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return rerr
		}
		if rel == "." {
			return nil
		}
		return visit(filepath.ToSlash(rel), d)
	})
	return
}

// findMatch: a bare pattern (no / and no **) names the file name itself, the
// find -name reading; anything else globs the root-relative path.
func findMatch(rel, pattern string) bool {
	if !strings.Contains(pattern, "/") && !strings.Contains(pattern, "**") {
		ok, _ := filepath.Match(pattern, filepath.Base(rel))
		return ok
	}
	return globMatch(rel, pattern)
}

// globMatch reports whether the slash-separated relative path rel matches
// pattern; a ** segment spans zero or more segments, everything else is
// path.Match. Recursive over pattern segments, bounded by their count.
func globMatch(rel, pattern string) bool {
	seg := func(s string) []string { return strings.Split(s, "/") }
	ps, qs := seg(pattern), seg(rel)
	var m func(i, j int) bool
	m = func(i, j int) bool {
		for i < len(ps) {
			if ps[i] == "**" {
				if m(i+1, j) {
					return true
				}
				if j < len(qs) {
					j++
					continue
				}
				return false
			}
			if j >= len(qs) {
				return false
			}
			ok, _ := filepath.Match(ps[i], qs[j])
			if !ok {
				return false
			}
			i++
			j++
		}
		return j == len(qs)
	}
	return m(0, 0)
}

func kindOf(e fs.DirEntry) string {
	switch {
	case e.IsDir():
		return "d"
	case e.Type().IsRegular():
		return "f"
	case e.Type()&fs.ModeSymlink != 0:
		return "l"
	default:
		return "-"
	}
}

func strictDecode(data json.RawMessage, out any) error {
	if len(bytes.TrimSpace(data)) == 0 {
		data = json.RawMessage(`{}`)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	return dec.Decode(out)
}
