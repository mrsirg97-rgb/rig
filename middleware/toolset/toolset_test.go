// The toolset's named cases (SPEC_PLUGINS 8, testing): the live tool
// table's two ends — the exec's Resolve and the request's Carry — with
// a swap in between. Pure core: no kernel, no python, no root. The
// table is the root's per-turn fact (the models-switch's semantics,
// SPEC_COMMANDS 6): a swap takes effect on the next call, never the
// in-flight one.
package toolset

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/core"
)

// stubTool is a named core.Tool that records its calls.
type stubTool struct {
	name   string
	result string
	err    error
	args   json.RawMessage
	calls  int
}

func (s *stubTool) Name() string            { return s.name }
func (s *stubTool) Description() string     { return "stub " + s.name }
func (s *stubTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (s *stubTool) Exec(ctx context.Context, args json.RawMessage) (string, error) {
	s.calls++
	s.args = args
	return s.result, s.err
}

var unknownExec = func(ctx context.Context, call core.ToolCall) (string, error) {
	msg := "unknown tool: " + call.Name
	return msg, errors.New(msg)
}

// TestResolveServesTheTableAndFallsThrough (SPEC_PLUGINS 8, named): a
// name the table carries runs the table's tool (the args and the
// result, verbatim); a name the table does not carry falls through to
// the inner exec (the loop's own, the unknown-tool voice).
func TestResolveServesTheTableAndFallsThrough(t *testing.T) {
	bash := &stubTool{name: "bash", result: "bash ran"}
	tbl := New(bash)
	exec := Resolve(tbl).Wrap(unknownExec)
	ctx := context.Background()

	args := json.RawMessage(`{"command":"echo"}`)
	out, err := exec(ctx, core.ToolCall{Name: "bash", Args: args})
	if err != nil || out != "bash ran" {
		t.Fatalf("a table name must run the table's tool: (%q, %v)", out, err)
	}
	if bash.calls != 1 || string(bash.args) != string(args) {
		t.Fatalf("the tool must receive the call's args verbatim: calls=%d args=%s", bash.calls, bash.args)
	}

	out, err = exec(ctx, core.ToolCall{Name: "forged"})
	if err == nil || !strings.Contains(err.Error(), "unknown tool: forged") {
		t.Fatalf("an absent name must fall through to the inner exec: (%q, %v)", out, err)
	}
	if bash.calls != 1 {
		t.Fatalf("the fall-through must not reach the table's tool (calls=%d)", bash.calls)
	}
}

// TestResolveSeesASwapOnTheNextCall (SPEC_PLUGINS 8, named): a tool
// absent before a Set executes after it (the next turn's exec), and a
// tool the swap dropped does not (the list rebuilds from the table —
// removal free). The exec closure outlives the swap: it is built once,
// as the loop's chain is.
func TestResolveSeesASwapOnTheNextCall(t *testing.T) {
	bash := &stubTool{name: "bash", result: "bash ran"}
	tbl := New(bash)
	exec := Resolve(tbl).Wrap(unknownExec) // built before the swaps
	ctx := context.Background()

	if _, err := exec(ctx, core.ToolCall{Name: "forged"}); err == nil {
		t.Fatal("forged must be unknown before the swap")
	}

	forged := &stubTool{name: "forged", result: "forged ran"}
	tbl.Set(append(tbl.List(), forged))
	out, err := exec(ctx, core.ToolCall{Name: "forged"})
	if err != nil || out != "forged ran" {
		t.Fatalf("the swapped-in tool must execute on the next call: (%q, %v)", out, err)
	}
	if out, err = exec(ctx, core.ToolCall{Name: "bash"}); err != nil || out != "bash ran" {
		t.Fatalf("the surviving tool must keep executing: (%q, %v)", out, err)
	}

	tbl.Set([]core.Tool{bash}) // the swap drops forged
	if _, err := exec(ctx, core.ToolCall{Name: "forged"}); err == nil {
		t.Fatal("a dropped tool must not execute after the swap")
	}
	if forged.calls != 1 {
		t.Fatalf("the dropped tool ran %d times, want exactly the one call before the drop", forged.calls)
	}
}

// recordingProvider records each request's tools array as stamped.
type recordingProvider struct {
	stamped [][]core.ToolSpec
}

func (p *recordingProvider) Stream(ctx context.Context, req core.Request) (<-chan core.Event, error) {
	p.stamped = append(p.stamped, append([]core.ToolSpec(nil), req.Tools...))
	ch := make(chan core.Event, 1)
	ch <- core.Done{}
	close(ch)
	return ch, nil
}

func names(specs []core.ToolSpec) []string {
	out := make([]string, 0, len(specs))
	for _, s := range specs {
		out = append(out, s.Name)
	}
	return out
}

// TestCarryStampsTheRequestPerCall (SPEC_PLUGINS 8, named): the
// request's tools array is the table's specs at call time — the
// loop's own (startup) list is a bootstrap, a Set changes the next
// call's array, and a call made before the Set keeps the list it was
// stamped with (next turn, never mid-turn).
func TestCarryStampsTheRequestPerCall(t *testing.T) {
	bash := &stubTool{name: "bash"}
	forged := &stubTool{name: "forged"}
	tbl := New(bash)
	inner := &recordingProvider{}
	prov := Carry(tbl, inner)
	ctx := context.Background()

	// the loop's request carries its own (stale) list; the stamp wins.
	stale := []core.ToolSpec{{Name: "stale"}}
	if ch, err := prov.Stream(ctx, core.Request{Tools: stale}); err != nil {
		t.Fatal(err)
	} else {
		for range ch {
		}
	}
	if got := names(inner.stamped[0]); len(got) != 1 || got[0] != "bash" {
		t.Fatalf("the first call's array = %v, want the table's (bash)", got)
	}

	tbl.Set(append(tbl.List(), forged))
	if ch, err := prov.Stream(ctx, core.Request{Tools: stale}); err != nil {
		t.Fatal(err)
	} else {
		for range ch {
		}
	}
	got := names(inner.stamped[1])
	if len(got) != 2 || got[0] != "bash" || got[1] != "forged" {
		t.Fatalf("the next call's array = %v, want the swapped list (bash, forged)", got)
	}
	if spec := inner.stamped[1][1]; spec.Description != "stub forged" {
		t.Fatalf("the spec carries the tool's description, got %q", spec.Description)
	}

	// the in-flight (first) call keeps the list it was stamped with.
	if got := names(inner.stamped[0]); len(got) != 1 || got[0] != "bash" {
		t.Fatalf("the earlier call's array changed after the Set: %v", got)
	}
}

// TestSetIsAtomic: a reader never sees a partial list — one snapshot or
// the next, never a mix of the two (the swap is one write).
func TestSetIsAtomic(t *testing.T) {
	a := &stubTool{name: "a"}
	b := &stubTool{name: "b"}
	tbl := New(a)
	for i := 0; i < 1000; i++ {
		tbl.Set([]core.Tool{a, b})
		tbl.Set([]core.Tool{a})
		list := tbl.List()
		if len(list) == 0 || len(list) > 2 {
			t.Fatalf("a partial list crossed: %d entries", len(list))
		}
	}
}

// TestIsPluginTracksTheSwap (SPEC_PLUGINS 7, the presence reversal):
// the live plugin table answers membership — a SetPlugins-approved
// plugin is a plugin, a native never is (the sets are disjoint), and a
// reload that drops the name stops admitting it (deleted-after-reload).
func TestIsPluginTracksTheSwap(t *testing.T) {
	bash := &stubTool{name: "bash"}
	tbl := New(bash)
	if tbl.IsPlugin("forged") {
		t.Fatal("no plugin is live before SetPlugins")
	}
	if tbl.IsPlugin("bash") {
		t.Fatal("a native must never answer as a plugin")
	}

	tbl.SetPlugins("forged") // the approved plugin goes live
	if !tbl.IsPlugin("forged") {
		t.Fatal("an approved plugin must answer live")
	}
	if tbl.IsPlugin("bash") {
		t.Fatal("the door must not admit a native")
	}

	tbl.SetPlugins() // the reload dropped it (removal free)
	if tbl.IsPlugin("forged") {
		t.Fatal("a dropped plugin must stop being admitted")
	}
}
