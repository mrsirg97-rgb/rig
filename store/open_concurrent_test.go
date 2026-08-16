package store_test

// External test package: the seam's concurrency cases may reach the todo
// verbs; an internal one would cycle (store/todo already imports store).

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mrsirg97-rgb/looper/store"
	todostore "github.com/mrsirg97-rgb/looper/store/todo"
	tododdl "github.com/mrsirg97-rgb/looper/store/todo/ddl"
)

// Concurrent writers against one workspace file must serialize inside the
// lock window, not drop each other: an interactive session and a scheduler
// worker are two Opens of the same file.
func TestConcurrentCreatesSerialize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared.sqlite")
	var dbs []store.DB
	for i := 0; i < 2; i++ {
		db, _, err := store.Open(path, tododdl.Statements(), 1)
		if err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
		dbs = append(dbs, db)
	}
	const per = 20
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)
	for wi, db := range dbs {
		wg.Add(1)
		go func(wi int, db store.DB) {
			defer wg.Done()
			for i := 0; i < per; i++ {
				_, err := todostore.Create(context.Background(), db,
					[]todostore.CreateItem{{Text: fmt.Sprintf("w%d-%d", wi, i)}},
					fmt.Sprintf("s%d", wi))
				if err != nil {
					mu.Lock()
					errs = append(errs, err)
					mu.Unlock()
				}
			}
		}(wi, db)
	}
	wg.Wait()
	if len(errs) > 0 {
		t.Fatalf("%d of %d concurrent creates failed: %v", len(errs), per*len(dbs), errs[0])
	}
	for wi, db := range dbs {
		reply, err := todostore.Read(context.Background(), db, "")
		if err != nil {
			t.Fatalf("read %d: %v", wi, err)
		}
		head := strings.Split(reply, "\n")[0]
		if want := fmt.Sprintf("0/%d done", per*len(dbs)); !strings.Contains(head, want) {
			t.Fatalf("read %d: head %q, want %s", wi, head, want)
		}
	}
}

// The same posture over the mutate verbs: disjoint completes, two writers.
func TestConcurrentCompletesSerialize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared.sqlite")
	d1, _, err := store.Open(path, tododdl.Statements(), 1)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	for i := 1; i <= 40; i++ {
		if _, err := todostore.Create(context.Background(), d1, []todostore.CreateItem{{Text: fmt.Sprintf("task-%d", i)}}, "s0"); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}
	d2, _, err := store.Open(path, tododdl.Statements(), 1)
	if err != nil {
		t.Fatalf("open two: %v", err)
	}
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)
	for wi, db := range []store.DB{d1, d2} {
		wg.Add(1)
		go func(wi int, db store.DB) {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				id := fmt.Sprintf("t%d", wi*20+i+1)
				sess := fmt.Sprintf("s%d", wi)
				if _, err := todostore.Start(context.Background(), db, id, sess); err != nil {
					mu.Lock()
					errs = append(errs, err)
					mu.Unlock()
					continue
				}
				if _, err := todostore.Complete(context.Background(), db, id, sess); err != nil {
					mu.Lock()
					errs = append(errs, err)
					mu.Unlock()
				}
			}
		}(wi, db)
	}
	wg.Wait()
	if len(errs) > 0 {
		t.Fatalf("%d of 40 concurrent completes failed: %v", len(errs), errs[0])
	}
	reply, err := todostore.Read(context.Background(), d1, "")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if head := strings.Split(reply, "\n")[0]; !strings.Contains(head, "40/40 done") {
		t.Fatalf("head %q, want every task done", head)
	}
}
