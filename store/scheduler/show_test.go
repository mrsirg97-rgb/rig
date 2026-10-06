package scheduler_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	sched "github.com/mrsirg97-rgb/rig/v2/store/scheduler"
)

func showHarness(t *testing.T, name, tag string) *harness {
	t.Helper()
	dir := "/ws/" + tag
	h := newHarness(t, dir)
	if _, err := h.create(sched.CreateInput{Model: "w", Name: name, Prompt: "p", Cron: "0 3 * * *", Cwd: dir}); err != nil {
		t.Fatal(err)
	}
	return h
}

func showOf(t *testing.T, h *harness, id string, ct sched.Crontab) (string, error) {
	t.Helper()
	if ct == nil {
		ct = h.ct
	}
	return sched.Show(context.Background(), h.db, ct, id, h.rigHome, nil, func() time.Time { return nowFixed })
}

func TestShowPrintsTheBlockListPrintsForThatJobPlusItsLastRun(t *testing.T) {
	h := showHarness(t, "nightly", "show1")
	if _, err := sched.RecordRun(context.Background(), h.db, sched.RunRecordInput{
		ID: "j1", Status: "ok", Exit: int64ptr(0), Duration: int64ptr(1500), Log: "runs/j1/a.log",
	}); err != nil {
		t.Fatal(err)
	}
	list, err := h.list()
	mustOK(t, err)
	block := strings.TrimPrefix(list, "/ws/show1:\n")
	if block == list {
		t.Fatalf("the list groups the job under its cwd:\n%s", list)
	}
	show, err := showOf(t, h, "j1", nil)
	mustOK(t, err)
	if !strings.HasPrefix(show, block) {
		t.Fatalf("show must lead with the bytes list prints for the job:\n%s\nwant prefix:\n%s", show, block)
	}
	tail := strings.TrimPrefix(show, block)
	if !strings.HasPrefix(tail, "\n  last ") {
		t.Fatalf("show adds one last-run line, got %q", tail)
	}
	runs, err := h.runs("j1", 0)
	mustOK(t, err)
	contains(t, runs, strings.TrimSpace(strings.TrimPrefix(tail, "\n  last ")))
	contains(t, tail, "ok")
	contains(t, tail, "exit 0")
	contains(t, tail, "1500ms")
	contains(t, tail, "runs/j1/a.log")
}

func TestShowCarriesTheDriftTheListCarries(t *testing.T) {
	h := showHarness(t, "nightly", "show2")
	dropLine(t, h, "j1")
	list, err := h.list()
	mustOK(t, err)
	show, err := showOf(t, h, "j1", nil)
	mustOK(t, err)
	if !strings.HasPrefix(show, strings.TrimPrefix(list, "/ws/show2:\n")) {
		t.Fatalf("show must print the drifting block verbatim:\n%s\nwant prefix:\n%s", show, list)
	}
	contains(t, show, "drift: no crontab line")
}

func TestShowOnAnUnreadableCrontabCarriesTheListNote(t *testing.T) {
	dir := "/ws/show3"
	h := newHarness(t, dir)
	if _, err := h.create(sched.CreateInput{Model: "w", Name: "bite", Prompt: "p", Cron: "once", At: "2026-08-16T03:07:00Z", Cwd: dir}); err != nil {
		t.Fatal(err)
	}
	if _, err := sched.RecordRun(context.Background(), h.db, sched.RunRecordInput{
		ID: "j1", Status: "ok", Exit: int64ptr(0), Duration: int64ptr(1500), Log: "runs/j1/a.log",
	}); err != nil {
		t.Fatal(err)
	}
	var ct sched.Crontab = failingCrontab{listErr: errors.New("crontab down")}
	list, err := sched.List(context.Background(), h.db, ct, h.sessCwd, h.rigHome, nil, func() time.Time { return nowFixed })
	mustOK(t, err)
	show, err := showOf(t, h, "j1", ct)
	mustOK(t, err)
	if !strings.HasPrefix(show, strings.TrimPrefix(list, dir+":\n")) {
		t.Fatalf("show must print what list prints without the crontab:\n%s\nwant prefix:\n%s", show, list)
	}
	contains(t, show, "drift: crontab unreadable")
}

func TestShowOfAJobWithNoRunsCarriesNoRunLine(t *testing.T) {
	h := showHarness(t, "nightly", "show4")
	list, err := h.list()
	mustOK(t, err)
	show, err := showOf(t, h, "j1", nil)
	mustOK(t, err)
	if want := strings.TrimPrefix(list, "/ws/show4:\n"); show != want {
		t.Fatalf("a job with no runs shows its block and nothing more:\n%s\nwant:\n%s", show, want)
	}
}

func TestShowOfAnUnknownIdRefusesNamingItAndPointingAtList(t *testing.T) {
	h := showHarness(t, "nightly", "show5")
	_, err := showOf(t, h, "j7", nil)
	mustErr(t, err, `no job 'j7' \(list`)
}

func TestShowOfARemovedJobRefusesTheTombstone(t *testing.T) {
	h := showHarness(t, "nightly", "show6")
	if _, err := sched.Remove(context.Background(), h.db, h.ct, "j1", h.sessCwd, "sess-core", h.rigHome); err != nil {
		t.Fatal(err)
	}
	_, err := showOf(t, h, "j1", nil)
	mustErr(t, err, `'j1' is removed`)
}
