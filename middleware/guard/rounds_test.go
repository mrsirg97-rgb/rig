package guard_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/middleware/guard"
)

type countingExec struct {
	mu    sync.Mutex
	total int
}

func (e *countingExec) Exec(ctx context.Context, call core.ToolCall) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.total++
	return "ran", nil
}

func TestRoundCapRefusesTheNextCallWithTheVoice(t *testing.T) {
	e := &countingExec{}
	var exec core.ToolExec = func(ctx context.Context, call core.ToolCall) (string, error) {
		return e.Exec(ctx, call)
	}
	exec = guard.Rounds(3).Wrap(exec)
	call := core.ToolCall{ID: "c", Name: "read", Args: json.RawMessage(`{}`)}

	for i := 0; i < 3; i++ {
		if _, er := exec(context.Background(), call); er != nil {
			t.Fatalf("calls within the cap must execute, call %d: %v", i+1, er)
		}
	}
	content, er := exec(context.Background(), call)
	if er == nil {
		t.Fatal("the n+1th call must be refused")
	}
	err := er.Error()
	if e.total != 3 {
		t.Fatalf("executions %d, want 3 (the refused call never executes)", e.total)
	}
	for _, want := range []string{"round cap", "3", "stop", "report", "operator"} {
		if !strings.Contains(content, want) || !strings.Contains(err, want) {
			t.Fatalf("the refusal must name the cap and teach in both content and error, got:\ncontent %q\nerror %q", content, err)
		}
	}

	if _, er := exec(context.Background(), call); er == nil {
		t.Fatal("every call past the cap must be refused, without executing")
	}
	if e.total != 3 {
		t.Fatalf("executions %d, want 3 (every past-cap call is refused)", e.total)
	}
}

func TestAlternatingCallsHitTheRoundCap(t *testing.T) {
	e := &countingExec{}
	var exec core.ToolExec = func(ctx context.Context, call core.ToolCall) (string, error) {
		return e.Exec(ctx, call)
	}
	exec = guard.Rounds(4).Wrap(exec)
	a := core.ToolCall{ID: "c1", Name: "read", Args: json.RawMessage(`{"path":"a"}`)}
	b := core.ToolCall{ID: "c2", Name: "grep", Args: json.RawMessage(`{"pattern":"b"}`)}

	for i := 0; i < 2; i++ {
		if _, er := exec(context.Background(), a); er != nil {
			t.Fatalf("alternating calls within the cap must execute: %v", er)
		}
		if _, er := exec(context.Background(), b); er != nil {
			t.Fatalf("alternating calls within the cap must execute: %v", er)
		}
	}

	content, er := exec(context.Background(), a)
	if er == nil || !strings.Contains(content, "round cap") {
		t.Fatalf("the fifth alternating call must hit the cap, got %q %v", content, er)
	}
	if e.total != 4 {
		t.Fatalf("executions %d, want 4 (the alternation the retry bound misses is capped)", e.total)
	}
}

func TestRoundCapClearsAtTheTurnBoundary(t *testing.T) {
	mw := guard.Rounds(2)
	obs, ok := mw.(core.TurnObserver)
	if !ok {
		t.Fatal("the round cap must implement TurnObserver (the loop fans it out)")
	}
	e := &countingExec{}
	exec := mw.Wrap(func(ctx context.Context, call core.ToolCall) (string, error) {
		return e.Exec(ctx, call)
	})
	call := core.ToolCall{ID: "c", Name: "read", Args: json.RawMessage(`{}`)}

	if _, er := exec(context.Background(), call); er != nil {
		t.Fatalf("call 1 must execute: %v", er)
	}
	if _, er := exec(context.Background(), call); er != nil {
		t.Fatalf("call 2 must execute: %v", er)
	}
	if _, er := exec(context.Background(), call); er == nil {
		t.Fatal("call 3 must refuse at limit 2")
	}
	obs.TurnStart(context.Background(), core.NewSession())
	if _, er := exec(context.Background(), call); er != nil {
		t.Fatalf("the new turn must start with a fresh budget: %v", er)
	}
	if e.total != 3 {
		t.Fatalf("executions %d, want 3 (one fresh call per turn; the refusals never execute)", e.total)
	}
}

func TestRoundCapCountsAConcurrentRunRaceFree(t *testing.T) {
	e := &countingExec{}
	var inner core.ToolExec = func(ctx context.Context, call core.ToolCall) (string, error) {
		return e.Exec(ctx, call)
	}
	exec := guard.Rounds(50).Wrap(inner)
	call := core.ToolCall{ID: "c", Name: "read", Args: json.RawMessage(`{"path":"a"}`)}
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = exec(context.Background(), call)
		}()
	}
	wg.Wait()
	if e.total != 50 {
		t.Fatalf("a concurrent run of 50 identical reads counts 50 (the cap is on calls, not turns), got %d", e.total)
	}

	if _, er := exec(context.Background(), call); er == nil {
		t.Fatal("the 51st call must refuse after a concurrent run of 50")
	}
}
