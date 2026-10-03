package graph

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mrsirg97-rgb/rig/v2/store"
	"github.com/mrsirg97-rgb/rig/v2/store/graph/ddl"
	"github.com/mrsirg97-rgb/rig/v2/store/graph/metadata"
)

const SchemaVersion = 2

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
	db, _, _, err := store.Open(path, Statements(), SchemaVersion, Migration())
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
		if err := deleteLexical(ctx, tx, g[0], g[1]); err != nil {
			return false, fmt.Errorf("graph: symbol lexical delete %s: %w", rel, err)
		}
	}
	for _, s := range r.Symbols {
		if _, err := tx.ExecContext(ctx, `INSERT INTO symbols (package, name, kind, file, line, end_line) VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(package, name) DO UPDATE SET kind = excluded.kind, file = excluded.file, line = excluded.line, end_line = excluded.end_line`,
			s.Package, s.Name, s.Kind, s.File, s.Line, s.EndLine); err != nil {
			return false, fmt.Errorf("graph: symbol upsert %s: %w", rel, err)
		}
		if err := insertLexical(ctx, tx, s); err != nil {
			return false, fmt.Errorf("graph: symbol lexical insert %s: %w", rel, err)
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

func deleteLexical(ctx context.Context, tx *sql.Tx, pkg, name string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM symbol_fts WHERE package = ? AND name = ?`, pkg, name); err != nil {
		return fmt.Errorf("graph: symbol fts delete: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM symbol_grams WHERE package = ? AND name = ?`, pkg, name); err != nil {
		return fmt.Errorf("graph: symbol grams delete: %w", err)
	}
	return nil
}

func insertLexical(ctx context.Context, tx *sql.Tx, s Symbol) error {
	if err := deleteLexical(ctx, tx, s.Package, s.Name); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO symbol_fts (package, name, kind, file, line, end_line) VALUES (?, ?, ?, ?, ?, ?)`,
		s.Package, s.Name, s.Kind, s.File, s.Line, s.EndLine); err != nil {
		return fmt.Errorf("graph: symbol fts insert: %w", err)
	}
	grams := symbolGrams(s)
	places := make([]string, len(grams))
	args := make([]any, 0, len(grams)*7)
	for i, gram := range grams {
		places[i] = fmt.Sprintf("($%d, $%d, $%d, $%d, $%d, $%d, $%d)", i*7+1, i*7+2, i*7+3, i*7+4, i*7+5, i*7+6, i*7+7)
		args = append(args, s.Package, s.Name, s.Kind, s.File, s.Line, s.EndLine, gram)
	}
	if len(places) > 0 {
		gramSQL := fmt.Sprintf(`INSERT OR IGNORE INTO symbol_grams (package, name, kind, file, line, end_line, gram) VALUES %s`, strings.Join(places, ", "))
		if _, err := tx.ExecContext(ctx, gramSQL, args...); err != nil {
			return fmt.Errorf("graph: symbol grams insert: %w", err)
		}
	}
	return nil
}
