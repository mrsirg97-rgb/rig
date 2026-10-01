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

	"github.com/mrsirg97-rgb/rig/v2/models"
	sched "github.com/mrsirg97-rgb/rig/v2/store/scheduler"
)

var runnerNow = time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)

const logName = "2026-08-15T12-00-00-000Z.log"

var modelsFixture = []struct {
	ID     string
	Alias  []string
	Status string
}{
	{ID: "qwen3.8-27b", Alias: []string{"qwen3.8"}},
	{ID: "qwen3.8-27b-workers", Alias: []string{"qwen3.8-workers"}},
}

type swapModel struct {
	ID     string
	Alias  []string
	Status string
}

func swapModelsJSON(all []swapModel) string {
	type alias struct {
		Aliases []string `json:"aliases"`
	}
	type meta struct {
		LLamaSwap alias `json:"llamaswap"`
	}
	type status struct {
		Value string `json:"value"`
	}
	type model struct {
		ID     string `json:"id"`
		Meta   meta   `json:"meta"`
		Status status `json:"status"`
	}
	var data []model
	for _, m := range all {
		st := m.Status
		if st == "" {
			st = "unloaded"
		}
		data = append(data, model{ID: m.ID, Meta: meta{LLamaSwap: alias{Aliases: m.Alias}}, Status: status{Value: st}})
	}
	b, _ := json.Marshal(map[string]any{"data": data, "object": "list"})
	return string(b)
}

func modelsJSON(statuses map[string]string) string {
	var all []swapModel
	for _, m := range modelsFixture {
		all = append(all, swapModel{ID: m.ID, Alias: m.Alias, Status: statuses[m.ID]})
	}
	return swapModelsJSON(all)
}

func modelTable(t *testing.T, ids ...string) func() models.Table {
	t.Helper()
	var rows []models.Model
	for _, id := range ids {
		rows = append(rows, models.Model{
			ID: id, Window: 65536, MaxTokens: 8192, Reserve: 8192, KeepRecent: 16384,
			Role: models.RoleWorker,
		})
	}
	tbl, err := models.New(rows...)
	if err != nil {
		t.Fatal(err)
	}
	return func() models.Table { return tbl }
}

func runningJSON(models ...string) string {
	type entry struct {
		Model string `json:"model"`
	}
	var rs []entry
	for _, m := range models {
		rs = append(rs, entry{Model: m})
	}
	b, _ := json.Marshal(map[string]any{"running": rs})
	return string(b)
}

type fetchOpts struct {
	failing  string
	statuses map[string]string
	models   []swapModel
	slots    [][]bool
}

func fakeFetch(running []string, opts fetchOpts) func(url string) (json.RawMessage, error) {
	return func(url string) (json.RawMessage, error) {
		if opts.failing != "" {
			return nil, jsonError(opts.failing)
		}
		switch {
		case strings.HasSuffix(url, "/v1/models"):
			if opts.models != nil {
				return json.RawMessage(swapModelsJSON(opts.models)), nil
			}
			return json.RawMessage(modelsJSON(opts.statuses)), nil
		case strings.HasSuffix(url, "/running"):
			return json.RawMessage(runningJSON(running...)), nil
		case strings.Contains(url, "/upstream/"):
			served := []bool{false}
			if len(opts.slots) > 0 {
				served = opts.slots[0]
			}
			type slot struct {
				ID           int  `json:"id"`
				IsProcessing bool `json:"is_processing"`
			}
			var out []slot
			for i, pr := range served {
				out = append(out, slot{ID: i, IsProcessing: pr})
			}
			b, _ := json.Marshal(out)
			return json.RawMessage(b), nil
		}
		return nil, jsonError("unexpected url " + url)
	}
}

type jsonErr string

func (e jsonErr) Error() string { return string(e) }

func jsonError(s string) error { return jsonErr(s) }

type fakeSpawn struct {
	calls  []fakeCall
	result sched.SpawnResult
	err    error
}

type fakeCall struct {
	Argv []string
	Cwd  string
}

