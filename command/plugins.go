package command

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mrsirg97-rgb/rig/v2/plugins"
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
	if len(infos) == 0 {
		switch {
		case verb != "":
			return "plugins: " + verb + " \u00b7 none"
		case dir == "":
			return "plugins: none"
		default:
			return "plugins: none in " + dir
		}
	}
	loaded, skipped, wID := 0, 0, 0
	for _, p := range infos {
		if p.Skipped {
			skipped++
		} else {
			loaded++
		}
		if n := len(pluginID(p)); n > wID {
			wID = n
		}
	}
	var b strings.Builder
	b.WriteString(plural(len(infos), "plugin"))
	if verb != "" {
		b.WriteString(" \u00b7 " + verb)
	}
	fmt.Fprintf(&b, " \u00b7 %d loaded \u00b7 %d skipped", loaded, skipped)
	for _, p := range infos {
		if p.Skipped {
			b.WriteString("\n" + row(pluginID(p), wID, markFailed, p.Reason, p.File))
		} else {
			b.WriteString("\n" + row(pluginID(p), wID, markDone, p.Description, p.File))
		}
	}
	return b.String()
}

func pluginID(p PluginInfo) string {
	if p.Name != "" {
		return p.Name
	}
	return strings.TrimSuffix(filepath.Base(p.File), filepath.Ext(p.File))
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
	var names, paths []string
	wID := 0
	for _, en := range entries {
		if en.IsDir() || !strings.HasSuffix(en.Name(), ".py") {
			continue
		}
		name := strings.TrimSuffix(en.Name(), ".py")
		names = append(names, name)
		paths = append(paths, filepath.Join(dir, en.Name()))
		if len(name) > wID {
			wID = len(name)
		}
	}
	if len(names) == 0 {
		return "plugins: no " + zone + " plugins", nil
	}
	var b strings.Builder
	b.WriteString(plural(len(names), zone+" plugin"))
	for i, name := range names {
		b.WriteString("\n" + row(name, wID, markIdle, descriptionOf(paths[i]), paths[i]))
	}
	return b.String(), nil
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
	_, statErr := os.Stat(dst)
	replacing := statErr == nil
	if err := os.Rename(src, dst); err != nil {
		return "", fmt.Errorf("plugins: approve: %v", err)
	}
	line := fmt.Sprintf("plugins: approved %s (%s -> %s)", name, src, dst)
	if replacing {
		line = fmt.Sprintf("plugins: approved %s, replacing the installed one (%s -> %s)", name, src, dst)
	}
	if e.Reload == nil {
		return line + "; the discovery loads it at the next start", nil
	}
	reply, err := e.Reload(ctx)
	if err != nil {
		return "", fmt.Errorf("%s; the reload failed: %v", line, err)
	}
	return line + "\n" + reply, nil
}
