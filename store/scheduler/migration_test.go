package scheduler_test

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/store"
	sched "github.com/mrsirg97-rgb/rig/store/scheduler"
)

type legacyJob struct {
	Name   string
	Prompt string
	Cron   string
	Cwd    string
}

func seedLegacyGlobal(t *testing.T, home string) {
	t.Helper()
	if _, _, _, err := store.Open(filepath.Join(home, "global.sqlite"), sched.Statements(), 1); err != nil {
		t.Fatalf("seed legacy global: %v", err)
	}
}

func legacyStore(t *testing.T, home, hash string, ct *fakeCrontab, jobs []legacyJob, recRun bool) {
	t.Helper()
	db, _, _, err := store.Open(filepath.Join(home, "cwd-"+hash+".sqlite"), sched.Statements(), 1)
	if err != nil {
		t.Fatalf("open legacy store: %v", err)
	}
	for _, j := range jobs {
		if _, err := sched.Create(context.Background(), db, ct, sched.CreateInput{
			Name: j.Name, Prompt: j.Prompt, Cron: j.Cron, Cwd: j.Cwd,
		}, "/sess", "sess-legacy", runnerCmd, func() time.Time { return nowFixed }); err != nil {
			t.Fatalf("legacy create: %v", err)
		}
	}
	if recRun {
		if _, err := sched.RecordRun(context.Background(), db, sched.RunRecordInput{
			ID: "j1", Status: "ok", Exit: int64ptr(0), Duration: int64ptr(1000), Log: "runs/x/a.log",
		}); err != nil {
			t.Fatalf("legacy record: %v", err)
		}
	}
	if err := db.DB.Close(); err != nil {
		t.Fatal(err)
	}
	// simulate the legacy crontab keys: cwd-<hash>:jN
	ct.mu.Lock()
	ct.text = regexp.MustCompile(`pane-scheduler:j(\d+)`).ReplaceAllString(ct.text, "pane-scheduler:cwd-"+hash+":j$1")
	ct.text = regexp.MustCompile(`run-job j(\d+)`).ReplaceAllString(ct.text, "run-job cwd-"+hash+":j$1")
	ct.mu.Unlock()
}

func TestMigrationFoldsTwoCwdStoresWithCollidingJ1s(t *testing.T) {
	home := t.TempDir()
	ct := newFakeCrontab("SHELL=/bin/bash\n")
	legacyStore(t, home, "aaaa1111bbbb", ct, []legacyJob{
		{Name: "from-a", Prompt: "p", Cron: "0 0 * * *", Cwd: "/dir-a"},
	}, true)
	legacyStore(t, home, "cccc2222dddd", ct, []legacyJob{
		{Name: "from-b", Prompt: "q", Cron: "0 1 * * *", Cwd: "/dir-b"},
	}, false)

	if !strings.Contains(ct.text, "cwd-aaaa1111bbbb:j1") || !strings.Contains(ct.text, "cwd-cccc2222dddd:j1") {
		t.Fatalf("legacy crontab keys not staged:\n%s", ct.text)
	}

	globalPath := filepath.Join(home, "global.sqlite")
	seedLegacyGlobal(t, home)
	gdb, _, report, err := store.Open(globalPath, sched.Statements(), sched.SchemaVersion, sched.Migration(home, ct))
	if err != nil {
		t.Fatal(err)
	}
	defer gdb.DB.Close()
	if !strings.Contains(report, "folded 2 jobs") {
		t.Fatalf("migration report %q, want the fold counted", report)
	}
	// ids are one sequence, cwds intact
	rowA := jobsRow(t, &harness{db: gdb}, "j1")
	rowB := jobsRow(t, &harness{db: gdb}, "j2")
	if rowA == nil || rowB == nil {
		t.Fatal("both folded jobs must exist with distinct ids")
	}
	if rowA["cwd"] != "/dir-a" || rowB["cwd"] != "/dir-b" {
		t.Fatalf("cwds lost: %v %v", rowA["cwd"], rowB["cwd"])
	}
	if rowA["name"] != "from-a" || rowB["name"] != "from-b" {
		t.Fatalf("names: %v %v", rowA["name"], rowB["name"])
	}
	// crontab lines rewritten to the new ids
	if !strings.Contains(ct.text, "pane-scheduler:j1") || !strings.Contains(ct.text, "pane-scheduler:j2") {
		t.Fatalf("crontab not rewritten to the new ids:\n%s", ct.text)
	}
	if strings.Contains(ct.text, "cwd-") {
		t.Fatalf("legacy keys still present:\n%s", ct.text)
	}
	// old files moved aside
	for _, name := range []string{"aaaa1111bbbb.sqlite.migrated", "cccc2222dddd.sqlite.migrated"} {
		if _, err := os.Stat(filepath.Join(home, name)); err != nil {
			t.Fatalf("old store not moved aside: %s", name)
		}
	}
	if _, err := os.Stat(filepath.Join(home, "cwd-aaaa1111bbbb.sqlite")); err == nil {
		t.Fatal("cwd store not moved")
	}
	// runs trail brought along, re-keyed
	runs, err := sched.Runs(context.Background(), gdb, "j1", 0)
	if err != nil {
		t.Fatal(err)
	}
	contains(t, runs, "1 run")
	contains(t, runs, "ok")
}

