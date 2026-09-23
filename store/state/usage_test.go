package state_test

import (
	"context"
	"testing"

	"github.com/mrsirg97-rgb/rig/store/state"
)

func TestSessionUsageReturnsRows(t *testing.T) {
	db := openStore(t)
	ctx := context.Background()
	if err := state.RecordSession(ctx, db, "s1", "/w", "m", "v"); err != nil {
		t.Fatal(err)
	}
	if _, err := state.RecordMessage(ctx, db, "s1", "user", "hi", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	seq, err := state.RecordMessage(ctx, db, "s1", "assistant", "done", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.RecordUsage(ctx, db, seq, 111, 222, 333, 444, 0); err != nil {
		t.Fatal(err)
	}
	rows, err := state.SessionUsage(ctx, db, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("usage rows: %d, want 1", len(rows))
	}
	r := rows[0]
	if r.Seq != seq || r.Prompt != 111 || r.Completion != 222 || r.CacheRead != 333 || r.CacheWrite != 444 {
		t.Fatalf("usage row %+v, want seq %d prompt 111 completion 222 cache_read 333 cache_write 444", r, seq)
	}
	if err := state.RecordSession(ctx, db, "s2", "/w", "m", "v"); err != nil {
		t.Fatal(err)
	}
	if rows, err = state.SessionUsage(ctx, db, "s2"); err != nil || len(rows) != 0 {
		t.Fatalf("empty session usage: rows %d err %v, want 0 and nil", len(rows), err)
	}
}

func TestListSessionsCarriesCwd(t *testing.T) {
	db := openStore(t)
	ctx := context.Background()
	if err := state.RecordSession(ctx, db, "s1", "/workspace/alpha", "m", "v"); err != nil {
		t.Fatal(err)
	}
	rows, err := state.ListSessions(ctx, db, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("sessions: %d, want 1", len(rows))
	}
	if rows[0].Cwd != "/workspace/alpha" {
		t.Fatalf("cwd %q, want /workspace/alpha", rows[0].Cwd)
	}
}

func TestCostRecordedAndSummed(t *testing.T) {
	db := openStore(t)
	ctx := context.Background()
	if err := state.RecordSession(ctx, db, "s1", "/w", "m", "v"); err != nil {
		t.Fatal(err)
	}
	if _, err := state.RecordMessage(ctx, db, "s1", "user", "hi", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	seq, err := state.RecordMessage(ctx, db, "s1", "assistant", "done", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.RecordUsage(ctx, db, seq, 100, 50, 10, 5, 0.25); err != nil {
		t.Fatal(err)
	}
	if err := state.AddUsage(ctx, db, seq, 0, 0, 0, 0, 0.10); err != nil {
		t.Fatal(err)
	}
	rows, err := state.SessionUsage(ctx, db, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("usage rows: %d, want 1", len(rows))
	}
	if rows[0].Cost != 0.35 {
		t.Fatalf("usage cost = %v, want 0.35 (0.25 recorded + 0.10 added)", rows[0].Cost)
	}
	cost, err := state.SessionCost(ctx, db, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if cost != 0.35 {
		t.Fatalf("session cost = %v, want 0.35", cost)
	}
	if err := state.RecordSession(ctx, db, "s2", "/w", "m", "v"); err != nil {
		t.Fatal(err)
	}
	if cost, err = state.SessionCost(ctx, db, "s2"); err != nil || cost != 0 {
		t.Fatalf("empty session cost = %v, err %v, want 0 and nil", cost, err)
	}
}
