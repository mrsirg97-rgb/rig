package command

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mrsirg97-rgb/rig/v2/core"
)

type effortCmd struct {
	subs func() []Sub
}

func (e effortCmd) Sub() []Sub {
	if e.subs == nil {
		return nil
	}
	return e.subs()
}

func EffortHints(cmds []core.Command, e *Env) {
	if e == nil || e.Efforts == nil {
		return
	}
	for _, c := range cmds {
		m, ok := c.(*effortCmd)
		if !ok {
			continue
		}
		m.subs = func() []Sub {
			var out []Sub
			for i, lv := range e.Efforts() {
				out = append(out, Sub{Name: lv, Desc: effortDesc(i)})
			}
			return out
		}
	}
}

func effortDesc(i int) string {
	descs := []string{"the quick pass", "the deliberate pass", "the deep pass"}
	if i >= len(descs) {
		i = len(descs) - 1
	}
	return descs[i]
}

func (effortCmd) Name() string { return "effort" }

func (effortCmd) Description() string {
	return "the reasoning budget: bare shows the level and the choices, /effort <level> sets it for the next turn"
}

func CheckEffort(levels []string, level, model string) error {
	if len(levels) == 0 {
		return fmt.Errorf("effort: %s names no levels (models.json: \"efforts\")", model)
	}
	if !contains(levels, level) {
		return fmt.Errorf("effort: %q is not a level for %s (available: %s)", level, model, strings.Join(levels, ", "))
	}
	return nil
}

func (effortCmd) Run(ctx context.Context, args string, env any) (string, error) {
	e, err := EnvOf(env)
	if err != nil {
		return "", err
	}
	if e.Efforts == nil {
		return "", errors.New("effort: no effort seam (the root did not wire one)")
	}
	fields := strings.Fields(args)
	if len(fields) > 1 {
		return "", errors.New("effort: usage: effort [<level>]")
	}
	levels := e.Efforts()
	if len(fields) == 1 {
		if e.ActiveModel == nil {
			return "", errors.New("effort: no active-model seam (the root did not wire one)")
		}
		if err := CheckEffort(levels, fields[0], e.ActiveModel()); err != nil {
			return "", err
		}
		if e.SetEffort == nil {
			return "", errors.New("effort: no set seam (the root did not wire one)")
		}
		e.SetEffort(ctx, fields[0])
		return "effort: " + fields[0] + " (next turn)", nil
	}
	if e.Effort == nil {
		return "", errors.New("effort: no effort seam (the root did not wire one)")
	}
	label := e.Effort()
	if label == "" {
		label = "server default"
	}
	if len(levels) == 0 {
		return "effort: " + label, nil
	}
	return choices(plural(len(levels), "level"), label, levels, nil), nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
