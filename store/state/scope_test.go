package state_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/store"
	"github.com/mrsirg97-rgb/rig/store/state"
	"github.com/mrsirg97-rgb/rig/store/state/domain"
)

func TestToolCallsAreSessionScoped(t *testing.T) {
	db := openStore(t)
	ctx := context.Background()
	for _, sid := range []string{"s-a", "s-b"} {
		if err := state.RecordSession(ctx, db, sid, "/w", "m", "v"); err != nil {
			t.Fatal(err)
		}
		if _, err := state.RecordMessage(ctx, db, sid, "assistant", "", nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := state.RecordToolCall(ctx, db, "s-a", 1, "call_0", "bash", `{"cmd":"ls"}`); err != nil {
		t.Fatal(err)
	}
	if err := state.RecordToolCall(ctx, db, "s-b", 2, "call_0", "bash", `{"cmd":"ls"}`); err != nil {
		t.Fatalf("the second session's same-id call must land: %v", err)
	}
	if err := state.RecordToolResult(ctx, db, "s-a", 1, "call_0", "out-a", nil); err != nil {
		t.Fatal(err)
	}
	if err := state.RecordToolResult(ctx, db, "s-b", 2, "call_0", "out-b", nil); err != nil {
		t.Fatal(err)
	}
	a := mustRead(t, db, func(c context.Context) (any, error) {
		return domain.NewToolCallDomain().GetToolCall(c, "s-a", 1, "call_0").Row()
	}).(*domain.ToolCall)
	b := mustRead(t, db, func(c context.Context) (any, error) {
		return domain.NewToolCallDomain().GetToolCall(c, "s-b", 2, "call_0").Row()
	}).(*domain.ToolCall)
	if a.Result == nil || *a.Result != "out-a" {
		t.Fatalf("session a's result overwritten: %+v", a)
	}
	if b.Result == nil || *b.Result != "out-b" {
		t.Fatalf("session b's result overwritten: %+v", b)
	}
}

func TestToolCallReuseAcrossTurnsStaysScopedToItsMessage(t *testing.T) {
	db := openStore(t)
	ctx := context.Background()
	if err := state.RecordSession(ctx, db, "s1", "/w", "m", "v"); err != nil {
		t.Fatal(err)
	}
	if _, err := state.RecordMessage(ctx, db, "s1", "assistant", "", nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := state.RecordMessage(ctx, db, "s1", "assistant", "", nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := state.RecordToolCall(ctx, db, "s1", 1, "call_0", "bash", `{}`); err != nil {
		t.Fatal(err)
	}
	if err := state.RecordToolCall(ctx, db, "s1", 2, "call_0", "bash", `{}`); err != nil {
		t.Fatalf("a reused wire id across turns must land under its own message: %v", err)
	}
	if err := state.RecordToolResult(ctx, db, "s1", 1, "call_0", "r1", nil); err != nil {
		t.Fatal(err)
	}
	if err := state.RecordToolResult(ctx, db, "s1", 2, "call_0", "r2", nil); err != nil {
		t.Fatal(err)
	}
	first := mustRead(t, db, func(c context.Context) (any, error) {
		return domain.NewToolCallDomain().GetToolCall(c, "s1", 1, "call_0").Row()
	}).(*domain.ToolCall)
	second := mustRead(t, db, func(c context.Context) (any, error) {
		return domain.NewToolCallDomain().GetToolCall(c, "s1", 2, "call_0").Row()
	}).(*domain.ToolCall)
	if first.Result == nil || *first.Result != "r1" {
		t.Fatalf("the first turn's result mis-attributed: %+v", first)
	}
	if second.Result == nil || *second.Result != "r2" {
		t.Fatalf("the second turn's result mis-attributed: %+v", second)
	}
}

func TestRecorderMintsStorageIDsForDuplicateWireIDs(t *testing.T) {
	db, _, _, err := store.Open(filepath.Join(t.TempDir(), "sessions.sqlite"), state.Statements(), state.SchemaVersion)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	sid := "rec-dup"
	rec := state.NewRecorder(&scripted{inputs: []string{"do it"}}, db, "/tmp/wt", "model-x", "0.1.0", sid, core.NewSession())
	ctx := context.Background()
	if _, err := rec.Input(ctx); err != nil {
		t.Fatal(err)
	}
	rec.Notify(core.ToolCallEvent{Call: core.ToolCall{ID: "c1", Name: "bash", Args: json.RawMessage(`{}`)}})
	rec.Notify(core.ToolCallEvent{Call: core.ToolCall{ID: "c1", Name: "bash", Args: json.RawMessage(`{}`)}})
	rec.Notify(core.Done{StopReason: "end_turn"})
	rec.Notify(core.ToolResult{ID: "c1", Content: "r1"})
	rec.Notify(core.ToolResult{ID: "c1", Content: "r2"})
	if err := rec.Close("ok"); err != nil {
		t.Fatal(err)
	}
	first := mustRead(t, db, func(c context.Context) (any, error) {
		return domain.NewToolCallDomain().GetToolCall(c, sid, 2, "c1").Row()
	}).(*domain.ToolCall)
	second := mustRead(t, db, func(c context.Context) (any, error) {
		return domain.NewToolCallDomain().GetToolCall(c, sid, 2, "c1-2").Row()
	}).(*domain.ToolCall)
	if first.Result == nil || *first.Result != "r1" {
		t.Fatalf("the first duplicate result mis-attributed: %+v", first)
	}
	if second.Result == nil || *second.Result != "r2" {
		t.Fatalf("the second duplicate result mis-attributed: %+v", second)
	}
}

func TestRecorderRelandAttributesDuplicateCallIDs(t *testing.T) {
	db, _, _, err := store.Open(filepath.Join(t.TempDir(), "sessions.sqlite"), state.Statements(), state.SchemaVersion)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	sid := "rec-rel-dup"
	ctx := context.Background()
	if err := state.RecordSession(ctx, db, sid, "/tmp/wt", "model-x", "0.1.0"); err != nil {
		t.Fatal(err)
	}
	sess := core.NewSession()
	sess.Append(core.Message{Role: core.RoleUser, Content: "[compaction] sum"})
	sess.Append(core.Message{Role: core.RoleUser, Content: "tail"})
	sess.Append(core.Message{Role: core.RoleAssistant, Content: "a1", ToolCalls: []core.ToolCall{{ID: "c2", Name: "bash", Args: json.RawMessage(`{}`)}}})
	sess.Append(core.Message{Role: core.RoleTool, ToolID: "c2", Content: "out1"})
	sess.Append(core.Message{Role: core.RoleAssistant, Content: "a2", ToolCalls: []core.ToolCall{{ID: "c2", Name: "bash", Args: json.RawMessage(`{}`)}}})
	sess.Append(core.Message{Role: core.RoleTool, ToolID: "c2", Content: "out2"})
	rec := state.NewRecorder(&scripted{}, db, "/tmp/wt", "model-x", "0.1.0", sid, sess)
	rec.Notify(core.Compacted{Summary: "[compaction] sum", Usage: core.Usage{Prompt: 1, Completion: 1}})

	first := mustRead(t, db, func(c context.Context) (any, error) {
		return domain.NewToolCallDomain().GetToolCall(c, sid, 3, "c2").Row()
	}).(*domain.ToolCall)
	second := mustRead(t, db, func(c context.Context) (any, error) {
		return domain.NewToolCallDomain().GetToolCall(c, sid, 4, "c2").Row()
	}).(*domain.ToolCall)
	if first.Result == nil || *first.Result != "out1" {
		t.Fatalf("the first relanded result mis-attributed: %+v", first)
	}
	if second.Result == nil || *second.Result != "out2" {
		t.Fatalf("the second relanded result mis-attributed: %+v", second)
	}
}

func TestRecorderRelandKeepsTheToolFailure(t *testing.T) {
	db, _, _, err := store.Open(filepath.Join(t.TempDir(), "sessions.sqlite"), state.Statements(), state.SchemaVersion)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	sid := "rec-rel-err"
	ctx := context.Background()
	if err := state.RecordSession(ctx, db, sid, "/tmp/wt", "model-x", "0.1.0"); err != nil {
		t.Fatal(err)
	}
	if _, err := state.RecordMessage(ctx, db, sid, "user", "go", nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := state.RecordMessage(ctx, db, sid, "assistant", "", nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := state.RecordToolCall(ctx, db, sid, 2, "c1", "bash", `{}`); err != nil {
		t.Fatal(err)
	}
	failure := "bash: ls: no such file"
	if err := state.RecordToolResult(ctx, db, sid, 2, "c1", "bash: ls: no such file", &failure); err != nil {
		t.Fatal(err)
	}
	sess := core.NewSession()
	sess.Append(core.Message{Role: core.RoleUser, Content: "[compaction] sum"})
	sess.Append(core.Message{Role: core.RoleAssistant, Content: "a", ToolCalls: []core.ToolCall{{ID: "c1", Name: "bash", Args: json.RawMessage(`{}`)}}})
	sess.Append(core.Message{Role: core.RoleTool, ToolID: "c1", Content: "bash: ls: no such file"})
	rec := state.NewRecorder(&scripted{}, db, "/tmp/wt", "model-x", "0.1.0", sid, sess)
	rec.Notify(core.Compacted{Summary: "[compaction] sum", Usage: core.Usage{Prompt: 1, Completion: 1}})

	row := mustRead(t, db, func(c context.Context) (any, error) {
		return domain.NewToolCallDomain().GetToolCall(c, sid, 4, "c1").Row()
	}).(*domain.ToolCall)
	if row.Err == nil || *row.Err != "bash: ls: no such file" {
		t.Fatalf("the relanded failure dropped the err column: %+v", row)
	}
}

func TestRecorderEmptyCompletionDropsUsage(t *testing.T) {
	db, _, _, err := store.Open(filepath.Join(t.TempDir(), "sessions.sqlite"), state.Statements(), state.SchemaVersion)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	sid := "rec-empty"
	rec := state.NewRecorder(&scripted{inputs: []string{"do it"}}, db, "/tmp/wt", "model-x", "0.1.0", sid, core.NewSession())
	ctx := context.Background()
	if _, err := rec.Input(ctx); err != nil {
		t.Fatal(err)
	}
	rec.Notify(core.Done{StopReason: "end_turn", Usage: core.Usage{Prompt: 7, Completion: 3}})
	if err := rec.Close("ok"); err != nil {
		t.Fatal(err)
	}
	u := mustRead(t, db, func(c context.Context) (any, error) {
		return domain.NewUsageDomain().GetUsage(c, 1).Row()
	}).(*domain.Usage)
	if u != nil {
		t.Fatalf("an empty completion's usage attached to the previous message: %+v", u)
	}
}

func TestMigrationBackfillsToolCallSessions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.sqlite")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	v1 := []string{
		`CREATE TABLE IF NOT EXISTS "tool_calls" (
  "id" TEXT NOT NULL,
  "args" TEXT NOT NULL,
  "ended_at" TIMESTAMP,
  "err" TEXT,
  "message_seq" INTEGER NOT NULL,
  "name" TEXT NOT NULL,
  "result" TEXT,
  "started_at" TIMESTAMP NOT NULL,
  PRIMARY KEY ("id")
)`,
		`CREATE TABLE IF NOT EXISTS "messages" (
  "seq" INTEGER NOT NULL,
  "content" TEXT NOT NULL,
  "created_at" TIMESTAMP NOT NULL,
  "reasoning" TEXT,
  "role" TEXT NOT NULL,
  "session_id" TEXT NOT NULL,
  "tool_id" TEXT,
  PRIMARY KEY ("seq")
)`,
	}
	for _, s := range v1 {
		if _, err := raw.Exec(s); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := raw.Exec(`CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO meta (key, value) VALUES ('schema_version', '1')`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO messages (seq, content, created_at, role, session_id) VALUES (1, '', '2026-01-01T00:00:00Z', 'assistant', 's1'), (2, '', '2026-01-01T00:00:00Z', 'assistant', 's2')`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO tool_calls (id, args, message_seq, name, started_at) VALUES ('call_0', '{}', 1, 'bash', '2026-01-01T00:00:00Z'), ('call_1', '{}', 2, 'bash', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	raw.Close()

	db, _, report, err := store.Open(path, state.Statements(), state.SchemaVersion, state.Migration())
	if err != nil {
		t.Fatalf("open after migration: %v", err)
	}
	defer db.DB.Close()
	if report == "" {
		t.Fatal("the migration must report its work")
	}
	first := mustRead(t, db, func(c context.Context) (any, error) {
		return domain.NewToolCallDomain().GetToolCall(c, "s1", 1, "call_0").Row()
	}).(*domain.ToolCall)
	second := mustRead(t, db, func(c context.Context) (any, error) {
		return domain.NewToolCallDomain().GetToolCall(c, "s2", 2, "call_1").Row()
	}).(*domain.ToolCall)
	if first.SessionId != "s1" || second.SessionId != "s2" {
		t.Fatalf("session_id not backfilled: %+v %+v", first, second)
	}
}
