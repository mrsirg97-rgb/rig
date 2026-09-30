package state_test

import (
	"context"
	"testing"

	"github.com/mrsirg97-rgb/rig/v2/store/state"
)

func TestSessionUsageNamesTheServingModel(t *testing.T) {
	db := openStore(t)
	ctx := context.Background()
	if err := state.RecordSession(ctx, db, "s1", "/w", "dsv4", "v"); err != nil {
		t.Fatal(err)
	}
	if _, err := state.RecordMessage(ctx, db, "s1", "user", "hi", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	seq, err := state.RecordMessage(ctx, db, "s1", "assistant", "done", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	served := "ox-alpha"
	if err := state.RecordUsage(ctx, db, seq, 100, 50, 10, 5, 0, &served); err != nil {
		t.Fatal(err)
	}
	rows, err := state.SessionUsage(ctx, db, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Model != "ox-alpha" {
		t.Fatalf("usage rows = %+v, want the serving id ox-alpha", rows)
	}
}

func TestSessionUsageReadsAModellessRowAsTheSessionModel(t *testing.T) {
	db := openStore(t)
	ctx := context.Background()
	if err := state.RecordSession(ctx, db, "s1", "/w", "dsv4", "v"); err != nil {
		t.Fatal(err)
	}
	if _, err := state.RecordMessage(ctx, db, "s1", "user", "hi", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	seq, err := state.RecordMessage(ctx, db, "s1", "assistant", "done", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.RecordUsage(ctx, db, seq, 100, 50, 10, 5, 0, nil); err != nil {
		t.Fatal(err)
	}
	rows, err := state.SessionUsage(ctx, db, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Model != "dsv4" {
		t.Fatalf("usage rows = %+v, want the modelless row read as the session's start model dsv4", rows)
	}
}

func TestUsageAddKeepsTheRowModel(t *testing.T) {
	db := openStore(t)
	ctx := context.Background()
	if err := state.RecordSession(ctx, db, "s1", "/w", "dsv4", "v"); err != nil {
		t.Fatal(err)
	}
	if _, err := state.RecordMessage(ctx, db, "s1", "user", "hi", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	seq, err := state.RecordMessage(ctx, db, "s1", "assistant", "done", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	first := "dsv4"
	resample := "ox-alpha"
	if err := state.RecordUsage(ctx, db, seq, 10, 2, 0, 0, 0, &first); err != nil {
		t.Fatal(err)
	}
	if err := state.AddUsage(ctx, db, seq, 5, 1, 0, 0, 0, &resample); err != nil {
		t.Fatal(err)
	}
	rows, err := state.SessionUsage(ctx, db, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("usage rows = %d, want one merged row", len(rows))
	}
	if rows[0].Prompt != 15 || rows[0].Completion != 3 {
		t.Fatalf("the add must fold its tokens in: %+v", rows[0])
	}
	if rows[0].Model != "dsv4" {
		t.Fatalf("the merged row must keep the call that created it: %+v", rows[0])
	}
}
