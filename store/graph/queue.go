package graph

import (
	"bufio"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/mrsirg97-rgb/rig/v2/store"
	"github.com/mrsirg97-rgb/rig/v2/store/scope"
)

const QueueCap = 256

const ReadCap = 1 << 20

const (
	packRowCap = 200
)

type Queue struct {
	ch      chan string
	home    string
	loud    func(string)
	extract *GoExtract
	mu      sync.Mutex
	dbs     map[string]store.DB
	lspMu   sync.Mutex
	lsp     *lspClient
	lspLang string
}

func NewQueue(home string, loud func(string)) *Queue {
	return &Queue{
		ch:      make(chan string, QueueCap),
		home:    home,
		loud:    loud,
		extract: &GoExtract{},
		dbs:     map[string]store.DB{},
	}
}

func (q *Queue) Home() string { return q.home }

func (q *Queue) Touch(path string) {
	select {
	case q.ch <- path:
	default:
		q.say("graph: queue full, dropping %s", path)
	}
}

func (q *Queue) Run(ctx context.Context) {
	defer q.stopLSP()
	for {
		select {
		case <-ctx.Done():
			return
		case p := <-q.ch:
			q.process(ctx, p)
		}
	}
}

func (q *Queue) Close() {
	q.stopLSP()
}

func (q *Queue) Drain(ctx context.Context) {
	for {
		select {
		case p := <-q.ch:
			q.process(ctx, p)
		default:
			return
		}
	}
}

func (q *Queue) say(format string, args ...any) {
	if q.loud != nil {
		q.loud(fmt.Sprintf(format, args...))
	}
}

func (q *Queue) process(ctx context.Context, path string) {
	lang := LanguageOf(path)
	if lang == "" || Skip(path) {
		return
	}
	if ctx.Err() != nil {
		return
	}
	ext := q.extractor(lang)
	if ext == nil {
		return
	}
	root, _, err := RootOf(filepath.Dir(path))
	if err != nil {
		q.say("graph: project of %s: %v", path, err)
		return
	}
	db, err := q.open(root)
	if err != nil {
		q.say("graph: %v", err)
		return
	}
	if err := q.index(ctx, db, root, path); err != nil {
		q.say("graph: %v", err)
	}
}

func (q *Queue) extractor(lang string) Extractor {
	if lang == "go" {
		return q.extract
	}
	if ServerOf(lang) != nil {
		return &lspExtract{q: q, lang: lang}
	}
	return nil
}

func (q *Queue) open(root string) (store.DB, error) {
	path := FilePath(q.home, scope.Key(root), scope.Worktree(root))
	q.mu.Lock()
	defer q.mu.Unlock()
	if db, ok := q.dbs[path]; ok {
		return db, nil
	}
	db, err := Open(q.home, scope.Key(root), scope.Worktree(root))
	if err != nil {
		return store.DB{}, err
	}
	q.dbs[path] = db
	return db, nil
}

func (q *Queue) index(ctx context.Context, db store.DB, root, abs string) error {
	lang := LanguageOf(abs)
	if lang == "" || Skip(abs) {
		return nil
	}
	ext := q.extractor(lang)
	if ext == nil {
		return nil
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return fmt.Errorf("rel of %s: %w", abs, err)
	}
	rel = filepath.ToSlash(rel)
	if _, err := os.Stat(abs); err != nil {
		if os.IsNotExist(err) {
			return dropFile(ctx, db, rel)
		}
		return fmt.Errorf("stat %s: %w", abs, err)
	}
	res, err := ext.Extract(ctx, abs)
	if err != nil {
		return err
	}
	sum, err := sha256File(abs)
	if err != nil {
		return err
	}
	_, err = Apply(ctx, db, rel, sum, lang, res)
	return err
}

