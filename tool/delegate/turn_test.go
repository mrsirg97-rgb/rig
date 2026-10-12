package delegate_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	sched "github.com/mrsirg97-rgb/rig/v3/store/scheduler"
)

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("%s never happened", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestADelegatePutsNoDeadlineOnTheSpawnSoALongWorkerReturns(t *testing.T) {
	h := newHarness(t, "/ws/sess")
	release := make(chan struct{})
	spawn := &fakeSpawn{result: sched.SpawnResult{Exit: 0, Stdout: "the long answer"}, block: release}
	tool := h.newTool(t, fakeFetch(""), spawn.spawn)

	done := make(chan string, 1)
	go func() {
		out, err := tool.Exec(context.Background(), runArgs("the sweep that outlives the old ten minute default"))
		if err != nil {
			t.Errorf("a worker that is still working is never cut short: %v", err)
		}
		done <- out
	}()

	waitUntil(t, "the worker spawn", func() bool { return spawn.count() == 1 })
	deadlineSeen := spawn.hadDeadline()
	close(release)
	out := <-done
	if deadlineSeen {
		t.Fatal("the delegate tool puts no deadline on the spawn: the worker lives until it exits or the turn is interrupted")
	}
	if !strings.Contains(out, "the long answer") {
		t.Fatalf("the late worker's message must be fed back:\n%s", out)
	}
}

func TestTheTurnsContextDoesNotRuleTheWorkerButTheSessionsDoes(t *testing.T) {
	h := newHarness(t, "/ws/sess")
	seen := make(chan context.Context, 1)
	spawn := func(ctx context.Context, argv []string, cwd string, env []string, observe func([]byte)) (sched.SpawnResult, error) {
		seen <- ctx
		<-ctx.Done()
		return sched.SpawnResult{Exit: -1}, nil
	}
	sessionCtx, endSession := context.WithCancel(context.Background())
	room, fe := newFleetRoom(t)
	tool := h.newToolRoom(t, sessionCtx, false, room, fakeFetch(""), spawn)

	turnCtx, cancelTurn := context.WithCancel(context.Background())
	if _, err := tool.Exec(turnCtx, runArgs("the long sweep")); err != nil {
		t.Fatalf("hand off: %v", err)
	}
	workerCtx := <-seen
	cancelTurn()
	select {
	case <-workerCtx.Done():
		t.Fatal("the turn ending is not what kills a worker: it is the session")
	case <-time.After(100 * time.Millisecond):
	}

	endSession()
	select {
	case <-workerCtx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("a worker lives until it exits or the session ends")
	}
	waitForReturn(t, fe, 1)
}

func TestDelegateSchemaCarriesTheCommandWorkersClock(t *testing.T) {
	h := newHarness(t, "/ws/sess")
	spawn := &fakeSpawn{result: sched.SpawnResult{Exit: 0, Stdout: "done"}}
	tool := h.newTool(t, fakeFetch(""), spawn.spawn)
	if strings.Contains(string(tool.Schema()), "stallMs") {
		t.Fatalf("stallMs is off the schema: %s", tool.Schema())
	}
	var schema struct {
		Properties map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(tool.Schema(), &schema); err != nil {
		t.Fatal(err)
	}
	knob, ok := schema.Properties["timeoutMs"].(map[string]any)
	if !ok {
		t.Fatalf("timeoutMs rides the schema for command workers: %s", tool.Schema())
	}
	if knob["minimum"].(float64) != 1000 || knob["maximum"].(float64) != 86400000 {
		t.Fatalf("the command worker's clock bounds: %v", knob)
	}
	b, _ := json.Marshal(map[string]any{"task": "t", "timeoutMs": 1000})
	out, err := tool.Exec(context.Background(), b)
	if err == nil || !strings.Contains(err.Error(), "no clock") {
		t.Fatalf("a model worker has no clock, named: (%q, %v)", out, err)
	}
	if spawn.count() != 0 {
		t.Fatalf("a refused call spawns nothing, got %d", spawn.count())
	}
}
