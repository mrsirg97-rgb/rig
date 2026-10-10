package decision_test

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/v2/decision"
	"github.com/mrsirg97-rgb/rig/v2/store"
	decisionstore "github.com/mrsirg97-rgb/rig/v2/store/decision"
	"github.com/mrsirg97-rgb/rig/v2/testenv"
)

type fakeDecider struct {
	answers []decision.Answer
	err     error
}

func (f *fakeDecider) Decide(ctx context.Context, state string, questions []decision.Question) ([]decision.Answer, error) {
	return f.answers, f.err
}

type storeSink struct {
	db      store.DB
	written chan decision.Answer
	mu      sync.Mutex
	seen    []decision.Pending
}

func (s *storeSink) ProposePending(ctx context.Context, p decision.Pending, a decision.Answer) error {
	s.mu.Lock()
	s.seen = append(s.seen, p)
	s.mu.Unlock()
	scope := p.Scope
	if scope == "" {
		scope = "proj"
	}
	_, err := decisionstore.Propose(ctx, s.db, decisionstore.ProposeInput{
		Scope: scope, Session: p.Session, Site: p.Site, State: p.State,
		Question: p.Question, Answer: a.Value, Confidence: a.Confidence, Decider: a.Decider,
	})
	if err == nil {
		s.written <- a
	}
	return err
}

type storeSettled struct{ db store.DB }

func (s storeSettled) Settled(ctx context.Context, site string, q decision.Question, state string) (string, bool, error) {
	return decisionstore.Settled(ctx, s.db, site, q, state)
}

type blockingDecider struct{ block chan struct{} }

func (d *blockingDecider) Decide(ctx context.Context, state string, questions []decision.Question) ([]decision.Answer, error) {
	<-d.block
	return nil, pastDeadline()
}

type brokenSettled struct{}

func (brokenSettled) Settled(ctx context.Context, site string, q decision.Question, state string) (string, bool, error) {
	return "", false, pastDeadline()
}

func openDecisionStore(t *testing.T) store.DB {
	t.Helper()
	db, _, _, err := store.Open(filepath.Join(t.TempDir(), "decision.sqlite"), decisionstore.Statements(), decisionstore.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func waitRows(t *testing.T, db store.DB, status string, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM decisions WHERE status = ?`, status).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("the rows never settled to %d %s", want, status)
}

func settleTwinQueue(t *testing.T, db store.DB, state, answer string, approved bool, correction string) {
	t.Helper()
	id, err := decisionstore.Propose(context.Background(), db, decisionstore.ProposeInput{
		Scope: "proj", Site: decision.SiteBash, State: state,
		Question:   decision.Choice("risk", "What risk does this bash call carry?", "safe", "changes", "dangerous"),
		Answer:     answer,
		Confidence: 0.9,
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

func TestAProposalLandsPendingAndMarksTheReviewerDirty(t *testing.T) {
	db := openDecisionStore(t)
	sink := &storeSink{db: db, written: make(chan decision.Answer, 1)}
	woken := make(chan struct{}, 1)
	var dec fakeDecider
	dec.answers = []decision.Answer{{Question: "risk", Value: "safe", Confidence: 0.71, Decider: "laya"}}
	q := decision.NewQueue(&dec, sink, storeSettled{db: db}, decisionstore.Recorder{DB: db, Scope: "proj"}, func() { woken <- struct{}{} }, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go q.Run(ctx)

	q.Propose(decision.Pending{
		Site:     decision.SiteBash,
		Scope:    "proj",
		State:    `{"command":"ls"}`,
		Question: decision.Choice("risk", "What risk does this bash call carry?", "safe", "changes", "dangerous"),
	})
	<-sink.written
	<-woken

	rows, err := decisionstore.Pending(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("the proposal is pending: %d rows", len(rows))
	}
	row := rows[0]
	if row.Site != decision.SiteBash || row.Answer != "safe" || row.Decider != "laya" {
		t.Fatalf("the row carries the proposal: %+v", row)
	}
	if row.Confidence == nil || *row.Confidence != 0.71 {
		t.Fatalf("the confidence rides the row: %v", row.Confidence)
	}
}

func TestAReplyWithProbabilitiesStoresTheMassOnTheRow(t *testing.T) {
	db := openDecisionStore(t)
	sink := &storeSink{db: db, written: make(chan decision.Answer, 1)}
	srv := testenv.Server(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"answers": {"risk": {"type": "choice", "choice": "safe",
			"probabilities": {"safe": 0.71, "changes": 0.2, "dangerous": 0.09}}}}`))
	}))
	dec, err := decision.NewHTTP(decision.HTTPOptions{URL: srv.URL, Client: testClient(), Decider: "laya"})
	if err != nil {
		t.Fatal(err)
	}
	q := decision.NewQueue(dec, sink, storeSettled{db: db}, decisionstore.Recorder{DB: db, Scope: "proj"}, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go q.Run(ctx)

	q.Propose(decision.Pending{
		Site:     decision.SiteBash,
		Scope:    "proj",
		State:    `{"command":"ls"}`,
		Question: decision.Choice("risk", "What risk does this bash call carry?", "safe", "changes", "dangerous"),
	})
	<-sink.written

	rows, err := decisionstore.Pending(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("the proposal is pending: %d rows", len(rows))
	}
	if rows[0].Answer != "safe" || rows[0].Confidence == nil || *rows[0].Confidence != 0.71 {
		t.Fatalf("the row keeps the mass on the chosen label, no answer_confidence in the reply: %+v", rows[0])
	}
}

