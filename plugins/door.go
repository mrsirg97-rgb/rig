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
	Plugin(name string) (core.Tool, bool)
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
	return "run a live plugin by name with its args, or fetch its contract: {\"action\": \"run\"|\"schema\", \"name\": ..., \"args\": ...}. Guidelines: the name enum is the live set; call schema first when you do not know a plugin's args; an unknown name re-discovers once, then refuses. Reply: the plugin's text, or its description and schema. Plugins are also importable from python by name."
}

func (d *Door) Schema() json.RawMessage {
	names := d.Live.PluginNames()
	enum, err := json.Marshal(names)
	if err != nil {
		enum = []byte("[]")
	}
	return json.RawMessage(fmt.Sprintf(`{"type":"object","properties":{"action":{"enum":["run","schema"],"description":"run the plugin, or fetch its contract"},"name":{"type":"string","enum":%s,"description":"the live plugin"},"args":{"type":"object","description":"the plugin's args, pass-through (run)"}},"required":["action","name"]}`, enum))
}

func (d *Door) Exec(ctx context.Context, args json.RawMessage) (string, error) {
	var in struct {
		Action string          `json:"action"`
		Name   string          `json:"name"`
		Args   json.RawMessage `json:"args"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", fmt.Errorf("plugin: bad call (want {action, name, args}): %v", err)
	}
	if in.Action == "" {
		return "", fmt.Errorf("plugin: no action (want {action, name, args})")
	}
	if in.Name == "" {
		return "", fmt.Errorf("plugin: no name (want {action, name, args})")
	}
	if in.Action != "run" && in.Action != "schema" {
		return "", fmt.Errorf("plugin: unknown action %q (want run or schema)", in.Action)
	}
	tool, ok := d.Live.Plugin(in.Name)
	if !ok && d.redo != nil {
		if err := d.redo(ctx); err != nil {
			return "", fmt.Errorf("plugin: unknown plugin %q; re-discovery failed: %v", in.Name, err)
		}
		tool, ok = d.Live.Plugin(in.Name)
	}
	if !ok {
		return "", fmt.Errorf("plugin: unknown plugin %q (live: %s)", in.Name, strings.Join(d.Live.PluginNames(), ", "))
	}
	switch in.Action {
	case "schema":
		return fmt.Sprintf("%s\nschema: %s", tool.Description(), tool.Schema()), nil
	default:
		body := in.Args
		if body == nil {
			body = json.RawMessage("{}")
		}
		return tool.Exec(ctx, body)
	}
}
