package decision_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/v2/broadcast"
	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/decision"
	"github.com/mrsirg97-rgb/rig/v2/testenv"
)

var errSinkBroken = errors.New("the sink is broken")

type packBrokenSink struct{}

func (packBrokenSink) ProposePending(ctx context.Context, p decision.Pending, a decision.Answer) error {
	return errSinkBroken
}

type packProbe struct {
	mu           sync.Mutex
	tasks        []string
	items        []string
	kinds        []string
	instructions []string
	requests     int
}

func (p *packProbe) record(task, item, kind, instructions string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.tasks = append(p.tasks, task)
	p.items = append(p.items, item)
	p.kinds = append(p.kinds, kind)
	p.instructions = append(p.instructions, instructions)
	p.requests++
}

func (p *packProbe) snapshot() ([]string, []string, []string, []string, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.tasks...), append([]string(nil), p.items...),
		append([]string(nil), p.kinds...), append([]string(nil), p.instructions...), p.requests
}

func noulAnswer(p float64) map[string]any {
	return map[string]any{"type": "noul", "noul": p}
}

func wobblyAnswer(p float64) map[string]any {
	return map[string]any{"type": "choice", "choice": "yes", "probabilities": map[string]any{"yes": p, "no": 1 - p}}
}

type packFake struct {
	answer  func(item string) (map[string]any, bool)
	omit    map[string]bool
	status5 map[string]bool
}

