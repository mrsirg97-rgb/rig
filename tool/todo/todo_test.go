package todo_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/middleware/paths"
	"github.com/mrsirg97-rgb/rig/v2/models"
	"github.com/mrsirg97-rgb/rig/v2/store"
	sched "github.com/mrsirg97-rgb/rig/v2/store/scheduler"
	"github.com/mrsirg97-rgb/rig/v2/store/scope"
	todostore "github.com/mrsirg97-rgb/rig/v2/store/todo"
	"github.com/mrsirg97-rgb/rig/v2/swarm"
	todoapi "github.com/mrsirg97-rgb/rig/v2/tool/todo"
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
	tool := todoapi.New(newDB(t), todoapi.Interactive)
	if _, err := exec(t, tool, context.Background(), map[string]any{}); err == nil {
		t.Fatal("bare execute succeeded")
	} else if !strings.Contains(err.Error(), "action required") {
		t.Errorf("bare voice: %v", err)
	}
}

func TestUnknownActionRefusesLoudly(t *testing.T) {
	tool := todoapi.New(newDB(t), todoapi.Interactive)
	if _, err := exec(t, tool, context.Background(), map[string]any{"action": "sideways"}); err == nil {
		t.Fatal("unknown action succeeded")
	} else if !strings.Contains(err.Error(), "unknown action") {
		t.Errorf("unknown-action voice: %v", err)
	}
}

func TestCreateMissingTasksFailsLoudly(t *testing.T) {
	tool := todoapi.New(newDB(t), todoapi.Interactive)
	if _, err := exec(t, tool, context.Background(), map[string]any{"action": "create"}); err == nil {
		t.Fatal("create without tasks succeeded")
	} else if want := "action 'create' requires tasks: array of {text}"; err.Error() != want {
		t.Errorf("voice:\n%q\nwant\n%q", err.Error(), want)
	}
}

func TestCreateMalformedTasksFailLoudly(t *testing.T) {
	tool := todoapi.New(newDB(t), todoapi.Interactive)
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
	tool := todoapi.New(newDB(t), todoapi.Interactive)
	for _, action := range []string{"start", "complete", "fail", "retry", "note", "notes", "accept", "reject"} {
		if _, err := exec(t, tool, context.Background(), map[string]any{"action": action}); err == nil {
			t.Fatalf("%s without id succeeded", action)
		} else if want := "action '" + action + "' requires id"; err.Error() != want {
			t.Errorf("%s voice:\n%q", action, err.Error())
		}
	}
}

