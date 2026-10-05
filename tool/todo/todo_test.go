package todo_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/v2"
	"github.com/mrsirg97-rgb/rig/v2/broadcast"
	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/evt"
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
	if _, ok := args["scope"]; !ok {
		wd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		args["scope"] = wd
	}
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
	if _, err := exec(t, tool, context.Background(), map[string]any{"action": "sideways", "scope": t.TempDir()}); err == nil {
		t.Fatal("unknown action succeeded")
	} else if !strings.Contains(err.Error(), "unknown action") {
		t.Errorf("unknown-action voice: %v", err)
	}
}

func TestCreateWithoutTextFailsLoudly(t *testing.T) {
	tool := todoapi.New(newDB(t), todoapi.Interactive)
	if _, err := exec(t, tool, context.Background(), map[string]any{"action": "create"}); err == nil {
		t.Fatal("create without text succeeded")
	} else if want := "action 'create' requires text"; err.Error() != want {
		t.Errorf("voice:\n%q\nwant\n%q", err.Error(), want)
	}
}

func TestCreateMalformedLinksFailLoudly(t *testing.T) {
	tool := todoapi.New(newDB(t), todoapi.Interactive)
	for _, link := range []any{1, 2.5, true, map[string]any{"id": "t1"}, []any{"t1"}} {
		_, err := exec(t, tool, context.Background(), map[string]any{"action": "create", "text": "a", "requires": link})
		if err == nil || !strings.Contains(err.Error(), "requires must be a task id (tN) from a reply, or null") {
			t.Fatalf("a link that is not an id or null must refuse by name, %v gave %v", link, err)
		}
	}
	if _, err := exec(t, tool, context.Background(), map[string]any{"action": "create", "text": 1}); err == nil {
		t.Fatal("a text that is not a string landed")
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
	reply, err := exec(t, tool, ctx, map[string]any{"action": "create", "text": "attributed"})
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
	reply, err := exec(t, tool, context.Background(), map[string]any{"action": "create", "text": "anon work"})
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
	reply, err := createAll(t, tool, ctx, []map[string]any{
		map[string]any{"text": "gate"},
		map[string]any{"text": "work", "requires": "gate"},
	})
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
	reply, err := createAll(t, tool, ctx, []map[string]any{
		map[string]any{"text": "gate", "requires": "", "blocks": ""},
		map[string]any{"text": "work", "requires": "", "blocks": ""},
	})
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
	reply, err := createAll(t, tool, ctx, []map[string]any{
		map[string]any{"text": "gate"},
		map[string]any{"text": "work", "requires": "gate"},
	})
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
		"the task queue for the current workspace",
		"every call names its scope: the workspace path, or global.",
		"scoped by its workspace ([rig]).",
	} {
		if !strings.Contains(d, want) {
			t.Fatalf("the description misses %q:\n%s", want, d)
		}
	}
	for _, bad := range []string{
		"this repo", "repo differs", "named by its repo",
		"another workspace than the one you started in", "later calls act there",
	} {
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
	if got := s.Properties["scope"].Description; got != "the workspace this acts on, as a path; or global. required." {
		t.Fatalf("the scope field must read the one sentence, got %q", got)
	}
}

func TestDescriptionAndSchemaCarryTheLinkContract(t *testing.T) {
	tool := todoapi.New(newDB(t), todoapi.Interactive)
	for _, want := range []string{"one task per create", "create the task it waits for first, then link", "copy them, never invent them"} {
		if !strings.Contains(tool.Description(), want) {
			t.Fatalf("the description must carry %q:\n%s", want, tool.Description())
		}
	}
	var s struct {
		Properties map[string]struct {
			Type        any    `json:"type"`
			Description string `json:"description"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(tool.Schema(), &s); err != nil {
		t.Fatalf("schema: %v", err)
	}
	if _, ok := s.Properties["tasks"]; ok {
		t.Fatal("the schema must not offer a tasks array: one task per create")
	}
	want := map[string]string{
		"text":     "for create: what needs doing. one task per call.",
		"requires": "for create: the task this one waits for, as its id from a reply (t12). omit when none; null removes a link.",
		"blocks":   "for create: the task that waits for this one, as its id. omit when none; null removes a link.",
	}
	for key, wantDesc := range want {
		if desc := s.Properties[key].Description; desc != wantDesc {
			t.Fatalf("%s description = %q, want %q", key, desc, wantDesc)
		}
	}
	for _, key := range []string{"requires", "blocks"} {
		if fmt.Sprint(s.Properties[key].Type) != "[string null]" {
			t.Fatalf("%s takes a string or null, never a number: %v", key, s.Properties[key].Type)
		}
	}
}

func TestExecRefusalsSurfaceAsVoices(t *testing.T) {
	tool := todoapi.New(newDB(t), todoapi.Interactive)
	sessA := core.NewSession()
	sessB := core.NewSession()
	ctxA := core.WithSession(context.Background(), sessA)
	reply, err := exec(t, tool, ctxA, map[string]any{"action": "create", "text": "owned"})
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
	reply, err := createAll(t, tool, ctx, []map[string]any{
		map[string]any{"text": "swarm work"},
	})
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
	reply, err := createAll(t, tool, ctx, []map[string]any{
		map[string]any{"text": "twice"},
	})
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
	reply, err := createAll(t, tool, ctx, []map[string]any{
		map[string]any{"text": "again"},
	})
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
	reply, err := createAll(t, solo, ctx, []map[string]any{
		map[string]any{"text": "solo"},
	})
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
	reply, err := createAll(t, worker, ctx, []map[string]any{
		map[string]any{"text": "board entry"},
	})
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
	reply, err := createAll(t, tool, ctx, []map[string]any{
		map[string]any{"text": "a"},
		map[string]any{"text": "b"},
		map[string]any{"text": "c"},
		map[string]any{"text": "work"},
	})
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
	reply, err := createAll(t, tool, ctx, []map[string]any{
		map[string]any{"text": "keep"},
		map[string]any{"text": "drop"},
	})
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

func TestTodoScopeRefusesAnAbsentDirectoryByName(t *testing.T) {
	tool := todoapi.New(newDB(t), todoapi.Interactive)
	aFile := filepath.Join(t.TempDir(), "a-file")
	if err := os.WriteFile(aFile, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{filepath.Join(t.TempDir(), "absent"), aFile} {
		payload, err := json.Marshal(map[string]any{"action": "read", "scope": bad})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tool.Exec(core.WithSession(context.Background(), core.NewSession()), payload); err == nil ||
			!strings.Contains(err.Error(), "todo: no such project directory: "+bad) {
			t.Fatalf("a scope that is not a directory must refuse by name, got %v", err)
		}
	}
}

func TestACallWithoutScopeRefusesNamingTheRule(t *testing.T) {
	tool := todoapi.New(newDB(t), todoapi.Interactive)
	payload, err := json.Marshal(map[string]any{"action": "create", "text": "x"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = tool.Exec(core.WithSession(context.Background(), core.NewSession()), payload)
	if err == nil || !strings.Contains(err.Error(), "scope required: name the workspace this acts on, as a path, or global") {
		t.Fatalf("a call without scope must refuse naming the rule, got %v", err)
	}
}

func TestGlobalScopeLandsInTheGlobalQueue(t *testing.T) {
	db := newDB(t)
	tool := todoapi.New(db, todoapi.Interactive)
	ctx := core.WithSession(context.Background(), core.NewSession())
	if _, err := exec(t, tool, ctx, map[string]any{
		"action": "create", "scope": "global", "text": "everywhere"}); err != nil {
		t.Fatalf("create at global: %v", err)
	}
	other := t.TempDir()
	read, err := exec(t, tool, ctx, map[string]any{"action": "read", "scope": other})
	if err != nil {
		t.Fatalf("read the project queue: %v", err)
	}
	if strings.Contains(read, "everywhere") {
		t.Fatalf("the global queue must not leak into a project's:\n%s", read)
	}
	read, err = exec(t, tool, ctx, map[string]any{"action": "read", "scope": "global"})
	if err != nil {
		t.Fatalf("read the global queue: %v", err)
	}
	if !strings.Contains(read, "[global] 1 open") || !strings.Contains(read, "everywhere") {
		t.Fatalf("the global scope must hold the task:\n%s", read)
	}
}

func TestStartAndClaimRepliesNameTheScope(t *testing.T) {
	db := newDB(t)
	tool := todoapi.New(db, todoapi.Interactive)
	ctx := core.WithSession(context.Background(), core.NewSession())
	home := t.TempDir()
	if _, err := exec(t, tool, ctx, map[string]any{
		"action": "create", "scope": home, "text": "the work"}); err != nil {
		t.Fatal(err)
	}
	started, err := exec(t, tool, ctx, map[string]any{"action": "start", "id": "t1", "scope": home})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(started, "\u00b7 scope "+home) {
		t.Fatalf("the start reply must carry the scope on the row:\n%s", started)
	}
	if _, err := exec(t, tool, ctx, map[string]any{
		"action": "create", "scope": home, "text": "the next"}); err != nil {
		t.Fatal(err)
	}
	claimed, err := exec(t, tool, ctx, map[string]any{"action": "claim", "scope": home})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(claimed, "\u00b7 scope "+home) {
		t.Fatalf("the claim reply must carry the scope on the row:\n%s", claimed)
	}
}

func TestStartReplyNamesTheGlobalScope(t *testing.T) {
	db := newDB(t)
	tool := todoapi.New(db, todoapi.Interactive)
	ctx := core.WithSession(context.Background(), core.NewSession())
	if _, err := exec(t, tool, ctx, map[string]any{
		"action": "create", "scope": "global", "text": "the work"}); err != nil {
		t.Fatal(err)
	}
	started, err := exec(t, tool, ctx, map[string]any{"action": "start", "id": "t1", "scope": "global"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(started, "\u00b7 scope global") {
		t.Fatalf("the global start reply must carry scope global:\n%s", started)
	}
}

func TestLaunchOutsideARepoWritesItsOwnWorkspace(t *testing.T) {
	db := newDB(t)
	tool := todoapi.New(db, todoapi.Interactive)
	dir := t.TempDir()
	ctx := core.WithSession(context.Background(), core.NewSession())
	reply, err := exec(t, tool, ctx, map[string]any{
		"action": "create", "scope": dir, "text": "a chore",
	})
	if err != nil {
		t.Fatalf("a write outside a repo must land in its own workspace: %v", err)
	}
	if !strings.Contains(reply, "["+scope.Label(dir)+"] ") {
		t.Fatalf("a non-repo workspace's head must name the directory, got %q", reply)
	}
	if strings.Contains(reply, "not a repo") {
		t.Fatalf("the head must not carry the not-a-repo decoration:\n%s", reply)
	}
	read, err := exec(t, tool, ctx, map[string]any{"action": "read", "scope": dir})
	if err != nil {
		t.Fatalf("a read must stay available: %v", err)
	}
	if !strings.HasPrefix(read, "["+scope.Label(dir)+"] ") {
		t.Fatalf("the read names the same workspace:\n%s", read)
	}
}

func TestScopeExpandsTildeAtTheBoundary(t *testing.T) {
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
	args := map[string]any{"action": "create", "text": "tilde task", "scope": "~/p"}
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
	read, err := exec(t, tool, ctx, map[string]any{"action": "read", "scope": proj})
	if err != nil {
		t.Fatalf("read the expanded project: %v", err)
	}
	if !strings.Contains(read, "tilde task") {
		t.Fatalf("~ must expand to the project at the boundary:\n%s", read)
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
		"action": "create", "text": "the work",
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
	prompt, _ := sched.PromptFrom(ctx)
	r.calls = append(r.calls, strings.Join(argv, " ")+" "+prompt)
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
	engine := evt.NewEngine()
	go engine.Start(context.Background())
	defer engine.Stop()
	room := broadcast.NewRoom("fleet", func(origin int64) broadcast.Transport {
		return broadcast.NewLoopTransport(origin, engine, rig.PriorityFleet)
	})
	ctl := swarm.New(swarm.Opts{
		TodoDB:  db,
		SchedDB: schedDB,
		Home:    home,
		Project: func(ctx context.Context, session string) (todostore.Project, error) { return proj, nil },
		Cwd:     dir, WorkerCmd: []string{"/x/rig"}, Fetch: fetch, Spawn: spawn.spawn,
		SwapURL: "http://127.0.0.1:8090", Sandbox: "off", RigHome: t.TempDir(), StateDir: t.TempDir(),
		DefaultModel: "qwen3.8-workers", Models: func() models.Table { return tbl },
		Engine: engine, Room: room,
	})
	defer ctl.Stop()
	sess := core.NewSession()
	tool := todoapi.New(db, todoapi.Interactive, ctl.Wake)
	ctx := core.WithSession(context.Background(), sess)
	if _, err := ctl.Start(ctx, swarm.StartOpts{Count: 1, Role: "worker"}); err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := exec(t, tool, ctx, map[string]any{
		"action": "create", "scope": dir, "text": "the work",
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

func TestTodoUpdateRemovingARequiresLinkWakesTheRouter(t *testing.T) {
	var mu sync.Mutex
	var calls int
	tool := todoapi.New(newDB(t), todoapi.Interactive, func() {
		mu.Lock()
		calls++
		mu.Unlock()
	})
	ctx := core.WithSession(context.Background(), core.NewSession())
	if _, err := exec(t, tool, ctx, map[string]any{
		"action": "create", "text": "the blocker",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := exec(t, tool, ctx, map[string]any{
		"action": "create", "text": "the dependent", "requires": "t1",
	}); err != nil {
		t.Fatalf("the linked create: %v", err)
	}
	mu.Lock()
	if calls != 2 {
		t.Fatalf("wake calls = %d, want one per create", calls)
	}
	mu.Unlock()
	shown, err := exec(t, tool, ctx, map[string]any{"action": "read"})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(shown, "requires t1") {
		t.Fatalf("the link must show before the update:\n%s", shown)
	}
	if _, err := exec(t, tool, ctx, map[string]any{
		"action": "create", "text": "the dependent", "requires": nil,
	}); err != nil {
		t.Fatalf("the update: %v", err)
	}
	mu.Lock()
	if calls != 3 {
		t.Fatalf("removing the requires link must wake the router, wake calls = %d", calls)
	}
	mu.Unlock()
	shown, err = exec(t, tool, ctx, map[string]any{"action": "read"})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(shown, "requires") {
		t.Fatalf("the update must clear the link:\n%s", shown)
	}
}

func TestALinkIsAnIdFromAReplyAndNothingElse(t *testing.T) {
	tool := todoapi.New(newDB(t), todoapi.Interactive)
	ctx := core.WithSession(context.Background(), core.NewSession())
	if _, err := exec(t, tool, ctx, map[string]any{"action": "create", "text": "gate"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	_, err := exec(t, tool, ctx, map[string]any{"action": "create", "text": "work", "requires": 1})
	if err == nil || !strings.Contains(err.Error(), "requires must be a task id") {
		t.Fatalf("a number is not a link: %v", err)
	}
	_, err = exec(t, tool, ctx, map[string]any{"action": "create", "text": "work", "requires": "gate"})
	if err == nil || !strings.Contains(err.Error(), "requires 'gate' not found") || !strings.Contains(err.Error(), "a link is a task id from a reply") {
		t.Fatalf("a text is not a link, and the refusal teaches the one form: %v", err)
	}
	reply, err := exec(t, tool, ctx, map[string]any{"action": "create", "text": "work", "requires": "t1"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !strings.Contains(reply, "t2 [ ] work \u00b7 requires t1") {
		t.Fatalf("the id from the reply links:\n%s", reply)
	}
}

func TestACreateThatCannotLinkShowsTheQueue(t *testing.T) {
	tool := todoapi.New(newDB(t), todoapi.Interactive)
	ctx := core.WithSession(context.Background(), core.NewSession())
	if _, err := exec(t, tool, ctx, map[string]any{"action": "create", "text": "already here"}); err != nil {
		t.Fatal(err)
	}
	_, err := exec(t, tool, ctx, map[string]any{"action": "create", "text": "new", "requires": "}, 2"})
	if err == nil {
		t.Fatal("an unresolvable link lands nothing")
	}
	for _, want := range []string{"requires '}, 2' not found", "a task id from a reply", "t1", "already here"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the refusal teaches the form and shows the queue, missing %q:\n%v", want, err)
		}
	}
	if strings.Contains(err.Error(), "new") || strings.Contains(err.Error(), "t2 [") {
		t.Fatalf("the queue shown is the one that exists, never the task that did not land:\n%v", err)
	}
}

var taskLine = regexp.MustCompile(`\b(t\d+) \[[~x!r ]\] (.*?)(?: · |$)`)

func createAll(t *testing.T, tool core.Tool, ctx context.Context, tasks []map[string]any, extra ...map[string]any) (string, error) {
	t.Helper()
	call := func(fields map[string]any) (string, error) {
		args := map[string]any{"action": "create"}
		for _, e := range extra {
			for k, v := range e {
				args[k] = v
			}
		}
		for k, v := range fields {
			args[k] = v
		}
		return exec(t, tool, ctx, args)
	}
	var reply string
	var err error
	for _, task := range tasks {
		if reply, err = call(map[string]any{"text": task["text"]}); err != nil {
			return reply, err
		}
	}
	ids := map[string]string{}
	for _, line := range strings.Split(reply, "\n") {
		if m := taskLine.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			ids[m[2]] = m[1]
		}
	}
	for _, task := range tasks {
		fields := map[string]any{"text": task["text"]}
		linked := false
		for _, key := range []string{"requires", "blocks"} {
			v, ok := task[key]
			if !ok {
				continue
			}
			linked = true
			if s, isText := v.(string); isText {
				if id, known := ids[s]; known {
					v = id
				}
			}
			fields[key] = v
		}
		if !linked {
			continue
		}
		if reply, err = call(fields); err != nil {
			return reply, err
		}
	}
	return reply, nil
}