func dropFile(ctx context.Context, db store.DB, rel string) error {
	_, tx, err := db.Tx(ctx)
	if err != nil {
		return fmt.Errorf("tx for %s: %w", rel, err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, stmt := range []struct{ q string }{
		{`DELETE FROM edges WHERE file = ?`},
		{`DELETE FROM symbols WHERE file = ?`},
		{`DELETE FROM files WHERE path = ?`},
	} {
		if _, err := tx.ExecContext(ctx, stmt.q, rel); err != nil {
			return fmt.Errorf("drop %s: %w", rel, err)
		}
	}
	return tx.Commit()
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("hash %s: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (q *Queue) IndexProject(ctx context.Context, cwd string) (string, error) {
	root, _, err := RootOf(cwd)
	if err != nil {
		return "", fmt.Errorf("graph: project of %s: %w", cwd, err)
	}
	db, err := q.open(root)
	if err != nil {
		return "", err
	}
	var mapped int
	err = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != root && Skip(p) {
				return filepath.SkipDir
			}
			return nil
		}
		lang := LanguageOf(p)
		if lang == "" || q.extractor(lang) == nil {
			return nil
		}
		if err := q.index(ctx, db, root, p); err != nil {
			q.say("graph: %s: %v", p, err)
			return nil
		}
		mapped++
		if ctx.Err() != nil {
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("graph: walk %s: %w", root, err)
	}
	return fmt.Sprintf("mapped %d files", mapped), nil
}

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

func (q *Queue) packSymbol(ctx context.Context, db store.DB, root, module, target string) (string, error) {
	var rows []symRow
	if qual, name, ok := strings.Cut(target, "."); ok && qual != "" && name != "" {
		all, err := querySymbols(ctx, db, `SELECT package, name, kind, file, line, end_line FROM symbols WHERE name = ?`, name)
		if err != nil {
			return "", err
		}
		for _, s := range all {
			if qualMatches(s.Package, qual, module) {
				rows = append(rows, s)
			}
		}
		if len(rows) == 0 {
			rows, err = querySymbols(ctx, db, `SELECT package, name, kind, file, line, end_line FROM symbols WHERE name = ?`, target)
			if err != nil {
				return "", err
			}
		}
	} else {
		var err error
		rows, err = querySymbols(ctx, db, `SELECT package, name, kind, file, line, end_line FROM symbols WHERE name = ?`, target)
		if err != nil {
			return "", err
		}
	}
	if len(rows) == 0 {
		if qual, name, ok := strings.Cut(target, "."); ok && qual != "" && name != "" {
			elsewhere, err := querySymbols(ctx, db, `SELECT package, name, kind, file, line, end_line FROM symbols WHERE name = ? ORDER BY package`, name)
			if err != nil {
				return "", err
			}
			if len(elsewhere) > 0 {
				var names []string
				for _, s := range elsewhere {
					names = append(names, baseName(s.Package)+"."+s.Name)
				}
				return "", fmt.Errorf("graph: no %s in package %s; the map has %s", name, qual, strings.Join(names, ", "))
			}
		}
		return "", fmt.Errorf("graph: %s is not in the map; run index first", target)
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
		return "", fmt.Errorf("graph: %s is defined in %s and %s: name the package", target, names[0], strings.Join(names[1:], ", "))
	}
	sym := rows[0]

	if moved, err := q.refresh(ctx, db, root, sym.File); err != nil {
		return "", err
	} else if moved {
		sel, err := querySymbols(ctx, db, `SELECT package, name, kind, file, line, end_line FROM symbols WHERE package = ? AND name = ?`, sym.Package, sym.Name)
		if err != nil {
			return "", err
		}
		if len(sel) == 0 {
			return "", fmt.Errorf("graph: %s left the map at the re-extraction", target)
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
		return "", fmt.Errorf("graph: %s left the map at the re-extraction", target)
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
	coverage(ctx, db, root, &b)
	out := b.String()
	if len(out) > ReadCap {
		out = out[:ReadCap] + "\n[truncated at the read ceiling]"
	}
	return out, nil
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
