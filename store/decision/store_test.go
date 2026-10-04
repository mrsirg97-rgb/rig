package decision_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/v2/broadcast"
	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/decision"
	"github.com/mrsirg97-rgb/rig/v2/evt"
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
		Question: decision.Binary("run", "run this call?"),
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

func settleTwin(t *testing.T, db store.DB, state, answer string, approved bool, correction string) {
	t.Helper()
	id, err := decisionstore.Propose(context.Background(), db, decisionstore.ProposeInput{
		Scope: "proj", Site: decision.SiteBash, State: state,
		Question:   decision.Choice("risk", "What risk does this bash call carry?", "safe", "changes", "dangerous"),
		Answer:     answer,
		Confidence: 0.5,
		Decider:    "laya",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := decisionstore.Settle(context.Background(), db, decisionstore.SettleInput{
		ID: id, Approved: approved, Reviewer: "reviewer", ReviewerAnswer: correction,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSettledAnswersNoneWhenNothingSettledFits(t *testing.T) {
	db := open(t)
	settleTwin(t, db, `{"command":"ls"}`, "changes", true, "")
	q := decision.Choice("risk", "What risk does this bash call carry?", "safe", "changes", "dangerous")
	for _, c := range []struct {
		name  string
		site  string
		state string
		q     decision.Question
	}{
		{"no row at all", decision.SiteBash, `{"command":"rm -rf x"}`, q},
		{"another site", decision.SitePack, `{"command":"ls"}`, q},
		{"another question", decision.SiteBash, `{"command":"ls"}`, decision.Choice("other", "What risk does this bash call carry?", "safe", "changes", "dangerous")},
	} {
		got, ok, err := decisionstore.Settled(context.Background(), db, c.site, c.q, c.state)
		if err != nil || ok || got != "" {
			t.Fatalf("%s: the read answers none, got %q ok=%v err=%v", c.name, got, ok, err)
		}
	}
}

func TestSettledAnswersAnApprovedRow(t *testing.T) {
	db := open(t)
	settleTwin(t, db, `{"command":"ls"}`, "changes", true, "")
	got, ok, err := decisionstore.Settled(context.Background(), db, decision.SiteBash,
		decision.Choice("risk", "What risk does this bash call carry?", "safe", "changes", "dangerous"), `{"command":"ls"}`)
	if err != nil || !ok {
		t.Fatalf("the approved twin answers: ok=%v err=%v", ok, err)
	}
	if got != "changes" {
		t.Fatalf("the approved twin answers its row: %q", got)
	}
}

func TestSettledAnswersADeniedRowWithItsCorrection(t *testing.T) {
	db := open(t)
	settleTwin(t, db, `{"command":"ls"}`, "safe", false, "dangerous")
	got, ok, err := decisionstore.Settled(context.Background(), db, decision.SiteBash,
		decision.Choice("risk", "What risk does this bash call carry?", "safe", "changes", "dangerous"), `{"command":"ls"}`)
	if err != nil || !ok {
		t.Fatalf("the denied twin answers: ok=%v err=%v", ok, err)
	}
	if got != "dangerous" {
		t.Fatalf("the denied twin answers its correction: %q", got)
	}
}

func TestSettledAnswersTheMostRecentTwin(t *testing.T) {
	db := open(t)
	q := decision.Choice("risk", "What risk does this bash call carry?", "safe", "changes", "dangerous")
	settleTwin(t, db, `{"command":"ls"}`, "changes", true, "")
	settleTwin(t, db, `{"command":"ls"}`, "safe", false, "dangerous")
	got, ok, err := decisionstore.Settled(context.Background(), db, decision.SiteBash, q, `{"command":"ls"}`)
	if err != nil || !ok || got != "dangerous" {
		t.Fatalf("the newest twin answers: %q ok=%v err=%v", got, ok, err)
	}
	settleTwin(t, db, `{"command":"ls"}`, "safe", true, "")
	got, ok, err = decisionstore.Settled(context.Background(), db, decision.SiteBash, q, `{"command":"ls"}`)
	if err != nil || !ok || got != "safe" {
		t.Fatalf("a newer twin still answers: %q ok=%v err=%v", got, ok, err)
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
	rec := decisionstore.Recorder{DB: db, Scope: "proj"}
	rec.Record(context.Background(), decision.Final{
		Site:     decision.SiteGuard,
		State:    `{"tool":"edit"}`,
		Question: decision.Binary("retry", "issue the identical failing call again?"),
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
	rec.Record(ctx, decision.Final{Site: decision.SiteGuard, Question: decision.Binary("retry", "again?"), Answer: "no", Decider: decision.SiteGuard})
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
	v, said := voice(t)
	dead := decisionstore.Recorder{DB: closed, Voice: v}
	dead.Record(context.Background(), decision.Final{Site: decision.SiteGuard, Question: decision.Binary("retry", "again?"), Answer: "no", Decider: decision.SiteGuard})
	select {
	case m := <-said:
		if !strings.HasPrefix(m, "decision: record") {
			t.Fatalf("the swallow is loud in the room when a voice is wired: %q", m)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the swallow is loud in the room when a voice is wired")
	}
}

func TestAFinalRowKeepsItsConfidenceAndUnsure(t *testing.T) {
	db := open(t)
	ctx := core.WithSession(context.Background(), &core.Session{ID: "s1"})
	conf := 0.42
	_, err := decisionstore.RecordFinal(ctx, db, decisionstore.FinalInput{
		Scope: "proj", Site: decision.SiteDecide, State: `{"item":"one"}`,
		Question: decision.Choice("item", "risk?", "safe", "changes"), Answer: "safe",
		Confidence: &conf, Unsure: true, Decider: "laya",
	})
	if err != nil {
		t.Fatal(err)
	}
	var answer string
	var confidence float64
	var unsure bool
	var status string
	if err := db.QueryRow(`SELECT answer, confidence, unsure, status FROM decisions WHERE site = 'decide'`).Scan(&answer, &confidence, &unsure, &status); err != nil {
		t.Fatal(err)
	}
	if answer != "safe" || confidence != 0.42 || !unsure || status != decision.StatusFinal {
		t.Fatalf("the decide row keeps its answer, confidence, unsure and status: %q %g %v %q", answer, confidence, unsure, status)
	}
}

func TestAFinalRuleKeepsNullConfidenceAndZeroUnsure(t *testing.T) {
	db := open(t)
	_, err := decisionstore.RecordFinal(context.Background(), db, decisionstore.FinalInput{
		Scope: "proj", Site: decision.SitePerm, State: "{}",
		Question: decision.Binary("allow", "allow bash?"), Answer: "no", Decider: decision.SitePerm,
	})
	if err != nil {
		t.Fatal(err)
	}
	var confidence *float64
	var unsure bool
	if err := db.QueryRow(`SELECT confidence, unsure FROM decisions WHERE site = 'perm'`).Scan(&confidence, &unsure); err != nil {
		t.Fatal(err)
	}
	if confidence != nil || unsure {
		t.Fatalf("a rule does not estimate: confidence %v unsure %v", confidence, unsure)
	}
}

func TestAnOutOfRangeConfidenceRefuses(t *testing.T) {
	db := open(t)
	conf := 1.5
	_, err := decisionstore.RecordFinal(context.Background(), db, decisionstore.FinalInput{
		Scope: "proj", Site: decision.SiteDecide, State: "{}",
		Question: decision.Choice("item", "risk?", "safe", "changes"), Answer: "safe",
		Confidence: &conf, Decider: "laya",
	})
	if err == nil || !strings.Contains(err.Error(), "probability") {
		t.Fatalf("a confidence outside 0..1 refuses: %v", err)
	}
}

func TestTheMigrationAddsUnsureToAVersionOneFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "decision.sqlite")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	v1 := `CREATE TABLE IF NOT EXISTS "decisions" (
  "id" INTEGER NOT NULL,
  "answer" TEXT NOT NULL,
  "confidence" REAL,
  "decider" TEXT NOT NULL,
  "outcome" TEXT,
  "question" TEXT NOT NULL,
  "reviewer" TEXT,
  "reviewer_answer" TEXT,
  "scope" TEXT NOT NULL,
  "session" TEXT,
  "site" TEXT NOT NULL,
  "state" TEXT NOT NULL,
  "status" TEXT NOT NULL,
  "ts" TEXT NOT NULL,
  PRIMARY KEY ("id")
)`
	for _, stmt := range []string{v1, `CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT)`, `INSERT INTO meta (key, value) VALUES ('schema_version', '1')`, `INSERT INTO decisions (id, scope, site, state, question, answer, decider, status, ts) VALUES (1, 'proj', 'bash', '{}', '{}', 'safe', 'rule', 'final', '2026-01-01')`} {
		if _, err := raw.Exec(stmt); err != nil {
			t.Fatalf("v1 seed: %v", err)
		}
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	db, _, _, err := store.Open(path, decisionstore.Statements(), decisionstore.SchemaVersion, decisionstore.Migration())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version string
	if err := db.QueryRow(`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != strconv.Itoa(decisionstore.SchemaVersion) {
		t.Fatalf("the version moved to %s, want %d", version, decisionstore.SchemaVersion)
	}
	var unsure bool
	var confidence *float64
	if err := db.QueryRow(`SELECT unsure, confidence FROM decisions WHERE id = 1`).Scan(&unsure, &confidence); err != nil {
		t.Fatalf("the unsure column must exist and read the old row: %v", err)
	}
	if unsure || confidence != nil {
		t.Fatalf("the pre-migration row is a rule: unsure %v confidence %v", unsure, confidence)
	}
}

func voice(t *testing.T) (broadcast.Member, <-chan string) {
	t.Helper()
	engine := evt.NewEngine()
	go engine.Start(context.Background())
	t.Cleanup(engine.Stop)
	room := broadcast.NewRoom("test", func(id int64) broadcast.Transport {
		return broadcast.NewLoopTransport(id, engine, 0)
	})
	said := make(chan string, 8)
	room.Add(-1).Subscribe(context.Background(), func(err error, messages ...broadcast.Message) {
		for _, m := range messages {
			if n, ok := m.Event().(core.Notice); err == nil && ok {
				said <- n.Source + ": " + n.Text
			}
		}
	})
	return room.Add(0), said
}

func TestAStoredYesnoRowReadsAsABinary(t *testing.T) {
	db := open(t)
	if _, err := db.Exec(`INSERT INTO decisions (scope, site, state, question, answer, status, decider, ts, unsure) VALUES (?, ?, ?, ?, ?, 'pending', 'laya', '2026-10-04T00:00:00Z', 0)`,
		"proj", decision.SiteBash, `{"command":"ls"}`, `{"id":"ok","kind":"yesno","prompt":"ok?"}`, "yes"); err != nil {
		t.Fatal(err)
	}
	rows, err := decisionstore.Pending(context.Background(), db)
	if err != nil || len(rows) != 1 {
		t.Fatalf("one pending row: %d %v", len(rows), err)
	}
	if rows[0].Question.Kind != decision.KindBinary {
		t.Fatalf("a row written as yesno reads as %q, want binary", rows[0].Question.Kind)
	}
}
