package scheduler_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/store"
	sched "github.com/mrsirg97-rgb/rig/store/scheduler"
)

var nowFixed = time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)

const runnerCmd = "/x/rig run-job"

type fakeCrontab struct {
	mu   sync.Mutex
	text string
}

func newFakeCrontab(initial string) *fakeCrontab {
	if initial == "" {
		initial = "SHELL=/bin/bash\n"
	}
	return &fakeCrontab{text: initial}
}

func (f *fakeCrontab) List() (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.text, nil
}

func (f *fakeCrontab) Install(text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.text = text
	return nil
}

type failingCrontab struct {
	listErr    error
	installErr error
}

func (f failingCrontab) List() (string, error) { return "", f.listErr }
func (f failingCrontab) Install(string) error  { return f.installErr }

type harness struct {
	home    string
	db      sched.DB
	ct      *fakeCrontab
	sessCwd string
}

func newHarness(t *testing.T, sessionCwd string) *harness {
	t.Helper()
	home := t.TempDir()
	open := func(name string) sched.DB {
		db, _, _, err := store.Open(filepath.Join(home, name), sched.Statements(), sched.SchemaVersion)
		if err != nil {
			t.Fatalf("open %s: %v", name, err)
		}
		return db
	}
	db := open("global.sqlite")
	return &harness{
		home:    home,
		db:      db,
		ct:      newFakeCrontab("SHELL=/bin/bash\n"),
		sessCwd: sessionCwd,
	}
}

func (h *harness) create(in sched.CreateInput) (string, error) {
	return sched.Create(context.Background(), h.db, h.ct, in, h.sessCwd, "sess-core", runnerCmd, func() time.Time { return nowFixed })
}

func (h *harness) list() (string, error) {
	return sched.List(context.Background(), h.db, h.ct, h.sessCwd, nil, func() time.Time { return nowFixed })
}

func (h *harness) runs(id string, n int) (string, error) {
	return sched.Runs(context.Background(), h.db, id, n)
}

func jobsRow(t *testing.T, h *harness, id string) map[string]any {
	t.Helper()
	var row map[string]any
	scanRow(t, h.db, "jobs", "id = ?", []any{id}, &row)
	return row
}

