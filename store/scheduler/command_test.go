package scheduler_test

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/store"
	sched "github.com/mrsirg97-rgb/rig/store/scheduler"
)

func TestCommandJobFiresTheLineNotTheWorker(t *testing.T) {
	cwd := realCwd(t, "cmdjob")
	h := newHarness(t, cwd)
	reply, err := h.create(sched.CreateInput{
		Name: "tick", Command: "echo hello", Cron: "20 10 * * *", Cwd: cwd,
	})
	mustOK(t, err)
	contains(t, reply, "created j1")
	contains(t, h.ct.text, "20 10 * * * "+runnerCmd+" j1  # pane-scheduler:j1")
	row := jobsRow(t, h, "j1")
	if row["prompt"] != "" || row["model"] != "" {
		t.Fatalf("a command job carries no prompt and no model: %v", row)
	}
	if row["command"] != "echo hello" {
		t.Fatalf("the command must be on the row: %v", row)
	}
	listing, err := h.list()
	mustOK(t, err)
	contains(t, listing, "command echo hello")

	spawn := &fakeSpawn{result: sched.SpawnResult{Exit: 0, Stdout: "hello"}}
	fetchCalls := 0
	fetch := func(url string) (json.RawMessage, error) {
		fetchCalls++
		return nil, jsonError("busy probe must not run")
	}
	if err := sched.RunJob("j1", sched.RunOpts{
		Home: h.home, Crontab: h.ct, Fetch: fetch, Spawn: spawn.spawn,
		WorkerCmd: []string{"/x/rig"}, Now: func() time.Time { return runnerNow },
		Sandbox: "off",
	}); err != nil {
		t.Fatal(err)
	}
	if fetchCalls != 0 {
		t.Fatalf("a command job's fire must skip the busy probe, ran %d times", fetchCalls)
	}
	if len(spawn.calls) != 1 {
		t.Fatalf("exactly one spawn, got %d", len(spawn.calls))
	}
	call := spawn.calls[0]
	if !reflect.DeepEqual(call.Argv, []string{"sh", "-c", "echo hello"}) {
		t.Fatalf("the fire must run the line under sh -c: %v", call.Argv)
	}
	if call.Cwd != cwd {
		t.Fatalf("the fire must run in the job's cwd: %s", call.Cwd)
	}
	runs, err := h.runs("j1", 0)
	mustOK(t, err)
	contains(t, runs, "1 run")
	contains(t, runs, "ok")
}

func TestCommandOnceJobConsumesItselfAfterTheFire(t *testing.T) {
	cwd := realCwd(t, "oncecmd")
	h := newHarness(t, cwd)
	_, err := h.create(sched.CreateInput{
		Name: "once", Command: "true", Cron: "once",
		At: "2026-08-16T00:00:00Z", Cwd: cwd,
	})
	mustOK(t, err)
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
	if strings.Contains(h.ct.text, "pane-scheduler:j1") {
		t.Fatalf("a consumed once job's line must be gone: %s", h.ct.text)
	}
	if row := jobsRow(t, h, "j1"); row["state"] != "done" {
		t.Fatalf("the once fire must move the job to done: %v", row["state"])
	}
}

func TestCommandJobFailureIsRecordedFail(t *testing.T) {
	cwd := realCwd(t, "failcmd")
	h := newHarness(t, cwd)
	if _, err := h.create(sched.CreateInput{
		Name: "fails", Command: "false", Cron: "0 3 * * *", Cwd: cwd,
	}); err != nil {
		t.Fatal(err)
	}
	spawn := &fakeSpawn{result: sched.SpawnResult{Exit: 2, Stderr: "boom"}}
	if err := sched.RunJob("j1", sched.RunOpts{
		Home: h.home, Crontab: h.ct, Fetch: func(string) (json.RawMessage, error) {
			return nil, jsonError("busy probe must not run")
		},
		Spawn: spawn.spawn, WorkerCmd: []string{"/x/rig"},
		Now: func() time.Time { return runnerNow }, Sandbox: "off",
	}); err != nil {
		t.Fatal(err)
	}
	runs, err := h.runs("j1", 0)
	mustOK(t, err)
	contains(t, runs, "fail")
	contains(t, runs, "exit 2")
}

func TestCreateRefusesACommandMixedWithTheModelPayloads(t *testing.T) {
	h := newHarness(t, "/ws/cmd-mix")
	for _, in := range []sched.CreateInput{
		{Name: "a", Command: "true", Prompt: "p", Cron: "0 3 * * *", Model: "w"},
		{Name: "b", Command: "true", Cron: "0 3 * * *", Model: "w"},
		{Name: "c", Command: "true", Cron: "0 3 * * *", Busy: "force"},
		{Name: "d", Cron: "0 3 * * *", Model: "w"},
	} {
		_, err := h.create(in)
		if err == nil {
			t.Fatalf("create %+v must refuse", in)
		}
		switch in.Name {
		case "a":
			contains(t, err.Error(), "prompt and a command")
		case "b":
			contains(t, err.Error(), "command jobs need no model")
		case "c":
			contains(t, err.Error(), "command jobs need no busy policy")
		case "d":
			contains(t, err.Error(), "a prompt or a command")
		}
	}
}

