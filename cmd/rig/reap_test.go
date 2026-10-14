package main

import (
	"context"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/v3/store"
	"github.com/mrsirg97-rgb/rig/v3/store/state"
	todostore "github.com/mrsirg97-rgb/rig/v3/store/todo"
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
	if err := state.RecordSession(ctx, sdb, deadID, cwd, "local", "0.25.0"); err != nil {
		t.Fatalf("record dead session: %v", err)
	}
	if err := state.CloseSession(ctx, sdb, deadID, "ok"); err != nil {
		t.Fatalf("close dead session: %v", err)
	}
	liveID := "live-session-5678"
	if err := state.RecordSession(ctx, sdb, liveID, cwd, "local", "0.25.0"); err != nil {
		t.Fatalf("record live session: %v", err)
	}

	proj := todostore.Project{Key: "reaptest", Label: "reaptest"}
	if _, err := todostore.Create(ctx, tdb, proj, todostore.CreateItem{Text: "dead claim"}, deadID); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := todostore.Create(ctx, tdb, proj, todostore.CreateItem{Text: "live claim"}, deadID); err != nil {
		t.Fatalf("create: %v", err)
	}
	reply, err := todostore.Read(ctx, tdb, proj, "current-session")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	dead := taskIDText(t, reply, "dead claim")
	live := taskIDText(t, reply, "live claim")
	if _, err := todostore.Start(ctx, tdb, proj, dead, deadID, false); err != nil {
		t.Fatalf("start dead: %v", err)
	}
	if _, err := todostore.Start(ctx, tdb, proj, live, liveID, false); err != nil {
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

func TestTheReapQueueTakesTheSessionRecordedBinding(t *testing.T) {
	sdb, _ := reapStores(t)
	ctx := context.Background()
	launch := t.TempDir()
	bound := t.TempDir()
	id := "bound-session-9012"
	if err := state.RecordSession(ctx, sdb, id, bound, "local", "0.25.0"); err != nil {
		t.Fatalf("record bound session: %v", err)
	}
	proj, err := sessionQueue(ctx, sdb, launch, id)
	if err != nil {
		t.Fatalf("sessionQueue: %v", err)
	}
	if proj.Key != todostore.ProjectOf(bound).Key {
		t.Fatalf("a recorded binding names the queue the session actually worked: got %q, want %q", proj.Key, todostore.ProjectOf(bound).Key)
	}
}

func TestTheReapQueueFallsBackToTheLaunchDirectoryWithoutABinding(t *testing.T) {
	sdb, _ := reapStores(t)
	ctx := context.Background()
	launch := t.TempDir()
	proj, err := sessionQueue(ctx, sdb, launch, "no-such-session-404")
	if err != nil {
		t.Fatalf("an unknown session is no binding, not an error: %v", err)
	}
	if proj.Key != todostore.ProjectOf(launch).Key {
		t.Fatalf("without a recorded binding the launch directory names the queue: got %q, want %q", proj.Key, todostore.ProjectOf(launch).Key)
	}
}

func TestAReapQueueReadFailureFallsBackLoud(t *testing.T) {
	sdb, _ := reapStores(t)
	launch := t.TempDir()
	if err := sdb.Close(); err != nil {
		t.Fatal(err)
	}
	proj, err := sessionQueue(context.Background(), sdb, launch, "bound-session-9012")
	if err == nil {
		t.Fatal("a binding read that fails must say so")
	}
	if proj.Key != todostore.ProjectOf(launch).Key {
		t.Fatalf("the failed read still falls back to the launch directory: got %q", proj.Key)
	}
}

func TestReapAtOpenIsIdleWhenNoSessionsHaveEnded(t *testing.T) {
	sdb, tdb := reapStores(t)
	ctx := context.Background()
	cwd := t.TempDir()
	liveID := "live-session-5678"
	if err := state.RecordSession(ctx, sdb, liveID, cwd, "local", "0.25.0"); err != nil {
		t.Fatalf("record live session: %v", err)
	}
	proj := todostore.Project{Key: "reaptest", Label: "reaptest"}
	reply, err := todostore.Create(ctx, tdb, proj, todostore.CreateItem{Text: "live claim"}, liveID)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := taskIDText(t, reply, "live claim")
	if _, err := todostore.Start(ctx, tdb, proj, id, liveID, false); err != nil {
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
