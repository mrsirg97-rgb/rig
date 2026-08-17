package command

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mrsirg97-rgb/rig/models"
)

// models lists the runtime table and switches the active model for the
// next turn (decision 6): the switch rebuilds the provider+policy pair
// at the root — the loop reads k.Provider / k.Policy fresh at each turn
// start, so the effect is the next turn's request.
type modelsCmd struct{}

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
// active row marked, the raw token counts (greppable — formatTokens is
// the event shaping, not the table's), plus trigger = Window - Reserve,
// the boundary the operator watches.
func renderTable(t models.Table, active string) string {
	ids := t.Known()
	w := 0
	for _, id := range ids {
		if len(id) > w {
			w = len(id)
		}
	}
	var b strings.Builder
	for _, id := range ids {
		m, _ := t.Get(id)
		mark := ""
		if id == active {
			mark = "  *"
		}
		fmt.Fprintf(&b, "%-*s window %d  max %d  reserve %d  keep %d  trigger %d%s\n",
			w+1, id, m.Window, m.MaxTokens, m.Reserve, m.KeepRecent, m.Window-m.Reserve, mark)
	}
	return b.String()
}
