package todo_test

import (
	"context"
	"strings"
	"testing"
	"time"

	todostore "github.com/mrsirg97-rgb/rig/store/todo"
)

const sessC = "sess-c"

func TestReleaseReturnsAStaleForeignClaimToPendingAndNamesTheOwner(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, p, []item{{Text: "write the spec"}}, sessA)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := taskIDText(t, reply, "write the spec")
	if _, err := todostore.Start(ctx, db, p, id, sessA); err != nil {
		t.Fatalf("start: %v", err)
	}
	// The claim is old: the owner is long gone.
	old := time.Now().Add(-todostore.StaleClaimAfter - time.Hour).UTC().Format(time.RFC3339)
	rawExec(t, db, "UPDATE events SET ts = ? WHERE op = 'start' AND args = ?", old, `{"id":"`+id+`"}`)
	released, err := todostore.Release(ctx, db, p, id, sessB)
	if err != nil {
		t.Fatalf("release: %v", err)
	}
	if !strings.Contains(released, "'"+id+"' released") {
		t.Errorf("release reply must name the task: %s", released)
	}
	if !strings.Contains(released, sessA) {
		t.Errorf("release reply must name the dead owner: %s", released)
	}
	if got := projStatus(t, db, "write the spec"); got != "pending" {
		t.Errorf("status = %v, want pending", got)
	}
	rows := rawQuery(t, db, "SELECT op FROM events ORDER BY seq DESC LIMIT 1")
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("no event")
	}
	var op string
	if err := rows.Scan(&op); err != nil {
		t.Fatal(err)
	}
	if op != "release" {
		t.Errorf("last op = %s, want release", op)
	}
}

func TestReleaseRefusesOwnUnclaimedFreshAndFinished(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, p, []item{{Text: "own"}, {Text: "pending"}, {Text: "done"}, {Text: "fresh"}}, sessA)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	own := taskIDText(t, reply, "own")
	pending := taskIDText(t, reply, "pending")
	done := taskIDText(t, reply, "done")
	fresh := taskIDText(t, reply, "fresh")
	if _, err := todostore.Start(ctx, db, p, own, sessA); err != nil {
		t.Fatalf("start own: %v", err)
	}
	if _, err := todostore.Complete(ctx, db, p, done, sessA); err != nil {
		t.Fatalf("complete done: %v", err)
	}
	if _, err := todostore.Start(ctx, db, p, fresh, sessA); err != nil {
		t.Fatalf("start fresh: %v", err)
	}
	if _, err := todostore.Release(ctx, db, p, own, sessA); err == nil {
		t.Error("releasing your own claim must refuse")
	} else if !strings.Contains(err.Error(), "claimed by you") {
		t.Errorf("own-claim refusal voice: %v", err)
	}
	if _, err := todostore.Release(ctx, db, p, pending, sessB); err == nil {
		t.Error("releasing an unclaimed task must refuse")
	} else if !strings.Contains(err.Error(), "not claimed") {
		t.Errorf("unclaimed refusal voice: %v", err)
	}
	if _, err := todostore.Release(ctx, db, p, done, sessB); err == nil {
		t.Error("releasing a done task must refuse")
	} else if !strings.Contains(err.Error(), "done") {
		t.Errorf("done refusal voice: %v", err)
	}
	if _, err := todostore.Release(ctx, db, p, fresh, sessB); err == nil {
		t.Error("releasing a live session's fresh claim must refuse")
	} else if !strings.Contains(err.Error(), sessA) {
		t.Errorf("fresh-claim refusal must name the owner: %v", err)
	}
	// create(1) + start own(2) + auto-start+complete done(3,4) + start fresh(5).
	if got := eventCount(t, db); got != 5 {
		t.Errorf("refused releases must append nothing: %d events", got)
	}
}

