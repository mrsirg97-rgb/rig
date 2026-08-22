package plugins

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mrsirg97-rgb/rig/core"
)

type Live interface {
	PluginNames() []string
	Tool(name string) (core.Tool, bool)
}

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
	return "run a live plugin by name with its args. Guidelines: the name enum is the live set; call plugin_schema first when you do not know a plugin's args; an unknown name re-discovers once, then refuses. Reply: the plugin's text. Plugins are also importable from python by name."
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
	return "show a live plugin's contract. Guidelines: before the first call of a plugin whose args you do not know. Reply: its description and JSON schema, verbatim."
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
