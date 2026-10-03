package graph

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mrsirg97-rgb/rig/v2/store"
	"github.com/mrsirg97-rgb/rig/v2/store/graph/ddl"
	"github.com/mrsirg97-rgb/rig/v2/store/graph/metadata"
)

const SchemaVersion = 1

func DDL() []string { return ddl.Statements() }

func Statements() []string {
	return append(ddl.Statements(), metadata.ExtraStatements()...)
}

func FilePath(home, key, worktree string) string {
	return filepath.Join(home, "graph", key, worktree+".sqlite")
}

func Open(home, key, worktree string) (store.DB, error) {
	path := FilePath(home, key, worktree)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return store.DB{}, fmt.Errorf("graph: mkdir: %w", err)
	}
	db, _, _, err := store.Open(path, Statements(), SchemaVersion)
	if err != nil {
		return store.DB{}, fmt.Errorf("graph: open %s: %w", path, err)
	}
	return db, nil
}

func Apply(ctx context.Context, db store.DB, rel, sha, language string, r Result) (bool, error) {
	_, tx, err := db.Tx(ctx)
	if err != nil {
		return false, fmt.Errorf("graph: tx for %s: %w", rel, err)
	}
	defer func() { _ = tx.Rollback() }()

	var cur string
	qerr := tx.QueryRowContext(ctx, `SELECT sha256 FROM files WHERE path = ?`, rel).Scan(&cur)
	if qerr == nil && cur == sha {
		return false, nil
	}
	if qerr != nil && qerr != sql.ErrNoRows {
		return false, fmt.Errorf("graph: sha of %s: %w", rel, qerr)
	}

	edgesSha := sql.NullString{}
	if r.Eager {
		edgesSha = sql.NullString{String: sha, Valid: true}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO files (path, sha256, language, edges_sha) VALUES (?, ?, ?, ?)
ON CONFLICT(path) DO UPDATE SET sha256 = excluded.sha256, language = excluded.language, edges_sha = excluded.edges_sha`,
		rel, sha, language, edgesSha); err != nil {
		return false, fmt.Errorf("graph: files upsert %s: %w", rel, err)
	}

	keep := map[string]bool{}
	for _, s := range r.Symbols {
		keep[s.Package+"\x00"+s.Name] = true
	}
	stale, err := tx.QueryContext(ctx, `SELECT package, name FROM symbols WHERE file = ?`, rel)
	if err != nil {
		return false, fmt.Errorf("graph: symbols of %s: %w", rel, err)
	}
	var gone [][2]string
	for stale.Next() {
		var p, n string
		if err := stale.Scan(&p, &n); err != nil {
			stale.Close()
			return false, fmt.Errorf("graph: symbols scan of %s: %w", rel, err)
		}
		if !keep[p+"\x00"+n] {
			gone = append(gone, [2]string{p, n})
		}
	}
	stale.Close()
	if err := stale.Err(); err != nil {
		return false, fmt.Errorf("graph: symbols of %s: %w", rel, err)
	}
	for _, g := range gone {
		if _, err := tx.ExecContext(ctx, `DELETE FROM symbols WHERE package = ? AND name = ?`, g[0], g[1]); err != nil {
			return false, fmt.Errorf("graph: symbol delete %s: %w", rel, err)
		}
	}
	for _, s := range r.Symbols {
		if _, err := tx.ExecContext(ctx, `INSERT INTO symbols (package, name, kind, file, line, end_line) VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(package, name) DO UPDATE SET kind = excluded.kind, file = excluded.file, line = excluded.line, end_line = excluded.end_line`,
			s.Package, s.Name, s.Kind, s.File, s.Line, s.EndLine); err != nil {
			return false, fmt.Errorf("graph: symbol upsert %s: %w", rel, err)
		}
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM edges WHERE file = ?`, rel); err != nil {
		return false, fmt.Errorf("graph: edges clear of %s: %w", rel, err)
	}
	for _, e := range r.Edges {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO edges (from_package, from_name, to_package, to_name, file, line) VALUES (?, ?, ?, ?, ?, ?)`,
			e.FromPackage, e.FromName, e.ToPackage, e.ToName, rel, e.Line); err != nil {
			return false, fmt.Errorf("graph: edge insert %s: %w", rel, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("graph: commit %s: %w", rel, err)
	}
	return true, nil
}
