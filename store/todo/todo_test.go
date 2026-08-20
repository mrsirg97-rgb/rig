package todo_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/store"
	todostore "github.com/mrsirg97-rgb/rig/store/todo"
	tododdl "github.com/mrsirg97-rgb/rig/store/todo/ddl"
)

// The named cases (SPEC_STATE, testing): fold, create upsert, DAG
// planning, replay integrity. Refusals loud, in a teaching voice.

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
	// reads ride the pool: a rolled-back transaction invalidates its rows
	// before the caller can consume them, so no transaction here
	rows, err := db.Query(q, args...)
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
	if _, err := tx.Exec(q, args...); err != nil {
		tx.Rollback()
		t.Fatalf("exec: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
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
	reply, err := todostore.Read(context.Background(), db, "s1")
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
	reply, err := todostore.Read(ctx, db, "s1")
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
	if _, err := todostore.Read(ctx, db, "s1"); err != nil {
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

// --- move, claim, compaction, dependency gating and presence. Voices
// asserted verbatim.

const (
	sessA = "sess-a"
	sessB = "sess-b"
)

// projTextOrder reads the projection in queue order, as an operator would.
func projTextOrder(t *testing.T, db store.DB) []string {
	t.Helper()
	rows := rawQuery(t, db, "SELECT text FROM tasks ORDER BY pos, created_seq")
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		out = append(out, s)
	}
	rows.Close()
	return out
}

func projStatus(t *testing.T, db store.DB, text string) string {
	t.Helper()
	rows := rawQuery(t, db, "SELECT status FROM tasks WHERE text = ?", text)
	defer rows.Close()
	if !rows.Next() {
		t.Fatalf("no task %q", text)
	}
	var s string
	if err := rows.Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

// projDep resolves a task's dependency through task_deps (rig's
// substrate carries links there, not in tasks).
func projDep(t *testing.T, db store.DB, text string) string {
	t.Helper()
	rows := rawQuery(t, db, "SELECT depends_on FROM task_deps WHERE task_id = (SELECT id FROM tasks WHERE text = ?)", text)
	defer rows.Close()
	if !rows.Next() {
		return ""
	}
	var dep sql.NullString
	if err := rows.Scan(&dep); err != nil {
		t.Fatal(err)
	}
	return dep.String
}

// taskIDText resolves the minted id of the task line carrying exact text,
// by scanning a reply the way an operator would.
func taskIDText(t *testing.T, reply, text string) string {
	t.Helper()
	re := regexp.MustCompile(`\bt(\d+)\b \[[~x! ]\] ` + regexp.QuoteMeta(text))
	if mm := re.FindStringSubmatch(reply); mm != nil {
		return "t" + mm[1]
	}
	t.Fatalf("no task %q in:\n%s", text, reply)
	return ""
}

// age appends n ghost start events (replay no-ops) to push the seq forward:
// deterministic aging, synthetic events, not sleeps.
func age(t *testing.T, db store.DB, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		rawExec(t, db, "INSERT INTO events (ts, op, args, session) VALUES (?, 'start', ?, NULL)",
			time.Now().UTC().Format(time.RFC3339), `{"id":"t999"}`)
	}
}

func compactRow(t *testing.T, db store.DB) (args, session string) {
	t.Helper()
	rows := rawQuery(t, db, "SELECT args, session FROM events WHERE op='compact' ORDER BY seq DESC LIMIT 1")
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("no compact event")
	}
	var sess sql.NullString
	if err := rows.Scan(&args, &sess); err != nil {
		t.Fatal(err)
	}
	return args, sess.String
}

func compactTasks(t *testing.T, db store.DB) []map[string]any {
	t.Helper()
	args, _ := compactRow(t, db)
	var payload struct {
		Tasks []map[string]any `json:"tasks"`
	}
	if err := json.Unmarshal([]byte(args), &payload); err != nil {
		t.Fatalf("snapshot args: %v", err)
	}
	return payload.Tasks
}

// --- move: reordering ---

func TestMoveRenumbersDeterministically(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, []item{{Text: "a"}, {Text: "b"}, {Text: "c"}}, "s1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	c := taskIDText(t, reply, "c")
	if _, err := todostore.Move(ctx, db, c, 1, "s1"); err != nil {
		t.Fatalf("move: %v", err)
	}
	if got := projTextOrder(t, db); !eqStrings(got, []string{"c", "a", "b"}) {
		t.Fatalf("dense renumbering = %v", got)
	}
	reply, _ = todostore.Read(ctx, db, "s1")
	if !strings.Contains(reply, "next: "+c) {
		t.Errorf("next follows the moved order:\n%s", reply)
	}
}

func TestMoveToMiddleInsertsBeforeOccupant(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, []item{{Text: "a"}, {Text: "b"}, {Text: "c"}}, "s1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	a := taskIDText(t, reply, "a")
	if _, err := todostore.Move(ctx, db, a, 2, "s1"); err != nil {
		t.Fatalf("move: %v", err)
	}
	if got := projTextOrder(t, db); !eqStrings(got, []string{"b", "a", "c"}) {
		t.Fatalf("middle insert = %v", got)
	}
}

func TestMoveToLastAppendsAtBack(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, []item{{Text: "a"}, {Text: "b"}, {Text: "c"}}, "s1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	a := taskIDText(t, reply, "a")
	if _, err := needMove(t, ctx, db, a, 3); err != nil {
		t.Fatalf("move: %v", err)
	}
	if got := projTextOrder(t, db); !eqStrings(got, []string{"b", "c", "a"}) {
		t.Fatalf("back append = %v", got)
	}
}

func TestMoveToCurrentPositionIsANoOp(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, []item{{Text: "a"}, {Text: "b"}}, "s1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	b := taskIDText(t, reply, "b")
	if _, err := todostore.Move(ctx, db, b, 2, "s1"); err != nil {
		t.Fatalf("no-op move: %v", err)
	}
	if got := projTextOrder(t, db); !eqStrings(got, []string{"a", "b"}) {
		t.Fatalf("no-op changed the order: %v", got)
	}
}

func TestMoveWorksOnDoneAndFailedTasks(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, []item{{Text: "done one"}, {Text: "fail one"}, {Text: "keep"}}, "s1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	doneID, failID := taskIDText(t, reply, "done one"), taskIDText(t, reply, "fail one")
	for _, c := range []func() error{
		func() error { _, e := todostore.Start(ctx, db, doneID, "s1"); return e },
		func() error { _, e := todostore.Complete(ctx, db, doneID, "s1"); return e },
		func() error { _, e := todostore.Start(ctx, db, failID, "s1"); return e },
		func() error { _, e := todostore.Fail(ctx, db, failID, "s1"); return e },
	} {
		if e := c(); e != nil {
			t.Fatalf("setup: %v", e)
		}
	}
	if _, err := todostore.Move(ctx, db, doneID, 3, "s1"); err != nil {
		t.Fatalf("move of done task: %v", err)
	}
	if got := projTextOrder(t, db); !eqStrings(got, []string{"fail one", "keep", "done one"}) {
		t.Fatalf("done/failed move = %v", got)
	}
	if got := projStatus(t, db, "done one"); got != "done" {
		t.Errorf("status lost: %v", got)
	}
}

func TestMoveOutOfRangePositionRefusesLoudly(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, []item{{Text: "a"}, {Text: "b"}}, "s1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	a := taskIDText(t, reply, "a")
	for _, pos := range []int{0, 3} {
		if _, err := todostore.Move(ctx, db, a, pos, "s1"); err == nil {
			t.Fatalf("move pos=%d succeeded", pos)
		} else if !strings.Contains(err.Error(), "between 1 and 2") {
			t.Errorf("range voice (pos=%d): %v", pos, err)
		}
	}
	if got := projTextOrder(t, db); !eqStrings(got, []string{"a", "b"}) {
		t.Errorf("refused move landed: %v", got)
	}
}

func TestMoveUnknownIdRefusesLoudly(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	if _, err := todostore.Create(ctx, db, []item{{Text: "a"}}, "s1"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := todostore.Move(ctx, db, "nope", 1, "s1"); err == nil {
		t.Fatal("move of missing task succeeded")
	} else if !strings.Contains(err.Error(), "no task") {
		t.Errorf("missing-task voice: %v", err)
	}
}

func TestMoveAppendsOneMoveEventWithArgsAsGiven(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, []item{{Text: "a"}, {Text: "b"}}, "s1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	b := taskIDText(t, reply, "b")
	if _, err := todostore.Move(ctx, db, b, 1, "s1"); err != nil {
		t.Fatalf("move: %v", err)
	}
	if got := eventCount(t, db); got != 2 {
		t.Fatalf("events = %d; want create+move", got)
	}
	rows := rawQuery(t, db, "SELECT op, args FROM events WHERE seq = 2")
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("no move event")
	}
	var op, args string
	if err := rows.Scan(&op, &args); err != nil {
		t.Fatal(err)
	}
	if op != "move" {
		t.Fatalf("op = %q", op)
	}
	var given struct {
		ID  string `json:"id"`
		Pos int    `json:"pos"`
	}
	if err := json.Unmarshal([]byte(args), &given); err != nil {
		t.Fatalf("args: %v", err)
	}
	if given.ID != b || given.Pos != 1 {
		t.Errorf("args = %+v", given)
	}
}

func TestMovedOrderSurvivesProjectionTamper(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, []item{{Text: "a"}, {Text: "b"}, {Text: "c"}}, "s1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	c := taskIDText(t, reply, "c")
	if _, err := todostore.Move(ctx, db, c, 1, "s1"); err != nil {
		t.Fatalf("move: %v", err)
	}
	rawExec(t, db, "DELETE FROM tasks")
	if _, err := todostore.Read(ctx, db, "s1"); err != nil {
		t.Fatalf("read: %v", err)
	}
	if got := projTextOrder(t, db); !eqStrings(got, []string{"c", "a", "b"}) {
		t.Errorf("rebuilt order = %v", got)
	}
}

func TestSequentialMovesComposeDeterministically(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, []item{{Text: "a"}, {Text: "b"}, {Text: "c"}, {Text: "d"}}, "s1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	a, d := taskIDText(t, reply, "a"), taskIDText(t, reply, "d")
	if _, err := todostore.Move(ctx, db, d, 1, "s1"); err != nil {
		t.Fatalf("move d: %v", err)
	}
	if _, err := todostore.Move(ctx, db, a, 4, "s1"); err != nil {
		t.Fatalf("move a: %v", err)
	}
	if got := projTextOrder(t, db); !eqStrings(got, []string{"d", "b", "c", "a"}) {
		t.Fatalf("composed order = %v", got)
	}
	rawExec(t, db, "DELETE FROM tasks")
	if _, err := todostore.Read(ctx, db, "s1"); err != nil {
		t.Fatalf("read: %v", err)
	}
	if got := projTextOrder(t, db); !eqStrings(got, []string{"d", "b", "c", "a"}) {
		t.Errorf("replay order = %v", got)
	}
}

// --- claim semantics ---

func TestEveryMutationEventRecordsTheSession(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, []item{{Text: "a"}}, sessA)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	a := taskIDText(t, reply, "a")
	if _, err := todostore.Start(ctx, db, a, sessA); err != nil {
		t.Fatalf("start: %v", err)
	}
	rows := rawQuery(t, db, "SELECT session FROM events ORDER BY seq")
	defer rows.Close()
	var got []string
	for rows.Next() {
		var s sql.NullString
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		got = append(got, s.String)
	}
	if got[0] != sessA || got[1] != sessA {
		t.Errorf("sessions = %v", got)
	}
}

