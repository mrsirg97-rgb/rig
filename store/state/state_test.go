package state_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/store"
	"github.com/mrsirg97-rgb/rig/store/state"
	"github.com/mrsirg97-rgb/rig/store/state/domain"
)

func mustRead(t *testing.T, db store.DB, get func(ctx context.Context) (any, error)) any {
	t.Helper()
	c, tx, err := db.Tx(context.Background())
	if err != nil {
		t.Fatalf("read tx: %v", err)
	}
	defer tx.Rollback()
	v, err := get(c)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return v
}

func openStore(t *testing.T) store.DB {
	t.Helper()
	db, _, _, err := store.Open(filepath.Join(t.TempDir(), "sessions.sqlite"), state.Statements(), state.SchemaVersion)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return db
}

func TestStateRecordsAndReadsBack(t *testing.T) {
	db := openStore(t)
	ctx := context.Background()

	if err := state.RecordSession(ctx, db, "s1", "/tmp/wt", "model-x", "0.1.0"); err != nil {
		t.Fatalf("record session: %v", err)
	}
	seq1, err := state.RecordMessage(ctx, db, "s1", "user", "hello", nil, nil, nil)
	if err != nil {
		t.Fatalf("record message: %v", err)
	}
	reasoning := "because"
	seq2, err := state.RecordMessage(ctx, db, "s1", "assistant", "", &reasoning, nil, nil)
	if err != nil {
		t.Fatalf("record reasoning message: %v", err)
	}
	if seq2 <= seq1 {
		t.Fatalf("seq minting not strictly increasing: %d then %d", seq1, seq2)
	}
	if err := state.RecordToolCall(ctx, db, "s1", seq2, "call_1", "bash", `{"cmd":"ls"}`); err != nil {
		t.Fatalf("record tool call: %v", err)
	}
	if err := state.RecordToolResult(ctx, db, "s1", seq2, "call_1", "out", nil); err != nil {
		t.Fatalf("record tool result: %v", err)
	}
	if err := state.RecordUsage(ctx, db, seq2, 10, 3, 0, 0); err != nil {
		t.Fatalf("record usage: %v", err)
	}
	if err := state.RecordFile(ctx, db, "s1", "/tmp/wt/a", "hash-a", time.Now().UnixNano()); err != nil {
		t.Fatalf("record file: %v", err)
	}
	if err := state.CloseSession(ctx, db, "s1", "ok"); err != nil {
		t.Fatalf("close session: %v", err)
	}

	m1 := mustRead(t, db, func(c context.Context) (any, error) {
		return domain.NewMessageDomain().GetMessage(c, seq1).Row()
	}).(*domain.Message)
	if m1.Content != "hello" || m1.Role != "user" || m1.SessionId != "s1" {
		t.Fatalf("message readback: %+v %v", m1, err)
	}
	if m1.Reasoning != nil {
		t.Errorf("null reasoning not preserved: %q", *m1.Reasoning)
	}
	tc := mustRead(t, db, func(c context.Context) (any, error) {
		return domain.NewToolCallDomain().GetToolCall(c, "s1", seq2, "call_1").Row()
	}).(*domain.ToolCall)
	if tc.Result == nil || *tc.Result != "out" || tc.Args != `{"cmd":"ls"}` {
		t.Fatalf("tool call readback: %+v %v", tc, err)
	}
	u := mustRead(t, db, func(c context.Context) (any, error) {
		return domain.NewUsageDomain().GetUsage(c, seq2).Row()
	}).(*domain.Usage)
	if u.Prompt != 10 || u.Completion != 3 {
		t.Fatalf("usage readback: %+v %v", u, err)
	}
}

func TestStateKillMidTurnLeavesCompletedRows(t *testing.T) {
	db := openStore(t)
	live := context.Background()
	if err := state.RecordSession(live, db, "s2", "/tmp/wt", "model-x", "0.1.0"); err != nil {
		t.Fatal(err)
	}
	seq1, err := state.RecordMessage(live, db, "s2", "user", "do it", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.RecordToolCall(live, db, "s2", seq1, "call_2", "bash", `{"cmd":"sleep 10"}`); err != nil {
		t.Fatal(err)
	}
	killed, cancel := context.WithCancel(live)
	cancel()
	if err := state.RecordToolResult(killed, db, "s2", seq1, "call_2", "never", nil); err == nil {
		t.Fatal("record under a cancelled context succeeded")
	}
	if err := state.CloseSession(killed, db, "s2", "cancelled"); err == nil {
		t.Fatal("close under a cancelled context succeeded")
	}

	m := mustRead(t, db, func(c context.Context) (any, error) {
		return domain.NewMessageDomain().GetMessage(c, seq1).Row()
	}).(*domain.Message)
	if m.Content != "do it" {
		t.Fatalf("completed message not readable after kill: %+v %v", m, err)
	}
	tc := mustRead(t, db, func(c context.Context) (any, error) {
		return domain.NewToolCallDomain().GetToolCall(c, "s2", seq1, "call_2").Row()
	}).(*domain.ToolCall)
	if tc.Result != nil {
		t.Fatalf("tool call with an unlanded result misreported: %+v %v", tc, err)
	}
	if err := state.CloseSession(live, db, "s2", "cancelled"); err != nil {
		t.Fatal(err)
	}
	s := mustRead(t, db, func(c context.Context) (any, error) {
		return domain.NewSessionDomain().GetSession(c, "s2").Row()
	}).(*domain.Session)
	if s.Exit != "cancelled" {
		t.Fatalf("session closure not readable: %+v %v", s, err)
	}
}

func TestStateFaultRows(t *testing.T) {
	db := openStore(t)
	ctx := context.Background()
	if err := state.RecordSession(ctx, db, "s3", "/tmp/wt", "model-x", "0.1.0"); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 8, 16, 1, 2, 3, 0, time.UTC)
	seq, err := state.RecordFault(ctx, db, "s3", at, "provider stream torn")
	if err != nil {
		t.Fatalf("record fault: %v", err)
	}
	if err := state.CloseSession(ctx, db, "s3", "fault"); err != nil {
		t.Fatal(err)
	}
	row := mustRead(t, db, func(c context.Context) (any, error) {
		return domain.NewFaultDomain().GetFault(c, seq).Row()
	}).(*domain.Fault)
	if row.Message != "provider stream torn" || row.SessionId != "s3" {
		t.Fatalf("fault readback: %+v %v", row, err)
	}
}

