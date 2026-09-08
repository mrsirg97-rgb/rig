package main

import (
	"context"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/store"
	"github.com/mrsirg97-rgb/rig/store/state"
	todostore "github.com/mrsirg97-rgb/rig/store/todo"
)

func taskIDText(t *testing.T, reply, text string) string {
	t.Helper()
	re := regexp.MustCompile(`\bt(\d+)\b \[[~x! ]\] ` + regexp.QuoteMeta(text))
	if mm := re.FindStringSubmatch(reply); mm != nil {
		return "t" + mm[1]
	}
	t.Fatalf("no task %q in:\n%s", text, reply)
	return ""
}

func reapStores(t *testing.T) (sdb, tdb store.DB) {
	t.Helper()
	dir := t.TempDir()
	sdb, _, _, err := store.Open(filepath.Join(dir, "sessions.sqlite"), state.Statements(), state.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	tdb, _, _, err = store.Open(filepath.Join(dir, "todo.sqlite"), todostore.Statements(), todostore.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	return sdb, tdb
}

func TestReapAtOpenReleasesClaimsOwnedByEndedSessions(t *testing.T) {
	sdb, tdb := reapStores(t)
	ctx := context.Background()
	cwd := t.TempDir()

	deadID := "dead-session-1234"
	if err := state.RecordSession(ctx, sdb, deadID, cwd, "local", "0.24.6"); err != nil {
		t.Fatalf("record dead session: %v", err)
	}
	if err := state.CloseSession(ctx, sdb, deadID, "ok"); err != nil {
		t.Fatalf("close dead session: %v", err)
	}
	liveID := "live-session-5678"
	if err := state.RecordSession(ctx, sdb, liveID, cwd, "local", "0.24.6"); err != nil {
		t.Fatalf("record live session: %v", err)
	}

	proj := todostore.Project{Key: "reaptest", Label: "reaptest"}
	reply, err := todostore.Create(ctx, tdb, proj, []todostore.CreateItem{{Text: "dead claim"}, {Text: "live claim"}}, deadID)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	dead := taskIDText(t, reply, "dead claim")
	live := taskIDText(t, reply, "live claim")
	if _, err := todostore.Start(ctx, tdb, proj, dead, deadID); err != nil {
		t.Fatalf("start dead: %v", err)
	}
	if _, err := todostore.Start(ctx, tdb, proj, live, liveID); err != nil {
		t.Fatalf("start live: %v", err)
	}

	note, err := reapClaims(ctx, sdb, tdb, cwd, proj, "current-session")
	if err != nil {
		t.Fatalf("reap: %v", err)
	}
	if !strings.Contains(note, dead) {
		t.Errorf("reap note must name the released task %s: %s", dead, note)
	}
	if !strings.Contains(note, deadID) {
		t.Errorf("reap note must name the dead owner %s: %s", deadID, note)
	}
	if strings.Contains(note, live) {
		t.Errorf("a live session's claim must not be in the reap note: %s", note)
	}
}

func TestReapAtOpenIsIdleWhenNoSessionsHaveEnded(t *testing.T) {
	sdb, tdb := reapStores(t)
	ctx := context.Background()
	cwd := t.TempDir()
	liveID := "live-session-5678"
	if err := state.RecordSession(ctx, sdb, liveID, cwd, "local", "0.24.6"); err != nil {
		t.Fatalf("record live session: %v", err)
	}
	proj := todostore.Project{Key: "reaptest", Label: "reaptest"}
	reply, err := todostore.Create(ctx, tdb, proj, []todostore.CreateItem{{Text: "live claim"}}, liveID)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := taskIDText(t, reply, "live claim")
	if _, err := todostore.Start(ctx, tdb, proj, id, liveID); err != nil {
		t.Fatalf("start: %v", err)
	}
	note, err := reapClaims(ctx, sdb, tdb, cwd, proj, "current-session")
	if err != nil {
		t.Fatalf("reap: %v", err)
	}
	if note != "" {
		t.Errorf("an all-live store must reap nothing: %s", note)
	}
}
