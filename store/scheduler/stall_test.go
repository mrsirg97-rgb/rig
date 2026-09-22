package scheduler_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/store"
	sched "github.com/mrsirg97-rgb/rig/store/scheduler"
)

func TestCreateWithStallPersistsColumnEventAndListing(t *testing.T) {
	h := newHarness(t, realCwd(t, "stallcreate"))
	_, err := h.create(sched.CreateInput{
		Name: "watch", Prompt: "p", Cron: "0 */4 * * *",
		Model: "qwen3.8-workers", Stall: 30,
	})
	mustOK(t, err)
	if got := jobsRow(t, h, "j1")["stall"]; got != int64(30) {
		t.Fatalf("the stall must ride the row: %v", jobsRow(t, h, "j1"))
	}
	args := eventArgs(t, h, "create")
	if args["stall"] != float64(30) {
		t.Fatalf("the create event must carry the stall: %v", args)
	}
	listing, err := h.list()
	mustOK(t, err)
	contains(t, listing, "stall 30m")
}

func TestCreateWithoutStallStaysNullAndUnlisted(t *testing.T) {
	h := newHarness(t, realCwd(t, "silence-default"))
	_, err := h.create(sched.CreateInput{
		Name: "plain", Prompt: "p", Cron: "0 */4 * * *", Model: "qwen3.8-workers",
	})
	mustOK(t, err)
	if got := jobsRow(t, h, "j1")["stall"]; got != nil {
		t.Fatalf("an unset stall must be NULL, got %v", got)
	}
	if _, ok := eventArgs(t, h, "create")["stall"]; ok {
		t.Fatalf("an unset stall must not ride the create event: %v", eventArgs(t, h, "create"))
	}
	listing, err := h.list()
	mustOK(t, err)
	if strings.Contains(listing, "stall ") {
		t.Fatalf("a job without a stall must not render one: %s", listing)
	}
}

func TestCreateRefusesZeroAndOverA24hStallByName(t *testing.T) {
	h := newHarness(t, realCwd(t, "stallrange"))
	for _, bad := range []int{-5, 1441} {
		_, err := h.create(sched.CreateInput{
			Name: "bad", Prompt: "p", Cron: "0 */4 * * *",
			Model: "qwen3.8-workers", Stall: bad,
		})
		mustErr(t, err, "stall")
	}
	_, err := h.create(sched.CreateInput{
		Name: "max", Prompt: "p", Cron: "0 */4 * * *",
		Model: "qwen3.8-workers", Stall: 1440,
	})
	mustOK(t, err)
}

func TestUpdateSetsAndResetsTheStallWithMinusOneAsTheDefault(t *testing.T) {
	h := newHarness(t, realCwd(t, "silence-update"))
	_, err := h.create(sched.CreateInput{
		Name: "u", Prompt: "p", Cron: "0 */4 * * *", Model: "qwen3.8-workers", Stall: 45,
	})
	mustOK(t, err)
	_, err = h.update(sched.UpdateInput{ID: "j1", Stall: 30})
	mustOK(t, err)
	if got := jobsRow(t, h, "j1")["stall"]; got != int64(30) {
		t.Fatalf("update must set the stall: %v", got)
	}
	_, err = h.update(sched.UpdateInput{ID: "j1", Stall: 0})
	mustErr(t, err, "needs a change")
	_, err = h.update(sched.UpdateInput{ID: "j1", Stall: -1})
	mustOK(t, err)
	if got := jobsRow(t, h, "j1")["stall"]; got != nil {
		t.Fatalf("stall -1 resets to the default (NULL): %v", got)
	}
	listing, err := h.list()
	mustOK(t, err)
	if strings.Contains(listing, "stall ") {
		t.Fatalf("a reset job must not render a stall: %s", listing)
	}
}

func TestUpdateRefusesAnOutOfRangeStallByName(t *testing.T) {
	h := newHarness(t, realCwd(t, "stallrange2"))
	_, err := h.create(sched.CreateInput{
		Name: "u", Prompt: "p", Cron: "0 */4 * * *", Model: "qwen3.8-workers",
	})
	mustOK(t, err)
	for _, bad := range []int{-2, 1441} {
		_, err = h.update(sched.UpdateInput{ID: "j1", Stall: bad})
		mustErr(t, err, "stall")
	}
}