func TestAnonymousCallsRecordAnonAndNeverClaimLock(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, []item{{Text: "anon work"}}, "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := taskIDText(t, reply, "anon work")
	if _, err := todostore.Start(ctx, db, id, ""); err != nil {
		t.Fatalf("anon start: %v", err)
	}
	rows := rawQuery(t, db, "SELECT session FROM events WHERE seq = 2")
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("no start event")
	}
	var s sql.NullString
	if err := rows.Scan(&s); err != nil {
		t.Fatal(err)
	}
	if s.String != "anon" {
		t.Errorf("anon event session = %q", s.String)
	}
	if _, err := todostore.Complete(ctx, db, id, ""); err != nil {
		t.Errorf("anon completing anon-started work: %v", err)
	}
}

func TestCompleteByForeignSessionRefusesWithClaimer(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, []item{{Text: "owned"}}, sessA)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := taskIDText(t, reply, "owned")
	if _, err := todostore.Start(ctx, db, id, sessA); err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := todostore.Complete(ctx, db, id, sessB); err == nil {
		t.Fatal("foreign complete succeeded")
	} else if !strings.Contains(err.Error(), "is claimed by "+sessA+"; fail it first to take over") {
		t.Errorf("claim voice: %v", err)
	}
}

func TestStartByForeignSessionRefusesAndNamesClaimer(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, []item{{Text: "owned"}}, sessA)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := taskIDText(t, reply, "owned")
	if _, err := todostore.Start(ctx, db, id, sessA); err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := todostore.Start(ctx, db, id, sessB); err == nil {
		t.Fatal("foreign start succeeded")
	} else if !strings.Contains(err.Error(), "is already in progress") || !strings.Contains(err.Error(), "claimed by "+sessA) {
		t.Errorf("claim voice: %v", err)
	}
}

