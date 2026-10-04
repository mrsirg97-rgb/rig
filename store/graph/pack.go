package graph

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mrsirg97-rgb/rig/v2/store"
)

func qualMatches(pkg, qual, module string) bool {
	return pkg == qual || pkg == module+"/"+qual || strings.HasSuffix(pkg, "/"+qual)
}

func baseName(pkg string) string {
	if i := strings.LastIndex(pkg, "/"); i >= 0 {
		return pkg[i+1:]
	}
	return pkg
}

func (q *Queue) Pack(ctx context.Context, cwd, target string) (string, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "", fmt.Errorf("graph: pack needs a symbol or a file")
	}
	root, module, err := RootOf(cwd)
	if err != nil {
		return "", fmt.Errorf("graph: project of %s: %w", cwd, err)
	}
	db, err := q.open(root)
	if err != nil {
		return "", err
	}
	if strings.Contains(target, " ") && !isFile(filepath.Join(cwd, target)) && !isFile(target) {
		return q.packTask(ctx, db, root, target)
	}
	if strings.Contains(target, "/") || isFile(filepath.Join(cwd, target)) || isFile(target) {
		if looksQualified(target) && !isFile(filepath.Join(cwd, target)) && !isFile(target) {
			return "", fmt.Errorf("graph: %s reads as an import path; name the symbol by its package tail (%s), or a file path", target, tailOf(target))
		}
		return q.packFile(ctx, db, root, cwd, target)
	}
	return q.packSymbol(ctx, db, root, module, target)
}

func looksQualified(target string) bool {
	last := target[strings.LastIndex(target, "/")+1:]
	return strings.Contains(last, ".") && !strings.Contains(last, " ") && filepath.Ext(target) != "" && LanguageOf(target) == ""
}

func tailOf(target string) string {
	return target[strings.LastIndex(target, "/")+1:]
}

func isFile(p string) bool {
	if !filepath.IsAbs(p) {
		return false
	}
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

func (q *Queue) packFile(ctx context.Context, db store.DB, root, cwd, target string) (string, error) {
	abs := target
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(cwd, target)
	}
	if _, err := os.Stat(abs); err != nil {
		return "", fmt.Errorf("graph: %s: %w", target, err)
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("graph: %s is outside the project", target)
	}
	rel = filepath.ToSlash(rel)
	if _, err := q.refresh(ctx, db, root, rel); err != nil {
		return "", err
	}
	rows, err := querySymbols(ctx, db, `SELECT package, name, kind, file, line, end_line FROM symbols WHERE file = ? ORDER BY line`, rel)
	if err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "", fmt.Errorf("graph: %s holds no mapped symbols", rel)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s — %d symbols:\n", rel, len(rows))
	for _, s := range rows {
		fmt.Fprintf(&b, "  %s %s — %s:%d\n", s.Kind, s.Name, s.File, s.Line)
	}
	coverage(ctx, db, root, &b)
	return b.String(), nil
}

type symRow struct {
	Package string
	Name    string
	Kind    string
	File    string
	Line    int64
	EndLine int64
}

