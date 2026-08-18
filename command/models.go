package command

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/models"
)

// models lists the runtime table and switches the active model for the
// next turn (decision 6): the switch rebuilds the provider+policy pair
// at the root — the loop reads k.Provider / k.Policy fresh at each turn
// start, so the effect is the next turn's request.
type modelsCmd struct {
	// subs is the TUI's hint source (SPEC_TUI 9, amended): the
	// table's known ids with their roles, read at call time. nil = no
	// hints (a set that never wired them).
	subs func() []Sub
}

// Sub is the TUI's argument hints (SPEC_TUI 9, amended): the known
// model names from the Env, their roles the descriptions.
func (m modelsCmd) Sub() []Sub {
	if m.subs == nil {
		return nil
	}
	return m.subs()
}

// ModelHints wires the models command's Sub() (SPEC_TUI 9, amended):
// the table's known ids with their roles, read at call time. The TUI's
// door calls it once, over the set it registered.
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

// renderTable is the plain table (decision 6): one line per row, the
// role after the id (SPEC_CONFIG 4), the active row marked, the raw
// token counts (greppable — formatTokens is the event shaping, not the
// table's), plus trigger = Window - Reserve, the boundary the operator
// watches. The listing order is stable: sorted by id (the table's
// Known() order), so the golden lines do not depend on merge order.
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
		// the +2 column width is the separator (the spec's sample: the
		// role column's own padding is the gap before the numbers).
		fmt.Fprintf(&b, "%-*s%-*swindow %d  max %d  reserve %d  keep %d  trigger %d%s\n",
			wID+2, id, wRole+2, m.Role, m.Window, m.MaxTokens, m.Reserve, m.KeepRecent, m.Window-m.Reserve, mark)
	}
	return b.String()
}
