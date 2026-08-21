package plugins

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mrsirg97-rgb/rig/core"
)

// Live is the root's live plugin table (SPEC_GROWTH 9): the door resolves a
// named plugin and the schema tool reads its contract. middleware/toolset's
// Table implements it; the leaf never imports the middleware.
type Live interface {
	PluginNames() []string              // the live plugin names, sorted
	Tool(name string) (core.Tool, bool) // the named tool, if the table carries it
}

// Door is the plugin dispatch tool (SPEC_GROWTH 9): one native tool that
// collapses all plugin schemas to one request entry — the context fix for
// a grown table. The schema's name field carries the live plugin names'
// enum; args pass through to the named plugin's Exec. An unknown name
// runs the redo seam once (the root's reload) and re-resolves
// (SPEC_STREAMLINE 4); a nil redo keeps the plain refusal.
type Door struct {
	Live Live
	redo func(ctx context.Context) error
}

var _ core.Tool = (*Door)(nil)

func NewDoor(live Live, redo func(ctx context.Context) error) *Door {
	return &Door{Live: live, redo: redo}
}

func (d *Door) Name() string { return "plugin" }

func (d *Door) Description() string {
	return "run a live plugin: the model calls `plugin` with {name, args}; the schema's name enum lists the live plugins, and an unknown name re-discovers once before refusing (an out-of-band install needs no reload). `plugin_schema <name>` shows a plugin's contract (description and args) before you call it. Plugins are also importable from the python tool by name."
}

func (d *Door) Schema() json.RawMessage {
	names := d.Live.PluginNames()
	enum, err := json.Marshal(names)
	if err != nil {
		enum = []byte("[]")
	}
	return json.RawMessage(fmt.Sprintf(`{"type":"object","properties":{"name":{"type":"string","enum":%s,"description":"the live plugin to run"},"args":{"type":"object","description":"the plugin's args, pass-through"}},"required":["name"]}`, enum))
}

func (d *Door) Exec(ctx context.Context, args json.RawMessage) (string, error) {
	var in struct {
		Name string          `json:"name"`
		Args json.RawMessage `json:"args"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", fmt.Errorf("plugin: bad call (want {name, args}): %v", err)
	}
	if in.Name == "" {
		return "", fmt.Errorf("plugin: no name (want {name, args})")
	}
	tool, ok := d.Live.Tool(in.Name)
	if !ok && d.redo != nil {
		if err := d.redo(ctx); err != nil {
			return "", fmt.Errorf("plugin: unknown plugin %q; re-discovery failed: %v", in.Name, err)
		}
		tool, ok = d.Live.Tool(in.Name)
	}
	if !ok {
		return "", fmt.Errorf("plugin: unknown plugin %q (live: %s)", in.Name, strings.Join(d.Live.PluginNames(), ", "))
	}
	body := in.Args
	if body == nil {
		body = json.RawMessage("{}")
	}
	return tool.Exec(ctx, body)
}

// SchemaDoor is the plugin_schema tool (SPEC_GROWTH 9): returns one live
// plugin's description and schema verbatim — the model fetches the args
// it needs before a non-trivial call, so the request can carry the door
// alone.
type SchemaDoor struct {
	Live Live
	redo func(ctx context.Context) error
}

var _ core.Tool = (*SchemaDoor)(nil)

func NewSchemaDoor(live Live, redo func(ctx context.Context) error) *SchemaDoor {
	return &SchemaDoor{Live: live, redo: redo}
}

func (d *SchemaDoor) Name() string { return "plugin_schema" }

func (d *SchemaDoor) Description() string {
	return "show a live plugin's contract (description and JSON schema) before calling it through the `plugin` door. Schema: {name}."
}

func (d *SchemaDoor) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"name":{"type":"string","description":"the plugin name"}},"required":["name"]}`)
}

func (d *SchemaDoor) Exec(ctx context.Context, args json.RawMessage) (string, error) {
	var in struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", fmt.Errorf("plugin_schema: bad call (want {name}): %v", err)
	}
	if in.Name == "" {
		return "", fmt.Errorf("plugin_schema: no name (want {name})")
	}
	tool, ok := d.Live.Tool(in.Name)
	if !ok && d.redo != nil {
		if err := d.redo(ctx); err != nil {
			return "", fmt.Errorf("plugin_schema: unknown plugin %q; re-discovery failed: %v", in.Name, err)
		}
		tool, ok = d.Live.Tool(in.Name)
	}
	if !ok {
		return "", fmt.Errorf("plugin_schema: unknown plugin %q (live: %s)", in.Name, strings.Join(d.Live.PluginNames(), ", "))
	}
	return fmt.Sprintf("%s\nschema: %s", tool.Description(), tool.Schema()), nil
}
