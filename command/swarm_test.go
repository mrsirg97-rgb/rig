package command_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/command"
	"github.com/mrsirg97-rgb/rig/core"
)

type fakeSwarm struct {
	started    []command.SwarmStart
	stopped    int
	rows       []command.SwarmWorker
	startReply string
	stopReply  string
	startErr   error
}

func (f *fakeSwarm) Start(ctx context.Context, in command.SwarmStart) (string, error) {
	if got, ok := core.SessionFrom(ctx); ok && got != nil {
		f.started = append(f.started, in)
	}
	if f.startErr != nil {
		return "", f.startErr
	}
	if f.startReply != "" {
		return f.startReply, nil
	}
	return "swarm: started", nil
}

func (f *fakeSwarm) List() []command.SwarmWorker { return f.rows }

func (f *fakeSwarm) Stop() (string, error) {
	f.stopped++
	if f.stopReply == "" {
		f.stopReply = "swarm: stopped"
	}
	return f.stopReply, nil
}

func swarmEnv(swarm command.Swarm) *command.Env {
	return &command.Env{
		Workers: command.Workers{Model: "qwen3.8-workers", Slots: 1, File: "/home/ng/.rig/workers.json", Configured: true},
		Swarm:   swarm,
		Session: func() *core.Session { return core.NewSession() },
	}
}

func runSwarm(t *testing.T, env *command.Env, args string) (string, error) {
	t.Helper()
	for _, c := range command.All() {
		if c.Name() == "swarm" {
			return c.Run(context.Background(), args, env)
		}
	}
	t.Fatal("swarm command not in the standard set")
	return "", nil
}

func TestSwarmStartParsesCountRoleAndModel(t *testing.T) {
	f := &fakeSwarm{}
	env := swarmEnv(f)
	got, err := runSwarm(t, env, "3")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got != "swarm: started" {
		t.Errorf("reply = %q", got)
	}
	if len(f.started) != 1 || f.started[0].Count != 3 || f.started[0].Role != "worker" || f.started[0].Model != "" {
		t.Errorf("start = %+v, want 3 workers", f.started)
	}
	got, err = runSwarm(t, env, "2 role=reviewer model=qwen3.8-review")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(f.started) != 2 || f.started[1].Count != 2 || f.started[1].Role != "reviewer" || f.started[1].Model != "qwen3.8-review" {
		t.Errorf("second start = %+v", f.started[1])
	}
	got, err = runSwarm(t, env, "1 model=qwen3.8-workers role=reviewer")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if f.started[2].Role != "reviewer" || f.started[2].Model != "qwen3.8-workers" {
		t.Errorf("the role/model tokens must be order-independent: %+v", f.started[2])
	}
}

func TestSwarmBareListsTheWorkers(t *testing.T) {
	f := &fakeSwarm{rows: []command.SwarmWorker{
		{ID: 1, Role: "worker", Model: "qwen3.8-workers", Task: "t3", Heartbeat: time.Now().Add(-2 * time.Second), Done: 1, Failed: 0, State: "running"},
		{ID: 2, Role: "reviewer", Model: "qwen3.8-review", Done: 2, Failed: 1, State: "exited"},
	}}
	got, err := runSwarm(t, swarmEnv(f), "")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want := "w1 worker qwen3.8-workers · task t3 · heartbeat 2s ago · done 1 failed 0\nw2 reviewer qwen3.8-review · exited · done 2 failed 1"
	if got != want {
		t.Errorf("list =\n%q\nwant\n%q", got, want)
	}
}

func TestSwarmListIdleAndNoHeartbeat(t *testing.T) {
	f := &fakeSwarm{rows: []command.SwarmWorker{
		{ID: 1, Role: "worker", Model: "m", State: "running"},
	}}
	got, err := runSwarm(t, swarmEnv(f), "")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(got, "task none · heartbeat —") {
		t.Errorf("an idle worker must say task none and no heartbeat:\n%s", got)
	}
}

func TestSwarmStopEndsTheWorkers(t *testing.T) {
	f := &fakeSwarm{stopReply: "swarm: stopped 2 workers"}
	got, err := runSwarm(t, swarmEnv(f), "stop")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got != "swarm: stopped 2 workers" {
		t.Errorf("reply = %q", got)
	}
	if f.stopped != 1 {
		t.Errorf("stop calls = %d, want 1", f.stopped)
	}
}

func TestSwarmUsageRefusals(t *testing.T) {
	env := swarmEnv(&fakeSwarm{})
	for _, args := range []string{"x", "1 2", "2 role=worker role=reviewer", "2 role=", "2 bogus", "stop extra"} {
		if _, err := runSwarm(t, env, args); err == nil {
			t.Errorf("/swarm %q must refuse", args)
		}
	}
}

func TestSwarmNoFleetRefusesByName(t *testing.T) {
	env := &command.Env{Workers: command.Workers{File: "/home/ng/.rig/workers.json"}, Swarm: &fakeSwarm{}}
	_, err := runSwarm(t, env, "2")
	if err == nil {
		t.Fatal("a start without a fleet must refuse")
	}
	if !strings.Contains(err.Error(), "no workers configured") || !strings.Contains(err.Error(), "workers.json") {
		t.Errorf("no-fleet voice: %v", err)
	}
}

func TestSwarmNoSeamListsEmptyAndStopRefuses(t *testing.T) {
	env := &command.Env{Workers: command.Workers{Configured: true}}
	got, err := runSwarm(t, env, "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if got != "swarm: no workers" {
		t.Errorf("empty list = %q", got)
	}
	if _, err := runSwarm(t, env, "stop"); err == nil || !strings.Contains(err.Error(), "no swarm running") {
		t.Errorf("stop voice: %v", err)
	}
}

func TestSwarmThreadsTheLiveSession(t *testing.T) {
	f := &fakeSwarm{}
	env := swarmEnv(f)
	if _, err := runSwarm(t, env, "1"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(f.started) != 1 {
		t.Fatalf("start calls = %d, want 1", len(f.started))
	}
}

func TestSwarmStartErrorSurfacesVerbatim(t *testing.T) {
	f := &fakeSwarm{startErr: context.DeadlineExceeded}
	if _, err := runSwarm(t, swarmEnv(f), "1"); err != context.DeadlineExceeded {
		t.Errorf("start error = %v, want verbatim", err)
	}
}

func TestSwarmSubHints(t *testing.T) {
	for _, c := range command.All() {
		if c.Name() != "swarm" {
			continue
		}
		s, ok := c.(command.Subber)
		if !ok {
			t.Fatal("swarm must be a Subber")
		}
		subs := s.Sub()
		if len(subs) != 2 || subs[0].Name != "stop" || subs[1].Name != "<n>" {
			t.Errorf("sub hints = %+v", subs)
		}
		return
	}
	t.Fatal("no swarm command")
}