func TestFailIsTheTakeoverPath(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, []item{{Text: "bail"}}, sessA)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := taskIDText(t, reply, "bail")
	if _, err := todostore.Start(ctx, db, id, sessA); err != nil {
		t.Fatalf("start: %v", err)
	}
	reply, err = todostore.Fail(ctx, db, id, sessB)
	if err != nil {
		t.Fatalf("free fail: %v", err)
	}
	if !strings.Contains(reply, "failed (released from "+sessA+")") {
		t.Errorf("release voice:\n%s", reply)
	}
	if _, err := todostore.Retry(ctx, db, id, sessB); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if _, err := todostore.Start(ctx, db, id, sessB); err != nil {
		t.Fatalf("takeover start: %v", err)
	}
	if _, err := todostore.Complete(ctx, db, id, sessB); err != nil {
		t.Errorf("takeover complete: %v", err)
	}
}

func TestOwnerIsDerivedFromLogNotProjection(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, []item{{Text: "ownership"}}, sessA)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := taskIDText(t, reply, "ownership")
	if _, err := todostore.Start(ctx, db, id, sessA); err != nil {
		t.Fatalf("start: %v", err)
	}
	rawExec(t, db, "DELETE FROM tasks")
	if _, err := todostore.Read(ctx, db, sessB); err != nil {
		t.Fatalf("read rebuilds: %v", err)
	}
	if _, err := todostore.Complete(ctx, db, id, sessB); err == nil {
		t.Fatal("foreign complete succeeded")
	} else if !strings.Contains(err.Error(), "claimed by "+sessA) {
		t.Errorf("claim survived the rebuild: %v", err)
	}
}

