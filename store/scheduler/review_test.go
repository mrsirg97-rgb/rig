package scheduler_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/v2/decision"
	"github.com/mrsirg97-rgb/rig/v2/store"
	decisionstore "github.com/mrsirg97-rgb/rig/v2/store/decision"
	sched "github.com/mrsirg97-rgb/rig/v2/store/scheduler"
)

func proposeReviewRows(t *testing.T, db store.DB, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if _, err := decisionstore.Propose(context.Background(), db, decisionstore.ProposeInput{
			Scope: "proj", Site: decision.SiteBash, State: `{"command":"ls"}`,
			Question:   decision.Choice("risk", "What risk does this bash call carry?", "safe", "changes", "dangerous"),
			Answer:     "safe",
			Confidence: 0.71,
			Decider:    "laya",
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func reviewRunOpts(t *testing.T, h *harness, running []string, spawn *fakeSpawn, extra fetchOpts, reviews decision.Reviews) sched.RunOpts {
	opts := runOpts(h, running, spawn, extra)
	opts.Models = modelTable(t, "qwen3.8-27b-workers")
	opts.Reviews = reviews
	return opts
}

func TestAReviewJobDrainsAndRecordsARunLikeAnyJob(t *testing.T) {
	decPath := filepath.Join(t.TempDir(), "decision.sqlite")
	decDB, _, _, err := store.Open(decPath, decisionstore.Statements(), decisionstore.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	defer decDB.Close()
	proposeReviewRows(t, decDB, 2)

	h, key := setupJob(t, realCwd(t, "job"), func(in *sched.CreateInput) {
		in.Prompt = sched.ReviewPrompt
		in.Model = "qwen3.8-27b-workers"
	})
	spawn := &fakeSpawn{result: sched.SpawnResult{Exit: 0, Stdout: "verdict: 1 approve\nverdict: 2 approve\n"}}
	opts := reviewRunOpts(t, h, nil, spawn, fetchOpts{statuses: map[string]string{"qwen3.8-27b-workers": "loaded"}}, decisionstore.Reviews{DB: decDB})
	if err := sched.RunJob(key, opts); err != nil {
		t.Fatal(err)
	}
	if len(spawn.calls) != 1 {
		t.Fatalf("the drain is one fire, got %d spawns", len(spawn.calls))
	}
	call := spawn.calls[0]
	if !strings.Contains(strings.Join(call.Argv, " "), "-p -") {
		t.Fatalf("the fire stays a rig -p - worker: %v", call.Argv)
	}
	prompt, ok := sched.PromptFrom(call.Ctx)
	if !ok || !strings.Contains(prompt, "verdict line per row") || !strings.Contains(prompt, "What risk does this bash call carry?") {
		t.Fatalf("the fire reads the contract and the rows on stdin: %q", prompt)
	}
	rows, err := decisionstore.Pending(context.Background(), decDB)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("the store drained: %d rows pending", len(rows))
	}
	var status string
	var exit any
	if err := h.db.DB.QueryRow(`SELECT last_status, last_exit FROM jobs WHERE id = ?`, key).Scan(&status, &exit); err != nil {
		t.Fatal(err)
	}
	if status != "ok" || exit == nil || exit.(int64) != 0 {
		t.Fatalf("the run row reads like any job: status %q exit %v", status, exit)
	}
	var logPath string
	if err := h.db.DB.QueryRow(`SELECT log_path FROM runs WHERE job_id = ? ORDER BY seq DESC LIMIT 1`, key).Scan(&logPath); err != nil {
		t.Fatal(err)
	}
	log, err := os.ReadFile(filepath.Join(h.home, logPath))
	if err != nil {
		t.Fatalf("the run log: %v", err)
	}
	for _, want := range []string{"== stdout ==", "verdict: 1 approve"} {
		if !strings.Contains(string(log), want) {
			t.Fatalf("the run log missing %q:\n%s", want, log)
		}
	}
}

func TestAReviewJobSkipsWhenTheFleetIsBusy(t *testing.T) {
	decPath := filepath.Join(t.TempDir(), "decision.sqlite")
	decDB, _, _, err := store.Open(decPath, decisionstore.Statements(), decisionstore.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	defer decDB.Close()
	proposeReviewRows(t, decDB, 2)

	h, key := setupJob(t, realCwd(t, "job"), func(in *sched.CreateInput) {
		in.Prompt = sched.ReviewPrompt
		in.Model = "qwen3.8-27b-workers"
	})
	spawn := &fakeSpawn{}
	opts := reviewRunOpts(t, h, nil, spawn, fetchOpts{statuses: map[string]string{"qwen3.8-27b": "loaded"}}, decisionstore.Reviews{DB: decDB})
	if err := sched.RunJob(key, opts); err != nil {
		t.Fatal(err)
	}
	if len(spawn.calls) != 0 {
		t.Fatalf("a busy fleet fires nothing, got %d spawns", len(spawn.calls))
	}
	rows, _ := decisionstore.Pending(context.Background(), decDB)
	if len(rows) != 2 {
		t.Fatalf("the rows stay pending: %d", len(rows))
	}
	var status, reason any
	if err := h.db.DB.QueryRow(`SELECT status, reason FROM runs WHERE job_id = ? ORDER BY seq DESC LIMIT 1`, key).Scan(&status, &reason); err != nil {
		t.Fatal(err)
	}
	if status != "skip" || !strings.Contains(reason.(string), "the GPU is held by") {
		t.Fatalf("the skip names the holder: %v %v", status, reason)
	}
}

func TestAReviewJobWithoutTheDecisionStoreSkipsLoud(t *testing.T) {
	h, key := setupJob(t, realCwd(t, "job"), func(in *sched.CreateInput) {
		in.Prompt = sched.ReviewPrompt
		in.Model = "qwen3.8-27b-workers"
	})
	spawn := &fakeSpawn{}
	opts := reviewRunOpts(t, h, nil, spawn, fetchOpts{statuses: map[string]string{"qwen3.8-27b-workers": "loaded"}}, nil)
	if err := sched.RunJob(key, opts); err != nil {
		t.Fatal(err)
	}
	if len(spawn.calls) != 0 {
		t.Fatalf("no reviews seam, no fire: %d spawns", len(spawn.calls))
	}
	var reason any
	if err := h.db.DB.QueryRow(`SELECT reason FROM runs WHERE job_id = ? ORDER BY seq DESC LIMIT 1`, key).Scan(&reason); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(reason.(string), "reviews") {
		t.Fatalf("the skip names the missing seam: %v", reason)
	}
}

func TestAReviewJobWithoutAModelRowSkipsLoud(t *testing.T) {
	decPath := filepath.Join(t.TempDir(), "decision.sqlite")
	decDB, _, _, err := store.Open(decPath, decisionstore.Statements(), decisionstore.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	defer decDB.Close()
	proposeReviewRows(t, decDB, 1)

	h, key := setupJob(t, realCwd(t, "job"), func(in *sched.CreateInput) {
		in.Prompt = sched.ReviewPrompt
		in.Model = "no-such-model"
	})
	spawn := &fakeSpawn{}
	opts := reviewRunOpts(t, h, nil, spawn, fetchOpts{}, decisionstore.Reviews{DB: decDB})
	if err := sched.RunJob(key, opts); err != nil {
		t.Fatal(err)
	}
	if len(spawn.calls) != 0 {
		t.Fatalf("no model row, no fire: %d spawns", len(spawn.calls))
	}
	var reason any
	if err := h.db.DB.QueryRow(`SELECT reason FROM runs WHERE job_id = ? ORDER BY seq DESC LIMIT 1`, key).Scan(&reason); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(reason.(string), "no model row") {
		t.Fatalf("the skip names the missing row: %v", reason)
	}
}

func TestADelegateFireNamesItsDeathAndItsLog(t *testing.T) {
	h, _ := setupJob(t, realCwd(t, "del"), nil)
	spawn := &fakeSpawn{result: sched.SpawnResult{Exit: -1, Signal: 13}}
	res, err := sched.Delegate(sched.DelegateInput{
		DB: h.db, Home: h.home, Session: "sess-del", Cwd: h.sessCwd, Task: "do it",
		WorkerSession: "wsess-del", Model: "qwen3.8-workers",
		Fetch: fakeFetch([]string{"qwen3.8-27b-workers"}, fetchOpts{statuses: map[string]string{"qwen3.8-27b-workers": "loaded"}}),
		Spawn: spawn.spawn, WorkerCmd: []string{"/x/rig"},
		SwapURL: "http://127.0.0.1:8090",
		RigHome: h.rigHome, StateDir: filepath.Join(h.rigHome, "sessions"),
		NoTools: true, Sandbox: "off",
		Models: modelTable(t, "qwen3.8-27b-workers"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Reason != "killed by signal 13" {
		t.Fatalf("the result carries the spawn's death: %q", res.Reason)
	}
	if res.LogRel == "" {
		t.Fatal("the result carries the run log path")
	}
	ferr := res.FireError(h.home)
	if ferr == nil {
		t.Fatal("a dead fire is an error")
	}
	if !strings.Contains(ferr.Error(), "killed by signal 13") || !strings.Contains(ferr.Error(), res.LogRel) {
		t.Fatalf("the fire error names the death and the log: %v", ferr)
	}
	if !strings.Contains(ferr.Error(), "the review fire ended exit -1 (timed out false)") {
		t.Fatalf("the old wording stays: %v", ferr)
	}
}

func TestAHealthyFireIsNoError(t *testing.T) {
	res := sched.DelegateResult{Exit: 0}
	if err := res.FireError("home"); err != nil {
		t.Fatalf("exit 0 is no error: %v", err)
	}
}
