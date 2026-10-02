package decision_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/v2/decision"
	"github.com/mrsirg97-rgb/rig/v2/store"
	decisionstore "github.com/mrsirg97-rgb/rig/v2/store/decision"
)

type storeReviews struct {
	db store.DB
}

func (r storeReviews) Pending(ctx context.Context) ([]decision.ReviewRow, error) {
	rows, err := decisionstore.Pending(ctx, r.db)
	if err != nil {
		return nil, err
	}
	out := make([]decision.ReviewRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, decision.ReviewRow{
			ID: row.ID, Site: row.Site, State: row.State, Question: row.Question,
			Answer: row.Answer, Confidence: row.Confidence, Decider: row.Decider,
		})
	}
	return out, nil
}

func (r storeReviews) Settle(ctx context.Context, id int64, approved bool, reviewer, answer string) error {
	return decisionstore.Settle(ctx, r.db, decisionstore.SettleInput{
		ID: id, Approved: approved, Reviewer: reviewer, ReviewerAnswer: answer,
	})
}

type fakeFire struct {
	stdouts []string
	calls   int
	prompts []string
	fired   chan struct{}
}

func (f *fakeFire) fire(ctx context.Context, prompt string) (string, error) {
	f.calls++
	f.prompts = append(f.prompts, prompt)
	if f.fired != nil {
		f.fired <- struct{}{}
	}
	if f.calls > len(f.stdouts) {
		return "", nil
	}
	return f.stdouts[f.calls-1], nil
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
	return decision.NewReviewer(storeReviews{db: db}, f.fire, "dsv4", budget, func(string) {})
}

func TestThreePendingRowsAreReviewedInOneFire(t *testing.T) {
	db := openReviewedStore(t, 3)
	f := &fakeFire{stdouts: []string{"reading\nverdict: 1 approve\nverdict: 2 approve\nverdict: 3 approve"}}
	r := reviewer(db, f)
	if err := r.Drain(context.Background()); err != nil {
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
	if err := r.Drain(context.Background()); err != nil {
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
	if err := r.Drain(context.Background()); err != nil {
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
	if err := r.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	rows, _ := decisionstore.Pending(context.Background(), db)
	if len(rows) != 1 {
		t.Fatalf("the deny without a correction leaves the row pending: %d", len(rows))
	}
}

func TestAFireTakesTheOldestRowsThatFitTheWindow(t *testing.T) {
	db := openReviewedStore(t, 2)
	// a row's state alone is past the budget, so one fire carries one row
	_, err := db.Exec(`UPDATE decisions SET state = ?`, strings.Repeat("x", 8000))
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeFire{stdouts: []string{"verdict: 1 approve", "verdict: 2 approve"}}
	r := reviewerWithBudget(db, f, 1000)
	if err := r.Drain(context.Background()); err != nil {
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

func TestALandingMarksDirtyAndTheTurnEndWakes(t *testing.T) {
	db := openReviewedStore(t, 1)
	f := &fakeFire{stdouts: []string{"verdict: 1 approve"}, fired: make(chan struct{}, 4)}
	r := reviewer(db, f)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.Run(ctx)

	r.Land()
	r.Wake()
	select {
	case <-f.fired:
	case <-time.After(2 * time.Second):
		t.Fatal("a landing marks the reviewer dirty; the turn end is the wake")
	}
}

func TestATurnEndWithNoLandingCostsNothing(t *testing.T) {
	db := openReviewedStore(t, 1)
	f := &fakeFire{stdouts: []string{"verdict: 1 approve"}, fired: make(chan struct{}, 4)}
	r := reviewer(db, f)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.Run(ctx)

	r.Wake()
	select {
	case <-f.fired:
		t.Fatal("a turn end with no landing behind it reviews nothing")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestTheRowsPastTheBudgetStayDirtyForTheNextTurnEnd(t *testing.T) {
	db := openReviewedStore(t, 2)
	_, err := db.Exec(`UPDATE decisions SET state = ?`, strings.Repeat("x", 8000))
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeFire{stdouts: []string{"verdict: 1 approve", "verdict: 2 approve"}, fired: make(chan struct{}, 4)}
	r := reviewerWithBudget(db, f, 1000)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.Run(ctx)

	r.Land()
	r.Wake()
	<-f.fired
	r.Wake() // the next turn end: the overflow is still dirty
	<-f.fired
	select {
	case <-f.fired:
		t.Fatal("both rows settled, the reviewer is clean")
	case <-time.After(200 * time.Millisecond):
	}
	rows, _ := decisionstore.Pending(context.Background(), db)
	if len(rows) != 0 {
		t.Fatalf("the overflow converged over the turn ends: %d pending", len(rows))
	}
}

func TestAFireThatSettlesNothingWaitsForTheNextLanding(t *testing.T) {
	db := openReviewedStore(t, 2)
	f := &fakeFire{stdouts: []string{"I have no idea"}, fired: make(chan struct{}, 4)}
	r := reviewer(db, f)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.Run(ctx)

	r.Land()
	r.Wake()
	<-f.fired
	r.Wake()
	select {
	case <-f.fired:
		t.Fatal("a fire that settles nothing waits for the next landing, not the next turn end")
	case <-time.After(200 * time.Millisecond):
	}
	rows, _ := decisionstore.Pending(context.Background(), db)
	if len(rows) != 2 {
		t.Fatalf("nothing settled: %d pending", len(rows))
	}
}
