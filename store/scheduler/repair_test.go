package scheduler_test

import (
	"context"
	"regexp"
	"strings"
	"testing"

	sched "github.com/mrsirg97-rgb/rig/v2/store/scheduler"
)

func taggedLine(t *testing.T, h *harness, id string) string {
	t.Helper()
	for _, l := range strings.Split(h.ct.text, "\n") {
		if strings.Contains(l, "rig-scheduler:"+sched.TagHome(h.rigHome)+":"+id) {
			return l
		}
	}
	return ""
}

func dropLine(t *testing.T, h *harness, id string) {
	t.Helper()
	h.ct.mu.Lock()
	defer h.ct.mu.Unlock()
	h.ct.text = regexp.MustCompile(`(?m).*rig-scheduler:`+sched.TagHome(h.rigHome)+`:`+id+`.*\n?`).ReplaceAllString(h.ct.text, "")
}

func eventsOps(t *testing.T, h *harness) []string {
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
	return ops
}

func TestRepairReinstatesAMissingLineNamesTheDriftAndWritesNoEvent(t *testing.T) {
	h := newHarness(t, "/ws/r1")
	if _, err := h.create(sched.CreateInput{Model: "w", Name: "one", Prompt: "p", Cron: "0 2 * * *", Cwd: "/ws/r1"}); err != nil {
		t.Fatal(err)
	}
	dropLine(t, h, "j1")
	before := jobsRow(t, h, "j1")

	reply, err := sched.Repair(context.Background(), h.db, h.ct, "j1", runnerCmd, h.rigHome)
	mustOK(t, err)
	if !strings.Contains(reply, "'j1' repaired: no crontab line") {
		t.Fatalf("reply %q, want the drift verbatim", reply)
	}
	line := taggedLine(t, h, "j1")
	if line == "" || !strings.HasPrefix(line, "0 2 * * * "+runnerCmd+" j1  # rig-scheduler:") {
		t.Fatalf("line %q, want the job's cron and the wired runner command", line)
	}
	if got := eventsOps(t, h); len(got) != 1 || got[0] != "create" {
		t.Fatalf("ops %v, want only create (repair is a crontab write only)", got)
	}
	after := jobsRow(t, h, "j1")
	if after["state"] != before["state"] || after["cron"] != before["cron"] || after["updated_seq"] != before["updated_seq"] {
		t.Fatalf("state changed: before %v after %v", before, after)
	}
}

func TestRepairRewritesAnAlteredCronLine(t *testing.T) {
	h := newHarness(t, "/ws/r2")
	if _, err := h.create(sched.CreateInput{Model: "w", Name: "one", Prompt: "p", Cron: "0 2 * * *", Cwd: "/ws/r2"}); err != nil {
		t.Fatal(err)
	}
	h.ct.mu.Lock()
	h.ct.text = strings.Replace(h.ct.text, "0 2 * * * "+runnerCmd+" j1", "59 23 * * * "+runnerCmd+" j1", 1)
	h.ct.mu.Unlock()

	reply, err := sched.Repair(context.Background(), h.db, h.ct, "j1", runnerCmd, h.rigHome)
	mustOK(t, err)
	if !strings.Contains(reply, "'j1' repaired: cron differs (crontab: 59 23 * * *)") {
		t.Fatalf("reply %q, want the drift verbatim", reply)
	}
	if line := taggedLine(t, h, "j1"); !strings.HasPrefix(line, "0 2 * * * "+runnerCmd+" j1") {
		t.Fatalf("line %q, want the store's cron back", line)
	}
	list, err := h.list()
	mustOK(t, err)
	if strings.Contains(list, "drift:") {
		t.Fatalf("clean after repair: %s", list)
	}
}

func TestRepairRecommentsADriftedPausedLine(t *testing.T) {
	h := newHarness(t, "/ws/r3")
	if _, err := h.create(sched.CreateInput{Model: "w", Name: "one", Prompt: "p", Cron: "0 3 * * *", Cwd: "/ws/r3"}); err != nil {
		t.Fatal(err)
	}
	if _, err := sched.Pause(context.Background(), h.db, h.ct, "j1", h.sessCwd, "sess-core", h.rigHome); err != nil {
		t.Fatal(err)
	}
	h.ct.mu.Lock()
	h.ct.text = strings.Replace(h.ct.text, taggedLine(t, h, "j1"), strings.TrimPrefix(taggedLine(t, h, "j1"), "# "), 1)
	h.ct.mu.Unlock()

	reply, err := sched.Repair(context.Background(), h.db, h.ct, "j1", runnerCmd, h.rigHome)
	mustOK(t, err)
	if !strings.Contains(reply, "'j1' repaired: line is active") {
		t.Fatalf("reply %q, want the drift verbatim", reply)
	}
	if line := taggedLine(t, h, "j1"); !strings.HasPrefix(line, "# 0 3 * * * "+runnerCmd+" j1") {
		t.Fatalf("line %q, want the paused job's line commented", line)
	}
}

