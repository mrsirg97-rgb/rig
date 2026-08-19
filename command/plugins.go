// The /plugins command (SPEC_PLUGINS 4): the loaded plugins (name,
// description, file) and the skipped ones with their reasons, in file
// order. No args, read-only, Frontend-side like the rest — the rows
// cross as plain PluginInfo (command stays a leaf over core and models).
package command

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
)

// PluginInfo is one discovered plugin file (SPEC_PLUGINS 4): the name
// (the filename stem), the description and file when loaded, the
// reason when skipped.
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
	return "list the python plugins: the loaded ones, and the skipped ones with their reasons"
}

func (pluginsCmd) Run(ctx context.Context, args string, env any) (string, error) {
	if args != "" {
		return "", errors.New("plugins: usage: plugins")
	}
	e, err := EnvOf(env)
	if err != nil {
		return "", err
	}
	if e.Plugins == nil {
		return "", errors.New("plugins: no plugins seam (the root did not wire one)")
	}
	if len(e.Plugins) == 0 {
		return "plugins: none", nil
	}
	loaded, skipped := 0, 0
	for _, p := range e.Plugins {
		if p.Skipped {
			skipped++
		} else {
			loaded++
		}
	}
	var b []byte
	b = fmt.Appendf(b, "plugins: %d loaded, %d skipped\n", loaded, skipped)
	if loaded > 0 {
		b = append(b, "loaded:\n"...)
		for _, p := range e.Plugins {
			if !p.Skipped {
				b = fmt.Appendf(b, "  %s: %s (%s)\n", p.Name, p.Description, p.File)
			}
		}
	}
	if skipped > 0 {
		b = append(b, "skipped:\n"...)
		for _, p := range e.Plugins {
			if p.Skipped {
				b = fmt.Appendf(b, "  %s: %s\n", filepath.Base(p.File), p.Reason)
			}
		}
	}
	return string(b), nil
}
