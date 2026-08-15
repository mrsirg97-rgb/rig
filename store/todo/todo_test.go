package todo_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/looper/store"
	todostore "github.com/mrsirg97-rgb/looper/store/todo"
	tododdl "github.com/mrsirg97-rgb/looper/store/todo/ddl"
)

// Pane's named cases, ported by name (SPEC_STATE, testing): fold, create
// upsert, DAG planning, replay integrity. Refusals loud, in pane's
// teaching voice.

type item = todostore.CreateItem

func newDB(t *testing.T) store.DB {
	t.Helper()
	db, _, err := store.Open(filepath.Join(t.TempDir(), "todo.sqlite"), tododdl.Statements(), 1)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return db
}

func rawQuery(t *testing.T, db store.DB, q string, args ...any) *sql.Rows {
	t.Helper()
	ctx := context.Background()
	_, tx, err := db.Tx(ctx)
	if err != nil {
		t.Fatalf("tx: %v", err)
	}
	defer tx.Rollback()
	rows, err := tx.Query(q, args...)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	return rows
}

func rawExec(t *testing.T, db store.DB, q string, args ...any) {
	t.Helper()
	ctx := context.Background()
	_, tx, err := db.Tx(ctx)
	if err != nil {
		t.Fatalf("tx: %v", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(q, args...); err != nil {
		t.Fatalf("exec: %v", err)
	}
}

func eventCount(t *testing.T, db store.DB) int {
	t.Helper()
	bound, tx, err := db.Tx(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	_ = bound
	rows, err := tx.Query("SELECT count(*) FROM events")
	if err != nil {
		t.Fatal(err)
	}
	var n int64
	if !rows.Next() {
		rows.Close()
		t.Fatal("no row")
	}
	if err := rows.Scan(&n); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	rows.Close()
	return int(n)
}

func eventRows(t *testing.T, db store.DB) [][2]any {
	t.Helper()
	bound, tx, err := db.Tx(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	_ = bound
	rows, err := tx.Query("SELECT seq, op, args, session FROM events ORDER BY seq")
	if err != nil {
		t.Fatal(err)
	}
	var out [][2]any
	for rows.Next() {
		var seq int64
		var op, args string
		var session sql.NullString
		if err := rows.Scan(&seq, &op, &args, &session); err != nil {
			t.Fatal(err)
		}
		out = append(out, [2]any{[4]any{seq, op, args, session.String}, nil})
	}
	rows.Close()
	return out
}

// --- create: minting, order, idempotence ---

func TestCreateMintsIdsAndReadsBack(t *testing.T) {
	db := newDB(t)
	reply, err := todostore.Create(context.Background(), db, []item{{Text: "a"}, {Text: "b"}}, "s1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	for _, want := range []string{"t1", "t2", "a", "b"} {
		if !strings.Contains(reply, want) {
			t.Errorf("reply missing %q:\n%s", want, reply)
		}
	}
}

func TestReplayingCreateWithIdenticalTextsDoesNotDuplicateIds(t *testing.T) {
	db := newDB(t)
	if _, err := todostore.Create(context.Background(), db, []item{{Text: "a"}, {Text: "b"}}, "s1"); err != nil {
		t.Fatal(err)
	}
	reply, err := todostore.Create(context.Background(), db, []item{{Text: "a"}, {Text: "b"}}, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(reply, "[") != 2 {
		t.Errorf("expected two rows, got:\n%s", reply)
	}
}

func TestUpsertPreservesStatusAndPosition(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	if _, err := todostore.Create(ctx, db, []item{{Text: "a"}, {Text: "b"}}, "s1"); err != nil {
		t.Fatal(err)
	}
	if _, err := todostore.Start(ctx, db, "t1", "s1"); err != nil {
		t.Fatal(err)
	}
	reply, err := todostore.Create(ctx, db, []item{{Text: "a"}, {Text: "b"}}, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(reply, "t1 [~]") {
		t.Errorf("status preserved:\n%s", reply)
	}
}

func TestNewTextsMintAtNextPositions(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	if _, err := todostore.Create(ctx, db, []item{{Text: "a"}, {Text: "b"}}, "s1"); err != nil {
		t.Fatal(err)
	}
	reply, err := todostore.Create(ctx, db, []item{{Text: "a"}, {Text: "b"}, {Text: "c"}}, "s1")
	if err != nil {
		t.Fatal(err)
	}
	iA, iB, iC := strings.Index(reply, "t1"), strings.Index(reply, "t2"), strings.Index(reply, "t3")
	if !(iA >= 0 && iB > iA && iC > iB) {
		t.Errorf("order:\n%s", reply)
	}
}

func TestExplicitClearStillEmptiesTheQueue(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	if _, err := todostore.Create(ctx, db, []item{{Text: "a"}}, "s1"); err != nil {
		t.Fatal(err)
	}
	reply, err := todostore.Create(ctx, db, nil, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(reply, "t1") {
		t.Errorf("clear left rows:\n%s", reply)
	}
}

// --- DAG planning ---

func TestSameBatchChainCreatesATaskTree(t *testing.T) {
	db := newDB(t)
	d := func(s string) *string { return &s }
	_, err := todostore.Create(context.Background(), db, []item{
		{Text: "a"},
		{Text: "b", DependsOn: d("a")},
		{Text: "c", DependsOn: d("b")},
	}, "s1")
	if err != nil {
		t.Fatalf("chain: %v", err)
	}
}

func TestDiamondSeveralTasksMayDependOnOneTask(t *testing.T) {
	db := newDB(t)
	d := func(s string) *string { return &s }
	_, err := todostore.Create(context.Background(), db, []item{
		{Text: "root"},
		{Text: "l", DependsOn: d("root")},
		{Text: "r", DependsOn: d("root")},
	}, "s1")
	if err != nil {
		t.Fatalf("diamond: %v", err)
	}
}

func TestForwardReferencesWithinABatchResolve(t *testing.T) {
	db := newDB(t)
	d := func(s string) *string { return &s }
	_, err := todostore.Create(context.Background(), db, []item{
		{Text: "first", DependsOn: d("second")},
		{Text: "second"},
	}, "s1")
	if err != nil {
		t.Fatalf("forward: %v", err)
	}
}

func TestIdBasedReferenceToAnExistingTask(t *testing.T) {
	db := newDB(t)
	d := func(s string) *string { return &s }
	ctx := context.Background()
	if _, err := todostore.Create(ctx, db, []item{{Text: "a"}}, "s1"); err != nil {
		t.Fatal(err)
	}
	_, err := todostore.Create(ctx, db, []item{{Text: "b", DependsOn: d("t1")}}, "s1")
	if err != nil {
		t.Fatalf("id reference: %v", err)
	}
}

func TestUnknownDependencyTargetRefusesLoudly(t *testing.T) {
	db := newDB(t)
	d := func(s string) *string { return &s }
	ctx := context.Background()
	before := eventCount(t, db)
	_, err := todostore.Create(ctx, db, []item{{Text: "a", DependsOn: d("nope")}}, "s1")
	if err == nil {
		t.Fatal("expected refusal")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("voice: %v", err)
	}
	if got := eventCount(t, db); got != before {
		t.Errorf("batch rejected atomically: %d -> %d events", before, got)
	}
}

func TestSelfDependencyRefusesLoudly(t *testing.T) {
	db := newDB(t)
	d := func(s string) *string { return &s }
	_, err := todostore.Create(context.Background(), db, []item{{Text: "a", DependsOn: d("a")}}, "s1")
	if err == nil || !strings.Contains(err.Error(), "cannot depend on itself") {
		t.Fatalf("voice: %v", err)
	}
}

func TestCyclesRefuseWithTheCyclePath(t *testing.T) {
	db := newDB(t)
	d := func(s string) *string { return &s }
	ctx := context.Background()
	if _, err := todostore.Create(ctx, db, []item{
		{Text: "x"},
		{Text: "y", DependsOn: d("x")},
	}, "s1"); err != nil {
		t.Fatal(err)
	}
	_, err := todostore.Create(ctx, db, []item{{Text: "z", DependsOn: d("y")}, {Text: "x", DependsOn: d("z")}}, "s1")
	if err == nil {
		t.Fatal("expected refusal")
	}
	if !strings.Contains(err.Error(), "cycle") || !strings.Contains(err.Error(), "->") {
		t.Errorf("cycle path voice: %v", err)
	}
}

func TestACreateCannotPushAnAcyclicQueueIntoACycle(t *testing.T) {
	db := newDB(t)
	d := func(s string) *string { return &s }
	ctx := context.Background()
	if _, err := todostore.Create(ctx, db, []item{
		{Text: "a"},
		{Text: "b", DependsOn: d("a")},
		{Text: "c", DependsOn: d("b")},
	}, "s1"); err != nil {
		t.Fatal(err)
	}
	_, err := todostore.Create(ctx, db, []item{{Text: "a", DependsOn: d("c")}}, "s1")
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("voice: %v", err)
	}
}

// --- upsert link semantics ---

func TestRecreateOmittedKeepsTheLink(t *testing.T) {
	db := newDB(t)
	d := func(s string) *string { return &s }
	ctx := context.Background()
	if _, err := todostore.Create(ctx, db, []item{
		{Text: "a"},
		{Text: "b", DependsOn: d("a")},
	}, "s1"); err != nil {
		t.Fatal(err)
	}
	reply, err := todostore.Create(ctx, db, []item{{Text: "b"}}, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(reply, "waits on") {
		t.Errorf("link kept:\n%s", reply)
	}
}

func TestRecreateProvidedUpdatesTheLink(t *testing.T) {
	db := newDB(t)
	d := func(s string) *string { return &s }
	ctx := context.Background()
	if _, err := todostore.Create(ctx, db, []item{
		{Text: "a"},
		{Text: "b", DependsOn: d("a")},
		{Text: "c"},
	}, "s1"); err != nil {
		t.Fatal(err)
	}
	if _, err := todostore.Create(ctx, db, []item{{Text: "b", DependsOn: d("c")}}, "s1"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"t3", "t2"} {
		if _, err := todostore.Start(ctx, db, id, "s1"); err != nil {
			t.Fatalf("start %s: %v", id, err)
		}
		if _, err := todostore.Complete(ctx, db, id, "s1"); err != nil {
			t.Fatalf("done %s: %v", id, err)
		}
	}
}

func TestRecreateNullClearsTheLink(t *testing.T) {
	db := newDB(t)
	d := func(s string) *string { return &s }
	ctx := context.Background()
	if _, err := todostore.Create(ctx, db, []item{
		{Text: "a"},
		{Text: "b", DependsOn: d("a")},
	}, "s1"); err != nil {
		t.Fatal(err)
	}
	if _, err := todostore.Create(ctx, db, []item{{Text: "b", DepNull: true}}, "s1"); err != nil {
		t.Fatal(err)
	}
	if _, err := todostore.Start(ctx, db, "t2", "s1"); err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := todostore.Complete(ctx, db, "t2", "s1"); err != nil {
		t.Errorf("link cleared: %v", err)
	}
}

func TestFirstOccurrenceWinsWithinABatch(t *testing.T) {
	db := newDB(t)
	d := func(s string) *string { return &s }
	_, err := todostore.Create(context.Background(), db, []item{
		{Text: "a"},
		{Text: "x"},
		{Text: "b", DependsOn: d("a")},
		{Text: "b", DependsOn: d("x")}, // duplicate text: ignored entirely
	}, "s1")
	if err != nil {
		t.Fatalf("first-occurrence: %v", err)
	}
}

// --- replay integrity ---

func TestDanglingDependencyFromACorruptCreateEventDropsOnReplay(t *testing.T) {
	db := newDB(t)
	if _, err := todostore.Create(context.Background(), db, []item{{Text: "a"}, {Text: "b"}}, "s1"); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{
		"tasks": []any{
			map[string]any{"text": "c"},
			map[string]any{"text": "d", "dependsOn": "ghost"},
		},
	})
	rawExec(t, db, "INSERT INTO events (ts, op, args, session) VALUES (?, ?, ?, ?)",
		"2026-01-01T00:00:00Z", "create", string(args), nil)
	reply, err := todostore.Read(context.Background(), db)
	if err != nil {
		t.Fatalf("replay never throws: %v", err)
	}
	if strings.Contains(reply, "waits on") {
		t.Errorf("dangling dropped:\n%s", reply)
	}
}

func TestProjectionTamperingSelfHealsOnRead(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	if _, err := todostore.Create(ctx, db, []item{{Text: "a"}}, "s1"); err != nil {
		t.Fatal(err)
	}
	rawExec(t, db, "UPDATE tasks SET status='done' WHERE id='t1'")
	reply, err := todostore.Read(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(reply, "[ ]") {
		t.Errorf("rebuilt from events:\n%s", reply)
	}
}

func TestEveryMutationAppendsExactlyOneEvent(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	before := eventCount(t, db)
	if _, err := todostore.Create(ctx, db, []item{{Text: "a"}}, "s1"); err != nil {
		t.Fatal(err)
	}
	if got := eventCount(t, db); got != before+1 {
		t.Errorf("%d -> %d", before, got)
	}
	replyBefore := eventCount(t, db)
	if _, err := todostore.Read(ctx, db); err != nil {
		t.Fatal(err)
	}
	if got := eventCount(t, db); got != replyBefore {
		t.Errorf("read appends nothing: %d -> %d", replyBefore, got)
	}
}

func TestEventArgsMirrorTheCall(t *testing.T) {
	db := newDB(t)
	if _, err := todostore.Create(context.Background(), db, []item{{Text: "a"}, {Text: "b"}}, "s1"); err != nil {
		t.Fatal(err)
	}
	rows := eventRows(t, db)
	if len(rows) != 1 {
		t.Fatalf("one create event, got %d", len(rows))
	}
	inner := rows[0][0].([4]any)
	if inner[1] != "create" {
		t.Errorf("op: %v", inner)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(inner[2].(string)), &decoded); err != nil {
		t.Fatal(err)
	}
	if _, ok := decoded["tasks"]; !ok {
		t.Errorf("args carry the tasks as given: %v", decoded)
	}
}

var _ = fmt.Sprintf
