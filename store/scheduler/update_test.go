package scheduler_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/store"
	sched "github.com/mrsirg97-rgb/rig/store/scheduler"
)

func (h *harness) update(in sched.UpdateInput) (string, error) {
	return sched.Update(context.Background(), h.db, h.ct, in, "sess-core", runnerCmd, func() time.Time { return nowFixed })
}

func eventOps(t *testing.T, h *harness) []string {
	t.Helper()
	rows, err := h.db.DB.Query(`SELECT op FROM events ORDER BY seq`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ops []string
	for rows.Next() {
		var op string
		if err := rows.Scan(&op); err != nil {
			t.Fatal(err)
		}
		ops = append(ops, op)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return ops
}

func TestUpdateKeepsTheIdAndTheRuns(t *testing.T) {
	h := newHarness(t, "/ws/u1")
	if _, err := h.create(sched.CreateInput{Name: "keep", Prompt: "p", Cron: "0 3 * * *", Cwd: "/ws/u1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := sched.RecordRun(context.Background(), h.db, sched.RunRecordInput{
		ID: "j1", Status: "ok", Exit: int64ptr(0), Duration: int64ptr(500), Log: "runs/x/a.log",
	}); err != nil {
		t.Fatal(err)
	}
	reply, err := h.update(sched.UpdateInput{ID: "j1", Prompt: "p2", Model: "brain", Busy: "force"})
	mustOK(t, err)
	contains(t, reply, "updated j1")
	row := jobsRow(t, h, "j1")
	if row == nil {
		t.Fatal("j1 must survive the update (no re-mint)")
	}
	if row["prompt"] != "p2" || row["model"] != "brain" || row["busy"] != "force" {
		t.Fatalf("the changed fields must be overlaid: %v", row)
	}
	if row["name"] != "keep" || row["cron"] != "0 3 * * *" {
		t.Fatalf("the untouched fields must stay: %v", row)
	}
	if row["created_seq"] != int64(1) {
		t.Fatalf("created_seq must survive: %v", row["created_seq"])
	}
	if row["updated_seq"] != int64(3) {
		t.Fatalf("updated_seq must advance to the update op: %v", row["updated_seq"])
	}
	runs, err := h.runs("j1", 0)
	mustOK(t, err)
	contains(t, runs, "1 run")
	contains(t, runs, "ok")
	if got := eventOps(t, h); len(got) != 3 || got[0] != "create" || got[1] != "run" || got[2] != "update" {
		t.Fatalf("ops = %v", got)
	}
}

func TestUpdateRewritesTheOneLineUnderTheSameKey(t *testing.T) {
	h := newHarness(t, "/ws/u2")
	if _, err := h.create(sched.CreateInput{Name: "one", Prompt: "p", Cron: "0 3 * * *", Cwd: "/ws/u2"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.create(sched.CreateInput{Name: "two", Prompt: "p", Cron: "0 4 * * *", Cwd: "/ws/u2"}); err != nil {
		t.Fatal(err)
	}
	foreignLine := `0 4 * * * ` + runnerCmd + ` j2  # pane-scheduler:j2`
	_, err := h.update(sched.UpdateInput{ID: "j1", Cron: "15 6 * * *"})
	mustOK(t, err)
	contains(t, h.ct.text, `15 6 * * * `+runnerCmd+` j1  # pane-scheduler:j1`)
	if strings.Contains(h.ct.text, `0 3 * * * `+runnerCmd+` j1`) {
		t.Fatalf("the old line must be gone: %s", h.ct.text)
	}
	if n := strings.Count(h.ct.text, "pane-scheduler:j1"); n != 1 {
		t.Fatalf("exactly one line for j1, got %d:\n%s", n, h.ct.text)
	}
	contains(t, h.ct.text, foreignLine)
}

func TestPausedUpdateStaysPausedAndLandsOnResume(t *testing.T) {
	h := newHarness(t, "/ws/u3")
	if _, err := h.create(sched.CreateInput{Name: "p", Prompt: "p", Cron: "0 3 * * *", Cwd: "/ws/u3"}); err != nil {
		t.Fatal(err)
	}
	if _, err := sched.Pause(context.Background(), h.db, h.ct, "j1", h.sessCwd, "sess-core"); err != nil {
		t.Fatal(err)
	}
	_, err := h.update(sched.UpdateInput{ID: "j1", Cron: "0 7 * * *"})
	mustOK(t, err)
	for _, l := range strings.Split(h.ct.text, "\n") {
		if strings.Contains(l, "pane-scheduler:j1") {
			if !strings.HasPrefix(l, "# ") {
				t.Fatalf("a paused job's rewritten line must stay commented: %q", l)
			}
			contains(t, l, `0 7 * * *`)
		}
	}
	row := jobsRow(t, h, "j1")
	if row["state"] != "paused" {
		t.Fatalf("update must not change the state: %v", row["state"])
	}
	if _, err := sched.Resume(context.Background(), h.db, h.ct, "j1", h.sessCwd, "sess-core"); err != nil {
		t.Fatal(err)
	}
	contains(t, h.ct.text, `0 7 * * * `+runnerCmd+` j1  # pane-scheduler:j1`)
	list, _ := h.list()
	if strings.Contains(list, "drift:") {
		t.Fatalf("clean after resume: %s", list)
	}
}

func TestUpdateRefusalsNameTheFaultAndWriteNothing(t *testing.T) {
	h := newHarness(t, "/ws/u4")
	if _, err := h.create(sched.CreateInput{Name: "a", Prompt: "p", Cron: "0 3 * * *", Cwd: "/ws/u4"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.create(sched.CreateInput{Name: "b", Prompt: "p", Cron: "0 4 * * *", Cwd: "/ws/u4"}); err != nil {
		t.Fatal(err)
	}
	before := h.ct.text

	_, err := h.update(sched.UpdateInput{ID: "j1"})
	mustErr(t, err, `update needs a change`)

	_, err = h.update(sched.UpdateInput{ID: "j1", Cron: "0 5 * * *", At: "2026-08-16T03:07:00Z"})
	mustErr(t, err, `both`)

	_, err = h.update(sched.UpdateInput{ID: "j99", Model: "brain"})
	mustErr(t, err, `no job 'j99'`)

	_, err = h.update(sched.UpdateInput{ID: "j1", Name: "b"})
	mustErr(t, err, `a job named 'b' already exists`)

	if h.ct.text != before {
		t.Fatalf("refusals must leave the crontab byte-identical: %q", h.ct.text)
	}
	if got := eventOps(t, h); len(got) != 2 {
		t.Fatalf("refusals must append nothing, ops = %v", got)
	}

	if _, err := h.update(sched.UpdateInput{ID: "j1", Name: "a", Prompt: "still me"}); err != nil {
		t.Fatalf("the job's own name is not a collision: %v", err)
	}

	if _, err := sched.Remove(context.Background(), h.db, h.ct, "j2", h.sessCwd, "sess-core"); err != nil {
		t.Fatal(err)
	}
	_, err = h.update(sched.UpdateInput{ID: "j2", Model: "brain"})
	mustErr(t, err, `job 'j2' is removed`)
}

func TestUpdateCadenceRefusalsAreCreatesVerbatim(t *testing.T) {
	h := newHarness(t, "/ws/u5")
	if _, err := h.create(sched.CreateInput{Name: "c", Prompt: "p", Cron: "0 3 * * *", Cwd: "/ws/u5"}); err != nil {
		t.Fatal(err)
	}
	_, err := h.update(sched.UpdateInput{ID: "j1", Cron: "once"})
	mustErr(t, err, `cron 'once' requires 'at'`)
	_, err = h.update(sched.UpdateInput{ID: "j1", At: "not-a-time"})
	mustErr(t, err, `valid ISO time`)
	_, err = h.update(sched.UpdateInput{ID: "j1", Cron: "* * * * "})
	mustErr(t, err, `cron:`)
}

func TestUpdateAtMakesTheJobOnceAndCronClearsTheAt(t *testing.T) {
	h := newHarness(t, "/ws/u6")
	if _, err := h.create(sched.CreateInput{Name: "r", Prompt: "p", Cron: "0 3 * * *", Cwd: "/ws/u6"}); err != nil {
		t.Fatal(err)
	}
	_, err := h.update(sched.UpdateInput{ID: "j1", At: "2026-08-16T03:07:00Z"})
	mustOK(t, err)
	row := jobsRow(t, h, "j1")
	if row["cron"] != "7 3 16 8 *" {
		t.Fatalf("cron %v", row["cron"])
	}
	if row["at"] != "2026-08-16T03:07:00Z" {
		t.Fatalf("at %v", row["at"])
	}
	contains(t, h.ct.text, `7 3 16 8 * `+runnerCmd+` j1  # pane-scheduler:j1`)

	_, err = h.update(sched.UpdateInput{ID: "j1", Cron: "0 9 * * *"})
	mustOK(t, err)
	row = jobsRow(t, h, "j1")
	if row["cron"] != "0 9 * * *" {
		t.Fatalf("cron %v", row["cron"])
	}
	if row["at"] != nil {
		t.Fatalf("at must be cleared: %v", row["at"])
	}
	contains(t, h.ct.text, `0 9 * * * `+runnerCmd+` j1  # pane-scheduler:j1`)
}

func TestUpdateOnADoneJobIsAllowedAndRestoresTheLine(t *testing.T) {
	h := newHarness(t, "/ws/u11")
	if _, err := h.create(sched.CreateInput{Name: "once", Prompt: "p", Cron: "once", At: "2026-08-16T03:07:00Z", Cwd: "/ws/u11"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.db.DB.Exec(`UPDATE jobs SET state = 'done' WHERE id = 'j1'`); err != nil {
		t.Fatal(err)
	}
	_, err := h.update(sched.UpdateInput{ID: "j1", Cron: "0 8 * * *"})
	mustOK(t, err)
	contains(t, h.ct.text, `0 8 * * * `+runnerCmd+` j1  # pane-scheduler:j1`)
}

func TestDriftStaysHonestAfterAnUpdate(t *testing.T) {
	h := newHarness(t, "/ws/u7")
	if _, err := h.create(sched.CreateInput{Name: "d", Prompt: "p", Cron: "0 3 * * *", Cwd: "/ws/u7"}); err != nil {
		t.Fatal(err)
	}
	h.ct.mu.Lock()
	h.ct.text = strings.Replace(h.ct.text, `0 3 * * * `+runnerCmd+` j1`, `5 5 * * * `+runnerCmd+` j1`, 1)
	h.ct.mu.Unlock()
	list, _ := h.list()
	contains(t, list, "cron differs")

	_, err := h.update(sched.UpdateInput{ID: "j1", Model: "brain"})
	mustOK(t, err)
	list, _ = h.list()
	contains(t, list, "cron differs")

	_, err = h.update(sched.UpdateInput{ID: "j1", Cron: "5 5 * * *"})
	mustOK(t, err)
	list, _ = h.list()
	if strings.Contains(list, "drift:") {
		t.Fatalf("a cadence update that matches the line must clear the drift: %s", list)
	}
}

func TestListShowsTheUpdatedFields(t *testing.T) {
	h := newHarness(t, "/ws/u8")
	if _, err := h.create(sched.CreateInput{Name: "oldname", Prompt: "p", Cron: "0 3 * * *", Cwd: "/ws/u8"}); err != nil {
		t.Fatal(err)
	}
	reply, err := h.update(sched.UpdateInput{ID: "j1", Name: "newname", Model: "brain", Busy: "force", Cwd: "/else/where"})
	mustOK(t, err)
	contains(t, reply, "newname")
	contains(t, reply, "brain")
	contains(t, reply, "busy force")
	list, err := h.list()
	mustOK(t, err)
	contains(t, list, "newname")
	contains(t, list, "/else/where:")
	if strings.Contains(list, "oldname") {
		t.Fatalf("the old name must be gone: %s", list)
	}
}

func TestUpdateWithoutACadenceChangeLeavesTheCrontabByteIdentical(t *testing.T) {
	h := newHarness(t, "/ws/u9")
	if _, err := h.create(sched.CreateInput{Name: "s", Prompt: "p", Cron: "0 3 * * *", Cwd: "/ws/u9"}); err != nil {
		t.Fatal(err)
	}
	before := h.ct.text
	_, err := h.update(sched.UpdateInput{ID: "j1", Model: "brain", Cron: "0 3 * * *"})
	mustOK(t, err)
	if h.ct.text != before {
		t.Fatalf("the same cron is not a change: %q", h.ct.text)
	}
}

func TestUpdateArgsCarryOnlyTheChangedFieldsAndReplaySurvivesReopen(t *testing.T) {
	h := newHarness(t, "/ws/u10")
	if _, err := h.create(sched.CreateInput{Name: "ev", Prompt: "p", Cron: "0 3 * * *", Cwd: "/ws/u10"}); err != nil {
		t.Fatal(err)
	}
	_, err := h.update(sched.UpdateInput{ID: "j1", Model: "brain"})
	mustOK(t, err)
	var args string
	if err := h.db.DB.QueryRow(`SELECT args FROM events WHERE op = 'update'`).Scan(&args); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(args, `"prompt"`) || strings.Contains(args, `"cron"`) || strings.Contains(args, `"name"`) {
		t.Fatalf("unchanged fields must not ride the op: %s", args)
	}
	if !strings.Contains(args, `"model":"brain"`) {
		t.Fatalf("the changed field must ride the op: %s", args)
	}

	path := filepath.Join(h.home, "global.sqlite")
	if err := h.db.Close(); err != nil {
		t.Fatal(err)
	}
	db, _, _, err := store.Open(path, sched.Statements(), sched.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	list, err := sched.List(context.Background(), db, h.ct, h.sessCwd, nil, func() time.Time { return nowFixed })
	if err != nil {
		t.Fatal(err)
	}
	contains(t, list, "brain")
}

func TestCrontabInstallFailureOnUpdateLeavesTheStoreUntouched(t *testing.T) {
	h := newHarness(t, "/ws/u12")
	if _, err := h.create(sched.CreateInput{Name: "f", Prompt: "p", Cron: "0 3 * * *", Cwd: "/ws/u12"}); err != nil {
		t.Fatal(err)
	}
	fc := failingCrontab{installErr: errors.New("crontab install failed (exit 2): boom")}
	_, err := sched.Update(context.Background(), h.db, fc,
		sched.UpdateInput{ID: "j1", Cron: "0 9 * * *"},
		"sess-core", runnerCmd, func() time.Time { return nowFixed })
	mustErr(t, err, `crontab install failed`)
	var n int
	if err := h.db.DB.QueryRow(`SELECT count(*) FROM events`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("events = %d, want 1 (the create)", n)
	}
}

func TestUpdateRefusesAnUnknownBusy(t *testing.T) {
	h := newHarness(t, "/ws/upd")
	if _, err := h.create(sched.CreateInput{Name: "b", Prompt: "p", Cron: "0 1 * * *"}); err != nil {
		t.Fatal(err)
	}
	_, err := sched.Update(context.Background(), h.db, h.ct, sched.UpdateInput{ID: "j1", Busy: "banana"}, "sess", runnerCmd, func() time.Time { return nowFixed })
	if err == nil || !strings.Contains(err.Error(), "busy must be") {
		t.Fatalf("an unknown busy must refuse by name, got %v", err)
	}
}