func (f *fakeSpawn) spawn(ctx context.Context, argv []string, cwd string, env []string, observe func([]byte)) (sched.SpawnResult, error) {
	f.calls = append(f.calls, fakeCall{Argv: argv, Cwd: cwd})
	return f.result, f.err
}

func realCwd(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return real
}

func setupJob(t *testing.T, cwd string, mutate func(in *sched.CreateInput)) (h *harness, key string) {
	t.Helper()
	return setupJobHome(t, cwd, "", mutate)
}

func setupJobHome(t *testing.T, cwd, rigHome string, mutate func(in *sched.CreateInput)) (h *harness, key string) {
	t.Helper()
	h = newHarnessRigHome(t, cwd, rigHome)
	in := sched.CreateInput{
		Name: "job", Prompt: "do the thing", Cron: "0 */4 * * *",
		Model: "qwen3.8-workers", Busy: "skip",
	}
	if mutate != nil {
		mutate(&in)
	}
	reply, err := h.create(in)
	if err != nil {
		t.Fatalf("create: %v (%s)", err, reply)
	}
	return h, "j1"
}

func runOpts(h *harness, running []string, spawn *fakeSpawn, extra fetchOpts) sched.RunOpts {

	return sched.RunOpts{
		Home:      h.home,
		RigHome:   h.rigHome,
		Crontab:   h.ct,
		Fetch:     fakeFetch(running, extra),
		Spawn:     spawn.spawn,
		WorkerCmd: []string{"/x/rig"},
		SwapURL:   "http://127.0.0.1:8090",
		Now:       func() time.Time { return runnerNow },
		Sandbox:   "off",
	}
}