func scanRow(t *testing.T, db sched.DB, table, where string, args []any, out *map[string]any) {
	t.Helper()
	rows, err := db.DB.Query(`SELECT * FROM `+table+` WHERE `+where, args...)
	if err != nil {
		t.Fatalf("query %s: %v", table, err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var vals []any
	for rows.Next() {
		vals = make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(vals) == 0 {
		return
	}
	m := map[string]any{}
	for i, c := range cols {
		m[c] = vals[i]
	}
	*out = m
}

func argsJSON(t *testing.T, args string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(args), &m); err != nil {
		t.Fatalf("args %q: %v", args, err)
	}
	return m
}

func mustErr(t *testing.T, err error, re string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error matching /%s/, got nil", re)
	}
	if !regexp.MustCompile(re).MatchString(err.Error()) {
		t.Fatalf("error %q does not match /%s/", err.Error(), re)
	}
}

func contains(t *testing.T, hay, needle string) {
	t.Helper()
	if !strings.Contains(hay, needle) {
		t.Fatalf("%q does not contain %q", hay, needle)
	}
}

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func int64ptr(v int64) *int64 { return &v }

func index(t *testing.T, hay, needle string) int {
	t.Helper()
	return strings.Index(hay, needle)
}

func TestCreateMintsJ1WritesStoreAndTaggedLineForeignIntact(t *testing.T) {
	h := newHarness(t, "/ws/a")
	reply, err := h.create(sched.CreateInput{Model: "w", Name: "nightly", Prompt: "do it", Cron: "0 */4 * * *"})
	mustOK(t, err)
	contains(t, reply, "created j1 'nightly'")

	contains(t, reply, "next 2026-08-15T16:00:00Z")
	line := `0 */4 * * * ` + runnerCmd + ` j1  # pane-scheduler:j1`
	contains(t, h.ct.text, line)
	contains(t, h.ct.text, "SHELL=/bin/bash")
	if _, err := os.Stat(filepath.Join(h.home, "global.sqlite")); err != nil {
		t.Fatal("global store file missing")
	}
}

func TestCreateExplicitCwdIsTheJobField(t *testing.T) {
	h := newHarness(t, "/ws/sess")
	reply, err := h.create(sched.CreateInput{Model: "w", Name: "gw", Prompt: "p", Cron: "0 0 * * *"})
	mustOK(t, err)
	contains(t, reply, "/ws/sess")

	_, err = h.create(sched.CreateInput{Model: "w", Name: "gw2", Prompt: "p", Cron: "1 0 * * *", Cwd: "/shop/make-money"})
	mustOK(t, err)
	if got := jobsRow(t, h, "j2")["cwd"]; got != "/shop/make-money" {
		t.Fatalf("explicit cwd not honored: %v", got)
	}
	list, _ := h.list()
	contains(t, list, "j1")
}

func TestRemovedJobsNameMayBeRecreatedIdsStillMintForward(t *testing.T) {
	h := newHarness(t, "/ws/nm")
	_, err := h.create(sched.CreateInput{Model: "w", Name: "recycle", Prompt: "p", Cron: "0 11 * * *"})
	mustOK(t, err)
	if _, err := sched.Remove(context.Background(), h.db, h.ct, "j1", h.sessCwd, "sess-core"); err != nil {
		t.Fatal(err)
	}
	_, err = h.create(sched.CreateInput{Model: "w", Name: "recycle", Prompt: "q", Cron: "1 11 * * *"})
	mustOK(t, err)
	row := jobsRow(t, h, "j2")
	if row == nil || row["name"] != "recycle" || row["state"] != "active" {
		t.Fatalf("recreated row: %v", row)
	}
}

func TestCreateRefusesDuplicateNameNothingWritten(t *testing.T) {
	h := newHarness(t, "/ws/b")
	if _, err := h.create(sched.CreateInput{Model: "w", Name: "nightly", Prompt: "p", Cron: "0 0 * * *"}); err != nil {
		t.Fatal(err)
	}
	before := h.ct.text
	_, err := h.create(sched.CreateInput{Model: "w", Name: "nightly", Prompt: "q", Cron: "1 0 * * *"})
	mustErr(t, err, `already exists`)
	if h.ct.text != before {
		t.Fatal("crontab must be untouched")
	}
	var n int
	if err := h.db.DB.QueryRow(`SELECT count(*) FROM events`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("events = %d, want 1", n)
	}
}

func TestCreateWithoutModelRefusesNamingTheCaller(t *testing.T) {
	h := newHarness(t, "/ws/d2")
	before := h.ct.text
	_, err := h.create(sched.CreateInput{Name: "nomodel", Prompt: "p", Cron: "0 0 * * *"})
	mustErr(t, err, `create requires a non-empty model \(the fleet's model, or the job's own\)`)
	if h.ct.text != before {
		t.Fatal("crontab must be untouched")
	}
	var n int
	if err := h.db.QueryRow(`SELECT COUNT(*) FROM jobs`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("jobs = %d rows, want none (the refusal is before the write)", n)
	}
}

func TestCreateValidatesCronBeforeWritingAnything(t *testing.T) {
	h := newHarness(t, "/ws/d")
	before := h.ct.text
	_, err := h.create(sched.CreateInput{Model: "w", Name: "bad", Prompt: "p", Cron: "* * * * "})
	mustErr(t, err, `cron:`)
	if h.ct.text != before {
		t.Fatal("crontab must be untouched")
	}
}

func TestCreateOnceTranslatesToCronFieldsMissingAtRefuses(t *testing.T) {
	h := newHarness(t, "/ws/e")
	_, err := h.create(sched.CreateInput{Model: "w", Name: "soon", Prompt: "p", Cron: "once"})
	mustErr(t, err, `at`)
	_, err = h.create(sched.CreateInput{Model: "w", Name: "soon", Prompt: "p", Cron: "once", At: "2026-08-16T03:07:00Z"})
	mustOK(t, err)
	row := jobsRow(t, h, "j1")
	if row["cron"] != "7 3 16 8 *" {
		t.Fatalf("cron %v", row["cron"])
	}
	if row["at"] != "2026-08-16T03:07:00Z" {
		t.Fatalf("at %v (normalized ISO)", row["at"])
	}
	contains(t, h.ct.text, `7 3 16 8 * `+runnerCmd+` j1  # pane-scheduler:j1`)
}

func TestListGroupsByDirectoryThisCwdFirst(t *testing.T) {
	h := newHarness(t, "/ws/f")
	if _, err := h.create(sched.CreateInput{Model: "w", Name: "cw", Prompt: "p", Cron: "0 0 * * *", Cwd: "/ws/f"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.create(sched.CreateInput{Model: "w", Name: "other", Prompt: "p", Cron: "0 1 * * *", Cwd: "/other"}); err != nil {
		t.Fatal(err)
	}
	list, _ := h.list()
	if i := index(t, list, "/ws/f:"); i < 0 || i > index(t, list, "/other:") {
		t.Fatalf("this directory must come first:\n%s", list)
	}
	contains(t, list, "j1")
	contains(t, list, "/other:")
}

func TestEmptyListNamesTheStore(t *testing.T) {
	h := newHarness(t, "/ws/e")
	list, _ := h.list()
	contains(t, list, "scheduler: no jobs (global.sqlite)")
}

func TestJobListedAndPausableFromAnotherDirectory(t *testing.T) {
	h := newHarness(t, "/a")
	if _, err := h.create(sched.CreateInput{Model: "w", Name: "shared", Prompt: "p", Cron: "0 0 * * *", Cwd: "/a"}); err != nil {
		t.Fatal(err)
	}
	list, err := sched.List(context.Background(), h.db, h.ct, "/b", nil, func() time.Time { return nowFixed })
	mustOK(t, err)
	contains(t, list, "j1")
	if _, err := sched.Pause(context.Background(), h.db, h.ct, "j1", "/b", "sess-b"); err != nil {
		t.Fatalf("pause from /b under the same id: %v", err)
	}
	row := jobsRow(t, h, "j1")
	if row == nil || row["state"] != "paused" {
		t.Fatalf("paused row: %v", row)
	}
}

func TestIdsAreOneSequenceAcrossDirectories(t *testing.T) {
	h := newHarness(t, "/a")
	if _, err := h.create(sched.CreateInput{Model: "w", Name: "one", Prompt: "p", Cron: "0 0 * * *", Cwd: "/a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.create(sched.CreateInput{Model: "w", Name: "two", Prompt: "p", Cron: "0 1 * * *", Cwd: "/b"}); err != nil {
		t.Fatal(err)
	}
	contains(t, h.ct.text, "j2  # pane-scheduler:j2")
	if got := jobsRow(t, h, "j2")["cwd"]; got != "/b" {
		t.Fatalf("cwd = %v", got)
	}
}

func TestDriftMissingLineAlteredCronAndStateSplitAreAllFlagged(t *testing.T) {
	h := newHarness(t, "/ws/g")
	if _, err := h.create(sched.CreateInput{Model: "w", Name: "one", Prompt: "p", Cron: "0 0 * * *", Cwd: "/ws/g"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.create(sched.CreateInput{Model: "w", Name: "two", Prompt: "p", Cron: "0 2 * * *", Cwd: "/ws/g"}); err != nil {
		t.Fatal(err)
	}

	h.ct.mu.Lock()
	h.ct.text = regexp.MustCompile(`(?m).*pane-scheduler:j1.*\n?`).ReplaceAllString(h.ct.text, "")
	h.ct.mu.Unlock()
	list, _ := h.list()
	contains(t, list, "drift: no crontab line")

	h.ct.mu.Lock()
	h.ct.text = strings.Replace(h.ct.text, `0 2 * * * `+runnerCmd+` j2`, `59 23 * * * `+runnerCmd+` j2`, 1)
	h.ct.mu.Unlock()
	list, _ = h.list()
	contains(t, list, "cron differs")

	if _, err := sched.Pause(context.Background(), h.db, h.ct, "j2", h.sessCwd, "sess-core"); err != nil {
		t.Fatal(err)
	}
	h.ct.mu.Lock()
	h.ct.text = strings.Replace(h.ct.text, `# 59 23 * * * `+runnerCmd+` j2`, `59 23 * * * `+runnerCmd+` j2`, 1)
	h.ct.mu.Unlock()
	list, _ = h.list()
	contains(t, list, "line is active")
}

func TestListMarksAJobRunningWhenItsLockIsHeld(t *testing.T) {
	h := newHarness(t, "/ws/h")
	if _, err := h.create(sched.CreateInput{Model: "w", Name: "locked", Prompt: "p", Cron: "0 0 * * *", Cwd: "/ws/h"}); err != nil {
		t.Fatal(err)
	}
	list, err := sched.List(context.Background(), h.db, h.ct, h.sessCwd, func(key string) bool {
		return key == "j1"
	}, func() time.Time { return nowFixed })
	mustOK(t, err)
	contains(t, list, "running (lock held)")
}

func TestPauseCommentsTheLineAndResumeRestoresByteIdentical(t *testing.T) {
	h := newHarness(t, "/ws/i")
	_, err := sched.Create(context.Background(), h.db, h.ct,
		sched.CreateInput{Model: "w", Name: "p", Prompt: "p", Cron: "0 3 * * *", Cwd: "/ws/i"}, "/ws/i", "sess-core", runnerCmd, func() time.Time { return nowFixed })
	mustOK(t, err)
	activeLine := ""
	for _, l := range strings.Split(h.ct.text, "\n") {
		if strings.Contains(l, "pane-scheduler:") {
			activeLine = l
		}
	}
	if activeLine == "" {
		t.Fatal("no tagged line")
	}
	_, err = sched.Pause(context.Background(), h.db, h.ct, "j1", h.sessCwd, "sess-core")
	mustOK(t, err)
	contains(t, h.ct.text, "# "+activeLine)
	var ops []string
	rows, err := h.db.DB.Query(`SELECT op, session FROM events ORDER BY seq`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var sess string
	for rows.Next() {
		var op string
		var s any
		if err := rows.Scan(&op, &s); err != nil {
			t.Fatal(err)
		}
		ops = append(ops, op)
		if ss, ok := s.(string); ok {
			sess = ss
		}
	}
	if len(ops) != 2 || ops[0] != "create" || ops[1] != "pause" {
		t.Fatalf("ops = %v", ops)
	}
	if sess != "sess-core" {
		t.Fatalf("pause event session = %q", sess)
	}
	_, err = sched.Resume(context.Background(), h.db, h.ct, "j1", h.sessCwd, "sess-core")
	mustOK(t, err)
	contains(t, h.ct.text, activeLine)
	list, _ := h.list()
	if strings.Contains(list, "drift:") {
		t.Fatalf("clean after resume: %s", list)
	}
}

func TestPauseOnPausedRefusesResumeOnActiveRefuses(t *testing.T) {
	h := newHarness(t, "/ws/j")
	if _, err := h.create(sched.CreateInput{Model: "w", Name: "p", Prompt: "p", Cron: "0 4 * * *", Cwd: "/ws/j"}); err != nil {
		t.Fatal(err)
	}
	_, err := sched.Resume(context.Background(), h.db, h.ct, "j1", h.sessCwd, "sess-core")
	mustErr(t, err, `not paused`)
	if _, err := sched.Pause(context.Background(), h.db, h.ct, "j1", h.sessCwd, "sess-core"); err != nil {
		t.Fatal(err)
	}
	_, err = sched.Pause(context.Background(), h.db, h.ct, "j1", h.sessCwd, "sess-core")
	mustErr(t, err, `already paused`)
	if _, err := sched.Resume(context.Background(), h.db, h.ct, "j1", h.sessCwd, "sess-core"); err != nil {
		t.Fatal(err)
	}
	_, err = sched.Resume(context.Background(), h.db, h.ct, "j1", h.sessCwd, "sess-core")
	mustErr(t, err, `not paused`)
}

func TestUnknownIdRefuses(t *testing.T) {
	h := newHarness(t, "/ws/k")
	_, err := sched.Pause(context.Background(), h.db, h.ct, "j99", h.sessCwd, "sess-core")
	mustErr(t, err, `no job 'j99'`)
	_, err = sched.Remove(context.Background(), h.db, h.ct, "j99", h.sessCwd, "sess-core")
	mustErr(t, err, `no job 'j99'`)
}

func TestRemoveTombstonesTheRowAndRunsSurvive(t *testing.T) {
	h := newHarness(t, "/ws/l")
	_, err := h.create(sched.CreateInput{Model: "w", Name: "doomed", Prompt: "p", Cron: "0 7 * * *", Cwd: "/ws/l"})
	mustOK(t, err)

	if _, err := sched.RecordRun(context.Background(), h.db, sched.RunRecordInput{
		ID: "j1", Status: "ok", Exit: int64ptr(0), Duration: int64ptr(1000), Log: "runs/x/a.log",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := sched.Remove(context.Background(), h.db, h.ct, "j1", h.sessCwd, "sess-core"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(h.ct.text, "pane-scheduler:") {
		t.Fatal("no trace in crontab")
	}
	_, err = sched.Remove(context.Background(), h.db, h.ct, "j1", h.sessCwd, "sess-core")
	mustErr(t, err, `already removed`)
	row := jobsRow(t, h, "j1")
	if row == nil || row["state"] != "removed" {
		t.Fatalf("tombstone: %v", row)
	}
	reply, err := h.runs("j1", 0)
	mustOK(t, err)
	contains(t, reply, "1 run")
	contains(t, reply, "ok")
}

func TestRunsReturnsTheLastNInChronologicalOrder(t *testing.T) {
	h := newHarness(t, "/ws/m")
	_, err := h.create(sched.CreateInput{Model: "w", Name: "busy", Prompt: "p", Cron: "0 8 * * *", Cwd: "/ws/m"})
	mustOK(t, err)
	rec := func(status string, exit *int64, reason string) {
		if _, err := sched.RecordRun(context.Background(), h.db, sched.RunRecordInput{
			ID: "j1", Status: status, Exit: exit, Duration: int64ptr(10),
			Log: "runs/x/" + status + ".log", Reason: reason,
		}); err != nil {
			t.Fatal(err)
		}
	}
	rec("ok", int64ptr(0), "")
	rec("fail", int64ptr(1), "pi exit 1")
	rec("skip", nil, "busy")
	all, err := h.runs("j1", 0)
	mustOK(t, err)
	iOK := index(t, all, "ok")
	iFail := index(t, all, "fail")
	iSkip := index(t, all, "skip")
	if !(iOK >= 0 && iOK < iFail && iFail < iSkip) {
		t.Fatalf("chronological order: %s", all)
	}
	two, err := h.runs("j1", 2)
	mustOK(t, err)
	if iOK := index(t, two, "ok"); iOK >= 0 {
		t.Fatalf("n=2 must drop the oldest: %s", two)
	}
	_, err = h.runs("j404", 0)
	mustErr(t, err, `no job 'j404'`)
}

func TestCrontabInstallFailureRefusesAndLeavesTheStoreUntouched(t *testing.T) {
	h := newHarness(t, "/ws/n")
	fc := failingCrontab{installErr: errors.New("crontab install failed (exit 2): boom")}
	_, err := sched.Create(context.Background(), h.db, fc, sched.CreateInput{
		Model: "w",
		Name:  "x", Prompt: "p", Cron: "0 9 * * *", Cwd: "/ws/n",
	}, "/ws/n", "sess-core", runnerCmd, func() time.Time { return nowFixed })
	mustErr(t, err, `crontab install failed`)
	var n int
	if err := h.db.DB.QueryRow(`SELECT count(*) FROM events`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("events = %d, want 0", n)
	}
}

func TestCrontabListFailureRefusesLoudlyBeforeAnythingIsWritten(t *testing.T) {
	h := newHarness(t, "/ws/o")
	fc := failingCrontab{listErr: errors.New("crontab: binary not found")}
	_, err := sched.Create(context.Background(), h.db, fc, sched.CreateInput{
		Model: "w",
		Name:  "x", Prompt: "p", Cron: "0 10 * * *", Cwd: "/ws/o",
	}, "/ws/o", "sess-core", runnerCmd, func() time.Time { return nowFixed })
	mustErr(t, err, `binary not found`)
	var n int
	if err := h.db.DB.QueryRow(`SELECT count(*) FROM events`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("events = %d, want 0", n)
	}
}

func TestCreateOnceRefusesAPastAt(t *testing.T) {
	h := newHarness(t, "/ws/past")
	_, err := h.create(sched.CreateInput{Model: "w", Name: "late", Prompt: "p", Cron: "once", At: "2026-08-15T11:59:00Z"})
	mustErr(t, err, "in the future")
	var n int
	if err := h.db.QueryRow(`SELECT COUNT(*) FROM jobs`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("jobs = %d rows, want none (the past at is refused before the write)", n)
	}
}

func TestUpdateOnceRefusesAPastAt(t *testing.T) {
	h := newHarness(t, "/ws/pastupd")
	_, err := h.create(sched.CreateInput{Model: "w", Name: "soon", Prompt: "p", Cron: "0 0 * * *"})
	mustOK(t, err)
	_, err = h.update(sched.UpdateInput{ID: "j1", At: "2026-08-15T11:00:00Z"})
	mustErr(t, err, "in the future")
}