func TestFailedTasksCarryNoOwnerAnySessionMayRetry(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, []item{{Text: "bail"}}, sessA)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := taskIDText(t, reply, "bail")
	if _, err := todostore.Start(ctx, db, id, sessA); err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := todostore.Fail(ctx, db, id, sessB); err != nil {
		t.Fatalf("fail: %v", err)
	}
	for _, c := range []func() error{
		func() error { _, e := todostore.Retry(ctx, db, id, sessB); return e },
		func() error { _, e := todostore.Start(ctx, db, id, sessB); return e },
		func() error { _, e := todostore.Complete(ctx, db, id, sessB); return e },
	} {
		if e := c(); e != nil {
			t.Fatalf("takeover path: %v", e)
		}
	}
}

func TestForeignClaimsShowInRenders(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, []item{{Text: "watched"}}, sessA)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := taskIDText(t, reply, "watched")
	if _, err := todostore.Start(ctx, db, id, sessA); err != nil {
		t.Fatalf("start: %v", err)
	}
	foreign, err := todostore.Read(ctx, db, sessB)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(foreign, "claimed by "+sessA) {
		t.Errorf("foreign claim not labeled:\n%s", foreign)
	}
	own, _ := todostore.Read(ctx, db, sessA)
	if strings.Contains(own, "claimed by") {
		t.Errorf("own claim labeled:\n%s", own)
	}
}

func TestStartReplyAlreadyCarriesTheClaim(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, []item{{Text: "instant"}}, sessA)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := taskIDText(t, reply, "instant")
	started, err := todostore.Start(ctx, db, id, sessA)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if strings.Contains(started, "claimed by") {
		t.Errorf("own claim labeled in start reply:\n%s", started)
	}
}

// --- compaction ---

func TestMutationPastThresholdCompacts(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, []item{{Text: "alpha"}, {Text: "beta"}}, "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	b := taskIDText(t, reply, "beta")
	age(t, db, 1010)
	if _, err := todostore.Start(ctx, db, b, ""); err != nil {
		t.Fatalf("start: %v", err)
	}
	if got := eventCount(t, db); got != 2 {
		t.Fatalf("events = %d; want bounded compact+mutation", got)
	}
	rows := rawQuery(t, db, "SELECT op, session FROM events ORDER BY seq")
	defer rows.Close()
	var ops, sess []string
	for rows.Next() {
		var op, s sql.NullString
		if err := rows.Scan(&op, &s); err != nil {
			t.Fatal(err)
		}
		ops = append(ops, op.String)
		sess = append(sess, s.String)
	}
	if ops[0] != "compact" || ops[1] != "start" {
		t.Fatalf("ops = %v", ops)
	}
	if sess[0] != "anon" {
		t.Errorf("compact attributed to %q", sess[0])
	}
	if got := projStatus(t, db, "beta"); got != "in_progress" {
		t.Errorf("beta = %v", got)
	}
}

func TestCompactSnapshotIsFullPreMutationCapture(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, []item{{Text: "alpha"}, {Text: "beta"}}, "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	b := taskIDText(t, reply, "beta")
	age(t, db, 1010)
	if _, err := todostore.Start(ctx, db, b, ""); err != nil {
		t.Fatalf("start: %v", err)
	}
	tasks := compactTasks(t, db)
	if len(tasks) != 2 {
		t.Fatalf("snapshot = %d tasks", len(tasks))
	}
	byText := func(s string) map[string]any {
		for _, tk := range tasks {
			if tk["text"] == s {
				return tk
			}
		}
		t.Fatalf("snapshot missing %q", s)
		return nil
	}
	if byText("alpha")["status"] != "pending" || byText("beta")["status"] != "pending" {
		t.Errorf("snapshot carries post-mutation state: %v", tasks)
	}
	if byText("alpha")["pos"] != float64(0) || byText("beta")["pos"] != float64(1) {
		t.Errorf("snapshot positions = %v", tasks)
	}
	for _, tk := range tasks {
		if tk["owner"] != nil {
			t.Errorf("snapshot owner = %v", tk)
		}
	}
}

func TestReplayReproducesQueueAfterCompaction(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, []item{{Text: "alpha"}, {Text: "beta"}}, "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	b := taskIDText(t, reply, "beta")
	age(t, db, 1010)
	if _, err := todostore.Start(ctx, db, b, ""); err != nil {
		t.Fatalf("start: %v", err)
	}
	rawExec(t, db, "UPDATE tasks SET pos = 99 - pos")
	rawExec(t, db, "DELETE FROM tasks WHERE text = 'beta'")
	if _, err := todostore.Read(ctx, db, ""); err != nil {
		t.Fatalf("read: %v", err)
	}
	if got := projTextOrder(t, db); !eqStrings(got, []string{"alpha", "beta"}) {
		t.Errorf("tamper self-heal order = %v", got)
	}
	if got := projStatus(t, db, "beta"); got != "in_progress" {
		t.Errorf("tamper self-heal status = %v", got)
	}
}

