package command_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/v2/command"
	"github.com/mrsirg97-rgb/rig/v2/core"
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
	return "swarm: added 1 agent (role worker · model qwen3.8-workers)", nil
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

func TestSwarmStartParsesBudget(t *testing.T) {
	f := &fakeSwarm{}
	env := swarmEnv(f)
	if _, err := runSwarm(t, env, "start 1 budget=5.50"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(f.started) != 1 || f.started[0].Budget != 5.50 || f.started[0].Count != 1 {
		t.Fatalf("start = %+v, want count 1 and budget 5.50", f.started)
	}
	if _, err := runSwarm(t, env, "start budget=-1"); err == nil {
		t.Fatal("a negative budget must refuse")
	}
	if _, err := runSwarm(t, env, "start budget=nope"); err == nil {
		t.Fatal("a non-numeric budget must refuse")
	}
}

func TestSwarmStartParsesRoleAndModel(t *testing.T) {
	f := &fakeSwarm{}
	env := swarmEnv(f)
	got, err := runSwarm(t, env, "start 2")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got != "swarm: added 1 agent (role worker · model qwen3.8-workers)" {
		t.Errorf("reply = %q", got)
	}
	if len(f.started) != 1 || f.started[0].Role != "worker" || f.started[0].Model != "" || f.started[0].Count != 2 {
		t.Errorf("start = %+v, want two workers", f.started)
	}
	got, err = runSwarm(t, env, "start 1 role=reviewer model=qwen3.8-review")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(f.started) != 2 || f.started[1].Role != "reviewer" || f.started[1].Model != "qwen3.8-review" {
		t.Errorf("second start = %+v", f.started[1])
	}
	got, err = runSwarm(t, env, "start 1 model=qwen3.8-workers role=reviewer")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if f.started[2].Role != "reviewer" || f.started[2].Model != "qwen3.8-workers" {
		t.Errorf("the role/model tokens must be order-independent: %+v", f.started[2])
	}
}

func TestSwarmCountRidesStart(t *testing.T) {
	env := swarmEnv(&fakeSwarm{})
	_, err := runSwarm(t, env, "3")
	if err == nil || !strings.Contains(err.Error(), "the count rides start") {
		t.Fatalf("a bare count must name start: %v", err)
	}
	for _, args := range []string{"start", "start abc"} {
		if _, err := runSwarm(t, env, args); err == nil {
			t.Errorf("/swarm %q must refuse", args)
		} else if !strings.Contains(err.Error(), "count") {
			t.Errorf("the refusal must name the count: %v", err)
		}
	}
	f := &fakeSwarm{}
	env = swarmEnv(f)
	for _, args := range []string{"start 0", "start 17"} {
		if _, err := runSwarm(t, env, args); err != nil {
			t.Errorf("/swarm %q must reach the seam (the controller owns the bounds): %v", args, err)
		}
	}
	if f.started[len(f.started)-1].Count != 17 {
		t.Errorf("the parsed count must ride through: %+v", f.started)
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
	want := "2 workers · 1 running\n" +
		"  w1 [~] worker qwen3.8-workers · task t3 · heartbeat 2s ago · done 1 failed 0\n" +
		"  w2 [x] reviewer qwen3.8-review · exited · done 2 failed 1"
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
	f := &fakeSwarm{stopReply: "swarm: stopped 2 agents"}
	got, err := runSwarm(t, swarmEnv(f), "stop")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got != "swarm: stopped 2 agents" {
		t.Errorf("reply = %q", got)
	}
	if f.stopped != 1 {
		t.Errorf("stop calls = %d, want 1", f.stopped)
	}
}

func TestSwarmUsageRefusals(t *testing.T) {
	env := swarmEnv(&fakeSwarm{})
	for _, args := range []string{"x", "3", "start 1 role=worker role=reviewer", "start 1 role=", "start 1 bogus", "stop extra"} {
		if _, err := runSwarm(t, env, args); err == nil {
			t.Errorf("/swarm %q must refuse", args)
		}
	}
}

func TestSwarmNoSeamListsEmptyAndStopRefuses(t *testing.T) {
	env := &command.Env{}
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
	if _, err := runSwarm(t, env, "start 1"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(f.started) != 1 {
		t.Fatalf("start calls = %d, want 1", len(f.started))
	}
}

func TestSwarmStartErrorSurfacesVerbatim(t *testing.T) {
	f := &fakeSwarm{startErr: context.DeadlineExceeded}
	if _, err := runSwarm(t, swarmEnv(f), "start 1"); err != context.DeadlineExceeded {
		t.Errorf("start error = %v, want verbatim", err)
	}
}
