package command

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

type themeCmd struct{}

func (themeCmd) Name() string { return "theme" }

func (themeCmd) Description() string {
	return "the colors: bare shows the preset, /theme <warm|cool|custom> sets it"
}

func (themeCmd) Sub() []Sub {
	return []Sub{
		{Name: "warm", Desc: "the default palette"},
		{Name: "cool", Desc: "the cool palette"},
		{Name: "custom", Desc: "your own theme.json in the rig home"},
	}
}

func themePresets() string { return "warm, cool, custom" }

func (themeCmd) Run(ctx context.Context, args string, env any) (string, error) {
	e, err := EnvOf(env)
	if err != nil {
		return "", err
	}
	fields := strings.Fields(args)
	if len(fields) > 1 {
		return "", errors.New("theme: usage: theme [<preset>]")
	}
	if e.Theme == nil || e.SetTheme == nil {
		return "", errors.New("theme: no theme seam (the root did not wire one)")
	}
	if len(fields) == 0 {
		name := e.Theme()
		switch name {
		case "":
			return "theme: warm (default)", nil
		case "custom":
			return "theme: custom (theme.json)", nil
		default:
			return "theme: " + name, nil
		}
	}
	name := fields[0]
	if name != "warm" && name != "cool" && name != "custom" {
		return "", fmt.Errorf("theme: %q is not a preset (%s)", name, themePresets())
	}
	if err := e.SetTheme(ctx, name); err != nil {
		return "", err
	}
	return "theme: " + name, nil
}
