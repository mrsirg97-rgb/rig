package todo_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/mrsirg97-rgb/rig/v3/store"
	todostore "github.com/mrsirg97-rgb/rig/v3/store/todo"
)

func TestRejectReasonNoteTimestampSurvivesTheRefold(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := create(ctx, db, p, []item{{Text: "ship it"}}, sessA)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := taskIDText(t, reply, "ship it")
	if _, err := todostore.Claim(ctx, db, p, sessA, ""); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := todostore.Complete(ctx, db, p, id, sessA, true); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if _, err := todostore.Claim(ctx, db, p, sessC, "review"); err != nil {
		t.Fatalf("claim review: %v", err)
	}
	if _, err := todostore.Reject(ctx, db, p, id, "needs tests", sessC); err != nil {
		t.Fatalf("reject: %v", err)
	}
	rows := rawQuery(t, db, "SELECT ts FROM events WHERE op = 'reject'")
	var rejectTs string
	if !rows.Next() {
		rows.Close()
		t.Fatal("no reject event")
	}
	if err := rows.Scan(&rejectTs); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	rows.Close()
	if rejectTs == "" {
		t.Fatal("the reject event carries no ts")
	}
	stamp := func(notes string, at string) string {
		t.Helper()
		re := regexp.MustCompile(`needs tests \(by ` + regexp.QuoteMeta(sessC) + `, ([^)]*)\)`)
		m := re.FindStringSubmatch(notes)
		if m == nil {
			t.Fatalf("no reject reason note after %s:\n%s", at, notes)
		}
		return m[1]
	}
	before, err := todostore.Notes(ctx, db, p, id, sessC)
	if err != nil {
		t.Fatalf("notes: %v", err)
	}
	if got := stamp(before, "the reject"); got != rejectTs {
		t.Fatalf("the reject reason note must carry the reject event's ts: note %q, event %q\n%s", got, rejectTs, before)
	}
	if _, err := todostore.Note(ctx, db, p, id, "refold the log", sessA); err != nil {
		t.Fatalf("note: %v", err)
	}
	after, err := todostore.Notes(ctx, db, p, id, sessC)
	if err != nil {
		t.Fatalf("notes after refold: %v", err)
	}
	if got := stamp(after, "the refold"); got != rejectTs {
		t.Fatalf("the refold must keep the reject reason note's ts: note %q, event %q\n%s", got, rejectTs, after)
	}
}

func TestReadLeavesTheProjectionToTheWriters(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := create(ctx, db, p, []item{{Text: "ship it"}}, sessA)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := taskIDText(t, reply, "ship it")
	rawExec(t, db, "DELETE FROM tasks WHERE scope = 'ws'")
	rawExec(t, db, "DELETE FROM task_deps WHERE scope = 'ws'")
	if _, err := todostore.Read(ctx, db, p, sessA); err != nil {
		t.Fatalf("read: %v", err)
	}
	if _, err := todostore.ReadOne(ctx, db, p, id, sessA); err != nil {
		t.Fatalf("read one: %v", err)
	}
	rows := rawQuery(t, db, "SELECT count(*) FROM tasks WHERE scope = 'ws'")
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("no count row")
	}
	var n int
	if err := rows.Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("a read must not rewrite the projection: %d task rows after tamper", n)
	}
	if err := todostore.Rebuild(ctx, db, p); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if got := projStatus(t, db, "ship it"); got != "pending" {
		t.Fatalf("the rebuild door must replay the log, got %q", got)
	}
}

func TestCreateRefusesTaskTextPastTheNoteBound(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	if _, err := todostore.Create(ctx, db, p, todostore.CreateItem{Text: strings.Repeat("ü", 501)}, sessA); err == nil {
		t.Fatal("a task text over the note bound must refuse")
	} else if !strings.Contains(err.Error(), "bytes") {
		t.Fatalf("the refusal must name bytes, not chars: %v", err)
	}
	if _, err := todostore.Create(ctx, db, p, todostore.CreateItem{Text: strings.Repeat("a", todostore.MaxTaskLen)}, sessA); err != nil {
		t.Fatalf("a text at the bound must create: %v", err)
	}
}

func TestCreateValidatesADiamondChainInLinearTime(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	const levels = 24
	mint := func(text string) string {
		t.Helper()
		reply, err := todostore.Create(ctx, db, p, todostore.CreateItem{Text: text}, sessA)
		if err != nil {
			t.Fatalf("create %s: %v", text, err)
		}
		return taskIDText(t, reply, text)
	}
	tops := make([]string, levels+1)
	for i := 0; i <= levels; i++ {
		tops[i] = mint(fmt.Sprintf("top-%d", i))
	}
	for i := 1; i <= levels; i++ {
		for _, side := range []string{"a", "b"} {
			if _, err := todostore.Create(ctx, db, p, todostore.CreateItem{
				Text:     fmt.Sprintf("%s-%d", side, i),
				Requires: ptrTo(tops[i-1]),
				Blocks:   ptrTo(tops[i]),
			}, sessA); err != nil {
				t.Fatalf("create %s-%d: %v", side, i, err)
			}
		}
	}
	bound, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if _, err := todostore.Create(bound, db, p, todostore.CreateItem{Text: "capstone", Requires: ptrTo(tops[levels])}, sessA); err != nil {
		t.Fatalf("the diamond chain must validate in linear time: %v", err)
	}
}