func TestRepairUncommentsADriftedActiveLine(t *testing.T) {
	h := newHarness(t, "/ws/r4")
	if _, err := h.create(sched.CreateInput{Model: "w", Name: "one", Prompt: "p", Cron: "0 4 * * *", Cwd: "/ws/r4"}); err != nil {
		t.Fatal(err)
	}
	h.ct.mu.Lock()
	h.ct.text = strings.Replace(h.ct.text, taggedLine(t, h, "j1"), "# "+taggedLine(t, h, "j1"), 1)
	h.ct.mu.Unlock()

	reply, err := sched.Repair(context.Background(), h.db, h.ct, "j1", runnerCmd, h.rigHome)
	mustOK(t, err)
	if !strings.Contains(reply, "'j1' repaired: line is paused") {
		t.Fatalf("reply %q, want the drift verbatim", reply)
	}
	if line := taggedLine(t, h, "j1"); !strings.HasPrefix(line, "0 4 * * * "+runnerCmd+" j1") {
		t.Fatalf("line %q, want the active job's line live", line)
	}
}

func TestRepairInSyncInstallsNothing(t *testing.T) {
	h := newHarness(t, "/ws/r5")
	if _, err := h.create(sched.CreateInput{Model: "w", Name: "one", Prompt: "p", Cron: "0 5 * * *", Cwd: "/ws/r5"}); err != nil {
		t.Fatal(err)
	}
	before := h.ct.text
	reply, err := sched.Repair(context.Background(), h.db, h.ct, "j1", runnerCmd, h.rigHome)
	mustOK(t, err)
	if reply != "'j1' is in sync" {
		t.Fatalf("reply %q", reply)
	}
	if h.ct.text != before {
		t.Fatalf("in-sync repair must install nothing: %q -> %q", before, h.ct.text)
	}
}

func TestRepairOfRemovedAndDoneJobsRefusesNothingToRepair(t *testing.T) {
	h := newHarness(t, "/ws/r6")
	if _, err := h.create(sched.CreateInput{Model: "w", Name: "one", Prompt: "p", Cron: "0 6 * * *", Cwd: "/ws/r6"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.create(sched.CreateInput{Model: "w", Name: "two", Prompt: "p", Cron: "once", At: "2026-08-16T03:07:00Z", Cwd: "/ws/r6"}); err != nil {
		t.Fatal(err)
	}
	if _, err := sched.Remove(context.Background(), h.db, h.ct, "j1", h.sessCwd, "sess-core", h.rigHome); err != nil {
		t.Fatal(err)
	}
	if _, err := sched.RecordRun(context.Background(), h.db, sched.RunRecordInput{ID: "j2", Status: "ok"}); err != nil {
		t.Fatal(err)
	}
	_, err := sched.Repair(context.Background(), h.db, h.ct, "j1", runnerCmd, h.rigHome)
	mustErr(t, err, `'j1' is removed; nothing to repair`)
	_, err = sched.Repair(context.Background(), h.db, h.ct, "j2", runnerCmd, h.rigHome)
	mustErr(t, err, `'j2' is done; nothing to repair`)
	_, err = sched.Repair(context.Background(), h.db, h.ct, "j99", runnerCmd, h.rigHome)
	mustErr(t, err, `no job 'j99'`)
}

func TestRepairAllFixesEveryDriftOneLineEachAndNothingWhenNone(t *testing.T) {
	h := newHarness(t, "/ws/r7")
	for _, name := range []string{"one", "two", "three"} {
		if _, err := h.create(sched.CreateInput{Model: "w", Name: name, Prompt: "p", Cron: "0 2 * * *", Cwd: "/ws/r7"}); err != nil {
			t.Fatal(err)
		}
	}
	dropLine(t, h, "j1")
	h.ct.mu.Lock()
	h.ct.text = strings.Replace(h.ct.text, "0 2 * * * "+runnerCmd+" j2", "59 23 * * * "+runnerCmd+" j2", 1)
	h.ct.mu.Unlock()
	if _, err := sched.Pause(context.Background(), h.db, h.ct, "j3", h.sessCwd, "sess-core", h.rigHome); err != nil {
		t.Fatal(err)
	}
	h.ct.mu.Lock()
	h.ct.text = strings.Replace(h.ct.text, taggedLine(t, h, "j3"), strings.TrimPrefix(taggedLine(t, h, "j3"), "# "), 1)
	h.ct.mu.Unlock()

	reply, err := sched.Repair(context.Background(), h.db, h.ct, "", runnerCmd, h.rigHome)
	mustOK(t, err)
	want := "'j1' repaired: no crontab line\n" +
		"'j2' repaired: cron differs (crontab: 59 23 * * *)\n" +
		"'j3' repaired: line is active"
	if reply != want {
		t.Fatalf("reply %q, want one line per repaired job:\n%s", reply, want)
	}
	if line := taggedLine(t, h, "j1"); line == "" {
		t.Fatal("j1's line missing after the walk")
	}
	if line := taggedLine(t, h, "j2"); !strings.HasPrefix(line, "0 2 * * * "+runnerCmd+" j2") {
		t.Fatalf("j2 line %q, want the store's cron back", line)
	}
	if line := taggedLine(t, h, "j3"); !strings.HasPrefix(line, "# 0 2 * * * "+runnerCmd+" j3") {
		t.Fatalf("j3 line %q, want the paused job's line commented", line)
	}
	if got := eventsOps(t, h); strings.Join(got, ",") != "create,create,create,pause" {
		t.Fatalf("ops %v, want the walk to write no events", got)
	}

	again, err := sched.Repair(context.Background(), h.db, h.ct, "", runnerCmd, h.rigHome)
	mustOK(t, err)
	if again != "nothing drifted" {
		t.Fatalf("second walk %q, want nothing drifted", again)
	}
}
