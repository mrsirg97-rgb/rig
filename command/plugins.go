package command

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mrsirg97-rgb/rig/plugins"
)

type PluginInfo struct {
	Name        string
	Description string
	File        string
	Skipped     bool
	Reason      string
}

type pluginsCmd struct{}

func (pluginsCmd) Name() string { return "plugins" }

func (pluginsCmd) Description() string {
	return "list the python plugins: the loaded and the skipped ones, the pending zone, and the disabled zone; approve <name> installs a pending plugin; disable <name> / enable <name> move a plugin between plugins/ and plugins/disabled/; reload re-registers from disk (the next turn); create <text> queues the authoring prompt"
}

func (pluginsCmd) Sub() []Sub {
	return []Sub{
		{Name: "pending", Desc: "list the pending zone (the model's authoring), with each file's DESCRIPTION"},
		{Name: "approve", Desc: "approve <name>: move the pending plugin to the top level (the operator's verb)"},
		{Name: "reload", Desc: "re-run the discovery; the new list is registered on the next turn"},
		{Name: "create", Desc: "create <text>: queue the authoring prompt (the plugin lands in the pending zone)"},
		{Name: "disabled", Desc: "list the disabled zone (plugins/disabled/), with each file's DESCRIPTION"},
		{Name: "enable", Desc: "enable <name>: move a plugin from plugins/disabled/ back to plugins/ (live next turn)"},
		{Name: "disable", Desc: "disable <name>: move a plugin into plugins/disabled/ (hidden, not callable, next turn)"},
	}
}

const usage = "plugins: usage: plugins | plugins pending | plugins disabled | plugins approve <name> | plugins reload | plugins create <text> | plugins enable <name> | plugins disable <name>"

const createTemplate = "author a plugin: %s; the contract is DESCRIPTION, SCHEMA, run(args) -> str; write it SELF-CONTAINED to the pending directory (SPEC_SANDBOX); the operator installs it with /plugins approve; then call it through the plugin door and test it with one call."

func (pluginsCmd) Run(ctx context.Context, args string, env any) (string, error) {
	fields := strings.Fields(args)
	switch {
	case len(fields) == 0:
		return listPlugins(env)
	case len(fields) == 1 && fields[0] == "pending":
		return zoneList(env, "pending")
	case len(fields) == 1 && fields[0] == "disabled":
		return zoneList(env, "disabled")
	case len(fields) == 1 && fields[0] == "reload":
		return reload(env, ctx)
	case len(fields) == 1 && fields[0] == "create":
		return "", errors.New(usage)
	case len(fields) > 1 && fields[0] == "create":
		return create(env, ctx, strings.TrimSpace(strings.TrimPrefix(args, "create")))
	case len(fields) == 2 && fields[0] == "approve":
		return approve(ctx, env, fields[1])
	case len(fields) == 2 && fields[0] == "disable":
		return move(ctx, env, "disable", fields[1], "", "disabled")
	case len(fields) == 2 && fields[0] == "enable":
		return move(ctx, env, "enable", fields[1], "disabled", "")
	default:
		return "", errors.New(usage)
	}
}

func move(ctx context.Context, env any, verb, name, from, to string) (string, error) {
	e, err := EnvOf(env)
	if err != nil {
		return "", err
	}
	if e.PluginsDir == "" {
		return "", errors.New("plugins: no plugins seam (the root did not wire one)")
	}
	src, dst, err := plugins.Move(e.PluginsDir, name, from, to)
	if err != nil {
		return "", fmt.Errorf("plugins: %s: %v", verb, err)
	}
	line := fmt.Sprintf("plugins: %sd %s (%s -> %s)", verb, name, src, dst)
	if e.Reload == nil {
		return line + "; the discovery applies it at the next start", nil
	}
	reply, err := e.Reload(ctx)
	if err != nil {
		return "", fmt.Errorf("%s; the reload failed: %v", line, err)
	}
	return line + "\n" + reply, nil
}

func listPlugins(env any) (string, error) {
	e, err := EnvOf(env)
	if err != nil {
		return "", err
	}
	if e.Plugins == nil {
		return "", errors.New("plugins: no plugins seam (the root did not wire one)")
	}
	return RenderPlugins(e.Plugins(), "", e.PluginsDir), nil
}

