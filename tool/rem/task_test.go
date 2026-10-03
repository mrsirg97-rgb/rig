package rem_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/decision"
	"github.com/mrsirg97-rgb/rig/v2/models"
	"github.com/mrsirg97-rgb/rig/v2/store"
	decisionstore "github.com/mrsirg97-rgb/rig/v2/store/decision"
	"github.com/mrsirg97-rgb/rig/v2/store/graph"
	"github.com/mrsirg97-rgb/rig/v2/testenv"
)

const packTask = "the target the helper and the call"

type taskSink struct {
	db store.DB
}

func (s taskSink) ProposePending(ctx context.Context, p decision.Pending, a decision.Answer) error {
	scope := p.Scope
	if scope == "" {
		scope = "proj"
	}
	_, err := decisionstore.Propose(ctx, s.db, decisionstore.ProposeInput{
		Scope: scope, Session: p.Session, Site: p.Site, State: p.State,
		Question: p.Question, Answer: a.Value, Confidence: a.Confidence, Decider: a.Decider,
	})
	return err
}

type packReviews struct {
	db store.DB
}

func (r packReviews) Pending(ctx context.Context) ([]decision.ReviewRow, error) {
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

func (r packReviews) Settle(ctx context.Context, id int64, approved bool, reviewer, answer string) error {
	return decisionstore.Settle(ctx, r.db, decisionstore.SettleInput{
		ID: id, Approved: approved, Reviewer: reviewer, ReviewerAnswer: answer,
	})
}

type taskProbe struct {
	mu    sync.Mutex
	asks  []string
	tasks []string
}

func (p *taskProbe) record(task, item string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.tasks = append(p.tasks, task)
	p.asks = append(p.asks, item)
}

func (p *taskProbe) snapshot() ([]string, []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.tasks...), append([]string(nil), p.asks...)
}

func noulAnswer(p float64) map[string]any {
	return map[string]any{"type": "noul", "noul": p}
}

func wobblyAnswer(p float64) map[string]any {
	return map[string]any{"type": "choice", "choice": "yes", "probabilities": map[string]any{"yes": p, "no": 1 - p}}
}

