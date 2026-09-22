package todo_test

import (
	"context"
	"database/sql"
	"strings"
	"sync"
	"testing"
	"time"

	todostore "github.com/mrsirg97-rgb/rig/store/todo"
)

func TestClaimTakesTheFirstUnblockedPendingTask(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, p, []item{
		{Text: "gate"}, {Text: "work", DependsOn: ptrTo("gate")}, {Text: "later"},
	}, sessA)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	gate := taskIDText(t, reply, "gate")
	claimed, err := todostore.Claim(ctx, db, p, sessB, "")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if !strings.Contains(claimed, "'"+gate+"' claimed") {
		t.Fatalf("claim reply must name the taken task: %s", claimed)
	}
	if got := projStatus(t, db, "gate"); got != "in_progress" {
		t.Errorf("status = %v, want in_progress", got)
	}
	again, err := todostore.Claim(ctx, db, p, sessB, "")
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	if !strings.Contains(again, "'t3' claimed") {
		t.Fatalf("a blocked task must not be claimed before the unblocked one: %s", again)
	}
	if got := projStatus(t, db, "work"); got != "pending" {
		t.Errorf("the blocked task must stay pending: %v", got)
	}
}

func TestClaimRepliesNothingToDoWhenAllBlocked(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, p, []item{
		{Text: "gate"}, {Text: "work", DependsOn: ptrTo("gate")},
	}, sessA)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	gate := taskIDText(t, reply, "gate")
	if _, err := todostore.Start(ctx, db, p, gate, sessA); err != nil {
		t.Fatalf("start: %v", err)
	}
	claimed, err := todostore.Claim(ctx, db, p, sessB, "")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if claimed != "nothing to do" {
		t.Errorf("all-blocked claim = %q, want \"nothing to do\"", claimed)
	}
}

func TestClaimOnAnEmptyQueueRepliesNothingToDo(t *testing.T) {
	db := newDB(t)
	claimed, err := todostore.Claim(context.Background(), db, p, sessA, "")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if claimed != "nothing to do" {
		t.Errorf("empty claim = %q, want \"nothing to do\"", claimed)
	}
}

func TestClaimRepliesNothingToDoWhenOnlyFailedTasks(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, p, []item{{Text: "broken"}}, sessA)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := taskIDText(t, reply, "broken")
	if _, err := todostore.Start(ctx, db, p, id, sessA); err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := todostore.Fail(ctx, db, p, id, sessA); err != nil {
		t.Fatalf("fail: %v", err)
	}
	claimed, err := todostore.Claim(ctx, db, p, sessB, "")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if claimed != "nothing to do" {
		t.Errorf("failed-only claim = %q, want \"nothing to do\"", claimed)
	}
}

func TestClaimAppendsOneClaimEventForTheSession(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, p, []item{{Text: "taken"}}, sessA)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := taskIDText(t, reply, "taken")
	if _, err := todostore.Claim(ctx, db, p, sessB, ""); err != nil {
		t.Fatalf("claim: %v", err)
	}
	rows := rawQuery(t, db, "SELECT op, args, session FROM events ORDER BY seq DESC LIMIT 1")
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("no claim event")
	}
	var op, args string
	var session sql.NullString
	if err := rows.Scan(&op, &args, &session); err != nil {
		t.Fatal(err)
	}
	if op != "claim" {
		t.Errorf("last op = %s, want claim", op)
	}
	if !strings.Contains(args, id) {
		t.Errorf("claim args = %s, want the taken id", args)
	}
	if session.String != sessB {
		t.Errorf("claim session = %s, want %s", session.String, sessB)
	}
	if got := eventCount(t, db); got != 2 {
		t.Errorf("claim must append exactly one event: %d", got)
	}
}

