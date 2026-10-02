package decision_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

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
}

func (f *fakeFire) fire(ctx context.Context, prompt string) (string, error) {
	if f.calls >= len(f.stdouts) {
		return "", nil
	}
	f.prompts = append(f.prompts, prompt)
	f.calls++
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
	return decision.NewReviewer(storeReviews{db: db}, f.fire, "dsv4", func(string) {})
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

func TestAFireThatSettlesNothingDoesNotSelfWake(t *testing.T) {
	db := openReviewedStore(t, 2)
	f := &fakeFire{stdouts: []string{"I have no idea"}}
	r := reviewer(db, f)
	if err := r.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.calls != 1 {
		t.Fatalf("a garbage fire converges, got %d fires", f.calls)
	}
	rows, _ := decisionstore.Pending(context.Background(), db)
	if len(rows) != 2 {
		t.Fatalf("nothing settled: %d pending", len(rows))
	}
}
