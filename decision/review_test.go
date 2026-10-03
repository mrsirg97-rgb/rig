package decision_test

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/v2/decision"
	"github.com/mrsirg97-rgb/rig/v2/store"
	decisionstore "github.com/mrsirg97-rgb/rig/v2/store/decision"
)

type fakeFire struct {
	stdouts []string
	calls   int
	prompts []string
	fired   chan struct{}
}

func (f *fakeFire) fire(ctx context.Context, prompt string) (string, string, error) {
	f.calls++
	f.prompts = append(f.prompts, prompt)
	if f.fired != nil {
		f.fired <- struct{}{}
	}
	if f.calls > len(f.stdouts) {
		return "", "dsv4", nil
	}
	return f.stdouts[f.calls-1], "dsv4", nil
}

func openReviewedStore(t *testing.T, n int) store.DB {
	t.Helper()
	db, _, _, err := store.Open(filepath.Join(t.TempDir(), "decision.sqlite"), decisionstore.Statements(), decisionstore.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	for i := 0; i < n; i++ {
		if _, err := decisionstore.Propose(ctx, db, decisionstore.ProposeInput{
			Scope: "proj", Site: decision.SiteBash, State: `{"command":"ls"}`,
			Question:   decision.Choice("risk", "What risk does this bash call carry?", "safe", "changes", "dangerous"),
			Answer:     "safe",
			Confidence: 0.71,
			Decider:    "laya",
		}); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func reviewer(db store.DB, f *fakeFire) *decision.Reviewer {
	return reviewerWithBudget(db, f, 1<<30)
}

func reviewerWithBudget(db store.DB, f *fakeFire, budget int) *decision.Reviewer {
	return reviewerWithReply(db, f, budget, 1<<30)
}

func reviewerWithReply(db store.DB, f *fakeFire, budget, maxOut int) *decision.Reviewer {
	return decision.NewReviewer(decisionstore.Reviews{DB: db}, f.fire, budget, maxOut, func(string) {})
}

func TestThreePendingRowsAreReviewedInOneFire(t *testing.T) {
	db := openReviewedStore(t, 3)
	f := &fakeFire{stdouts: []string{"reading\nverdict: 1 approve\nverdict: 2 approve\nverdict: 3 approve"}}
	r := reviewer(db, f)
	if _, err := r.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.calls != 1 {
		t.Fatalf("every pending row is one fire, got %d fires", f.calls)
	}
	if !strings.Contains(f.prompts[0], "verdict:") || !strings.Contains(f.prompts[0], "What risk does this bash call carry?") {
		t.Fatalf("the prompt carries the rows and the contract: %q", f.prompts[0])
	}
	rows, _ := decisionstore.Pending(context.Background(), db)
	if len(rows) != 0 {
		t.Fatalf("all three settled: %d rows pending", len(rows))
	}
	var approved int
	if err := db.QueryRow(`SELECT count(*) FROM decisions WHERE status = 'approved'`).Scan(&approved); err != nil {
		t.Fatal(err)
	}
	if approved != 3 {
		t.Fatalf("all three approved, got %d", approved)
	}
}

func TestADenyStoresTheCorrectedAnswer(t *testing.T) {
	db := openReviewedStore(t, 1)
	f := &fakeFire{stdouts: []string{"the call removes a tree\nverdict: 1 deny changes"}}
	r := reviewer(db, f)
	if _, err := r.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	var status, rev string
	var correction *string
	if err := db.QueryRow(`SELECT status, reviewer, reviewer_answer FROM decisions WHERE id = 1`).Scan(&status, &rev, &correction); err != nil {
		t.Fatal(err)
	}
	if status != decision.StatusDenied || rev != "dsv4" || correction == nil || *correction != "changes" {
		t.Fatalf("the deny stores the correction and the reviewer: %q %q %v", status, rev, correction)
	}
}

func TestAPartialReplyLeavesTheUnnamedRowPending(t *testing.T) {
	db := openReviewedStore(t, 2)
	f := &fakeFire{stdouts: []string{"verdict: 1 approve"}}
	r := reviewer(db, f)
	if _, err := r.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	rows, _ := decisionstore.Pending(context.Background(), db)
	if len(rows) != 1 || rows[0].ID != 2 {
		t.Fatalf("the unnamed row stays pending: %+v", rows)
	}
	var status string
	if err := db.QueryRow(`SELECT status FROM decisions WHERE id = 1`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != decision.StatusApproved {
		t.Fatalf("the named row settled: %q", status)
	}
}

func TestADenyWithoutACorrectionIsNotAVerdict(t *testing.T) {
	db := openReviewedStore(t, 1)
	f := &fakeFire{stdouts: []string{"verdict: 1 deny"}}
	r := reviewer(db, f)
	if _, err := r.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	rows, _ := decisionstore.Pending(context.Background(), db)
	if len(rows) != 1 {
		t.Fatalf("the deny without a correction leaves the row pending: %d", len(rows))
	}
}

func TestAFireTakesTheOldestRowsThatFitTheWindow(t *testing.T) {
	db := openReviewedStore(t, 2)
	_, err := db.Exec(`UPDATE decisions SET state = ?`, strings.Repeat("x", 8000))
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeFire{stdouts: []string{"verdict: 1 approve", "verdict: 2 approve"}}
	r := reviewerWithBudget(db, f, 1000)
	if _, err := r.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.calls != 1 || strings.Count(f.prompts[0], "\n== ") != 1 {
		t.Fatalf("the fire took the one row that fits: %d fires, %d rows in the prompt", f.calls, strings.Count(f.prompts[0], "\n== "))
	}
	rows, _ := decisionstore.Pending(context.Background(), db)
	if len(rows) != 1 || rows[0].ID != 2 {
		t.Fatalf("the row past the budget stays pending: %+v", rows)
	}
}

func TestTheReplyBoundTakesFortyOfTwoHundredSixtyFourRows(t *testing.T) {
	db := openReviewedStore(t, 264)
	var reply strings.Builder
	for i := 1; i <= 40; i++ {
		fmt.Fprintf(&reply, "verdict: %d approve\n", i)
	}
	f := &fakeFire{stdouts: []string{reply.String()}}
	r := reviewerWithReply(db, f, 1<<30, 40*decision.VerdictLineCost())
	summary, err := r.Drain(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if f.calls != 1 {
		t.Fatalf("one door is one fire, got %d fires", f.calls)
	}
	if rows := strings.Count(f.prompts[0], "\n== "); rows != 40 {
		t.Fatalf("the fire took %d rows, want the 40 verdict lines the reply budget fits", rows)
	}
	pending, _ := decisionstore.Pending(context.Background(), db)
	if len(pending) != 224 {
		t.Fatalf("the rest stay pending for the next door: %d", len(pending))
	}
	if !strings.Contains(summary, "40") || !strings.Contains(summary, "224") {
		t.Fatalf("the summary names the fire and what stays: %q", summary)
	}
}

func TestTheWindowBoundHoldsUnderAReplyBudgetThatFitsEverything(t *testing.T) {
	db := openReviewedStore(t, 2)
	_, err := db.Exec(`UPDATE decisions SET state = ?`, strings.Repeat("x", 8000))
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeFire{stdouts: []string{"verdict: 1 approve", "verdict: 2 approve"}}
	r := reviewerWithReply(db, f, 1000, 1<<30)
	if _, err := r.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	if rows := strings.Count(f.prompts[0], "\n== "); rows != 1 {
		t.Fatalf("the window minus the reserve is still the second bound: %d rows", rows)
	}
}

func TestAMaxOutputUnderOneVerdictLineStillFiresOneRow(t *testing.T) {
	db := openReviewedStore(t, 3)
	f := &fakeFire{stdouts: []string{"verdict: 1 approve\nverdict: 2 approve\nverdict: 3 approve"}}
	r := reviewerWithReply(db, f, 1<<30, decision.VerdictLineCost()-1)
	if _, err := r.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	if rows := strings.Count(f.prompts[0], "\n== "); rows != 1 {
		t.Fatalf("one row still goes out under a budget that fits no line: %d rows", rows)
	}
}

func TestTheVerdictLineCostIsDerivedFromTheContract(t *testing.T) {
	cost := decision.VerdictLineCost()
	if cost <= 0 {
		t.Fatal("the contract lost its verdict lines; the reply bound cannot be derived")
	}
	if again := decision.VerdictLineCost(); again != cost {
		t.Fatalf("the derivation is not deterministic: %d then %d", cost, again)
	}
}

func TestAFireThatSettlesNothingLeavesTheRowsPending(t *testing.T) {
	db := openReviewedStore(t, 2)
	f := &fakeFire{stdouts: []string{"I have no idea"}}
	r := reviewer(db, f)
	summary, err := r.Drain(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	rows, _ := decisionstore.Pending(context.Background(), db)
	if len(rows) != 2 {
		t.Fatalf("nothing settled: %d pending", len(rows))
	}
	if !strings.Contains(summary, "settled 0") {
		t.Fatalf("the summary names the outcome: %q", summary)
	}
}

func TestADoorWithNothingPendingCostsNoFire(t *testing.T) {
	db := openReviewedStore(t, 0)
	f := &fakeFire{}
	r := reviewer(db, f)
	summary, err := r.Drain(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if f.calls != 0 {
		t.Fatalf("nothing pending fires nothing, got %d fires", f.calls)
	}
	if summary == "" {
		t.Fatal("the door still gets a summary")
	}
}