func TestConcurrentClaimExactlyOneSessionWins(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, p, []item{{Text: "one task"}}, sessA)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := taskIDText(t, reply, "one task")
	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make([]string, 2)
	errs := make([]error, 2)
	for i, session := range []string{sessA, sessB} {
		wg.Add(1)
		go func(i int, session string) {
			defer wg.Done()
			<-start
			results[i], errs[i] = todostore.Claim(ctx, db, p, session, "")
		}(i, session)
	}
	close(start)
	wg.Wait()
	for i := range results {
		if errs[i] != nil {
			t.Fatalf("claim %s: %v", []string{sessA, sessB}[i], errs[i])
		}
	}
	claimed, idle := 0, 0
	for _, r := range results {
		if strings.Contains(r, "'"+id+"' claimed") {
			claimed++
		} else if r == "nothing to do" {
			idle++
		}
	}
	if claimed != 1 || idle != 1 {
		t.Fatalf("concurrent claims = %v, want exactly one winner", results)
	}
	if got := projStatus(t, db, "one task"); got != "in_progress" {
		t.Errorf("status = %v, want in_progress", got)
	}
}

func TestClaimWithReviewFilterTakesTheFirstUnownedReviewTask(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, p, []item{{Text: "ready"}}, sessA)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := taskIDText(t, reply, "ready")
	if _, err := todostore.Claim(ctx, db, p, sessA, ""); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := todostore.Complete(ctx, db, p, id, sessA); err != nil {
		t.Fatalf("complete: %v", err)
	}
	claimed, err := todostore.Claim(ctx, db, p, sessB, "review")
	if err != nil {
		t.Fatalf("claim review: %v", err)
	}
	if !strings.Contains(claimed, "'"+id+"' claimed for review") {
		t.Fatalf("review claim reply: %s", claimed)
	}
	if got := projStatus(t, db, "ready"); got != "review" {
		t.Errorf("a review claim must keep the task in review: %v", got)
	}
	peek, err := todostore.Read(ctx, db, p, sessC)
	if err != nil {
		t.Fatalf("peek: %v", err)
	}
	if !strings.Contains(peek, "claimed for review by "+sessB) {
		t.Errorf("the review claim must name the holder:\n%s", peek)
	}
}

func TestClaimReviewSkipsTasksAlreadyHeld(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, p, []item{{Text: "one"}, {Text: "two"}}, sessA)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	one := taskIDText(t, reply, "one")
	two := taskIDText(t, reply, "two")
	for _, id := range []string{one, two} {
		if _, err := todostore.Claim(ctx, db, p, sessA, ""); err != nil {
			t.Fatalf("claim %s: %v", id, err)
		}
		if _, err := todostore.Complete(ctx, db, p, id, sessA); err != nil {
			t.Fatalf("complete %s: %v", id, err)
		}
	}
	if _, err := todostore.Claim(ctx, db, p, sessB, "review"); err != nil {
		t.Fatalf("first review claim: %v", err)
	}
	second, err := todostore.Claim(ctx, db, p, sessC, "review")
	if err != nil {
		t.Fatalf("second review claim: %v", err)
	}
	if !strings.Contains(second, "'"+two+"' claimed for review") {
		t.Fatalf("a held review task must be skipped: %s", second)
	}
	none, err := todostore.Claim(ctx, db, p, sessC, "review")
	if err != nil {
		t.Fatalf("third review claim: %v", err)
	}
	if none != "nothing to do" {
		t.Errorf("all-held review claim = %q, want \"nothing to do\"", none)
	}
}

func TestClaimReviewWithNoReviewTasksRepliesNothingToDo(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	if _, err := todostore.Create(ctx, db, p, []item{{Text: "pending"}}, sessA); err != nil {
		t.Fatalf("create: %v", err)
	}
	claimed, err := todostore.Claim(ctx, db, p, sessA, "review")
	if err != nil {
		t.Fatalf("claim review: %v", err)
	}
	if claimed != "nothing to do" {
		t.Errorf("no-review claim = %q, want \"nothing to do\"", claimed)
	}
}

func TestClaimRefusesAnUnknownStatusFilter(t *testing.T) {
	db := newDB(t)
	if _, err := todostore.Claim(context.Background(), db, p, sessA, "done"); err == nil {
		t.Fatal("unknown claim status succeeded")
	} else if !strings.Contains(err.Error(), "unknown claim status") {
		t.Errorf("unknown-status voice: %v", err)
	}
}

