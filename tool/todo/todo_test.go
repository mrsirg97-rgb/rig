package todo_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/middleware/paths"
	"github.com/mrsirg97-rgb/rig/store"
	todostore "github.com/mrsirg97-rgb/rig/store/todo"
	todoapi "github.com/mrsirg97-rgb/rig/tool/todo"
)

func newDB(t *testing.T) store.DB {
	t.Helper()
	db, _, _, err := store.Open(filepath.Join(t.TempDir(), "todo.sqlite"), todostore.Statements(), todostore.SchemaVersion)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return db
}

func exec(t *testing.T, tool core.Tool, ctx context.Context, args map[string]any) (string, error) {
	t.Helper()
	payload, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	return tool.Exec(ctx, payload)
}

func rawEvents(t *testing.T, db store.DB) []string {
	t.Helper()
	_, tx, err := db.Tx(context.Background())
	if err != nil {
		t.Fatalf("tx: %v", err)
	}
	defer tx.Rollback()
	rows, err := tx.Query("SELECT session FROM events ORDER BY seq")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s sql.NullString
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s.String)
	}
	return out
}

func TestBareTodoIsLoudAtExecute(t *testing.T) {
	tool := todoapi.New(newDB(t))
	if _, err := exec(t, tool, context.Background(), map[string]any{}); err == nil {
		t.Fatal("bare execute succeeded")
	} else if !strings.Contains(err.Error(), "action required") {
		t.Errorf("bare voice: %v", err)
	}
}

func TestUnknownActionRefusesLoudly(t *testing.T) {
	tool := todoapi.New(newDB(t))
	if _, err := exec(t, tool, context.Background(), map[string]any{"action": "sideways"}); err == nil {
		t.Fatal("unknown action succeeded")
	} else if !strings.Contains(err.Error(), "unknown action") {
		t.Errorf("unknown-action voice: %v", err)
	}
}

func TestCreateMissingTasksFailsLoudly(t *testing.T) {
	tool := todoapi.New(newDB(t))
	if _, err := exec(t, tool, context.Background(), map[string]any{"action": "create"}); err == nil {
		t.Fatal("create without tasks succeeded")
	} else if want := "action 'create' requires tasks: array of {text}"; err.Error() != want {
		t.Errorf("voice:\n%q\nwant\n%q", err.Error(), want)
	}
}

func TestCreateMalformedTasksFailLoudly(t *testing.T) {
	tool := todoapi.New(newDB(t))
	for _, tasks := range []any{
		[]any{map[string]any{}},
		[]any{"a"},
		[]any{map[string]any{"text": "a"}, 1},
	} {
		if _, err := exec(t, tool, context.Background(), map[string]any{"action": "create", "tasks": tasks}); err == nil {
			t.Fatalf("malformed tasks %v succeeded", tasks)
		}
	}
}

func TestStateVerbsRefuseIdAbsenceLoudly(t *testing.T) {
	tool := todoapi.New(newDB(t))
	for _, action := range []string{"start", "complete", "fail", "retry"} {
		if _, err := exec(t, tool, context.Background(), map[string]any{"action": action}); err == nil {
			t.Fatalf("%s without id succeeded", action)
		} else if want := "action '" + action + "' requires id"; err.Error() != want {
			t.Errorf("%s voice:\n%q", action, err.Error())
		}
	}
}

func TestMoveRefusesIdOrPosAbsence(t *testing.T) {
	tool := todoapi.New(newDB(t))
	if _, err := exec(t, tool, context.Background(), map[string]any{"action": "move"}); err == nil {
		t.Fatal("move without id succeeded")
	} else if want := "action 'move' requires id"; err.Error() != want {
		t.Errorf("voice:\n%q", err.Error())
	}
	if _, err := exec(t, tool, context.Background(), map[string]any{"action": "move", "id": "t1"}); err == nil {
		t.Fatal("move without pos succeeded")
	} else if want := "action 'move' requires pos"; err.Error() != want {
		t.Errorf("voice:\n%q", err.Error())
	}
}

