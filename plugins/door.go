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

type Plugin interface {
	tool.Definition
	Exec(ctx context.Context, args json.RawMessage) (string, error)

	Run(ctx context.Context, name string, args json.RawMessage) (string, error)
	Contract(ctx context.Context, name string) (string, error)
	List(ctx context.Context) (string, error)
	Create(ctx context.Context, name, source string) (string, error)
	Delete(ctx context.Context, name string) (string, error)
	Reload(ctx context.Context) (string, error)
}

type door struct {
	tool.Definition
	Live Live
	redo func(ctx context.Context) error
	eco  *Ecosystem
}

var _ Plugin = (*door)(nil)

func NewDoor(live Live, redo func(ctx context.Context) error, eco *Ecosystem) Plugin {
	return &door{Definition: tool.Def("plugin"), Live: live, redo: redo, eco: eco}
}

func (d *door) Schema() json.RawMessage {
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

func (d *door) Exec(ctx context.Context, args json.RawMessage) (string, error) {
	var in struct {
		Action string          `json:"action"`
		Name   string          `json:"name"`
		Args   json.RawMessage `json:"args"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", fmt.Errorf("plugin: bad call (want {action, name, args, source}): %v", err)
	}
	switch in.Action {
	case "":
		return "", fmt.Errorf("plugin: no action (want run, schema, list, create, delete or reload)")
	case "run":
		body := in.Args
		if body == nil {
			body = json.RawMessage("{}")
		}
		return d.Run(ctx, in.Name, body)
	case "schema":
		return d.Contract(ctx, in.Name)
	case "list":
		return d.List(ctx)
	case "create":
		source, err := sourceArg(args)
		if err != nil {
			return "", err
		}
		return d.Create(ctx, in.Name, source)
	case "delete":
		return d.Delete(ctx, in.Name)
	case "reload":
		return d.Reload(ctx)
	default:
		return "", fmt.Errorf("plugin: unknown action %q (want run, schema, list, create, delete or reload)", in.Action)
	}
}

func sourceArg(args json.RawMessage) (string, error) {
	var in struct {
		Source string `json:"source"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", fmt.Errorf("plugin: bad call (want {action, name, source}): %v", err)
	}
	return in.Source, nil
}

func (d *door) Run(ctx context.Context, name string, args json.RawMessage) (string, error) {
	if name == "" {
		return "", fmt.Errorf("plugin: run needs a name (the live plugin)")
	}
	tool, err := d.lookup(ctx, name)
	if err != nil {
		return "", err
	}
	return tool.Exec(ctx, args)
}

func (d *door) Contract(ctx context.Context, name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("plugin: schema needs a name (the live plugin)")
	}
	tool, err := d.lookup(ctx, name)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s\nschema: %s", tool.Description(), tool.Schema()), nil
}

func (d *door) lookup(ctx context.Context, name string) (core.Tool, error) {
	tool, ok := d.Live.Plugin(name)
	if !ok && d.redo != nil {
		if err := d.redo(ctx); err != nil {
			return nil, fmt.Errorf("plugin: unknown plugin %q; re-discovery failed: %v", name, err)
		}
		tool, ok = d.Live.Plugin(name)
	}
	if !ok {
		return nil, fmt.Errorf("plugin: unknown plugin %q (live: %s)", name, strings.Join(d.Live.PluginNames(), ", "))
	}
	return tool, nil
}

func (d *door) List(ctx context.Context) (string, error) {
	if d.eco == nil {
		return "", fmt.Errorf("plugin: list: no ecosystem seam (the root did not wire one)")
	}
	return d.eco.ListEcosystem(ctx)
}

func (d *door) Create(ctx context.Context, name, source string) (string, error) {
	if d.eco == nil {
		return "", fmt.Errorf("plugin: create: no ecosystem seam (the root did not wire one)")
	}
	return d.eco.Create(ctx, name, source)
}

func (d *door) Delete(ctx context.Context, name string) (string, error) {
	if d.eco == nil {
		return "", fmt.Errorf("plugin: delete: no ecosystem seam (the root did not wire one)")
	}
	return d.eco.Delete(ctx, name)
}

func (d *door) Reload(ctx context.Context) (string, error) {
	if d.eco == nil {
		return "", fmt.Errorf("plugin: reload: no ecosystem seam (the root did not wire one)")
	}
	return d.eco.Reload(ctx)
}
