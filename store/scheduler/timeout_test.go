package scheduler_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/store"
	sched "github.com/mrsirg97-rgb/rig/store/scheduler"
)

func TestCreateWithTimeoutPersistsColumnEventAndListing(t *testing.T) {
	h := newHarness(t, realCwd(t, "to"))
	_, err := h.create(sched.CreateInput{
		Name: "long", Prompt: "p", Cron: "0 */4 * * *",
		Model: "qwen3.8-workers", Timeout: 60,
	})
	mustOK(t, err)
	if got := jobsRow(t, h, "j1")["timeout"]; got != int64(60) {
		t.Fatalf("the timeout must ride the row: %v", jobsRow(t, h, "j1"))
	}
	args := eventArgs(t, h, "create")
	if args["timeout"] != float64(60) {
		t.Fatalf("the create event must carry the timeout: %v", args)
	}
	listing, err := h.list()
	mustOK(t, err)
	contains(t, listing, "timeout 60m")
}

func TestCreateWithoutTimeoutStaysNullAndUnlisted(t *testing.T) {
	h := newHarness(t, realCwd(t, "todefault"))
	_, err := h.create(sched.CreateInput{
		Name: "plain", Prompt: "p", Cron: "0 */4 * * *", Model: "qwen3.8-workers",
	})
	mustOK(t, err)
	if got := jobsRow(t, h, "j1")["timeout"]; got != nil {
		t.Fatalf("an unset timeout must be NULL, got %v", got)
	}
	if _, ok := eventArgs(t, h, "create")["timeout"]; ok {
		t.Fatalf("an unset timeout must not ride the create event: %v", eventArgs(t, h, "create"))
	}
	listing, err := h.list()
	mustOK(t, err)
	if strings.Contains(listing, "timeout") {
		t.Fatalf("a job without a timeout must not render one: %s", listing)
	}
}

func TestCreateRefusesZeroAndOverA24hTimeoutByName(t *testing.T) {
	h := newHarness(t, realCwd(t, "toor"))
	for _, bad := range []int{-5, 1441} {
		_, err := h.create(sched.CreateInput{
			Name: "bad", Prompt: "p", Cron: "0 */4 * * *",
			Model: "qwen3.8-workers", Timeout: bad,
		})
		mustErr(t, err, "timeout")
	}
	_, err := h.create(sched.CreateInput{
		Name: "max", Prompt: "p", Cron: "0 */4 * * *",
		Model: "qwen3.8-workers", Timeout: 1440,
	})
	mustOK(t, err)
}

func TestUpdateSetsAndResetsTheTimeoutWithZeroAsTheDefault(t *testing.T) {
	h := newHarness(t, realCwd(t, "tou"))
	_, err := h.create(sched.CreateInput{
		Name: "u", Prompt: "p", Cron: "0 */4 * * *", Model: "qwen3.8-workers", Timeout: 45,
	})
	mustOK(t, err)
	_, err = h.update(sched.UpdateInput{ID: "j1", Timeout: 60})
	mustOK(t, err)
	if got := jobsRow(t, h, "j1")["timeout"]; got != int64(60) {
		t.Fatalf("update must set the timeout: %v", got)
	}
	_, err = h.update(sched.UpdateInput{ID: "j1", Timeout: 0})
	mustErr(t, err, "needs a change")
	_, err = h.update(sched.UpdateInput{ID: "j1", Timeout: -1})
	mustOK(t, err)
	if got := jobsRow(t, h, "j1")["timeout"]; got != nil {
		t.Fatalf("timeout -1 resets to the runner default (NULL): %v", got)
	}
	listing, err := h.list()
	mustOK(t, err)
	if strings.Contains(listing, "timeout") {
		t.Fatalf("a reset job must not render a timeout: %s", listing)
	}
}

func TestUpdateRefusesAnOutOfRangeTimeoutByName(t *testing.T) {
	h := newHarness(t, realCwd(t, "tour"))
	_, err := h.create(sched.CreateInput{
		Name: "u", Prompt: "p", Cron: "0 */4 * * *", Model: "qwen3.8-workers",
	})
	mustOK(t, err)
	for _, bad := range []int{-2, 1441} {
		_, err = h.update(sched.UpdateInput{ID: "j1", Timeout: bad})
		mustErr(t, err, "timeout")
	}
}

