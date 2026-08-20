package command

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mrsirg97-rgb/rig/core"
)

// effortCmd is the /effort dial (SPEC_MODES 1): the row's vocabulary,
// not a global scale — bare shows the active level and the model's
// available ones, an argument sets it (effective next turn). A row
// without efforts is a dial that is off for that row.
type effortCmd struct {
	subs func() []Sub
}

func (e effortCmd) Sub() []Sub {
	if e.subs == nil {
		return nil
	}
	return e.subs()
}

// EffortHints wires the effort command's Sub() hints from the Env, the
// way ModelHints wires /models: the active row's levels, in the row's
// order, each described with a generic one-liner (the words are the
// model's; the descriptions are positional).
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

// effortDesc is the generic one-liner for the i-th level: the words are
// the model's, so the descriptions are positional, not semantic.
func effortDesc(i int) string {
	descs := []string{"the quick pass", "the deliberate pass", "the deep pass"}
	if i >= len(descs) {
		i = len(descs) - 1
	}
	return descs[i]
}

func (effortCmd) Name() string { return "effort" }

func (effortCmd) Description() string {
	return "the model's reasoning budget: show or set the active effort (effective next turn)"
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
		if len(levels) == 0 {
			return "", fmt.Errorf("effort: %s names no levels (models.json: \"efforts\")", e.ActiveModel())
		}
		if !contains(levels, fields[0]) {
			return "", fmt.Errorf("effort: %q is not a level for %s (available: %s)", fields[0], e.ActiveModel(), strings.Join(levels, ", "))
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
	return fmt.Sprintf("effort: %s (available: %s)", label, strings.Join(levels, ", ")), nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