func TestClaimsSurviveCompaction(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, []item{{Text: "claimed"}}, sessA)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := taskIDText(t, reply, "claimed")
	if _, err := todostore.Start(ctx, db, id, sessA); err != nil {
		t.Fatalf("start: %v", err)
	}
	age(t, db, 1010) // the start event will be compacted away
	if _, err := todostore.Complete(ctx, db, id, sessB); err == nil {
		t.Fatal("foreign complete succeeded")
	} else if !strings.Contains(err.Error(), "claimed by "+sessA) {
		t.Errorf("claim survived the snapshot: %v", err)
	}
}

func TestMovesAndDependenciesSurviveCompaction(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, []item{
		{Text: "root"},
		{Text: "leaf", DependsOn: ptrTo("root")},
	}, "s1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	leaf := taskIDText(t, reply, "leaf")
	if _, err := todostore.Move(ctx, db, leaf, 1, "s1"); err != nil {
		t.Fatalf("move: %v", err)
	}
	age(t, db, 1010)
	if _, err := todostore.Read(ctx, db, "s1"); err != nil {
		t.Fatalf("read: %v", err)
	}
	if got := projTextOrder(t, db); !eqStrings(got, []string{"leaf", "root"}) {
		t.Errorf("order after age = %v", got)
	}
	rootID := taskIDText(t, reply, "root")
	if got := projDep(t, db, "leaf"); got != rootID {
		t.Errorf("dependency = %q", got)
	}
}

func TestStalenessEpochResetsAfterCompaction(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, []item{{Text: "ancient"}}, "s1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := taskIDText(t, reply, "ancient")
	age(t, db, 1010)
	if _, err := todostore.Start(ctx, db, id, "s1"); err != nil { // triggers the compaction
		t.Fatalf("start: %v", err)
	}
	age(t, db, 210)
	stale, _ := todostore.Read(ctx, db, "s1")
	if !strings.Contains(stale, "1 unresolved since") {
		t.Errorf("stale footer after epoch reset:\n%s", stale)
	}
}

func TestReadNeverCompacts(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	if _, err := todostore.Create(ctx, db, []item{{Text: "quiet"}}, "s1"); err != nil {
		t.Fatalf("create: %v", err)
	}
	age(t, db, 1010)
	if _, err := todostore.Read(ctx, db, "s1"); err != nil {
		t.Fatalf("read: %v", err)
	}
	if got := eventCount(t, db); got != 1011 {
		t.Errorf("read touched the log: %d events", got)
	}
}

// --- stale footer ---

func TestStaleTasksAppendFooterFreshOmit(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	if _, err := todostore.Create(ctx, db, []item{{Text: "ancient"}}, "s1"); err != nil {
		t.Fatalf("create: %v", err)
	}
	fresh, _ := todostore.Read(ctx, db, "s1")
	if strings.Contains(fresh, "unresolved since") {
		t.Errorf("fresh queue footered:\n%s", fresh)
	}
	age(t, db, 210)
	stale, _ := todostore.Read(ctx, db, "s1")
	if !strings.Contains(stale, "1 unresolved since") {
		t.Errorf("stale footer missing:\n%s", stale)
	}
}

// --- completion gating ---

func TestCompleteOnBlockedTaskRefusesWithBlockerStatus(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, []item{
		{Text: "gate"},
		{Text: "work", DependsOn: ptrTo("gate")},
	}, "s1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	gate, work := taskIDText(t, reply, "gate"), taskIDText(t, reply, "work")
	if _, err := todostore.Start(ctx, db, work, "s1"); err != nil {
		t.Fatalf("start work: %v", err)
	}
	want := func(hint string) string {
		return "'" + work + "' is blocked by '" + gate + "' (" + hint + ")"
	}
	if _, err := todostore.Complete(ctx, db, work, "s1"); err == nil {
		t.Fatal("blocked complete (pending) succeeded")
	} else if got := err.Error(); got != want("pending; start it first") {
		t.Fatalf("blocked voice (pending):\n%q\nwant\n%q", got, want("pending; start it first"))
	}
	if _, err := todostore.Start(ctx, db, gate, "s1"); err != nil {
		t.Fatalf("start gate: %v", err)
	}
	if _, err := todostore.Complete(ctx, db, work, "s1"); err == nil {
		t.Fatal("blocked complete (in_progress) succeeded")
	} else if got := err.Error(); got != want("in_progress") {
		t.Fatalf("blocked voice (in_progress):\n%q", got)
	}
	if _, err := todostore.Fail(ctx, db, gate, "s1"); err != nil {
		t.Fatalf("fail gate: %v", err)
	}
	if _, err := todostore.Complete(ctx, db, work, "s1"); err == nil {
		t.Fatal("blocked complete (failed) succeeded")
	} else if got := err.Error(); got != want("failed; retry it first") {
		t.Fatalf("blocked voice (failed):\n%q", got)
	}
	// retry -> start -> complete unblocks the dependent
	if _, err := todostore.Retry(ctx, db, gate, "s1"); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if _, err := todostore.Start(ctx, db, gate, "s1"); err != nil {
		t.Fatalf("start gate: %v", err)
	}
	if _, err := todostore.Complete(ctx, db, gate, "s1"); err != nil {
		t.Fatalf("complete gate: %v", err)
	}
	done, err := todostore.Complete(ctx, db, work, "s1")
	if err != nil {
		t.Fatalf("unblocked complete: %v", err)
	}
	if !strings.Contains(done, "[x] work") {
		t.Errorf("done marker:\n%s", done)
	}
}