func packServer(t *testing.T, probe *packProbe, fake packFake) *httptest.Server {
	t.Helper()
	srv := testenv.Server(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/systemone" {
			t.Errorf("the endpoint is /v1/systemone, got %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var req struct {
			State     string                     `json:"state"`
			Questions map[string]json.RawMessage `json:"questions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var s struct {
			Task string `json:"task"`
			Item string `json:"item"`
		}
		if err := json.Unmarshal([]byte(req.State), &s); err != nil {
			t.Errorf("state: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var id string
		for k := range req.Questions {
			id = k
		}
		var q struct {
			Type         string `json:"type"`
			Instructions string `json:"instructions"`
		}
		if err := json.Unmarshal(req.Questions[id], &q); err != nil {
			t.Errorf("question: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		probe.record(s.Task, s.Item, q.Type, q.Instructions)
		if fake.status5[s.Item] {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if fake.omit[s.Item] {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(wireReply(map[string]any{})))
			return
		}
		a, ok := fake.answer(s.Item)
		if !ok {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(wireReply(map[string]any{id: a})))
	}))
	return srv
}

func httpDeciderAt(t *testing.T, srv *httptest.Server) decision.Decider {
	t.Helper()
	dec, err := decision.NewHTTP(decision.HTTPOptions{URL: srv.URL, Client: &http.Client{Transport: testenv.Transport()}})
	if err != nil {
		t.Fatal(err)
	}
	return dec
}

func packScorer(t *testing.T, srv *httptest.Server, sink decision.Sink, land func(), voice broadcast.Member, parallel int) *decision.PackScorer {
	t.Helper()
	s, err := decision.NewPackScorer(httpDeciderAt(t, srv), sink, land, voice, parallel)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

var packItems = []string{
	"func Target() int { return Helper() } — alpha/a.go:5",
	"func Helper() int { return 1 } — alpha/a.go:7",
	"func Call() int { return alpha.Target() } — beta/b.go:5",
	"func Same() int { return 1 } — gamma/g.go:5",
}

func TestThePackScorerAsksOneYesNoPerCandidate(t *testing.T) {
	probe := &packProbe{}
	srv := packServer(t, probe, packFake{answer: func(string) (map[string]any, bool) { return noulAnswer(0.9), true }})
	s := packScorer(t, srv, nil, nil, nil, 2)
	verdicts, err := s.Score(context.Background(), "the target the helper and the call", packItems)
	if err != nil {
		t.Fatal(err)
	}
	tasks, items, kinds, instructions, requests := probe.snapshot()
	if requests != 4 {
		t.Fatalf("one request per candidate: %d", requests)
	}
	seen := map[string]bool{}
	for i := range tasks {
		if tasks[i] != "the target the helper and the call" {
			t.Fatalf("request %d carries the task as state: %q", i, tasks[i])
		}
		seen[items[i]] = true
		if kinds[i] != "noul" {
			t.Fatalf("request %d asks a yes/no: %q", i, kinds[i])
		}
		if instructions[i] != "Does this symbol matter for the task?" {
			t.Fatalf("request %d asks the matter question: %q", i, instructions[i])
		}
	}
	for _, item := range packItems {
		if !seen[item] {
			t.Fatalf("the item %q went out as a state", item)
		}
	}
	if len(verdicts) != 4 {
		t.Fatalf("one verdict per candidate: %d", len(verdicts))
	}
	for i, v := range verdicts {
		if !v.Yes || v.Unsure || v.Probability != 0.9 {
			t.Fatalf("verdict %d is the server's yes with its probability: %+v", i, v)
		}
	}
}

func TestEveryScoredCandidateWritesOnePendingRow(t *testing.T) {
	probe := &packProbe{}
	answers := map[string]map[string]any{packItems[0]: noulAnswer(0.9), packItems[1]: noulAnswer(0.8), packItems[2]: noulAnswer(0.4)}
	srv := packServer(t, probe, packFake{answer: func(item string) (map[string]any, bool) { return answers[item], true }})
	db := openStore(t)
	land := 0
	s := packScorer(t, srv, &storeSink{db: db, written: make(chan decision.Answer, 8)}, func() { land++ }, nil, 2)
	ctx := core.WithSession(context.Background(), &core.Session{ID: "sess-9"})
	if _, err := s.Score(ctx, "the target the helper and the call", packItems); err != nil {
		t.Fatal(err)
	}
	if land != 3 {
		t.Fatalf("the reviewer is marked dirty per row: %d", land)
	}
	rows := map[int]row{}
	if err := scanRows(db, rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("one pending row per scored candidate, got %d", len(rows))
	}
	wantAnswer := map[int]string{1: "yes", 2: "yes", 3: "no"}
	wantConf := map[int]float64{1: 0.9, 2: 0.8, 3: 0.6}
	for i := 1; i <= 3; i++ {
		r := rows[i]
		if r.site != decision.SitePack || r.status != decision.StatusPending {
			t.Fatalf("row %d: site %q status %q, want a pending pack row", i, r.site, r.status)
		}
		if r.answer != wantAnswer[i] {
			t.Fatalf("row %d: answer %q, want %q", i, r.answer, wantAnswer[i])
		}
		if r.confidence != wantConf[i] {
			t.Fatalf("row %d: confidence %g, want %g", i, r.confidence, wantConf[i])
		}
		if r.decider != host(srv) {
			t.Fatalf("row %d: decider %q, want the server host %q", i, r.decider, host(srv))
		}
		if r.session != "sess-9" {
			t.Fatalf("row %d: session %q, want sess-9", i, r.session)
		}
		if r.scope != "proj" {
			t.Fatalf("row %d: scope %q, want proj", i, r.scope)
		}
		var state struct {
			Task string `json:"task"`
			Item string `json:"item"`
		}
		if err := json.Unmarshal([]byte(r.state), &state); err != nil {
			t.Fatalf("row %d: state %q: %v", i, r.state, err)
		}
		if state.Task != "the target the helper and the call" || state.Item != packItems[i-1] {
			t.Fatalf("row %d: the state carries the task and the item: %q", i, r.state)
		}
	}
}

func TestAWobblyAnswerIsUnsureAndAConfidentNoIsHidden(t *testing.T) {
	probe := &packProbe{}
	answers := map[string]map[string]any{packItems[0]: noulAnswer(0.9), packItems[1]: wobblyAnswer(0.4), packItems[2]: noulAnswer(0.4)}
	srv := packServer(t, probe, packFake{
		answer: func(item string) (map[string]any, bool) { return answers[item], true },
		omit:   map[string]bool{packItems[3]: true},
	})
	db := openStore(t)
	s := packScorer(t, srv, &storeSink{db: db, written: make(chan decision.Answer, 8)}, nil, nil, 2)
	verdicts, err := s.Score(context.Background(), "the target the helper and the call", packItems)
	if err != nil {
		t.Fatal(err)
	}
	if !verdicts[0].Yes || verdicts[0].Unsure {
		t.Fatalf("the affirmed candidate is the yes set: %+v", verdicts[0])
	}
	if verdicts[1].Yes || !verdicts[1].Unsure {
		t.Fatalf("the wobbly answer is the unsure list: %+v", verdicts[1])
	}
	if verdicts[2].Yes || verdicts[2].Unsure {
		t.Fatalf("the confident no is hidden: %+v", verdicts[2])
	}
	if verdicts[3].Yes || verdicts[3].Unsure {
		t.Fatalf("the unanswered candidate is neither set: %+v", verdicts[3])
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM decisions`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("every scored candidate one pending row, the unanswered none, got %d", count)
	}
}

func TestASinkErrorIsLoudAndNeverFailsTheScore(t *testing.T) {
	probe := &packProbe{}
	srv := packServer(t, probe, packFake{answer: func(string) (map[string]any, bool) { return noulAnswer(0.9), true }})
	v, loud := voice(t)
	s := packScorer(t, srv, packBrokenSink{}, nil, v, 2)
	verdicts, err := s.Score(context.Background(), "the task", packItems[:1])
	if err != nil {
		t.Fatalf("a store error never fails a call: %v", err)
	}
	if !verdicts[0].Yes {
		t.Fatalf("the verdict stands beside the store error: %+v", verdicts[0])
	}
	select {
	case m := <-loud:
		if !strings.Contains(m, "decision: pack") {
			t.Fatalf("the swallow is loud and named: %q", m)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the swallow is loud")
	}
}

func TestAServerErrorRefusesThePackAndWritesNoRows(t *testing.T) {
	probe := &packProbe{}
	srv := packServer(t, probe, packFake{
		answer:  func(string) (map[string]any, bool) { return noulAnswer(0.9), true },
		status5: map[string]bool{packItems[1]: true},
	})
	db := openStore(t)
	s := packScorer(t, srv, &storeSink{db: db, written: make(chan decision.Answer, 8)}, nil, nil, 1)
	if _, err := s.Score(context.Background(), "the task", packItems[:4]); err == nil {
		t.Fatal("a server error is a refusal")
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM decisions`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("a refused score writes no rows, got %d", count)
	}
}

type packOverlap struct {
	inFlight int32
	max      int32
}

func (o *packOverlap) Decide(ctx context.Context, state string, questions []decision.Question) ([]decision.Answer, error) {
	n := atomic.AddInt32(&o.inFlight, 1)
	for {
		cur := atomic.LoadInt32(&o.max)
		if n <= cur || atomic.CompareAndSwapInt32(&o.max, cur, n) {
			break
		}
	}
	time.Sleep(30 * time.Millisecond)
	atomic.AddInt32(&o.inFlight, -1)
	return []decision.Answer{{Question: "matter", Value: "yes", Confidence: 0.9, Decider: "fake"}}, nil
}

func TestThePackScorerStaysBoundedAtParallelOne(t *testing.T) {
	o := &packOverlap{}
	s, err := decision.NewPackScorer(o, nil, nil, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Score(context.Background(), "the task", []string{"one", "two", "three"}); err != nil {
		t.Fatal(err)
	}
	if o.max != 1 {
		t.Fatalf("parallel one bounds the fan-out, got %d in flight", o.max)
	}
}

func TestThePackScorerNeedsADeciderAndABound(t *testing.T) {
	if _, err := decision.NewPackScorer(nil, nil, nil, nil, 1); err == nil {
		t.Fatal("no decider, no scorer")
	}
	if _, err := decision.NewPackScorer(&fakeDecider{}, nil, nil, nil, 0); err == nil {
		t.Fatal("the fan-out needs a bound")
	}
}

func TestTheStampedParallelBoundsTheFanOut(t *testing.T) {
	o := &packOverlap{}
	s, err := decision.NewPackScorer(o, nil, nil, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Score(context.Background(), "the task", []string{"one", "two"}); err != nil {
		t.Fatal(err)
	}
	if o.max != 1 {
		t.Fatalf("the stamped parallel bounds the fan-out, got %d in flight", o.max)
	}
}