func RenderPlugins(infos []PluginInfo, verb, dir string) string {
	if len(infos) == 0 && verb == "" {
		if dir == "" {
			return "plugins: none"
		}
		return "plugins: none in " + dir
	}
	loaded, skipped := 0, 0
	for _, p := range infos {
		if p.Skipped {
			skipped++
		} else {
			loaded++
		}
	}
	var b []byte
	if verb == "" {
		b = fmt.Appendf(b, "plugins: %d loaded, %d skipped\n", loaded, skipped)
	} else {
		b = fmt.Appendf(b, "plugins: %s: %d loaded, %d skipped\n", verb, loaded, skipped)
	}
	if loaded > 0 {
		b = append(b, "loaded:\n"...)
		for _, p := range infos {
			if !p.Skipped {
				b = fmt.Appendf(b, "  %s: %s (%s)\n", p.Name, p.Description, p.File)
			}
		}
	}
	if skipped > 0 {
		b = append(b, "skipped:\n"...)
		for _, p := range infos {
			if p.Skipped {
				b = fmt.Appendf(b, "  %s: %s\n", filepath.Base(p.File), p.Reason)
			}
		}
	}
	if len(infos) == 0 {
		return strings.TrimSuffix(string(b), "\n")
	}
	return string(b)
}

func reload(env any, ctx context.Context) (string, error) {
	e, err := EnvOf(env)
	if err != nil {
		return "", err
	}
	if e.Reload == nil {
		return "", errors.New("plugins: no reload seam (the root did not wire one)")
	}
	return e.Reload(ctx)
}

func create(env any, ctx context.Context, text string) (string, error) {
	e, err := EnvOf(env)
	if err != nil {
		return "", err
	}
	if text == "" {
		return "", errors.New(usage)
	}
	if e.Steer == nil {
		return "", errors.New("plugins: no steering seam (the frontend does not support steering)")
	}
	line := fmt.Sprintf(createTemplate, text)
	if e.Steer.Steer(line) {
		return "plugins: create: queued " + line + " · turn interrupted", nil
	}
	return "plugins: create: queued " + line, nil
}

func zoneList(env any, zone string) (string, error) {
	e, err := EnvOf(env)
	if err != nil {
		return "", err
	}
	if e.PluginsDir == "" {
		return "", errors.New("plugins: no plugins seam (the root did not wire one)")
	}
	dir := filepath.Join(e.PluginsDir, zone)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "plugins: no " + zone + " plugins", nil
		}
		return "", fmt.Errorf("plugins: %s: %v", zone, err)
	}
	var rows []string
	for _, en := range entries {
		if en.IsDir() || !strings.HasSuffix(en.Name(), ".py") {
			continue
		}
		name := strings.TrimSuffix(en.Name(), ".py")
		path := filepath.Join(dir, en.Name())
		rows = append(rows, fmt.Sprintf("  %s: %s (%s)", name, descriptionOf(path), path))
	}
	if len(rows) == 0 {
		return "plugins: no " + zone + " plugins", nil
	}
	return fmt.Sprintf("plugins: %d %s\n%s\n", len(rows), zone, strings.Join(rows, "\n")), nil
}

func descriptionOf(path string) string {
	d := plugins.DescriptionOf(path)
	if d == "" {
		if _, err := os.Stat(path); err != nil {
			return fmt.Sprintf("(read: %v)", err)
		}
		return "(no DESCRIPTION)"
	}
	return d
}

func approve(ctx context.Context, env any, name string) (string, error) {
	e, err := EnvOf(env)
	if err != nil {
		return "", err
	}
	if e.PluginsDir == "" {
		return "", errors.New("plugins: no plugins seam (the root did not wire one)")
	}
	if name == "." || name == ".." || strings.ContainsAny(name, "/\\") {
		return "", fmt.Errorf("plugins: approve: %q is not a plugin name (the filename stem)", name)
	}
	src := filepath.Join(e.PluginsDir, "pending", name+".py")
	if _, err := os.Stat(src); err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("plugins: approve: no pending plugin %q (%s absent)", name, src)
		}
		return "", fmt.Errorf("plugins: approve: %v", err)
	}
	if e.Tools != nil {
		if _, collides := e.Tools[name]; collides {
			return "", fmt.Errorf("plugins: name collision: %q (%s.py) is already a native tool", name, name)
		}
	}
	dst := filepath.Join(e.PluginsDir, name+".py")
	if _, err := os.Stat(dst); err == nil {
		return "", fmt.Errorf("plugins: approve: %q is already installed (%s exists — remove it to install the pending one)", name, dst)
	}
	if err := os.Rename(src, dst); err != nil {
		return "", fmt.Errorf("plugins: approve: %v", err)
	}
	line := fmt.Sprintf("plugins: approved %s (%s -> %s)", name, src, dst)
	if e.Reload == nil {
		return line + "; the discovery loads it at the next start", nil
	}
	reply, err := e.Reload(ctx)
	if err != nil {
		return "", fmt.Errorf("%s; the reload failed: %v", line, err)
	}
	return line + "\n" + reply, nil
}