func TestNoteAppendsAndReadShowsNotesInOrderWithTheirSession(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, p, []item{{Text: "shared work"}}, sessA)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := taskIDText(t, reply, "shared work")
	if _, err := todostore.Note(ctx, db, p, id, "first thought", sessA); err != nil {
		t.Fatalf("note 1: %v", err)
	}
	if _, err := todostore.Note(ctx, db, p, id, "second thought", sessB); err != nil {
		t.Fatalf("note 2: %v", err)
	}
	read, err := todostore.Read(ctx, db, p, sessC)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	first := strings.Index(read, "first thought (by "+sessA+")")
	second := strings.Index(read, "second thought (by "+sessB+")")
	if first == -1 || second == -1 {
		t.Fatalf("read must show the notes with their sessions:\n%s", read)
	}
	if first > second {
		t.Fatalf("notes must render in order:\n%s", read)
	}
}

func TestNoteOnATaskYouDoNotHoldIsAllowed(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, p, []item{{Text: "theirs"}}, sessA)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := taskIDText(t, reply, "theirs")
	if _, err := todostore.Claim(ctx, db, p, sessA, ""); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := todostore.Note(ctx, db, p, id, "heads up", sessB); err != nil {
		t.Fatalf("a note on a task you do not hold must land: %v", err)
	}
	if got := projStatus(t, db, "theirs"); got != "in_progress" {
		t.Errorf("the note must not move the task: %v", got)
	}
}

func TestNoteMustNameATask(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	if _, err := todostore.Create(ctx, db, p, []item{{Text: "here"}}, sessA); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := todostore.Note(ctx, db, p, "t99", "hello", sessA); err == nil {
		t.Fatal("a note on a missing task succeeded")
	} else if !strings.Contains(err.Error(), "no task 't99'") {
		t.Errorf("unknown-task voice: %v", err)
	}
}

func TestNoteRefusesEmptyAndOverlongText(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, p, []item{{Text: "here"}}, sessA)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := taskIDText(t, reply, "here")
	if _, err := todostore.Note(ctx, db, p, id, "   ", sessA); err == nil {
		t.Fatal("an empty note succeeded")
	} else if !strings.Contains(err.Error(), "must not be empty") {
		t.Errorf("empty-note voice: %v", err)
	}
	long := strings.Repeat("x", todostore.MaxNoteLen+1)
	if _, err := todostore.Note(ctx, db, p, id, long, sessA); err == nil {
		t.Fatal("an overlong note succeeded")
	} else if !strings.Contains(err.Error(), "too long") {
		t.Errorf("overlong-note voice: %v", err)
	}
	if got := eventCount(t, db); got != 1 {
		t.Errorf("refused notes must append nothing: %d events", got)
	}
}

func TestNoteSurvivesCompaction(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, p, []item{{Text: "remembered"}}, sessA)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := taskIDText(t, reply, "remembered")
	if _, err := todostore.Note(ctx, db, p, id, "kept by the snapshot", sessB); err != nil {
		t.Fatalf("note: %v", err)
	}
	age(t, db, 1010)
	if _, err := todostore.Move(ctx, db, p, id, 1, sessA); err != nil {
		t.Fatalf("move (compaction trigger): %v", err)
	}
	read, err := todostore.Read(ctx, db, p, sessA)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(read, "kept by the snapshot (by "+sessB+")") {
		t.Fatalf("a note must survive compaction:\n%s", read)
	}
}

func TestCompleteMovesActiveToReview(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, p, []item{{Text: "done enough"}}, sessA)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := taskIDText(t, reply, "done enough")
	if _, err := todostore.Claim(ctx, db, p, sessA, ""); err != nil {
		t.Fatalf("claim: %v", err)
	}
	completed, err := todostore.Complete(ctx, db, p, id, sessA)
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if !strings.Contains(completed, "in review") {
		t.Fatalf("complete reply must say the review gate: %s", completed)
	}
	if got := projStatus(t, db, "done enough"); got != "review" {
		t.Errorf("status = %v, want review", got)
	}
	if !strings.Contains(completed, "[r] done enough") {
		t.Errorf("the echo must carry the review marker:\n%s", completed)
	}
}

