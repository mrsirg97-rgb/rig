package command

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/models"
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
		note, err := e.SwitchModel(ctx, fields[0])
		if err != nil {
			return "", err
		}
		reply := "models: active is now " + fields[0]
		if note != "" {
			reply += "\n" + note
		}
		return reply, nil
	}
	if e.Models == nil || e.ActiveModel == nil {
		return "", errors.New("models: no models seam (the root did not wire one)")
	}
	return renderTable(e.Models(), e.ActiveModel()), nil
}

func renderTable(t models.Table, active string) string {
	ids := t.Known()
	wID := 0
	for _, id := range ids {
		if len(id) > wID {
			wID = len(id)
		}
	}
	var b strings.Builder
	b.WriteString(plural(len(ids), "model"))
	if active != "" {
		b.WriteString(" \u00b7 active " + active)
	}
	for _, id := range ids {
		m, _ := t.Get(id)
		mark := markIdle
		if id == active {
			mark = markActive
		}
		where := ""
		if m.Remote {
			where = "remote"
			if m.Provider != "" {
				where += " " + m.Provider
			}
			if m.BaseURL != "" {
				where += " " + m.BaseURL
			}
		}
		b.WriteString("\n" + row(id, wID, mark, m.Role,
			fmt.Sprintf("window %d", m.Window), fmt.Sprintf("max %d", m.MaxTokens), fmt.Sprintf("reserve %d", m.Reserve),
			fmt.Sprintf("keep %d", m.KeepRecent), fmt.Sprintf("trigger %d", m.Window-m.Reserve), where))
	}
	return b.String()
}
