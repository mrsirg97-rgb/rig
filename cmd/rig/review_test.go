package main

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/v2/broadcast"
	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/decision"
	"github.com/mrsirg97-rgb/rig/v2/models"
	"github.com/mrsirg97-rgb/rig/v2/store"
	decisionstore "github.com/mrsirg97-rgb/rig/v2/store/decision"
	sched "github.com/mrsirg97-rgb/rig/v2/store/scheduler"
	"github.com/mrsirg97-rgb/rig/v2/store/state"
)

type countingDecider struct{ answers []decision.Answer }

func (d *countingDecider) Decide(ctx context.Context, state string, questions []decision.Question) ([]decision.Answer, error) {
	return d.answers, nil
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

func waitCount(t *testing.T, db store.DB, status string, want int) {
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

type biteFire struct {
	mu      sync.Mutex
	prompts []string
}

func (f *biteFire) fire(ctx context.Context, prompt string, voice broadcast.Member) (string, error) {
	f.mu.Lock()
	f.prompts = append(f.prompts, prompt)
	f.mu.Unlock()
	for i, seg := range strings.Split(prompt, "\n== ") {
		if i == 0 {
			continue
		}
		if id, _, ok := strings.Cut(seg, " "); ok {
			row, _ := strconv.ParseInt(id, 10, 64)
			voice.Publish(context.Background(), func(error) {}, broadcast.NewMessage(voice.Id(), true, core.Verdict{Row: row, Accept: true}))
		}
	}
	return "local", nil
}

func (f *biteFire) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.prompts)
}

func (f *biteFire) prompt(i int) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.prompts[i]
}

func TestATurnEndFiresOneBiteAndTheNextTakesTheRest(t *testing.T) {
	decDB := openDecisionStore(t)
	r := testRoot(nullFrontend{})
	sdb, _, _, err := store.Open(filepath.Join(t.TempDir(), "sessions.sqlite"), state.Statements(), state.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sdb.Close() })
	r.rec = state.NewRecorder(nullFrontend{}, sdb, t.TempDir(), "local", Version, r.session.ID, r.session)
	f := &biteFire{}
	fleet(r)
	go r.engine.Start(context.Background())
	defer r.engine.Stop()
	r.decRev = decision.NewReviewer(context.Background(), r.engine, &dbReviews{db: decDB}, f.fire, 10,
		models.Model{Window: 1 << 30, Reserve: 0, MaxTokens: 1 << 30}, r.room, "proj")
	r.decQ = decision.NewQueue(&countingDecider{answers: []decision.Answer{{
		Question: "risk", Value: "safe", Confidence: 0.71, Decider: "laya",
	}}}, &dbSink{db: decDB}, &dbReviews{db: decDB}, decisionstore.Recorder{DB: decDB, Scope: "proj"}, r.decRev.Land, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.decQ.Run(ctx)

	for i := 0; i < 12; i++ {
		r.decQ.Propose(decision.Pending{
			Site: decision.SiteBash, Scope: "proj", State: `{"command":"ls"}`,
			Question: decision.Choice("risk", "What risk does this bash call carry?", "safe", "changes", "dangerous"),
		})
	}
	waitCount(t, decDB, "pending", 12)

	r.frontend().Notify(core.TurnEnd{})
	waitCount(t, decDB, "approved", 10)
	if rows := strings.Count(f.prompt(0), "\n== "); rows != 10 {
		t.Fatalf("the turn end took a bite of %d rows, want 10", rows)
	}
	if !strings.Contains(f.prompt(0), "\n== 1 ") || strings.Contains(f.prompt(0), "\n== 12 ") {
		t.Fatalf("the bite is the oldest rows: %.120s", f.prompt(0))
	}
	r.frontend().Notify(core.TurnEnd{})
	waitCount(t, decDB, "approved", 12)
	if f.calls() != 2 {
		t.Fatalf("the backlog left dirty takes the next turn end, got %d fires", f.calls())
	}
	waitCount(t, decDB, "pending", 0)
}

func TestTheReviewFireNamesTheDeathAndTheRunLog(t *testing.T) {
	home := t.TempDir()
	r := &root{activeID: "ox-alpha", cwd: t.TempDir()}
	r.delegate = func(in sched.DelegateInput) (sched.DelegateResult, error) {
		return sched.DelegateResult{
			Model: "ox-alpha", Exit: -1, Reason: "killed by signal 13",
			LogRel: filepath.Join("runs", "j31", "2026-08-15T12-00-00-000Z.log"),
		}, nil
	}
	fleet(r)
	fire := r.reviewFire(home, store.DB{}, "http://127.0.0.1:1", "rig", t.TempDir(), "", nil)
	_, err := fire(context.Background(), "review these", r.room.Mint())
	if err == nil {
		t.Fatal("a dead fire is an error")
	}
	for _, want := range []string{
		"the review fire ended exit -1 (timed out false)",
		"killed by signal 13",
		filepath.Join(home, "runs", "j31", "2026-08-15T12-00-00-000Z.log"),
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the fire error names the death and the log, missing %q: %v", want, err)
		}
	}
}

func TestTheReviewFireAsksForTheRowsLowestEffort(t *testing.T) {
	r := &root{
		activeID: "ox-alpha",
		cwd:      t.TempDir(),
		row:      models.Model{ID: "ox-alpha", Efforts: []string{"low", "medium", "xhigh"}, Effort: "xhigh"},
	}
	var seen sched.DelegateInput
	r.delegate = func(in sched.DelegateInput) (sched.DelegateResult, error) {
		seen = in
		return sched.DelegateResult{Model: "ox-alpha", Exit: 0}, nil
	}
	fleet(r)
	fire := r.reviewFire(t.TempDir(), store.DB{}, "http://127.0.0.1:1", "rig", t.TempDir(), "", nil)
	if _, err := fire(context.Background(), "review these", r.room.Mint()); err != nil {
		t.Fatal(err)
	}
	if seen.Effort != "low" {
		t.Fatalf("the fire asked for effort %q, want the row's lowest low (the row's own xhigh is the brain, not the reviewer's)", seen.Effort)
	}

	r2 := &root{activeID: "ox-alpha", cwd: t.TempDir(), row: models.Model{ID: "ox-alpha"}}
	r2.delegate = r.delegate
	fleet(r2)
	fire2 := r2.reviewFire(t.TempDir(), store.DB{}, "http://127.0.0.1:1", "rig", t.TempDir(), "", nil)
	seen = sched.DelegateInput{}
	if _, err := fire2(context.Background(), "review these", r2.room.Mint()); err != nil {
		t.Fatal(err)
	}
	if seen.Effort != "" {
		t.Fatalf("a row without levels must pass no effort, got %q", seen.Effort)
	}
}

func TestAHealthyReviewFireReturnsTheModel(t *testing.T) {
	r := &root{activeID: "ox-alpha", cwd: t.TempDir()}
	r.delegate = func(in sched.DelegateInput) (sched.DelegateResult, error) {
		return sched.DelegateResult{Model: "ox-alpha", Exit: 0}, nil
	}
	fleet(r)
	fire := r.reviewFire(t.TempDir(), store.DB{}, "http://127.0.0.1:1", "rig", t.TempDir(), "", nil)
	model, err := fire(context.Background(), "review these", r.room.Mint())
	if err != nil {
		t.Fatal(err)
	}
	if model != "ox-alpha" {
		t.Fatalf("fire = %q", model)
	}
}