func TestCompleteOnOwnPendingSubmitsForReview(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, p, []item{{Text: "quick"}}, sessA)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := taskIDText(t, reply, "quick")
	completed, err := todostore.Complete(ctx, db, p, id, sessA)
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if !strings.Contains(completed, "auto-started") {
		t.Fatalf("the auto path must say so: %s", completed)
	}
	if got := projStatus(t, db, "quick"); got != "review" {
		t.Errorf("status = %v, want review", got)
	}
	if got := eventCount(t, db); got != 3 {
		t.Errorf("auto-submit must append start+complete: %d events", got)
	}
}

func TestCompleteOnAReviewTaskRefuses(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, p, []item{{Text: "twice"}}, sessA)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := taskIDText(t, reply, "twice")
	if _, err := todostore.Claim(ctx, db, p, sessA, ""); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := todostore.Complete(ctx, db, p, id, sessA); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if _, err := todostore.Complete(ctx, db, p, id, sessA); err == nil {
		t.Fatal("completing a review task succeeded")
	} else if !strings.Contains(err.Error(), "in review; accept or reject it first") {
		t.Errorf("review-complete voice: %v", err)
	}
}

func TestStartOnAReviewTaskRefuses(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, p, []item{{Text: "held"}}, sessA)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := taskIDText(t, reply, "held")
	if _, err := todostore.Claim(ctx, db, p, sessA, ""); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := todostore.Complete(ctx, db, p, id, sessA); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if _, err := todostore.Start(ctx, db, p, id, sessB); err == nil {
		t.Fatal("starting a review task succeeded")
	} else if !strings.Contains(err.Error(), "in review; accept or reject it first") {
		t.Errorf("review-start voice: %v", err)
	}
}

func TestFailOnAReviewTaskRefuses(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, p, []item{{Text: "broken"}}, sessA)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := taskIDText(t, reply, "broken")
	if _, err := todostore.Claim(ctx, db, p, sessA, ""); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := todostore.Complete(ctx, db, p, id, sessA); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if _, err := todostore.Fail(ctx, db, p, id, sessB); err == nil {
		t.Fatal("failing a review task succeeded")
	} else if !strings.Contains(err.Error(), "in review; accept or reject it first") {
		t.Errorf("review-fail voice: %v", err)
	}
}

func TestAcceptMovesReviewToDone(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, p, []item{{Text: "accepted"}}, sessA)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := taskIDText(t, reply, "accepted")
	if _, err := todostore.Claim(ctx, db, p, sessA, ""); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := todostore.Complete(ctx, db, p, id, sessA); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if _, err := todostore.Claim(ctx, db, p, sessB, "review"); err != nil {
		t.Fatalf("claim review: %v", err)
	}
	accepted, err := todostore.Accept(ctx, db, p, id, sessB)
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if !strings.Contains(accepted, "'"+id+"' accepted") {
		t.Fatalf("accept reply: %s", accepted)
	}
	if got := projStatus(t, db, "accepted"); got != "done" {
		t.Errorf("status = %v, want done", got)
	}
}

func TestAcceptRequiresTheReviewHold(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, p, []item{{Text: "held"}, {Text: "unclaimed"}}, sessA)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	unclaimed := taskIDText(t, reply, "unclaimed")
	held := taskIDText(t, reply, "held")
	for _, id := range []string{unclaimed, held} {
		if _, err := todostore.Claim(ctx, db, p, sessA, ""); err != nil {
			t.Fatalf("claim %s: %v", id, err)
		}
		if _, err := todostore.Complete(ctx, db, p, id, sessA); err != nil {
			t.Fatalf("complete %s: %v", id, err)
		}
	}
	if _, err := todostore.Claim(ctx, db, p, sessB, "review"); err != nil {
		t.Fatalf("claim review: %v", err)
	}
	if _, err := todostore.Accept(ctx, db, p, unclaimed, sessB); err == nil {
		t.Fatal("accepting an unclaimed review task succeeded")
	} else if !strings.Contains(err.Error(), "claim it first") {
		t.Errorf("unclaimed-accept voice: %v", err)
	}
	if _, err := todostore.Accept(ctx, db, p, held, sessC); err == nil {
		t.Fatal("accepting another reviewer's task succeeded")
	} else if !strings.Contains(err.Error(), "claimed for review by "+sessB) {
		t.Errorf("foreign-accept voice: %v", err)
	}
}

