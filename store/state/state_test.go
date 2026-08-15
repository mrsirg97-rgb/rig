package state_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/looper/store"
	"github.com/mrsirg97-rgb/looper/store/state"
	"github.com/mrsirg97-rgb/looper/store/state/domain"
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
	db, _, err := store.Open(filepath.Join(t.TempDir(), "sessions.sqlite"), state.Statements(), 1)
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
	seq1, err := state.RecordMessage(ctx, db, "s1", "user", "hello", nil, nil)
	if err != nil {
		t.Fatalf("record message: %v", err)
	}
	reasoning := "because"
	seq2, err := state.RecordMessage(ctx, db, "s1", "assistant", "", &reasoning, nil)
	if err != nil {
		t.Fatalf("record reasoning message: %v", err)
	}
	if seq2 <= seq1 {
		t.Fatalf("seq minting not strictly increasing: %d then %d", seq1, seq2)
	}
	if err := state.RecordToolCall(ctx, db, seq2, "call_1", "bash", `{"cmd":"ls"}`); err != nil {
		t.Fatalf("record tool call: %v", err)
	}
	if err := state.RecordToolResult(ctx, db, "call_1", "out", nil); err != nil {
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
		return domain.NewToolCallDomain().GetToolCall(c, "call_1").Row()
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
	seq1, err := state.RecordMessage(live, db, "s2", "user", "do it", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.RecordToolCall(live, db, seq1, "call_2", "bash", `{"cmd":"sleep 10"}`); err != nil {
		t.Fatal(err)
	}
	killed, cancel := context.WithCancel(live)
	cancel() // the kill lands here: result and message two never land
	if err := state.RecordToolResult(killed, db, "call_2", "never", nil); err == nil {
		t.Fatal("record under a cancelled context succeeded")
	}
	if err := state.CloseSession(killed, db, "s2", "cancelled"); err == nil {
		t.Fatal("close under a cancelled context succeeded")
	}
	// every row that completed before the kill stays readable
	m := mustRead(t, db, func(c context.Context) (any, error) {
		return domain.NewMessageDomain().GetMessage(c, seq1).Row()
	}).(*domain.Message)
	if m.Content != "do it" {
		t.Fatalf("completed message not readable after kill: %+v %v", m, err)
	}
	tc := mustRead(t, db, func(c context.Context) (any, error) {
		return domain.NewToolCallDomain().GetToolCall(c, "call_2").Row()
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