func TestMigrationKeepsExistingGlobalJobs(t *testing.T) {
	home := t.TempDir()
	ct := newFakeCrontab("SHELL=/bin/bash\n")
	legacyStore(t, home, "aaaa1111bbbb", ct, []legacyJob{
		{Name: "from-a", Prompt: "p", Cron: "0 0 * * *", Cwd: "/dir-a"},
	}, false)
	gdb, _, _, err := store.Open(filepath.Join(home, "global.sqlite"), sched.Statements(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sched.Create(context.Background(), gdb, ct, sched.CreateInput{
		Name: "global-job", Prompt: "p", Cron: "0 2 * * *", Cwd: "/global",
	}, "/sess", "sess", runnerCmd, func() time.Time { return nowFixed }); err != nil {
		t.Fatal(err)
	}
	gdb.DB.Close()

	globalPath := filepath.Join(home, "global.sqlite")
	gdb2, _, report, err := store.Open(globalPath, sched.Statements(), sched.SchemaVersion, sched.Migration(home, ct))
	if err != nil {
		t.Fatal(err)
	}
	defer gdb2.DB.Close()
	if !strings.Contains(report, "folded 1 job") {
		t.Fatalf("report %q", report)
	}
	rowG := jobsRow(t, &harness{db: gdb2}, "j1")
	rowF := jobsRow(t, &harness{db: gdb2}, "j2")
	if rowG == nil || rowF == nil {
		t.Fatal("the existing global job and the folded job must both survive")
	}
	if rowG["name"] != "global-job" || rowG["cwd"] != "/global" {
		t.Fatalf("global job lost: %v", rowG)
	}
	if rowF["name"] != "from-a" || rowF["cwd"] != "/dir-a" {
		t.Fatalf("folded job lost: %v", rowF)
	}
}

func TestMigrationIsANoOpOnTheSecondOpen(t *testing.T) {
	home := t.TempDir()
	ct := newFakeCrontab("SHELL=/bin/bash\n")
	legacyStore(t, home, "aaaa1111bbbb", ct, []legacyJob{
		{Name: "from-a", Prompt: "p", Cron: "0 0 * * *", Cwd: "/dir-a"},
	}, false)
	globalPath := filepath.Join(home, "global.sqlite")
	seedLegacyGlobal(t, home)
	if _, _, report, err := store.Open(globalPath, sched.Statements(), sched.SchemaVersion, sched.Migration(home, ct)); err != nil {
		t.Fatal(err)
	} else if !strings.Contains(report, "folded") {
		t.Fatalf("first open report %q", report)
	}
	gdb, _, report, err := store.Open(globalPath, sched.Statements(), sched.SchemaVersion, sched.Migration(home, ct))
	if err != nil {
		t.Fatal(err)
	}
	defer gdb.DB.Close()
	if report != "" {
		t.Fatalf("second open must do nothing, got report %q", report)
	}
	var n int
	if err := gdb.DB.QueryRow(`SELECT count(*) FROM jobs WHERE state != 'removed'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("jobs = %d, want 1 (no duplicate fold)", n)
	}
}

func TestMigrationMovesEmptyFileAsideWithNoRow(t *testing.T) {
	home := t.TempDir()
	ct := newFakeCrontab("SHELL=/bin/bash\n")
	db, _, _, err := store.Open(filepath.Join(home, "cwd-aaaa1111bbbb.sqlite"), sched.Statements(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sched.Create(context.Background(), db, ct, sched.CreateInput{
		Name: "gone", Prompt: "p", Cron: "0 0 * * *", Cwd: "/dir-a",
	}, "/sess", "sess", runnerCmd, func() time.Time { return nowFixed }); err != nil {
		t.Fatal(err)
	}
	if _, err := sched.Remove(context.Background(), db, ct, "j1", "/sess", "sess"); err != nil {
		t.Fatal(err)
	}
	if err := db.DB.Close(); err != nil {
		t.Fatal(err)
	}
	globalPath := filepath.Join(home, "global.sqlite")
	seedLegacyGlobal(t, home)
	gdb, _, report, err := store.Open(globalPath, sched.Statements(), sched.SchemaVersion, sched.Migration(home, ct))
	if err != nil {
		t.Fatal(err)
	}
	defer gdb.DB.Close()
	if report != "" {
		t.Fatalf("empty file must write no row, got report %q", report)
	}
	var n int
	if err := gdb.DB.QueryRow(`SELECT count(*) FROM jobs WHERE state != 'removed'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("jobs = %d, want 0", n)
	}
	if _, err := os.Stat(filepath.Join(home, "aaaa1111bbbb.sqlite.migrated")); err != nil {
		t.Fatal("empty store must still move aside")
	}
}
