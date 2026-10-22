package scheduler_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/evt/broadcast"
	"github.com/mrsirg97-rgb/rig/v3/room"
	sched "github.com/mrsirg97-rgb/rig/v3/store/scheduler"
)

func TestTheRunnerTellsTheRoomStartAndEnd(t *testing.T) {
	cwd := realCwd(t, "telljob")
	h := newHarness(t, cwd)
	if _, err := h.create(sched.CreateInput{
		Name: "tick", Command: "true", Cron: "20 10 * * *", Cwd: cwd,
	}); err != nil {
		t.Fatal(err)
	}
	var told []room.JobStatus
	tell := func(st room.JobStatus) { told = append(told, st) }
	if err := sched.RunJob("j1", sched.RunOpts{
		Home: h.home, Crontab: h.ct, RigHome: h.rigHome,
		Fetch:     func(string) (json.RawMessage, error) { return nil, jsonError("no fetch") },
		Spawn:     (&fakeSpawn{result: sched.SpawnResult{Exit: 0}}).spawn,
		WorkerCmd: []string{"/x/rig"}, Now: func() time.Time { return runnerNow },
		Sandbox: "off", Tell: tell,
	}); err != nil {
		t.Fatal(err)
	}
	if len(told) != 2 {
		t.Fatalf("a command job tells start and end, got %d: %+v", len(told), told)
	}
	if told[0].State != room.JobRunning || told[0].Workers != 0 || told[0].Call != "" {
		t.Fatalf("the start names a command job's zero workers: %+v", told[0])
	}
	if !told[0].Started.Equal(runnerNow) {
		t.Fatalf("the start carries the fire's start: %+v", told[0])
	}
	if told[1].State != room.JobDone || told[1].Ended.Before(told[0].Started) || told[1].Reason != "" {
		t.Fatalf("the end is a clean done: %+v", told[1])
	}
	for _, st := range told {
		if st.Job != "j1" {
			t.Fatalf("the act is keyed to the job: %+v", st)
		}
	}
}

func TestTheRunnerTellsAFailedEnd(t *testing.T) {
	cwd := realCwd(t, "tellfail")
	h := newHarness(t, cwd)
	if _, err := h.create(sched.CreateInput{
		Name: "tick", Command: "true", Cron: "20 10 * * *", Cwd: cwd,
	}); err != nil {
		t.Fatal(err)
	}
	var told []room.JobStatus
	tell := func(st room.JobStatus) { told = append(told, st) }
	if err := sched.RunJob("j1", sched.RunOpts{
		Home: h.home, Crontab: h.ct, RigHome: h.rigHome,
		Fetch:     func(string) (json.RawMessage, error) { return nil, jsonError("no fetch") },
		Spawn:     (&fakeSpawn{err: fmt.Errorf("the worker refused")}).spawn,
		WorkerCmd: []string{"/x/rig"}, Now: func() time.Time { return runnerNow },
		Sandbox: "off", Tell: tell,
	}); err == nil {
		t.Fatal("a failing spawn must fail the fire")
	}
	if len(told) != 2 {
		t.Fatalf("a failed spawn tells start and failed, got %d: %+v", len(told), told)
	}
	if told[1].State != room.JobFailed || told[1].Reason != "the worker refused" {
		t.Fatalf("the end names the failure: %+v", told[1])
	}
}