func TestAcceptOnNonReviewRefuses(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, p, []item{
		{Text: "active"}, {Text: "done"}, {Text: "failed"}, {Text: "unclaimed review"}, {Text: "pending"},
	}, sessA)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	pending := taskIDText(t, reply, "pending")
	active := taskIDText(t, reply, "active")
	done := taskIDText(t, reply, "done")
	failed := taskIDText(t, reply, "failed")
	review := taskIDText(t, reply, "unclaimed review")
	for range []int{0, 1, 2} {
		if _, err := todostore.Claim(ctx, db, p, sessA, ""); err != nil {
			t.Fatalf("claim: %v", err)
		}
	}
	if _, err := todostore.Complete(ctx, db, p, done, sessA); err != nil {
		t.Fatalf("complete done: %v", err)
	}
	if _, err := todostore.Claim(ctx, db, p, sessA, "review"); err != nil {
		t.Fatalf("claim review: %v", err)
	}
	if _, err := todostore.Accept(ctx, db, p, done, sessA); err != nil {
		t.Fatalf("accept done: %v", err)
	}
	if _, err := todostore.Fail(ctx, db, p, failed, sessA); err != nil {
		t.Fatalf("fail: %v", err)
	}
	if _, err := todostore.Complete(ctx, db, p, review, sessA); err != nil {
		t.Fatalf("complete review: %v", err)
	}
	for id, want := range map[string]string{
		pending: "pending",
		active:  "in progress",
		done:    "done",
		failed:  "failed",
		review:  "claim it first",
	} {
		if _, err := todostore.Accept(ctx, db, p, id, sessA); err == nil {
			t.Fatalf("accept on %s succeeded", id)
		} else if !strings.Contains(err.Error(), want) {
			t.Errorf("accept-on-%s voice %q must name %q", id, err.Error(), want)
		}
	}
}

func TestRejectMovesReviewToPendingAndNotesTheReason(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, p, []item{{Text: "needs work"}}, sessA)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := taskIDText(t, reply, "needs work")
	if _, err := todostore.Claim(ctx, db, p, sessA, ""); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := todostore.Complete(ctx, db, p, id, sessA); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if _, err := todostore.Claim(ctx, db, p, sessB, "review"); err != nil {
		t.Fatalf("claim review: %v", err)
	}
	rejected, err := todostore.Reject(ctx, db, p, id, "tests are missing", sessB)
	if err != nil {
		t.Fatalf("reject: %v", err)
	}
	if !strings.Contains(rejected, "'"+id+"' rejected") {
		t.Fatalf("reject reply: %s", rejected)
	}
	if got := projStatus(t, db, "needs work"); got != "pending" {
		t.Errorf("status = %v, want pending", got)
	}
	read, err := todostore.Read(ctx, db, p, sessA)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(read, "tests are missing (by "+sessB+")") {
		t.Fatalf("the rejection reason must be a note:\n%s", read)
	}
}

func TestRejectRequiresTheHoldAndAReason(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, p, []item{{Text: "unclaimed"}}, sessA)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := taskIDText(t, reply, "unclaimed")
	if _, err := todostore.Claim(ctx, db, p, sessA, ""); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := todostore.Complete(ctx, db, p, id, sessA); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if _, err := todostore.Reject(ctx, db, p, id, "no", sessB); err == nil {
		t.Fatal("rejecting an unclaimed review task succeeded")
	} else if !strings.Contains(err.Error(), "claim it first") {
		t.Errorf("unclaimed-reject voice: %v", err)
	}
	if _, err := todostore.Claim(ctx, db, p, sessB, "review"); err != nil {
		t.Fatalf("claim review: %v", err)
	}
	if _, err := todostore.Reject(ctx, db, p, id, "", sessB); err == nil {
		t.Fatal("rejecting with no reason succeeded")
	} else if !strings.Contains(err.Error(), "reason") {
		t.Errorf("empty-reason voice: %v", err)
	}
	if _, err := todostore.Reject(ctx, db, p, id, "no", sessC); err == nil {
		t.Fatal("rejecting another reviewer's task succeeded")
	} else if !strings.Contains(err.Error(), "claimed for review by "+sessB) {
		t.Errorf("foreign-reject voice: %v", err)
	}
	if got := projStatus(t, db, "unclaimed"); got != "review" {
		t.Errorf("a refused reject must not move the task: %v", got)
	}
}

