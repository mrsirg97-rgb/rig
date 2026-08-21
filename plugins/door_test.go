package plugins

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/core"
)

// stubLive is the door's Live seam: a tiny table with one plugin.
type stubLive struct {
	names []string
	tool  core.Tool
}

func (s *stubLive) PluginNames() []string { return s.names }
func (s *stubLive) Tool(name string) (core.Tool, bool) {
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

// TestDoorSurfacesAreTheNativeContract (SPEC_GROWTH 9, named): `plugin`
// and `plugin_schema` are natives with the small schemas; the door's
// schema carries the live names' enum.
func TestDoorSurfacesAreTheNativeContract(t *testing.T) {
	live := &stubLive{names: []string{"networth"}, tool: &stubTool{name: "networth"}}
	door := NewDoor(live)
	schema := NewSchemaDoor(live)
	if door.Name() != "plugin" || schema.Name() != "plugin_schema" {
		t.Fatalf("door names: %q, %q", door.Name(), schema.Name())
	}
	var params struct {
		Properties struct {
			Name struct {
				Enum []string `json:"enum"`
			} `json:"name"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(door.Schema(), &params); err != nil {
		t.Fatalf("the door's schema is not JSON: %v", err)
	}
	if len(params.Properties.Name.Enum) != 1 || params.Properties.Name.Enum[0] != "networth" {
		t.Fatalf("the door's enum = %v, want the live names", params.Properties.Name.Enum)
	}
	// the enum reflects the live table (a swap changes the next schema).
	live.names = []string{"networth", "flip_calc"}
	if err := json.Unmarshal(door.Schema(), &params); err != nil {
		t.Fatal(err)
	}
	if len(params.Properties.Name.Enum) != 2 {
		t.Fatalf("the door's enum must follow the live table, got %v", params.Properties.Name.Enum)
	}
}

// TestDoorExecResolvesAndCalls (SPEC_GROWTH 9, named): `plugin` with
// {name, args} calls the named plugin (args verbatim, result verbatim);
// an unknown name is a loud tool error naming the live plugins;
// `plugin_schema` returns the plugin's description and schema.
func TestDoorExecResolvesAndCalls(t *testing.T) {
	live := &stubLive{names: []string{"networth"}, tool: &stubTool{name: "networth"}}
	door := NewDoor(live)
	schema := NewSchemaDoor(live)
	ctx := context.Background()

	out, err := door.Exec(ctx, json.RawMessage(`{"name":"networth","args":{"a":1}}`))
	if err != nil || out != "ran networth: {\"a\":1}" {
		t.Fatalf("the door's call = (%q, %v), want the plugin's run verbatim", out, err)
	}
	out, err = door.Exec(ctx, json.RawMessage(`{"name":"nope"}`))
	if err == nil || !strings.Contains(err.Error(), "unknown plugin \"nope\"") || !strings.Contains(err.Error(), "networth") {
		t.Fatalf("an unknown name must be a loud error naming the live plugins: (%q, %v)", out, err)
	}
	out, err = schema.Exec(ctx, json.RawMessage(`{"name":"networth"}`))
	if err != nil || out != "stub networth\nschema: {\"type\":\"object\"}" {
		t.Fatalf("plugin_schema = (%q, %v), want the plugin's contract", out, err)
	}
	if _, err = schema.Exec(ctx, json.RawMessage(`{"name":"nope"}`)); err == nil {
		t.Fatal("plugin_schema of an unknown name must error")
	}
}
