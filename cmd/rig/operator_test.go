package main

import (
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/frontend/oneshot"
	"github.com/mrsirg97-rgb/rig/v2/store"
	sched "github.com/mrsirg97-rgb/rig/v2/store/scheduler"
	todostore "github.com/mrsirg97-rgb/rig/v2/store/todo"
	todoapi "github.com/mrsirg97-rgb/rig/v2/tool/todo"
)

func todoStoreFor(t *testing.T) store.DB {
	t.Helper()
	db, _, _, err := store.Open(filepath.Join(t.TempDir(), "todo.sqlite"), todostore.Statements(), todostore.SchemaVersion)
	if err != nil {
		t.Fatalf("todo store: %v", err)
	}
	t.Cleanup(func() { db.DB.Close() })
	return db
}

func rootWithTodo(t *testing.T) (*root, store.DB, string) {
	t.Helper()
	dir := t.TempDir()
	r := testRoot(&oneshot.OneShot{Prompt: "work", Out: io.Discard, Err: io.Discard})
	r.allow = []string{"todo", "bash", "scheduler"}
	tdb := todoStoreFor(t)
	r.tools["todo"] = todoapi.New(tdb, todoapi.Worker)
	return r, tdb, dir
}

func callOf(t *testing.T, name, action, scope string) core.ToolCall {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"action": action, "scope": scope})
	if err != nil {
		t.Fatal(err)
	}
	return core.ToolCall{ID: "c1", Name: name, Args: raw}
}

func doneTaskCount(t *testing.T, db store.DB, scope string) int {
	t.Helper()
	rows, err := db.DB.Query(`SELECT count(*) FROM tasks WHERE scope = ? AND status = 'done'`, scope)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("no count row")
	}
	var n int
	if err := rows.Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func seedDoneTask(t *testing.T, db store.DB, proj todostore.Project) {
	t.Helper()
	ctx := context.Background()
	if _, err := todostore.Create(ctx, db, proj, todostore.CreateItem{Text: "board entry"}, "sess-root"); err != nil {
		t.Fatalf("create: %v", err)
	}
	var id string
	if err := db.DB.QueryRow(`SELECT id FROM tasks WHERE scope = ?`, proj.Key).Scan(&id); err != nil {
		t.Fatalf("seed id: %v", err)
	}
	if _, err := todostore.Complete(ctx, db, proj, id, "sess-root", false); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if doneTaskCount(t, db, proj.Key) != 1 {
		t.Fatal("the seed task is not done")
	}
}

func liveSpec(t *testing.T, r *root, name string) core.Tool {
	t.Helper()
	for _, tool := range r.live.List() {
		if tool.Name() == name {
			return tool
		}
	}
	t.Fatalf("the live table holds no %s", name)
	return nil
}

func todoActionEnum(t *testing.T, r *root) []string {
	t.Helper()
	var schema struct {
		Properties struct {
			Action struct {
				Enum []string `json:"enum"`
			} `json:"action"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(liveSpec(t, r, "todo").Schema(), &schema); err != nil {
		t.Fatal(err)
	}
	return schema.Properties.Action.Enum
}

func TestADelegatedWorkerSMenuDropsTheOperatorVerbs(t *testing.T) {
	t.Setenv(sched.DelegateEnv, "1")
	r, _, _ := rootWithTodo(t)
	wire(r)
	enum := todoActionEnum(t, r)
	for _, verb := range []string{"prune", "accept", "reject", "move"} {
		for _, v := range enum {
			if v == verb {
				t.Fatalf("a delegated worker's menu offers %q: %v", verb, enum)
			}
		}
	}
	if strings.Contains(liveSpec(t, r, "todo").Description(), "prune") ||
		strings.Contains(liveSpec(t, r, "todo").Description(), "accept") ||
		strings.Contains(liveSpec(t, r, "todo").Description(), "reject") ||
		strings.Contains(liveSpec(t, r, "todo").Description(), "move") {
		t.Fatalf("a delegated worker's description says the operator's verbs: %s", liveSpec(t, r, "todo").Description())
	}
}

func TestADelegatedWorkerSPruneIsRefusedAndTheStoreIsUnchanged(t *testing.T) {
	t.Setenv(sched.DelegateEnv, "1")
	r, tdb, dir := rootWithTodo(t)
	wire(r)
	proj := todostore.ProjectOf(dir)
	seedDoneTask(t, tdb, proj)

	exec := chainFor(t, r, func(ctx context.Context, call core.ToolCall) (string, error) {
		return "ran", nil
	})
	content, err := exec(context.Background(), callOf(t, "todo", "prune", dir))
	if err != nil {
		t.Fatalf("the refusal is a teaching result, not a fault: %v", err)
	}
	want := "todo prune: the session's verb — a worker does not judge or delete the board; leave it for the session"
	if content != want {
		t.Fatalf("refusal = %q, want %q", content, want)
	}
	if doneTaskCount(t, tdb, proj.Key) != 1 {
		t.Fatal("a refused prune must leave the store unchanged")
	}

	raw, err := json.Marshal(map[string]any{"action": "create", "text": "the next step", "scope": dir})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := exec(context.Background(), core.ToolCall{ID: "c2", Name: "todo", Args: raw}); err != nil {
		t.Fatalf("a worker still creates and reads the board: %v", err)
	}
}

func TestADelegatedWorkerSRefusesTheOtherOperatorPairs(t *testing.T) {
	t.Setenv(sched.DelegateEnv, "1")
	r, _, dir := rootWithTodo(t)
	wire(r)
	exec := chainFor(t, r, func(ctx context.Context, call core.ToolCall) (string, error) {
		return "ran", nil
	})
	for _, pair := range [][2]string{
		{"todo", "accept"}, {"todo", "reject"}, {"todo", "move"},
		{"scheduler", "remove"}, {"plugin", "delete"},
	} {
		raw, err := json.Marshal(map[string]any{"action": pair[1], "scope": dir, "id": "t1"})
		if err != nil {
			t.Fatal(err)
		}
		content, err := exec(context.Background(), core.ToolCall{ID: "c1", Name: pair[0], Args: raw})
		if err != nil {
			t.Fatalf("%s %s: %v", pair[0], pair[1], err)
		}
		if !strings.HasPrefix(content, pair[0]+" "+pair[1]+": the session's verb") {
			t.Fatalf("%s %s: refusal = %q", pair[0], pair[1], content)
		}
	}
}

func TestAHeadlessRunWithoutTheMarkerKeepsTheOperatorVerbs(t *testing.T) {
	t.Setenv(sched.DelegateEnv, "")
	r, tdb, dir := rootWithTodo(t)
	wire(r)
	enum := todoActionEnum(t, r)
	for _, verb := range []string{"prune", "accept", "reject", "move"} {
		found := false
		for _, v := range enum {
			if v == verb {
				found = true
			}
		}
		if !found {
			t.Fatalf("a session of its own keeps %q on the menu: %v", verb, enum)
		}
	}
	proj := todostore.ProjectOf(dir)
	seedDoneTask(t, tdb, proj)

	exec := chainFor(t, r, func(ctx context.Context, call core.ToolCall) (string, error) {
		return "ran", nil
	})
	if _, err := exec(context.Background(), callOf(t, "todo", "prune", dir)); err != nil {
		t.Fatalf("a session of its own prunes its own board: %v", err)
	}
	if doneTaskCount(t, tdb, proj.Key) != 0 {
		t.Fatal("the prune must run: without the delegate marker the middleware is not wired")
	}
}
