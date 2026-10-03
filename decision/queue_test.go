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

func openDecisionStore(t *testing.T) store.DB {
	t.Helper()
	db, _, _, err := store.Open(filepath.Join(t.TempDir(), "decision.sqlite"), decisionstore.Statements(), decisionstore.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestAProposalLandsPending(t *testing.T) {
	db := openDecisionStore(t)
	sink := &storeSink{db: db, written: make(chan decision.Answer, 1)}
	var dec fakeDecider
	dec.answers = []decision.Answer{{Question: "risk", Value: "safe", Confidence: 0.71, Decider: "laya"}}
	q := decision.NewQueue(&dec, sink, func(string) {})
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
	q := decision.NewQueue(dec, sink, func(string) {})
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
	loud := make(chan string, 1)
	dec := &fakeDecider{err: pastDeadline()}
	q := decision.NewQueue(dec, sink, func(m string) { loud <- m })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go q.Run(ctx)

	q.Propose(decision.Pending{Site: decision.SiteBash, Scope: "proj", Question: decision.YesNo("ok", "ok?")})
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
	loud := make(chan string, 1)
	var dec fakeDecider
	dec.answers = []decision.Answer{{Question: "ok", Value: "yes", Confidence: 0.5, Decider: "laya"}}
	q := decision.NewQueue(&dec, brokenSink{}, func(m string) { loud <- m })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go q.Run(ctx)

	q.Propose(decision.Pending{Site: decision.SiteBash, Scope: "proj", Question: decision.YesNo("ok", "ok?")})
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
	loud := make(chan string, 1)
	blocking := make(chan struct{})
	q := decision.NewQueue(&fakeDecider{}, blockingSink{block: blocking}, func(m string) { loud <- m })
	for i := 0; i < decision.QueueCap; i++ {
		q.Propose(decision.Pending{Site: decision.SiteBash, Scope: "proj", Question: decision.YesNo("ok", "ok?")})
	}
	q.Propose(decision.Pending{Site: decision.SiteBash, Scope: "proj", Question: decision.YesNo("ok", "ok?")})
	select {
	case m := <-loud:
		if !strings.Contains(m, "queue full") {
			t.Fatalf("the drop names the queue: %q", m)
		}
	default:
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

func pastDeadline() error {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Hour))
	defer cancel()
	return ctx.Err()
}