func taskServer(t *testing.T, probe *taskProbe, answer func(item string) (map[string]any, bool)) *httptest.Server {
	t.Helper()
	srv := testenv.Server(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		probe.record(s.Task, s.Item)
		var id string
		for k := range req.Questions {
			id = k
		}
		a, ok := answer(s.Item)
		if !ok {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		body, err := json.Marshal(map[string]any{"answers": map[string]any{id: a}})
		if err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	return srv
}

func symbolAnswers(call map[string]any) func(string) (map[string]any, bool) {
	return func(item string) (map[string]any, bool) {
		switch {
		case strings.Contains(item, "func Target()"):
			return noulAnswer(0.9), true
		case strings.Contains(item, "func Helper()"):
			return noulAnswer(0.8), true
		case strings.Contains(item, "func Call()"):
			return call, true
		}
		return nil, false
	}
}

func allYes(string) (map[string]any, bool) {
	return noulAnswer(0.9), true
}

func allNo(string) (map[string]any, bool) {
	return noulAnswer(0.4), true
}

func openDecisionStore(t *testing.T) store.DB {
	t.Helper()
	db, _, _, err := store.Open(filepath.Join(t.TempDir(), "decision.sqlite"), decisionstore.Statements(), decisionstore.SchemaVersion, decisionstore.Migration())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func taskModule(t *testing.T, answer func(item string) (map[string]any, bool)) (string, *graph.Queue, core.Tool, *taskProbe, store.DB) {
	t.Helper()
	root, q, tool := packModule(t)
	probe := &taskProbe{}
	db := openDecisionStore(t)
	dec, err := decision.NewHTTP(decision.HTTPOptions{URL: taskServer(t, probe, answer).URL, Client: &http.Client{Transport: testenv.Transport()}})
	if err != nil {
		t.Fatal(err)
	}
	scorer, err := decision.NewPackScorer(dec, taskSink{db: db}, nil, nil, 2)
	if err != nil {
		t.Fatal(err)
	}
	q.SetScorer(scorer)
	return root, q, tool, probe, db
}

func mapThePackFixture(t *testing.T, root string, q *graph.Queue) {
	t.Helper()
	mapWithReads(t, root, q, filepath.Join(root, "alpha", "a.go"), filepath.Join(root, "beta", "b.go"))
}

func TestPackByTaskLoadsTheYesSetAndListsTheUnsure(t *testing.T) {
	root, q, tool, probe, _ := taskModule(t, symbolAnswers(wobblyAnswer(0.4)))
	mapThePackFixture(t, root, q)
	reply, err := packExec(t, tool, root, packTask, nil)
	if err != nil {
		t.Fatalf("pack: %v", err)
	}
	if !strings.Contains(reply, "Target — func — alpha/a.go:") {
		t.Fatalf("the affirmed candidate loads live:\n%s", reply)
	}
	if !strings.Contains(reply, "\nHelper — func — alpha/a.go:") {
		t.Fatalf("the second affirmed candidate loads live:\n%s", reply)
	}
	if strings.Contains(reply, "\nCall — func") {
		t.Fatalf("the declined candidate must not load:\n%s", reply)
	}
	if strings.Index(reply, "Target — func") > strings.Index(reply, "Helper — func") {
		t.Fatalf("the yes set rides the lexical rank:\n%s", reply)
	}
	if !strings.Contains(reply, "unsure (1) — the server did not say yes; pack one by hand") {
		t.Fatalf("the declined candidate is the unsure list:\n%s", reply)
	}
	if !strings.Contains(reply, "  beta.Call") {
		t.Fatalf("the unsure candidate is listed by name:\n%s", reply)
	}
	if !strings.Contains(reply, "coverage:") || !strings.Contains(reply, "scored 3 candidates") {
		t.Fatalf("the reply names the coverage and the count scored:\n%s", reply)
	}
	tasks, asks := probe.snapshot()
	if len(asks) != 3 {
		t.Fatalf("one request per candidate: %v", asks)
	}
	for i, task := range tasks {
		if task != packTask {
			t.Fatalf("request %d carries the task: %q", i, task)
		}
	}
}

func TestPackByTaskStopsAtTheBudgetOnTheLexicalTop(t *testing.T) {
	root, q, tool, _, _ := taskModule(t, func(item string) (map[string]any, bool) {
		switch {
		case strings.Contains(item, "func Target()"):
			return noulAnswer(0.9), true
		case strings.Contains(item, "func Helper()"):
			return noulAnswer(0.8), true
		case strings.Contains(item, "func Call()"):
			return noulAnswer(0.7), true
		}
		return nil, false
	})
	mapThePackFixture(t, root, q)
	q.SetPackCaps(1<<20, 1)
	reply, err := packExec(t, tool, root, packTask, nil)
	if err != nil {
		t.Fatalf("pack: %v", err)
	}
	if !strings.Contains(reply, "Target — func — alpha/a.go:") {
		t.Fatalf("the lexical top loads first:\n%s", reply)
	}
	if strings.Contains(reply, "\nHelper — func") || strings.Contains(reply, "\nCall — func") {
		t.Fatalf("the pack stops at the budget:\n%s", reply)
	}
	if strings.Contains(reply, "unsure") {
		t.Fatalf("the budget does not decline candidates:\n%s", reply)
	}
}

func TestPackByTaskCutsTheCandidatesPastTheCeilingBeforeAnyRequest(t *testing.T) {
	root, q, tool, probe, _ := taskModule(t, allYes)
	mapThePackFixture(t, root, q)
	q.SetPackCaps(60, 1<<20)
	reply, err := packExec(t, tool, root, packTask, nil)
	if err != nil {
		t.Fatalf("pack: %v", err)
	}
	_, asks := probe.snapshot()
	if len(asks) != 1 {
		t.Fatalf("the ceiling cuts before any request: %d went out", len(asks))
	}
	if !strings.Contains(reply, "scored 1 candidates") {
		t.Fatalf("the reply names the count scored:\n%s", reply)
	}
}

func TestPackByTaskWithoutAServerPacksTheLexicalCandidatesAndWritesNoRows(t *testing.T) {
	root, q, tool := packModule(t)
	mapThePackFixture(t, root, q)
	reply, err := packExec(t, tool, root, packTask, nil)
	if err != nil {
		t.Fatalf("pack: %v", err)
	}
	for _, want := range []string{"Target — func", "Helper — func", "Call — func"} {
		if !strings.Contains(reply, want) {
			t.Fatalf("the lexical candidate %q loads in rank order:\n%s", want, reply)
		}
	}
	if strings.Contains(reply, "unsure") || strings.Contains(reply, "scored") {
		t.Fatalf("the unset pack scores nothing:\n%s", reply)
	}
	if !strings.Contains(reply, "coverage:") {
		t.Fatalf("the unset pack keeps the coverage line:\n%s", reply)
	}
}

func TestPackByTaskScoresNoMoreThanTheLoadCouldHold(t *testing.T) {
	root, q, tool, probe, _ := taskModule(t, allYes)
	mapThePackFixture(t, root, q)
	if _, err := packExec(t, tool, root, packTask, nil); err != nil {
		t.Fatalf("pack: %v", err)
	}
	_, asks := probe.snapshot()
	if len(asks) != 3 {
		t.Fatalf("the unbounded pack asks about every candidate: %v", asks)
	}
	lens := []int{len(asks[0]), len(asks[1]), len(asks[2])}
	sort.Ints(lens)
	root2, q2, tool2, probe2, _ := taskModule(t, allYes)
	mapThePackFixture(t, root2, q2)
	q2.SetPackCaps(1<<20, lens[0]+lens[1]+1)
	reply, err := packExec(t, tool2, root2, packTask, nil)
	if err != nil {
		t.Fatalf("pack: %v", err)
	}
	_, asks2 := probe2.snapshot()
	if len(asks2) != 2 {
		t.Fatalf("the load budget bounds the scored set, got %d asks: %v", len(asks2), asks2)
	}
	for _, a := range asks2 {
		if strings.Contains(a, "func Call()") {
			t.Fatalf("the rank prefix is what the load could hold, the tail is not scored: %v", asks2)
		}
	}
	if !strings.Contains(reply, "scored 2 candidates") {
		t.Fatalf("the reply names the count scored:\n%s", reply)
	}
}

func TestPackByTaskOrdersTheYesSetByTheLexicalRankNotTheServer(t *testing.T) {
	root, q, tool, _, _ := taskModule(t, func(item string) (map[string]any, bool) {
		switch {
		case strings.Contains(item, "func Target()"):
			return noulAnswer(0.7), true
		case strings.Contains(item, "func Helper()"):
			return noulAnswer(0.8), true
		case strings.Contains(item, "func Call()"):
			return noulAnswer(0.9), true
		}
		return nil, false
	})
	mapThePackFixture(t, root, q)
	reply, err := packExec(t, tool, root, packTask, nil)
	if err != nil {
		t.Fatalf("pack: %v", err)
	}
	target, helper, call := strings.Index(reply, "Target — func"), strings.Index(reply, "Helper — func"), strings.Index(reply, "Call — func")
	if target < 0 || helper < 0 || call < 0 {
		t.Fatalf("every affirmed candidate loads:\n%s", reply)
	}
	if target > helper || helper > call {
		t.Fatalf("the yes set rides the lexical rank, not the server's probabilities:\n%s", reply)
	}
}

func TestTheReviewerDeniesAPackRowWithTheCorrectedAnswer(t *testing.T) {
	root, q, tool, _, db := taskModule(t, symbolAnswers(noulAnswer(0.4)))
	mapThePackFixture(t, root, q)
	if _, err := packExec(t, tool, root, packTask, nil); err != nil {
		t.Fatalf("pack: %v", err)
	}
	fire := func(ctx context.Context, prompt string) (string, string, error) {
		var lines []string
		for _, block := range strings.Split(prompt, "\n== ")[1:] {
			id := block[:strings.Index(block, " ")]
			if strings.Contains(block, "func Call()") {
				lines = append(lines, "verdict: "+id+" deny yes")
				continue
			}
			lines = append(lines, "verdict: "+id+" approve")
		}
		return strings.Join(lines, "\n"), "rev", nil
	}
	rev := decision.NewReviewer(packReviews{db: db}, fire, 1<<20, models.Model{Window: 1 << 30, Reserve: 0, MaxTokens: 1 << 30}, func(string) {})
	if _, err := rev.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	res, err := db.Query(`SELECT id, status, reviewer, reviewer_answer FROM decisions`)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Close()
	denied := 0
	for res.Next() {
		var id int64
		var status, reviewer string
		var answer *string
		if err := res.Scan(&id, &status, &reviewer, &answer); err != nil {
			t.Fatal(err)
		}
		if status == decision.StatusDenied {
			denied++
			if answer == nil || *answer != "yes" {
				t.Fatalf("the deny stores the corrected answer: %v", answer)
			}
			if reviewer != "rev" {
				t.Fatalf("the deny names the reviewer: %q", reviewer)
			}
		}
	}
	if denied != 1 {
		t.Fatalf("one row denied, got %d", denied)
	}
}

func TestPackByTaskFillsTheLexicalRankWhenTheServerAffirmsNothing(t *testing.T) {
	root, q, tool, probe, _ := taskModule(t, allNo)
	mapThePackFixture(t, root, q)
	reply, err := packExec(t, tool, root, packTask, nil)
	if err != nil {
		t.Fatalf("pack: %v", err)
	}
	_, asks := probe.snapshot()
	if len(asks) != 3 {
		t.Fatalf("every candidate scored: %d", len(asks))
	}
	for _, want := range []string{"Target — func — alpha/a.go:", "Helper — func — alpha/a.go:", "Call — func — beta/b.go:"} {
		if !strings.Contains(reply, want) {
			t.Fatalf("the lexical top fills the pack:\n%s", reply)
		}
	}
	if strings.Contains(reply, "unsure") {
		t.Fatalf("a confident no is not listed:\n%s", reply)
	}
	if strings.Index(reply, "Target — func") > strings.Index(reply, "Call — func") {
		t.Fatalf("the fill rides the lexical rank:\n%s", reply)
	}
	if !strings.Contains(reply, "scored 3 candidates") {
		t.Fatalf("the reply names the count scored:\n%s", reply)
	}
}

func TestPackByTaskLoadsTheYesSetThenTheLexicalFill(t *testing.T) {
	root, q, tool, _, _ := taskModule(t, func(item string) (map[string]any, bool) {
		switch {
		case strings.Contains(item, "func Target()"):
			return noulAnswer(0.9), true
		case strings.Contains(item, "func Helper()"):
			return noulAnswer(0.3), true
		case strings.Contains(item, "func Call()"):
			return noulAnswer(0.4), true
		}
		return nil, false
	})
	mapThePackFixture(t, root, q)
	reply, err := packExec(t, tool, root, packTask, nil)
	if err != nil {
		t.Fatalf("pack: %v", err)
	}
	if strings.Contains(reply, "unsure") {
		t.Fatalf("a confident no is not listed:\n%s", reply)
	}
	target, helper, call := strings.Index(reply, "Target — func"), strings.Index(reply, "Helper — func"), strings.Index(reply, "Call — func")
	if target < 0 || helper < 0 || call < 0 {
		t.Fatalf("the yes set loads first and the lexical top fills the rest:\n%s", reply)
	}
	if target > helper || helper > call {
		t.Fatalf("the affirmations lead and the fill rides the rank:\n%s", reply)
	}
	if !strings.Contains(reply, "scored 3 candidates") {
		t.Fatalf("the reply names the count scored:\n%s", reply)
	}
}
