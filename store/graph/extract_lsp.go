package graph

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mrsirg97-rgb/rig/v2/store"
)

func (q *Queue) resolveRefs(ctx context.Context, c *lspClient, db store.DB, root string, sym symRow, force bool) error {
	key := "refs:" + sym.Package + ":" + sym.Name
	abs := filepath.Join(root, filepath.FromSlash(sym.File))
	live, err := sha256File(abs)
	if err != nil {
		return fmt.Errorf("graph: %s: %w", sym.File, err)
	}
	if !force {
		var val string
		if err := db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = ?`, key).Scan(&val); err == nil && val == live {
			return nil
		}
	}
	locs, err := c.references(ctx, abs, sym.Line, sym.Name)
	if err != nil {
		return err
	}
	_, tx, err := db.Tx(ctx)
	if err != nil {
		return fmt.Errorf("graph: tx for %s: %w", sym.File, err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM edges WHERE to_package = ? AND to_name = ?`, sym.Package, sym.Name); err != nil {
		return fmt.Errorf("graph: refs clear of %s: %w", sym.Name, err)
	}
	for _, loc := range locs {
		absRef, err := lspPath(loc.URI)
		if err != nil {
			return fmt.Errorf("graph: lsp location %s: %w", loc.URI, err)
		}
		relRef, err := filepath.Rel(root, absRef)
		if err != nil || strings.HasPrefix(relRef, "..") {
			continue
		}
		relRef = filepath.ToSlash(relRef)
		line := int64(loc.Range.Start.Line + 1)
		var fromPkg, fromName string
		if err := tx.QueryRowContext(ctx, `SELECT package, name FROM symbols WHERE file = ? AND line <= ? ORDER BY line DESC LIMIT 1`, relRef, line).Scan(&fromPkg, &fromName); err != nil {
			if err != sql.ErrNoRows {
				return fmt.Errorf("graph: enclosing symbol of %s:%d: %w", relRef, line, err)
			}
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO edges (from_package, from_name, to_package, to_name, file, line) VALUES (?, ?, ?, ?, ?, ?)`,
			fromPkg, fromName, sym.Package, sym.Name, relRef, line); err != nil {
			return fmt.Errorf("graph: ref edge %s: %w", relRef, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO meta (key, value) VALUES (?, ?)`, key, live); err != nil {
		return fmt.Errorf("graph: refs marker %s: %w", sym.Name, err)
	}
	return tx.Commit()
}

type lspExtract struct {
	q    *Queue
	lang string
}

type lspSymbol struct {
	Name     string       `json:"name"`
	Kind     int          `json:"kind"`
	Range    *lspRange    `json:"range"`
	Location *lspLocWrap  `json:"location"`
	Children []*lspSymbol `json:"children"`
}

type lspRange struct {
	Start struct {
		Line      int `json:"line"`
		Character int `json:"character"`
	} `json:"start"`
}

type lspLocWrap struct {
	Range *lspRange `json:"range"`
}

func lspKind(k int) string {
	switch k {
	case 6, 9:
		return KindMethod
	case 12:
		return KindFunc
	case 5, 10, 11, 23:
		return KindType
	case 13:
		return KindVar
	case 14, 22:
		return KindConst
	}
	return ""
}

func (e *lspExtract) Extract(ctx context.Context, abs string) (Result, error) {
	if Skip(abs) {
		return Result{}, fmt.Errorf("graph: %s is skipped (vendor, testdata, or a dot or underscore path)", abs)
	}
	c, err := e.q.lspFor(ctx, e.lang)
	if err != nil {
		return Result{}, err
	}
	if c == nil {
		return Result{}, fmt.Errorf("graph: no language server is configured for %s", e.lang)
	}
	root, module, err := RootOf(filepath.Dir(abs))
	if err != nil {
		return Result{}, fmt.Errorf("graph: project of %s: %w", abs, err)
	}
	syms, err := c.documentSymbol(ctx, abs)
	if err != nil {
		return Result{}, err
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return Result{}, fmt.Errorf("graph: rel of %s: %w", abs, err)
	}
	fileRel := filepath.ToSlash(rel)
	pkg := PackagePath(root, module, filepath.Dir(abs))
	res := Result{}
	var walk func(list []*lspSymbol)
	walk = func(list []*lspSymbol) {
		for _, s := range list {
			if kind := lspKind(s.Kind); kind != "" {
				line := int64(1)
				if s.Range != nil {
					line = int64(s.Range.Start.Line + 1)
				} else if s.Location != nil && s.Location.Range != nil {
					line = int64(s.Location.Range.Start.Line + 1)
				}
				res.Symbols = append(res.Symbols, Symbol{
					Package: pkg,
					Name:    s.Name,
					Kind:    kind,
					File:    fileRel,
					Line:    line,
				})
			}
			walk(s.Children)
		}
	}
	walk(syms)
	return res, nil
}

func (c *lspClient) documentSymbol(ctx context.Context, abs string) ([]*lspSymbol, error) {
	raw, err := c.request(ctx, "textDocument/documentSymbol", map[string]any{
		"textDocument": map[string]any{"uri": lspURI(abs)},
	})
	if err != nil {
		return nil, err
	}
	var syms []*lspSymbol
	if err := json.Unmarshal(raw, &syms); err != nil {
		return nil, fmt.Errorf("graph: lsp %s documentSymbol: %w", c.lang, err)
	}
	return syms, nil
}

type lspLocation struct {
	Range struct {
		Start struct {
			Line      int `json:"line"`
			Character int `json:"character"`
		} `json:"start"`
	} `json:"range"`
	URI string `json:"uri"`
}

func (c *lspClient) references(ctx context.Context, abs string, line int64, name string) ([]lspLocation, error) {
	char := 0
	if text, err := os.ReadFile(abs); err == nil {
		lines := strings.Split(string(text), "\n")
		if int(line) >= 1 && int(line) <= len(lines) {
			if _, seg, ok := strings.Cut(name, "."); ok {
				name = seg
			}
			if i := strings.Index(lines[line-1], name); i >= 0 {
				char = i
			}
		}
	}
	raw, err := c.request(ctx, "textDocument/references", map[string]any{
		"textDocument": map[string]any{"uri": lspURI(abs)},
		"position":     map[string]any{"line": line - 1, "character": char},
		"context":      map[string]any{"includeDeclaration": false},
	})
	if err != nil {
		return nil, err
	}
	var locs []lspLocation
	if err := json.Unmarshal(raw, &locs); err != nil {
		return nil, fmt.Errorf("graph: lsp %s references: %w", c.lang, err)
	}
	return locs, nil
}
