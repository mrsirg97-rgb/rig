package graph

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/mrsirg97-rgb/rig/v2/broadcast"
	"github.com/mrsirg97-rgb/rig/v2/store"
	"github.com/mrsirg97-rgb/rig/v2/store/scope"
)

const QueueCap = 256

const ReadCap = 1 << 20

type Queue struct {
	ch      chan string
	home    string
	voice   broadcast.Member
	extract *GoExtract
	mu      sync.Mutex
	dbs     map[string]store.DB
	lspMu   sync.Mutex
	lsp     *lspClient
	lspLang string
	lspGone map[string]bool
	scorer  Scorer
	itemCap int
	loadCap int
}

type Option func(*Queue)

func WithPackCaps(items, load int) Option {
	if items <= 0 || load <= 0 {
		panic(fmt.Sprintf("graph: pack caps %d/%d: the ceiling needs a bound", items, load))
	}
	return func(q *Queue) { q.itemCap, q.loadCap = items, load }
}

func WithScorer(s Scorer) Option {
	return func(q *Queue) { q.scorer = s }
}

func NewQueue(home string, voice broadcast.Member, opts ...Option) *Queue {
	q := &Queue{
		ch:      make(chan string, QueueCap),
		home:    home,
		voice:   voice,
		extract: &GoExtract{},
		dbs:     map[string]store.DB{},
		lspGone: map[string]bool{},
		itemCap: ReadCap,
		loadCap: ReadCap,
	}
	for _, opt := range opts {
		opt(q)
	}
	return q
}

func (q *Queue) PackCaps() (item, load int) { return q.itemCap, q.loadCap }

func (q *Queue) Home() string { return q.home }

func (q *Queue) Touch(path string) {
	select {
	case q.ch <- path:
	default:
		q.say("queue full, dropping %s", path)
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
	if q.voice != nil {
		broadcast.Say(q.voice, "graph", strings.TrimPrefix(fmt.Sprintf(format, args...), "graph: "))
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
		q.say("project of %s: %v", path, err)
		return
	}
	db, err := q.open(root)
	if err != nil {
		q.say("%v", err)
		return
	}
	if err := q.index(ctx, db, root, path); err != nil {
		q.say("%v", err)
	}
}

func (q *Queue) extractor(lang string) Extractor {
	if lang == "go" {
		return q.extract
	}
	q.lspMu.Lock()
	gone := q.lspGone[lang]
	q.lspMu.Unlock()
	if ServerOf(lang) != nil && !gone {
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
	root, _, ok := ProjectRoot(cwd)
	if !ok {
		return "", fmt.Errorf("graph: %s is not a project (no go.mod above it and not a git worktree); index from inside one, or name it with project", cwd)
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
		if ctx.Err() != nil {
			return ctx.Err()
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
			if ctx.Err() != nil {
				return ctx.Err()
			}
			q.say("%s: %v", p, err)
			return nil
		}
		mapped++
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("graph: walk %s: %w (mapped %d files)", root, err, mapped)
	}
	return fmt.Sprintf("mapped %d files", mapped), nil
}
