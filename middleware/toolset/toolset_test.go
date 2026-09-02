package toolset

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/core"
)

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

func TestResolveSeesASwapOnTheNextCall(t *testing.T) {
	bash := &stubTool{name: "bash", result: "bash ran"}
	tbl := New(bash)
	exec := Resolve(tbl).Wrap(unknownExec)
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

	tbl.Set([]core.Tool{bash})
	if _, err := exec(ctx, core.ToolCall{Name: "forged"}); err == nil {
		t.Fatal("a dropped tool must not execute after the swap")
	}
	if forged.calls != 1 {
		t.Fatalf("the dropped tool ran %d times, want exactly the one call before the drop", forged.calls)
	}
}

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

func TestCarryStampsTheRequestPerCall(t *testing.T) {
	bash := &stubTool{name: "bash"}
	forged := &stubTool{name: "forged"}
	tbl := New(bash)
	inner := &recordingProvider{}
	prov := Carry(tbl, inner)
	ctx := context.Background()

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

	if got := names(inner.stamped[0]); len(got) != 1 || got[0] != "bash" {
		t.Fatalf("the earlier call's array changed after the Set: %v", got)
	}
}

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

func TestIsPluginTracksTheSwap(t *testing.T) {
	bash := &stubTool{name: "bash"}
	tbl := New(bash)
	if tbl.IsPlugin("forged") {
		t.Fatal("no plugin is live before SetPlugins")
	}
	if tbl.IsPlugin("bash") {
		t.Fatal("a native must never answer as a plugin")
	}

	tbl.SetPlugins("forged")
	if !tbl.IsPlugin("forged") {
		t.Fatal("an approved plugin must answer live")
	}
	if tbl.IsPlugin("bash") {
		t.Fatal("the door must not admit a native")
	}

	tbl.SetPlugins()
	if tbl.IsPlugin("forged") {
		t.Fatal("a dropped plugin must stop being admitted")
	}
}

func TestNativeSpecsExcludesPlugins(t *testing.T) {
	bash := &stubTool{name: "bash"}
	networth := &stubTool{name: "networth"}
	tbl := New(bash, networth)
	tbl.SetPlugins("networth")

	if got := names(tbl.NativeSpecs()); len(got) != 1 || got[0] != "bash" {
		t.Fatalf("NativeSpecs = %v, want the natives only (bash)", got)
	}
	if got := tbl.PluginNames(); len(got) != 1 || got[0] != "networth" {
		t.Fatalf("PluginNames = %v, want the live plugin", got)
	}

	flip := &stubTool{name: "flip_calc"}
	tbl.Set([]core.Tool{bash, networth, flip})
	tbl.SetPlugins("networth", "flip_calc")
	if got := tbl.PluginNames(); len(got) != 2 || got[0] != "flip_calc" || got[1] != "networth" {
		t.Fatalf("PluginNames after the swap = %v, want the sorted live set", got)
	}
}

func TestGetResolvesTheTable(t *testing.T) {
	bash := &stubTool{name: "bash"}
	tbl := New(bash)
	if got, ok := tbl.Tool("bash"); !ok || got != bash {
		t.Fatal("a live name must resolve to its tool")
	}
	if _, ok := tbl.Tool("nope"); ok {
		t.Fatal("an absent name must resolve nil")
	}
}

func TestPluginLookupRefusesNatives(t *testing.T) {
	bash := &stubTool{name: "bash"}
	forged := &stubTool{name: "forged"}
	tbl := New(bash, forged)
	tbl.SetPlugins("forged")
	if _, ok := tbl.Plugin("bash"); ok {
		t.Fatal("a native must not resolve through the plugin lookup")
	}
	if tool, ok := tbl.Plugin("forged"); !ok || tool != forged {
		t.Fatal("an approved plugin must resolve through the plugin lookup")
	}
	if _, ok := tbl.Tool("bash"); !ok {
		t.Fatal("the full lookup still serves natives")
	}
	tbl.SetPlugins()
	if _, ok := tbl.Plugin("forged"); ok {
		t.Fatal("a dropped plugin must stop resolving")
	}
}