func TestTheRunnerTellsTheWorkersCall(t *testing.T) {
	cwd := realCwd(t, "tellcall")
	h := newHarness(t, cwd)
	if _, err := h.create(sched.CreateInput{
		Name: "tick", Prompt: "do the thing", Model: "qwen3.8-workers", Cron: "20 10 * * *", Cwd: cwd,
	}); err != nil {
		t.Fatal(err)
	}
	var told []room.JobStatus
	tell := func(st room.JobStatus) { told = append(told, st) }
	run := func(ctx context.Context, argv []string, cwd string, env []string, observe func([]byte)) (sched.SpawnResult, error) {
		_, w, ok := sched.FleetFrom(ctx)
		if !ok {
			return sched.SpawnResult{}, fmt.Errorf("the worker has no fleet end")
		}
		frame, err := json.Marshal(map[string]any{
			"origin": 0, "ok": true, "kind": "tool_start",
			"payload": map[string]any{"call": map[string]any{"name": "bash", "args": "ls"}},
		})
		if err != nil {
			return sched.SpawnResult{}, err
		}
		if _, err := w.Write(append(frame, '\n')); err != nil {
			return sched.SpawnResult{}, err
		}
		time.Sleep(50 * time.Millisecond)
		return sched.SpawnResult{Exit: 0}, nil
	}
	if err := sched.RunJob("j1", sched.RunOpts{
		Home: h.home, Crontab: h.ct, RigHome: h.rigHome,
		Fetch: func(string) (json.RawMessage, error) {
			return json.RawMessage(`{"data":[{"id":"qwen3.8-workers"}]}`), nil
		},
		Spawn:     run,
		WorkerCmd: []string{"/x/rig"}, Now: func() time.Time { return runnerNow },
		Sandbox: "off", Tell: tell,
	}); err != nil {
		t.Fatal(err)
	}
	if len(told) != 3 {
		t.Fatalf("a worker's fire tells start, call and end, got %d: %+v", len(told), told)
	}
	if told[0].Workers != 1 {
		t.Fatalf("a model job's start names its worker: %+v", told[0])
	}
	if told[1].Call != "bash ls" {
		t.Fatalf("the call act carries the worker's call: %+v", told[1])
	}
	if told[1].CallAt.IsZero() || told[1].CallAt.Before(told[0].Started) {
		t.Fatalf("the call act is stamped when the frame arrived: %+v", told[1])
	}
	if told[2].State != room.JobDone {
		t.Fatalf("the fire ends done: %+v", told[2])
	}
}

func TestTheRunnerPostsNothingWhenTheTellIsUnwired(t *testing.T) {
	cwd := realCwd(t, "tellnil")
	h := newHarness(t, cwd)
	if _, err := h.create(sched.CreateInput{
		Name: "tick", Command: "true", Cron: "20 10 * * *", Cwd: cwd,
	}); err != nil {
		t.Fatal(err)
	}
	if err := sched.RunJob("j1", sched.RunOpts{
		Home: h.home, Crontab: h.ct, RigHome: h.rigHome,
		Fetch:     func(string) (json.RawMessage, error) { return nil, jsonError("no fetch") },
		Spawn:     (&fakeSpawn{result: sched.SpawnResult{Exit: 0}}).spawn,
		WorkerCmd: []string{"/x/rig"}, Now: func() time.Time { return runnerNow },
		Sandbox: "off",
	}); err != nil {
		t.Fatal(err)
	}
}

