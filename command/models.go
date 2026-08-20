package command

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/models"
)

type modelsCmd struct {
	subs func() []Sub
}

func (m modelsCmd) Sub() []Sub {
	if m.subs == nil {
		return nil
	}
	return m.subs()
}

func ModelHints(cmds []core.Command, e *Env) {
	if e == nil || e.Models == nil {
		return
	}
	for _, c := range cmds {
		m, ok := c.(*modelsCmd)
		if !ok {
			continue
		}
		m.subs = func() []Sub {
			t := e.Models()
			var out []Sub
			for _, id := range t.Known() {
				row, _ := t.Get(id)
				out = append(out, Sub{Name: id, Desc: row.Role})
			}
			return out
		}
	}
}

func (modelsCmd) Name() string { return "models" }

func (modelsCmd) Description() string {
	return "list the model table, or switch the active model (effective next turn)"
}

func (modelsCmd) Run(ctx context.Context, args string, env any) (string, error) {
	e, err := EnvOf(env)
	if err != nil {
		return "", err
	}
	fields := strings.Fields(args)
	if len(fields) > 1 {
		return "", errors.New("models: usage: models [<id>]")
	}
	if len(fields) == 1 {
		if e.SwitchModel == nil {
			return "", errors.New("models: no switch seam (the root did not wire one)")
		}
		if err := e.SwitchModel(ctx, fields[0]); err != nil {
			return "", err
		}
		return "models: active is now " + fields[0], nil
	}
	if e.Models == nil || e.ActiveModel == nil {
		return "", errors.New("models: no models seam (the root did not wire one)")
	}
	return renderTable(e.Models(), e.ActiveModel()), nil
}

func renderTable(t models.Table, active string) string {
	ids := t.Known()
	wID, wRole := 0, 0
	for _, id := range ids {
		m, _ := t.Get(id)
		if len(id) > wID {
			wID = len(id)
		}
		if len(m.Role) > wRole {
			wRole = len(m.Role)
		}
	}
	var b strings.Builder
	for _, id := range ids {
		m, _ := t.Get(id)
		mark := ""
		if id == active {
			mark = "  *"
		}
		fmt.Fprintf(&b, "%-*s%-*swindow %d  max %d  reserve %d  keep %d  trigger %d%s\n",
			wID+2, id, wRole+2, m.Role, m.Window, m.MaxTokens, m.Reserve, m.KeepRecent, m.Window-m.Reserve, mark)
	}
	return b.String()
}
