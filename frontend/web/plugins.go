package web

import (
	"path/filepath"
	"strings"

	"github.com/mrsirg97-rgb/rig/plugins"
)

type Plugin struct {
	Name        string
	Description string
	File        string
	Pending     bool
}

func listPlugins(home string) (loaded, pending, disabled []Plugin, err error) {
	files, err := plugins.List(home)
	if err != nil {
		return nil, nil, nil, err
	}
	loaded = make([]Plugin, 0, len(files))
	for _, f := range files {
		loaded = append(loaded, pluginRow(f, false))
	}
	pfiles, err := plugins.Zone(home, "pending")
	if err != nil {
		return nil, nil, nil, err
	}
	pending = make([]Plugin, 0, len(pfiles))
	for _, f := range pfiles {
		pending = append(pending, pluginRow(f, true))
	}
	dfiles, err := plugins.Zone(home, "disabled")
	if err != nil {
		return nil, nil, nil, err
	}
	disabled = make([]Plugin, 0, len(dfiles))
	for _, f := range dfiles {
		disabled = append(disabled, pluginRow(f, false))
	}
	return loaded, pending, disabled, nil
}

func pluginRow(file string, pending bool) Plugin {
	name := strings.TrimSuffix(filepath.Base(file), ".py")
	return Plugin{Name: name, Description: descriptionOf(file), File: file, Pending: pending}
}

func descriptionOf(file string) string { return plugins.DescriptionOf(file) }
