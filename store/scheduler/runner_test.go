package scheduler_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	sched "github.com/mrsirg97-rgb/looper/store/scheduler"
)

// pane's scheduler-runner cases, by name, over this runtime's seams:
// RunJob(key, opts) with fake fetch/spawn/crontab. TZ is UTC-pinned
// package-wide.

var runnerNow = time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)

const logName = "2026-08-15T12-00-00-000Z.log"

// pane's MODELS fixture: catalog ids with llama-swap aliases.
var modelsFixture = []struct {
	ID    string
	Alias []string
	Status string
}{
	{ID: "qwen3.8-27b", Alias: []string{"qwen3.8"}},
	{ID: "qwen3.8-27b-workers", Alias: []string{"qwen3.8-workers"}},
}

func modelsJSON(statuses map[string]string) string {
	type alias struct{ Aliases []string `json:"aliases"` }
	type meta struct {
		LLamaSwap alias `json:"llamaswap"`
	}
	type status struct{ Value string `json:"value"` }
	type model struct {
		ID     string `json:"id"`
		Meta   meta   `json:"meta"`
		Status status `json:"status"`
	}
	var data []model
	for _, m := range modelsFixture {
		st := "unloaded"
		if statuses != nil {
			st = statuses[m.ID]
		}
		data = append(data, model{ID: m.ID, Meta: meta{LLamaSwap: alias{Aliases: m.Alias}}, Status: status{Value: st}})
	}
	b, _ := json.Marshal(map[string]any{"data": data, "object": "list"})
	return string(b)
}

func runningJSON(models ...string) string {
	type entry struct{ Model string `json:"model"` }
	var rs []entry
	for _, m := range models {
		rs = append(rs, entry{Model: m})
	}
	b, _ := json.Marshal(map[string]any{"running": rs})
	return string(b)
}

// fakeFetch mirrors pane's fakeSwap: payloads by URL suffix.
type fetchOpts struct {
	failing  string
	statuses map[string]string
}

func fakeFetch(running []string, opts fetchOpts) func(url string) (json.RawMessage, error) {
	return func(url string) (json.RawMessage, error) {
		if opts.failing != "" {
			return nil, jsonError(opts.failing)
		}
		switch {
		case strings.HasSuffix(url, "/v1/models"):
			return json.RawMessage(modelsJSON(opts.statuses)), nil
		case strings.HasSuffix(url, "/running"):
			return json.RawMessage(runningJSON(running...)), nil
		}
		return nil, jsonError("unexpected url " + url)
	}
}

type jsonErr string

func (e jsonErr) Error() string { return string(e) }

func jsonError(s string) error { return jsonErr(s) }

// fakeSpawn records calls and replays the pinned result (pane's fakeSpawn).
type fakeSpawn struct {
	calls  []fakeCall
	result sched.SpawnResult
	err    error
}

type fakeCall struct {
	Argv []string
	Cwd  string
}

func (f *fakeSpawn) spawn(ctx context.Context, argv []string, cwd string) (sched.SpawnResult, error) {
	f.calls = append(f.calls, fakeCall{Argv: argv, Cwd: cwd})
	return f.result, f.err
}

// setupJob mirrors pane's setup: a job created in a scratch home, its key
// derived.
func setupJob(t *testing.T, cwd, scope string, mutate func(in *sched.CreateInput)) (h *harness, key string) {
	t.Helper()
	h = newHarness(t, cwd)
	in := sched.CreateInput{
		Name: "job", Prompt: "do the thing", Cron: "0 */4 * * *",
		Model: "qwen3.8-workers", Busy: "skip", Scope: scope,
	}
	if mutate != nil {
		mutate(&in)
	}
	reply, err := h.create(in)
	if err != nil {
		t.Fatalf("create: %v (%s)", err, reply)
	}
	if scope == "global" {
		key = "j1"
	} else {
		key = "cwd-" + sched.CwdHash(cwd) + ":j1"
	}
	return h, key
}

func runOpts(h *harness, running []string, spawn *fakeSpawn, extra fetchOpts) sched.RunOpts {
	return sched.RunOpts{
		Home:      h.home,
		Crontab:   h.ct,
		Fetch:     fakeFetch(running, extra),
		Spawn:     spawn.spawn,
		WorkerCmd: []string{"/x/looper"},
		SwapURL:   "http://127.0.0.1:8090",
		Now:       func() time.Time { return runnerNow },
	}
}