func TestBlockedByClearsOnlyOnDone(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, p, []item{
		{Text: "gate"}, {Text: "work", DependsOn: ptrTo("gate")},
	}, sessA)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	gate := taskIDText(t, reply, "gate")
	if _, err := todostore.Claim(ctx, db, p, sessA, ""); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := todostore.Complete(ctx, db, p, gate, sessA); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if claimed, err := todostore.Claim(ctx, db, p, sessB, ""); err != nil {
		t.Fatalf("claim: %v", err)
	} else if claimed != "nothing to do" {
		t.Errorf("a review dependency must still block: %q", claimed)
	}
	if _, err := todostore.Claim(ctx, db, p, sessB, "review"); err != nil {
		t.Fatalf("claim review: %v", err)
	}
	if _, err := todostore.Accept(ctx, db, p, gate, sessB); err != nil {
		t.Fatalf("accept: %v", err)
	}
	claimed, err := todostore.Claim(ctx, db, p, sessB, "")
	if err != nil {
		t.Fatalf("claim after accept: %v", err)
	}
	if !strings.Contains(claimed, "'t2' claimed") {
		t.Errorf("done must clear the blocker: %s", claimed)
	}
}

func TestPruneDropsDoneOnly(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, p, []item{
		{Text: "accepted"}, {Text: "failed"}, {Text: "in review"}, {Text: "pending"},
	}, sessA)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	accepted := taskIDText(t, reply, "accepted")
	inReview := taskIDText(t, reply, "in review")
	failed := taskIDText(t, reply, "failed")
	for _, id := range []string{accepted, failed, inReview} {
		if _, err := todostore.Claim(ctx, db, p, sessA, ""); err != nil {
			t.Fatalf("claim %s: %v", id, err)
		}
	}
	if _, err := todostore.Complete(ctx, db, p, accepted, sessA); err != nil {
		t.Fatalf("complete accepted: %v", err)
	}
	if _, err := todostore.Complete(ctx, db, p, inReview, sessA); err != nil {
		t.Fatalf("complete in review: %v", err)
	}
	if _, err := todostore.Fail(ctx, db, p, failed, sessA); err != nil {
		t.Fatalf("fail: %v", err)
	}
	if _, err := todostore.Claim(ctx, db, p, sessA, "review"); err != nil {
		t.Fatalf("claim review: %v", err)
	}
	if _, err := todostore.Accept(ctx, db, p, accepted, sessA); err != nil {
		t.Fatalf("accept: %v", err)
	}
	if _, err := todostore.Prune(ctx, db, p, sessA); err != nil {
		t.Fatalf("prune: %v", err)
	}
	for _, text := range []string{"in review", "pending", "failed"} {
		if got := projStatus(t, db, text); got == "" {
			t.Errorf("prune must not drop the %s row", text)
		}
	}
	if rows := rawQuery(t, db, "SELECT count(*) FROM tasks WHERE scope = 'ws' AND text = 'accepted'"); !rows.Next() {
		t.Fatal("count query failed")
	} else {
		var n int64
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		if n != 0 {
			t.Errorf("prune must drop the accepted row, found %d", n)
		}
	}
}

