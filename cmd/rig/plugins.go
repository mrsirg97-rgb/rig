package main

import (
	"context"
	"errors"

	"github.com/mrsirg97-rgb/rig/v3/command"
	"github.com/mrsirg97-rgb/rig/v3/core"
	"github.com/mrsirg97-rgb/rig/v3/plugins"
)

func capPlugins(reports []plugins.Report, max int) []plugins.Report {
	if max <= 0 {
		return reports
	}
	out := make([]plugins.Report, 0, len(reports))
	live := 0
	for _, rep := range reports {
		if !rep.Skipped {
			if live >= max {
				rep.Skipped = true
				rep.Reason = "disabled: over the settings.json plugins.max cap"
			} else {
				live++
			}
		}
		out = append(out, rep)
	}
	return out
}

func (r *root) swapPlugins(ctx context.Context, reports []plugins.Report) (string, error) {
	reports = capPlugins(reports, r.pluginMax)
	infos := make([]command.PluginInfo, 0, len(reports))
	var pluginTools []core.Tool
	for _, rep := range reports {
		infos = append(infos, command.PluginInfo{
			Name: rep.Name, Description: rep.Description, File: rep.File,
			Skipped: rep.Skipped, Reason: rep.Reason,
		})
		if !rep.Skipped {
			tool := plugins.New(rep.Name, rep.Description, rep.File, rep.Schema, r.py)
			pluginTools = append(pluginTools, tool)
			r.tools[rep.Name] = tool
		}
	}
	names := make([]string, 0, len(reports))
	for _, rep := range reports {
		if !rep.Skipped {
			names = append(names, rep.Name)
		}
	}
	r.pluginTools = pluginTools
	r.live.Swap(append(r.tableTools(), r.pluginTools...), names...)
	r.pluginInfos = infos
	return command.RenderPlugins(infos, "reload", r.pluginsHome), nil
}

func (r *root) pluginNames() []string {
	out := make([]string, 0, len(r.pluginTools))
	for _, t := range r.pluginTools {
		out = append(out, t.Name())
	}
	return out
}

func (r *root) pluginDoor() func(string) bool {
	return func(name string) bool {
		return r.live != nil && r.live.IsPlugin(name)
	}
}

func (r *root) redoPlugins(ctx context.Context) error {
	_, err := r.reloadPlugins(ctx)
	return err
}

func (r *root) reloadPlugins(ctx context.Context) (string, error) {
	if r.eco == nil {
		return "", errors.New("plugins: no ecosystem (the root did not wire one)")
	}
	return r.eco.Reload(ctx)
}
