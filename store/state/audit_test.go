package state_test

import (
	"context"
	"testing"

	"github.com/mrsirg97-rgb/rig/v3/policy/compact"
	"github.com/mrsirg97-rgb/rig/v3/store/state"
)

func TestResumeTruncatesAtALiteralMarkerPrompt(t *testing.T) {
	db := openStore(t)
	ctx := context.Background()
	sid := "resume-literal-marker"
	if err := state.RecordSession(ctx, db, sid, "/w", "m", "v"); err != nil {
		t.Fatal(err)
	}
	for _, m := range []struct{ role, text string }{
		{"user", "hello"},
		{"assistant", "hi"},
		{"user", compact.SummaryMarker + "typed by the operator"},
		{"assistant", "after"},
	} {
		if _, err := state.RecordMessage(ctx, db, sid, m.role, m.text, nil, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	sess, err := state.Resume(ctx, db, sid)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if len(sess.Messages) != 2 {
		t.Fatalf("a user row with the marker prefix is the boundary: %d messages, want 2\n%+v", len(sess.Messages), sess.Messages)
	}
	if sess.Messages[0].Content != compact.SummaryMarker+"typed by the operator" || sess.Messages[1].Content != "after" {
		t.Fatalf("the boundary prompt must open the transcript:\n%+v", sess.Messages)
	}
}

func TestListSessionsRefusesALimitOutsideTheCap(t *testing.T) {
	db := openStore(t)
	ctx := context.Background()
	if err := state.RecordSession(ctx, db, "s1", "/w", "m", "v"); err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{0, -1, state.ListCap + 1} {
		if _, err := state.ListSessions(ctx, db, n); err == nil {
			t.Fatalf("limit %d must refuse", n)
		}
	}
	if _, err := state.ListSessions(ctx, db, 1); err != nil {
		t.Fatalf("1 must list: %v", err)
	}
	if _, err := state.ListSessions(ctx, db, state.ListCap); err != nil {
		t.Fatalf("the cap must list: %v", err)
	}
}

func TestListSessionsTieBreaksEqualStartsOnTheId(t *testing.T) {
	db := openStore(t)
	ctx := context.Background()
	for _, id := range []string{"s-aaa", "s-bbb", "s-ccc"} {
		if err := state.RecordSession(ctx, db, id, "/w", "m", "v"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.DB.Exec(`UPDATE sessions SET started_at = (SELECT started_at FROM sessions WHERE id = 's-aaa')`); err != nil {
		t.Fatal(err)
	}
	first, err := state.ListSessions(ctx, db, state.ListCap)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(first) != 3 {
		t.Fatalf("rows: %d, want 3", len(first))
	}
	if first[0].ID != "s-ccc" || first[1].ID != "s-bbb" || first[2].ID != "s-aaa" {
		t.Fatalf("equal starts must tie-break on the id, descending: %v %v %v", first[0].ID, first[1].ID, first[2].ID)
	}
	second, err := state.ListSessions(ctx, db, state.ListCap)
	if err != nil {
		t.Fatalf("list again: %v", err)
	}
	if second[0].ID != first[0].ID || second[1].ID != first[1].ID || second[2].ID != first[2].ID {
		t.Fatalf("the order must be stable across calls: %v then %v", first, second)
	}
}
