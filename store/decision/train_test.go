package decision_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/decision"
	decisionstore "github.com/mrsirg97-rgb/rig/v2/store/decision"
)

func TestGoldReadsTheSettledRows(t *testing.T) {
	db := open(t)
	ctx := core.WithSession(context.Background(), &core.Session{ID: "s1"})
	settle := func(state, answer, correction string, approved bool) {
		t.Helper()
		id, err := decisionstore.Propose(ctx, db, decisionstore.ProposeInput{
			Scope: "proj", Site: decision.SiteBash, State: state,
			Question: decision.Choice("risk", "risk?"), Answer: answer, Confidence: 0.5, Decider: "server",
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := decisionstore.Settle(ctx, db, decisionstore.SettleInput{ID: id, Approved: approved, Reviewer: "reviewer", ReviewerAnswer: correction}); err != nil {
			t.Fatal(err)
		}
	}
	settle(`{"command":"ls"}`, "safe", "", true)
	settle(`{"command":"rm"}`, "changes", "the label is dangerous", false)
	if _, err := decisionstore.Propose(ctx, db, decisionstore.ProposeInput{
		Scope: "proj", Site: decision.SiteBash, State: `{"command":"x"}`,
		Question: decision.Choice("risk", "risk?"), Answer: "safe", Confidence: 0.5, Decider: "server",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := decisionstore.RecordFinal(ctx, db, decisionstore.FinalInput{
		Scope: "proj", Site: decision.SiteGuard, State: "{}",
		Question: decision.Binary("bound", "over the bound?"), Answer: "yes", Decider: "rule",
	}); err != nil {
		t.Fatal(err)
	}
	rows, err := decisionstore.Gold(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("gold reads the settled rows only: %d", len(rows))
	}
	if rows[0].Status != decision.StatusApproved || rows[0].Answer != "safe" {
		t.Fatalf("an approved row carries its answer: %+v", rows[0])
	}
	if rows[1].Status != decision.StatusDenied || rows[1].Corrected == nil || *rows[1].Corrected != "the label is dangerous" {
		t.Fatalf("a denied row carries its correction: %+v", rows[1])
	}
	if rows[0].Question.Kind != decision.KindChoice || rows[0].Question.ID != "risk" {
		t.Fatalf("the question rides the row: %+v", rows[0].Question)
	}
}

func TestGoldReadsTheLegacyYesnoKindAsBinary(t *testing.T) {
	db := open(t)
	ctx := core.WithSession(context.Background(), &core.Session{ID: "s1"})
	_, err := db.Exec(`INSERT INTO "decisions" ("id", "scope", "site", "state", "question", "answer", "confidence", "decider", "session", "status", "unsure", "reviewer", "reviewer_answer", "outcome", "ts")
		VALUES (1, 'proj', 'pack', '{}', '{"id":"matter","kind":"yesno","prompt":"matters?"}', 'yes', 0.5, 'server', NULL, 'approved', 0, NULL, NULL, NULL, '2026-10-06T00:00:00Z')`)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := decisionstore.Gold(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Question.Kind != decision.KindBinary {
		t.Fatalf("the legacy kind reads as binary: %+v", rows)
	}
}

func TestATrainingRowRoundTrips(t *testing.T) {
	db := open(t)
	ctx := context.Background()
	id, err := decisionstore.RecordTraining(ctx, db, decisionstore.TrainingInput{
		Scope: "proj", Trainer: "laya", Rows: 210, Skipped: 2, TrainRows: 167, HeldRows: 43,
		RunDir: "/h/.rig/decision/train/20261006-1658", Checkpoint: "/h/laya/ft-rig",
		Constant: `{"questions":{}}`, Candidate: `{"questions":{}}`, Promoted: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var trainer string
	var rows, skipped, trainRows, heldRows int64
	var incumbent *string
	var promoted bool
	var ts string
	if err := db.QueryRow(`SELECT "trainer", "rows", "skipped", "train_rows", "held_rows", "incumbent", "promoted", "ts" FROM "trainings" WHERE "id" = $1`, id).
		Scan(&trainer, &rows, &skipped, &trainRows, &heldRows, &incumbent, &promoted, &ts); err != nil {
		t.Fatal(err)
	}
	if trainer != "laya" || rows != 210 || skipped != 2 || trainRows != 167 || heldRows != 43 {
		t.Fatalf("the run record: %s %d %d %d %d", trainer, rows, skipped, trainRows, heldRows)
	}
	if incumbent != nil {
		t.Fatalf("no unit named, no incumbent report: %v", *incumbent)
	}
	if !promoted {
		t.Fatal("the promoted word rides the row")
	}
	if ts == "" {
		t.Fatal("the row carries its start")
	}
	second, err := decisionstore.RecordTraining(ctx, db, decisionstore.TrainingInput{
		Scope: "proj", Trainer: "laya", Rows: 1, RunDir: "/x", Checkpoint: "/c",
		Constant: "{}", Incumbent: `{"questions":{}}`, Candidate: "{}", Promoted: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if second != id+1 {
		t.Fatalf("ids mint forward: %d", second)
	}
	var incumbent2 *string
	if err := db.QueryRow(`SELECT "incumbent" FROM "trainings" WHERE "id" = $1`, second).Scan(&incumbent2); err != nil {
		t.Fatal(err)
	}
	if incumbent2 == nil || !strings.Contains(*incumbent2, "questions") {
		t.Fatalf("the incumbent report rides the row: %v", incumbent2)
	}
	if _, err := decisionstore.RecordTraining(ctx, db, decisionstore.TrainingInput{Rows: 1}); err == nil || !strings.Contains(err.Error(), "no trainer") {
		t.Fatalf("a run without a trainer refuses: %v", err)
	}
}