func TestTheJobStatusActCrossesTheRegistry(t *testing.T) {
	started := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	enc := room.Codec()
	frame, err := enc.Encode(broadcast.NewMessage(7, true, room.JobStatus{
		Job: "j8", Prompt: "do the thing", State: room.JobRunning,
		Started: started, Workers: 1,
		Summary: "decision train laya · 569 gold · promoted",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(frame), "room/job-status") {
		t.Fatalf("the act rides its declared kind: %s", frame)
	}
	msg, err := enc.Decode(frame)
	if err != nil {
		t.Fatal(err)
	}
	st, ok := msg.Signal().(room.JobStatus)
	if !ok {
		t.Fatalf("the decode returns the act: %T", msg.Signal())
	}
	if st.Job != "j8" || st.State != room.JobRunning || st.Workers != 1 || !st.Started.Equal(started) {
		t.Fatalf("the act survives the frame: %+v", st)
	}
	if st.Summary != "decision train laya · 569 gold · promoted" {
		t.Fatalf("the summary survives the frame: %+v", st)
	}

	old, err := enc.Encode(broadcast.NewMessage(7, true, room.JobStatus{
		Job: "j8", Prompt: "do the thing", State: room.JobRunning,
		Started: started, Workers: 1,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(old), "Summary") {
		t.Fatalf("an empty summary stays off the wire (a 3.3.1 peer sends exactly this frame): %s", old)
	}
	oldMsg, err := enc.Decode(old)
	if err != nil {
		t.Fatal(err)
	}
	oldSt, ok := oldMsg.Signal().(room.JobStatus)
	if !ok {
		t.Fatalf("the old payload decodes to the act: %T", oldMsg.Signal())
	}
	if oldSt.Job != "j8" || oldSt.Workers != 1 || oldSt.Summary != "" {
		t.Fatalf("a 3.3.1 peer's frame decodes with the summary empty: %+v", oldSt)
	}
	if msg.Origin() != 7 {
		t.Fatalf("the act carries its origin: %d", msg.Origin())
	}
}

func TestTheEndStatusCarriesTheRunSummary(t *testing.T) {
	cwd := realCwd(t, "tellsummary")
	h := newHarness(t, cwd)
	if _, err := h.create(sched.CreateInput{
		Name: "tick", Command: "true", Cron: "20 10 * * *", Cwd: cwd,
	}); err != nil {
		t.Fatal(err)
	}
	var told []room.JobStatus
	tell := func(st room.JobStatus) { told = append(told, st) }
	if err := sched.RunJob("j1", sched.RunOpts{
		Home: h.home, Crontab: h.ct, RigHome: h.rigHome,
		Fetch: func(string) (json.RawMessage, error) { return nil, jsonError("no fetch") },
		Spawn: (&fakeSpawn{result: sched.SpawnResult{Exit: 0,
			Stdout: "loading rows\nepoch 1 done\nepoch 2 done\n"}}).spawn,
		WorkerCmd: []string{"/x/rig"}, Now: func() time.Time { return runnerNow },
		Sandbox: "off", Tell: tell,
	}); err != nil {
		t.Fatal(err)
	}
	if told[1].State != room.JobDone || told[1].Summary != "epoch 2 done" {
		t.Fatalf("the done status carries the run's last stdout line: %+v", told[1])
	}

	told = nil
	if err := sched.RunJob("j1", sched.RunOpts{
		Home: h.home, Crontab: h.ct, RigHome: h.rigHome,
		Fetch: func(string) (json.RawMessage, error) { return nil, jsonError("no fetch") },
		Spawn: (&fakeSpawn{result: sched.SpawnResult{Exit: 1,
			Stdout: "step ok\n",
			Stderr: "warn x\n[runner: killed after stall]\nthe trainer died\n"}}).spawn,
		WorkerCmd: []string{"/x/rig"}, Now: func() time.Time { return runnerNow },
		Sandbox: "off", Tell: tell,
	}); err != nil {
		t.Fatal(err)
	}
	if told[1].State != room.JobFailed || told[1].Summary != "the trainer died" {
		t.Fatalf("a failed end carries stderr's last line over the runner's notes: %+v", told[1])
	}

	told = nil
	if err := sched.RunJob("j1", sched.RunOpts{
		Home: h.home, Crontab: h.ct, RigHome: h.rigHome,
		Fetch: func(string) (json.RawMessage, error) { return nil, jsonError("no fetch") },
		Spawn: (&fakeSpawn{result: sched.SpawnResult{Exit: 0,
			Stdout: "progress\n[spawn: output truncated, kept the first and last 131072 bytes of 262144]\n"}}).spawn,
		WorkerCmd: []string{"/x/rig"}, Now: func() time.Time { return runnerNow },
		Sandbox: "off", Tell: tell,
	}); err != nil {
		t.Fatal(err)
	}
	if told[1].Summary != "progress" {
		t.Fatalf("the runner's truncation note is not the run's result: %+v", told[1])
	}
}
