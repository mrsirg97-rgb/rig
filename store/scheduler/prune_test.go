package scheduler_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	sched "github.com/mrsirg97-rgb/rig/v3/store/scheduler"
)

func countRows(t *testing.T, db sched.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.DB.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestPruneKeepsTheTwentyNewestRunRowsPerJob(t *testing.T) {
	h, key := setupJob(t, t.TempDir(), nil)
	for i := 0; i < 25; i++ {
		if _, err := sched.RecordRun(context.Background(), h.db, sched.RunRecordInput{ID: key, Status: "ok"}); err != nil {
			t.Fatal(err)
		}
	}
	if got := countRows(t, h.db, `SELECT count(*) FROM runs WHERE job_id = ?`, key); got != 25 {
		t.Fatalf("fixture: %d run rows, want 25", got)
	}
	mustOK(t, sched.Prune(context.Background(), h.db, h.home))
	if got := countRows(t, h.db, `SELECT count(*) FROM runs WHERE job_id = ?`, key); got != 20 {
		t.Fatalf("%d run rows survive the prune, want the newest 20", got)
	}
	var newest, oldest int64
	if err := h.db.DB.QueryRow(`SELECT max(seq), min(seq) FROM runs WHERE job_id = ?`, key).Scan(&newest, &oldest); err != nil {
		t.Fatal(err)
	}
	if newest-oldest != 19 {
		t.Fatalf("the kept window is off: newest %d, oldest kept %d", newest, oldest)
	}
	if state := jobsRow(t, h, key)["state"]; state != "active" {
		t.Fatalf("an active job is never pruned, got %v", state)
	}
}

func TestPruneKeepsTheTwentyNewestSettledJobsAndTheMintReservation(t *testing.T) {
	h := newHarness(t, "/ws/prune")
	at := nowFixed.Add(time.Hour).Format(time.RFC3339)
	var ids []string
	for i := 0; i < 25; i++ {
		_, err := h.create(sched.CreateInput{
			Name: fmt.Sprintf("once-%02d", i), Prompt: "p", Cron: "once", At: at,
			Model: "w", Cwd: h.sessCwd,
		})
		mustOK(t, err)
		ids = append(ids, fmt.Sprintf("j%d", i+1))
	}
	for i := len(ids) - 1; i >= 0; i-- {
		if _, err := sched.RecordRun(context.Background(), h.db, sched.RunRecordInput{ID: ids[i], Status: "ok"}); err != nil {
			t.Fatal(err)
		}
	}
	oldest := ids[20]
	dir := filepath.Join(h.home, "runs", oldest)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "log.log"), []byte("# rig-scheduler run\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustOK(t, sched.Prune(context.Background(), h.db, h.home))
	for _, id := range ids[:20] {
		if jobsRow(t, h, id) == nil {
			t.Fatalf("the newest settled job %s must survive the prune", id)
		}
	}
	for _, id := range ids[20:24] {
		if jobsRow(t, h, id) != nil {
			t.Fatalf("the stale settled job %s must be pruned", id)
		}
		if got := countRows(t, h.db, `SELECT count(*) FROM runs WHERE job_id = ?`, id); got != 0 {
			t.Fatalf("the pruned job %s must not leave run rows (%d left)", id, got)
		}
	}
	if jobsRow(t, h, ids[24]) == nil {
		t.Fatal("the highest minted id must survive as the mint reservation even though it settled oldest")
	}
	if _, err := os.Stat(filepath.Join(h.home, "runs", oldest)); !os.IsNotExist(err) {
		t.Fatalf("the pruned job's run log dir must go with it (stat: %v)", err)
	}
	id, err := h.create(sched.CreateInput{Name: "after-prune", Prompt: "p", Cron: "0 */4 * * *", Model: "w", Cwd: h.sessCwd})
	mustOK(t, err)
	contains(t, id, "created j26 'after-prune'")
	if jobsRow(t, h, "j26") == nil || jobsRow(t, h, "j25") == nil {
		t.Fatalf("a create after the prune must mint j26 (no id is ever reused), got %q", id)
	}
}

func TestAFirePrunesTheSettledJobsBehindIt(t *testing.T) {
	cwd := t.TempDir()
	h, key := setupJob(t, cwd, nil)
	at := nowFixed.Add(time.Hour).Format(time.RFC3339)
	var ids []string
	for i := 0; i < 25; i++ {
		_, err := h.create(sched.CreateInput{
			Name: fmt.Sprintf("once-%02d", i), Prompt: "p", Cron: "once", At: at,
			Model: "w", Cwd: cwd,
		})
		mustOK(t, err)
		ids = append(ids, fmt.Sprintf("j%d", i+2))
	}
	for _, id := range ids {
		if _, err := sched.RecordRun(context.Background(), h.db, sched.RunRecordInput{ID: id, Status: "ok"}); err != nil {
			t.Fatal(err)
		}
	}
	spawn := &fakeSpawn{result: sched.SpawnResult{Exit: 0}}
	mustOK(t, sched.RunJob(key, runOpts(h, nil, spawn, fetchOpts{})))
	if got := countRows(t, h.db, `SELECT count(*) FROM jobs WHERE state = 'done'`); got != 20 {
		t.Fatalf("a fire must prune the settled jobs behind it: %d done rows, want 20", got)
	}
	if jobsRow(t, h, ids[0]) != nil {
		t.Fatalf("the oldest settled job %s must be pruned by the fire", ids[0])
	}
}

func TestADelegatePrunesTheSettledJobsBehindIt(t *testing.T) {
	spawn := &delegateSpawn{result: sched.SpawnResult{Exit: 0, Stdout: "done\n"}}
	in := delegateInput(t, delegateFetch(t, false, ""), spawn.spawn, nil)
	ct := newFakeCrontab("")
	now := func() time.Time { return time.Now() }
	for i := 0; i < 25; i++ {
		_, err := sched.Create(context.Background(), in.DB, ct, sched.CreateInput{
			Name: fmt.Sprintf("once-%02d", i), Prompt: "p", Cron: "once",
			At:    now().Add(time.Hour).Format(time.RFC3339),
			Model: "w", Cwd: in.Cwd,
		}, in.Cwd, "sess", runnerCmd, in.Home, now)
		mustOK(t, err)
		if _, err := sched.RecordRun(context.Background(), in.DB, sched.RunRecordInput{ID: fmt.Sprintf("j%d", i+1), Status: "ok"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := sched.Delegate(in); err != nil {
		t.Fatalf("delegate: %v", err)
	}
	if got := countRows(t, in.DB, `SELECT count(*) FROM jobs WHERE state = 'done'`); got != 20 {
		t.Fatalf("a delegate must prune the settled jobs behind it: %d done rows, want 20", got)
	}
}

func TestPruneSparesActiveAndPausedJobs(t *testing.T) {
	h := newHarness(t, "/ws/spare")
	first, err := h.create(sched.CreateInput{Name: "active", Prompt: "p", Cron: "0 */4 * * *", Model: "w", Cwd: h.sessCwd})
	mustOK(t, err)
	second, err := h.create(sched.CreateInput{Name: "paused", Prompt: "p", Cron: "1 */4 * * *", Model: "w", Cwd: h.sessCwd})
	mustOK(t, err)
	first, second = "j1", "j2"
	if _, err := sched.Pause(context.Background(), h.db, h.ct, second, h.sessCwd, "sess-core", h.rigHome); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 30; i++ {
		_, err := h.create(sched.CreateInput{
			Name: fmt.Sprintf("once-%02d", i), Prompt: "p", Cron: "once", At: nowFixed.Add(time.Hour).Format(time.RFC3339),
			Model: "w", Cwd: h.sessCwd,
		})
		mustOK(t, err)
		if _, err := sched.RecordRun(context.Background(), h.db, sched.RunRecordInput{ID: fmt.Sprintf("j%d", i+3), Status: "ok"}); err != nil {
			t.Fatal(err)
		}
	}
	mustOK(t, sched.Prune(context.Background(), h.db, h.home))
	for _, id := range []string{first, second} {
		if jobsRow(t, h, id) == nil {
			t.Fatalf("the %s job %s must survive the prune", map[string]string{first: "active", second: "paused"}[id], id)
		}
	}
}
