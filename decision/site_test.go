package decision_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/decision"
	decisionstore "github.com/mrsirg97-rgb/rig/v2/store/decision"
)

func TestABashCallProposesPendingAndTheReplyIsUnchanged(t *testing.T) {
	db := openDecisionStore(t)
	sink := &storeSink{db: db, written: make(chan decision.Answer, 1)}
	var dec fakeDecider
	dec.answers = []decision.Answer{{Question: "risk", Value: "dangerous", Confidence: 0.33, Decider: "laya"}}
	q := decision.NewQueue(&dec, sink, storeSettled{db: db}, decisionstore.Recorder{DB: db, Scope: "proj"}, func() {}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go q.Run(ctx)

	var exec core.ToolExec = func(ctx context.Context, call core.ToolCall) (string, error) {
		return "total 0\n", nil
	}
	site := decision.Site(q).Wrap(exec)
	got, err := site(core.WithSession(context.Background(), &core.Session{ID: "s1"}), core.ToolCall{
		ID: "c1", Name: "bash", Args: json.RawMessage(`{"command":"rm -rf /tmp/x"}`),
	})
	if err != nil || got != "total 0\n" {
		t.Fatalf("the call's reply is unchanged: (%q, %v)", got, err)
	}
	<-sink.written

	rows, err := decisionstore.Pending(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("one pending proposal per bash call, got %d", len(rows))
	}
	row := rows[0]
	if row.Site != decision.SiteBash || row.Answer != "dangerous" || row.Session != "s1" {
		t.Fatalf("the row carries the proposal: %+v", row)
	}
	if row.Question.Kind != decision.KindChoice || len(row.Question.Choices) != 3 {
		t.Fatalf("the risk question is a choice of three: %+v", row.Question)
	}
	for _, label := range []string{"safe", "changes", "dangerous"} {
		if row.Question.Description[label] == "" {
			t.Fatalf("the risk question describes %s: %+v", label, row.Question.Description)
		}
	}
	if !strings.Contains(row.State, "rm -rf /tmp/x") {
		t.Fatalf("the state carries the command: %q", row.State)
	}
}

func TestOnlyBashProposes(t *testing.T) {
	db := openDecisionStore(t)
	sink := &storeSink{db: db, written: make(chan decision.Answer, 1)}
	q := decision.NewQueue(&fakeDecider{}, sink, storeSettled{db: db}, decisionstore.Recorder{DB: db, Scope: "proj"}, func() {}, nil)
	var exec core.ToolExec = func(ctx context.Context, call core.ToolCall) (string, error) {
		return "read", nil
	}
	site := decision.Site(q).Wrap(exec)
	if _, err := site(context.Background(), core.ToolCall{ID: "c1", Name: "read", Args: json.RawMessage(`{"path":"a"}`)}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-sink.written:
		t.Fatal("a read proposes nothing")
	default:
	}
}

func TestAProposalCarriesTheNormalizedCommand(t *testing.T) {
	db := openDecisionStore(t)
	sink := &storeSink{db: db, written: make(chan decision.Answer, 1)}
	var dec fakeDecider
	dec.answers = []decision.Answer{{Question: "risk", Value: "safe", Confidence: 0.5, Decider: "laya"}}
	q := decision.NewQueue(&dec, sink, storeSettled{db: db}, decisionstore.Recorder{DB: db, Scope: "proj"}, func() {}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go q.Run(ctx)

	var exec core.ToolExec = func(ctx context.Context, call core.ToolCall) (string, error) {
		return "total 0\n", nil
	}
	site := decision.Site(q).Wrap(exec)
	if _, err := site(context.Background(), core.ToolCall{
		ID: "c1", Name: "bash", Args: json.RawMessage(`{"command":"cd  /tmp  &&  rm  -rf  x"}`),
	}); err != nil {
		t.Fatal(err)
	}
	<-sink.written

	rows, err := decisionstore.Pending(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("one pending proposal, got %d", len(rows))
	}
	var state struct {
		Command   string `json:"command"`
		Workspace string `json:"workspace"`
	}
	if err := json.Unmarshal([]byte(rows[0].State), &state); err != nil {
		t.Fatal(err)
	}
	if state.Command != "rm -rf x" {
		t.Fatalf("the state carries the normalized command: %q", state.Command)
	}
}

func TestTheProposerNeverBlocksTheCall(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	db := openDecisionStore(t)
	q := decision.NewQueue(&fakeDecider{}, blockingSink{block: block}, storeSettled{db: db}, decisionstore.Recorder{DB: db, Scope: "proj"}, func() {}, nil)
	var exec core.ToolExec = func(ctx context.Context, call core.ToolCall) (string, error) {
		return "ran", nil
	}
	site := decision.Site(q).Wrap(exec)
	got, err := site(context.Background(), core.ToolCall{ID: "c1", Name: "bash", Args: json.RawMessage(`{"command":"ls"}`)})
	if err != nil || got != "ran" {
		t.Fatalf("the call never waits on the proposal: (%q, %v)", got, err)
	}
}