func TestReleaseFreesAStaleReviewClaimKeepingTheStatus(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, p, []item{{Text: "released"}, {Text: "reaped"}}, sessA)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	releasedID := taskIDText(t, reply, "released")
	reapedID := taskIDText(t, reply, "reaped")
	for _, id := range []string{releasedID, reapedID} {
		if _, err := todostore.Claim(ctx, db, p, sessA, ""); err != nil {
			t.Fatalf("claim %s: %v", id, err)
		}
		if _, err := todostore.Complete(ctx, db, p, id, sessA); err != nil {
			t.Fatalf("complete %s: %v", id, err)
		}
		if _, err := todostore.Claim(ctx, db, p, sessA, "review"); err != nil {
			t.Fatalf("claim review %s: %v", id, err)
		}
	}
	old := time.Now().Add(-todostore.StaleClaimAfter - time.Hour).UTC().Format(time.RFC3339)
	rawExec(t, db, "UPDATE events SET ts = ? WHERE op = 'claim'", old)
	released, err := todostore.Release(ctx, db, p, releasedID, sessB)
	if err != nil {
		t.Fatalf("release: %v", err)
	}
	if !strings.Contains(released, releasedID) || !strings.Contains(released, sessA) {
		t.Fatalf("release reply must name the task and the dead holder: %s", released)
	}
	if got := projStatus(t, db, "released"); got != "review" {
		t.Errorf("a released review claim must stay in review: %v", got)
	}
	reaped, err := todostore.Reap(ctx, db, p, []string{sessA}, sessB)
	if err != nil {
		t.Fatalf("reap: %v", err)
	}
	if !strings.Contains(reaped, reapedID) {
		t.Fatalf("reap must free the dead reviewer's claim: %s", reaped)
	}
	if got := projStatus(t, db, "reaped"); got != "review" {
		t.Errorf("a reaped review claim must stay in review: %v", got)
	}
}

func TestReleaseOnAReviewTaskRefusesOwnUnclaimedAndFresh(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, p, []item{{Text: "mine"}, {Text: "fresh"}, {Text: "open"}}, sessA)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	mine := taskIDText(t, reply, "mine")
	open := taskIDText(t, reply, "open")
	fresh := taskIDText(t, reply, "fresh")
	for _, id := range []string{mine, fresh, open} {
		if _, err := todostore.Claim(ctx, db, p, sessA, ""); err != nil {
			t.Fatalf("claim %s: %v", id, err)
		}
		if _, err := todostore.Complete(ctx, db, p, id, sessA); err != nil {
			t.Fatalf("complete %s: %v", id, err)
		}
	}
	if _, err := todostore.Claim(ctx, db, p, sessA, "review"); err != nil {
		t.Fatalf("claim review mine: %v", err)
	}
	if _, err := todostore.Claim(ctx, db, p, sessB, "review"); err != nil {
		t.Fatalf("claim review fresh: %v", err)
	}
	if _, err := todostore.Release(ctx, db, p, mine, sessA); err == nil {
		t.Fatal("releasing your own review claim must refuse")
	} else if !strings.Contains(err.Error(), "claimed for review by you") {
		t.Errorf("own-review voice: %v", err)
	}
	if _, err := todostore.Release(ctx, db, p, open, sessC); err == nil {
		t.Fatal("releasing an unclaimed review task must refuse")
	} else if !strings.Contains(err.Error(), "not claimed for review") {
		t.Errorf("unclaimed-review voice: %v", err)
	}
	if _, err := todostore.Release(ctx, db, p, fresh, sessC); err == nil {
		t.Fatal("releasing a live review claim must refuse")
	} else if !strings.Contains(err.Error(), sessB) {
		t.Errorf("fresh-review voice must name the holder: %v", err)
	}
}