func TestStartOnBlockedTaskIsLegal(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, []item{
		{Text: "prereq"},
		{Text: "later", DependsOn: ptrTo("prereq")},
	}, "s1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	later := taskIDText(t, reply, "later")
	started, err := todostore.Start(ctx, db, later, "s1")
	if err != nil {
		t.Fatalf("start on blocked task: %v", err)
	}
	if !strings.Contains(started, "[~] later") {
		t.Errorf("started marker:\n%s", started)
	}
}

// --- next: presence ---

func TestNextSkipsBlockedTasks(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, []item{
		{Text: "dep-a"},
		{Text: "dep-b"},
		{Text: "leaf", DependsOn: ptrTo("dep-b")},
		{Text: "free"},
	}, "s1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	depA, depB := taskIDText(t, reply, "dep-a"), taskIDText(t, reply, "dep-b")
	if !strings.Contains(reply, "waits on "+depB) {
		t.Errorf("waits-on suffix:\n%s", reply)
	}
	if !strings.Contains(reply, "next: "+depA) {
		t.Errorf("next skips the blocked leaf:\n%s", reply)
	}
}

func TestAllBlockedQueueShowsNoNext(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, []item{
		{Text: "prereq"},
		{Text: "dependent", DependsOn: ptrTo("prereq")},
	}, "s1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	prereq := taskIDText(t, reply, "prereq")
	if _, err := todostore.Start(ctx, db, prereq, "s1"); err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := todostore.Fail(ctx, db, prereq, "s1"); err != nil {
		t.Fatalf("fail: %v", err)
	}
	read, _ := todostore.Read(ctx, db, "s1")
	if strings.Contains(read, "next: ") {
		t.Errorf("blocked-only queue shows a next:\n%s", read)
	}
	if !strings.Contains(read, "waits on ") {
		t.Errorf("waits-on suffix:\n%s", read)
	}
}

func eqStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func ptrTo(s string) *string {
	q := s
	return &q
}

// needMove is a thin indirection so move-order assertions share one choke point.
func needMove(t *testing.T, ctx context.Context, db store.DB, id string, pos int) (string, error) {
	t.Helper()
	return todostore.Move(ctx, db, id, pos, "s1")
}

// --- dependency resolution ---

// A minted id must not shadow a matching text: existing ids resolve
// id-first, batch-internal references resolve by text only.
func TestMintedIdDoesNotShadowMatchingText(t *testing.T) {
	db := newDB(t)
	d := func(s string) *string { return &s }
	ctx := context.Background()
	if _, err := todostore.Create(ctx, db, []item{{Text: "x"}}, "s1"); err != nil {
		t.Fatal(err)
	}
	if _, err := todostore.Create(ctx, db, []item{{Text: "t3"}}, "s1"); err != nil {
		t.Fatal(err)
	}
	// beta mints t3; the dep must land on the existing task by text, not on
	// beta's own fresh id (a self-cycle).
	if _, err := todostore.Create(ctx, db, []item{{Text: "beta", DependsOn: d("t3")}}, "s1"); err != nil {
		t.Fatalf("text match shadowed by the minted id: %v", err)
	}
	if dep := projDep(t, db, "beta"); dep != "t2" {
		t.Fatalf("beta's dep = %q, want t2 (the text match)", dep)
	}
}

// Three-node cycles within one batch refuse with the path, atomically.
func TestThreeNodeCyclesRefuseWithPath(t *testing.T) {
	db := newDB(t)
	d := func(s string) *string { return &s }
	before := eventCount(t, db)
	_, err := todostore.Create(context.Background(), db, []item{
		{Text: "a", DependsOn: d("c")},
		{Text: "b", DependsOn: d("a")},
		{Text: "c", DependsOn: d("b")},
	}, "s1")
	if err == nil {
		t.Fatal("expected refusal")
	}
	if !strings.Contains(err.Error(), "cycle") || !strings.Contains(err.Error(), "->") {
		t.Errorf("cycle path voice: %v", err)
	}
	if got := eventCount(t, db); got != before {
		t.Errorf("batch rejected atomically: %d -> %d events", before, got)
	}
}

// --- lifecycle voices and workspace isolation ---

