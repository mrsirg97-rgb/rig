package plugins

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/tool"
)

type Live interface {
	PluginNames() []string
	Plugin(name string) (core.Tool, bool)
}

type Door struct {
	tool.Definition
	Live Live
	redo func(ctx context.Context) error
}

var _ core.Tool = (*Door)(nil)

func NewDoor(live Live, redo func(ctx context.Context) error) *Door {
	return &Door{Definition: tool.Def("plugin"), Live: live, redo: redo}
}

func (d *Door) Schema() json.RawMessage {
	schema := d.Definition.Schema()
	names := d.Live.PluginNames()
	if len(names) == 0 {
		return schema
	}
	enum, err := json.Marshal(names)
	if err != nil {
		return schema
	}
	loc := liveName.FindIndex(schema)
	if loc == nil {
		return schema
	}
	out := make([]byte, 0, len(schema)+len(enum)+8)
	out = append(out, schema[:loc[1]]...)
	out = append(out, ` "enum": `...)
	out = append(out, enum...)
	out = append(out, ',')
	out = append(out, schema[loc[1]:]...)
	return out
}

var liveName = regexp.MustCompile(`"name"\s*:\s*\{\s*"type"\s*:\s*"string"\s*,`)

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
