package state_test

import (
	"context"
	"testing"

	"github.com/mrsirg97-rgb/rig/store/state"
)

// TestSessionUsageReturnsRows (SPEC_SERVE, named): the typed usage read
// beside Resume returns the session's usage rows, in transcript order; a
// session with no usage rows returns an empty slice, not an error.
func TestSessionUsageReturnsRows(t *testing.T) {
	db := openStore(t)
	ctx := context.Background()
	if err := state.RecordSession(ctx, db, "s1", "/w", "m", "v"); err != nil {
		t.Fatal(err)
	}
	if _, err := state.RecordMessage(ctx, db, "s1", "user", "hi", nil, nil); err != nil {
		t.Fatal(err)
	}
	seq, err := state.RecordMessage(ctx, db, "s1", "assistant", "done", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.RecordUsage(ctx, db, seq, 111, 222, 333, 444); err != nil {
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

// TestListSessionsCarriesCwd (SPEC_SERVE, named): SessionRow.Cwd is
// populated so the dashboard groups by workspace and the cwd picker can
// enumerate the workspaces that have sessions.
func TestListSessionsCarriesCwd(t *testing.T) {
	db := openStore(t)
	ctx := context.Background()
	if err := state.RecordSession(ctx, db, "s1", "/workspace/alpha", "m", "v"); err != nil {
		t.Fatal(err)
	}
	rows, err := state.ListSessions(ctx, db)
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