func TestStallSurvivesCompactionFoldAndRewrite(t *testing.T) {
	h := newHarness(t, realCwd(t, "stallcompact"))
	_, err := h.create(sched.CreateInput{
		Name: "kept", Prompt: "p", Cron: "0 */4 * * *",
		Model: "qwen3.8-workers", Stall: 60,
	})
	mustOK(t, err)
	for i := 0; i < 1000; i++ {
		if _, err := h.db.DB.Exec(
			`INSERT INTO events (seq, ts, op, args, session) VALUES (?, ?, 'noop', '{}', NULL)`,
			2+i, nowFixed.Format(time.RFC3339)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h.update(sched.UpdateInput{ID: "j1", Name: "kept2"}); err != nil {
		t.Fatal(err)
	}
	var compacted string
	if err := h.db.DB.QueryRow(`SELECT op FROM events WHERE op='compact' LIMIT 1`).Scan(&compacted); err != nil {
		t.Fatalf("the threshold compaction must have run: %v", err)
	}
	var stall any
	if err := h.db.DB.QueryRow(`SELECT stall FROM jobs WHERE id='j1'`).Scan(&stall); err != nil {
		t.Fatal(err)
	}
	if stall != int64(60) {
		t.Fatalf("the stall must survive the compact fold, got %v", stall)
	}
	listing, err := h.list()
	mustOK(t, err)
	contains(t, listing, "stall 60m")
}

func TestMigrationAddsTheStallColumnToASchemaFourStore(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "global.sqlite")
	db, _, _, err := store.Open(path, schemaFourStatements(), 4)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB.Exec(`INSERT INTO jobs (id, name, prompt, cron, cwd, model, busy, timeout, state, created_seq, updated_seq)
		VALUES ('j1', 'old', 'p', '0 */4 * * *', '/x', 'm', 'skip', 60, 'active', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if err := db.DB.Close(); err != nil {
		t.Fatal(err)
	}

	gdb, _, _, err := store.Open(path, sched.Statements(), sched.SchemaVersion, sched.Migration(home, newFakeCrontab("")))
	if err != nil {
		t.Fatal(err)
	}
	defer gdb.DB.Close()
	var stall any
	if err := gdb.DB.QueryRow(`SELECT stall FROM jobs WHERE id='j1'`).Scan(&stall); err != nil {
		t.Fatalf("the migrated jobs table must carry the stall column: %v", err)
	}
	if stall != nil {
		t.Fatalf("a pre-stall job defaults to NULL, got %v", stall)
	}
	var timeout any
	if err := gdb.DB.QueryRow(`SELECT timeout FROM jobs WHERE id='j1'`).Scan(&timeout); err != nil {
		t.Fatal(err)
	}
	if timeout != int64(60) {
		t.Fatalf("the migration must preserve the timeout, got %v", timeout)
	}
	var count int
	if err := gdb.DB.QueryRow(`SELECT count(*) FROM jobs WHERE id='j1'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("the migration must preserve the row, got %d", count)
	}
}

func TestRunJobKillsASilentFireAfterTheStallWindow(t *testing.T) {
	cwd := realCwd(t, "stallkill")
	h := newHarness(t, cwd)
	_, err := h.create(sched.CreateInput{
		Name: "silent", Prompt: "p", Cron: "0 */4 * * *",
		Model: "qwen3.8-workers", Cwd: cwd,
	})
	mustOK(t, err)
	spawn := func(ctx context.Context, argv []string, wd string, env []string, observe func([]byte)) (sched.SpawnResult, error) {
		<-ctx.Done()
		return sched.SpawnResult{Exit: 1}, nil
	}
	opts := runOpts(h, nil, &fakeSpawn{}, fetchOpts{})
	opts.Spawn = spawn
	opts.Stall = 60 * time.Millisecond
	if err := sched.RunJob("j1", opts); err != nil {
		t.Fatal(err)
	}
	logBody := readRunLog(t, h, "j1", logName)
	if !strings.Contains(logBody, "[runner: killed after stall]") {
		t.Fatalf("a stalled fire must name the reason in its log: %s", logBody)
	}
	if !strings.Contains(logBody, "exit=1") {
		t.Fatalf("a stalled fire must record exit 1: %s", logBody)
	}
	runs, err := h.runs("j1", 1)
	mustOK(t, err)
	if !strings.Contains(runs, "fail") {
		t.Fatalf("a stalled fire must record a fail run: %s", runs)
	}
	if _, err := os.Stat(filepath.Join(h.home, "runs", "j1", "2026-08-15T12-00-00-000Z.stream")); !os.IsNotExist(err) {
		t.Fatalf("the live stream must be removed when the run ends: %v", err)
	}
}

func TestRunJobKeepsAFireThatHeartbeatsOnStderr(t *testing.T) {
	cwd := realCwd(t, "stallactive")
	h := newHarness(t, cwd)
	_, err := h.create(sched.CreateInput{
		Name: "chatty", Prompt: "p", Cron: "0 */4 * * *",
		Model: "qwen3.8-workers", Cwd: cwd,
	})
	mustOK(t, err)
	spawn := func(ctx context.Context, argv []string, wd string, env []string, observe func([]byte)) (sched.SpawnResult, error) {
		answer := "the answer\n"
		heartbeats := ""
		for i := 0; i < 8; i++ {
			observe([]byte("rig: heartbeat\n"))
			heartbeats += "rig: heartbeat\n"
			time.Sleep(50 * time.Millisecond)
		}
		return sched.SpawnResult{Exit: 0, Stdout: answer, Stderr: heartbeats}, nil
	}
	opts := runOpts(h, nil, &fakeSpawn{}, fetchOpts{})
	opts.Spawn = spawn
	opts.Stall = 500 * time.Millisecond
	if err := sched.RunJob("j1", opts); err != nil {
		t.Fatal(err)
	}
	logBody := readRunLog(t, h, "j1", logName)
	if strings.Contains(logBody, "killed after stall") {
		t.Fatalf("a fire silent on stdout but heartbeating on stderr must never stall: %s", logBody)
	}
	if !strings.Contains(logBody, "exit=0") {
		t.Fatalf("the heartbeating fire must end ok: %s", logBody)
	}
	if !strings.Contains(logBody, "the answer") {
		t.Fatalf("stdout must still carry the answer: %s", logBody)
	}
}

func TestRealSpawnReportsEveryOutputByteToTheObserver(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var mu sync.Mutex
	var seen []byte
	observe := func(p []byte) {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, p...)
	}
	res, err := sched.RealSpawn(ctx, []string{"sh", "-c", "printf out; printf err >&2"}, t.TempDir(), os.Environ(), observe)
	if err != nil {
		t.Fatal(err)
	}
	if res.Exit != 0 {
		t.Fatalf("the spawn must exit 0, got %d", res.Exit)
	}
	got := string(seen)
	if !strings.Contains(got, "out") || !strings.Contains(got, "err") {
		t.Fatalf("the observer must see both streams, got %q", got)
	}
}

