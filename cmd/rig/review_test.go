package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/v2/command"
	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/decision"
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

func waitSettled(t *testing.T, db store.DB, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM decisions WHERE status = 'pending'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the rows never settled to %d pending", want)
}

func TestATurnEndWithPendingRowsFiresNothing(t *testing.T) {
	decDB := openDecisionStore(t)
	var fires int
	r := testRoot(nullFrontend{})
	sdb, _, _, err := store.Open(filepath.Join(t.TempDir(), "sessions.sqlite"), state.Statements(), state.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sdb.Close() })
	r.rec = state.NewRecorder(nullFrontend{}, sdb, t.TempDir(), "local", Version, r.session.ID, r.session)
	r.decRev = decision.NewReviewer(decisionstore.Reviews{DB: decDB},
		func(ctx context.Context, prompt string) (string, string, error) {
			fires++
			return "verdict: 1 approve\n", "local", nil
		}, 1<<30, 1<<30, func(string) {})
	r.decQ = decision.NewQueue(&countingDecider{answers: []decision.Answer{{
		Question: "risk", Value: "safe", Confidence: 0.71, Decider: "laya",
	}}}, &dbSink{db: decDB}, func(string) {})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.decQ.Run(ctx)

	r.decQ.Propose(decision.Pending{
		Site: decision.SiteBash, Scope: "proj", State: `{"command":"ls"}`,
		Question: decision.Choice("risk", "What risk does this bash call carry?", "safe", "changes", "dangerous"),
	})
	waitSettled(t, decDB, 1)

	r.frontend().Notify(core.TurnEnd{})
	time.Sleep(200 * time.Millisecond)
	if fires != 0 {
		t.Fatalf("a turn end with pending rows fires nothing, got %d fires", fires)
	}
}

func TestDecideReviewFiresOnceAndSettlesWhatTheWorkerAnswers(t *testing.T) {
	decDB := openDecisionStore(t)
	for i := 0; i < 2; i++ {
		if _, err := decisionstore.Propose(context.Background(), decDB, decisionstore.ProposeInput{
			Scope: "proj", Site: decision.SiteBash, State: `{"command":"ls"}`,
			Question:   decision.Choice("risk", "What risk does this bash call carry?", "safe", "changes", "dangerous"),
			Answer:     "safe",
			Confidence: 0.71,
			Decider:    "laya",
		}); err != nil {
			t.Fatal(err)
		}
	}
	var fires int
	r := testRoot(nullFrontend{})
	r.decRev = decision.NewReviewer(decisionstore.Reviews{DB: decDB},
		func(ctx context.Context, prompt string) (string, string, error) {
			fires++
			return "verdict: 1 approve\nverdict: 2 approve\n", "local", nil
		}, 1<<30, 1<<30, func(string) {})

	var cmd core.Command
	for _, c := range command.All() {
		if c.Name() == "decide" {
			cmd = c
		}
	}
	if cmd == nil {
		t.Fatal("the decide command is not registered")
	}
	out, err := cmd.Run(context.Background(), "review", r.commandEnv())
	if err != nil {
		t.Fatal(err)
	}
	if out == "" {
		t.Fatal("the command answers before the fire")
	}
	waitSettled(t, decDB, 0)
	if fires != 1 {
		t.Fatalf("one door is one fire, got %d", fires)
	}
	var settled int
	if err := decDB.QueryRow(`SELECT count(*) FROM decisions WHERE status = 'approved'`).Scan(&settled); err != nil {
		t.Fatal(err)
	}
	if settled != 2 {
		t.Fatalf("the worker's verdicts settled the rows: %d approved", settled)
	}
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
	fire := r.reviewFire(home, store.DB{}, "http://127.0.0.1:1", "rig", t.TempDir(), "", nil)
	_, _, err := fire(context.Background(), "review these")
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

func TestAHealthyReviewFireReturnsTheReply(t *testing.T) {
	r := &root{activeID: "ox-alpha", cwd: t.TempDir()}
	r.delegate = func(in sched.DelegateInput) (sched.DelegateResult, error) {
		return sched.DelegateResult{Model: "ox-alpha", Exit: 0, Stdout: "verdict: 1 approve\n"}, nil
	}
	fire := r.reviewFire(t.TempDir(), store.DB{}, "http://127.0.0.1:1", "rig", t.TempDir(), "", nil)
	reply, model, err := fire(context.Background(), "review these")
	if err != nil {
		t.Fatal(err)
	}
	if reply != "verdict: 1 approve\n" || model != "ox-alpha" {
		t.Fatalf("fire = %q %q", reply, model)
	}
}

func TestDecideReviewWithNoReviewerRefuses(t *testing.T) {
	r := testRoot(nullFrontend{})
	var cmd core.Command
	for _, c := range command.All() {
		if c.Name() == "decide" {
			cmd = c
		}
	}
	_, err := cmd.Run(context.Background(), "review", r.commandEnv())
	if err == nil || !strings.Contains(err.Error(), "no reviewer") {
		t.Fatalf("no reviewer refuses by name: %v", err)
	}
}
