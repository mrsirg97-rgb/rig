package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/core"
)

type stubLive struct {
	names []string
	tool  core.Tool
}

func (s *stubLive) PluginNames() []string { return s.names }
func (s *stubLive) Plugin(name string) (core.Tool, bool) {
	if s.tool != nil && s.tool.Name() == name {
		return s.tool, true
	}
	return nil, false
}

type stubTool struct{ name string }

func (s *stubTool) Name() string            { return s.name }
func (s *stubTool) Description() string     { return "stub " + s.name }
func (s *stubTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (s *stubTool) Exec(ctx context.Context, args json.RawMessage) (string, error) {
	return "ran " + s.name + ": " + string(args), nil
}

func TestDoorSurfacesAreTheNativeContract(t *testing.T) {
	live := &stubLive{names: []string{"networth"}, tool: &stubTool{name: "networth"}}
	door := NewDoor(live, nil)
	if door.Name() != "plugin" {
		t.Fatalf("door name = %q, want plugin (a native tool)", door.Name())
	}
	var params struct {
		Properties struct {
			Name struct {
				Enum []string `json:"enum"`
			} `json:"name"`
			Action struct {
				Enum []string `json:"enum"`
			} `json:"action"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(door.Schema(), &params); err != nil {
		t.Fatalf("the door's schema is not JSON: %v", err)
	}
	if len(params.Properties.Name.Enum) != 1 || params.Properties.Name.Enum[0] != "networth" {
		t.Fatalf("the door's enum = %v, want the live names", params.Properties.Name.Enum)
	}
	if len(params.Properties.Action.Enum) != 2 || params.Properties.Action.Enum[0] != "run" || params.Properties.Action.Enum[1] != "schema" {
		t.Fatalf("the door's action enum = %v, want run and schema", params.Properties.Action.Enum)
	}

	live.names = []string{"networth", "flip_calc"}
	if err := json.Unmarshal(door.Schema(), &params); err != nil {
		t.Fatal(err)
	}
	if len(params.Properties.Name.Enum) != 2 {
		t.Fatalf("the door's enum must follow the live table, got %v", params.Properties.Name.Enum)
	}
}

func TestDoorExecResolvesAndCalls(t *testing.T) {
	live := &stubLive{names: []string{"networth"}, tool: &stubTool{name: "networth"}}
	door := NewDoor(live, nil)
	ctx := context.Background()

	out, err := door.Exec(ctx, json.RawMessage(`{"action":"run","name":"networth","args":{"a":1}}`))
	if err != nil || out != "ran networth: {\"a\":1}" {
		t.Fatalf("the run = (%q, %v), want the plugin's run verbatim", out, err)
	}
	out, err = door.Exec(ctx, json.RawMessage(`{"action":"schema","name":"networth"}`))
	if err != nil || out != "stub networth\nschema: {\"type\":\"object\"}" {
		t.Fatalf("the schema = (%q, %v), want the plugin's contract", out, err)
	}
	out, err = door.Exec(ctx, json.RawMessage(`{"action":"run","name":"nope"}`))
	if err == nil || !strings.Contains(err.Error(), "unknown plugin \"nope\"") || !strings.Contains(err.Error(), "networth") {
		t.Fatalf("an unknown name must be a loud error naming the live plugins: (%q, %v)", out, err)
	}
	if _, err = door.Exec(ctx, json.RawMessage(`{"action":"schema","name":"nope"}`)); err == nil {
		t.Fatal("the schema of an unknown name must error")
	}
}

func TestDoorUnknownActionRefuses(t *testing.T) {
	live := &stubLive{names: []string{"networth"}, tool: &stubTool{name: "networth"}}
	door := NewDoor(live, nil)
	_, err := door.Exec(context.Background(), json.RawMessage(`{"action":"sideways","name":"networth"}`))
	if err == nil || !strings.Contains(err.Error(), `unknown action "sideways"`) {
		t.Fatalf("an unknown action must refuse by name, got %v", err)
	}
	_, err = door.Exec(context.Background(), json.RawMessage(`{"name":"networth"}`))
	if err == nil || !strings.Contains(err.Error(), "no action") {
		t.Fatalf("a missing action must refuse, got %v", err)
	}
}

type deferredLive struct {
	name    string
	tool    core.Tool
	ready   bool
	redoErr error
	calls   int
}

func (d *deferredLive) PluginNames() []string {
	if d.ready {
		return []string{d.name}
	}
	return nil
}

func (d *deferredLive) Plugin(name string) (core.Tool, bool) {
	if d.ready && name == d.name {
		return d.tool, true
	}
	return nil, false
}

func (d *deferredLive) redo(ctx context.Context) error {
	d.calls++
	if d.redoErr != nil {
		return d.redoErr
	}
	d.ready = true
	return nil
}

func TestDoorRedisoversOnceOnUnknownName(t *testing.T) {
	live := &deferredLive{name: "forged", tool: &stubTool{name: "forged"}}
	door := NewDoor(live, live.redo)
	out, err := door.Exec(context.Background(), json.RawMessage(`{"action":"run","name":"forged","args":{"text":"hi"}}`))
	if err != nil || out != "ran forged: {\"text\":\"hi\"}" {
		t.Fatalf("the self-healed call = (%q, %v), want the plugin's run verbatim", out, err)
	}
	if live.calls != 1 {
		t.Fatalf("redo ran %d times, want exactly one", live.calls)
	}
}

func TestDoorSkipsRedoOnKnownName(t *testing.T) {
	live := &deferredLive{name: "forged", tool: &stubTool{name: "forged"}, ready: true}
	door := NewDoor(live, live.redo)
	if _, err := door.Exec(context.Background(), json.RawMessage(`{"action":"run","name":"forged"}`)); err != nil {
		t.Fatalf("the known name's call: %v", err)
	}
	if live.calls != 0 {
		t.Fatalf("redo ran %d times on a known name, want none", live.calls)
	}
}

func TestDoorNamesRedoFailure(t *testing.T) {
	live := &deferredLive{name: "forged", tool: &stubTool{name: "forged"}}
	live.redoErr = errors.New("the kernel said no")
	door := NewDoor(live, live.redo)
	_, err := door.Exec(context.Background(), json.RawMessage(`{"action":"run","name":"forged"}`))
	if err == nil || !strings.Contains(err.Error(), "re-discovery failed") || !strings.Contains(err.Error(), "the kernel said no") {
		t.Fatalf("the failing redo must be named in the refusal: %v", err)
	}
}

func TestDoorNilRedoKeepsTheRefusal(t *testing.T) {
	live := &stubLive{names: []string{"networth"}, tool: &stubTool{name: "networth"}}
	door := NewDoor(live, nil)
	_, err := door.Exec(context.Background(), json.RawMessage(`{"action":"run","name":"ghost"}`))
	if err == nil || !strings.Contains(err.Error(), `unknown plugin "ghost"`) || !strings.Contains(err.Error(), "networth") {
		t.Fatalf("the nil-redo refusal must name the live plugins: %v", err)
	}
}

func TestDoorSchemaCarriesTheSameSelfHeal(t *testing.T) {
	ctx := context.Background()

	live := &deferredLive{name: "forged", tool: &stubTool{name: "forged"}}
	door := NewDoor(live, live.redo)
	out, err := door.Exec(ctx, json.RawMessage(`{"action":"schema","name":"forged"}`))
	if err != nil || out != "stub forged\nschema: {\"type\":\"object\"}" {
		t.Fatalf("the self-healed contract = (%q, %v), want the plugin's contract", out, err)
	}
	if live.calls != 1 {
		t.Fatalf("redo ran %d times, want exactly one", live.calls)
	}

	steady := &deferredLive{name: "alpha", tool: &stubTool{name: "alpha"}, ready: true}
	if _, err := NewDoor(steady, steady.redo).Exec(ctx, json.RawMessage(`{"action":"schema","name":"alpha"}`)); err != nil {
		t.Fatalf("the known name's contract: %v", err)
	}
	if steady.calls != 0 {
		t.Fatalf("redo ran %d times on a known name, want none", steady.calls)
	}

	broken := &deferredLive{name: "beta", tool: &stubTool{name: "beta"}}
	broken.redoErr = errors.New("the kernel said no")
	_, err = NewDoor(broken, broken.redo).Exec(ctx, json.RawMessage(`{"action":"schema","name":"beta"}`))
	if err == nil || !strings.Contains(err.Error(), "re-discovery failed") || !strings.Contains(err.Error(), "the kernel said no") {
		t.Fatalf("the failing redo must be named: %v", err)
	}

	plain := &stubLive{names: []string{"networth"}, tool: &stubTool{name: "networth"}}
	_, err = NewDoor(plain, nil).Exec(ctx, json.RawMessage(`{"action":"schema","name":"ghost"}`))
	if err == nil || !strings.Contains(err.Error(), `unknown plugin "ghost"`) || !strings.Contains(err.Error(), "networth") {
		t.Fatalf("the nil-redo refusal must name the live plugins: %v", err)
	}
}
