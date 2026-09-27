package web

import (
	"database/sql"
	"os"
	"path/filepath"
	"sync"

	"github.com/mrsirg97-rgb/rig/store"
	sched "github.com/mrsirg97-rgb/rig/store/scheduler"
	"github.com/mrsirg97-rgb/rig/store/state"
	todostore "github.com/mrsirg97-rgb/rig/store/todo"
)

type storeCache struct {
	home      string
	crontab   sched.Crontab
	runnerCmd string
	mu        sync.Mutex
	dbs       map[string]store.DB
}

func newStoreCache(home string, ct sched.Crontab, runnerCmd string) *storeCache {
	return &storeCache{home: home, crontab: ct, runnerCmd: runnerCmd, dbs: map[string]store.DB{}}
}

func (c *storeCache) open(path string, statements []string, version int, migrate ...func(*sql.Tx, int, int) (string, error)) (store.DB, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if db, ok := c.dbs[path]; ok {
		return db, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return store.DB{}, err
	}
	db, _, _, err := store.Open(path, statements, version, migrate...)
	if err != nil {
		return store.DB{}, err
	}
	c.dbs[path] = db
	return db, nil
}

func (c *storeCache) state(cwd string) (store.DB, error) {
	return c.open(state.StorePath(c.home, cwd), state.Statements(), state.SchemaVersion, state.Migration())
}

func (c *storeCache) todo(cwd string) (store.DB, error) {
	path := todostore.FilePath(c.home)
	return c.open(path, todostore.Statements(), todostore.SchemaVersion, todostore.Migration(cwd, filepath.Dir(path)), todostore.ReviewMigration)
}

func (c *storeCache) scheduler() (store.DB, error) {
	shome := filepath.Join(c.home, "scheduler")
	return c.open(filepath.Join(shome, "global.sqlite"), sched.Statements(), sched.SchemaVersion, sched.Migration(shome, c.home, c.runnerCmd, c.crontab))
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