func TestADeciderErrorDropsLoudlyAndLandsNothing(t *testing.T) {
	db := openDecisionStore(t)
	sink := &storeSink{db: db, written: make(chan decision.Answer, 1)}
	v, loud := voice(t)
	dec := &fakeDecider{err: pastDeadline()}
	q := decision.NewQueue(dec, sink, storeSettled{db: db}, decisionstore.Recorder{DB: db, Scope: "proj"}, func() {}, v)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go q.Run(ctx)

	q.Propose(decision.Pending{Site: decision.SiteBash, Scope: "proj", Question: decision.Binary("ok", "ok?")})
	select {
	case m := <-loud:
		if !strings.Contains(m, "decide") {
			t.Fatalf("the drop is loud: %q", m)
		}
	case <-sink.written:
		t.Fatal("an errored decide lands nothing")
	}
	rows, _ := decisionstore.Pending(context.Background(), db)
	if len(rows) != 0 {
		t.Fatalf("an errored decide lands nothing: %d rows", len(rows))
	}
}

func TestAProposerErrorDropsLoudly(t *testing.T) {
	closed, _, _, err := store.Open(filepath.Join(t.TempDir(), "dead.sqlite"), decisionstore.Statements(), decisionstore.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	if err := closed.Close(); err != nil {
		t.Fatal(err)
	}
	v, loud := voice(t)
	var dec fakeDecider
	dec.answers = []decision.Answer{{Question: "ok", Value: "yes", Confidence: 0.5, Decider: "laya"}}
	livedb := openDecisionStore(t)
	q := decision.NewQueue(&dec, brokenSink{}, storeSettled{db: livedb}, decisionstore.Recorder{DB: livedb, Scope: "proj"}, func() {}, v)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go q.Run(ctx)

	q.Propose(decision.Pending{Site: decision.SiteBash, Scope: "proj", Question: decision.Binary("ok", "ok?")})
	select {
	case m := <-loud:
		if !strings.Contains(m, "propose") {
			t.Fatalf("the drop is loud: %q", m)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the sink error never surfaced")
	}
}

func TestAFullQueueDropsLoudlyWithoutBlocking(t *testing.T) {
	v, loud := voice(t)
	blocking := make(chan struct{})
	db := openDecisionStore(t)
	q := decision.NewQueue(&fakeDecider{}, blockingSink{block: blocking}, storeSettled{db: db}, decisionstore.Recorder{DB: db, Scope: "proj"}, func() {}, v)
	for i := 0; i < decision.QueueCap; i++ {
		q.Propose(decision.Pending{Site: decision.SiteBash, Scope: "proj", Question: decision.Binary("ok", "ok?")})
	}
	q.Propose(decision.Pending{Site: decision.SiteBash, Scope: "proj", Question: decision.Binary("ok", "ok?")})
	select {
	case m := <-loud:
		if !strings.Contains(m, "queue full") {
			t.Fatalf("the drop names the queue: %q", m)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a full queue drops loudly")
	}
}

type blockingSink struct{ block chan struct{} }

func (b blockingSink) ProposePending(ctx context.Context, p decision.Pending, a decision.Answer) error {
	<-b.block
	return nil
}

type brokenSink struct{}

func (brokenSink) ProposePending(ctx context.Context, p decision.Pending, a decision.Answer) error {
	return errBroken
}

var errBroken = pastDeadline()

func TestASettledTwinSkipsTheDeciderAndLandsFinal(t *testing.T) {
	db := openDecisionStore(t)
	settleTwinQueue(t, db, `{"command":"ls"}`, "safe", false, "dangerous")
	sink := &storeSink{db: db, written: make(chan decision.Answer, 1)}
	block := make(chan struct{})
	defer close(block)
	q := decision.NewQueue(&blockingDecider{block: block}, sink, storeSettled{db: db},
		decisionstore.Recorder{DB: db, Scope: "proj"}, func() {}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go q.Run(ctx)

	q.Propose(decision.Pending{
		Site:     decision.SiteBash,
		Scope:    "proj",
		State:    `{"command":"ls"}`,
		Question: decision.Choice("risk", "What risk does this bash call carry?", "safe", "changes", "dangerous"),
	})
	waitRows(t, db, "final", 1)
	var answer, decider string
	var conf *float64
	if err := db.QueryRow(`SELECT answer, confidence, decider FROM decisions WHERE status = 'final'`).Scan(&answer, &conf, &decider); err != nil {
		t.Fatal(err)
	}
	if answer != "dangerous" || decider != "reviewed" || conf == nil || *conf != 1 {
		t.Fatalf("the twin lands the store's answer at certainty: %q %q %v", answer, decider, conf)
	}
	rows, err := decisionstore.Pending(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("a settled twin proposes nothing: %d rows", len(rows))
	}
}

func TestANovelStateStillProposes(t *testing.T) {
	db := openDecisionStore(t)
	settleTwinQueue(t, db, `{"command":"ls"}`, "changes", true, "")
	sink := &storeSink{db: db, written: make(chan decision.Answer, 1)}
	var dec fakeDecider
	dec.answers = []decision.Answer{{Question: "risk", Value: "dangerous", Confidence: 0.8, Decider: "laya"}}
	q := decision.NewQueue(&dec, sink, storeSettled{db: db}, decisionstore.Recorder{DB: db, Scope: "proj"}, func() {}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go q.Run(ctx)

	q.Propose(decision.Pending{
		Site:     decision.SiteBash,
		Scope:    "proj",
		State:    `{"command":"rm -rf x"}`,
		Question: decision.Choice("risk", "What risk does this bash call carry?", "safe", "changes", "dangerous"),
	})
	<-sink.written
	rows, err := decisionstore.Pending(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].State != `{"command":"rm -rf x"}` || rows[0].Answer != "dangerous" {
		t.Fatalf("a novel state proposes through the decider: %+v", rows)
	}
}

func TestASettledLookupErrorFallsThroughToTheDecider(t *testing.T) {
	db := openDecisionStore(t)
	sink := &storeSink{db: db, written: make(chan decision.Answer, 1)}
	v, loud := voice(t)
	var dec fakeDecider
	dec.answers = []decision.Answer{{Question: "risk", Value: "safe", Confidence: 0.7, Decider: "laya"}}
	q := decision.NewQueue(&dec, sink, brokenSettled{}, decisionstore.Recorder{DB: db, Scope: "proj"}, func() {}, v)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go q.Run(ctx)

	q.Propose(decision.Pending{
		Site:     decision.SiteBash,
		Scope:    "proj",
		State:    `{"command":"ls"}`,
		Question: decision.Choice("risk", "What risk does this bash call carry?", "safe", "changes", "dangerous"),
	})
	select {
	case m := <-loud:
		if !strings.Contains(m, "settled") {
			t.Fatalf("the read error says itself: %q", m)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the read error never surfaced")
	}
	<-sink.written
	rows, err := decisionstore.Pending(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Answer != "safe" {
		t.Fatalf("a failed read still proposes: %+v", rows)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM decisions WHERE status = 'final'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("a failed read lands no final")
	}
}

func pastDeadline() error {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Hour))
	defer cancel()
	return ctx.Err()
}