// runEvents: the op='run' events, oldest first (pane's events(dbPath,"run")).
func runEvents(t *testing.T, h *harness, scope string) []struct {
	TS      string
	Args    map[string]any
	Session string
} {
	t.Helper()
	// "" scopes to the cwd store: these tests' jobs are cwd-scope; global
	// requires an explicit scope.
	db := h.st.Cwd
	if scope == "global" {
		db = h.st.Global
	}
	rows, err := db.DB.Query(`SELECT ts, args, session FROM events WHERE op = 'run' ORDER BY seq`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []struct {
		TS      string
		Args    map[string]any
		Session string
	}
	for rows.Next() {
		var ts, args string
		var sess any
		if err := rows.Scan(&ts, &args, &sess); err != nil {
			t.Fatal(err)
		}
		var s string
		if ss, ok := sess.(string); ok {
			s = ss
		}
		out = append(out, struct {
			TS      string
			Args    map[string]any
			Session string
		}{ts, argsJSON(t, args), s})
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// --- key parsing ---

func TestParseKeyGlobalAndCwdKeysGarbageRefuses(t *testing.T) {
	g, err := sched.ParseKey("j1")
	if err != nil || g.Scope != "global" || g.Hash != "" || g.ID != "j1" {
		t.Fatalf("ParseKey(j1) = %+v, %v", g, err)
	}
	c, err := sched.ParseKey("cwd-b01229c83837:j42")
	if err != nil || c.Scope != "cwd" || c.Hash != "b01229c83837" || c.ID != "j42" {
		t.Fatalf("ParseKey(cwd) = %+v, %v", c, err)
	}
	for _, bad := range []string{"garbage", "cwd-zzz:j1"} {
		if _, err := sched.ParseKey(bad); err == nil || !regexp.MustCompile(`bad key`).MatchString(err.Error()) {
			t.Fatalf("ParseKey(%q) = %v, want /bad key/", bad, err)
		}
	}
}

// --- busy matrix ---

func TestOwnModelResidentViaAliasRunsArgvCwdReportBackLogOKRecord(t *testing.T) {
	h, key := setupJob(t, "/ws/r1", "", nil)
	spawn := &fakeSpawn{result: sched.SpawnResult{Exit: 0, Stdout: "hello\n"}}
	err := sched.RunJob(key, runOpts(h, []string{"qwen3.8-27b"}, spawn, fetchOpts{
		statuses: map[string]string{"qwen3.8-27b-workers": "loaded"},
	}))
	mustOK(t, err)

	c := spawn.calls[0]
	if len(c.Argv) < 5 || c.Argv[0] != "/x/looper" || c.Argv[1] != "-p" {
		t.Fatalf("argv prefix %v", c.Argv)
	}
	prompt := c.Argv[2]
	if !strings.HasPrefix(prompt, "do the thing") {
		t.Fatal("job prompt must lead")
	}
	if !strings.Contains(prompt, "rem") {
		t.Fatal("report-back must mention rem")
	}
	if !strings.Contains(prompt, "cwd") {
		t.Fatal("report-back must name the job cwd scope")
	}
	tail := c.Argv[len(c.Argv)-2:]
	if len(tail) != 2 || tail[0] != "-model" || tail[1] != "qwen3.8-workers" {
		t.Fatalf("argv tail %v", tail)
	}
	if c.Cwd != "/ws/r1" {
		t.Fatalf("cwd %q", c.Cwd)
	}

	rec := runEvents(t, h, "")[0]
	logPath, _ := rec.Args["log"].(string)
	if !strings.HasPrefix(logPath, "runs/") {
		t.Fatalf("log %q", logPath)
	}
	log, err := os.ReadFile(filepath.Join(h.home, logPath))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(log), "hello") {
		t.Fatal("stdout must be in the log")
	}
	if !strings.Contains(string(log), "exit=0") {
		t.Fatal("exit= must be in the log")
	}
	if rec.Args["status"] != "ok" || rec.Args["exit"] != float64(0) {
		t.Fatalf("record %v", rec.Args)
	}
	if rec.Session != "" {
		t.Fatalf("runner events are session-less: %q", rec.Session)
	}
	row := jobsRow(t, h, "", "j1")
	if row["last_status"] != "ok" {
		t.Fatalf("last_status %v", row["last_status"])
	}
	if !strings.Contains(h.ct.text, "pane-scheduler:") {
		t.Fatal("recurring line must stay")
	}
}

func TestNothingResidentRuns(t *testing.T) {
	h, key := setupJob(t, "/ws/r2", "", nil)
	spawn := &fakeSpawn{result: sched.SpawnResult{Exit: 0}}
	mustOK(t, sched.RunJob(key, runOpts(h, nil, spawn, fetchOpts{})))
	if len(spawn.calls) != 1 {
		t.Fatalf("spawn calls = %d", len(spawn.calls))
	}
}

func TestSomethingElseResidentBusySkipRecordsAndSpawnsNothing(t *testing.T) {
	h, key := setupJob(t, "/ws/r3", "", nil)
	spawn := &fakeSpawn{}
	before := h.ct.text
	err := sched.RunJob(key, runOpts(h, []string{"qwen3.8-27b"}, spawn, fetchOpts{}))
	mustOK(t, err)
	rec := runEvents(t, h, "")[0]
	if rec.Args["status"] != "skip" {
		t.Fatalf("status %v", rec.Args["status"])
	}
	if !regexp.MustCompile(`busy`).MatchString(toString(rec.Args["reason"])) {
		t.Fatalf("reason %v", rec.Args["reason"])
	}
	if !regexp.MustCompile(`qwen3\.8-27b`).MatchString(toString(rec.Args["reason"])) {
		t.Fatalf("reason must name the resident: %v", rec.Args["reason"])
	}
	if len(spawn.calls) != 0 {
		t.Fatal("no spawn on skip")
	}
	if h.ct.text != before {
		t.Fatal("line must stay untouched")
	}
}

func TestSomethingElseResidentBusyForceRunsAndEatsTheEviction(t *testing.T) {
	h, key := setupJob(t, "/ws/r4", "", func(in *sched.CreateInput) { in.Busy = "force" })
	spawn := &fakeSpawn{result: sched.SpawnResult{Exit: 0}}
	mustOK(t, sched.RunJob(key, runOpts(h, []string{"qwen3.8-27b"}, spawn, fetchOpts{})))
	if len(spawn.calls) != 1 {
		t.Fatalf("spawn calls = %d, want 1", len(spawn.calls))
	}
}

func TestOwnModelLoadedIdleWhileAnotherResidentRuns(t *testing.T) {
	h, key := setupJob(t, "/ws/r5", "", nil)
	spawn := &fakeSpawn{result: sched.SpawnResult{Exit: 0}}
	err := sched.RunJob(key, runOpts(h, []string{"qwen3.8-27b"}, spawn, fetchOpts{
		statuses: map[string]string{"qwen3.8-27b-workers": "loaded"},
	}))
	mustOK(t, err) // own slot already allocated: concurrent is safe
	if len(spawn.calls) != 1 {
		t.Fatalf("spawn calls = %d, want 1", len(spawn.calls))
	}
}

func TestOwnModelNotLoadedSomethingElseResidentSkips(t *testing.T) {
	h, key := setupJob(t, "/ws/r6", "", nil)
	spawn := &fakeSpawn{}
	err := sched.RunJob(key, runOpts(h, []string{"qwen3.8-27b"}, spawn, fetchOpts{
		statuses: map[string]string{"qwen3.8-27b": "loaded"},
	}))
	mustOK(t, err)
	rec := runEvents(t, h, "")[0]
	if rec.Args["status"] != "skip" || !regexp.MustCompile(`busy`).MatchString(toString(rec.Args["reason"])) {
		t.Fatalf("record %v", rec.Args)
	}
	if len(spawn.calls) != 0 {
		t.Fatal("no spawn")
	}
}

func TestBusyCheckFetchFailureFailsClosedWithReason(t *testing.T) {
	h, key := setupJob(t, "/ws/r7", "", nil)
	spawn := &fakeSpawn{}
	err := sched.RunJob(key, runOpts(h, nil, spawn, fetchOpts{failing: "fetch failed: ECONNREFUSED"}))
	mustOK(t, err)
	rec := runEvents(t, h, "")[0]
	if rec.Args["status"] != "skip" {
		t.Fatalf("status %v", rec.Args["status"])
	}
	if !regexp.MustCompile(`busy check failed`).MatchString(toString(rec.Args["reason"])) {
		t.Fatalf("reason %v", rec.Args["reason"])
	}
	if !regexp.MustCompile(`ECONNREFUSED`).MatchString(toString(rec.Args["reason"])) {
		t.Fatalf("reason must carry the failure: %v", rec.Args["reason"])
	}
	if len(spawn.calls) != 0 {
		t.Fatal("no spawn")
	}
}

// --- run outcomes ---

func TestWorkerExitNonZeroRecordsFailWithExitLogCarriesStderr(t *testing.T) {
	h, key := setupJob(t, "/ws/r8", "", nil)
	spawn := &fakeSpawn{result: sched.SpawnResult{Exit: 3, Stdout: "out", Stderr: "boom\n"}}
	mustOK(t, sched.RunJob(key, runOpts(h, nil, spawn, fetchOpts{})))
	rec := runEvents(t, h, "")[0]
	if rec.Args["status"] != "fail" || rec.Args["exit"] != float64(3) {
		t.Fatalf("record %v", rec.Args)
	}
	logPath, _ := rec.Args["log"].(string)
	log, err := os.ReadFile(filepath.Join(h.home, logPath))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(log), "boom") {
		t.Fatal("stderr must be in the log")
	}
	row := jobsRow(t, h, "", "j1")
	if row["last_status"] != "fail" || row["last_exit"] != int64(3) {
		t.Fatalf("row %v", row)
	}
}

func TestOnceFireConsumesTheLineAndMarksDone(t *testing.T) {
	h, key := setupJob(t, "/ws/r9", "", func(in *sched.CreateInput) {
		in.Cron = "once"
		in.At = "2026-08-16T03:07:00Z"
	})
	if !strings.Contains(h.ct.text, "pane-scheduler:") {
		t.Fatal("once line present before fire")
	}
	spawn := &fakeSpawn{result: sched.SpawnResult{Exit: 0}}
	mustOK(t, sched.RunJob(key, runOpts(h, nil, spawn, fetchOpts{})))
	if strings.Contains(h.ct.text, "pane-scheduler:") {
		t.Fatal("line must be consumed")
	}
	row := jobsRow(t, h, "", "j1")
	if row["state"] != "done" {
		t.Fatalf("state %v", row["state"])
	}
}

func TestOnceWithFailingWorkerDoneWithFailNoRetry(t *testing.T) {
	h, key := setupJob(t, "/ws/r10", "", func(in *sched.CreateInput) {
		in.Cron = "once"
		in.At = "2026-08-16T03:07:00Z"
	})
	spawn := &fakeSpawn{result: sched.SpawnResult{Exit: 1, Stderr: "nope"}}
	mustOK(t, sched.RunJob(key, runOpts(h, nil, spawn, fetchOpts{})))
	rec := runEvents(t, h, "")
	if len(rec) != 1 {
		t.Fatalf("exactly one run record, got %d", len(rec))
	}
	if rec[0].Args["status"] != "fail" {
		t.Fatalf("status %v", rec[0].Args)
	}
	row := jobsRow(t, h, "", "j1")
	if row["state"] != "done" {
		t.Fatalf("state %v", row["state"])
	}
	if strings.Contains(h.ct.text, "pane-scheduler:") {
		t.Fatal("line must be consumed")
	}
}

// --- zombie / drift self-heal ---

func TestZombieLineWithMissingRowLineDeletedSkipRecorded(t *testing.T) {
	h, key := setupJob(t, "/ws/z1", "", nil)
	// drop the projection row: the line outlives its job
	if _, err := h.st.Cwd.DB.Exec(`DELETE FROM jobs WHERE id = 'j1'`); err != nil {
		t.Fatal(err)
	}
	mustOK(t, sched.RunJob(key, runOpts(h, nil, &fakeSpawn{}, fetchOpts{})))
	rec := runEvents(t, h, "")[0]
	if rec.Args["status"] != "skip" || !regexp.MustCompile(`no job row`).MatchString(toString(rec.Args["reason"])) {
		t.Fatalf("record %v", rec.Args)
	}
	if strings.Contains(h.ct.text, "pane-scheduler:") {
		t.Fatal("zombie line must be deleted")
	}
}

func TestCrashWindowRowDoneButLineAliveLineDeletedSkipRecorded(t *testing.T) {
	h, key := setupJob(t, "/ws/z2", "", func(in *sched.CreateInput) {
		in.Cron = "once"
		in.At = "2026-08-16T03:07:00Z"
	})
	if _, err := h.st.Cwd.DB.Exec(`UPDATE jobs SET state = 'done' WHERE id = 'j1'`); err != nil {
		t.Fatal(err)
	}
	mustOK(t, sched.RunJob(key, runOpts(h, nil, &fakeSpawn{}, fetchOpts{})))
	rec := runEvents(t, h, "")[0]
	if rec.Args["status"] != "skip" || !regexp.MustCompile(`already done`).MatchString(toString(rec.Args["reason"])) {
		t.Fatalf("record %v", rec.Args)
	}
	if strings.Contains(h.ct.text, "pane-scheduler:") {
		t.Fatal("line must be healed")
	}
}

func TestPausedRowLineDriftedActiveSkipLineUntouched(t *testing.T) {
	h, key := setupJob(t, "/ws/z3", "", nil)
	if _, err := h.st.Cwd.DB.Exec(`UPDATE jobs SET state = 'paused' WHERE id = 'j1'`); err != nil {
		t.Fatal(err)
	}
	before := h.ct.text
	mustOK(t, sched.RunJob(key, runOpts(h, nil, &fakeSpawn{}, fetchOpts{})))
	rec := runEvents(t, h, "")[0]
	if rec.Args["status"] != "skip" || !regexp.MustCompile(`paused`).MatchString(toString(rec.Args["reason"])) {
		t.Fatalf("record %v", rec.Args)
	}
	if h.ct.text != before {
		t.Fatal("line must stay for the list to flag")
	}
}

// --- logs ---

func TestLogsPruneToTheNewestTwenty(t *testing.T) {
	h, key := setupJob(t, "/ws/p1", "", nil)
	dir := filepath.Join(h.home, "runs", "cwd-"+sched.CwdHash("/ws/p1"), "j1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 25; i++ {
		name := time.Date(2026, 8, 14, 0, i, 0, 0, time.UTC).Format("2006-01-02T15-04-05-000Z") + ".log"
		if err := os.WriteFile(filepath.Join(dir, name), []byte("old"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	spawn := &fakeSpawn{result: sched.SpawnResult{Exit: 0}}
	mustOK(t, sched.RunJob(key, runOpts(h, nil, spawn, fetchOpts{})))
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var logs []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".log") {
			logs = append(logs, e.Name())
		}
	}
	if len(logs) != 20 {
		t.Fatalf("logs = %d, want 20 (pruned)", len(logs))
	}
	foundNew, foundOld := false, false
	for _, l := range logs {
		if l == logName {
			foundNew = true
		}
		if l == "2026-08-14T00-00-00-000Z.log" {
			foundOld = true
		}
	}
	if !foundNew {
		t.Fatal("new log must be kept")
	}
	if foundOld {
		t.Fatal("oldest must be dropped")
	}
}

// --- lock contention (the flock mechanism, held in-process) ---

func TestLockHeldRecordsSkipWithoutRunningTheWorker(t *testing.T) {
	h, key := setupJob(t, "/ws/l1", "", nil)
	lockDir := filepath.Join(h.home, "locks")
	if err := os.MkdirAll(lockDir, 0o755); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(lockDir, strings.ReplaceAll(key, ":", "_")+".lock")
	fd, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer fd.Close()
	if err := syscall.Flock(int(fd.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatalf("holder flock: %v", err)
	}
	defer syscall.Flock(int(fd.Fd()), syscall.LOCK_UN)

	spawn := &fakeSpawn{}
	before := h.ct.text
	mustOK(t, sched.RunJob(key, runOpts(h, nil, spawn, fetchOpts{})))
	if len(spawn.calls) != 0 {
		t.Fatal("no spawn while the lock is held")
	}
	if h.ct.text != before {
		t.Fatal("crontab must stay untouched")
	}
	rec := runEvents(t, h, "")[0]
	if rec.Args["status"] != "skip" || !regexp.MustCompile(`lock held`).MatchString(toString(rec.Args["reason"])) {
		t.Fatalf("record %v", rec.Args)
	}
}

// --- loud failures ---

func TestCrontabListFailureLoudNothingRecorded(t *testing.T) {
	h, key := setupJob(t, "/ws/l2", "", nil)
	fc := failingCrontab{listErr: jsonErr("crontab list failed (exit 1): PAM: user not authorized")}
	err := sched.RunJob(key, sched.RunOpts{
		Home:      h.home,
		Crontab:   fc,
		Fetch:     fakeFetch(nil, fetchOpts{}),
		Spawn:     (&fakeSpawn{}).spawn,
		WorkerCmd: []string{"/x/looper"},
		SwapURL:   "http://127.0.0.1:8090",
		Now:       func() time.Time { return runnerNow },
	})
	if err == nil || !regexp.MustCompile(`crontab list failed`).MatchString(err.Error()) {
		t.Fatalf("error %v", err)
	}
	if rec := runEvents(t, h, ""); len(rec) != 0 {
		t.Fatalf("fail closed: nothing recorded, got %d", len(rec))
	}
	if !strings.Contains(h.ct.text, "pane-scheduler:") {
		t.Fatal("crontab must be untouched")
	}
}

// --- plumbing ---

func toString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
