package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/decision"
	"github.com/mrsirg97-rgb/rig/v2/plugins"
	"github.com/mrsirg97-rgb/rig/v2/store"
	decisionstore "github.com/mrsirg97-rgb/rig/v2/store/decision"
	"github.com/mrsirg97-rgb/rig/v2/store/graph"
)

type fakeDecider struct {
	reply func(state string) decision.Answer
}

func (f fakeDecider) Decide(ctx context.Context, state string, questions []decision.Question) ([]decision.Answer, error) {
	if f.reply == nil {
		return []decision.Answer{{Question: "item", Value: "safe", Confidence: 0.9, Decider: "fake"}}, nil
	}
	return []decision.Answer{f.reply(state)}, nil
}

func wiredDecide(t *testing.T, rec decision.Recorder, reply func(state string) decision.Answer) *decision.Decide {
	t.Helper()
	d, err := decision.NewDecide(decision.DecideOptions{Decider: fakeDecider{reply: reply}, Recorder: rec, Parallel: 1})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func decideArgs(items ...string) json.RawMessage {
	b, err := json.Marshal(map[string]any{
		"kind":   "choice",
		"prompt": "What risk does this call carry?",
		"labels": []any{
			map[string]any{"label": "safe", "description": "reads or lists"},
			map[string]any{"label": "changes", "description": "writes inside the workspace"},
		},
		"items": items,
	})
	if err != nil {
		panic(err)
	}
	return b
}

func TestWithNoDecisionUrlNothingMoves(t *testing.T) {
	r := testRoot(nullFrontend{})
	k := wire(r)
	for _, tool := range k.Tools {
		if tool.Name() == "decide" {
			t.Fatal("without decisionUrl the tool menu does not carry decide")
		}
	}
	if strings.Contains(r.fullSystem, "hand the items to decide") {
		t.Fatal("without decisionUrl the system prompt carries no decide guideline")
	}
	if strings.Contains(r.fullSystem, "pack the task before reading files for it") {
		t.Fatal("without decisionUrl the system prompt carries no pack guideline")
	}
}

func TestADecideToolJoinsTheTable(t *testing.T) {
	r := testRoot(nullFrontend{})
	r.decide = wiredDecide(t, nil, nil)
	k := wire(r)
	if _, ok := r.live.Tool("decide"); !ok {
		t.Fatal("the decide tool sits in the live table")
	}
	if r.live.IsPlugin("decide") {
		t.Fatal("decide is a built-in entry, not a plugin")
	}
	found := false
	for _, tool := range k.Tools {
		if tool.Name() == "decide" {
			found = true
		}
	}
	if !found {
		t.Fatal("the kernel carries the decide tool")
	}
	if !r.natives["decide"] {
		t.Fatal("decide is native: a plugin by that name collides and manual mode does not ask")
	}
}

func TestAWiredDecideBringsThePackGuideline(t *testing.T) {
	r := testRoot(nullFrontend{})
	r.decide = wiredDecide(t, nil, nil)
	k := wire(r)
	if strings.Contains(r.fullSystem, "hand the items to decide") {
		t.Fatalf("the decide trigger lives in the tool, not the system prompt:\n%s", r.fullSystem)
	}
	if !strings.Contains(r.fullSystem, "pack the task before reading files for it") {
		t.Fatalf("the pack guideline joins the system prompt:\n%s", r.fullSystem)
	}
	if len(k.Middleware) != 10 {
		t.Fatalf("the guideline is one link: %d", len(k.Middleware))
	}
}

func TestADecideCallSortsAndRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "decision.sqlite")
	db, _, _, err := store.Open(path, decisionstore.Statements(), decisionstore.SchemaVersion, decisionstore.Migration())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := testRoot(nullFrontend{})
	r.drec = decisionstore.Recorder{DB: db, Scope: "proj"}
	r.decide = wiredDecide(t, r.drec, nil)
	r.allow = []string{"bash", "decide"}
	k := wire(r)
	var exec core.ToolExec = func(ctx context.Context, call core.ToolCall) (string, error) {
		return "ran", nil
	}
	for _, mw := range k.Middleware {
		exec = mw.Wrap(exec)
	}
	ctx := core.WithSession(context.Background(), &core.Session{ID: "sess-3"})
	out, err := exec(ctx, core.ToolCall{ID: "c1", Name: "decide", Args: decideArgs("alpha", "beta")})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "safe (2): #1 alpha; #2 beta") {
		t.Fatalf("the items group under their label:\n%q", out)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM decisions`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("one row per item, got %d", count)
	}
	var site, status, decider, session, scope, state string
	var confidence float64
	var unsure bool
	if err := db.QueryRow(`SELECT site, status, decider, session, scope, state, confidence, unsure FROM decisions ORDER BY id LIMIT 1`).
		Scan(&site, &status, &decider, &session, &scope, &state, &confidence, &unsure); err != nil {
		t.Fatal(err)
	}
	if site != decision.SiteDecide || status != decision.StatusFinal {
		t.Fatalf("the rows are final decide rows: %q %q", site, status)
	}
	if decider != "fake" || session != "sess-3" || scope != "proj" {
		t.Fatalf("the row names the decider, the session and the scope: %q %q %q", decider, session, scope)
	}
	if state != `{"item":"alpha"}` {
		t.Fatalf("the state is the item: %q", state)
	}
	if confidence != 0.9 || unsure {
		t.Fatalf("the row keeps the answer's confidence: %g unsure %v", confidence, unsure)
	}
}

func TestADecideNameIsNativeToPlugins(t *testing.T) {
	if _, err := plugins.DiscoverChecked(context.Background(), nil, []string{filepath.Join(t.TempDir(), "decide.py")}, map[string]bool{"decide": true}); err == nil {
		t.Fatal("a plugin named decide collides with the built-in")
	}
}

type overlapDecider struct {
	inFlight int32
	max      int32
}

func (o *overlapDecider) Decide(ctx context.Context, state string, questions []decision.Question) ([]decision.Answer, error) {
	n := atomic.AddInt32(&o.inFlight, 1)
	for {
		m := atomic.LoadInt32(&o.max)
		if n <= m || atomic.CompareAndSwapInt32(&o.max, m, n) {
			break
		}
	}
	time.Sleep(50 * time.Millisecond)
	atomic.AddInt32(&o.inFlight, -1)
	return []decision.Answer{{Question: "item", Value: "safe", Confidence: 0.9, Decider: "fake"}}, nil
}

func TestTheKernelParallelStampsTheFanOut(t *testing.T) {
	o := &overlapDecider{}
	r := testRoot(nullFrontend{})
	d, err := decision.NewDecide(decision.DecideOptions{Decider: o, Parallel: 1})
	if err != nil {
		t.Fatal(err)
	}
	r.decide = d
	wire(r)
	if _, err := d.Exec(context.Background(), decideArgs("alpha", "beta")); err != nil {
		t.Fatal(err)
	}
	if o.max < 2 {
		t.Fatalf("the kernel's parallel stamps the fan-out: constructed 1, got %d in flight", o.max)
	}
}

func TestTheFanOutStaysBoundedAtOne(t *testing.T) {
	o := &overlapDecider{}
	d, err := decision.NewDecide(decision.DecideOptions{Decider: o, Parallel: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(context.Background(), decideArgs("alpha", "beta", "gamma")); err != nil {
		t.Fatal(err)
	}
	if o.max != 1 {
		t.Fatalf("parallel one bounds the fan-out, got %d in flight", o.max)
	}
}

func TestTheRootResultCapBoundsThePack(t *testing.T) {
	r := testRoot(nullFrontend{})
	r.graph = graph.NewQueue(t.TempDir(), nil)
	r.resultCap = 1234
	wire(r)
	item, load := r.graph.PackCaps()
	if item != graph.ReadCap {
		t.Fatalf("the candidate items keep the read ceiling: %d", item)
	}
	if load != 1234 {
		t.Fatalf("the pack loads by the result cap the root passes in: %d", load)
	}
}
