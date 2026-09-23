package todo_test

import (
	"context"
	"strings"
	"testing"

	todostore "github.com/mrsirg97-rgb/rig/store/todo"
)

func TestReleaseOfAReviewClaimSurvivesReplay(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, p, []item{{Text: "in review"}}, sessA)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := taskIDText(t, reply, "in review")
	if _, err := todostore.Claim(ctx, db, p, sessA, ""); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := todostore.Complete(ctx, db, p, id, sessA, true); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if _, err := todostore.Claim(ctx, db, p, sessB, "review"); err != nil {
		t.Fatalf("claim review: %v", err)
	}
	if _, err := todostore.Reap(ctx, db, p, []string{sessB}, sessA); err != nil {
		t.Fatalf("reap: %v", err)
	}
	rawExec(t, db, "DELETE FROM tasks WHERE scope = 'ws'")
	rawExec(t, db, "DELETE FROM task_deps WHERE scope = 'ws'")
	peek, err := todostore.Read(ctx, db, p, sessA)
	if err != nil {
		t.Fatalf("read after tamper: %v", err)
	}
	if got := projStatus(t, db, "in review"); got != "review" {
		t.Errorf("a released review claim must keep the task in review: %v", got)
	}
	if strings.Contains(peek, "claimed for review by "+sessB) {
		t.Errorf("the released review holder must not survive replay:\n%s", peek)
	}
	claimed, err := todostore.Claim(ctx, db, p, sessC, "review")
	if err != nil {
		t.Fatalf("claim review again: %v", err)
	}
	if !strings.Contains(claimed, "'"+id+"' claimed for review") {
		t.Errorf("the freed review task must be claimable again: %s", claimed)
	}
}

func TestFailOfAReviewClaimSurvivesReplay(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, p, []item{{Text: "abandoned review"}}, sessA)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := taskIDText(t, reply, "abandoned review")
	if _, err := todostore.Claim(ctx, db, p, sessA, ""); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := todostore.Complete(ctx, db, p, id, sessA, true); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if _, err := todostore.Claim(ctx, db, p, sessB, "review"); err != nil {
		t.Fatalf("claim review: %v", err)
	}
	if _, err := todostore.Fail(ctx, db, p, id, sessB, false); err != nil {
		t.Fatalf("fail own review claim: %v", err)
	}
	rawExec(t, db, "DELETE FROM tasks WHERE scope = 'ws'")
	rawExec(t, db, "DELETE FROM task_deps WHERE scope = 'ws'")
	peek, err := todostore.Read(ctx, db, p, sessA)
	if err != nil {
		t.Fatalf("read after tamper: %v", err)
	}
	if got := projStatus(t, db, "abandoned review"); got != "failed" {
		t.Errorf("a failed review claim must replay as failed: %v", got)
	}
	if strings.Contains(peek, "claimed for review by "+sessB) {
		t.Errorf("the failed review holder must not survive replay:\n%s", peek)
	}
}

func TestWorkerModeFailOnAReviewClaimRefuses(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, p, []item{{Text: "review in flight"}}, sessA)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := taskIDText(t, reply, "review in flight")
	if _, err := todostore.Claim(ctx, db, p, sessA, ""); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := todostore.Complete(ctx, db, p, id, sessA, true); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if _, err := todostore.Fail(ctx, db, p, id, sessB, true); err == nil {
		t.Fatal("a worker must not fail a review task it does not hold")
	} else if !strings.Contains(err.Error(), "in review") {
		t.Errorf("worker fail on an unowned review voice = %q", err)
	}
}