func TestMoveRefusesIdOrPosAbsence(t *testing.T) {
	tool := todoapi.New(newDB(t), todoapi.Interactive)
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
	tool := todoapi.New(db, todoapi.Interactive)
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
	tool := todoapi.New(db, todoapi.Interactive)
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
	tool := todoapi.New(newDB(t), todoapi.Interactive)
	sess := core.NewSession()
	ctx := core.WithSession(context.Background(), sess)
	reply, err := exec(t, tool, ctx, map[string]any{"action": "create", "tasks": []any{
		map[string]any{"text": "gate"},
		map[string]any{"text": "work", "requires": "gate"},
	}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !strings.Contains(reply, "2 open") || !strings.Contains(reply, "next: ") {
		t.Errorf("counts/next missing:\n%s", reply)
	}
	if !strings.Contains(reply, "requires t1") {
		t.Errorf("requires suffix missing:\n%s", reply)
	}
	read, err := exec(t, tool, core.WithSession(context.Background(), core.NewSession()), map[string]any{"action": "read"})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(read, "requires t1") {
		t.Errorf("presence labels missing:\n%s", read)
	}
}

func TestEmptyRequiresOrBlocksIsNoLink(t *testing.T) {
	tool := todoapi.New(newDB(t), todoapi.Interactive)
	ctx := core.WithSession(context.Background(), core.NewSession())
	reply, err := exec(t, tool, ctx, map[string]any{"action": "create", "tasks": []any{
		map[string]any{"text": "gate", "requires": "", "blocks": ""},
		map[string]any{"text": "work", "requires": "", "blocks": ""},
	}})
	if err != nil {
		t.Fatalf("an empty link must be the same as omitting the field, got: %v", err)
	}
	if strings.Contains(reply, "\u00b7 requires") || strings.Contains(reply, "\u00b7 blocks") {
		t.Fatalf("empty links must create no edges:\n%s", reply)
	}
	if !strings.Contains(reply, "next: t1") {
		t.Fatalf("an empty link must not block the queue:\n%s", reply)
	}
}

func TestSiblingTextLinkResolvesInOneCreate(t *testing.T) {
	tool := todoapi.New(newDB(t), todoapi.Interactive)
	ctx := core.WithSession(context.Background(), core.NewSession())
	reply, err := exec(t, tool, ctx, map[string]any{"action": "create", "tasks": []any{
		map[string]any{"text": "gate"},
		map[string]any{"text": "work", "requires": "gate"},
	}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !strings.Contains(reply, "\u00b7 requires t1") {
		t.Fatalf("a sibling's exact text must resolve to its id in the same create:\n%s", reply)
	}
	if !strings.Contains(reply, "next: t1") {
		t.Fatalf("the linked task must not be the next:\n%s", reply)
	}
}

func TestDescriptionAndSchemaSpeakWorkspace(t *testing.T) {
	tool := todoapi.New(newDB(t), todoapi.Interactive)
	d := tool.Description()
	for _, want := range []string{
		"The task queue for this workspace.",
		"different workspace than the one you started in.",
		"named by its workspace ([rig]).",
	} {
		if !strings.Contains(d, want) {
			t.Fatalf("the description misses %q:\n%s", want, d)
		}
	}
	for _, bad := range []string{"this repo", "repo differs", "named by its repo"} {
		if strings.Contains(d, bad) {
			t.Fatalf("the description must not speak repo: %q", bad)
		}
	}
	var s struct {
		Properties map[string]struct {
			Description string `json:"description"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(tool.Schema(), &s); err != nil {
		t.Fatalf("schema: %v", err)
	}
	if got := s.Properties["project"].Description; got != "Another workspace, as a path. Later calls act there until you name a different one. ~ expands." {
		t.Fatalf("the project field must read the one sentence, got %q", got)
	}
}

func TestDescriptionAndSchemaCarryTheLinkContract(t *testing.T) {
	tool := todoapi.New(newDB(t), todoapi.Interactive)
	if d := tool.Description(); !strings.Contains(d, "A task can wait for another: set requires on the one that waits.") {
		t.Fatalf("the description misses the one-line link sentence: %q", d)
	}
	var s struct {
		Properties map[string]struct {
			Items struct {
				Properties map[string]struct {
					Description string `json:"description"`
				} `json:"properties"`
			} `json:"items"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(tool.Schema(), &s); err != nil {
		t.Fatalf("schema: %v", err)
	}
	want := map[string]string{
		"requires": "The task this one waits for: its id (tN), its exact text, or its number in this list, where 1 is the first. Omit when none; null removes a link.",
		"blocks":   "The task that waits for this one, named the same way. Omit when none; null removes a link.",
	}
	for key, wantDesc := range want {
		if desc := s.Properties["tasks"].Items.Properties[key].Description; desc != wantDesc {
			t.Fatalf("%s description = %q, want %q", key, desc, wantDesc)
		}
	}
}

func TestExecRefusalsSurfaceAsVoices(t *testing.T) {
	tool := todoapi.New(newDB(t), todoapi.Interactive)
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
	tool := todoapi.New(newDB(t), todoapi.Interactive)
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
	if !strings.Contains(noted, "note added to '"+id+"'") || !strings.Contains(noted, "\u00b7 1 note") {
		t.Fatalf("note reply:\n%s", noted)
	}
	notes, err := exec(t, tool, ctx, map[string]any{"action": "notes", "id": id})
	if err != nil {
		t.Fatalf("notes: %v", err)
	}
	if !strings.Contains(notes, "on it (by "+sess.ID+", ") {
		t.Fatalf("the note must carry its session and time:\n%s", notes)
	}
	if _, err := exec(t, tool, ctx, map[string]any{"action": "complete", "id": id}); err != nil {
		t.Fatalf("complete: %v", err)
	}
	history, err := exec(t, tool, ctx, map[string]any{"action": "read", "all": true})
	if err != nil {
		t.Fatalf("read all: %v", err)
	}
	if !strings.Contains(history, "[x] swarm work") {
		t.Fatalf("the round trip must end done:\n%s", history)
	}
}

func TestCompleteTwiceIsIdempotentThroughTheTool(t *testing.T) {
	db := newDB(t)
	tool := todoapi.New(db, todoapi.Interactive)
	sess := core.NewSession()
	ctx := core.WithSession(context.Background(), sess)
	reply, err := exec(t, tool, ctx, map[string]any{"action": "create", "tasks": []any{
		map[string]any{"text": "twice"},
	}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := strings.Fields(strings.Split(reply, "\n")[2])[0]
	if _, err := exec(t, tool, ctx, map[string]any{"action": "start", "id": id}); err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := exec(t, tool, ctx, map[string]any{"action": "complete", "id": id}); err != nil {
		t.Fatalf("complete: %v", err)
	}
	before := len(rawEvents(t, db))
	again, err := exec(t, tool, ctx, map[string]any{"action": "complete", "id": id})
	if err != nil {
		t.Fatalf("complete on a done task must be a no-op success through the tool: %v", err)
	}
	if !strings.Contains(again, "[x] twice") {
		t.Errorf("the tool reply must show the done row:\n%s", again)
	}
	if !strings.Contains(again, "0 open") {
		t.Errorf("the tool reply must carry the queue summary:\n%s", again)
	}
	if got := len(rawEvents(t, db)); got != before {
		t.Errorf("the no-op must write no event: %d -> %d", before, got)
	}
}

func TestStartTwiceIsIdempotentThroughTheTool(t *testing.T) {
	db := newDB(t)
	tool := todoapi.New(db, todoapi.Interactive)
	sess := core.NewSession()
	ctx := core.WithSession(context.Background(), sess)
	reply, err := exec(t, tool, ctx, map[string]any{"action": "create", "tasks": []any{
		map[string]any{"text": "again"},
	}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := strings.Fields(strings.Split(reply, "\n")[2])[0]
	if _, err := exec(t, tool, ctx, map[string]any{"action": "start", "id": id}); err != nil {
		t.Fatalf("start: %v", err)
	}
	before := len(rawEvents(t, db))
	again, err := exec(t, tool, ctx, map[string]any{"action": "start", "id": id})
	if err != nil {
		t.Fatalf("start on an in-progress task must be a no-op success through the tool: %v", err)
	}
	if !strings.Contains(again, "[~] again") {
		t.Errorf("the tool reply must show the in-progress row:\n%s", again)
	}
	if !strings.Contains(again, "1 open") {
		t.Errorf("the tool reply must carry the queue summary:\n%s", again)
	}
	if got := len(rawEvents(t, db)); got != before {
		t.Errorf("the no-op must write no event: %d -> %d", before, got)
	}
}

func TestModeKeysTheGate(t *testing.T) {
	db := newDB(t)
	solo := todoapi.New(db, todoapi.Interactive)
	sess := core.NewSession()
	ctx := core.WithSession(context.Background(), sess)
	reply, err := exec(t, solo, ctx, map[string]any{"action": "create", "tasks": []any{
		map[string]any{"text": "solo"},
	}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := strings.Fields(strings.Split(reply, "\n")[2])[0]
	if _, err := exec(t, solo, ctx, map[string]any{"action": "claim"}); err != nil {
		t.Fatalf("claim: %v", err)
	}
	done, err := exec(t, solo, ctx, map[string]any{"action": "complete", "id": id})
	if err != nil {
		t.Fatalf("solo complete: %v", err)
	}
	if !strings.Contains(done, "[x] solo") || strings.Contains(done, "in review") {
		t.Fatalf("solo complete must land done in one call:\n%s", done)
	}

	worker := todoapi.New(db, todoapi.Worker)
	ctx = core.WithSession(context.Background(), sess)
	if _, err := exec(t, worker, ctx, map[string]any{"action": "claim"}); err == nil {
		t.Fatal("a worker's claim must refuse")
	} else if !strings.Contains(err.Error(), "the supervisor owns the board") {
		t.Errorf("claim voice = %q, want the supervisor refusal", err.Error())
	}
	if _, err := exec(t, worker, ctx, map[string]any{"action": "note", "id": id, "note": "findings"}); err != nil {
		t.Fatalf("a worker's note must land: %v", err)
	}
}

func TestWorkerModeIsReadNoteOnly(t *testing.T) {
	worker := todoapi.New(newDB(t), todoapi.Worker)
	sess := core.NewSession()
	ctx := core.WithSession(context.Background(), sess)
	reply, err := exec(t, worker, ctx, map[string]any{"action": "create", "tasks": []any{
		map[string]any{"text": "board entry"},
	}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := strings.Fields(strings.Split(reply, "\n")[2])[0]
	for _, action := range []string{"claim", "start", "complete", "fail", "accept", "reject"} {
		args := map[string]any{"action": action}
		if action != "claim" {
			args["id"] = id
		}
		if action == "reject" {
			args["note"] = "not mine to judge"
		}
		if _, err := exec(t, worker, ctx, args); err == nil {
			t.Fatalf("worker %s must refuse", action)
		} else if !strings.Contains(err.Error(), "the supervisor owns the board") {
			t.Errorf("%s voice = %q, want the supervisor refusal", action, err.Error())
		}
	}
	if _, err := exec(t, worker, ctx, map[string]any{"action": "note", "id": id, "note": "findings"}); err != nil {
		t.Fatalf("a worker's note must land: %v", err)
	}
	board, err := exec(t, worker, ctx, map[string]any{"action": "read"})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(board, id+" [ ] board entry") || !strings.Contains(board, "\u00b7 1 note") {
		t.Fatalf("the refused verbs must not move the board, and the count must show:\n%s", board)
	}
	notes, err := exec(t, worker, ctx, map[string]any{"action": "notes", "id": id})
	if err != nil {
		t.Fatalf("worker notes: %v", err)
	}
	if !strings.Contains(notes, "findings (by "+sess.ID+", ") {
		t.Fatalf("the worker's note must carry its session:\n%s", notes)
	}
}

func TestRejectRequiresItsReasonThroughTheTool(t *testing.T) {
	tool := todoapi.New(newDB(t), todoapi.Interactive)
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
	tool := todoapi.New(newDB(t), todoapi.Interactive)
	sess := core.NewSession()
	ctx := core.WithSession(context.Background(), sess)
	if _, err := exec(t, tool, ctx, map[string]any{"action": "claim", "status": "done"}); err == nil {
		t.Fatal("claim with an unknown status succeeded")
	} else if !strings.Contains(err.Error(), "unknown claim status") {
		t.Errorf("voice: %v", err)
	}
}

func TestFinishedActionListsNewestFirst(t *testing.T) {
	tool := todoapi.New(newDB(t), todoapi.Interactive)
	sess := core.NewSession()
	ctx := core.WithSession(context.Background(), sess)
	reply, err := exec(t, tool, ctx, map[string]any{"action": "create", "tasks": []any{
		map[string]any{"text": "a"},
		map[string]any{"text": "b"},
		map[string]any{"text": "c"},
		map[string]any{"text": "work"},
	}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	rows := strings.Split(reply, "\n")
	a := strings.Fields(rows[2])[0]
	b := strings.Fields(rows[3])[0]
	c := strings.Fields(rows[4])[0]
	for _, id := range []string{a, b, c} {
		if _, err := exec(t, tool, ctx, map[string]any{"action": "complete", "id": id}); err != nil {
			t.Fatalf("complete %s: %v", id, err)
		}
	}
	listed, err := exec(t, tool, ctx, map[string]any{"action": "finished", "n": 2})
	if err != nil {
		t.Fatalf("finished: %v", err)
	}
	if !strings.Contains(listed, "[x] c") || !strings.Contains(listed, "[x] b") {
		t.Fatalf("the finished list must be newest first:\n%s", listed)
	}
	if !strings.Contains(listed, "· 1 more finished · todo list finished 3") {
		t.Fatalf("the finished list names its hidden rows:\n%s", listed)
	}
	if _, err := exec(t, tool, ctx, map[string]any{"action": "finished", "n": 101}); err == nil ||
		!strings.Contains(err.Error(), "1-100") {
		t.Fatalf("over the cap must refuse naming the range, got %v", err)
	}
}

func TestReadAllTrueReturnsHistory(t *testing.T) {
	tool := todoapi.New(newDB(t), todoapi.Interactive)
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
	if !strings.Contains(defaultRead, "drop") {
		t.Errorf("default read must keep the recent finished row visible:\n%s", defaultRead)
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

func TestProjectBindsTheSession(t *testing.T) {
	db := newDB(t)
	tool := todoapi.New(db, todoapi.Interactive)
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

func TestLaunchOutsideARepoWritesItsOwnWorkspace(t *testing.T) {
	db := newDB(t)
	tool := todoapi.New(db, todoapi.Interactive)
	dir := t.TempDir()
	t.Chdir(dir)
	ctx := core.WithSession(context.Background(), core.NewSession())
	reply, err := exec(t, tool, ctx, map[string]any{
		"action": "create", "tasks": []any{map[string]any{"text": "a chore"}},
	})
	if err != nil {
		t.Fatalf("a bare write outside a repo must land in its own workspace: %v", err)
	}
	if !strings.Contains(reply, "["+scope.Label(dir)+"] ") {
		t.Fatalf("a non-repo workspace's head must name the directory, got %q", reply)
	}
	if strings.Contains(reply, "not a repo") {
		t.Fatalf("the head must not carry the not-a-repo decoration:\n%s", reply)
	}
	read, err := exec(t, tool, ctx, map[string]any{"action": "read"})
	if err != nil {
		t.Fatalf("a read must stay available: %v", err)
	}
	if !strings.HasPrefix(read, "["+scope.Label(dir)+"] ") {
		t.Fatalf("the read names the same workspace:\n%s", read)
	}
	reported, err := exec(t, tool, ctx, map[string]any{"action": "bind"})
	if err != nil {
		t.Fatalf("a bare bind reports: %v", err)
	}
	if !strings.Contains(reported, "queue: "+scope.Label(dir)+" (this workspace; not bound)") {
		t.Fatalf("an unbound report must name the workspace and say it is not bound, got %q", reported)
	}
	named, err := exec(t, tool, ctx, map[string]any{"action": "create", "project": dir,
		"tasks": []any{map[string]any{"text": "a named chore"}}})
	if err != nil {
		t.Fatalf("naming a project lets the write land: %v", err)
	}
	if !strings.Contains(named, "bound to") {
		t.Fatalf("the named write must bind:\n%s", named)
	}
	started, err := exec(t, tool, ctx, map[string]any{"action": "start", "id": "t1"})
	if err != nil {
		t.Fatalf("a bare verb after the bind: %v", err)
	}
	if !strings.Contains(started, "t1 [~]") {
		t.Fatalf("the bind must carry to the next verb:\n%s", started)
	}
}

func TestEveryReplyNamesTheQueue(t *testing.T) {
	db := newDB(t)
	tool := todoapi.New(db, todoapi.Interactive)
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
	tool := todoapi.New(db, todoapi.Interactive)
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

func TestReadWithProjectIsAPeekNotAMove(t *testing.T) {
	db := newDB(t)
	tool := todoapi.New(db, todoapi.Interactive)
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

func TestFailedWriteWithProjectLeavesTheBindingAlone(t *testing.T) {
	db := newDB(t)
	tool := todoapi.New(db, todoapi.Interactive)
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

func TestTodoCreateAndCompleteWakeTheRouter(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	tool := todoapi.New(newDB(t), todoapi.Interactive, func() {
		mu.Lock()
		calls = append(calls, "wake")
		mu.Unlock()
	})
	ctx := core.WithSession(context.Background(), core.NewSession())
	if _, err := exec(t, tool, ctx, map[string]any{
		"action": "create", "tasks": []any{map[string]any{"text": "the work"}},
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	mu.Lock()
	if len(calls) != 1 {
		t.Fatalf("wake calls after create = %d, want 1", len(calls))
	}
	mu.Unlock()
	if _, err := exec(t, tool, ctx, map[string]any{"action": "read"}); err != nil {
		t.Fatalf("read: %v", err)
	}
	if _, err := exec(t, tool, ctx, map[string]any{"action": "note", "id": "t1", "note": "a finding"}); err != nil {
		t.Fatalf("note: %v", err)
	}
	mu.Lock()
	if len(calls) != 1 {
		t.Fatalf("a read and a note must not wake the router: %v", calls)
	}
	mu.Unlock()
	if _, err := exec(t, tool, ctx, map[string]any{"action": "complete", "id": "t1"}); err != nil {
		t.Fatalf("complete: %v", err)
	}
	mu.Lock()
	if len(calls) != 2 {
		t.Fatalf("wake calls after the completion = %d, want 2: %v", len(calls), calls)
	}
	mu.Unlock()
}

type routerSpawn struct {
	mu    sync.Mutex
	calls []string
}

func (r *routerSpawn) spawn(ctx context.Context, argv []string, cwd string, env []string, observe func([]byte)) (sched.SpawnResult, error) {
	r.mu.Lock()
	r.calls = append(r.calls, strings.Join(argv, " "))
	r.mu.Unlock()
	return sched.SpawnResult{Exit: 0, Stdout: "done\n"}, nil
}

func TestTodoCreateWakesTheRouterToClaimIt(t *testing.T) {
	db := newDB(t)
	home := t.TempDir()
	schedDB, _, _, err := store.Open(filepath.Join(home, "global.sqlite"), sched.Statements(), sched.SchemaVersion)
	if err != nil {
		t.Fatalf("sched store: %v", err)
	}
	dir := t.TempDir()
	proj := todostore.ProjectOf(dir)
	spawn := &routerSpawn{}
	fetch := func(url string) (json.RawMessage, error) {
		if strings.HasSuffix(url, "/v1/models") {
			return json.RawMessage(`{"data":[],"object":"list"}`), nil
		}
		if strings.HasSuffix(url, "/running") {
			return json.RawMessage(`{"running":[]}`), nil
		}
		return nil, fmt.Errorf("unexpected url %s", url)
	}
	tbl, err := models.New(models.Model{ID: "qwen3.8-workers", Window: 65536, MaxTokens: 8192, Reserve: 8192, KeepRecent: 16384, Role: models.RoleWorker})
	if err != nil {
		t.Fatalf("models: %v", err)
	}
	ctl := swarm.New(swarm.Opts{
		TodoDB:  db,
		SchedDB: schedDB,
		Home:    home,
		Project: func(ctx context.Context, session string) (todostore.Project, error) { return proj, nil },
		Cwd:     dir, WorkerCmd: []string{"/x/rig"}, Fetch: fetch, Spawn: spawn.spawn,
		SwapURL: "http://127.0.0.1:8090", Sandbox: "off", RigHome: t.TempDir(), StateDir: t.TempDir(),
		DefaultModel: "qwen3.8-workers", Models: func() models.Table { return tbl },
	})
	defer ctl.Stop()
	sess := core.NewSession()
	tool := todoapi.New(db, todoapi.Interactive, ctl.Wake)
	ctx := core.WithSession(context.Background(), sess)
	if _, err := ctl.Start(ctx, swarm.StartOpts{Count: 1, Role: "worker"}); err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := exec(t, tool, ctx, map[string]any{
		"action": "create", "project": dir, "tasks": []any{map[string]any{"text": "the work"}},
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		spawn.mu.Lock()
		n := len(spawn.calls)
		argv := ""
		if n > 0 {
			argv = spawn.calls[0]
		}
		spawn.mu.Unlock()
		if n == 1 {
			if !strings.Contains(argv, "the work") {
				t.Fatalf("the router's handout must carry the task brief: %s", argv)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the create must wake the router to a spawn, calls = %d", n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
