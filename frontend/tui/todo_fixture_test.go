package tui_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/store"
	todostore "github.com/mrsirg97-rgb/rig/store/todo"
	tooltodo "github.com/mrsirg97-rgb/rig/tool/todo"
)

type todoFixture struct {
	t    *testing.T
	db   store.DB
	proj todostore.Project
}

func newTodoFixture(t *testing.T) *todoFixture {
	t.Helper()
	db, _, _, err := store.Open(filepath.Join(t.TempDir(), "todo.sqlite"), todostore.Statements(), todostore.SchemaVersion)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return &todoFixture{t: t, db: db, proj: todostore.Project{Key: "ws", Label: "rig"}}
}

func (f *todoFixture) create(session string, items ...todostore.CreateItem) string {
	f.t.Helper()
	reply, err := todostore.Create(context.Background(), f.db, f.proj, items, session)
	if err != nil {
		f.t.Fatalf("create: %v", err)
	}
	return reply
}

func (f *todoFixture) id(reply, text string) string {
	f.t.Helper()
	re := regexp.MustCompile(`\bt(\d+)\b \[[~x!r ]\] ` + regexp.QuoteMeta(text))
	if mm := re.FindStringSubmatch(reply); mm != nil {
		return "t" + mm[1]
	}
	f.t.Fatalf("no task %q in:\n%s", text, reply)
	return ""
}

func (f *todoFixture) exec(op func() (string, error)) {
	f.t.Helper()
	if _, err := op(); err != nil {
		f.t.Fatalf("todo: %v", err)
	}
}

func (f *todoFixture) queue() string {
	f.t.Helper()
	sv := "steer verb"
	reply := f.create("s1",
		todostore.CreateItem{Text: "wire the models table"},
		todostore.CreateItem{Text: "the switch seam"},
		todostore.CreateItem{Text: sv},
		todostore.CreateItem{Text: "policy test", Requires: &sv},
		todostore.CreateItem{Text: "rem check"},
	)
	f.exec(func() (string, error) {
		return todostore.Start(context.Background(), f.db, f.proj, f.id(reply, sv), "s1", false)
	})
	for _, text := range []string{"wire the models table", "the switch seam"} {
		f.exec(func() (string, error) {
			return todostore.Complete(context.Background(), f.db, f.proj, f.id(reply, text), "s1", false)
		})
	}
	policy := f.id(reply, "policy test")
	for _, note := range []string{"one", "two"} {
		f.exec(func() (string, error) {
			return todostore.Note(context.Background(), f.db, f.proj, policy, note, "s1")
		})
	}
	out, err := todostore.ReadAll(context.Background(), f.db, f.proj, "s1")
	if err != nil {
		f.t.Fatalf("read all: %v", err)
	}
	return out
}

func (f *todoFixture) claim() string {
	f.t.Helper()
	reply := f.create("s1",
		todostore.CreateItem{Text: "wire the models table"},
		todostore.CreateItem{Text: "the switch seam"},
		todostore.CreateItem{Text: "rem check"},
	)
	f.exec(func() (string, error) {
		return todostore.Complete(context.Background(), f.db, f.proj, f.id(reply, "wire the models table"), "s1", false)
	})
	f.exec(func() (string, error) {
		return todostore.Start(context.Background(), f.db, f.proj, f.id(reply, "the switch seam"), "01a011f6", false)
	})
	out, err := todostore.ReadAll(context.Background(), f.db, f.proj, "s1")
	if err != nil {
		f.t.Fatalf("read all: %v", err)
	}
	return out
}

func (f *todoFixture) review() string {
	f.t.Helper()
	reply := f.create("sessA", todostore.CreateItem{Text: "ready"})
	id := f.id(reply, "ready")
	f.exec(func() (string, error) {
		return todostore.Claim(context.Background(), f.db, f.proj, "sessA", "")
	})
	f.exec(func() (string, error) {
		return todostore.Complete(context.Background(), f.db, f.proj, id, "sessA", true)
	})
	f.exec(func() (string, error) {
		return todostore.Claim(context.Background(), f.db, f.proj, "sessB", "review")
	})
	out, err := todostore.Read(context.Background(), f.db, f.proj, "sessC")
	if err != nil {
		f.t.Fatalf("read: %v", err)
	}
	return out
}

func (f *todoFixture) stale() string {
	f.t.Helper()
	sv := "steer verb"
	reply := f.create("s1",
		todostore.CreateItem{Text: "wire the models table"},
		todostore.CreateItem{Text: "the switch seam"},
		todostore.CreateItem{Text: sv},
		todostore.CreateItem{Text: "policy test", Requires: &sv},
		todostore.CreateItem{Text: "rem check"},
	)
	f.exec(func() (string, error) {
		return todostore.Start(context.Background(), f.db, f.proj, f.id(reply, sv), "s1", false)
	})
	for _, text := range []string{"wire the models table", "the switch seam"} {
		f.exec(func() (string, error) {
			return todostore.Complete(context.Background(), f.db, f.proj, f.id(reply, text), "s1", false)
		})
	}
	policy := f.id(reply, "policy test")
	for _, note := range []string{"one", "two"} {
		f.exec(func() (string, error) {
			return todostore.Note(context.Background(), f.db, f.proj, policy, note, "s1")
		})
	}
	ctx := context.Background()
	for i := 0; i < 210; i++ {
		_, tx, err := f.db.Tx(ctx)
		if err != nil {
			f.t.Fatalf("tx: %v", err)
		}
		if _, err := tx.Exec("INSERT INTO events (ts, op, args, session, scope) VALUES (?, 'start', ?, NULL, 'ws')",
			time.Now().UTC().Format(time.RFC3339), `{"id":"t999"}`); err != nil {
			tx.Rollback()
			f.t.Fatalf("age: %v", err)
		}
		if err := tx.Commit(); err != nil {
			f.t.Fatalf("age commit: %v", err)
		}
	}
	out, err := todostore.Read(context.Background(), f.db, f.proj, "s1")
	if err != nil {
		f.t.Fatalf("read: %v", err)
	}
	return out
}

func (f *todoFixture) bareQueue() string {
	f.t.Helper()
	ctx := core.WithSession(context.Background(), &core.Session{ID: "sess-1"})
	if err := todostore.Bind(ctx, f.db, todostore.Binding{Session: "sess-1", Scope: "ws", Label: "rig"}); err != nil {
		f.t.Fatalf("bind: %v", err)
	}
	out, err := tooltodo.New(f.db, tooltodo.Interactive).Exec(ctx, json.RawMessage(`{"action":"bind"}`))
	if err != nil {
		f.t.Fatalf("report: %v", err)
	}
	return out
}
