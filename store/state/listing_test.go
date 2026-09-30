package state_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/v2/store/state"
)

func TestListSessionsSeeksNotScans(t *testing.T) {
	db := openStore(t)
	ctx := context.Background()
	for _, want := range []string{"messages_session_role_seq", "faults_session"} {
		var name string
		if err := db.DB.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'index' AND name = ?`, want).Scan(&name); err != nil {
			t.Fatalf("index %s missing from a fresh store: %v", want, err)
		}
	}
	const sessions, perSession = 200, 100
	for i := 0; i < sessions; i++ {
		sid := fmt.Sprintf("s%03d", i)
		if err := state.RecordSession(ctx, db, sid, "/w", "m", "v"); err != nil {
			t.Fatal(err)
		}
		for j := 0; j < perSession; j++ {
			role := "assistant"
			if j%2 == 0 {
				role = "user"
			}
			seq, err := state.RecordMessage(ctx, db, sid, role, fmt.Sprintf("m%d", j), nil, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			if role == "assistant" {
				if err := state.RecordUsage(ctx, db, seq, 100, 10, 90, 0, 0, nil); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	t0 := time.Now()
	rows, err := state.ListSessions(ctx, db, state.ListCap)
	if err != nil {
		t.Fatal(err)
	}
	took := time.Since(t0)
	if len(rows) != state.ListCap {
		t.Fatalf("rows: %d, want %d", len(rows), state.ListCap)
	}
	if rows[0].Turns != perSession/2 {
		t.Fatalf("turns: %d, want %d", rows[0].Turns, perSession/2)
	}
	if rows[0].Tokens != int64(perSession/2*110) {
		t.Fatalf("tokens: %d, want %d", rows[0].Tokens, perSession/2*110)
	}
	if took > 2*time.Second {
		t.Fatalf("ListSessions took %s over %d sessions / %d messages", took, sessions, sessions*perSession)
	}
	plan, err := db.DB.Query(`EXPLAIN QUERY PLAN SELECT (SELECT count(*) FROM "messages" m WHERE m."session_id" = 's001' AND m."role" = 'user')`)
	if err != nil {
		t.Fatal(err)
	}
	defer plan.Close()
	for plan.Next() {
		var a, b, c int
		var detail string
		if err := plan.Scan(&a, &b, &c, &detail); err != nil {
			t.Fatal(err)
		}
		if len(detail) >= 6 && detail[:6] == "SCAN m" {
			t.Fatalf("the per-session count scans messages: %s", detail)
		}
	}
	cwds, err := state.Cwds(ctx, db)
	if err != nil || len(cwds) != 1 || cwds[0] != "/w" {
		t.Fatalf("Cwds = %v, %v; want [/w]", cwds, err)
	}
}