// Done tasks never report a blocker: the status gate lands first.
func TestDoneTasksNeverReportABlocker(t *testing.T) {
	db := newDB(t)
	d := func(s string) *string { return &s }
	ctx := context.Background()
	if _, err := todostore.Create(ctx, db, []item{{Text: "dep"}, {Text: "outer", DependsOn: d("dep")}}, "s1"); err != nil {
		t.Fatal(err)
	}
	// both done through the claim path
	if _, err := todostore.Start(ctx, db, "t1", "s1"); err != nil {
		t.Fatal(err)
	}
	if _, err := todostore.Complete(ctx, db, "t1", "s1"); err != nil {
		t.Fatal(err)
	}
	if _, err := todostore.Start(ctx, db, "t2", "s1"); err != nil {
		t.Fatal(err)
	}
	if _, err := todostore.Complete(ctx, db, "t2", "s1"); err != nil {
		t.Fatal(err)
	}
	// now dep points at a pending task, via the recreate's link update
	if _, err := todostore.Create(ctx, db, []item{{Text: "c"}}, "s1"); err != nil {
		t.Fatal(err)
	}
	if _, err := todostore.Create(ctx, db, []item{{Text: "dep", DependsOn: d("c")}}, "s1"); err != nil {
		t.Fatal(err)
	}
	_, err := todostore.Complete(ctx, db, "t1", "s1")
	if err == nil {
		t.Fatal("expected refusal")
	}
	voice := err.Error()
	if !strings.Contains(voice, "done; read-only") {
		t.Fatalf("voice: %v", err)
	}
	if strings.Contains(voice, "blocked by") {
		t.Fatalf("done task reported a blocker: %v", err)
	}
}

// pending -> in_progress -> done; done is read-only for complete and start.
func TestLifecycleDoneIsReadOnly(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	if _, err := todostore.Create(ctx, db, []item{{Text: "lc"}}, "s1"); err != nil {
		t.Fatal(err)
	}
	if _, err := todostore.Start(ctx, db, "t1", "s1"); err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := todostore.Complete(ctx, db, "t1", "s1"); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if got := projStatus(t, db, "lc"); got != "done" {
		t.Fatalf("status = %q, want done", got)
	}
	for _, verb := range []func() error{
		func() error { _, err := todostore.Complete(ctx, db, "t1", "s1"); return err },
		func() error { _, err := todostore.Start(ctx, db, "t1", "s1"); return err },
	} {
		err := verb()
		if err == nil || !strings.Contains(err.Error(), "done; read-only") {
			t.Fatalf("read-only voice: %v", err)
		}
	}
}

// failed -> retry -> started again: the chain walks back and completes.
func TestFailedToRetryToStartedAgain(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	if _, err := todostore.Create(ctx, db, []item{{Text: "fc"}}, "s1"); err != nil {
		t.Fatal(err)
	}
	if _, err := todostore.Start(ctx, db, "t1", "s1"); err != nil {
		t.Fatal(err)
	}
	if _, err := todostore.Fail(ctx, db, "t1", "s1"); err != nil {
		t.Fatal(err)
	}
	if got := projStatus(t, db, "fc"); got != "failed" {
		t.Fatalf("status = %q, want failed", got)
	}
	if _, err := todostore.Retry(ctx, db, "t1", "s1"); err != nil {
		t.Fatal(err)
	}
	if got := projStatus(t, db, "fc"); got != "pending" {
		t.Fatalf("status = %q, want pending", got)
	}
	if _, err := todostore.Start(ctx, db, "t1", "s1"); err != nil {
		t.Fatal(err)
	}
	if _, err := todostore.Complete(ctx, db, "t1", "s1"); err != nil {
		t.Fatal(err)
	}
	if got := projStatus(t, db, "fc"); got != "done" {
		t.Fatalf("status = %q, want done", got)
	}
}

