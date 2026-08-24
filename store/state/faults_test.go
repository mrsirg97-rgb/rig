package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/store/state"
)

func TestSessionFaultsNewestFirst(t *testing.T) {
	db := openStore(t)
	ctx := context.Background()
	if err := state.RecordSession(ctx, db, "s1", "/w", "m", "v"); err != nil {
		t.Fatal(err)
	}
	base := time.Now().Add(-time.Hour)
	if _, e := state.RecordFault(ctx, db, "s1", base, "first fault"); e != nil {
		t.Fatal(e)
	}
	if _, e := state.RecordFault(ctx, db, "s1", base.Add(time.Minute), "second fault"); e != nil {
		t.Fatal(e)
	}
	if _, e := state.RecordFault(ctx, db, "s1", base.Add(2*time.Minute), "third fault\nwith a second line"); e != nil {
		t.Fatal(e)
	}
	if err := state.RecordSession(ctx, db, "s2", "/w", "m", "v"); err != nil {
		t.Fatal(err)
	}
	if _, e := state.RecordFault(ctx, db, "s2", base.Add(3*time.Minute), "other session fault"); e != nil {
		t.Fatal(e)
	}

	rows, err := state.SessionFaults(ctx, db, "s1")
	if err != nil {
		t.Fatalf("SessionFaults: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("want 3 faults, got %d", len(rows))
	}
	if rows[0].Message != "third fault\nwith a second line" {
		t.Fatalf("newest first, got %q", rows[0].Message)
	}
	if rows[1].Message != "second fault" || rows[2].Message != "first fault" {
		t.Fatalf("desc order broken: %q %q", rows[1].Message, rows[2].Message)
	}
	if rows[0].SessionID != "s1" {
		t.Fatalf("the row must carry its session, got %q", rows[0].SessionID)
	}
	if rows[0].At.Before(base) {
		t.Fatalf("the row must carry its time")
	}
}

func TestSessionFaultsUnknownSessionIsEmpty(t *testing.T) {
	db := openStore(t)
	rows, err := state.SessionFaults(context.Background(), db, "nope")
	if err != nil {
		t.Fatalf("SessionFaults: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("an unknown session has no faults, got %d", len(rows))
	}
}
