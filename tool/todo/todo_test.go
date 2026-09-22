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
	for _, action := range []string{"start", "complete", "fail", "retry", "note", "accept", "reject"} {
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

func TestNewVerbsRoundTrip(t *testing.T) {
	tool := todoapi.New(newDB(t))
	sess := core.NewSession()
	ctx := core.WithSession(context.Background(), sess)
	reply, err := exec(t, tool, ctx, map[string]any{"action": "create", "tasks": []any{
		map[string]any{"text": "swarm work"},
	}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := strings.Fields(strings.Split(reply, "\n")[2])[0]
	claimed, err := exec(t, tool, ctx, map[string]any{"action": "claim"})
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if !strings.Contains(claimed, "'"+id+"' claimed") {
		t.Fatalf("claim reply: %s", claimed)
	}
	noted, err := exec(t, tool, ctx, map[string]any{"action": "note", "id": id, "note": "on it"})
	if err != nil {
		t.Fatalf("note: %v", err)
	}
	if !strings.Contains(noted, "note added to '"+id+"'") || !strings.Contains(noted, "on it (by "+sess.ID+")") {
		t.Fatalf("note reply:\n%s", noted)
	}
	if _, err := exec(t, tool, ctx, map[string]any{"action": "complete", "id": id}); err != nil {
		t.Fatalf("complete: %v", err)
	}
	review, err := exec(t, tool, ctx, map[string]any{"action": "claim", "status": "review"})
	if err != nil {
		t.Fatalf("claim review: %v", err)
	}
	if !strings.Contains(review, "'"+id+"' claimed for review") {
		t.Fatalf("claim review reply: %s", review)
	}
	if _, err := exec(t, tool, ctx, map[string]any{"action": "accept", "id": id}); err != nil {
		t.Fatalf("accept: %v", err)
	}
	history, err := exec(t, tool, ctx, map[string]any{"action": "read", "all": true})
	if err != nil {
		t.Fatalf("read all: %v", err)
	}
	if !strings.Contains(history, "[x] swarm work") {
		t.Fatalf("the review flow must end done:\n%s", history)
	}
}

func TestRejectRequiresItsReasonThroughTheTool(t *testing.T) {
	tool := todoapi.New(newDB(t))
	sess := core.NewSession()
	ctx := core.WithSession(context.Background(), sess)
	if _, err := exec(t, tool, ctx, map[string]any{"action": "reject", "id": "t1"}); err == nil {
		t.Fatal("reject without a reason succeeded")
	} else if want := "action 'reject' requires a reason"; err.Error() != want {
		t.Errorf("voice:\n%q", err.Error())
	}
	if _, err := exec(t, tool, ctx, map[string]any{"action": "note", "id": "t1"}); err == nil {
		t.Fatal("note without text succeeded")
	} else if want := "action 'note' requires note text"; err.Error() != want {
		t.Errorf("voice:\n%q", err.Error())
	}
}

func TestClaimStatusOnlyKnowsReview(t *testing.T) {
	tool := todoapi.New(newDB(t))
	sess := core.NewSession()
	ctx := core.WithSession(context.Background(), sess)
	if _, err := exec(t, tool, ctx, map[string]any{"action": "claim", "status": "done"}); err == nil {
		t.Fatal("claim with an unknown status succeeded")
	} else if !strings.Contains(err.Error(), "unknown claim status") {
		t.Errorf("voice: %v", err)
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
	if _, err := exec(t, tool, ctx, map[string]any{"action": "claim", "status": "review"}); err != nil {
		t.Fatalf("claim review: %v", err)
	}
	if _, err := exec(t, tool, ctx, map[string]any{"action": "accept", "id": drop}); err != nil {
		t.Fatalf("accept: %v", err)
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

// A project named on a call is not a one-off: it binds the session, whose
// bare verbs then act there. The queue follows the work, not the
// directory the process happened to start in.
func TestProjectBindsTheSession(t *testing.T) {
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
	if !strings.Contains(reply, "bound to") {
		t.Fatalf("a named project must say it bound:\n%s", reply)
	}
	if !strings.Contains(reply, "[") || !strings.Contains(reply, "over there") {
		t.Fatalf("create reply lost the task:\n%s", reply)
	}
	read, err := exec(t, tool, ctx, map[string]any{"action": "read"})
	if err != nil {
		t.Fatalf("read after a bind: %v", err)
	}
	if !strings.Contains(read, "over there") {
		t.Fatalf("the bare verb must follow the binding:\n%s", read)
	}
	reported, err := exec(t, tool, ctx, map[string]any{"action": "bind"})
	if err != nil {
		t.Fatalf("bind with no project reports: %v", err)
	}
	if !strings.Contains(reported, "(bound)") {
		t.Fatalf("a bare bind must report the binding, got %q", reported)
	}
}

// A session launched outside any repo has no project to write into: the
// shared cwd bucket is a place, not a project, so a bare write refuses and
// says what to do, while a read stays available and labels itself.
func TestLaunchOutsideARepoRefusesWrites(t *testing.T) {
	db := newDB(t)
	tool := todoapi.New(db)
	t.Chdir(t.TempDir())
	ctx := core.WithSession(context.Background(), core.NewSession())
	if _, err := exec(t, tool, ctx, map[string]any{
		"action": "create", "tasks": []any{map[string]any{"text": "a chore"}},
	}); err == nil || !strings.Contains(err.Error(), "no project") {
		t.Fatalf("a bare write outside a repo must refuse naming the rule, got %v", err)
	}
	read, err := exec(t, tool, ctx, map[string]any{"action": "read"})
	if err != nil {
		t.Fatalf("a read must stay available: %v", err)
	}
	if !strings.Contains(read, "not a repo") {
		t.Fatalf("a non-repo queue must say so:\n%s", read)
	}
	if _, err := exec(t, tool, ctx, map[string]any{"action": "create", "tasks": []any{
		map[string]any{"text": "a chore"}}}); err == nil {
		t.Fatal("the refusal must hold for every write verb")
	}
	home := t.TempDir()
	if _, err := exec(t, tool, ctx, map[string]any{"action": "create", "project": home,
		"tasks": []any{map[string]any{"text": "a chore"}}}); err != nil {
		t.Fatalf("naming a project lets the write land: %v", err)
	}
	started, err := exec(t, tool, ctx, map[string]any{"action": "start", "id": "t1"})
	if err != nil {
		t.Fatalf("a bare verb after the bind: %v", err)
	}
	if !strings.Contains(started, "t1 [~]") {
		t.Fatalf("the bind must carry to the next verb:\n%s", started)
	}
}

// Every reply says which queue it speaks for: a shared bucket is the one
// place where two queues can look identical.
func TestEveryReplyNamesTheQueue(t *testing.T) {
	db := newDB(t)
	tool := todoapi.New(db)
	ctx := core.WithSession(context.Background(), core.NewSession())
	if _, err := exec(t, tool, ctx, map[string]any{
		"action": "create", "tasks": []any{map[string]any{"text": "name me"}}}); err != nil {
		t.Fatal(err)
	}
	for _, args := range []map[string]any{
		{"action": "read"},
		{"action": "start", "id": "t1"},
		{"action": "complete", "id": "t1"},
	} {
		out, err := exec(t, tool, ctx, args)
		if err != nil {
			t.Fatalf("%v: %v", args["action"], err)
		}
		if !strings.Contains(out, "[") || !strings.Contains(out, "] ") {
			t.Fatalf("the reply for %v must name the queue:\n%s", args["action"], out)
		}
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

// A read that names a project is a peek, not a move: looking at another
// repo's queue must not quietly relocate the session's own bare verbs.
func TestReadWithProjectIsAPeekNotAMove(t *testing.T) {
	db := newDB(t)
	tool := todoapi.New(db)
	here, there := t.TempDir(), t.TempDir()
	ctx := core.WithSession(context.Background(), core.NewSession())
	if _, err := exec(t, tool, ctx, map[string]any{
		"action": "create", "project": here,
		"tasks": []any{map[string]any{"text": "mine"}}}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if _, err := todostore.Create(context.Background(), db, todostore.ProjectOf(there),
		[]todostore.CreateItem{{Text: "theirs"}}, "seed"); err != nil {
		t.Fatalf("seed the other queue: %v", err)
	}
	peek, err := exec(t, tool, ctx, map[string]any{"action": "read", "project": there})
	if err != nil {
		t.Fatalf("peek: %v", err)
	}
	if !strings.Contains(peek, "theirs") {
		t.Fatalf("the peek must read the named queue:\n%s", peek)
	}
	if strings.Contains(peek, "bound to") {
		t.Fatalf("a peek must not announce a move it did not make:\n%s", peek)
	}
	reported, err := exec(t, tool, ctx, map[string]any{"action": "bind"})
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if !strings.Contains(reported, "queue: "+filepath.Base(here)) {
		t.Fatalf("a peek must not move the binding, got %q", reported)
	}
	if _, err := exec(t, tool, ctx, map[string]any{"action": "start", "id": "t1"}); err != nil {
		t.Fatalf("start in the bound queue: %v", err)
	}
	started, err := exec(t, tool, ctx, map[string]any{"action": "read"})
	if err != nil {
		t.Fatalf("read after the peek: %v", err)
	}
	if !strings.Contains(started, "mine") || strings.Contains(started, "theirs") {
		t.Fatalf("the bare verb must stay in the bound queue:\n%s", started)
	}
}

// A write that names a project and fails changes nothing, including the
// binding: the refusal already named the queue it tried.
func TestFailedWriteWithProjectLeavesTheBindingAlone(t *testing.T) {
	db := newDB(t)
	tool := todoapi.New(db)
	here, there := t.TempDir(), t.TempDir()
	ctx := core.WithSession(context.Background(), core.NewSession())
	if _, err := exec(t, tool, ctx, map[string]any{
		"action": "create", "project": here,
		"tasks": []any{map[string]any{"text": "mine"}}}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	_, err := exec(t, tool, ctx, map[string]any{
		"action": "complete", "id": "t99", "project": there,
	})
	if err == nil || !strings.Contains(err.Error(), "no task 't99'") {
		t.Fatalf("the failed write must name the queue it tried, got %v", err)
	}
	reported, err := exec(t, tool, ctx, map[string]any{"action": "bind"})
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if !strings.Contains(reported, "queue: "+filepath.Base(here)) {
		t.Fatalf("a failed call must not move the session, got %q", reported)
	}
	// And the same call, succeeding, does move it.
	if _, err := exec(t, tool, ctx, map[string]any{
		"action": "create", "id": "", "project": there,
		"tasks": []any{map[string]any{"text": "theirs"}}}); err != nil {
		t.Fatalf("the succeeding write: %v", err)
	}
	moved, err := exec(t, tool, ctx, map[string]any{"action": "bind"})
	if err != nil {
		t.Fatalf("report after the move: %v", err)
	}
	if !strings.Contains(moved, "queue: "+filepath.Base(there)+" (bound)") {
		t.Fatalf("a successful write must move the binding, got %q", moved)
	}
}
