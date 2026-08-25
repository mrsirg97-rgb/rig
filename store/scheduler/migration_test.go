package scheduler_test

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
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
	db, _, _, err := store.Open(filepath.Join(home, hash+".sqlite"), sched.Statements(), 1)
	if err != nil {
		t.Fatalf("open legacy store: %v", err)
	}
	for _, j := range jobs {
		if _, err := sched.Create(context.Background(), db, ct, sched.CreateInput{
			Model: "w",
			Name:  j.Name, Prompt: j.Prompt, Cron: j.Cron, Cwd: j.Cwd,
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
	if !strings.Contains(ct.text, "pane-scheduler:j1") || !strings.Contains(ct.text, "pane-scheduler:j2") {
		t.Fatalf("crontab not rewritten to the new ids:\n%s", ct.text)
	}
	if strings.Contains(ct.text, "cwd-") {
		t.Fatalf("legacy keys still present:\n%s", ct.text)
	}
	for _, name := range []string{"aaaa1111bbbb.sqlite.migrated", "cccc2222dddd.sqlite.migrated"} {
		if _, err := os.Stat(filepath.Join(home, name)); err != nil {
			t.Fatalf("old store not moved aside: %s", name)
		}
	}
	if _, err := os.Stat(filepath.Join(home, "aaaa1111bbbb.sqlite")); err == nil {
		t.Fatal("cwd store not moved")
	}
	if _, err := os.Stat(filepath.Join(home, "global.sqlite.migrated")); err == nil {
		t.Fatal("the global store is not a legacy file")
	}
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
		Model: "w",
		Name:  "global-job", Prompt: "p", Cron: "0 2 * * *", Cwd: "/global",
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
	db, _, _, err := store.Open(filepath.Join(home, "aaaa1111bbbb.sqlite"), sched.Statements(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sched.Create(context.Background(), db, ct, sched.CreateInput{
		Model: "w",
		Name:  "gone", Prompt: "p", Cron: "0 0 * * *", Cwd: "/dir-a",
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

func TestMigrationRewritesJ1AndJ10WithoutCorruptingEither(t *testing.T) {
	home := t.TempDir()
	ct := newFakeCrontab("SHELL=/bin/bash\n")
	var jobs []legacyJob
	for i := 1; i <= 10; i++ {
		jobs = append(jobs, legacyJob{Name: "n" + strconv.Itoa(i), Prompt: "p", Cron: "0 " + strconv.Itoa(i) + " * * *", Cwd: "/dir"})
	}
	legacyStore(t, home, "aaaa1111bbbb", ct, jobs, false)
	if !strings.Contains(ct.text, "cwd-aaaa1111bbbb:j10") {
		t.Fatalf("j10 not staged:\n%s", ct.text)
	}
	gdb, _, report, err := store.Open(filepath.Join(home, "global.sqlite"), sched.Statements(), sched.SchemaVersion, sched.Migration(home, ct))
	if err != nil {
		t.Fatal(err)
	}
	defer gdb.DB.Close()
	if !strings.Contains(report, "folded 10 jobs") {
		t.Fatalf("report %q", report)
	}
	if strings.Contains(ct.text, "cwd-") {
		t.Fatalf("legacy keys remain:\n%s", ct.text)
	}
	seen := map[string]bool{}
	for _, m := range regexp.MustCompile(`run-job (j\d+)`).FindAllStringSubmatch(ct.text, -1) {
		seen[m[1]] = true
	}
	for i := 1; i <= 10; i++ {
		if !seen["j"+strconv.Itoa(i)] {
			t.Fatalf("j%d missing from the rewritten crontab (a prefix rewrite corrupts j1/j10):\n%s", i, ct.text)
		}
	}
	if len(seen) != 10 {
		t.Fatalf("%d distinct ids in the crontab, want 10:\n%s", len(seen), ct.text)
	}
	for i := 1; i <= 10; i++ {
		row := jobsRow(t, &harness{db: gdb}, "j"+strconv.Itoa(i))
		if row == nil || row["name"] != "n"+strconv.Itoa(i) {
			t.Fatalf("j%d must be n%d (the fold is in id order): %v", i, i, row)
		}
	}
}

func TestMigrationFoldsWithNoGlobalStoreYet(t *testing.T) {
	home := t.TempDir()
	ct := newFakeCrontab("SHELL=/bin/bash\n")
	legacyStore(t, home, "aaaa1111bbbb", ct, []legacyJob{
		{Name: "only-cwd", Prompt: "p", Cron: "0 0 * * *", Cwd: "/dir-a"},
	}, false)
	if _, err := os.Stat(filepath.Join(home, "global.sqlite")); err == nil {
		t.Fatal("precondition: no global store")
	}
	gdb, _, report, err := store.Open(filepath.Join(home, "global.sqlite"), sched.Statements(), sched.SchemaVersion, sched.Migration(home, ct))
	if err != nil {
		t.Fatal(err)
	}
	defer gdb.DB.Close()
	if !strings.Contains(report, "folded 1 job") {
		t.Fatalf("a fresh global store must still fold the legacy stores, report %q", report)
	}
	if row := jobsRow(t, &harness{db: gdb}, "j1"); row == nil || row["name"] != "only-cwd" {
		t.Fatalf("folded job missing: %v", row)
	}
	if strings.Contains(ct.text, "cwd-") {
		t.Fatalf("legacy keys remain:\n%s", ct.text)
	}
}

func TestMigrationMovesTheSidecarsAside(t *testing.T) {
	home := t.TempDir()
	ct := newFakeCrontab("SHELL=/bin/bash\n")
	legacyStore(t, home, "aaaa1111bbbb", ct, []legacyJob{
		{Name: "a", Prompt: "p", Cron: "0 0 * * *", Cwd: "/dir-a"},
	}, false)
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.WriteFile(filepath.Join(home, "aaaa1111bbbb.sqlite"+suffix), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gdb, _, _, err := store.Open(filepath.Join(home, "global.sqlite"), sched.Statements(), sched.SchemaVersion, sched.Migration(home, ct))
	if err != nil {
		t.Fatal(err)
	}
	defer gdb.DB.Close()
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if _, err := os.Stat(filepath.Join(home, "aaaa1111bbbb.sqlite"+suffix)); err == nil {
			t.Fatalf("legacy file left behind under its old name: aaaa1111bbbb.sqlite%s", suffix)
		}
	}
	if _, err := os.Stat(filepath.Join(home, "aaaa1111bbbb.sqlite.migrated")); err != nil {
		t.Fatal("the legacy store must move aside")
	}
}

func TestRunJobNamesALegacyKey(t *testing.T) {
	err := sched.RunJob("cwd-aaaa1111bbbb:j1", sched.RunOpts{
		Home:      t.TempDir(),
		Crontab:   newFakeCrontab(""),
		Fetch:     fakeFetch(nil, fetchOpts{}),
		Spawn:     (&fakeSpawn{}).spawn,
		WorkerCmd: []string{"/x/rig"},
		Now:       func() time.Time { return runnerNow },
	})
	if err == nil || !strings.Contains(err.Error(), "legacy key") || !strings.Contains(err.Error(), "start rig once") {
		t.Fatalf("a legacy key must be named with the fix, got %v", err)
	}
}
