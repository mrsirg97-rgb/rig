package graph

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/mrsirg97-rgb/rig/v2/store"
)

func (q *Queue) refresh(ctx context.Context, db store.DB, root, rel string) (bool, error) {
	abs := filepath.Join(root, filepath.FromSlash(rel))
	live, err := sha256File(abs)
	if err != nil {
		return false, fmt.Errorf("graph: %s: %w", rel, err)
	}
	var stored string
	if err := db.QueryRowContext(ctx, `SELECT sha256 FROM files WHERE path = ?`, rel).Scan(&stored); err != nil {
		if err != sql.ErrNoRows {
			return false, fmt.Errorf("graph: sha of %s: %w", rel, err)
		}
	}
	if stored == live {
		return false, nil
	}
	return true, q.index(ctx, db, root, abs)
}

type liveLine struct {
	n    int64
	text string
}

func liveLines(root, rel string, start, end int64) ([]liveLine, error) {
	if end < start {
		end = start
	}
	f, err := os.Open(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return nil, fmt.Errorf("graph: %s: %w", rel, err)
	}
	defer f.Close()
	var out []liveLine
	var size int64
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), ReadCap)
	var n int64
	for sc.Scan() {
		n++
		if n < start {
			continue
		}
		text := sc.Text()
		out = append(out, liveLine{n, text})
		size += int64(len(text))
		if n >= end || size >= ReadCap {
			break
		}
	}
	return out, sc.Err()
}

func sigLines(root, rel string, start, end int64) ([]liveLine, error) {
	lines, err := liveLines(root, rel, start, end)
	if err != nil {
		return nil, err
	}
	for i, l := range lines {
		if strings.Count(l.text, "{") > strings.Count(l.text, "}") {
			return lines[:i+1], nil
		}
	}
	return lines, nil
}

func coverage(ctx context.Context, db store.DB, root string, b *strings.Builder) {
	mapped := map[string]bool{}
	rows, err := db.QueryContext(ctx, `SELECT path FROM files`)
	if err == nil {
		for rows.Next() {
			var p string
			if rows.Scan(&p) == nil {
				mapped[path.Dir(p)] = true
			}
		}
		rows.Close()
	}
	present := map[string]bool{}
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != root && Skip(p) {
				return filepath.SkipDir
			}
			return nil
		}
		if LanguageOf(p) != "" {
			rel, rerr := filepath.Rel(root, p)
			if rerr == nil {
				present[path.Dir(filepath.ToSlash(rel))] = true
			}
		}
		return nil
	})
	fmt.Fprintf(b, "coverage: %d of %d packages mapped\n", len(mapped), len(present))
}