func runEvents(t *testing.T, h *harness, scope string) []struct {
	TS      string
	Args    map[string]any
	Session string
} {
	t.Helper()

	db := h.db
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

func TestParseKeyJNAndGarbageRefuses(t *testing.T) {
	id, err := sched.ParseKey("j1")
	if err != nil || id != "j1" {
		t.Fatalf("ParseKey(j1) = %q, %v", id, err)
	}
	for _, bad := range []string{"garbage", "cwd-b01229c83837:j42"} {
		_, err := sched.ParseKey(bad)
		if err == nil {
			t.Fatalf("ParseKey(%q) must refuse", bad)
		}
	}
}

func TestOwnModelResidentViaAliasRunsArgvCwdReportBackLogOKRecord(t *testing.T) {
	h, key := setupJob(t, realCwd(t, "job"), nil)
	spawn := &fakeSpawn{result: sched.SpawnResult{Exit: 0, Stdout: "hello\n"}}
	err := sched.RunJob(key, runOpts(h, []string{"qwen3.8-27b"}, spawn, fetchOpts{
		statuses: map[string]string{"qwen3.8-27b-workers": "loaded"},
	}))
	mustOK(t, err)

	c := spawn.calls[0]
	if len(c.Argv) < 5 || c.Argv[0] != "/x/rig" || c.Argv[1] != "-p" {
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

	baseIdx := -1
	for i, a := range c.Argv {
		if a == "-base-url" {
			baseIdx = i
		}
	}
	if baseIdx < 0 || baseIdx+1 >= len(c.Argv) || c.Argv[baseIdx+1] != "http://127.0.0.1:8090/v1" {
		t.Fatalf("argv missing the worker's swap endpoint: %v", c.Argv)
	}
	if c.Cwd != h.sessCwd {
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
	row := jobsRow(t, h, "j1")
	if row["last_status"] != "ok" {
		t.Fatalf("last_status %v", row["last_status"])
	}
	if !strings.Contains(h.ct.text, "rig-scheduler:"+sched.TagHome(h.rigHome)+":") {
		t.Fatal("recurring line must stay")
	}
}

func TestNothingResidentRuns(t *testing.T) {
	h, key := setupJob(t, realCwd(t, "job"), nil)
	spawn := &fakeSpawn{result: sched.SpawnResult{Exit: 0}}
	mustOK(t, sched.RunJob(key, runOpts(h, nil, spawn, fetchOpts{})))
	if len(spawn.calls) != 1 {
		t.Fatalf("spawn calls = %d", len(spawn.calls))
	}
}

func TestSomethingElseResidentSkipRecordsAndSpawnsNothing(t *testing.T) {
	h, key := setupJob(t, realCwd(t, "job"), nil)
	spawn := &fakeSpawn{}
	before := h.ct.text
	err := sched.RunJob(key, runOpts(h, []string{"qwen3.8-27b"}, spawn, fetchOpts{}))
	mustOK(t, err)
	rec := runEvents(t, h, "")[0]
	if rec.Args["status"] != "skip" {
		t.Fatalf("status %v", rec.Args["status"])
	}
	if !regexp.MustCompile(`held by`).MatchString(toString(rec.Args["reason"])) {
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

func TestOwnModelLoadedIdleWhileAnotherResidentRuns(t *testing.T) {
	h, key := setupJob(t, realCwd(t, "job"), nil)
	spawn := &fakeSpawn{result: sched.SpawnResult{Exit: 0}}
	err := sched.RunJob(key, runOpts(h, []string{"qwen3.8-27b"}, spawn, fetchOpts{
		statuses: map[string]string{"qwen3.8-27b-workers": "loaded"},
	}))
	mustOK(t, err)
	if len(spawn.calls) != 1 {
		t.Fatalf("spawn calls = %d, want 1", len(spawn.calls))
	}
}

func TestOwnModelNotLoadedSomethingElseResidentSkips(t *testing.T) {
	h, key := setupJob(t, realCwd(t, "job"), nil)
	spawn := &fakeSpawn{}
	err := sched.RunJob(key, runOpts(h, []string{"qwen3.8-27b"}, spawn, fetchOpts{
		statuses: map[string]string{"qwen3.8-27b": "loaded"},
	}))
	mustOK(t, err)
	rec := runEvents(t, h, "")[0]
	if rec.Args["status"] != "skip" || !regexp.MustCompile(`held by`).MatchString(toString(rec.Args["reason"])) {
		t.Fatalf("record %v", rec.Args)
	}
	if len(spawn.calls) != 0 {
		t.Fatal("no spawn")
	}
}

func TestBusyCheckFetchFailureFailsClosedWithReason(t *testing.T) {
	h, key := setupJob(t, realCwd(t, "job"), nil)
	spawn := &fakeSpawn{}
	err := sched.RunJob(key, runOpts(h, nil, spawn, fetchOpts{failing: "fetch failed: ECONNREFUSED"}))
	mustOK(t, err)
	rec := runEvents(t, h, "")[0]
	if rec.Args["status"] != "skip" {
		t.Fatalf("status %v", rec.Args["status"])
	}
	if !regexp.MustCompile(`gate check failed`).MatchString(toString(rec.Args["reason"])) {
		t.Fatalf("reason %v", rec.Args["reason"])
	}
	if !regexp.MustCompile(`ECONNREFUSED`).MatchString(toString(rec.Args["reason"])) {
		t.Fatalf("reason must carry the failure: %v", rec.Args["reason"])
	}
	if len(spawn.calls) != 0 {
		t.Fatal("no spawn")
	}
}

func TestWorkerExitNonZeroRecordsFailWithExitLogCarriesStderr(t *testing.T) {
	h, key := setupJob(t, realCwd(t, "job"), nil)
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
	row := jobsRow(t, h, "j1")
	if row["last_status"] != "fail" || row["last_exit"] != int64(3) {
		t.Fatalf("row %v", row)
	}
}

func TestOnceFireConsumesTheLineAndMarksDone(t *testing.T) {
	h, key := setupJob(t, realCwd(t, "job"), func(in *sched.CreateInput) {
		in.Cron = "once"
		in.At = "2026-08-16T03:07:00Z"
	})
	if !strings.Contains(h.ct.text, "rig-scheduler:"+sched.TagHome(h.rigHome)+":") {
		t.Fatal("once line present before fire")
	}
	spawn := &fakeSpawn{result: sched.SpawnResult{Exit: 0}}
	mustOK(t, sched.RunJob(key, runOpts(h, nil, spawn, fetchOpts{})))
	if strings.Contains(h.ct.text, "rig-scheduler:"+sched.TagHome(h.rigHome)+":") {
		t.Fatal("line must be consumed")
	}
	row := jobsRow(t, h, "j1")
	if row["state"] != "done" {
		t.Fatalf("state %v", row["state"])
	}
}

func TestOnceWithFailingWorkerDoneWithFailNoRetry(t *testing.T) {
	h, key := setupJob(t, realCwd(t, "job"), func(in *sched.CreateInput) {
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
	row := jobsRow(t, h, "j1")
	if row["state"] != "done" {
		t.Fatalf("state %v", row["state"])
	}
	if strings.Contains(h.ct.text, "rig-scheduler:"+sched.TagHome(h.rigHome)+":") {
		t.Fatal("line must be consumed")
	}
}

func TestOnceDoneIsAnEventAndSurvivesTheNextFold(t *testing.T) {
	h, key := setupJob(t, realCwd(t, "job"), func(in *sched.CreateInput) {
		in.Cron = "once"
		in.At = "2026-08-16T03:07:00Z"
	})
	spawn := &fakeSpawn{result: sched.SpawnResult{Exit: 0}}
	mustOK(t, sched.RunJob(key, runOpts(h, nil, spawn, fetchOpts{})))
	if _, err := h.create(sched.CreateInput{Name: "later", Prompt: "p", Cron: "0 */4 * * *", Model: "qwen3.8-workers"}); err != nil {
		t.Fatal(err)
	}
	row := jobsRow(t, h, "j1")
	if row["state"] != "done" {
		t.Fatalf("state after refold %v", row["state"])
	}
	var ops []string
	rows, err := h.db.DB.Query(`SELECT op FROM events ORDER BY seq`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var op string
		if err := rows.Scan(&op); err != nil {
			t.Fatal(err)
		}
		ops = append(ops, op)
	}
	if strings.Join(ops, ",") != "create,run,done,create" {
		t.Fatalf("ops %v", ops)
	}
	out, err := h.list()
	mustOK(t, err)
	if !strings.Contains(out, "j1 job done") || strings.Contains(out, "no crontab line") {
		t.Fatalf("list:\n%s", out)
	}
}

func TestZombieLineWithMissingRowLineDeletedSkipRecorded(t *testing.T) {
	h, key := setupJob(t, realCwd(t, "job"), nil)

	if _, err := h.db.DB.Exec(`DELETE FROM jobs WHERE id = 'j1'`); err != nil {
		t.Fatal(err)
	}
	mustOK(t, sched.RunJob(key, runOpts(h, nil, &fakeSpawn{}, fetchOpts{})))
	rec := runEvents(t, h, "")[0]
	if rec.Args["status"] != "skip" || !regexp.MustCompile(`no job row`).MatchString(toString(rec.Args["reason"])) {
		t.Fatalf("record %v", rec.Args)
	}
	if strings.Contains(h.ct.text, "rig-scheduler:"+sched.TagHome(h.rigHome)+":") {
		t.Fatal("zombie line must be deleted")
	}
}

func TestCrashWindowRowDoneButLineAliveLineDeletedSkipRecorded(t *testing.T) {
	h, key := setupJob(t, realCwd(t, "job"), func(in *sched.CreateInput) {
		in.Cron = "once"
		in.At = "2026-08-16T03:07:00Z"
	})
	if _, err := h.db.DB.Exec(`UPDATE jobs SET state = 'done' WHERE id = 'j1'`); err != nil {
		t.Fatal(err)
	}
	mustOK(t, sched.RunJob(key, runOpts(h, nil, &fakeSpawn{}, fetchOpts{})))
	rec := runEvents(t, h, "")[0]
	if rec.Args["status"] != "skip" || !regexp.MustCompile(`already done`).MatchString(toString(rec.Args["reason"])) {
		t.Fatalf("record %v", rec.Args)
	}
	if strings.Contains(h.ct.text, "rig-scheduler:"+sched.TagHome(h.rigHome)+":") {
		t.Fatal("line must be healed")
	}
}

func TestPausedRowLineDriftedActiveSkipLineUntouched(t *testing.T) {
	h, key := setupJob(t, realCwd(t, "job"), nil)
	if _, err := h.db.DB.Exec(`UPDATE jobs SET state = 'paused' WHERE id = 'j1'`); err != nil {
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

func TestLogsPruneToTheNewestTwenty(t *testing.T) {
	h, key := setupJob(t, realCwd(t, "job"), nil)
	dir := filepath.Join(h.home, "runs", "j1")
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

func TestLockHeldRecordsSkipWithoutRunningTheWorker(t *testing.T) {
	h, key := setupJob(t, realCwd(t, "job"), nil)
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

func TestCrontabListFailureLoudNothingRecorded(t *testing.T) {
	h, key := setupJob(t, realCwd(t, "job"), nil)
	fc := failingCrontab{listErr: jsonErr("crontab list failed (exit 1): PAM: user not authorized")}
	err := sched.RunJob(key, sched.RunOpts{
		Home:      h.home,
		RigHome:   h.rigHome,
		Crontab:   fc,
		Fetch:     fakeFetch(nil, fetchOpts{}),
		Spawn:     (&fakeSpawn{}).spawn,
		WorkerCmd: []string{"/x/rig"},
		SwapURL:   "http://127.0.0.1:8090",
		Now:       func() time.Time { return runnerNow },
	})
	if err == nil || !regexp.MustCompile(`crontab list failed`).MatchString(err.Error()) {
		t.Fatalf("error %v", err)
	}
	if rec := runEvents(t, h, ""); len(rec) != 0 {
		t.Fatalf("fail closed: nothing recorded, got %d", len(rec))
	}
	if !strings.Contains(h.ct.text, "rig-scheduler:"+sched.TagHome(h.rigHome)+":") {
		t.Fatal("crontab must be untouched")
	}
}

func toString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func TestRunJobRefusesAReplacedOrMissingCwd(t *testing.T) {
	for _, tc := range []struct {
		name string
		swap func(t *testing.T, cwd string)
	}{
		{name: "replaced", swap: func(t *testing.T, cwd string) {
			outside := t.TempDir()
			if err := os.Rename(cwd, cwd+".old"); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, cwd); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "missing", swap: func(t *testing.T, cwd string) {
			if err := os.RemoveAll(cwd); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cwd := realCwd(t, "job")
			h, key := setupJob(t, cwd, nil)
			tc.swap(t, cwd)
			spawn := &fakeSpawn{result: sched.SpawnResult{Exit: 0}}
			before := h.ct.text
			mustOK(t, sched.RunJob(key, runOpts(h, nil, spawn, fetchOpts{})))
			if len(spawn.calls) != 0 {
				t.Fatal("a replaced or missing cwd must not spawn")
			}
			rec := runEvents(t, h, "")[0]
			if rec.Args["status"] != "skip" {
				t.Fatalf("status %v", rec.Args["status"])
			}
			if !regexp.MustCompile(`cwd`).MatchString(toString(rec.Args["reason"])) {
				t.Fatalf("the skip reason must name the cwd rule: %v", rec.Args["reason"])
			}
			if h.ct.text != before {
				t.Fatal("the crontab line must stay untouched")
			}
		})
	}
}

func TestRealSpawnCapturesTheKillingSignal(t *testing.T) {
	res, err := sched.RealSpawn(context.Background(),
		[]string{"/bin/sh", "-c", "kill -9 $$"}, "", nil, nil)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if res.Exit != -1 {
		t.Errorf("exit = %d, want -1 for a signal death", res.Exit)
	}
	if res.Signal != syscall.SIGKILL {
		t.Errorf("signal = %v, want SIGKILL", res.Signal)
	}
}

func TestUnnamedJobFiresOnTheResidentModel(t *testing.T) {
	h, key := setupJob(t, realCwd(t, "job"), func(in *sched.CreateInput) { in.Model = "" })
	spawn := &fakeSpawn{result: sched.SpawnResult{Exit: 0}}
	opts := runOpts(h, []string{"glm5.3-flash"}, spawn, fetchOpts{})
	opts.Models = modelTable(t, "glm5.3-flash")
	mustOK(t, sched.RunJob(key, opts))
	if len(spawn.calls) != 1 {
		t.Fatalf("spawn calls = %d, want 1", len(spawn.calls))
	}
	tail := spawn.calls[0].Argv[len(spawn.calls[0].Argv)-2:]
	if tail[0] != "-model" || tail[1] != "glm5.3-flash" {
		t.Fatalf("an unnamed job must fire on the resident model, argv tail %v", tail)
	}
	rec := runEvents(t, h, "")[0]
	if rec.Args["status"] != "ok" || rec.Args["model"] != "glm5.3-flash" {
		t.Fatalf("the run record must name the resolved model: %v", rec.Args)
	}
	out, err := h.runs("j1", 5)
	if err != nil || !strings.Contains(out, "model glm5.3-flash") {
		t.Fatalf("runs must show the resolved model: %q, %v", out, err)
	}
	logPath, _ := rec.Args["log"].(string)
	log, err := os.ReadFile(filepath.Join(h.home, logPath))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(log), "model=glm5.3-flash") {
		t.Fatal("the fire log must name the resolved model")
	}
}

func TestUnnamedJobResidentAliasFiresOnTheTableRow(t *testing.T) {
	h, key := setupJob(t, realCwd(t, "job"), func(in *sched.CreateInput) { in.Model = "" })
	spawn := &fakeSpawn{result: sched.SpawnResult{Exit: 0}}
	opts := runOpts(h, []string{"glm5.3-flash"}, spawn, fetchOpts{
		models: []swapModel{{ID: "glm5.3-flash", Alias: []string{"ox-alpha"}}},
	})
	opts.Models = modelTable(t, "ox-alpha")
	mustOK(t, sched.RunJob(key, opts))
	if len(spawn.calls) != 1 {
		t.Fatalf("spawn calls = %d, want 1", len(spawn.calls))
	}
	tail := spawn.calls[0].Argv[len(spawn.calls[0].Argv)-2:]
	if tail[0] != "-model" || tail[1] != "ox-alpha" {
		t.Fatalf("the resident's alias must fire on the models-table row, argv tail %v", tail)
	}
	rec := runEvents(t, h, "")
	if rec[0].Args["status"] != "ok" || rec[0].Args["model"] != "ox-alpha" {
		t.Fatalf("the run record must name the row id: %v", rec[0].Args)
	}
	logPath, _ := rec[0].Args["log"].(string)
	log, err := os.ReadFile(filepath.Join(h.home, logPath))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(log), "model=ox-alpha") {
		t.Fatal("the fire log must name the row id")
	}
	out, err := h.runs("j1", 5)
	if err != nil || !strings.Contains(out, "model ox-alpha") {
		t.Fatalf("runs must show the row id: %q, %v", out, err)
	}
}

func TestUnnamedJobResidentRowFiresUnchanged(t *testing.T) {
	h, key := setupJob(t, realCwd(t, "job"), func(in *sched.CreateInput) { in.Model = "" })
	spawn := &fakeSpawn{result: sched.SpawnResult{Exit: 0}}
	opts := runOpts(h, []string{"qwen3.8-27b"}, spawn, fetchOpts{})
	opts.Models = modelTable(t, "qwen3.8-27b")
	mustOK(t, sched.RunJob(key, opts))
	if len(spawn.calls) != 1 {
		t.Fatalf("spawn calls = %d, want 1", len(spawn.calls))
	}
	tail := spawn.calls[0].Argv[len(spawn.calls[0].Argv)-2:]
	if tail[0] != "-model" || tail[1] != "qwen3.8-27b" {
		t.Fatalf("a resident id that is itself a row must fire unchanged, argv tail %v", tail)
	}
	rec := runEvents(t, h, "")
	if rec[0].Args["status"] != "ok" || rec[0].Args["model"] != "qwen3.8-27b" {
		t.Fatalf("the run record must name the row id: %v", rec[0].Args)
	}
}

func TestUnnamedJobResidentWithoutARowSkipsNamingItAndTheKnownRows(t *testing.T) {
	h, key := setupJob(t, realCwd(t, "job"), func(in *sched.CreateInput) { in.Model = "" })
	spawn := &fakeSpawn{}
	opts := runOpts(h, []string{"glm5.3-flash"}, spawn, fetchOpts{})
	opts.Models = modelTable(t, "dsv4", "ox-alpha")
	mustOK(t, sched.RunJob(key, opts))
	if len(spawn.calls) != 0 {
		t.Fatal("a resident with no row must never spawn")
	}
	rec := runEvents(t, h, "")
	reason := toString(rec[0].Args["reason"])
	if rec[0].Args["status"] != "skip" || !strings.Contains(reason, "glm5.3-flash") {
		t.Fatalf("the skip must name the resident: %v", rec[0].Args)
	}
	if !strings.Contains(reason, "known:") || !strings.Contains(reason, "dsv4") || !strings.Contains(reason, "ox-alpha") {
		t.Fatalf("the skip must name the known rows: %v", reason)
	}
}

func TestUnnamedJobNothingResidentFiresOnTheDefault(t *testing.T) {
	h, key := setupJob(t, realCwd(t, "job"), func(in *sched.CreateInput) { in.Model = "" })
	spawn := &fakeSpawn{result: sched.SpawnResult{Exit: 0}}
	opts := runOpts(h, nil, spawn, fetchOpts{})
	opts.DefaultModel = "dsv4"
	mustOK(t, sched.RunJob(key, opts))
	if len(spawn.calls) != 1 {
		t.Fatalf("spawn calls = %d, want 1", len(spawn.calls))
	}
	tail := spawn.calls[0].Argv[len(spawn.calls[0].Argv)-2:]
	if tail[0] != "-model" || tail[1] != "dsv4" {
		t.Fatalf("nothing resident must fall to the wired default, argv tail %v", tail)
	}
	rec := runEvents(t, h, "")[0]
	if rec.Args["status"] != "ok" || rec.Args["model"] != "dsv4" {
		t.Fatalf("the run record must name the default: %v", rec.Args)
	}
}

func TestUnnamedJobNoDefaultNothingResidentSkipsNamed(t *testing.T) {
	h, key := setupJob(t, realCwd(t, "job"), func(in *sched.CreateInput) { in.Model = "" })
	spawn := &fakeSpawn{}
	mustOK(t, sched.RunJob(key, runOpts(h, nil, spawn, fetchOpts{})))
	if len(spawn.calls) != 0 {
		t.Fatal("no model anywhere must not spawn")
	}
	rec := runEvents(t, h, "")[0]
	if rec.Args["status"] != "skip" || !strings.Contains(toString(rec.Args["reason"]), "no model") {
		t.Fatalf("the skip must name the missing model: %v", rec.Args)
	}
}

func TestNamedModelAnotherResidentStillSkipsNamingTheHolder(t *testing.T) {
	h, key := setupJob(t, realCwd(t, "job"), func(in *sched.CreateInput) { in.Model = "dsv4" })
	spawn := &fakeSpawn{}
	mustOK(t, sched.RunJob(key, runOpts(h, []string{"glm5.3-flash"}, spawn, fetchOpts{})))
	if len(spawn.calls) != 0 {
		t.Fatal("a named model never evicts the resident")
	}
	rec := runEvents(t, h, "")[0]
	if rec.Args["status"] != "skip" {
		t.Fatalf("status %v", rec.Args["status"])
	}
	if !strings.Contains(toString(rec.Args["reason"]), "held by glm5.3-flash") {
		t.Fatalf("the skip must name the holder: %v", rec.Args["reason"])
	}
}