func querySymbols(ctx context.Context, db store.DB, q string, args ...any) ([]symRow, error) {
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("graph: symbols: %w", err)
	}
	defer rows.Close()
	var out []symRow
	for rows.Next() {
		var s symRow
		if err := rows.Scan(&s.Package, &s.Name, &s.Kind, &s.File, &s.Line, &s.EndLine); err != nil {
			return nil, fmt.Errorf("graph: symbols: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (q *Queue) lookupSymbol(ctx context.Context, db store.DB, module, target string) (symRow, error) {
	var rows []symRow
	if qual, name, ok := strings.Cut(target, "."); ok && qual != "" && name != "" {
		all, err := querySymbols(ctx, db, `SELECT package, name, kind, file, line, end_line FROM symbols WHERE name = ?`, name)
		if err != nil {
			return symRow{}, err
		}
		for _, s := range all {
			if qualMatches(s.Package, qual, module) {
				rows = append(rows, s)
			}
		}
		if len(rows) == 0 {
			rows, err = querySymbols(ctx, db, `SELECT package, name, kind, file, line, end_line FROM symbols WHERE name = ?`, target)
			if err != nil {
				return symRow{}, err
			}
		}
	} else {
		var err error
		rows, err = querySymbols(ctx, db, `SELECT package, name, kind, file, line, end_line FROM symbols WHERE name = ?`, target)
		if err != nil {
			return symRow{}, err
		}
	}
	if len(rows) == 0 {
		if qual, name, ok := strings.Cut(target, "."); ok && qual != "" && name != "" {
			elsewhere, err := querySymbols(ctx, db, `SELECT package, name, kind, file, line, end_line FROM symbols WHERE name = ? ORDER BY package`, name)
			if err != nil {
				return symRow{}, err
			}
			if len(elsewhere) > 0 {
				var names []string
				for _, s := range elsewhere {
					names = append(names, baseName(s.Package)+"."+s.Name)
				}
				return symRow{}, fmt.Errorf("graph: no %s in package %s; the map has %s", name, qual, strings.Join(names, ", "))
			}
		}
		return symRow{}, fmt.Errorf("graph: %s is not in the map; run index first", target)
	}
	pkgs := map[string]bool{}
	for _, s := range rows {
		pkgs[s.Package] = true
	}
	if len(pkgs) > 1 {
		var names []string
		for p := range pkgs {
			names = append(names, p)
		}
		sort.Strings(names)
		return symRow{}, fmt.Errorf("graph: %s is defined in %s and %s: name the package", target, names[0], strings.Join(names[1:], ", "))
	}
	return rows[0], nil
}

func (q *Queue) packSymbol(ctx context.Context, db store.DB, root, module, target string) (string, error) {
	sym, err := q.lookupSymbol(ctx, db, module, target)
	if err != nil {
		return "", err
	}
	block, err := q.packOne(ctx, db, root, sym)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString(block)
	coverage(ctx, db, root, &b)
	out := b.String()
	if len(out) > ReadCap {
		out = out[:ReadCap] + "\n[truncated at the read ceiling]"
	}
	return out, nil
}

func (q *Queue) packOne(ctx context.Context, db store.DB, root string, sym symRow) (string, error) {
	if moved, err := q.refresh(ctx, db, root, sym.File); err != nil {
		return "", err
	} else if moved {
		sel, err := querySymbols(ctx, db, `SELECT package, name, kind, file, line, end_line FROM symbols WHERE package = ? AND name = ?`, sym.Package, sym.Name)
		if err != nil {
			return "", err
		}
		if len(sel) == 0 {
			return "", fmt.Errorf("graph: %s left the map at the re-extraction", sym.Name)
		}
		sym = sel[0]
	}
	symLang := LanguageOf(filepath.Join(root, filepath.FromSlash(sym.File)))
	var client *lspClient
	if symLang != "go" {
		c, err := q.lspFor(ctx, symLang)
		if err != nil {
			return "", err
		}
		if c != nil {
			if err := q.resolveRefs(ctx, c, db, root, sym, false); err != nil {
				return "", err
			}
		}
		client = c
	}
	callers, err := queryEdges(ctx, db, `SELECT from_package, from_name, file, line FROM edges WHERE to_package = ? AND to_name = ? ORDER BY file, line`, sym.Package, sym.Name)
	if err != nil {
		return "", err
	}
	callees, err := queryEdges(ctx, db, `SELECT to_package, to_name, file, line FROM edges WHERE from_package = ? AND from_name = ? ORDER BY line`, sym.Package, sym.Name)
	if err != nil {
		return "", err
	}
	cited := map[string]bool{sym.File: true}
	for _, e := range callers {
		cited[e.File] = true
	}
	for _, e := range callees {
		cited[e.File] = true
	}
	movedAny := false
	for _, rel := range sortedKeys(cited) {
		moved, err := q.refresh(ctx, db, root, rel)
		if err != nil {
			return "", err
		}
		movedAny = movedAny || moved
	}
	sel, err := querySymbols(ctx, db, `SELECT package, name, kind, file, line, end_line FROM symbols WHERE package = ? AND name = ?`, sym.Package, sym.Name)
	if err != nil {
		return "", err
	}
	if len(sel) == 0 {
		return "", fmt.Errorf("graph: %s left the map at the re-extraction", sym.Name)
	}
	sym = sel[0]
	if movedAny && symLang != "go" && client != nil {
		if err := q.resolveRefs(ctx, client, db, root, sym, true); err != nil {
			return "", err
		}
	}
	callers, err = queryEdges(ctx, db, `SELECT from_package, from_name, file, line FROM edges WHERE to_package = ? AND to_name = ? ORDER BY file, line`, sym.Package, sym.Name)
	if err != nil {
		return "", err
	}
	callees, err = queryEdges(ctx, db, `SELECT to_package, to_name, file, line FROM edges WHERE from_package = ? AND from_name = ? ORDER BY line`, sym.Package, sym.Name)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s — %s — %s:%d\n", sym.Name, sym.Kind, sym.File, sym.Line)
	live, err := liveLines(root, sym.File, sym.Line, sym.EndLine)
	if err != nil {
		return "", err
	}
	for _, l := range live {
		fmt.Fprintf(&b, "  %d %s\n", l.n, l.text)
	}
	if len(callers) > 0 {
		fmt.Fprintf(&b, "callers (%d):\n", len(callers))
		for _, e := range callers {
			fmt.Fprintf(&b, "  %s.%s — %s:%d\n", baseName(e.Package), e.Name, e.File, e.Line)
		}
	}
	if len(callees) > 0 {
		fmt.Fprintf(&b, "callees (%d):\n", len(callees))
		for _, e := range callees {
			defs, err := querySymbols(ctx, db, `SELECT package, name, kind, file, line, end_line FROM symbols WHERE package = ? AND name = ?`, e.Package, e.Name)
			if err != nil {
				return "", err
			}
			if len(defs) == 0 {
				fmt.Fprintf(&b, "  %s.%s — not mapped\n", baseName(e.Package), e.Name)
				continue
			}
			d := defs[0]
			fmt.Fprintf(&b, "  %s.%s — %s — %s:%d\n", baseName(e.Package), e.Name, d.Kind, d.File, d.Line)
			sig, err := sigLines(root, d.File, d.Line, d.EndLine)
			if err != nil {
				return "", err
			}
			for _, l := range sig {
				fmt.Fprintf(&b, "    %d %s\n", l.n, l.text)
			}
		}
	}
	return b.String(), nil
}

type edgeRow struct {
	Package string
	Name    string
	File    string
	Line    int64
}

func queryEdges(ctx context.Context, db store.DB, q string, args ...any) ([]edgeRow, error) {
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("graph: edges: %w", err)
	}
	defer rows.Close()
	var out []edgeRow
	for rows.Next() {
		var e edgeRow
		if err := rows.Scan(&e.Package, &e.Name, &e.File, &e.Line); err != nil {
			return nil, fmt.Errorf("graph: edges: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func sortedKeys(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
