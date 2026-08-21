package web

import (
	"os"
	"path/filepath"
	"sync"

	"github.com/mrsirg97-rgb/rig/store"
	sched "github.com/mrsirg97-rgb/rig/store/scheduler"
	"github.com/mrsirg97-rgb/rig/store/state"
	todostore "github.com/mrsirg97-rgb/rig/store/todo"
)

// storeCache opens the rig home's store files the way the root does
// (SPEC_SERVE 2) and caches them per file: the dashboard is a reader of
// the same SQLite files a live session is writing, never a second copy.
// The per-cwd path formulas are the stores' own (one source, shared with
// the root).
type storeCache struct {
	home string
	mu   sync.Mutex
	dbs  map[string]store.DB
}

func newStoreCache(home string) *storeCache {
	return &storeCache{home: home, dbs: map[string]store.DB{}}
}

func (c *storeCache) open(path string, statements []string, version int) (store.DB, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if db, ok := c.dbs[path]; ok {
		return db, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return store.DB{}, err
	}
	db, _, err := store.Open(path, statements, version)
	if err != nil {
		return store.DB{}, err
	}
	c.dbs[path] = db
	return db, nil
}

func (c *storeCache) state(cwd string) (store.DB, error) {
	return c.open(state.StorePath(c.home, cwd), state.Statements(), state.SchemaVersion)
}

func (c *storeCache) todo(cwd string) (store.DB, error) {
	return c.open(todostore.StorePath(c.home, cwd), todostore.Statements(), todostore.SchemaVersion)
}

func (c *storeCache) schedulerGlobal() (store.DB, error) {
	shome := filepath.Join(c.home, "scheduler")
	return c.open(sched.StorePathFor(shome, sched.JobKey{Scope: "global"}), sched.Statements(), sched.SchemaVersion)
}

func (c *storeCache) schedulerCwd(cwd string) (store.DB, error) {
	shome := filepath.Join(c.home, "scheduler")
	return c.open(sched.StorePathFor(shome, sched.JobKey{Scope: "cwd", Hash: sched.CwdHash(cwd)}), sched.Statements(), sched.SchemaVersion)
}

func (c *storeCache) closeAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for p, db := range c.dbs {
		if db.DB != nil {
			db.DB.Close()
		}
		delete(c.dbs, p)
	}
}