func readRunLog(t *testing.T, h *harness, id, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(h.home, "runs", id, name))
	if err != nil {
		t.Fatalf("read run log: %v", err)
	}
	return string(b)
}

func schemaFourStatements() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS "events" (
  "seq" INTEGER NOT NULL,
  "args" TEXT NOT NULL,
  "op" TEXT NOT NULL,
  "session" TEXT,
  "ts" TEXT NOT NULL,
  PRIMARY KEY ("seq")
)`,
		`CREATE TABLE IF NOT EXISTS "jobs" (
  "id" TEXT NOT NULL,
  "at" TEXT,
  "busy" TEXT NOT NULL,
  "command" TEXT,
  "created_seq" INTEGER NOT NULL,
  "cron" TEXT NOT NULL,
  "cwd" TEXT NOT NULL,
  "last_exit" INTEGER,
  "last_status" TEXT,
  "last_ts" TEXT,
  "model" TEXT NOT NULL,
  "name" TEXT NOT NULL,
  "prompt" TEXT NOT NULL,
  "state" TEXT NOT NULL,
  "timeout" INTEGER,
  "updated_seq" INTEGER NOT NULL,
  PRIMARY KEY ("id")
)`,
		`CREATE TABLE IF NOT EXISTS "meta" (
  "key" TEXT NOT NULL,
  "value" TEXT NOT NULL,
  PRIMARY KEY ("key")
)`,
		`CREATE TABLE IF NOT EXISTS "runs" (
  "seq" INTEGER NOT NULL,
  "duration_ms" INTEGER,
  "ended_at" TEXT NOT NULL,
  "exit" INTEGER,
  "job_id" TEXT NOT NULL,
  "log_path" TEXT,
  "reason" TEXT,
  "started_at" TEXT NOT NULL,
  "status" TEXT NOT NULL,
  PRIMARY KEY ("seq")
)`,
	}
}
