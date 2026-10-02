package decision_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/decision"
	"github.com/mrsirg97-rgb/rig/v2/store"
	decisionstore "github.com/mrsirg97-rgb/rig/v2/store/decision"
)

func open(t *testing.T) store.DB {
	t.Helper()
	db, _, _, err := store.Open(filepath.Join(t.TempDir(), "decision.sqlite"), decisionstore.Statements(), decisionstore.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestAFinalRowRoundTrips(t *testing.T) {
	db := open(t)
	ctx := core.WithSession(context.Background(), &core.Session{ID: "s1"})
	id, err := decisionstore.RecordFinal(ctx, db, decisionstore.FinalInput{
		Scope:    "proj",
		Site:     decision.SiteApprove,
		State:    `{"call":"bash"}`,
		Question: decision.YesNo("run", "run this call?"),
		Answer:   "no",
		Decider:  decision.SiteApprove,
	})
	if err != nil {
		t.Fatal(err)
	}
	if id != 1 {
		t.Fatalf("ids mint forward from one: %d", id)
	}
	rows, err := decisionstore.Pending(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("a final row is never pending: %d", len(rows))
	}
	var status string
	var session *string
	var scope string
	if err := db.QueryRow(`SELECT status, session, scope FROM decisions WHERE id = 1`).Scan(&status, &session, &scope); err != nil {
		t.Fatal(err)
	}
	if status != decision.StatusFinal || scope != "proj" {
		t.Fatalf("final row: status %q scope %q", status, scope)
	}
	if session != nil {
		t.Fatalf("the verb stores the session it is given, got %q", *session)
	}
}

func TestAProposalIsPending(t *testing.T) {
	db := open(t)
	id, err := decisionstore.Propose(context.Background(), db, decisionstore.ProposeInput{
		Scope:      "proj",
		Site:       decision.SiteBash,
		State:      `{"command":"ls"}`,
		Question:   decision.Choice("risk", "What risk does this bash call carry?", "safe", "changes", "dangerous"),
		Answer:     "safe",
		Confidence: 0.71,
		Decider:    "127.0.0.1:8712",
	})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := decisionstore.Pending(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != id {
		t.Fatalf("the proposal is pending: %+v", rows)
	}
	row := rows[0]
	if row.Site != decision.SiteBash || row.Answer != "safe" || row.Decider != "127.0.0.1:8712" {
		t.Fatalf("the row carries the proposal: %+v", row)
	}
	if row.Confidence == nil || *row.Confidence != 0.71 {
		t.Fatalf("the confidence rides the row: %+v", row.Confidence)
	}
	if row.Question.Kind != decision.KindChoice || len(row.Question.Choices) != 3 {
		t.Fatalf("the typed question round-trips: %+v", row.Question)
	}
}

func TestSettleApproves(t *testing.T) {
	db := open(t)
	id, err := decisionstore.Propose(context.Background(), db, decisionstore.ProposeInput{
		Site: decision.SiteBash, Scope: "proj", Question: decision.Choice("risk", "risk?", "safe", "changes", "dangerous"),
		Answer: "safe", Confidence: 0.5, Decider: "laya",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := decisionstore.Settle(context.Background(), db, decisionstore.SettleInput{ID: id, Approved: true, Reviewer: "dsv4"}); err != nil {
		t.Fatal(err)
	}
	rows, _ := decisionstore.Pending(context.Background(), db)
	if len(rows) != 0 {
		t.Fatalf("a settled row is not pending")
	}
	var status, reviewer string
	var answer *string
	if err := db.QueryRow(`SELECT status, reviewer, reviewer_answer FROM decisions WHERE id = ?`, id).Scan(&status, &reviewer, &answer); err != nil {
		t.Fatal(err)
	}
	if status != decision.StatusApproved || reviewer != "dsv4" || answer != nil {
		t.Fatalf("approved: %q %q %v", status, reviewer, answer)
	}
}

func TestSettleDeniesWithTheCorrection(t *testing.T) {
	db := open(t)
	id, err := decisionstore.Propose(context.Background(), db, decisionstore.ProposeInput{
		Site: decision.SiteBash, Scope: "proj", Question: decision.Choice("risk", "risk?", "safe", "changes", "dangerous"),
		Answer: "safe", Confidence: 0.5, Decider: "laya",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := decisionstore.Settle(context.Background(), db, decisionstore.SettleInput{
		ID: id, Approved: false, Reviewer: "dsv4", ReviewerAnswer: "dangerous",
	}); err != nil {
		t.Fatal(err)
	}
	var status, reviewer string
	var correction *string
	if err := db.QueryRow(`SELECT status, reviewer, reviewer_answer FROM decisions WHERE id = ?`, id).Scan(&status, &reviewer, &correction); err != nil {
		t.Fatal(err)
	}
	if status != decision.StatusDenied || reviewer != "dsv4" || correction == nil || *correction != "dangerous" {
		t.Fatalf("denied: %q %q %v", status, reviewer, correction)
	}
}

func TestSettleIsANoopOnceSettled(t *testing.T) {
	db := open(t)
	id, err := decisionstore.Propose(context.Background(), db, decisionstore.ProposeInput{
		Site: decision.SiteBash, Scope: "proj", Question: decision.Choice("risk", "risk?", "safe", "changes", "dangerous"),
		Answer: "safe", Confidence: 0.5, Decider: "laya",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := decisionstore.Settle(context.Background(), db, decisionstore.SettleInput{ID: id, Approved: true, Reviewer: "a"}); err != nil {
		t.Fatal(err)
	}
	if err := decisionstore.Settle(context.Background(), db, decisionstore.SettleInput{ID: id, Approved: false, Reviewer: "b", ReviewerAnswer: "x"}); err != nil {
		t.Fatal(err)
	}
	var status, reviewer string
	if err := db.QueryRow(`SELECT status, reviewer FROM decisions WHERE id = ?`, id).Scan(&status, &reviewer); err != nil {
		t.Fatal(err)
	}
	if status != decision.StatusApproved || reviewer != "a" {
		t.Fatalf("the second settle must be a no-op: %q by %q", status, reviewer)
	}
}

func TestOutcomeWritesOnceKnown(t *testing.T) {
	db := open(t)
	id, err := decisionstore.Propose(context.Background(), db, decisionstore.ProposeInput{
		Site: decision.SiteBash, Scope: "proj", Question: decision.Choice("risk", "risk?", "safe", "changes", "dangerous"),
		Answer: "safe", Confidence: 0.5, Decider: "laya",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := decisionstore.Outcome(context.Background(), db, id, "held"); err != nil {
		t.Fatal(err)
	}
	var outcome string
	if err := db.QueryRow(`SELECT outcome FROM decisions WHERE id = ?`, id).Scan(&outcome); err != nil {
		t.Fatal(err)
	}
	if outcome != "held" {
		t.Fatalf("outcome %q", outcome)
	}
}

func TestTheRecorderLandsTheRowAndSwallowsAStoreError(t *testing.T) {
	db := open(t)
	rec := decisionstore.Recorder{DB: db, Scope: "proj", Log: func(string) {}}
	rec.Record(context.Background(), decision.Final{
		Site:     decision.SiteGuard,
		State:    `{"tool":"edit"}`,
		Question: decision.YesNo("retry", "issue the identical failing call again?"),
		Answer:   "no",
		Decider:  decision.SiteGuard,
	})
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM decisions`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("the recorder lands the row, got %d", count)
	}
	ctx := core.WithSession(context.Background(), &core.Session{ID: "s1"})
	rec.Record(ctx, decision.Final{Site: decision.SiteGuard, Question: decision.YesNo("retry", "again?"), Answer: "no", Decider: decision.SiteGuard})
	var session *string
	if err := db.QueryRow(`SELECT session FROM decisions WHERE id = 2`).Scan(&session); err != nil {
		t.Fatal(err)
	}
	if session == nil || *session != "s1" {
		t.Fatalf("the recorder names the ctx session, got %v", session)
	}

	closed, _, _, err := store.Open(filepath.Join(t.TempDir(), "dead.sqlite"), decisionstore.Statements(), decisionstore.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	if err := closed.Close(); err != nil {
		t.Fatal(err)
	}
	said := ""
	dead := decisionstore.Recorder{DB: closed, Log: func(m string) { said = m }}
	dead.Record(context.Background(), decision.Final{Site: decision.SiteGuard, Question: decision.YesNo("retry", "again?"), Answer: "no", Decider: decision.SiteGuard})
	if !strings.Contains(said, "decision") {
		t.Fatalf("the swallow is loud when a log is wired: %q", said)
	}
}