func TestClaimSuffixCutsTheOwnerAtARuneBoundary(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	owner := "séanceüüüüü"
	reply, err := create(ctx, db, p, []item{{Text: "held"}}, owner)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := taskIDText(t, reply, "held")
	if _, err := todostore.Start(ctx, db, p, id, owner, false); err != nil {
		t.Fatalf("start: %v", err)
	}
	read, err := todostore.Read(ctx, db, p, sessA)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !utf8.ValidString(read) {
		t.Fatalf("the claim suffix must stay valid UTF-8:\n%s", read)
	}
	if !strings.Contains(read, "claimed by séanceüü") {
		t.Fatalf("the owner must cut at 8 runes:\n%s", read)
	}
	if strings.Contains(read, "claimed by séanceüüü") {
		t.Fatalf("the owner must cut at 8 runes, not more:\n%s", read)
	}
}

func TestCleanNoteNamesBytesNotChars(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := create(ctx, db, p, []item{{Text: "noted"}}, sessA)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := taskIDText(t, reply, "noted")
	if _, err := todostore.Note(ctx, db, p, id, strings.Repeat("ü", 501), sessA); err == nil {
		t.Fatal("a note over the bound must refuse")
	} else if !strings.Contains(err.Error(), fmt.Sprintf("bytes; max %d", todostore.MaxNoteLen)) {
		t.Fatalf("the refusal must say bytes, not chars: %v", err)
	}
}

func TestQueueCountsCountsActiveTasks(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := create(ctx, db, p, []item{{Text: "working"}, {Text: "queued"}}, sessA)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	working := taskIDText(t, reply, "working")
	if _, err := todostore.Start(ctx, db, p, working, sessA, false); err != nil {
		t.Fatalf("start: %v", err)
	}
	counts, err := todostore.Counts(ctx, db, p)
	if err != nil {
		t.Fatalf("counts: %v", err)
	}
	if counts.Active != 1 || counts.Pending != 1 {
		t.Fatalf("counts = %+v, want active 1 pending 1", counts)
	}
	if counts.Pending+counts.Active+counts.Review+counts.Done+counts.Failed != 2 {
		t.Fatalf("the counts must sum to the board: %+v", counts)
	}
}

func TestFoldSurvivesACommitFailureOnTheNextOpen(t *testing.T) {
	dir := t.TempDir()
	hash := "bbbb1111cccc2222dddd4444"
	seedLegacy(t, dir, hash, []string{"from the failed fold"})
	path := filepath.Join(dir, "todo.sqlite")
	db, _, _, err := store.Open(path, todostore.Statements(), todostore.SchemaVersion)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	ctx := context.Background()
	_, tx, err := db.Tx(ctx)
	if err != nil {
		t.Fatalf("tx: %v", err)
	}
	if _, err := todostore.Migration("", dir)(tx, todostore.SchemaVersion, todostore.SchemaVersion); err != nil {
		t.Fatalf("fold: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, hash+".sqlite.migrated")); err != nil {
		t.Fatalf("the failed fold renamed the legacy store: %v", err)
	}
	db.DB.Close()

	db2, _, report, err := store.Open(path, todostore.Statements(), todostore.SchemaVersion, todostore.Migration("", dir))
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer db2.DB.Close()
	if !strings.Contains(report, "folded 1 events from 1 store") {
		t.Fatalf("the orphaned fold must be refolded on the next open: %q", report)
	}
	read, err := todostore.Read(ctx, db2, todostore.Project{Key: hash, Label: "b"}, sessA)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(read, "from the failed fold") {
		t.Fatalf("the renamed legacy store must survive a failed commit:\n%s", read)
	}
	db3, _, report3, err := store.Open(path, todostore.Statements(), todostore.SchemaVersion, todostore.Migration("", dir))
	if err != nil {
		t.Fatalf("third open: %v", err)
	}
	defer db3.DB.Close()
	if report3 != "" {
		t.Fatalf("the recovered fold must be a no-op on the next open, got %q", report3)
	}
	rows := rawQuery(t, db3, "SELECT args FROM events WHERE op = 'create'")
	defer rows.Close()
	var texts []string
	for rows.Next() {
		var args string
		if err := rows.Scan(&args); err != nil {
			t.Fatal(err)
		}
		var payload struct {
			Tasks []map[string]any `json:"tasks"`
		}
		if json.Unmarshal([]byte(args), &payload) != nil {
			t.Fatalf("create args: %s", args)
		}
		for _, tk := range payload.Tasks {
			texts = append(texts, tk["text"].(string))
		}
	}
	if len(texts) != 1 || texts[0] != "from the failed fold" {
		t.Fatalf("the orphan must fold exactly once: %v", texts)
	}
}