func TestExecThreadsTheSession(t *testing.T) {
	db := newDB(t)
	tool := todoapi.New(db)
	sess := core.NewSession()
	ctx := core.WithSession(context.Background(), sess)
	reply, err := exec(t, tool, ctx, map[string]any{"action": "create", "tasks": []any{map[string]any{"text": "attributed"}}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := strings.Fields(strings.Split(reply, "\n")[2])[0]
	if _, err := exec(t, tool, ctx, map[string]any{"action": "start", "id": id}); err != nil {
		t.Fatalf("start: %v", err)
	}
	sessions := rawEvents(t, db)
	if len(sessions) != 2 || sessions[0] != sess.ID || sessions[1] != sess.ID {
		t.Errorf("sessions = %v; want the threaded id", sessions)
	}
}

func TestAnonymousExecutivesRecordAnon(t *testing.T) {
	db := newDB(t)
	tool := todoapi.New(db)
	reply, err := exec(t, tool, context.Background(), map[string]any{"action": "create", "tasks": []any{map[string]any{"text": "anon work"}}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := strings.Fields(strings.Split(reply, "\n")[2])[0]
	if _, err := exec(t, tool, context.Background(), map[string]any{"action": "start", "id": id}); err != nil {
		t.Fatalf("start: %v", err)
	}
	sessions := rawEvents(t, db)
	if len(sessions) != 2 || sessions[0] != "anon" || sessions[1] != "anon" {
		t.Errorf("anonymous sessions = %v", sessions)
	}
}

func TestExecSurfacesTheReplies(t *testing.T) {
	tool := todoapi.New(newDB(t))
	sess := core.NewSession()
	ctx := core.WithSession(context.Background(), sess)
	reply, err := exec(t, tool, ctx, map[string]any{"action": "create", "tasks": []any{
		map[string]any{"text": "gate"},
		map[string]any{"text": "work", "dependsOn": "gate"},
	}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !strings.Contains(reply, "0/2 done") || !strings.Contains(reply, "next: ") {
		t.Errorf("counts/next missing:\n%s", reply)
	}
	if !strings.Contains(reply, "waits on") {
		t.Errorf("waits-on suffix missing:\n%s", reply)
	}
	read, err := exec(t, tool, core.WithSession(context.Background(), core.NewSession()), map[string]any{"action": "read"})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(read, "claimed by ") && !strings.Contains(read, "waits on") {
		t.Errorf("presence labels missing:\n%s", read)
	}
}

func TestExecRefusalsSurfaceAsVoices(t *testing.T) {
	tool := todoapi.New(newDB(t))
	sessA := core.NewSession()
	sessB := core.NewSession()
	ctxA := core.WithSession(context.Background(), sessA)
	reply, err := exec(t, tool, ctxA, map[string]any{"action": "create", "tasks": []any{map[string]any{"text": "owned"}}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := strings.Fields(strings.Split(reply, "\n")[2])[0]
	if _, err := exec(t, tool, ctxA, map[string]any{"action": "start", "id": id}); err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := exec(t, tool, core.WithSession(context.Background(), sessB), map[string]any{"action": "complete", "id": id}); err == nil {
		t.Fatal("foreign complete succeeded")
	} else if !strings.Contains(err.Error(), "claimed by "+sessA.ID) {
		t.Errorf("claim voice: %v", err)
	}
}

func TestReadAllTrueReturnsHistory(t *testing.T) {
	tool := todoapi.New(newDB(t))
	sess := core.NewSession()
	ctx := core.WithSession(context.Background(), sess)
	reply, err := exec(t, tool, ctx, map[string]any{"action": "create", "tasks": []any{
		map[string]any{"text": "keep"},
		map[string]any{"text": "drop"},
	}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	drop := strings.Fields(strings.Split(reply, "\n")[3])[0]
	if _, err := exec(t, tool, ctx, map[string]any{"action": "start", "id": drop}); err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := exec(t, tool, ctx, map[string]any{"action": "complete", "id": drop}); err != nil {
		t.Fatalf("complete: %v", err)
	}
	defaultRead, err := exec(t, tool, ctx, map[string]any{"action": "read"})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(defaultRead, "drop") {
		t.Errorf("default read leaked the done row:\n%s", defaultRead)
	}
	history, err := exec(t, tool, ctx, map[string]any{"action": "read", "all": true})
	if err != nil {
		t.Fatalf("read all: %v", err)
	}
	if !strings.Contains(history, "drop") {
		t.Errorf("all:true dropped the done row:\n%s", history)
	}
	if !strings.Contains(history, "[x] drop") {
		t.Errorf("all:true lost the done marker:\n%s", history)
	}
}

func TestProjectReadsAndWritesAnotherQueue(t *testing.T) {
	db := newDB(t)
	tool := todoapi.New(db)
	proj := t.TempDir()
	ctx := core.WithSession(context.Background(), core.NewSession())
	reply, err := exec(t, tool, ctx, map[string]any{
		"action": "create", "tasks": []any{map[string]any{"text": "over there"}}, "project": proj,
	})
	if err != nil {
		t.Fatalf("create in project: %v", err)
	}
	if !strings.Contains(reply, "over there") {
		t.Fatalf("create reply lost the task:\n%s", reply)
	}
	read, err := exec(t, tool, ctx, map[string]any{"action": "read", "project": proj})
	if err != nil {
		t.Fatalf("read project: %v", err)
	}
	if !strings.Contains(read, "over there") {
		t.Fatalf("a project queue must read from anywhere:\n%s", read)
	}
	def, err := exec(t, tool, ctx, map[string]any{"action": "read"})
	if err != nil {
		t.Fatalf("read default: %v", err)
	}
	if strings.Contains(def, "over there") {
		t.Fatalf("the default (cwd) queue must not see the project's:\n%s", def)
	}
}

func TestProjectExpandsTildeAtTheBoundary(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	proj := filepath.Join(home, "p")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	db := newDB(t)
	tool := todoapi.New(db)
	wrapped := paths.Middleware().Wrap(func(ctx context.Context, call core.ToolCall) (string, error) {
		return tool.Exec(ctx, call.Args)
	})
	ctx := core.WithSession(context.Background(), core.NewSession())
	args := map[string]any{"action": "create", "tasks": []any{map[string]any{"text": "tilde task"}}, "project": "~/p"}
	payload, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	reply, err := wrapped(ctx, core.ToolCall{ID: "c1", Name: "todo", Args: payload})
	if err != nil {
		t.Fatalf("create via ~: %v", err)
	}
	if !strings.Contains(reply, "tilde task") {
		t.Fatalf("create reply lost the task:\n%s", reply)
	}
	read, err := exec(t, tool, ctx, map[string]any{"action": "read", "project": proj})
	if err != nil {
		t.Fatalf("read the expanded project: %v", err)
	}
	if !strings.Contains(read, "tilde task") {
		t.Fatalf("~ must expand to the project at the boundary:\n%s", read)
	}
}