func TestReplayAcrossClaimNoteRejectAccept(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, p, []item{{Text: "ship it"}}, sessA)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := taskIDText(t, reply, "ship it")
	if _, err := todostore.Claim(ctx, db, p, sessA, ""); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := todostore.Note(ctx, db, p, id, "on it", sessB); err != nil {
		t.Fatalf("note: %v", err)
	}
	if _, err := todostore.Complete(ctx, db, p, id, sessA); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if _, err := todostore.Claim(ctx, db, p, sessC, "review"); err != nil {
		t.Fatalf("claim review: %v", err)
	}
	if _, err := todostore.Reject(ctx, db, p, id, "needs tests", sessC); err != nil {
		t.Fatalf("reject: %v", err)
	}
	if _, err := todostore.Claim(ctx, db, p, sessA, ""); err != nil {
		t.Fatalf("claim again: %v", err)
	}
	if _, err := todostore.Note(ctx, db, p, id, "redone", sessA); err != nil {
		t.Fatalf("note again: %v", err)
	}
	if _, err := todostore.Complete(ctx, db, p, id, sessA); err != nil {
		t.Fatalf("complete again: %v", err)
	}
	if _, err := todostore.Claim(ctx, db, p, sessC, "review"); err != nil {
		t.Fatalf("claim review again: %v", err)
	}
	if _, err := todostore.Accept(ctx, db, p, id, sessC); err != nil {
		t.Fatalf("accept: %v", err)
	}
	rows := rawQuery(t, db, "SELECT op FROM events ORDER BY seq")
	defer rows.Close()
	var ops []string
	for rows.Next() {
		var op string
		if err := rows.Scan(&op); err != nil {
			t.Fatal(err)
		}
		ops = append(ops, op)
	}
	want := []string{"create", "claim", "note", "complete", "claim", "reject", "claim", "note", "complete", "claim", "accept"}
	if strings.Join(ops, ",") != strings.Join(want, ",") {
		t.Errorf("event order = %v, want %v", ops, want)
	}
	rawExec(t, db, "DELETE FROM tasks WHERE scope = 'ws'")
	rawExec(t, db, "DELETE FROM task_deps WHERE scope = 'ws'")
	if _, err := todostore.Read(ctx, db, p, sessA); err != nil {
		t.Fatalf("read after tamper: %v", err)
	}
	if got := projStatus(t, db, "ship it"); got != "done" {
		t.Errorf("replay must reproduce done: %v", got)
	}
	read, err := todostore.ReadAll(ctx, db, p, sessA)
	if err != nil {
		t.Fatalf("read all: %v", err)
	}
	for _, note := range []string{
		"on it (by " + sessB + ")",
		"needs tests (by " + sessC + ")",
		"redone (by " + sessA + ")",
	} {
		if !strings.Contains(read, note) {
			t.Errorf("replay must keep the note %q:\n%s", note, read)
		}
	}
}

func TestNotesAndReviewSurviveCompaction(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, p, []item{{Text: "review me"}}, sessA)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := taskIDText(t, reply, "review me")
	if _, err := todostore.Claim(ctx, db, p, sessA, ""); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := todostore.Note(ctx, db, p, id, "done", sessA); err != nil {
		t.Fatalf("note: %v", err)
	}
	if _, err := todostore.Complete(ctx, db, p, id, sessA); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if _, err := todostore.Claim(ctx, db, p, sessB, "review"); err != nil {
		t.Fatalf("claim review: %v", err)
	}
	age(t, db, 1010)
	if _, err := todostore.Move(ctx, db, p, id, 1, sessB); err != nil {
		t.Fatalf("move (compaction trigger): %v", err)
	}
	if got := projStatus(t, db, "review me"); got != "review" {
		t.Errorf("review must survive compaction: %v", got)
	}
	read, err := todostore.Read(ctx, db, p, sessA)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(read, "done (by "+sessA+")") {
		t.Errorf("notes must survive compaction:\n%s", read)
	}
	if !strings.Contains(read, "claimed for review by "+sessB) {
		t.Errorf("the review hold must survive compaction:\n%s", read)
	}
}

func TestSummaryCountsTasksInReview(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, p, []item{{Text: "awaiting"}}, sessA)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := taskIDText(t, reply, "awaiting")
	if _, err := todostore.Claim(ctx, db, p, sessA, ""); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := todostore.Complete(ctx, db, p, id, sessA); err != nil {
		t.Fatalf("complete: %v", err)
	}
	read, err := todostore.Read(ctx, db, p, sessA)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(read, "0/1 done") || !strings.Contains(read, "1 in review") {
		t.Fatalf("the summary must count the review row:\n%s", read)
	}
}