// Workspace lists are isolated per working directory: one file per
// workspace, and a workspace's read never sees another's tasks.
func TestWorkspaceListsAreIsolated(t *testing.T) {
	a := filepath.Join(t.TempDir(), "ws-a.sqlite")
	b := filepath.Join(t.TempDir(), "ws-b.sqlite")
	dba, _, err := store.Open(a, todostore.Statements(), todostore.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	dbb, _, err := store.Open(b, todostore.Statements(), todostore.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := todostore.Create(ctx, dba, []item{{Text: "only in workspace a"}}, "s1"); err != nil {
		t.Fatal(err)
	}
	reply, err := todostore.Read(ctx, dbb, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(reply, "only in workspace a") {
		t.Fatalf("workspace b saw workspace a's tasks:\n%s", reply)
	}
	if !strings.Contains(reply, "(no tasks)") {
		t.Fatalf("workspace b's render must be empty:\n%s", reply)
	}
}

// --- the lean render (SPEC_TODO_LEAN): the actionable queue, the summary
// fold, and the per-transition echo. Done rows are hidden by default;
// every transition echo is the affected line plus the summary.

var rowRE = regexp.MustCompile(`^\s+t\d+ \[`)

func rowCount(reply string) int {
	n := 0
	for _, ln := range strings.Split(reply, "\n") {
		if rowRE.MatchString(ln) {
			n++
		}
	}
	return n
}

func TestReadDefaultHidesDoneRows(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, []item{{Text: "keep"}, {Text: "drop"}}, "s1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	drop := taskIDText(t, reply, "drop")
	if _, err := todostore.Start(ctx, db, drop, "s1"); err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := todostore.Complete(ctx, db, drop, "s1"); err != nil {
		t.Fatalf("complete: %v", err)
	}
	read, err := todostore.Read(ctx, db, "s1")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(read, "drop") {
		t.Errorf("done row leaked into the actionable read:\n%s", read)
	}
	if !strings.Contains(read, "keep") {
		t.Errorf("actionable row missing:\n%s", read)
	}
	if !strings.Contains(read, "1/2 done") {
		t.Errorf("summary fold missing:\n%s", read)
	}
	if rowCount(read) != 1 {
		t.Errorf("default read row count = %d, want only the actionable row:\n%s", rowCount(read), read)
	}
}

func TestAllDoneQueueRendersSummaryOnly(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	if _, err := todostore.Create(ctx, db, []item{{Text: "a"}, {Text: "b"}}, "s1"); err != nil {
		t.Fatalf("create: %v", err)
	}
	for _, id := range []string{"t1", "t2"} {
		if _, err := todostore.Start(ctx, db, id, "s1"); err != nil {
			t.Fatalf("start: %v", err)
		}
		if _, err := todostore.Complete(ctx, db, id, "s1"); err != nil {
			t.Fatalf("complete: %v", err)
		}
	}
	read, err := todostore.Read(ctx, db, "s1")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(read, "(no tasks)") {
		t.Errorf("an all-done queue must never say '(no tasks)':\n%s", read)
	}
	if !strings.Contains(read, "2/2 done") {
		t.Errorf("summary fold missing:\n%s", read)
	}
	if rowCount(read) != 0 {
		t.Errorf("all-done queue must render as summary with zero rows:\n%s", read)
	}
}

func TestReadAllShowsDoneRows(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, []item{{Text: "keep"}, {Text: "drop"}}, "s1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	drop := taskIDText(t, reply, "drop")
	if _, err := todostore.Start(ctx, db, drop, "s1"); err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := todostore.Complete(ctx, db, drop, "s1"); err != nil {
		t.Fatalf("complete: %v", err)
	}
	full, err := todostore.ReadAll(ctx, db, "s1")
	if err != nil {
		t.Fatalf("read all: %v", err)
	}
	if !strings.Contains(full, "drop") {
		t.Errorf("history read dropped the done row:\n%s", full)
	}
	if !strings.Contains(full, "[x] drop") {
		t.Errorf("done marker missing:\n%s", full)
	}
	if !strings.Contains(full, "1/2 done") {
		t.Errorf("summary fold missing:\n%s", full)
	}
	if rowCount(full) != 2 {
		t.Errorf("history row count = %d, want every row:\n%s", rowCount(full), full)
	}
}

func TestNoWaitsOnReferencesAHiddenDoneRow(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	if _, err := todostore.Create(ctx, db, []item{{Text: "gate"}, {Text: "work", DependsOn: ptrTo("gate")}}, "s1"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := todostore.Start(ctx, db, "t1", "s1"); err != nil {
		t.Fatalf("start gate: %v", err)
	}
	if _, err := todostore.Complete(ctx, db, "t1", "s1"); err != nil {
		t.Fatalf("complete gate: %v", err)
	}
	read, err := todostore.Read(ctx, db, "s1")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(read, "waits on") {
		t.Errorf("a hidden (done) dep must not leave a waits-on suffix:\n%s", read)
	}
	if !strings.Contains(read, "work") {
		t.Errorf("the unblocked dependent vanished:\n%s", read)
	}
}

func TestFailedAndForeignClaimedRowsSurviveTheFilter(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, []item{{Text: "failme"}, {Text: "watched"}}, sessA)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	failID := taskIDText(t, reply, "failme")
	watched := taskIDText(t, reply, "watched")
	if _, err := todostore.Start(ctx, db, failID, sessA); err != nil {
		t.Fatalf("start failme: %v", err)
	}
	if _, err := todostore.Fail(ctx, db, failID, sessB); err != nil {
		t.Fatalf("fail failme: %v", err)
	}
	if _, err := todostore.Start(ctx, db, watched, sessA); err != nil {
		t.Fatalf("start watched: %v", err)
	}
	read, err := todostore.Read(ctx, db, sessB)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(read, "[!] failme") {
		t.Errorf("failed row dropped by the filter:\n%s", read)
	}
	if !strings.Contains(read, "claimed by "+sessA) {
		t.Errorf("foreign-claimed row lost its claim label:\n%s", read)
	}
}

func TestEachTransitionEchoesOneAffectedLine(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	if _, err := todostore.Create(ctx, db, []item{{Text: "a"}, {Text: "b"}, {Text: "c"}}, "s1"); err != nil {
		t.Fatalf("create: %v", err)
	}
	var echoes []string
	for _, id := range []string{"t1", "t2", "t3"} {
		if _, err := todostore.Start(ctx, db, id, "s1"); err != nil {
			t.Fatalf("start %s: %v", id, err)
		}
		done, err := todostore.Complete(ctx, db, id, "s1")
		if err != nil {
			t.Fatalf("complete %s: %v", id, err)
		}
		echoes = append(echoes, done)
	}
	for i, e := range echoes {
		if rowCount(e) != 1 {
			t.Errorf("echo %d shows %d rows, want exactly the affected line:\n%s", i, rowCount(e), e)
		}
		if !strings.Contains(e, "completed") {
			t.Errorf("echo %d lost its note:\n%s", i, e)
		}
	}
}
