package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/v2/core"
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
	door := NewDoor(live, nil, nil)
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
	if strings.Join(params.Properties.Action.Enum, ",") != "run,schema,list,create,delete,reload" {
		t.Fatalf("the door's action enum = %v, want run, schema, list, create, delete, reload", params.Properties.Action.Enum)
	}

	live.names = []string{"networth", "flip_calc"}
	if err := json.Unmarshal(door.Schema(), &params); err != nil {
		t.Fatal(err)
	}
	if len(params.Properties.Name.Enum) != 2 {
		t.Fatalf("the door's enum must follow the live table, got %v", params.Properties.Name.Enum)
	}
}

func TestDoorSchemaOmitsTheNameEnumWhenThereAreNoLivePlugins(t *testing.T) {
	door := NewDoor(&stubLive{names: []string{}}, nil, nil)
	var schema struct {
		Properties struct {
			Name struct {
				Type string   `json:"type"`
				Enum []string `json:"enum"`
			} `json:"name"`
			Action struct {
				Enum []string `json:"enum"`
			} `json:"action"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(door.Schema(), &schema); err != nil {
		t.Fatalf("the zero-plugin schema is not JSON: %v", err)
	}
	if schema.Properties.Name.Type != "string" {
		t.Fatalf("name = %+v, want a plain string", schema.Properties.Name)
	}
	if schema.Properties.Name.Enum != nil {
		t.Fatalf("an empty enum must be omitted: llama-server rejects it, got %v", schema.Properties.Name.Enum)
	}
	if len(schema.Properties.Action.Enum) != 6 {
		t.Fatalf("the action enum must stay: %v", schema.Properties.Action.Enum)
	}
}

func TestDoorExecResolvesAndCalls(t *testing.T) {
	live := &stubLive{names: []string{"networth"}, tool: &stubTool{name: "networth"}}
	door := NewDoor(live, nil, nil)
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
	door := NewDoor(live, nil, nil)
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
	door := NewDoor(live, live.redo, nil)
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
	door := NewDoor(live, live.redo, nil)
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
	door := NewDoor(live, live.redo, nil)
	_, err := door.Exec(context.Background(), json.RawMessage(`{"action":"run","name":"forged"}`))
	if err == nil || !strings.Contains(err.Error(), "re-discovery failed") || !strings.Contains(err.Error(), "the kernel said no") {
		t.Fatalf("the failing redo must be named in the refusal: %v", err)
	}
}

func TestDoorNilRedoKeepsTheRefusal(t *testing.T) {
	live := &stubLive{names: []string{"networth"}, tool: &stubTool{name: "networth"}}
	door := NewDoor(live, nil, nil)
	_, err := door.Exec(context.Background(), json.RawMessage(`{"action":"run","name":"ghost"}`))
	if err == nil || !strings.Contains(err.Error(), `unknown plugin "ghost"`) || !strings.Contains(err.Error(), "networth") {
		t.Fatalf("the nil-redo refusal must name the live plugins: %v", err)
	}
}

func TestDoorSchemaCarriesTheSameSelfHeal(t *testing.T) {
	ctx := context.Background()

	live := &deferredLive{name: "forged", tool: &stubTool{name: "forged"}}
	door := NewDoor(live, live.redo, nil)
	out, err := door.Exec(ctx, json.RawMessage(`{"action":"schema","name":"forged"}`))
	if err != nil || out != "stub forged\nschema: {\"type\":\"object\"}" {
		t.Fatalf("the self-healed contract = (%q, %v), want the plugin's contract", out, err)
	}
	if live.calls != 1 {
		t.Fatalf("redo ran %d times, want exactly one", live.calls)
	}

	steady := &deferredLive{name: "alpha", tool: &stubTool{name: "alpha"}, ready: true}
	if _, err := NewDoor(steady, steady.redo, nil).Exec(ctx, json.RawMessage(`{"action":"schema","name":"alpha"}`)); err != nil {
		t.Fatalf("the known name's contract: %v", err)
	}
	if steady.calls != 0 {
		t.Fatalf("redo ran %d times on a known name, want none", steady.calls)
	}

	broken := &deferredLive{name: "beta", tool: &stubTool{name: "beta"}}
	broken.redoErr = errors.New("the kernel said no")
	_, err = NewDoor(broken, broken.redo, nil).Exec(ctx, json.RawMessage(`{"action":"schema","name":"beta"}`))
	if err == nil || !strings.Contains(err.Error(), "re-discovery failed") || !strings.Contains(err.Error(), "the kernel said no") {
		t.Fatalf("the failing redo must be named: %v", err)
	}

	plain := &stubLive{names: []string{"networth"}, tool: &stubTool{name: "networth"}}
	_, err = NewDoor(plain, nil, nil).Exec(ctx, json.RawMessage(`{"action":"schema","name":"ghost"}`))
	if err == nil || !strings.Contains(err.Error(), `unknown plugin "ghost"`) || !strings.Contains(err.Error(), "networth") {
		t.Fatalf("the nil-redo refusal must name the live plugins: %v", err)
	}
}

func TestDoorCarriesTheEcosystemActions(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "plugins"), 0o755); err != nil {
		t.Fatal(err)
	}
	swapped := 0
	eco := NewEcosystem(home, map[string]bool{"bash": true, "plugin": true}, &fakeKernel{}, func(ctx context.Context, reports []Report) (string, error) {
		swapped++
		return "plugins: reload: 0 loaded, 0 skipped", nil
	}, func() (string, error) { return "plugins: none", nil })
	door := NewDoor(&stubLive{}, nil, eco)
	out, err := door.Exec(context.Background(), json.RawMessage(`{"action":"list"}`))
	if err != nil || out != "plugins: none" {
		t.Fatalf("list through the door = %q, %v", out, err)
	}
	out, err = door.Exec(context.Background(), json.RawMessage(`{"action":"create","name":"echo","source":"DESCRIPTION = 'x'\nSCHEMA = {}\ndef run(args):\n    return 'x'\n"}`))
	if err != nil || !strings.Contains(out, "plugin: create: created echo") {
		t.Fatalf("create through the door = %q, %v", out, err)
	}
	if _, err := os.Stat(filepath.Join(home, "plugins", "pending", "echo.py")); err != nil {
		t.Fatalf("create must land in pending: %v", err)
	}
	if _, err := door.Exec(context.Background(), json.RawMessage(`{"action":"reload"}`)); err != nil || swapped != 1 {
		t.Fatalf("reload through the door: swapped=%d err=%v", swapped, err)
	}
}

func TestDoorWithoutAnEcosystemNamesTheMissingSeam(t *testing.T) {
	door := NewDoor(&stubLive{}, nil, nil)
	_, err := door.Exec(context.Background(), json.RawMessage(`{"action":"list"}`))
	if err == nil || !strings.Contains(err.Error(), "no ecosystem seam") {
		t.Fatalf("err = %v, want the missing seam named", err)
	}
}

func TestDoorRunNeedsANameAndTheEcosystemVerbsDoNot(t *testing.T) {
	door := NewDoor(&stubLive{}, nil, nil)
	_, err := door.Exec(context.Background(), json.RawMessage(`{"action":"run"}`))
	if err == nil || !strings.Contains(err.Error(), "run needs a name") {
		t.Fatalf("run without a name: %v", err)
	}
	_, err = door.Exec(context.Background(), json.RawMessage(`{"action":"paint"}`))
	if err == nil || !strings.Contains(err.Error(), `unknown action "paint" (want run, schema, list, create, delete or reload)`) {
		t.Fatalf("unknown action: %v", err)
	}
}

func TestPluginDoorVerbsAndExecShareTheirChecks(t *testing.T) {
	live := &stubLive{names: []string{"networth"}, tool: &stubTool{name: "networth"}}
	door := NewDoor(live, nil, nil)
	ctx := context.Background()

	_, runErr := door.Run(ctx, "", json.RawMessage(`{}`))
	_, execErr := door.Exec(ctx, json.RawMessage(`{"action":"run"}`))
	if runErr == nil || execErr == nil {
		t.Fatalf("a run without a name refuses on both doors, run=%v exec=%v", runErr, execErr)
	}
	if runErr.Error() != "plugin: run needs a name (the live plugin)" || execErr.Error() != runErr.Error() {
		t.Fatalf("the refusal is the same words on either door: run %q exec %q", runErr, execErr)
	}

	_, schemaErr := door.Contract(ctx, "")
	_, execErr = door.Exec(ctx, json.RawMessage(`{"action":"schema"}`))
	if schemaErr == nil || execErr == nil {
		t.Fatalf("a schema without a name refuses on both doors, schema=%v exec=%v", schemaErr, execErr)
	}
	if schemaErr.Error() != "plugin: schema needs a name (the live plugin)" || execErr.Error() != schemaErr.Error() {
		t.Fatalf("the refusal is the same words on either door: schema %q exec %q", schemaErr, execErr)
	}

	_, runErr = door.Run(ctx, "nope", json.RawMessage(`{}`))
	_, execErr = door.Exec(ctx, json.RawMessage(`{"action":"run","name":"nope"}`))
	if runErr == nil || execErr == nil || !strings.Contains(runErr.Error(), `unknown plugin "nope"`) || !strings.Contains(runErr.Error(), "networth") || execErr.Error() != runErr.Error() {
		t.Fatalf("an unknown plugin refuses the same words on either door: run=%v exec=%v", runErr, execErr)
	}

	_, listErr := door.List(ctx)
	_, execErr = door.Exec(ctx, json.RawMessage(`{"action":"list"}`))
	if listErr == nil || execErr == nil || !strings.Contains(listErr.Error(), "no ecosystem seam") || execErr.Error() != listErr.Error() {
		t.Fatalf("a door without the ecosystem seam refuses the same words on either door: list=%v exec=%v", listErr, execErr)
	}

	got, err := door.Run(ctx, "networth", json.RawMessage(`{"a":1}`))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want, err := door.Exec(ctx, json.RawMessage(`{"action":"run","name":"networth","args":{"a":1}}`))
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if got != want || got != `ran networth: {"a":1}` {
		t.Fatalf("one run replies the same bytes through either door: run %q exec %q", got, want)
	}

	fromContract, err := door.Contract(ctx, "networth")
	if err != nil {
		t.Fatalf("contract: %v", err)
	}
	fromSchema, err := door.Exec(ctx, json.RawMessage(`{"action":"schema","name":"networth"}`))
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if fromContract != fromSchema {
		t.Fatalf("one contract replies the same bytes through either door: contract %q exec %q", fromContract, fromSchema)
	}
}