func TestUpdateNeverConvertsAJobsKind(t *testing.T) {
	cwd := realCwd(t, "kind")
	h := newHarness(t, cwd)
	if _, err := h.create(sched.CreateInput{
		Name: "cmd", Command: "echo one", Cron: "0 3 * * *", Cwd: cwd,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.create(sched.CreateInput{
		Name: "model", Prompt: "p", Cron: "0 4 * * *", Model: "w", Cwd: cwd,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.update(sched.UpdateInput{ID: "j1", Prompt: "p"}); err == nil {
		t.Fatal("a prompt update on a command job must refuse")
	} else {
		contains(t, err.Error(), "runs a command")
	}
	if _, err := h.update(sched.UpdateInput{ID: "j1", Model: "w"}); err == nil {
		t.Fatal("a model update on a command job must refuse")
	} else {
		contains(t, err.Error(), "command jobs need no model")
	}
	if _, err := h.update(sched.UpdateInput{ID: "j2", Command: "true"}); err == nil {
		t.Fatal("a command update on a model job must refuse")
	} else {
		contains(t, err.Error(), "runs a model prompt")
	}
	reply, err := h.update(sched.UpdateInput{ID: "j1", Command: "echo two"})
	mustOK(t, err)
	contains(t, reply, "updated j1")
	if row := jobsRow(t, h, "j1"); row["command"] != "echo two" {
		t.Fatalf("the command text must overlay: %v", row)
	}
}

func TestSchemaThreeAddsTheCommandColumnToAV2Store(t *testing.T) {
	home := t.TempDir()
	ct := newFakeCrontab("SHELL=/bin/bash\n")
	path := filepath.Join(home, "global.sqlite")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	oldDDL := []string{
		`CREATE TABLE IF NOT EXISTS "events" ("seq" INTEGER NOT NULL, "args" TEXT NOT NULL, "op" TEXT NOT NULL, "session" TEXT, "ts" TEXT NOT NULL, PRIMARY KEY ("seq"))`,
		`CREATE TABLE IF NOT EXISTS "jobs" ("id" TEXT NOT NULL, "at" TEXT, "busy" TEXT NOT NULL, "created_seq" INTEGER NOT NULL, "cron" TEXT NOT NULL, "cwd" TEXT NOT NULL, "last_exit" INTEGER, "last_status" TEXT, "last_ts" TEXT, "model" TEXT NOT NULL, "name" TEXT NOT NULL, "prompt" TEXT NOT NULL, "state" TEXT NOT NULL, "updated_seq" INTEGER NOT NULL, PRIMARY KEY ("id"))`,
		`CREATE TABLE IF NOT EXISTS "meta" ("key" TEXT NOT NULL, "value" TEXT NOT NULL, PRIMARY KEY ("key"))`,
		`CREATE TABLE IF NOT EXISTS "runs" ("seq" INTEGER NOT NULL, "ended_at" TEXT NOT NULL, "exit" INTEGER, "duration_ms" INTEGER, "job_id" TEXT NOT NULL, "log_path" TEXT, "reason" TEXT, "started_at" TEXT NOT NULL, "status" TEXT NOT NULL, PRIMARY KEY ("seq"))`,
	}
	for _, s := range oldDDL {
		if _, err := raw.Exec(s); err != nil {
			t.Fatal(err)
		}
	}
	createArgs := `{"id":"j1","name":"legacy","prompt":"p","cron":"0 3 * * *","cwd":"/ws","model":"w","busy":"skip"}`
	if _, err := raw.Exec(`INSERT INTO events (seq, ts, op, args, session) VALUES (1, '2026-08-15T12:00:00.000Z', 'create', ?, NULL)`, createArgs); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO jobs (id, name, prompt, cron, at, cwd, model, busy, state, last_status, last_ts, last_exit, created_seq, updated_seq) VALUES ('j1', 'legacy', 'p', '0 3 * * *', NULL, '/ws', 'w', 'skip', 'active', NULL, NULL, NULL, 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO meta (key, value) VALUES ('schema_version', '2')`); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	db, _, _, err := store.Open(path, sched.Statements(), sched.SchemaVersion, sched.Migration(home, ct))
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{home: home, db: db, ct: ct, sessCwd: "/ws"}
	var version string
	if err := db.DB.QueryRow(`SELECT value FROM meta WHERE key='schema_version'`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != "4" {
		t.Fatalf("the version must move to head (4), got %s", version)
	}
	if row := jobsRow(t, h, "j1"); row == nil || row["prompt"] != "p" || row["command"] != nil {
		t.Fatalf("the legacy model job must survive with a NULL command: %v", row)
	}
	if _, err := h.create(sched.CreateInput{
		Name: "fresh", Command: "true", Cron: "0 5 * * *", Cwd: "/ws",
	}); err != nil {
		t.Fatalf("a command job must be creatable after the migration: %v", err)
	}
	if row := jobsRow(t, h, "j2"); row == nil || row["command"] != "true" {
		t.Fatalf("the migrated store must carry command jobs: %v", row)
	}
}