func TestACommandJobCarriesItsTimeoutToo(t *testing.T) {
	cwd := realCwd(t, "tocmd")
	h := newHarness(t, cwd)
	_, err := h.create(sched.CreateInput{
		Name: "c", Command: "true", Cron: "20 10 * * *", Cwd: cwd, Timeout: 90,
	})
	mustOK(t, err)
	if got := jobsRow(t, h, "j1")["timeout"]; got != int64(90) {
		t.Fatalf("the timeout is orthogonal to the kind: %v", got)
	}
	spawn := &fakeSpawn{result: sched.SpawnResult{Exit: 0}}
	if err := sched.RunJob("j1", sched.RunOpts{
		Home: h.home, Crontab: h.ct, Fetch: func(string) (json.RawMessage, error) {
			return nil, jsonError("busy probe must not run")
		},
		Spawn: spawn.spawn, WorkerCmd: []string{"/x/rig"},
		Now: func() time.Time { return runnerNow }, Sandbox: "off",
	}); err != nil {
		t.Fatal(err)
	}
	if len(spawn.calls) != 1 {
		t.Fatalf("the command must still fire once: %d", len(spawn.calls))
	}
}

func TestTimeoutSurvivesCompactionFoldAndRewrite(t *testing.T) {
	h := newHarness(t, realCwd(t, "tocompact"))
	_, err := h.create(sched.CreateInput{
		Name: "kept", Prompt: "p", Cron: "0 */4 * * *",
		Model: "qwen3.8-workers", Timeout: 60,
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
	var timeout any
	if err := h.db.DB.QueryRow(`SELECT timeout FROM jobs WHERE id='j1'`).Scan(&timeout); err != nil {
		t.Fatal(err)
	}
	if timeout != int64(60) {
		t.Fatalf("the timeout must survive the compact fold, got %v", timeout)
	}
	listing, err := h.list()
	mustOK(t, err)
	contains(t, listing, "timeout 60m")
}

func eventArgs(t *testing.T, h *harness, op string) map[string]any {
	t.Helper()
	var args string
	if err := h.db.DB.QueryRow(`SELECT args FROM events WHERE op=? ORDER BY seq LIMIT 1`, op).Scan(&args); err != nil {
		t.Fatalf("no %s event: %v", op, err)
	}
	return argsJSON(t, args)
}

func TestRunJobBoundsTheSpawnContextByThePerJobTimeout(t *testing.T) {
	cwd := realCwd(t, "torun")
	h := newHarness(t, cwd)
	_, err := h.create(sched.CreateInput{
		Name: "long", Prompt: "p", Cron: "0 */4 * * *",
		Model: "qwen3.8-workers", Cwd: cwd, Timeout: 60,
	})
	mustOK(t, err)

	remaining := runAndCatchDeadline(t, h, 5*time.Minute)
	if remaining < 59*time.Minute || remaining > 60*time.Minute {
		t.Fatalf("the job's own timeout must bound the spawn context, got %v", remaining)
	}
}

func TestRunJobWithoutAPerJobTimeoutKeepsTheDefault(t *testing.T) {
	cwd := realCwd(t, "torunDefault")
	h := newHarness(t, cwd)
	_, err := h.create(sched.CreateInput{
		Name: "plain", Prompt: "p", Cron: "0 */4 * * *",
		Model: "qwen3.8-workers", Cwd: cwd,
	})
	mustOK(t, err)

	remaining := runAndCatchDeadline(t, h, 0)
	if remaining < 29*time.Minute || remaining > 30*time.Minute {
		t.Fatalf("an unbound job keeps the 30 minute default, got %v", remaining)
	}
}

func runAndCatchDeadline(t *testing.T, h *harness, optsTimeout time.Duration) time.Duration {
	t.Helper()
	var remaining time.Duration
	spawn := func(ctx context.Context, argv []string, wd string, env []string, observe func([]byte)) (sched.SpawnResult, error) {
		dl, ok := ctx.Deadline()
		if !ok {
			t.Fatal("the spawn context must carry a deadline")
		}
		remaining = time.Until(dl)
		return sched.SpawnResult{Exit: 0}, nil
	}
	opts := runOpts(h, nil, &fakeSpawn{}, fetchOpts{})
	opts.Spawn = spawn
	opts.Timeout = optsTimeout
	if err := sched.RunJob("j1", opts); err != nil {
		t.Fatal(err)
	}
	return remaining
}

func TestMigrationAddsTheTimeoutColumnToASchemaThreeStore(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "global.sqlite")
	db, _, _, err := store.Open(path, schemaThreeStatements(), 3)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB.Exec(`INSERT INTO jobs (id, name, prompt, cron, cwd, model, busy, state, created_seq, updated_seq)
		VALUES ('j1', 'old', 'p', '0 */4 * * *', '/x', 'm', 'skip', 'active', 1, 1)`); err != nil {
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
	var timeout any
	if err := gdb.DB.QueryRow(`SELECT timeout FROM jobs WHERE id='j1'`).Scan(&timeout); err != nil {
		t.Fatalf("the migrated jobs table must carry the timeout column: %v", err)
	}
	if timeout != nil {
		t.Fatalf("a pre-timeout job defaults to NULL, got %v", timeout)
	}
	var count int
	if err := gdb.DB.QueryRow(`SELECT count(*) FROM jobs WHERE id='j1'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("the migration must preserve the row, got %d", count)
	}
}

func schemaThreeStatements() []string {
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