func TestReapReleasesEndedAndStaleClaims(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, p,
		[]item{{Text: "dead"}, {Text: "stale"}, {Text: "fresh"}, {Text: "mine"}}, sessA)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	dead := taskIDText(t, reply, "dead")
	stale := taskIDText(t, reply, "stale")
	fresh := taskIDText(t, reply, "fresh")
	mine := taskIDText(t, reply, "mine")

	// dead: claimed by sessA, whose session row has ended.
	if _, err := todostore.Start(ctx, db, p, dead, sessA); err != nil {
		t.Fatalf("start dead: %v", err)
	}
	// stale: claimed by sessB a long time ago (the claim event is old).
	if _, err := todostore.Start(ctx, db, p, stale, sessB); err != nil {
		t.Fatalf("start stale: %v", err)
	}
	old := time.Now().Add(-todostore.StaleClaimAfter - time.Hour).UTC().Format(time.RFC3339)
	rawExec(t, db, "UPDATE events SET ts = ? WHERE op = 'start' AND args = ? AND session = ?", old, `{"id":"`+stale+`"}`, sessB)
	// fresh: claimed by sessC recently.
	if _, err := todostore.Start(ctx, db, p, fresh, sessC); err != nil {
		t.Fatalf("start fresh: %v", err)
	}
	// mine: claimed by the reaping session itself.
	if _, err := todostore.Start(ctx, db, p, mine, sessC); err != nil {
		t.Fatalf("start mine: %v", err)
	}

	reaped, err := todostore.Reap(ctx, db, p, []string{sessA}, sessC)
	if err != nil {
		t.Fatalf("reap: %v", err)
	}
	if !strings.Contains(reaped, dead) {
		t.Errorf("reap reply must name the released task %s: %s", dead, reaped)
	}
	if !strings.Contains(reaped, sessA) {
		t.Errorf("reap reply must name the dead owner %s: %s", sessA, reaped)
	}
	if !strings.Contains(reaped, stale) {
		t.Errorf("reap reply must name the stale task %s: %s", stale, reaped)
	}
	if got := projStatus(t, db, "dead"); got != "pending" {
		t.Errorf("dead = %v, want pending", got)
	}
	if got := projStatus(t, db, "stale"); got != "pending" {
		t.Errorf("stale = %v, want pending", got)
	}
	if got := projStatus(t, db, "fresh"); got != "in_progress" {
		t.Errorf("fresh = %v, want in_progress (never reaped)", got)
	}
	if got := projStatus(t, db, "mine"); got != "in_progress" {
		t.Errorf("mine = %v, want in_progress (the reaper never takes its own claims)", got)
	}
}

func TestReapIsIdleWhenNothingIsStale(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, p, []item{{Text: "fresh"}}, sessA)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := taskIDText(t, reply, "fresh")
	if _, err := todostore.Start(ctx, db, p, id, sessA); err != nil {
		t.Fatalf("start: %v", err)
	}
	reaped, err := todostore.Reap(ctx, db, p, nil, sessB)
	if err != nil {
		t.Fatalf("reap: %v", err)
	}
	if strings.Contains(reaped, "released") {
		t.Errorf("a fresh claim must not be reaped: %s", reaped)
	}
	if got := eventCount(t, db); got != 2 {
		t.Errorf("an idle reap must append nothing: %d events", got)
	}
}

func TestReapReleasesNothingWhenDeadOwnerOwnsNothing(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reaped, err := todostore.Reap(ctx, db, p, []string{sessA}, sessB)
	if err != nil {
		t.Fatalf("reap: %v", err)
	}
	if strings.Contains(reaped, "released") {
		t.Errorf("no tasks must reap nothing: %s", reaped)
	}
}

func TestStaleClaimSurvivesCompaction(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, p, []item{{Text: "old claim"}}, sessA)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := taskIDText(t, reply, "old claim")
	if _, err := todostore.Start(ctx, db, p, id, sessA); err != nil {
		t.Fatalf("start: %v", err)
	}
	// The claim is old; then a compaction folds the log and must carry
	// the claim's age with it (the snapshot carries updatedTs).
	old := time.Now().Add(-todostore.StaleClaimAfter - time.Hour).UTC().Format(time.RFC3339)
	rawExec(t, db, "UPDATE events SET ts = ? WHERE op = 'start' AND args = ?", old, `{"id":"`+id+`"}`)
	age(t, db, 1010)
	if _, err := todostore.Move(ctx, db, p, id, 1, sessB); err != nil {
		t.Fatalf("move (compaction trigger): %v", err)
	}
	reaped, err := todostore.Reap(ctx, db, p, nil, sessC)
	if err != nil {
		t.Fatalf("reap: %v", err)
	}
	if !strings.Contains(reaped, id) {
		t.Errorf("a stale claim must still reap after a compaction: %s", reaped)
	}
}