func TestRecordMessageStampsModel(t *testing.T) {
	db := openStore(t)
	ctx := context.Background()

	if err := state.RecordSession(ctx, db, "s1", "/tmp/wt", "model-x", "0.1.0"); err != nil {
		t.Fatalf("record session: %v", err)
	}
	served := "glm5.3-flash"
	seqA, err := state.RecordMessage(ctx, db, "s1", "assistant", "served reply", nil, nil, &served)
	if err != nil {
		t.Fatalf("record assistant: %v", err)
	}
	seqU, err := state.RecordMessage(ctx, db, "s1", "user", "asked", nil, nil, nil)
	if err != nil {
		t.Fatalf("record user: %v", err)
	}

	a := mustRead(t, db, func(c context.Context) (any, error) {
		return domain.NewMessageDomain().GetMessage(c, seqA).Row()
	}).(*domain.Message)
	if a.Model == nil || *a.Model != "glm5.3-flash" {
		t.Fatalf("assistant model not stamped: %+v", a.Model)
	}
	u := mustRead(t, db, func(c context.Context) (any, error) {
		return domain.NewMessageDomain().GetMessage(c, seqU).Row()
	}).(*domain.Message)
	if u.Model != nil {
		t.Fatalf("user row carries no model: %+v", u.Model)
	}
}

var v2Schema = []string{
	`CREATE TABLE IF NOT EXISTS "faults" (
  "seq" INTEGER NOT NULL,
  "at" TIMESTAMP NOT NULL,
  "message" TEXT NOT NULL,
  "session_id" TEXT NOT NULL,
  PRIMARY KEY ("seq")
)`,
	`CREATE TABLE IF NOT EXISTS "files" (
  "session_id" TEXT NOT NULL,
  "path" TEXT NOT NULL,
  "hash" TEXT NOT NULL,
  "mtime" INTEGER NOT NULL,
  PRIMARY KEY ("session_id", "path")
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
	`CREATE TABLE IF NOT EXISTS "sessions" (
  "id" TEXT NOT NULL,
  "cwd" TEXT NOT NULL,
  "ended_at" TIMESTAMP,
  "exit" TEXT NOT NULL,
  "model" TEXT NOT NULL,
  "started_at" TIMESTAMP NOT NULL,
  "version" TEXT NOT NULL,
  PRIMARY KEY ("id")
)`,
	`CREATE TABLE IF NOT EXISTS "tool_calls" (
  "session_id" TEXT NOT NULL,
  "message_seq" INTEGER NOT NULL,
  "id" TEXT NOT NULL,
  "args" TEXT NOT NULL,
  "ended_at" TIMESTAMP,
  "err" TEXT,
  "name" TEXT NOT NULL,
  "result" TEXT,
  "started_at" TIMESTAMP NOT NULL,
  PRIMARY KEY ("session_id", "message_seq", "id")
)`,
	`CREATE TABLE IF NOT EXISTS "usage" (
  "message_seq" INTEGER NOT NULL,
  "cache_read" INTEGER NOT NULL,
  "cache_write" INTEGER NOT NULL,
  "completion" INTEGER NOT NULL,
  "prompt" INTEGER NOT NULL,
  PRIMARY KEY ("message_seq")
)`,
}

func TestMigrationV2ToV3AddsMessagesModel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.sqlite")
	db, _, _, err := store.Open(path, v2Schema, 2, state.Migration())
	if err != nil {
		t.Fatalf("open v2: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO "messages" ("seq", "content", "created_at", "role", "session_id")
		VALUES (1, 'old row', CURRENT_TIMESTAMP, 'assistant', 's1')`); err != nil {
		t.Fatalf("insert v2 row: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close v2: %v", err)
	}

	db2, _, report, err := store.Open(path, state.Statements(), state.SchemaVersion, state.Migration())
	if err != nil {
		t.Fatalf("reopen v3: %v", err)
	}
	defer db2.Close()
	if !strings.Contains(report, "model") {
		t.Fatalf("migration report should name the messages model change: %q", report)
	}
	m := mustRead(t, db2, func(c context.Context) (any, error) {
		return domain.NewMessageDomain().GetMessage(c, 1).Row()
	}).(*domain.Message)
	if m.Model != nil {
		t.Fatalf("pre-migration row carries no model: %+v", m.Model)
	}
}
